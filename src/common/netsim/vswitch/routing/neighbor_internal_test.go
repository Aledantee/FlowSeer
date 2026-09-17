package routing

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// The conservation property below is the one this package owes its callers: a frame that enters
// a neighbor's hold queue leaves it exactly once, and Wake says how. It is asserted as a property
// rather than case by case because the three ways out were each fixed separately and each fix
// opened the next hole — a released frame, a timed-out frame and an evicted frame are one
// mechanism, and only a rule over all of them notices a fourth way out being added.
//
// Two accounting rules the assertions cannot infer:
//
//   - Entered means observed in an entry's queue or evicted list after an operation, not "a call
//     was made". A frame Originate refuses before the queue was never in it. The evicted list
//     counts because appendHeld moves a frame there inside the same call that queues it, so an
//     evicted frame is never observable in queue and would otherwise look like an exit for a
//     frame that never entered.
//   - DiscardHeld removes a frame from the entered multiset rather than producing an exit. A
//     derive boundary discards held frames unconditionally, and the fork that discarded them is a
//     different run from the one that queued them; TestDiscardHeldThenWakePastDeadlineFailsTheEntry
//     pins that decision.
type conservationOp struct {
	// kind is queue, resolve, discard, wake, or timeout.
	kind string
	// neighbor indexes conservationAddrs; ignored by the ops that take no neighbor.
	neighbor int
}

var conservationAddrs = []netip.Addr{
	netip.MustParseAddr("10.0.10.11"),
	netip.MustParseAddr("10.0.10.12"),
	netip.MustParseAddr("10.0.10.13"),
}

func q(n int) conservationOp    { return conservationOp{kind: "queue", neighbor: n} }
func qBig(n int) conservationOp { return conservationOp{kind: "queue-oversize", neighbor: n} }
func r(n int) conservationOp    { return conservationOp{kind: "resolve", neighbor: n} }

var (
	opDiscard = conservationOp{kind: "discard"}
	opWake    = conservationOp{kind: "wake"}
	opTimeout = conservationOp{kind: "timeout"}
)

// conservationLayer builds a single-VRF, single-interface layer holding at most depth frames per
// neighbor, with a resolution timeout the driver can step past deliberately.
func conservationLayer(t *testing.T, depth int) *Layer {
	t.Helper()
	l, err := New(Config{VRFs: map[string]VRF{
		DefaultVRF: {
			Interfaces: map[string]Interface{
				"vlan10": {
					VLAN:     10,
					MAC:      netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
					Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
				},
			},
			NeighborPolicy: NeighborPolicy{ResolutionTimeout: time.Second, HoldDepth: depth},
		},
	}}, port.Table{}, "sw1")
	if err != nil {
		t.Fatalf("routing.New: %v", err)
	}
	return l
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
		{"discard mid-run", 3, []conservationOp{q(0), q(1), opDiscard, q(2), r(2), opWake}},
		{"discard after an overflow", 1, []conservationOp{q(0), q(0), opDiscard, q(0), r(0), opWake}},
		{"resolve one of three", 2, []conservationOp{q(0), q(1), q(2), r(1), opWake, opTimeout}},
		{"queue after a resolve drains", 2, []conservationOp{q(0), r(0), opWake, q(0), opTimeout}},
		{"queue onto a failed entry", 2, []conservationOp{q(0), opTimeout, q(0), q(0), opWake}},
		{"an unencodable datagram never enters", 3, []conservationOp{q(0), qBig(0), q(0), r(0), opWake}},
		{"an unencodable datagram on a full queue", 1, []conservationOp{qBig(0), q(0), q(0), opTimeout}},
		{"interleaved everything", 3, []conservationOp{q(0), q(1), r(0), q(2), opWake, q(1), q(1), q(1), q(1), opDiscard, q(2), opTimeout}},
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
			for _, marker := range run.discarded {
				if slices.Contains(run.exited, marker) {
					t.Errorf("sequence %d: payload %d was discarded but still reported an exit", i, marker)
				}
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
// the markers Wake reported leaving one, the markers DiscardHeld took out of the accounting, and
// a count per cause.
type conservationRun struct {
	entered   []byte
	exited    []byte
	discarded []byte
	causes    map[HeldCause]int
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
		for _, hf := range l.Wake(at).Exits {
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
			// release, fail there, and leave the queue by a path Wake reports nothing for.
			marker++
			payload := make([]byte, 65516)
			payload[len(payload)-1] = marker
			l.Originate(now, DefaultVRF, conservationAddrs[op.neighbor], 17, payload, true)
		case "resolve":
			l.Observe(now, Advertisement{
				Interface: "vlan10",
				Addr:      conservationAddrs[op.neighbor],
				MAC:       netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, byte(op.neighbor + 1)},
				HasMAC:    true, Solicited: true, Override: true,
			})
		case "discard":
			run.discarded = append(run.discarded, held()...)
			l.DiscardHeld()
		case "wake":
			drain(now)
		case "timeout":
			now = now.Add(2 * time.Second)
			drain(now)
		default:
			t.Fatalf("unknown op kind %q", op.kind)
		}
		observe()
	}

	// Drain whatever the sequence left behind, so a frame still sitting in a queue at the end is
	// not mistaken for one that conserved.
	drain(now)
	now = now.Add(2 * time.Second)
	drain(now)

	for _, m := range run.discarded {
		run.entered = slices.DeleteFunc(run.entered, func(e byte) bool { return e == m })
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

// TestResolveNeighborStoredZeroStateIsAMiss is finding 7: [NeighborState]'s zero value,
// [NeighborUnobserved], is meaningful only as a lookup answer, but [neighborEntry]{} is a legal
// Go zero value too. No exported path stores one — this reaches into the package to reproduce it
// directly and pins that [vrfState.resolveNeighbor] treats a stored zero state as a miss rather
// than silently starting to hold frames for a neighbor nothing ever looked up.
func TestResolveNeighborStoredZeroStateIsAMiss(t *testing.T) {
	t.Parallel()

	vs := &vrfState{
		neighbors: map[neighborKey]*neighborEntry{},
		policy:    NeighborPolicy{}.normalize(),
	}
	key := neighborKey{iface: "vlan10", addr: netip.MustParseAddr("10.0.10.9")}
	vs.neighbors[key] = &neighborEntry{}

	calls := 0
	lookup := vs.resolveNeighbor(time.Now(), key, true, func() heldEntry {
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
