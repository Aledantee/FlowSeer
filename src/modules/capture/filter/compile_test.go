package filter_test

import (
	"strings"
	"testing"

	"golang.org/x/net/bpf"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/filter"
)

// --- frame builders -------------------------------------------------------

var (
	dstMAC = [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	srcMAC = [6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
)

func ethHeader(etherType uint16, vlanID *uint16) []byte {
	b := append(append([]byte{}, dstMAC[:]...), srcMAC[:]...)
	if vlanID != nil {
		b = append(b, 0x81, 0x00, byte(*vlanID>>8), byte(*vlanID))
	}
	b = append(b, byte(etherType>>8), byte(etherType))
	return b
}

func ipv4Header(protocol byte, src, dst [4]byte, totalLen uint16) []byte {
	h := make([]byte, 20)
	h[0] = 0x45 // version 4, IHL 5 (20 bytes, no options)
	h[1] = 0    // DSCP/ECN
	h[2], h[3] = byte(totalLen>>8), byte(totalLen)
	h[8] = 64 // TTL
	h[9] = protocol
	copy(h[12:16], src[:])
	copy(h[16:20], dst[:])
	return h
}

func ipv6Header(nextHeader byte, src, dst [16]byte, payloadLen uint16) []byte {
	h := make([]byte, 40)
	h[0] = 0x60 // version 6
	h[4], h[5] = byte(payloadLen>>8), byte(payloadLen)
	h[6] = nextHeader
	h[7] = 64 // hop limit
	copy(h[8:24], src[:])
	copy(h[24:40], dst[:])
	return h
}

func tcpHeader(srcPort, dstPort uint16, flags byte) []byte {
	h := make([]byte, 20)
	h[0], h[1] = byte(srcPort>>8), byte(srcPort)
	h[2], h[3] = byte(dstPort>>8), byte(dstPort)
	h[12] = 5 << 4 // data offset, no options
	h[13] = flags
	return h
}

func udpHeader(srcPort, dstPort, length uint16) []byte {
	h := make([]byte, 8)
	h[0], h[1] = byte(srcPort>>8), byte(srcPort)
	h[2], h[3] = byte(dstPort>>8), byte(dstPort)
	h[4], h[5] = byte(length>>8), byte(length)
	return h
}

func icmpv4Header(typ, code byte) []byte {
	return []byte{typ, code, 0, 0}
}

func frame(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// tcpV4Frame builds an untagged Ethernet/IPv4/TCP frame to dstPort.
func tcpV4Frame(dstPort uint16) []byte {
	tcp := tcpHeader(51000, dstPort, 0x02) // SYN
	ip := ipv4Header(6 /* TCP */, [4]byte{10, 0, 0, 1}, [4]byte{10, 0, 0, 2}, uint16(20+len(tcp)))
	return frame(ethHeader(0x0800, nil), ip, tcp)
}

func udpV4Frame(dstPort uint16) []byte {
	udp := udpHeader(51000, dstPort, uint16(8))
	ip := ipv4Header(17 /* UDP */, [4]byte{10, 0, 0, 1}, [4]byte{10, 0, 0, 2}, uint16(20+len(udp)))
	return frame(ethHeader(0x0800, nil), ip, udp)
}

func arpFrame() []byte {
	return frame(ethHeader(0x0806, nil), make([]byte, 28))
}

func run(t *testing.T, insts []bpf.Instruction, data []byte) int {
	t.Helper()
	vm, err := bpf.NewVM(insts)
	if err != nil {
		t.Fatalf("NewVM: %v", err)
	}
	n, err := vm.Run(data)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return n
}

// --- tests -----------------------------------------------------------------

func TestCompile_AcceptEverything(t *testing.T) {
	for name, f := range map[string]*capturev1.CaptureFilter{
		"nil filter":            nil,
		"filter with no any_of": {},
	} {
		t.Run(name, func(t *testing.T) {
			insts := mustCompile(t, f)
			if n := run(t, insts, tcpV4Frame(22)); n == 0 {
				t.Errorf("empty filter rejected a frame it should accept")
			}
		})
	}
}

func mustCompile(t *testing.T, f *capturev1.CaptureFilter) []bpf.Instruction {
	t.Helper()
	insts, err := filter.Compile(f)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return insts
}

// TestCompile_AcceptsPort22OrARP proves a filter compiles to a cBPF program
// the kernel accepts, and the program accepts exactly the packets the
// filter describes: a filter naming TCP:22 or ARP accepts a TCP segment to
// port 22 and an ARP frame, and rejects a UDP datagram to port 22.
func TestCompile_AcceptsPort22OrARP(t *testing.T) {
	f := &capturev1.CaptureFilter{}
	f.SetAnyOf([]*capturev1.CaptureFilterClause{
		clauseTCPPort22(),
		clauseEtherType(packetv1.EtherType_ETHER_TYPE_ARP),
	})
	insts := mustCompile(t, f)

	if n := run(t, insts, tcpV4Frame(22)); n == 0 {
		t.Errorf("TCP:22 frame rejected, want accept")
	}
	if n := run(t, insts, arpFrame()); n == 0 {
		t.Errorf("ARP frame rejected, want accept")
	}
	if n := run(t, insts, udpV4Frame(22)); n != 0 {
		t.Errorf("UDP:22 frame accepted (%d bytes), want reject", n)
	}
}

func clauseTCPPort22() *capturev1.CaptureFilterClause {
	c := &capturev1.CaptureFilterClause{}
	c.SetIpProtocol(packetv1.IpProtocol_IP_PROTOCOL_TCP)
	port := &packetv1.TransportPortMatch{}
	port.SetExact(22)
	c.SetDstPort(port)
	return c
}

func clauseEtherType(et packetv1.EtherType) *capturev1.CaptureFilterClause {
	c := &capturev1.CaptureFilterClause{}
	c.SetEtherType(et)
	return c
}

func TestCompile_VlanMatch(t *testing.T) {
	c := &capturev1.CaptureFilterClause{}
	vm := &capturev1.VlanMatch{}
	vm.SetVlanId(42)
	c.SetVlan(vm)
	f := &capturev1.CaptureFilter{}
	f.SetAnyOf([]*capturev1.CaptureFilterClause{c})
	insts := mustCompile(t, f)

	vid := uint16(42)
	tagged := frame(ethHeader(0x0800, &vid), ipv4Header(6, [4]byte{1, 1, 1, 1}, [4]byte{2, 2, 2, 2}, 20), tcpHeader(1, 2, 0))
	if n := run(t, insts, tagged); n == 0 {
		t.Errorf("VLAN 42 frame rejected, want accept")
	}

	otherVid := uint16(7)
	wrongTag := frame(ethHeader(0x0800, &otherVid), ipv4Header(6, [4]byte{1, 1, 1, 1}, [4]byte{2, 2, 2, 2}, 20), tcpHeader(1, 2, 0))
	if n := run(t, insts, wrongTag); n != 0 {
		t.Errorf("VLAN 7 frame accepted, want reject (filter wants VLAN 42)")
	}

	if n := run(t, insts, tcpV4Frame(80)); n != 0 {
		t.Errorf("untagged frame accepted, want reject (filter requires a VLAN tag)")
	}
}

func TestCompile_DstPrefixIPv6(t *testing.T) {
	c := &capturev1.CaptureFilterClause{}
	prefix := &addrv1.IpPrefix{}
	v6 := &addrv1.Ipv6Prefix{}
	addr := &addrv1.Ipv6Address{}
	addr.SetOctets([]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	v6.SetAddress(addr)
	v6.SetLength(32) // 2001:db8::/32
	prefix.SetV6(v6)
	c.SetDstPrefix(prefix)
	f := &capturev1.CaptureFilter{}
	f.SetAnyOf([]*capturev1.CaptureFilterClause{c})
	insts := mustCompile(t, f)

	inPrefix := [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	outPrefix := [16]byte{0x20, 0x01, 0x0d, 0xb9, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	src := [16]byte{0xfe, 0x80}

	match := frame(ethHeader(0x86dd, nil), ipv6Header(6, src, inPrefix, 20), tcpHeader(1, 2, 0))
	if n := run(t, insts, match); n == 0 {
		t.Errorf("address inside 2001:db8::/32 rejected, want accept")
	}

	noMatch := frame(ethHeader(0x86dd, nil), ipv6Header(6, src, outPrefix, 20), tcpHeader(1, 2, 0))
	if n := run(t, insts, noMatch); n != 0 {
		t.Errorf("address outside 2001:db8::/32 accepted, want reject")
	}

	v4NoMatch := tcpV4Frame(80)
	if n := run(t, insts, v4NoMatch); n != 0 {
		t.Errorf("IPv4 frame accepted by an IPv6-only prefix filter, want reject")
	}
}

func TestCompile_ManyClausesFallthrough(t *testing.T) {
	const n = 32
	clauses := make([]*capturev1.CaptureFilterClause, n)
	for i := range n {
		// Every clause but the last names a port nothing in this test uses;
		// the last one is the only one that can ever match, proving the
		// OR-chain reaches the final clause rather than stopping early.
		port := &packetv1.TransportPortMatch{}
		port.SetExact(uint32(10000 + i))
		c := &capturev1.CaptureFilterClause{}
		c.SetDstPort(port)
		clauses[i] = c
	}
	f := &capturev1.CaptureFilter{}
	f.SetAnyOf(clauses)
	insts := mustCompile(t, f)

	if n := run(t, insts, tcpV4Frame(10031)); n == 0 {
		t.Errorf("frame matching only the last of 32 clauses was rejected")
	}
	if n := run(t, insts, tcpV4Frame(9999)); n != 0 {
		t.Errorf("frame matching no clause was accepted")
	}
}

func TestCompile_InstructionCeiling(t *testing.T) {
	// A dense filter — many clauses, each with several fields that expand
	// under the tagged/untagged and address-family duplication Compile's
	// own doc comment describes — pushes the compiled program over the
	// classic BPF ceiling.
	const n = 32
	clauses := make([]*capturev1.CaptureFilterClause, n)
	for i := range n {
		c := &capturev1.CaptureFilterClause{}
		c.SetIpProtocol(packetv1.IpProtocol_IP_PROTOCOL_TCP)
		port := &packetv1.TransportPortMatch{}
		port.SetExact(uint32(1000 + i))
		c.SetDstPort(port)
		c.SetSrcPort(port)
		flags := &packetv1.TcpFlagsMatch{}
		flags.SetRequiredSet([]packetv1.TcpFlag{packetv1.TcpFlag_TCP_FLAG_SYN, packetv1.TcpFlag_TCP_FLAG_ACK})
		flags.SetRequiredClear([]packetv1.TcpFlag{packetv1.TcpFlag_TCP_FLAG_RST})
		c.SetTcpFlags(flags)
		icmp := &packetv1.IcmpMatch{}
		v4 := &packetv1.Icmpv4Match{}
		v4.SetTypes([]uint32{0, 3, 8, 11})
		v4.SetCodes([]uint32{0, 1, 2, 3})
		icmp.SetV4(v4)
		c.SetIcmp(icmp)
		clauses[i] = c
	}
	f := &capturev1.CaptureFilter{}
	f.SetAnyOf(clauses)

	_, err := filter.Compile(f)
	if err == nil {
		t.Fatalf("Compile: want an instruction-ceiling error, got nil")
	}
	if !strings.Contains(err.Error(), "classic BPF ceiling") {
		t.Errorf("Compile error = %q, want it to name the classic BPF ceiling", err.Error())
	}
}

func TestCompile_ClauseWithNoField(t *testing.T) {
	f := &capturev1.CaptureFilter{}
	f.SetAnyOf([]*capturev1.CaptureFilterClause{{}})
	if _, err := filter.Compile(f); err == nil {
		t.Fatalf("Compile: want an error for a clause with no populated field, got nil")
	}
}

func TestCompile_Icmpv4TypeCode(t *testing.T) {
	c := &capturev1.CaptureFilterClause{}
	icmp := &packetv1.IcmpMatch{}
	v4 := &packetv1.Icmpv4Match{}
	v4.SetTypes([]uint32{8}) // echo request
	icmp.SetV4(v4)
	c.SetIcmp(icmp)
	f := &capturev1.CaptureFilter{}
	f.SetAnyOf([]*capturev1.CaptureFilterClause{c})
	insts := mustCompile(t, f)

	echoReq := icmpv4Header(8, 0)
	ip := ipv4Header(1 /* ICMP */, [4]byte{1, 1, 1, 1}, [4]byte{2, 2, 2, 2}, uint16(20+len(echoReq)))
	match := frame(ethHeader(0x0800, nil), ip, echoReq)
	if n := run(t, insts, match); n == 0 {
		t.Errorf("ICMP echo request rejected, want accept")
	}

	echoReply := icmpv4Header(0, 0)
	ip2 := ipv4Header(1, [4]byte{1, 1, 1, 1}, [4]byte{2, 2, 2, 2}, uint16(20+len(echoReply)))
	noMatch := frame(ethHeader(0x0800, nil), ip2, echoReply)
	if n := run(t, insts, noMatch); n != 0 {
		t.Errorf("ICMP echo reply accepted by a type-8-only filter, want reject")
	}
}

func TestCompile_PortRange(t *testing.T) {
	c := &capturev1.CaptureFilterClause{}
	port := &packetv1.TransportPortMatch{}
	r := &packetv1.TransportPortRange{}
	r.SetStart(8000)
	r.SetEnd(8010)
	port.SetRange(r)
	c.SetDstPort(port)
	f := &capturev1.CaptureFilter{}
	f.SetAnyOf([]*capturev1.CaptureFilterClause{c})
	insts := mustCompile(t, f)

	if n := run(t, insts, tcpV4Frame(8005)); n == 0 {
		t.Errorf("port 8005 rejected by an 8000-8010 range filter, want accept")
	}
	if n := run(t, insts, tcpV4Frame(8011)); n != 0 {
		t.Errorf("port 8011 accepted by an 8000-8010 range filter, want reject")
	}
}
