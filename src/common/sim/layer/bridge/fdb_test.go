package bridge_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestSwitchportAdmitsFID(t *testing.T) {
	for _, tc := range []struct {
		name string
		port bridge.Switchport
		want bool
	}{
		{"PVID", bridge.Switchport{PVID: new(vlan.ID(10))}, true},
		{"tagged", bridge.Switchport{Tagged: []vlan.ID{10}}, true},
		{"untagged", bridge.Switchport{Untagged: []vlan.ID{10}}, true},
		{"tunnel", bridge.Switchport{Tunnel: &bridge.Tunnel{VID: 10}}, true},
		{"other VLAN", bridge.Switchport{Tagged: []vlan.ID{20}}, false},
		{"absent", bridge.Switchport{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bridge.SwitchportAdmitsFID(tc.port, 10); got != tc.want {
				t.Fatalf("SwitchportAdmitsFID = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestSeedNamingALagMemberIsStoredUnderTheLag(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
	b.Range("1/1/%d", 5, 6, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	tbl, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	macB := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0xbb}
	br := mustNewBridge(t, bridge.Config{}, tbl)
	if err := br.InstallSeeds([]bridge.Seed{{MAC: macB, Port: "1/1/6", LearnedAt: now}}); err != nil {
		t.Fatalf("InstallSeeds() error = %v", err)
	}
	entries := br.Entries()
	if len(entries) != 1 || entries[0].Port != "lag1" {
		t.Fatalf("Entries() = %+v, want one entry on lag1", entries)
	}
	if err := br.InstallSeeds([]bridge.Seed{{MAC: netaddr.MAC{0x01, 0x00, 0x5e, 0, 0, 1}, Port: "1/1/1", LearnedAt: now}}); err == nil {
		t.Fatal("InstallSeeds() accepted a group MAC")
	}
	if got := br.Entries(); len(got) != 1 || got[0] != entries[0] {
		t.Errorf("Entries() after invalid Learn = %+v, want unchanged %+v", got, entries)
	}

	res := br.Forward(now, "1/1/5", ethernet.Frame{Dst: macB, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa}})
	if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonSamePort {
		t.Errorf("a frame to macB from the other member = %s/%s, want Dropped/%s", res.Outcome, res.Reason, bridge.ReasonSamePort)
	}
}

func TestDynamicEntriesAgeAndStaticEntriesPersist(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	br := mustNewBridge(t, cfg, ports)
	learnFrame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("learn"),
	}
	br.Forward(testTime0, "1/1/1", learnFrame)

	mustLearn(t, br, []bridge.Seed{
		{
			FID:       10,
			MAC:       macC,
			Port:      "1/1/2",
			Lifetime:  bridge.Static,
			LearnedAt: testTime0,
		},
	})

	t299 := testTime0.Add(299 * time.Second)
	br.Advance(t299)

	queryDynamic := ethernet.Frame{
		Dst:       macA,
		Src:       macD,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("query"),
	}
	res299 := br.Peek(t299, "1/1/3", queryDynamic)
	if res299.Outcome != trace.Forwarded {
		t.Fatalf("dynamic entry at t0+299s outcome = %q, want Forwarded", res299.Outcome)
	}

	t301 := testTime0.Add(301 * time.Second)
	br.Advance(t301)

	res301 := br.Peek(t301, "1/1/3", queryDynamic)
	if res301.Outcome != trace.Flooded {
		t.Fatalf("aged dynamic entry at t0+301s outcome = %q, want Flooded", res301.Outcome)
	}

	queryStatic := ethernet.Frame{
		Dst:       macC,
		Src:       macD,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("query-static"),
	}
	resStatic := br.Peek(t301, "1/1/3", queryStatic)
	if resStatic.Outcome != trace.Forwarded || resStatic.Egress[0].Port != "1/1/2" {
		t.Fatalf("static entry after age outcome = %q port = %s, want Forwarded to 1/1/2", resStatic.Outcome, resStatic.Egress[0].Port)
	}

	frameFromStaticMacOnAnotherPort := ethernet.Frame{
		Dst:       macB,
		Src:       macC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("move-static"),
	}
	br.Forward(t301, "1/1/1", frameFromStaticMacOnAnotherPort)

	for _, entry := range br.Entries() {
		if entry.MAC == macC {
			if entry.Port != "1/1/2" {
				t.Errorf("static entry port moved to %q, want 1/1/2", entry.Port)
			}
		}
	}
}

// TestFlushWithEmptyFIDsRemovesEveryFIDOnThePort is evidence that a
// [layer.FlushTarget] with no FIDs, the shape a link down or a CIST-wide
// change produces, removes every dynamic entry on its port regardless of FID.
func TestFlushWithEmptyFIDsRemovesEveryFIDOnThePort(t *testing.T) {
	ports := buildTestPorts(t, 3)
	br := mustNewBridge(t, bridge.Config{}, ports)
	br.Forward(testTime0, "1/1/1", ethernet.Frame{Dst: macB, Src: macA, EtherType: ethernet.EtherTypeIPv4})
	br.Forward(testTime0, "1/1/2", ethernet.Frame{Dst: macA, Src: macB, EtherType: ethernet.EtherTypeIPv4})

	if len(br.Entries()) != 2 {
		t.Fatalf("initial entries count = %d, want 2", len(br.Entries()))
	}

	br.Flush([]layer.FlushTarget{{Port: "1/1/1"}})
	entries := br.Entries()
	if len(entries) != 1 || entries[0].Port != "1/1/2" {
		t.Fatalf("after Flush Entries() = %+v, want 1 entry on 1/1/2", entries)
	}
}

// TestFlushWithFIDsFiltersToTheNamedFIDs is evidence that a [layer.FlushTarget]
// naming FIDs removes only entries on that port carrying one of them, which is
// the shape a per-tree topology change produces: the other FIDs on the same
// port survive.
func TestFlushWithFIDsFiltersToTheNamedFIDs(t *testing.T) {
	ports := buildTestPorts(t, 3)
	trunk := bridge.Switchport{PVID: mustVLAN(10), Tagged: []vlan.ID{10, 20}}
	br := mustNewBridge(t, bridge.Config{VLAN: &bridge.VLAN{
		Table: map[vlan.ID]string{10: "ten", 20: "twenty"},
		Switchports: map[string]bridge.Switchport{
			"1/1/1": trunk,
			"1/1/2": trunk,
			"1/1/3": trunk,
		},
	}}, ports)
	now := testTime0
	mustLearn(t, br, []bridge.Seed{
		{FID: 10, MAC: macA, Port: "1/1/1", LearnedAt: now},
		{FID: 20, MAC: macB, Port: "1/1/1", LearnedAt: now},
		{FID: 10, MAC: macC, Port: "1/1/2", LearnedAt: now},
	})

	br.Flush([]layer.FlushTarget{{Port: "1/1/1", FIDs: []vlan.ID{10}}})

	entries := br.Entries()
	if len(entries) != 2 {
		t.Fatalf("after Flush Entries() = %+v, want 2 entries (VLAN 20 on 1/1/1 and VLAN 10 on 1/1/2 kept)", entries)
	}
	for _, e := range entries {
		if e.Port == "1/1/1" && e.FID != 20 {
			t.Errorf("entry kept on 1/1/1 = %+v, want only the FID 20 entry", e)
		}
		if e.Port == "1/1/2" && e.FID != 10 {
			t.Errorf("entry kept on 1/1/2 = %+v, want the untouched FID 10 entry", e)
		}
	}
}

// TestFlushUnionsTargetsNamingTheSamePort is a guard for a caller that builds
// its targets one tree at a time and so can name a port twice. The two FID sets
// are unioned, and an empty set on either side widens the port to every FID; a
// later target replacing an earlier one would silently drop the first tree's
// flush and leave entries the topology change made stale.
func TestFlushUnionsTargetsNamingTheSamePort(t *testing.T) {
	trunk := bridge.Switchport{PVID: mustVLAN(10), Tagged: []vlan.ID{10, 20, 30}}
	vlanCfg := func() *bridge.VLAN {
		return &bridge.VLAN{
			Table: map[vlan.ID]string{10: "ten", 20: "twenty", 30: "thirty"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": trunk,
				"1/1/2": trunk,
				"1/1/3": trunk,
			},
		}
	}
	seeds := func() []bridge.Seed {
		return []bridge.Seed{
			{FID: 10, MAC: macA, Port: "1/1/1", LearnedAt: testTime0},
			{FID: 20, MAC: macB, Port: "1/1/1", LearnedAt: testTime0},
			{FID: 30, MAC: macC, Port: "1/1/1", LearnedAt: testTime0},
		}
	}

	t.Run("two FID sets union", func(t *testing.T) {
		br := mustNewBridge(t, bridge.Config{VLAN: vlanCfg()}, buildTestPorts(t, 3))
		mustLearn(t, br, seeds())

		br.Flush([]layer.FlushTarget{
			{Port: "1/1/1", FIDs: []vlan.ID{10}},
			{Port: "1/1/1", FIDs: []vlan.ID{20}},
		})

		entries := br.Entries()
		if len(entries) != 1 || entries[0].FID != 30 {
			t.Fatalf("after Flush Entries() = %+v, want only the FID 30 entry left", entries)
		}
	})

	t.Run("an empty set widens to every FID", func(t *testing.T) {
		br := mustNewBridge(t, bridge.Config{VLAN: vlanCfg()}, buildTestPorts(t, 3))
		mustLearn(t, br, seeds())

		// The empty target comes first, so a later target replacing an earlier
		// one would narrow the flush to FID 10 rather than widening it.
		br.Flush([]layer.FlushTarget{
			{Port: "1/1/1"},
			{Port: "1/1/1", FIDs: []vlan.ID{10}},
		})

		if entries := br.Entries(); len(entries) != 0 {
			t.Fatalf("after Flush Entries() = %+v, want every entry on the port gone", entries)
		}
	})
}

func TestBoundedTableEvictsOldestDynamicEntry(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		MaxEntries: 2,
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "ten",
				20: "twenty",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {Tagged: []vlan.ID{10, 20}},
				"1/1/4": {},
			},
		},
	}
	br := mustNewBridge(t, cfg, ports)

	broadcastMAC := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	frameA := ethernet.Frame{Dst: broadcastMAC, Src: macA}
	frameB := ethernet.Frame{Dst: broadcastMAC, Src: macB}
	frameC := ethernet.Frame{Dst: broadcastMAC, Src: macC}

	br.Forward(testTime0, "1/1/1", frameA)
	br.Forward(testTime0.Add(1*time.Second), "1/1/2", frameB)
	resC := br.Forward(testTime0.Add(2*time.Second), "1/1/1", frameC)

	entries := br.Entries()
	if len(entries) != 2 {
		t.Fatalf("len(Entries()) = %d, want 2", len(entries))
	}
	if entries[0].MAC != macB || entries[1].MAC != macC {
		t.Errorf("Entries() = %+v, want MACs B and C", entries)
	}

	counters := br.Counters()
	wantCounters := bridge.Counters{Learned: 3, Evicted: 1}
	if counters != wantCounters {
		t.Errorf("Counters() = %+v, want %+v", counters, wantCounters)
	}

	foundEvictedStep := false
	for _, step := range resC.Steps {
		if step.Layer == bridge.LayerName && step.Op == trace.OpLearn && step.RuleID == "evict" && step.Subject.Key == macA.String() {
			foundEvictedStep = true
			break
		}
	}
	if !foundEvictedStep {
		t.Errorf("resC.Steps did not contain learn step with RuleID \"evict\" for %s; steps = %+v", macA, resC.Steps)
	}

	br.Forward(testTime0.Add(3*time.Second), "1/1/2", frameB)
	countersAfterB := br.Counters()
	if countersAfterB.Learned != 3 || countersAfterB.Evicted != 1 {
		t.Errorf("Counters() after fourth frame = %+v, want Learned: 3, Evicted: 1", countersAfterB)
	}
}

func TestMovedCounterIncrementsOnPortChange(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "ten",
				20: "twenty",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {Tagged: []vlan.ID{10, 20}},
				"1/1/4": {},
			},
		},
	}
	br := mustNewBridge(t, cfg, ports)

	broadcastMAC := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	frameA := ethernet.Frame{Dst: broadcastMAC, Src: macA}
	br.Forward(testTime0, "1/1/1", frameA)
	br.Forward(testTime0.Add(1*time.Second), "1/1/2", frameA)

	counters := br.Counters()
	if counters.Moved != 1 || counters.Learned != 1 {
		t.Errorf("Counters() = %+v, want Moved: 1, Learned: 1", counters)
	}
}

func TestStaticEntriesSurviveAgingAndTheBound(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		MaxEntries: 1,
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "ten",
				20: "twenty",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {Tagged: []vlan.ID{10, 20}},
				"1/1/4": {},
			},
		},
	}
	br := mustNewBridge(t, cfg, ports)

	mustLearn(t, br, []bridge.Seed{
		{FID: 10, MAC: macD, Port: "1/1/3", Lifetime: bridge.Static, LearnedAt: testTime0},
	})

	broadcastMAC := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	frameA := ethernet.Frame{Dst: broadcastMAC, Src: macA}
	br.Forward(testTime0, "1/1/1", frameA)

	counters := br.Counters()
	if counters.Learned != 1 || counters.Evicted != 0 {
		t.Errorf("Counters() after frame A = %+v, want Learned: 1, Evicted: 0", counters)
	}
	if entries := br.Entries(); len(entries) != 2 {
		t.Fatalf("len(Entries()) = %d, want 2", len(entries))
	}

	br.Advance(testTime0.Add(301 * time.Second))
	counters = br.Counters()
	if counters.Expired != 1 {
		t.Errorf("Counters().Expired = %d, want 1", counters.Expired)
	}
	entries := br.Entries()
	if len(entries) != 1 {
		t.Fatalf("len(Entries()) after Advance = %d, want 1", len(entries))
	}
	if entries[0].MAC != macD || entries[0].Lifetime != bridge.Static {
		t.Errorf("Entries()[0] = %+v, want D with Lifetime: bridge.Static", entries[0])
	}

	if !br.Forget(10, macD) {
		t.Errorf("Forget(10, D) = false, want true")
	}
	if len(br.Entries()) != 0 {
		t.Errorf("len(Entries()) after Forget = %d, want 0", len(br.Entries()))
	}

	if br.Forget(10, macD) {
		t.Errorf("second Forget(10, D) = true, want false")
	}
}

// TestAgeActsOnLifetimeNotOrigin verifies that Origin and Lifetime are independent axes,
// so Advance removes exactly the Aging entries regardless of who installed
// them, and a Configured, Aging entry ages while an Observed, Static one does not.
func TestAgeActsOnLifetimeNotOrigin(t *testing.T) {
	ports := buildTestPorts(t, 4)
	br := mustNewBridge(t, bridge.Config{}, ports)

	mustLearn(t, br, []bridge.Seed{
		{MAC: macA, Port: "1/1/1", Origin: bridge.Configured, Lifetime: bridge.Static, LearnedAt: testTime0},
		{MAC: macB, Port: "1/1/2", Origin: bridge.Configured, Lifetime: bridge.Aging, LearnedAt: testTime0},
		{MAC: macC, Port: "1/1/3", Origin: bridge.Observed, Lifetime: bridge.Static, LearnedAt: testTime0},
		{MAC: macD, Port: "1/1/4", Origin: bridge.Observed, Lifetime: bridge.Aging, LearnedAt: testTime0},
	})

	br.Advance(testTime0.Add(301 * time.Second))

	entries := br.Entries()
	if len(entries) != 2 {
		t.Fatalf("len(Entries()) after Advance = %d, want 2, got %+v", len(entries), entries)
	}
	byMAC := make(map[netaddr.MAC]bridge.Entry, len(entries))
	for _, e := range entries {
		byMAC[e.MAC] = e
	}
	if a, ok := byMAC[macA]; !ok || a.Origin != bridge.Configured || a.Lifetime != bridge.Static {
		t.Errorf("Configured, Static entry (A) = %+v, ok=%v, want present and unchanged", a, ok)
	}
	if _, ok := byMAC[macB]; ok {
		t.Errorf("Configured, Aging entry (B) survived Advance, want removed")
	}
	if c, ok := byMAC[macC]; !ok || c.Origin != bridge.Observed || c.Lifetime != bridge.Static {
		t.Errorf("Observed, Static entry (C) = %+v, ok=%v, want present and unchanged", c, ok)
	}
	if _, ok := byMAC[macD]; ok {
		t.Errorf("Observed, Aging entry (D) survived Advance, want removed")
	}
}

func TestLearnSeedsCountAndKeepTheBound(t *testing.T) {
	ports := buildTestPorts(t, 4)
	br := mustNewBridge(t, bridge.Config{MaxEntries: 2}, ports)

	mustLearn(t, br, []bridge.Seed{
		{MAC: macA, Port: "1/1/1", LearnedAt: testTime0},
		{MAC: macB, Port: "1/1/2", LearnedAt: testTime0.Add(time.Second)},
		{MAC: macC, Port: "1/1/1", LearnedAt: testTime0.Add(2 * time.Second)},
	})
	if got := br.Counters(); got.Learned != 3 || got.Evicted != 1 {
		t.Fatalf("Counters() after three seeds = %+v, want Learned 3, Evicted 1", got)
	}
	if entries := br.Entries(); len(entries) != 2 || entries[0].MAC != macB || entries[1].MAC != macC {
		t.Fatalf("Entries() = %+v, want B and C with the oldest seed evicted", entries)
	}

	mustLearn(t, br, []bridge.Seed{{MAC: macB, Port: "1/1/3", LearnedAt: testTime0.Add(3 * time.Second)}})
	if got := br.Counters(); got.Moved != 1 || got.Learned != 3 || got.Evicted != 1 {
		t.Fatalf("Counters() after a seed moved B = %+v, want Moved 1 and nothing else changed", got)
	}

	// A removal frees a slot, so the next new seed evicts nothing.
	if !br.Forget(0, macC) {
		t.Fatal("Forget(C) = false, want true")
	}
	mustLearn(t, br, []bridge.Seed{{MAC: macD, Port: "1/1/4", LearnedAt: testTime0.Add(4 * time.Second)}})
	if got := br.Counters(); got.Evicted != 1 || got.Learned != 4 {
		t.Fatalf("Counters() after Forget then a new seed = %+v, want Learned 4, Evicted 1", got)
	}
}
