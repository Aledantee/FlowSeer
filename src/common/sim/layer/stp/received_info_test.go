package stp_test

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

// rootBPDU builds an RST BPDU from a Designated port of bridge, naming root,
// at the cost given.
func rootBPDU(root, bridge bpdu.BridgeID, cost uint32) bpdu.BPDU {
	b := bpdu.BPDU{
		Version:      2,
		Type:         bpdu.TypeRapid,
		RootID:       root,
		RootPathCost: cost,
		BridgeID:     bridge,
		PortID:       0x8001,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetRole(bpdu.RoleDesignated)

	return b
}

// decodedEmissions decodes the IEEE-addressed emissions on port.
func decodedEmissions(t *testing.T, emissions []layer.Emission, port string) []bpdu.BPDU {
	t.Helper()

	var out []bpdu.BPDU
	for _, em := range emissions {
		if em.Port != port || em.Frame.Dst == bpdu.GroupAddressSSTP() {
			continue
		}
		b, err := bpdu.Decode(em.Frame)
		if err != nil {
			t.Fatalf("decode emission on %s: %v", port, err)
		}
		out = append(out, b)
	}

	return out
}

// TestInferiorBPDUOfAnotherKindLeavesStoredInformationReadAsStored pins that
// the internal or external mark moves only with stored information. A port
// holds internal information from a bridge inside its region, then hears an
// RST BPDU from another bridge that is worse, which is not stored. The mark
// stays internal, so the stored vector is still read with its regional root
// and internal cost, and the root election keeps its answer.
func TestInferiorBPDUOfAnotherKindLeavesStoredInformationReadAsStored(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	region := stp.MST{Name: "region-1", Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}}}
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"l1": {}},
		MST:      &region,
	}, mustPortTable(t, "l1"))
	l.LinkChange(t0, "l1", true, true, 1_000_000_000)

	rootID := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:aa:00:00:00:01")}
	cid := region.ConfigID()
	internal := rootBPDU(rootID, rootID, 0)
	internal.Version = 3
	internal.ConfigID = &cid
	internal.RegionalRootID = rootID
	internal.RemainingHops = 20

	l.Receive(t0.Add(time.Second), "l1", internal)
	wantRoot, wantCost, wantPort := l.Root()
	if wantPort != "l1" || wantRoot != rootID {
		t.Fatalf("Root() = %v via %q, want %v via l1 before the inferior BPDU", wantRoot, wantPort, rootID)
	}
	if info := l.PortInfo("l1"); info.Role != bpdu.RoleRoot {
		t.Fatalf("role = %v, want Root before the inferior BPDU", info.Role)
	}

	otherID := bpdu.BridgeID{Priority: 8192, Address: mustMAC(t, "00:bb:00:00:00:02")}
	l.Receive(t0.Add(2*time.Second), "l1", rootBPDU(otherID, otherID, 0))

	gotRoot, gotCost, gotPort := l.Root()
	if gotRoot != wantRoot || gotCost != wantCost || gotPort != wantPort {
		t.Errorf("Root() = %v cost %d via %q, want %v cost %d via %q: a BPDU that was not stored changed how the stored one is read",
			gotRoot, gotCost, gotPort, wantRoot, wantCost, wantPort)
	}
	if info := l.PortInfo("l1"); info.Role != bpdu.RoleRoot {
		t.Errorf("role = %v, want Root", info.Role)
	}
}

// TestPathCostSumSaturatesInsteadOfWrapping pins the election between a path
// of cost 100 and one of cost 0xfffffff0, each over a 20,000 link. The second
// sum overflows 32 bits and wrapped to a cost near 19,984, which beat the
// first.
func TestPathCostSumSaturatesInsteadOfWrapping(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{"1/1/1": {}, "1/1/2": {}})

	rootID := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:aa:00:00:00:01")}
	near := bpdu.BridgeID{Priority: 8192, Address: mustMAC(t, "00:bb:00:00:00:02")}
	far := bpdu.BridgeID{Priority: 8192, Address: mustMAC(t, "00:bb:00:00:00:03")}

	l.Receive(t0.Add(time.Second), "1/1/1", rootBPDU(rootID, near, 100))
	l.Receive(t0.Add(time.Second), "1/1/2", rootBPDU(rootID, far, 0xfffffff0))

	gotRoot, gotCost, gotPort := l.Root()
	if gotRoot != rootID || gotPort != "1/1/1" || gotCost != 100+20000 {
		t.Errorf("Root() = %v cost %d via %q, want %v cost 20100 via 1/1/1", gotRoot, gotCost, gotPort, rootID)
	}
}

// TestPathCostSumSaturatesAtTheLargestCost pins the value a saturated sum
// takes when it is the only path.
func TestPathCostSumSaturatesAtTheLargestCost(t *testing.T) {
	t.Parallel()

	l, t0 := guardLayer(t, map[string]stp.Port{"1/1/1": {}})

	rootID := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:aa:00:00:00:01")}
	l.Receive(t0.Add(time.Second), "1/1/1", rootBPDU(rootID, rootID, 0xfffffff0))

	if _, cost, port := l.Root(); port != "1/1/1" || cost != math.MaxUint32 {
		t.Errorf("Root() cost %d via %q, want %d via 1/1/1", cost, port, uint32(math.MaxUint32))
	}
}

// forwardDelayFixture brings up a bridge whose root port l1 hears a root
// advertising Forward Delay 4 seconds and Hello Time 1 second, and whose
// shared-link port l2 has no peer to agree with, so it climbs the
// forward-delay ladder. It returns the layer and the instant the root's
// BPDU first arrived.
func forwardDelayFixture(t *testing.T) (*stp.Layer, time.Time, bpdu.BPDU) {
	t.Helper()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"l1": {}, "l2": {}},
	}, mustPortTable(t, "l1", "l2"))

	rootID := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:aa:00:00:00:01")}
	root := rootBPDU(rootID, rootID, 0)
	root.HelloTime = time.Second
	root.ForwardDelay = 4 * time.Second

	l.LinkChange(t0, "l1", true, true, 1_000_000_000)
	l.Receive(t0, "l1", root)
	l.LinkChange(t0, "l2", true, false, 1_000_000_000)

	return l, t0, root
}

// TestForwardDelayLadderStepsByTheRootsForwardDelay pins the ladder to the
// Forward Delay in force, which a bridge that is not root takes from its root
// port. The root advertises 4 seconds where this bridge's own is 15.
func TestForwardDelayLadderStepsByTheRootsForwardDelay(t *testing.T) {
	t.Parallel()

	l, t0, root := forwardDelayFixture(t)

	step := func(at time.Duration) stp.PortInfo {
		now := t0.Add(at)
		l.Receive(now, "l1", root)
		l.Advance(now)

		return l.PortInfo("l2")
	}

	if info := step(3 * time.Second); info.State != stp.StateDiscarding {
		t.Fatalf("state at 3s = %v, want Discarding", info.State)
	}
	if info := step(4 * time.Second); info.State != stp.StateLearning {
		t.Errorf("state at 4s = %v, want Learning after the root's Forward Delay", info.State)
	}
	if info := step(7 * time.Second); info.State != stp.StateLearning {
		t.Errorf("state at 7s = %v, want Learning", info.State)
	}
	if info := step(8 * time.Second); info.State != stp.StateForwarding {
		t.Errorf("state at 8s = %v, want Forwarding after a second Forward Delay", info.State)
	}
}

// TestMSTIForwardDelayLadderStepsByTheCISTsTimes pins that an MSTI's ladder
// steps by the CIST's Forward Delay on a bridge that is not the regional root.
// An MSTI record carries no timers, so the MSTI's Root port has none of its
// own to read (P802.1aq/D1.5 13.28.9, draft text: FwdDelay is the Forward
// Delay component of the CIST's designatedTimes). The root advertises a
// Forward Delay of 10 seconds where this bridge's own is 15, and its hellos
// keep arriving, so a port that comes up with no peer climbs MSTI 1's ladder
// in step with the CIST's on the root's times.
func TestMSTIForwardDelayLadderStepsByTheCISTsTimes(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	region := func() *stp.MST {
		return &stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{
			1: {VLANs: []vlan.ID{10}},
		}}
	}
	root := mustNewSTP(t, stp.Config{
		Priority:     4096,
		Address:      mustMAC(t, "00:11:22:33:44:01"),
		MaxAge:       18 * time.Second,
		ForwardDelay: 10 * time.Second,
		Ports:        map[string]stp.Port{"l1": {}},
		MST:          region(),
	}, mustPortTable(t, "l1"))
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:02"),
		Ports:    map[string]stp.Port{"l1": {}, "p3": {}},
		MST:      region(),
	}, mustPortTable(t, "l1", "p3"))

	layers := []*stp.Layer{root, l}
	cables := []cableLink{{swA: 0, portA: "l1", swB: 1, portB: "l1"}}
	snapshot := func() string {
		cist, msti := l.VLANPortInfo(1, "l1"), l.VLANPortInfo(10, "l1")
		return fmt.Sprintf("%v/%v %v/%v", cist.Role, cist.State, msti.Role, msti.State)
	}
	t0 := convergeLayers(t, start, layers, cables, snapshot)
	if info := l.VLANPortInfo(10, "l1"); info.Role != bpdu.RoleRoot {
		t.Fatalf("MSTI 1 l1 role = %v, want Root before the peerless port comes up", info.Role)
	}

	l.LinkChange(t0, "p3", true, true, 1_000_000_000)

	// Only l1 is cabled, so a frame from either side's l1 reaches the other's.
	deliver := func(now time.Time, from int, emissions []layer.Emission) {
		type frame struct {
			to int
			f  ethernet.Frame
		}
		var queue []frame
		push := func(src int, ems []layer.Emission) {
			for _, em := range ems {
				if em.Port == "l1" {
					queue = append(queue, frame{to: 1 - src, f: em.Frame})
				}
			}
		}
		push(from, emissions)
		for len(queue) > 0 {
			next := queue[0]
			queue = queue[1:]
			b, err := bpdu.Decode(next.f)
			if err != nil {
				t.Fatalf("decode a frame for switch %d: %v", next.to, err)
			}
			push(next.to, layers[next.to].Receive(now, "l1", b).Emissions)
		}
	}
	runUntil := func(target time.Time) {
		for {
			var wake time.Time
			found := false
			for _, sw := range layers {
				if w, ok := sw.NextWake(); ok && !w.After(target) && (!found || w.Before(wake)) {
					wake, found = w, true
				}
			}
			if !found {
				break
			}
			for i, sw := range layers {
				deliver(wake, i, sw.Advance(wake).Emissions)
			}
		}
		for i, sw := range layers {
			deliver(target, i, sw.Advance(target).Emissions)
		}
	}

	for _, c := range []struct {
		at   time.Duration
		want stp.State
	}{
		{0, stp.StateDiscarding},
		{9 * time.Second, stp.StateDiscarding},
		{10 * time.Second, stp.StateLearning},
		{19 * time.Second, stp.StateLearning},
		{20 * time.Second, stp.StateForwarding},
	} {
		runUntil(t0.Add(c.at))
		if info := l.VLANPortInfo(10, "l1"); info.Role != bpdu.RoleRoot {
			t.Fatalf("MSTI 1 l1 role at +%v = %v, want Root: the root's information must stay current", c.at, info.Role)
		}
		if got := l.VLANPortInfo(1, "p3").State; got != c.want {
			t.Fatalf("CIST p3 state at +%v = %v, want %v", c.at, got, c.want)
		}
		if got := l.VLANPortInfo(10, "p3").State; got != c.want {
			t.Errorf("MSTI 1 p3 state at +%v = %v, want %v in step with the CIST", c.at, got, c.want)
		}
	}
}

// TestHelloTimeFieldCarriesTheBridgesOwn pins that a BPDU names this bridge's
// own Hello Time whatever the root advertised, and that Times reports it
// beside the root's Max Age and Forward Delay.
func TestHelloTimeFieldCarriesTheBridgesOwn(t *testing.T) {
	t.Parallel()

	l, t0, _ := forwardDelayFixture(t)

	fx := l.Advance(t0.Add(2 * time.Second))
	sent := decodedEmissions(t, fx.Emissions, "l2")
	if len(sent) == 0 {
		t.Fatal("no BPDU on the Designated port l2 at the hello")
	}
	for _, b := range sent {
		if b.HelloTime != 2*time.Second {
			t.Errorf("Hello Time field = %v, want the bridge's own 2s", b.HelloTime)
		}
		if b.ForwardDelay != 4*time.Second {
			t.Errorf("Forward Delay field = %v, want the root's 4s", b.ForwardDelay)
		}
	}

	maxAge, hello, forwardDelay := l.Times()
	if maxAge != 20*time.Second || hello != 2*time.Second || forwardDelay != 4*time.Second {
		t.Errorf("Times() = %v, %v, %v, want 20s, 2s, 4s", maxAge, hello, forwardDelay)
	}
}

// TestAdvanceExpiresInformationBeforeItSendsAHello pins the order inside one
// Advance. The root's information expires at the instant a hello is due, so
// the hello must name this bridge as root.
func TestAdvanceExpiresInformationBeforeItSendsAHello(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	own := bpdu.BridgeID{Priority: 32768, Address: mustMAC(t, "00:11:22:33:44:01")}
	l := mustNewSTP(t, stp.Config{
		Priority: own.Priority,
		Address:  own.Address,
		Ports:    map[string]stp.Port{"l1": {}, "l2": {}},
	}, mustPortTable(t, "l1", "l2"))
	l.LinkChange(t0, "l1", true, true, 1_000_000_000)
	l.LinkChange(t0, "l2", true, true, 1_000_000_000)

	rootID := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:aa:00:00:00:01")}
	l.Receive(t0, "l1", rootBPDU(rootID, rootID, 0))
	if got, _, _ := l.Root(); got != rootID {
		t.Fatalf("root before expiry = %v, want %v", got, rootID)
	}

	// The information lives 3 hello times from t0, and the hello armed at t0
	// is due every 2 seconds, so both are due at 6 seconds.
	fx := l.Advance(t0.Add(6 * time.Second))

	sent := decodedEmissions(t, fx.Emissions, "l2")
	if len(sent) == 0 {
		t.Fatal("no BPDU on l2 at the instant the hello is due")
	}
	for _, b := range sent {
		if b.RootID != own {
			t.Errorf("BPDU on l2 names root %v, want this bridge %v: the information had expired", b.RootID, own)
		}
	}
}

// pvstLoopGuardLayer builds a PVST bridge with VLANs 1 and 10 and loop guard
// on l1, whose root port is l1 for both trees. It returns the layer and the
// instant both trees heard their root.
func pvstLoopGuardLayer(t *testing.T) (*stp.Layer, time.Time) {
	t.Helper()

	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"l1": {LoopGuard: true}},
		PVST:     pvstTrees(nil, 1, 10),
	}, mustPortTable(t, "l1"))
	l.LinkChange(t0, "l1", true, true, 1_000_000_000)

	now := t0.Add(time.Second)
	l.Receive(now, "l1", superiorBPDU(0, 20*time.Second))

	vlan10 := superiorBPDU(0, 20*time.Second)
	vlan10.RootID.Priority |= 10
	vlan10.BridgeID.Priority |= 10
	arrival := stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}
	if _, outcome := l.ReceiveSSTP(now, "l1", arrival, vlan10); outcome != stp.SSTPApplied {
		t.Fatalf("VLAN 10 outcome = %q, want applied", outcome)
	}
	if info := l.VLANPortInfo(10, "l1"); info.Role != bpdu.RoleRoot {
		t.Fatalf("VLAN 10 role = %v, want Root", info.Role)
	}

	return l, now
}

// expireVLAN10 lets VLAN 10's information age out while VLAN 1 keeps hearing
// its root, and returns the instant of the expiry. VLAN 1 is last heard 2
// seconds before the expiry, so a Receive never lands on the expiry instant,
// where the election would already have dropped VLAN 10's Root role.
func expireVLAN10(t *testing.T, l *stp.Layer, heard time.Time) time.Time {
	t.Helper()

	for _, at := range []time.Duration{2 * time.Second, 4 * time.Second} {
		l.Receive(heard.Add(at), "l1", superiorBPDU(0, 20*time.Second))
	}
	now := heard.Add(6 * time.Second)
	l.Advance(now)

	return now
}

// TestLoopGuardWatchesEveryPVSTTree pins that a port that is Root for a PVST
// VLAN other than 1 is held when that tree's information expires, while a
// tree that kept hearing its root forwards.
func TestLoopGuardWatchesEveryPVSTTree(t *testing.T) {
	t.Parallel()

	l, heard := pvstLoopGuardLayer(t)
	expireVLAN10(t, l, heard)

	vlan10 := l.VLANPortInfo(10, "l1")
	if vlan10.BlockReason != stp.BlockReasonLoopInconsistent {
		t.Errorf("VLAN 10 block reason = %q, want %q", vlan10.BlockReason, stp.BlockReasonLoopInconsistent)
	}
	if vlan10.Role != bpdu.RoleAlternate || vlan10.State != stp.StateDiscarding {
		t.Errorf("VLAN 10 = %v/%v, want Alternate/Discarding", vlan10.Role, vlan10.State)
	}
	if l.Forwards("l1", 10) {
		t.Error("VLAN 10 forwards on a loop-inconsistent port")
	}

	vlan1 := l.PortInfo("l1")
	if vlan1.BlockReason != "" || vlan1.Role != bpdu.RoleRoot || vlan1.State != stp.StateForwarding {
		t.Errorf("VLAN 1 = %v/%v reason %q, want Root/Forwarding with none", vlan1.Role, vlan1.State, vlan1.BlockReason)
	}
}

// TestPVSTLoopGuardClearsOnABPDUAppliedToItsOwnTree pins the recovery: a BPDU
// applied to VLAN 1 leaves VLAN 10 held, and one applied to VLAN 10 releases it.
func TestPVSTLoopGuardClearsOnABPDUAppliedToItsOwnTree(t *testing.T) {
	t.Parallel()

	l, heard := pvstLoopGuardLayer(t)
	now := expireVLAN10(t, l, heard)
	if reason := l.VLANPortInfo(10, "l1").BlockReason; reason != stp.BlockReasonLoopInconsistent {
		t.Fatalf("VLAN 10 block reason = %q, want %q before the recovery", reason, stp.BlockReasonLoopInconsistent)
	}

	now = now.Add(time.Second)
	l.Receive(now, "l1", superiorBPDU(0, 20*time.Second))
	if reason := l.VLANPortInfo(10, "l1").BlockReason; reason != stp.BlockReasonLoopInconsistent {
		t.Errorf("VLAN 10 block reason = %q after a VLAN 1 BPDU, want it held", reason)
	}

	vlan10 := superiorBPDU(0, 20*time.Second)
	vlan10.RootID.Priority |= 10
	vlan10.BridgeID.Priority |= 10
	arrival := stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}
	l.ReceiveSSTP(now, "l1", arrival, vlan10)
	if info := l.VLANPortInfo(10, "l1"); info.BlockReason != "" || info.Role != bpdu.RoleRoot {
		t.Errorf("VLAN 10 = %v reason %q after its own BPDU, want Root with none", info.Role, info.BlockReason)
	}
}

// TestPVSTLoopGuardClearsOnLinkDown pins that a link down releases every
// tree's mark.
func TestPVSTLoopGuardClearsOnLinkDown(t *testing.T) {
	t.Parallel()

	l, heard := pvstLoopGuardLayer(t)
	now := expireVLAN10(t, l, heard)
	if reason := l.VLANPortInfo(10, "l1").BlockReason; reason != stp.BlockReasonLoopInconsistent {
		t.Fatalf("VLAN 10 block reason = %q, want %q before the link goes down", reason, stp.BlockReasonLoopInconsistent)
	}

	l.LinkChange(now.Add(time.Second), "l1", false, true, 0)
	if reason := l.VLANPortInfo(10, "l1").BlockReason; reason != "" {
		t.Errorf("VLAN 10 block reason = %q after link down, want none", reason)
	}
}

// bpduDecisionText returns the canonical text of the decision fact for b.
func bpduDecisionText(b bpdu.BPDU) string {
	return stp.BPDUDecisionFact(b, stp.PortInfo{}, stp.PortInfo{}).Canonical()
}

// TestBPDUDecisionFactCarriesTheMSTFields pins that two MST BPDUs differing
// in one MSTI record's cost give two fact texts, and that each field of the
// MST shape appears.
func TestBPDUDecisionFactCarriesTheMSTFields(t *testing.T) {
	t.Parallel()

	region := stp.MST{Name: "region-1", Revision: 3, Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}}}
	cid := region.ConfigID()
	rootID := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:aa:00:00:00:01")}

	build := func(cost uint32) bpdu.BPDU {
		b := rootBPDU(rootID, rootID, 0)
		b.Version = 3
		b.ConfigID = &cid
		b.RegionalRootID = bpdu.BridgeID{Priority: 8192, Address: mustMAC(t, "00:bb:00:00:00:02")}
		b.InternalRootPathCost = 777
		b.RemainingHops = 19
		b.MSTIs = []bpdu.MSTIRecord{{
			MSTID:                1,
			RegionalRootID:       rootID,
			InternalRootPathCost: cost,
			BridgePriority:       0x40,
			PortPriority:         0x80,
			RemainingHops:        18,
		}}

		return b
	}

	a, b := bpduDecisionText(build(100)), bpduDecisionText(build(200))
	if a == b {
		t.Fatal("two BPDUs differing in one MSTI record's cost give one fact text")
	}

	for _, want := range []string{
		`region-1`, `8192/00:bb:00:00:00:02`, `777`, `19`, `mstid=1`, `internal_cost=100`, `remaining_hops=18`,
	} {
		if !strings.Contains(a, want) {
			t.Errorf("fact text lacks %q: %s", want, a)
		}
	}
}

// TestBPDUDecisionFactOfAPlainBPDUHasNoMSTFields pins that a BPDU without a
// configuration identifier prints as it did before the MST fields existed.
func TestBPDUDecisionFactOfAPlainBPDUHasNoMSTFields(t *testing.T) {
	t.Parallel()

	rootID := bpdu.BridgeID{Priority: 4096, Address: mustMAC(t, "00:aa:00:00:00:01")}
	got := bpduDecisionText(rootBPDU(rootID, rootID, 0))

	const want = `bpdu={version=2;type=0;flags=12;root="4096/00:aa:00:00:00:01";root_cost=0;` +
		`bridge="4096/00:aa:00:00:00:01";port_id=32769;message_age=0;max_age=20000000000;` +
		`hello=2000000000;forward_delay=15000000000};before=`
	if !strings.HasPrefix(got, want) {
		t.Errorf("fact text = %s, want prefix %s", got, want)
	}
}

// TestFactsOfAPVSTVLANAndAnMSTIDiffer pins that a PVST VLAN 10 snapshot and an
// MSTI 10 snapshot are not the same text, since the two are different kinds
// of tree that share the number.
func TestFactsOfAPVSTVLANAndAnMSTIDiffer(t *testing.T) {
	t.Parallel()

	pvst := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"l1": {}},
		PVST:     pvstTrees(nil, 1, 10),
	}, mustPortTable(t, "l1"))
	mst := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  mustMAC(t, "00:11:22:33:44:01"),
		Ports:    map[string]stp.Port{"l1": {}},
		MST:      &stp.MST{Name: "region-1", Instances: map[bpdu.MSTID]stp.Instance{10: {VLANs: []vlan.ID{10}}}},
	}, mustPortTable(t, "l1"))

	a := pvst.ForwardingFact("l1", 10, true, true).Canonical()
	b := mst.ForwardingFact("l1", 10, true, true).Canonical()
	if a == b {
		t.Errorf("a PVST VLAN 10 fact and an MSTI 10 fact are the same text: %s", a)
	}
	if want := `state={tree_kind="VLAN";tree_id=10;`; !strings.Contains(a, want) {
		t.Errorf("PVST fact = %s, want it to contain %s", a, want)
	}
	if want := `state={tree_kind="MSTI";tree_id=10;`; !strings.Contains(b, want) {
		t.Errorf("MSTI fact = %s, want it to contain %s", b, want)
	}
}

// TestPortInfoNamesItsTreeByKind pins the kind and identifier of each tree a
// snapshot can describe, including VLAN 1's tree under PVST, which sits in the
// CIST's slot but is a VLAN tree.
func TestPortInfoNamesItsTreeByKind(t *testing.T) {
	t.Parallel()

	ports := mustPortTable(t, "l1")
	addr := mustMAC(t, "00:11:22:33:44:01")

	rstp := mustNewSTP(t, stp.Config{Priority: 32768, Address: addr, Ports: map[string]stp.Port{"l1": {}}}, ports)
	mst := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  addr,
		Ports:    map[string]stp.Port{"l1": {}},
		MST:      &stp.MST{Name: "region-1", Instances: map[bpdu.MSTID]stp.Instance{10: {VLANs: []vlan.ID{10}}}},
	}, ports)
	pvst := mustNewSTP(t, stp.Config{
		Priority: 32768,
		Address:  addr,
		Ports:    map[string]stp.Port{"l1": {}},
		PVST:     pvstTrees(nil, 1, 10),
	}, ports)

	for _, c := range []struct {
		name string
		got  stp.PortInfo
		want stp.TreeRef
	}{
		{"RSTP common tree", rstp.PortInfo("l1"), stp.TreeRef{Kind: stp.TreeCIST, ID: 0}},
		{"MST common tree", mst.PortInfo("l1"), stp.TreeRef{Kind: stp.TreeCIST, ID: 0}},
		{"MSTI 10", mst.VLANPortInfo(10, "l1"), stp.TreeRef{Kind: stp.TreeMSTI, ID: 10}},
		{"PVST VLAN 1", pvst.PortInfo("l1"), stp.TreeRef{Kind: stp.TreeVLAN, ID: 1}},
		{"PVST VLAN 10", pvst.VLANPortInfo(10, "l1"), stp.TreeRef{Kind: stp.TreeVLAN, ID: 10}},
	} {
		if c.got.Tree != c.want {
			t.Errorf("%s: Tree = %+v, want %+v", c.name, c.got.Tree, c.want)
		}
	}
}
