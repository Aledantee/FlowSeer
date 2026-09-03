package bench

import (
	"fmt"
	"net"
	"sort"
	"testing"
)

// responder_test.go is a minimal, dependency-free in-process SNMP v2c
// agent used as the shared wire peer for the Tier 1 micro-benchmarks. It
// hand-rolls just enough BER to answer Get / GetNext / GetBulk against a
// fixed canned MIB, so the FlowSeer client and the gosnmp client are
// benchmarked against the identical responder and each pays its own real
// per-operation cost (codec + reactor/loop + socket).
//
// It is intentionally NOT the snmp package's production codec (that is
// unexported); a small independent encoder here keeps the bench module
// standalone and ensures the responder is not co-tuned with the client
// under test.

// scalarOID is sysDescr.0 — the target of the Get / cold-start benchmarks.
var scalarOID = []uint32{1, 3, 6, 1, 2, 1, 1, 1, 0}

// mibEntry is one canned-MIB row. vb holds the fully pre-encoded varbind
// (SEQUENCE of OID + value TLV), built once in buildMIB so the timed
// response path does no per-request OID/value encoding — keeping the
// responder's allocations out of the benchmark's process-global -benchmem
// accounting.
type mibEntry struct {
	oid []uint32
	vb  []byte // complete varbind SEQUENCE (oid TLV + value TLV)
}

// buildMIB returns a lexicographically sorted MIB: a sysDescr scalar plus
// an ifTable-shaped subtree of rows columns ifDescr (OCTET STRING) and
// ifInOctets (Counter32), giving a walkable subtree for the BulkWalk
// benchmark.
func buildMIB(rows int) []mibEntry {
	add := func(es []mibEntry, oid []uint32, val []byte) []mibEntry {
		return append(es, mibEntry{oid: oid, vb: varbind(oid, val)})
	}
	var es []mibEntry
	es = add(es, scalarOID, octetTLV("FlowSeer bench responder"))
	for r := 1; r <= rows; r++ {
		ifDescr := []uint32{1, 3, 6, 1, 2, 1, 2, 2, 1, 2, uint32(r)}
		ifInOctets := []uint32{1, 3, 6, 1, 2, 1, 2, 2, 1, 10, uint32(r)}
		es = add(es, ifDescr, octetTLV(fmt.Sprintf("eth%d", r)))
		es = add(es, ifInOctets, counter32TLV(uint32(r)*1000))
	}
	// A sentinel scalar AFTER the ifTable subtree (ip.ipForwarding.0).
	// Walking ifTableRoot then terminates by subtree-exit — the
	// real-agent case — rather than by endOfMibView, so both the
	// FlowSeer and gosnmp walkers stop identically without the
	// terminal pseudo-row.
	es = add(es, []uint32{1, 3, 6, 1, 2, 1, 4, 1, 0}, intTLV(1))
	sort.Slice(es, func(i, j int) bool { return compareOID(es[i].oid, es[j].oid) < 0 })
	return es
}

func encLen(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte(n)}, b...)
		n >>= 8
	}
	return append([]byte{byte(0x80 | len(b))}, b...)
}

func tlv(tag byte, content []byte) []byte {
	out := append([]byte{tag}, encLen(len(content))...)
	return append(out, content...)
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func intTLV(v int) []byte {
	if v == 0 {
		return tlv(0x02, []byte{0})
	}
	var b []byte
	n := v
	for n > 0 {
		b = append([]byte{byte(n)}, b...)
		n >>= 8
	}
	if b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return tlv(0x02, b)
}

func counter32TLV(v uint32) []byte {
	var b []byte
	if v == 0 {
		b = []byte{0}
	} else {
		for v > 0 {
			b = append([]byte{byte(v)}, b...)
			v >>= 8
		}
		if b[0]&0x80 != 0 {
			b = append([]byte{0}, b...)
		}
	}
	return tlv(0x41, b)
}

func octetTLV(s string) []byte { return tlv(0x04, []byte(s)) }

func base128(v uint32) []byte {
	if v == 0 {
		return []byte{0}
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte(v & 0x7f)}, b...)
		v >>= 7
	}
	for i := 0; i < len(b)-1; i++ {
		b[i] |= 0x80
	}
	return b
}

func oidTLV(oid []uint32) []byte {
	body := []byte{byte(40*oid[0] + oid[1])}
	for _, a := range oid[2:] {
		body = append(body, base128(a)...)
	}
	return tlv(0x06, body)
}

// endOfMibView is the v2c [2] IMPLICIT NULL exception value TLV.
var endOfMibView = []byte{0x82, 0x00}

// noSuchObject is the v2c [0] IMPLICIT NULL exception value TLV.
var noSuchObject = []byte{0x80, 0x00}

func parseTLV(b []byte) (tag byte, content, rest []byte, ok bool) {
	if len(b) < 2 {
		return 0, nil, nil, false
	}
	tag = b[0]
	l := int(b[1])
	i := 2
	if l&0x80 != 0 {
		nb := l & 0x7f
		if nb == 0 || 2+nb > len(b) {
			return 0, nil, nil, false
		}
		l = 0
		for j := 0; j < nb; j++ {
			l = l<<8 | int(b[2+j])
		}
		i = 2 + nb
	}
	if i+l > len(b) {
		return 0, nil, nil, false
	}
	return tag, b[i : i+l], b[i+l:], true
}

func decodeUint(b []byte) int {
	n := 0
	for _, c := range b {
		n = n<<8 | int(c)
	}
	return n
}

func parseOID(content []byte) []uint32 {
	if len(content) == 0 {
		return nil
	}
	oid := []uint32{uint32(content[0]) / 40, uint32(content[0]) % 40}
	var v uint32
	for _, c := range content[1:] {
		v = v<<7 | uint32(c&0x7f)
		if c&0x80 == 0 {
			oid = append(oid, v)
			v = 0
		}
	}
	return oid
}

func compareOID(a, b []uint32) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// handle decodes one request datagram and returns the response datagram,
// or nil if the packet is unparseable (the responder drops it silently).
func handle(mib []mibEntry, pkt []byte) []byte {
	tag, seq, _, ok := parseTLV(pkt)
	if !ok || tag != 0x30 {
		return nil
	}
	_, _, afterVer, ok := parseTLV(seq) // version
	if !ok {
		return nil
	}
	_, community, afterComm, ok := parseTLV(afterVer) // community
	if !ok {
		return nil
	}
	pduTag, pdu, _, ok := parseTLV(afterComm)
	if !ok {
		return nil
	}
	_, ridBytes, p1, ok := parseTLV(pdu) // request-id
	if !ok {
		return nil
	}
	_, f1, p2, ok := parseTLV(p1) // error-status | non-repeaters
	if !ok {
		return nil
	}
	_, f2, p3, ok := parseTLV(p2) // error-index | max-repetitions
	if !ok {
		return nil
	}
	_, vbl, _, ok := parseTLV(p3) // varbind list
	if !ok {
		return nil
	}

	var reqOIDs [][]uint32
	rest := vbl
	for len(rest) > 0 {
		var vb []byte
		_, vb, rest, ok = parseTLV(rest)
		if !ok {
			break
		}
		ot, oc, _, ok := parseTLV(vb)
		if !ok || ot != 0x06 {
			continue
		}
		reqOIDs = append(reqOIDs, parseOID(oc))
	}

	var vbs [][]byte
	switch pduTag {
	case 0xA0: // GetRequest
		for _, o := range reqOIDs {
			vbs = append(vbs, getExact(mib, o))
		}
	case 0xA1: // GetNextRequest
		for _, o := range reqOIDs {
			vbs = append(vbs, getNext(mib, o))
		}
	case 0xA5: // GetBulkRequest
		nonRep := decodeUint(f1)
		maxRep := decodeUint(f2)
		for i, o := range reqOIDs {
			if i < nonRep {
				vbs = append(vbs, getNext(mib, o))
				continue
			}
			cur := o
			for r := 0; r < maxRep; r++ {
				idx := firstGreater(mib, cur)
				if idx < 0 {
					vbs = append(vbs, varbind(cur, endOfMibView))
					break
				}
				vbs = append(vbs, mib[idx].vb)
				cur = mib[idx].oid
			}
		}
	default:
		return nil
	}

	reqID := decodeUint(ridBytes)
	pduContent := concat(intTLV(reqID), intTLV(0), intTLV(0), tlv(0x30, concat(vbs...)))
	return tlv(0x30, concat(intTLV(1), tlv(0x04, community), tlv(0xA2, pduContent)))
}

func varbind(oid []uint32, valTLV []byte) []byte {
	return tlv(0x30, concat(oidTLV(oid), valTLV))
}

func getExact(mib []mibEntry, o []uint32) []byte {
	for _, e := range mib {
		if compareOID(e.oid, o) == 0 {
			return e.vb
		}
	}
	return varbind(o, noSuchObject)
}

func getNext(mib []mibEntry, o []uint32) []byte {
	if idx := firstGreater(mib, o); idx >= 0 {
		return mib[idx].vb
	}
	return varbind(o, endOfMibView)
}

// firstGreater returns the index of the first MIB entry strictly greater
// than o, or -1 if none (mib is sorted).
func firstGreater(mib []mibEntry, o []uint32) int {
	i := sort.Search(len(mib), func(i int) bool { return compareOID(mib[i].oid, o) > 0 })
	if i < len(mib) {
		return i
	}
	return -1
}

// startResponder launches the loopback responder and returns its
// host:port. It stops when the benchmark ends (b.Cleanup closes the
// socket, which unblocks the read loop).
func startResponder(b testing.TB) string {
	b.Helper()
	mib := buildMIB(benchRows)
	return startResponderWithHandler(b, func(pkt []byte) []byte { return handle(mib, pkt) })
}

func startResponderWithHandler(b testing.TB, respond func([]byte) []byte) string {
	b.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	b.Cleanup(func() {
		_ = conn.Close()
		<-done
	})

	go func() {
		defer close(done)
		buf := make([]byte, 65535)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return // socket closed at cleanup
			}
			// handle retains no sub-slice of buf (parseOID copies, values
			// come from the MIB, community is copied into the response), and
			// WriteToUDP copies before returning, so buf is safe to reuse
			// without a per-datagram copy.
			if resp := respond(buf[:n]); resp != nil {
				_, _ = conn.WriteToUDP(resp, addr)
			}
		}
	}()

	return conn.LocalAddr().String()
}
