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
