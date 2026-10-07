package routing

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/ndp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

// The conservation property below is the one this package owes its callers: a frame that enters
// a neighbor's hold queue leaves it exactly once, and DrainExits says how. It is asserted as a property
// rather than case by case because the three ways out were each fixed separately and each fix
// opened the next hole — a released frame, a timed-out frame and an evicted frame are one
// mechanism, and only a rule over all of them notices a fourth way out being added.
//
// Two accounting rules the assertions cannot infer:
//
//   - Entered means observed in an entry's queue or evicted list after an operation, not "a call
//     was made". A frame Originate or Route refuses before it queues was never in it. The evicted
//     list counts because appendHeld moves a frame there inside the same call that queues it, so
//     an evicted frame is never observable in queue and would otherwise look like an exit for a
//     frame that never entered.
type conservationOp struct {
	// kind is queue, queue-oversize, queue-route, queue-route-badheader, resolve, fail-held, wake,
	// or timeout.
	kind string
	// neighbor indexes conservationAddrs or conservationAddrsV6, depending on kind; ignored by
	// the ops that take no neighbor.
	neighbor int
}

func TestOriginateScopeMatrix(t *testing.T) {
	t.Parallel()
	destinations := []string{"fe80::99", "2001:db8:9::99", "169.254.9.99", "127.0.0.1", "192.0.2.99", "224.0.0.2", "224.0.1.1", "239.1.2.3"}
	for scope := range 16 {
		destinations = append(destinations, fmt.Sprintf("ff0%x::2", scope))
	}
	// RFC 6724 section 3.1 maps link-local unicast to multicast scope 2 and
	// global unicast to scope 14. Section 3.2 assigns IPv4 link-local scope
	// to 169.254/16 and 127/8, and global scope to all other IPv4 addresses.
	// The layer departs from section 3.2 by treating 224.0.0.0/24 as
	// link-local and 127/8 as global. It also accepts IPv6 multicast scope 0.
	scope := func(a netip.Addr) int {
		if a.Is6() && a.IsMulticast() {
			return int(a.As16()[1] & 15)
		}
		if a.Is4() && netip.MustParsePrefix("224.0.0.0/24").Contains(a) {
			return 2
		}
		if netip.MustParsePrefix("fe80::/10").Contains(a) || netip.MustParsePrefix("169.254.0.0/16").Contains(a) {
			return 2
		}
		return 14
	}
	count := 0
	for _, destination := range destinations {
		for mask := range 16 {
			for _, gateway := range []string{"", "local", "global"} {
				count++
				t.Run(fmt.Sprintf("%s/%04b/%s", destination, mask, gateway), func(t *testing.T) {
					dst := netip.MustParseAddr(destination)
					addresses := []string{"fe80::1/64", "2001:db8:1::1/64", "fe80::2/64", "2001:db8:2::1/64"}
					localHop, globalHop := "fe80::99", "2001:db8:1::99"
					if dst.Is4() {
						addresses = []string{"169.254.1.1/24", "198.51.100.1/24", "169.254.2.1/24", "203.0.113.1/24"}
						localHop, globalHop = "169.254.1.99", "198.51.100.99"
					}
					other := []string{"169.254.3.1/24", "198.18.1.1/24", "169.254.4.1/24", "198.18.2.1/24"}
					if dst.Is4() {
						other = []string{"fe80::3/64", "2001:db8:3::1/64", "fe80::4/64", "2001:db8:4::1/64"}
					}
					interfaces := map[string]Interface{
						"vlan10": {VLAN: 10, MAC: conservationDeviceMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix(other[0]), netip.MustParsePrefix(other[1])}},
						"a0":     {VLAN: 20, Prefixes: []netip.Prefix{netip.MustParsePrefix(other[2]), netip.MustParsePrefix(other[3])}},
					}
					var all, egress []netip.Addr
					for i, address := range addresses {
						if mask&(1<<i) == 0 {
							continue
						}
						name := "vlan10"
						if i >= 2 {
							name = "a0"
						}
						iface := interfaces[name]
						p := netip.MustParsePrefix(address)
						iface.Prefixes = append(iface.Prefixes, p)
						interfaces[name] = iface
						all = append(all, p.Addr())
						if name == "vlan10" {
							egress = append(egress, p.Addr())
						}
					}
					hop := dst
					route := Route{Prefix: netip.PrefixFrom(dst, dst.BitLen()), Interface: "vlan10"}
					if gateway != "" {
						hop = netip.MustParseAddr(globalHop)
						if gateway == "local" {
							hop = netip.MustParseAddr(localHop)
						}
						route.NextHop = hop
					}
					l, err := New(Config{VRFs: map[string]VRF{DefaultVRF: {Interfaces: interfaces, Routes: []Route{route}}}}, layer.Env{})
					if err != nil {
						t.Fatal(err)
					}
					// Isolate source selection even when the egress has no address
					// from which it could solicit this neighbor.
					l.vrfs[DefaultVRF].neighbors[neighborKey{iface: "vlan10", addr: hop}] = &neighborEntry{origin: configured, state: NeighborReachable, mac: conservationSenderMAC}
					res := l.Originate(time.Time{}, DefaultVRF, dst, 17, nil, false)
					if len(all) == 0 {
						if res.Reason != ReasonNoRoute {
							t.Fatalf("without sources: %+v", res)
						}
						return
					}
					hdr, _, err := ip.Decode(res.Frame.Payload)
					if err != nil {
						t.Fatalf("result %+v: %v", res, err)
					}
					wide := scope(dst) > 2
					eligible := func(a netip.Addr) bool {
						if wide && slices.ContainsFunc(all, func(a netip.Addr) bool { return scope(a) > 2 }) {
							return scope(a) > 2
						}
						if !wide && slices.ContainsFunc(egress, func(a netip.Addr) bool { return scope(a) == 2 }) {
							return scope(a) == 2
						}
						return true
					}
					if !slices.Contains(all, hdr.Src) || hdr.Src.Is4() != dst.Is4() || !eligible(hdr.Src) {
						t.Fatalf("source %s violates destination scope %d, VRF %v, egress %v", hdr.Src, scope(dst), all, egress)
					}
					if slices.ContainsFunc(egress, eligible) && !slices.Contains(egress, hdr.Src) {
						t.Fatalf("source %s bypasses eligible egress %v", hdr.Src, egress)
					}
				})
			}
		}
	}
	if count != 1152 {
		t.Fatalf("ran %d cases, want 1152", count)
	}
}

func TestNeighborEntryPointsMatchPriorAdvance(t *testing.T) {
	t.Parallel()
	count := 0
	for _, dst := range []netip.Addr{conservationAddrs[0], conservationAddrsV6[0]} {
		for _, scenario := range []struct {
			name  string
			state NeighborState
			sent  int
		}{
			{"incomplete-exhausted", NeighborIncomplete, 3},
			{"incomplete-due", NeighborIncomplete, 1},
			{"probe-exhausted", NeighborProbe, 3},
			{"probe-due", NeighborProbe, 1},
			{"delay-expired", NeighborDelay, 0},
			{"reachable-expired", NeighborReachable, 0},
			{"stale", NeighborStale, 0},
		} {
			entries := []string{"route", "route-read", "originate", "originate-read"}
			if dst.Is4() {
				entries = append(entries, "arp-local", "arp-other", "arp-reply")
			} else {
				entries = append(entries, "solicitation", "solicitation-no-option", "advertisement", "advertisement-override")
			}
			for _, point := range entries {
				count++
				t.Run(fmt.Sprintf("%s/%s/%s", dst, scenario.name, point), func(t *testing.T) {
					unaged := conservationLayer(t, 1)
					t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
					unaged.Originate(t0, DefaultVRF, dst, 17, []byte{1}, true)
					queued := unaged.Originate(t0, DefaultVRF, dst, 17, []byte{2}, true)
					held := unaged.vrfs[DefaultVRF].neighbors[neighborKey{iface: "vlan10", addr: dst}].queue[0]
					if scenario.state == NeighborIncomplete {
						for sent := 1; sent < scenario.sent; sent++ {
							unaged.Advance(t0.Add(time.Duration(sent) * time.Second))
						}
					} else {
						unaged.observe(t0, advertisement{Interface: "vlan10", Addr: dst, MAC: conservationSenderMAC, HasMAC: true, Override: true, Solicited: scenario.state == NeighborReachable})
						unaged.Advance(t0)
						unaged.DrainExits()
						if scenario.state == NeighborDelay || scenario.state == NeighborProbe {
							unaged.Originate(t0, DefaultVRF, dst, 17, nil, true)
						}
						if scenario.state == NeighborProbe {
							for sent := 1; sent <= scenario.sent; sent++ {
								unaged.Advance(t0.Add(time.Duration(4+sent) * time.Second))
							}
						}
					}
					entry := unaged.vrfs[DefaultVRF].neighbors[neighborKey{iface: "vlan10", addr: dst}]
					if entry.state != scenario.state || entry.origin != observed {
						t.Fatalf("seed = %+v, want observed %s", entry, scenario.state)
					}
					if scenario.state == NeighborProbe && scenario.sent == 3 {
						// Probe normally has no queue. A held frame exercises the
						// timeout exit contract in both exhausted states.
						entry.queue = []heldEntry{held}
					}
					now := t0.Add(40 * time.Second)
					aged := unaged.Clone()
					aged.Advance(now)
					agedExits := aged.DrainExits()
					if scenario.sent == 3 {
						if _, exists := aged.vrfs[DefaultVRF].neighbors[neighborKey{iface: "vlan10", addr: dst}]; exists {
							t.Fatal("Advance retained exhausted neighbor")
						}
						if !slices.ContainsFunc(agedExits, func(exit HeldFrame) bool {
							return exit.Cause == HeldTimedOut && exit.Token == queued.Hold
						}) {
							t.Fatalf("Advance exits %v omit timed-out token %d", agedExits, queued.Hold)
						}
					}
					before := unaged.Clone()
					beforeAged := aged.Clone()
					invoke := func(l *Layer) (Result, layer.Effects) {
						commit := point != "route-read" && point != "originate-read"
						if point == "originate" || point == "originate-read" {
							return l.Originate(now, DefaultVRF, dst, 17, []byte{3}, commit), layer.Effects{}
						}
						hdr := ip.Header{Src: dst, Dst: netip.MustParseAddr("10.0.10.1"), HopLimit: 255, Protocol: 58, V4: &ip.V4{}}
						etherType := ethernet.EtherTypeIPv4
						if dst.Is6() {
							hdr.Dst, hdr.V4, hdr.V6 = netip.MustParseAddr("2001:db8:10::1"), nil, &ip.V6{}
							etherType = ethernet.EtherTypeIPv6
						}
						var body []byte
						var err error
						switch {
						case point == "route" || point == "route-read":
							hdr.Src, hdr.Dst, hdr.Protocol, hdr.HopLimit = hdr.Dst, dst, 17, 64
							body = []byte{3}
						case dst.Is4():
							msg := arp.Message{HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Request, SenderAddr: dst, SenderMAC: conservationSenderMAC, TargetAddr: hdr.Dst, TargetMAC: conservationDeviceMAC}
							if point == "arp-other" {
								msg.TargetAddr = netip.MustParseAddr("10.0.10.98")
							}
							if point == "arp-reply" {
								msg.Operation = arp.Reply
							}
							frame, encodeErr := arp.Encode(msg, conservationDeviceMAC)
							if encodeErr != nil {
								t.Fatal(encodeErr)
							}
							return Result{}, l.Receive(now, "vlan10", frame)
						default:
							msg := ndp.Message{Type: ndp.NeighborSolicitation, Target: hdr.Dst, HasLinkLayerAddr: point != "solicitation-no-option", LinkLayerAddr: conservationSenderMAC}
							if point == "advertisement" || point == "advertisement-override" {
								msg.Type, msg.Target, msg.Solicited, msg.Override = ndp.NeighborAdvertisement, dst, point == "advertisement", true
							}
							body, err = ndp.Encode(hdr, msg)
							if err != nil {
								t.Fatal(err)
							}
						}
						packet, err := hdr.Encode(body)
						if err != nil {
							t.Fatal(err)
						}
						frame := ethernet.Frame{Src: conservationSenderMAC, Dst: conservationDeviceMAC, EtherType: etherType, Payload: packet}
						if point == "route" || point == "route-read" {
							return l.Route(now, "vlan10", frame, commit), layer.Effects{}
						}
						return Result{}, l.Receive(now, "vlan10", frame)
					}
					got, gotFX := invoke(unaged)
					want, wantFX := invoke(aged)
					if got.Reason != want.Reason || !reflect.DeepEqual(got.Frame, want.Frame) || !reflect.DeepEqual(got.Emissions, want.Emissions) || (got.Hold != 0) != (want.Hold != 0) || !reflect.DeepEqual(gotFX, wantFX) {
						t.Errorf("results differ: %+v/%+v vs %+v/%+v", got, gotFX, want, wantFX)
					}
					if (point == "route-read" || point == "originate-read") && (!reflect.DeepEqual(unaged, before) || !reflect.DeepEqual(aged, beforeAged)) {
						t.Error("non-committing call changed layer")
					}
					unaged.Advance(now)
					aged.Advance(now)
					agedExits = append(agedExits, aged.DrainExits()...)
					if !reflect.DeepEqual(unaged.Neighbors(), aged.Neighbors()) {
						t.Errorf("neighbors differ: %v vs %v", unaged.Neighbors(), aged.Neighbors())
					}
					gotWake, gotTimer := unaged.NextWake()
					wantWake, wantTimer := aged.NextWake()
					if gotWake != wantWake || gotTimer != wantTimer {
						t.Errorf("wake differs: %v/%t vs %v/%t", gotWake, gotTimer, wantWake, wantTimer)
					}
					causes := func(exits []HeldFrame) map[HoldToken]HeldCause {
						out := map[HoldToken]HeldCause{}
						for _, exit := range exits {
							if _, exists := out[exit.Token]; exists {
								t.Errorf("duplicate exit %d", exit.Token)
							}
							out[exit.Token] = exit.Cause
						}
						return out
					}
					if gotExits := causes(unaged.DrainExits()); !reflect.DeepEqual(gotExits, causes(agedExits)) {
						t.Errorf("exits differ: %v vs %v", gotExits, causes(agedExits))
					}
				})
			}
		}
	}
	if count != 105 {
		t.Fatalf("ran %d cases, want 105", count)
	}
}

var conservationAddrs = []netip.Addr{
	netip.MustParseAddr("10.0.10.11"),
	netip.MustParseAddr("10.0.10.12"),
	netip.MustParseAddr("10.0.10.13"),
}

// conservationAddrsV6 is the destination space for the queue-route-badheader op: each address is
// on-link over the IPv6 prefix conservationLayer configures, so it routes the same way the IPv4
// addresses above do, through an unresolved neighbor.
var conservationAddrsV6 = []netip.Addr{
	netip.MustParseAddr("2001:db8:10::11"),
	netip.MustParseAddr("2001:db8:10::12"),
	netip.MustParseAddr("2001:db8:10::13"),
}

// badIPv6Src is IPv4-mapped (RFC 4291 section 2.5.5.2): ip.Decode accepts it as a Src address,
// but ip.Header.Encode refuses it.
var badIPv6Src = netip.MustParseAddr("::ffff:10.0.0.1")

var (
	conservationDeviceMAC = netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	conservationSenderMAC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
)

func q(n int) conservationOp    { return conservationOp{kind: "queue", neighbor: n} }
func qBig(n int) conservationOp { return conservationOp{kind: "queue-oversize", neighbor: n} }
func qr(n int) conservationOp   { return conservationOp{kind: "queue-route", neighbor: n} }
func qrBad(n int) conservationOp {
	return conservationOp{kind: "queue-route-badheader", neighbor: n}
}
func r(n int) conservationOp { return conservationOp{kind: "resolve", neighbor: n} }

var (
	opFailHeld = conservationOp{kind: "fail-held"}
	opWake     = conservationOp{kind: "wake"}
	opTimeout  = conservationOp{kind: "timeout"}
)

// conservationLayer builds a single-VRF, single-interface layer holding at most depth frames per
// neighbor, with a retransmission timer the driver can step past deliberately. The interface carries
// both an IPv4 and an IPv6 prefix, so it routes queue-route and queue-route-badheader's IPv6
// destinations on-link the same way it already routes the IPv4 ones.
func conservationLayer(t *testing.T, depth int) *Layer {
	t.Helper()
	l, err := New(Config{VRFs: map[string]VRF{
		DefaultVRF: {
			Interfaces: map[string]Interface{
				"vlan10": {
					VLAN: 10,
					MAC:  conservationDeviceMAC,
					Prefixes: []netip.Prefix{
						netip.MustParsePrefix("10.0.10.1/24"),
						netip.MustParsePrefix("2001:db8:10::1/64"),
					},
				},
			},
			NeighborPolicy: NeighborPolicy{RetransTimer: time.Second, HoldDepth: depth},
		},
	}}, layer.Env{NodeID: "sw1"})
	if err != nil {
		t.Fatalf("routing.New: %v", err)
	}
	return l
}

// encodeUnencodableIPv6Packet builds the wire form of an IPv6 header whose Src is badIPv6Src,
// matching decodeV6's layout in src/common/net/ip/ip.go so [ip.Decode] reads it back as that
// header. It cannot use [ip.Header.Encode], which refuses an IPv4-mapped Src, which is the point:
// this builds bytes Decode accepts and Encode does not.
func encodeUnencodableIPv6Packet(dst netip.Addr, payload []byte) []byte {
	b := make([]byte, ip.V6HeaderLen+len(payload))
	binary.BigEndian.PutUint32(b[0:4], uint32(6)<<28)
	binary.BigEndian.PutUint16(b[4:6], uint16(len(payload)))
	b[6] = 17 // UDP
	b[7] = 64 // hop limit
	srcBytes := badIPv6Src.As16()
	copy(b[8:24], srcBytes[:])
	dstBytes := dst.As16()
	copy(b[24:40], dstBytes[:])
	copy(b[ip.V6HeaderLen:], payload)
	return b
}

// TestHoldQueueConservesEveryFrame is the property in this file's header comment, over a fixed
// list of sequences rather than a random source, so a failure reproduces from the sequence index
// alone.
func TestHoldQueueConservesEveryFrame(t *testing.T) {
	t.Parallel()

	sequences := []struct {
		name  string
		depth int
		ops   []conservationOp
	}{
		{"queue then resolve", 3, []conservationOp{q(0), q(0), r(0), opWake}},
		{"queue then time out", 3, []conservationOp{q(0), q(1), opTimeout}},
		{"overflow at depth 1", 1, []conservationOp{q(0), q(0), q(0), r(0), opWake}},
		{"overflow then time out", 1, []conservationOp{q(0), q(0), q(0), opTimeout}},
		{"overflow at depth 2 across neighbors", 2, []conservationOp{q(0), q(1), q(0), q(0), q(1), r(1), opWake, opTimeout}},
		{"fail-held mid-run", 3, []conservationOp{q(0), q(1), opFailHeld, q(2), r(2), opWake}},
		{"fail-held after an overflow", 1, []conservationOp{q(0), q(0), opFailHeld, q(0), r(0), opWake}},
		{"resolve one of three", 2, []conservationOp{q(0), q(1), q(2), r(1), opWake, opTimeout}},
		{"queue after a resolve drains", 2, []conservationOp{q(0), r(0), opWake, q(0), opTimeout}},
		{"queue onto a failed entry", 2, []conservationOp{q(0), opTimeout, q(0), q(0), opWake}},
		{"an unencodable datagram never enters", 3, []conservationOp{q(0), qBig(0), q(0), r(0), opWake}},
		{"an unencodable datagram on a full queue", 1, []conservationOp{qBig(0), q(0), q(0), opTimeout}},
		{"interleaved everything", 3, []conservationOp{q(0), q(1), r(0), q(2), opWake, q(1), q(1), q(1), q(1), opFailHeld, q(2), opTimeout}},
		{"route then originate on the same neighbor", 3, []conservationOp{qr(0), q(0), r(0), opWake}},
		{"queue via route then time out", 2, []conservationOp{qr(0), qr(1), opTimeout}},
		{"an unencodable datagram via route never enters", 3, []conservationOp{qr(0), qrBad(0), qr(0), r(0), opWake}},
	}

	// The subtests run in sequence rather than in parallel: each is a handful of map operations
	// on its own layer, and causesSeen below accumulates across all of them.
	causesSeen := map[HeldCause]int{}
	for i, seq := range sequences {
		t.Run(seq.name, func(t *testing.T) {
			run := runConservation(t, seq.depth, seq.ops)
			for cause, n := range run.causes {
				causesSeen[cause] += n
			}

			slices.Sort(run.entered)
			slices.Sort(run.exited)
			if !slices.Equal(run.entered, run.exited) {
				t.Fatalf("sequence %d: entered %v, exited %v", i, run.entered, run.exited)
			}
			if dup := firstDuplicate(run.exited); dup != 0 {
				t.Fatalf("sequence %d: payload %d exited twice", i, dup)
			}
		})
	}

	// A fix that stamps every exit with one cause satisfies the multiset on its own. The per-exit
	// shape check in runConservation catches most of that; this catches the rest, a vocabulary
	// that collapsed to fewer values than the layer has exits.
	for _, cause := range []HeldCause{HeldReleased, HeldTimedOut, HeldEvicted} {
		if causesSeen[cause] == 0 {
			t.Errorf("no exit carried cause %q across every sequence", cause)
		}
	}
}

// conservationRun is what one sequence produced: the payload markers observed in a hold queue,
// the markers DrainExits and FailHeld reported leaving one, and
// a count per cause.
type conservationRun struct {
	entered []byte
	exited  []byte
	causes  map[HeldCause]int
}

func runConservation(t *testing.T, depth int, ops []conservationOp) conservationRun {
	t.Helper()
	l := conservationLayer(t, depth)
	run := conservationRun{causes: map[HeldCause]int{}}
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	var marker byte

	held := func() []byte {
		var out []byte
		for _, vs := range l.vrfs {
			for _, entry := range vs.neighbors {
				for _, h := range slices.Concat(entry.queue, entry.evicted) {
					out = append(out, h.payload[len(h.payload)-1])
				}
			}
		}
		return out
	}
	observe := func() {
		for _, m := range held() {
			if !slices.Contains(run.entered, m) {
				run.entered = append(run.entered, m)
			}
		}
	}
	drain := func(at time.Time) {
		l.Advance(at)
		for _, hf := range l.DrainExits() {
			run.exited = append(run.exited, hf.Frame.Payload[len(hf.Frame.Payload)-1])
			run.causes[hf.Cause]++
			// A released frame carries a resolved destination; the two failure causes carry
			// none. A uniform stamp fails here on whichever exits it got wrong.
			if resolved := hf.Frame.Dst != (netaddr.MAC{}); resolved != (hf.Cause == HeldReleased) {
				t.Errorf("exit with cause %q has dst %s, which does not match the cause", hf.Cause, hf.Frame.Dst)
			}
		}
	}

	for _, op := range ops {
		now = now.Add(time.Millisecond)
		switch op.kind {
		case "queue":
			marker++
			l.Originate(now, DefaultVRF, conservationAddrs[op.neighbor], 17, []byte{marker}, true)
		case "queue-oversize":
			// 65516 octets under a 20-octet IPv4 header is one past the total-length field, so
			// Originate must refuse it outright. Queued instead, it would be re-encoded at
			// release, fail there, and leave the queue by a path DrainExits reports nothing for.
			marker++
			payload := make([]byte, 65516)
			payload[len(payload)-1] = marker
			l.Originate(now, DefaultVRF, conservationAddrs[op.neighbor], 17, payload, true)
		case "queue-route":
			marker++
			hdr := ip.Header{
				Src:      netip.MustParseAddr("10.0.10.99"),
				Dst:      conservationAddrs[op.neighbor],
				HopLimit: 64,
				Protocol: 17,
				V4:       &ip.V4{},
			}
			b, err := hdr.Encode([]byte{marker})
			if err != nil {
				t.Fatalf("encode route packet: %v", err)
			}
			l.Route(now, "vlan10", ethernet.Frame{
				Src:       conservationSenderMAC,
				Dst:       conservationDeviceMAC,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   b,
			}, true)
		case "queue-route-badheader":
			// ip.Decode accepts an IPv6 header whose Src is IPv4-mapped, but
			// ip.Header.Encode refuses it: queued anyway, it would be
			// re-encoded at release, fail there, and leave the queue by a path DrainExits
			// reports nothing for.
			marker++
			b := encodeUnencodableIPv6Packet(conservationAddrsV6[op.neighbor], []byte{marker})
			l.Route(now, "vlan10", ethernet.Frame{
				Src:       conservationSenderMAC,
				Dst:       conservationDeviceMAC,
				EtherType: ethernet.EtherTypeIPv6,
				Payload:   b,
			}, true)
		case "resolve":
			frame, err := arp.Encode(arp.Message{
				HardwareType: 1, ProtocolType: 0x0800,
				Operation: arp.Reply, SenderAddr: conservationAddrs[op.neighbor],
				SenderMAC:  netaddr.MAC{0x02, 0, 0, 0, 0, byte(op.neighbor + 1)},
				TargetAddr: netip.MustParseAddr("10.0.10.1"), TargetMAC: conservationDeviceMAC,
			}, conservationDeviceMAC)
			if err != nil {
				t.Fatal(err)
			}
			l.Receive(now, "vlan10", frame)
		case "fail-held":
			for _, hf := range l.FailHeld() {
				run.exited = append(run.exited, hf.Frame.Payload[len(hf.Frame.Payload)-1])
				run.causes[hf.Cause]++
			}
		case "wake":
			drain(now)
		case "timeout":
			for range 3 {
				now = now.Add(time.Second)
				drain(now)
			}
		default:
			t.Fatalf("unknown op kind %q", op.kind)
		}
		observe()
	}

	// Drain whatever the sequence left behind, so a frame still sitting in a queue at the end is
	// not mistaken for one that conserved.
	drain(now)
	for range 3 {
		now = now.Add(time.Second)
		drain(now)
	}

	return run
}

// firstDuplicate returns the first value appearing twice in the sorted slice s, or 0.
func firstDuplicate(s []byte) byte {
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			return s[i]
		}
	}
	return 0
}

// TestResolveNeighborStoredZeroStateIsAMiss tests that [Layer.resolveNeighbor] treats a stored
// zero state as a miss rather than silently starting to hold frames for a neighbor nothing ever looked up.
// [NeighborState]'s zero value, [NeighborUnobserved], is meaningful only as a lookup answer, but [neighborEntry]{}
// is a legal Go zero value too. No exported path stores one. This reaches into the package to reproduce it directly.
func TestResolveNeighborStoredZeroStateIsAMiss(t *testing.T) {
	t.Parallel()

	vs := &vrfState{
		neighbors: map[neighborKey]*neighborEntry{},
		policy:    NeighborPolicy{}.normalize(),
	}
	key := neighborKey{iface: "vlan10", addr: netip.MustParseAddr("10.0.10.9")}
	vs.neighbors[key] = &neighborEntry{}

	calls := 0
	lookup := (&Layer{}).resolveNeighbor(vs, time.Now(), key, Interface{}, true, func() heldEntry {
		calls++
		return heldEntry{}
	})

	if lookup.state != NeighborUnobserved {
		t.Errorf("state = %q, want %q", lookup.state, NeighborUnobserved)
	}
	if lookup.ok {
		t.Error("ok = true, want false for a stored zero-state entry")
	}
	if calls != 0 {
		t.Errorf("held() called %d times, want 0: a zero-state entry must not start holding frames", calls)
	}
	if len(vs.neighbors[key].queue) != 0 {
		t.Errorf("queue length = %d, want 0", len(vs.neighbors[key].queue))
	}
}

func TestFailHeldDeletesIncompleteEntry(t *testing.T) {
	l := conservationLayer(t, 3)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	res := l.Originate(now, DefaultVRF, conservationAddrs[0], 17, []byte{1}, true)
	if res.Hold == 0 {
		t.Fatal("frame is not held")
	}
	exits := l.FailHeld()
	if len(exits) != 1 || exits[0].Cause != HeldTimedOut || exits[0].Token != res.Hold {
		t.Fatalf("exits = %+v", exits)
	}
	if len(l.Neighbors()) != 0 {
		t.Fatal("FailHeld retains incomplete entry")
	}
	if _, ok := l.NextWake(); ok {
		t.Fatal("FailHeld retains timer")
	}
}

func TestNeighborLookupMatchesPriorAdvance(t *testing.T) {
	t.Parallel()
	for _, dst := range []netip.Addr{conservationAddrs[0], conservationAddrsV6[0]} {
		for _, scenario := range []struct {
			state     NeighborState
			exhausted bool
		}{
			{NeighborIncomplete, true},
			{NeighborProbe, true},
			{NeighborDelay, false},
			{NeighborIncomplete, false},
			{NeighborProbe, false},
		} {
			state := scenario.state
			for _, commit := range []bool{false, true} {
				for _, originate := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/exhausted=%t/commit=%t/originate=%t", dst, state, scenario.exhausted, commit, originate), func(t *testing.T) {
						unaged := conservationLayer(t, 1)
						t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
						first := unaged.Originate(t0, DefaultVRF, dst, 17, []byte{1}, true)
						second := unaged.Originate(t0, DefaultVRF, dst, 17, []byte{2}, true)
						var third Result
						if state != NeighborIncomplete {
							unaged.observe(t0, advertisement{Interface: "vlan10", Addr: dst, MAC: conservationSenderMAC, HasMAC: true, Override: true})
							unaged.Advance(t0)
							unaged.DrainExits()
							unaged.Originate(t0, DefaultVRF, dst, 17, nil, true)
							if state == NeighborProbe {
								seconds := []int{5}
								if scenario.exhausted {
									seconds = []int{5, 6, 7}
								}
								for _, seconds := range seconds {
									unaged.Advance(t0.Add(time.Duration(seconds) * time.Second))
								}
							}
						} else {
							unaged.Advance(t0.Add(time.Second))
							if scenario.exhausted {
								unaged.Advance(t0.Add(2 * time.Second))
							}
							// Keep an eviction waiting at the instant the exhausted entry fails.
							third = unaged.Originate(t0.Add(2*time.Second), DefaultVRF, dst, 17, []byte{3}, true)
						}
						now := t0.Add(10 * time.Second)
						aged := unaged.Clone()
						aged.Advance(now)
						agedExits := aged.DrainExits()
						before := unaged.Clone()
						lookup := func(l *Layer) Result {
							if originate {
								return l.Originate(now, DefaultVRF, dst, 17, []byte{4}, commit)
							}
							hdr := ip.Header{Src: netip.MustParseAddr("10.0.10.99"), Dst: dst, HopLimit: 64, Protocol: 17, V4: &ip.V4{}}
							etherType := ethernet.EtherTypeIPv4
							if dst.Is6() {
								hdr.Src, hdr.V4, hdr.V6 = netip.MustParseAddr("2001:db8:10::99"), nil, &ip.V6{}
								etherType = ethernet.EtherTypeIPv6
							}
							packet, err := hdr.Encode([]byte{4})
							if err != nil {
								t.Fatal(err)
							}
							return l.Route(now, "vlan10", ethernet.Frame{Src: conservationSenderMAC, Dst: conservationDeviceMAC, EtherType: etherType, Payload: packet}, commit)
						}
						got, want := lookup(unaged), lookup(aged)
						if got.Reason != want.Reason || got.Frame.Dst != want.Frame.Dst || !reflect.DeepEqual(got.Emissions, want.Emissions) || (got.Hold != 0) != (want.Hold != 0) {
							t.Fatalf("unaged result = %+v, aged = %+v", got, want)
						}
						if scenario.exhausted && (got.Reason != ReasonNeighborPending || (commit && (got.Hold == 0 || len(got.Emissions) != 1))) {
							t.Fatalf("exhausted cycle result = %+v, want fresh resolution", got)
						}
						if !commit && !reflect.DeepEqual(unaged, before) {
							t.Fatal("non-committing lookup changed layer")
						}
						unaged.Advance(now)
						aged.Advance(now)
						agedExits = append(agedExits, aged.DrainExits()...)
						if !reflect.DeepEqual(unaged.Neighbors(), aged.Neighbors()) {
							t.Fatalf("neighbors differ: %v vs %v", unaged.Neighbors(), aged.Neighbors())
						}
						gotWake, gotTimer := unaged.NextWake()
						wantWake, wantTimer := aged.NextWake()
						if gotWake != wantWake || gotTimer != wantTimer {
							t.Fatalf("wake differs: %v/%t vs %v/%t", gotWake, gotTimer, wantWake, wantTimer)
						}
						if gotExits := unaged.DrainExits(); !reflect.DeepEqual(gotExits, agedExits) {
							t.Fatalf("exits differ: %+v vs %+v", gotExits, agedExits)
						}
						if state == NeighborIncomplete && scenario.exhausted {
							causes := map[HoldToken]HeldCause{}
							for _, exit := range agedExits {
								causes[exit.Token] = exit.Cause
							}
							if causes[first.Hold] != HeldEvicted || causes[second.Hold] != HeldEvicted {
								t.Fatalf("eviction causes = %v", causes)
							}
							if len(causes) != 3 {
								t.Fatalf("exits = %v, want two evicted and one timed-out", causes)
							}
							if causes[third.Hold] != HeldTimedOut {
								t.Fatalf("timeout missing: %v", causes)
							}
						}
					})
				}
			}
		}
	}
}

func TestNeighborReachableDeadlineLookup(t *testing.T) {
	t.Parallel()
	l := conservationLayer(t, 3)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dst := conservationAddrsV6[0]
	l.Originate(t0, DefaultVRF, dst, 17, nil, true)
	l.observe(t0, advertisement{Interface: "vlan10", Addr: dst, MAC: conservationSenderMAC, HasMAC: true, Solicited: true, Override: true})
	vs := l.vrfs[DefaultVRF]
	key := neighborKey{iface: "vlan10", addr: dst}
	now := t0.Add(vs.policy.ReachableTime)
	got := l.resolveNeighbor(vs, now, key, l.ifaces["vlan10"], true, nil)
	if got.state != NeighborStale || !got.ok || vs.neighbors[key].state != NeighborDelay {
		t.Fatalf("deadline lookup %+v, entry %+v, want stale lookup and Delay entry", got, vs.neighbors[key])
	}
}

func TestNeighborIncompleteRetransmissionsAndTimeoutInterface(t *testing.T) {
	t.Parallel()
	for _, dst := range []netip.Addr{conservationAddrs[0], conservationAddrsV6[0]} {
		t.Run(dst.String(), func(t *testing.T) {
			l := conservationLayer(t, 3)
			t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			first := l.Originate(t0, DefaultVRF, dst, 17, []byte{1}, true)
			if len(first.Emissions) != 1 {
				t.Fatalf("first solicitation: %+v", first)
			}
			for sent := 1; sent < 3; sent++ {
				fx := l.Advance(t0.Add(time.Duration(sent) * time.Second))
				if len(fx.Emissions) != 1 || !reflect.DeepEqual(fx.Emissions[0].Frame, first.Emissions[0].Frame) {
					t.Fatalf("solicitation %d differs from first: %+v", sent+1, fx)
				}
			}
			l.Advance(t0.Add(3 * time.Second))
			exits := l.DrainExits()
			if len(exits) != 1 || exits[0].Cause != HeldTimedOut || exits[0].Token != first.Hold || exits[0].Interface != "vlan10" {
				t.Fatalf("timeout exits %+v, want timed-out token %d on vlan10", exits, first.Hold)
			}
		})
	}
}

func TestNeighborProbeNextWake(t *testing.T) {
	t.Parallel()
	l := conservationLayer(t, 3)
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dst := conservationAddrsV6[0]
	l.Originate(t0, DefaultVRF, dst, 17, nil, true)
	l.observe(t0, advertisement{Interface: "vlan10", Addr: dst, MAC: conservationSenderMAC, HasMAC: true, Override: true})
	l.Advance(t0)
	l.Originate(t0, DefaultVRF, dst, 17, nil, true)
	for second := 5; second < 8; second++ {
		now := t0.Add(time.Duration(second) * time.Second)
		l.Advance(now)
		if l.Neighbors()[0].State != NeighborProbe {
			t.Fatalf("neighbors %v, want Probe", l.Neighbors())
		}
		if wake, ok := l.NextWake(); !ok || wake != now.Add(time.Second) {
			t.Fatalf("probe wake %v/%t, want %v", wake, ok, now.Add(time.Second))
		}
	}
}

func TestValidateAllowsTwoAddressesOnOneInterfaceSubnet(t *testing.T) {
	t.Parallel()
	for _, addresses := range [][]string{{"192.0.2.1/24", "192.0.2.2/24"}, {"2001:db8::1/64", "2001:db8::2/64"}} {
		cfg := Config{VRFs: map[string]VRF{DefaultVRF: {Interfaces: map[string]Interface{"vlan10": {
			VLAN: 10, MAC: conservationDeviceMAC,
			Prefixes: []netip.Prefix{netip.MustParsePrefix(addresses[0]), netip.MustParsePrefix(addresses[1])},
		}}}}}
		if err := cfg.Validate(layer.Env{}); err != nil {
			t.Fatalf("one interface with addresses %v: %v", addresses, err)
		}
	}
}
