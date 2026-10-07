package stp

import (
	"fmt"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

func rerootBridge(mst *MST, pvst *PVST) (*Layer, time.Time) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	l := newLayer(Config{
		Priority: 32768,
		Address:  syncMAC(9),
		Ports:    map[string]Port{"p1": {}, "p2": {}, "p3": {}},
		MST:      mst,
		PVST:     pvst,
	}.Normalize(layer.Env{}))
	for _, name := range []string{"p1", "p2", "p3"} {
		l.LinkChange(now, name, true, true, 1_000_000_000)
	}
	return l, now
}

func rerootBPDU(priority uint16, cost uint32) bpdu.BPDU {
	return syncBPDU(syncMAC(1), syncMAC(2), priority, cost, bpdu.RoleDesignated)
}

func rerootPair(t *testing.T) (*Layer, time.Time) {
	t.Helper()
	l, now := rerootBridge(nil, nil)
	l.Receive(now, "p1", rerootBPDU(4096, 20_000))
	l.Receive(now, "p2", rerootBPDU(4096, 40_000))
	if p := l.cist().ports["p1"]; p.role != bpdu.RoleRoot || p.state != StateForwarding {
		t.Fatalf("fixture p1 = %v/%v, want Root/Forwarding", p.role, p.state)
	}
	if p := l.cist().ports["p2"]; p.role != bpdu.RoleAlternate || p.state != StateDiscarding {
		t.Fatalf("fixture p2 = %v/%v, want Alternate/Discarding", p.role, p.state)
	}
	return l, now
}

func rerootState(t *testing.T, tr *tree, name string, role bpdu.Role, state State) {
	t.Helper()
	p := tr.ports[name]
	if p.role != role || p.state != state {
		t.Errorf("tree %d %s = %v/%v, want %v/%v", tr.id, name, p.role, p.state, role, state)
	}
}

func rerootAgreement(t *testing.T, fx layer.Effects, name string, msti bool, want bool) {
	t.Helper()
	for _, emission := range fx.Emissions {
		if emission.Port != name {
			continue
		}
		b, err := bpdu.Decode(emission.Frame)
		if err != nil {
			t.Fatalf("Decode emitted BPDU: %v", err)
		}
		if msti {
			found := false
			for _, record := range b.MSTIs {
				if record.MSTID == 1 {
					b.Flags = record.Flags
					found = true
				}
			}
			if !found {
				t.Fatal("emitted BPDU has no MSTI 1 record")
			}
		}
		if b.Agreement() != want {
			t.Errorf("%s Agreement = %v, want %v", name, b.Agreement(), want)
		}
		return
	}
	t.Fatalf("no frame emitted on %s", name)
}

func TestRerootForwardsWithoutSync(t *testing.T) {
	t.Parallel()
	t.Run("RSTP ladder port", func(t *testing.T) {
		t.Parallel()
		l, now := rerootPair(t)
		for range 8 {
			now = now.Add(time.Second)
			l.Receive(now, "p1", rerootBPDU(4096, 20_000))
			l.Receive(now, "p2", rerootBPDU(4096, 40_000))
			l.Advance(now)
		}
		rerootState(t, l.cist(), "p3", bpdu.RoleDesignated, StateForwarding)
		fx := l.LinkChange(now.Add(time.Second), "p1", false, true, 0)
		rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateForwarding)
		rerootState(t, l.cist(), "p3", bpdu.RoleDesignated, StateForwarding)
		if p := l.cist().ports["p3"]; p.agreed || p.synced {
			t.Errorf("p3 agreed=%v synced=%v, want false/false after the worse vector", p.agreed, p.synced)
		}
		rerootAgreement(t, fx, "p2", false, false)
	})
	t.Run("MSTI agreed port loses its agreement", func(t *testing.T) {
		t.Parallel()
		region := MST{Name: "region", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
		l, now := rerootBridge(&region, nil)
		id := region.ConfigID()
		l.Receive(now, "p1", syncMSTBPDU(id, syncMAC(2), 20_000, 20_000, bpdu.RoleDesignated, syncMSTFlags{}))
		l.Receive(now, "p2", syncMSTBPDU(id, syncMAC(2), 40_000, 40_000, bpdu.RoleDesignated, syncMSTFlags{}))
		l.Receive(now, "p3", syncMSTBPDU(id, syncMAC(3), 1_000_000, 1_000_000, bpdu.RoleRoot, syncMSTFlags{agreement: true}))
		tr := l.trees[treeID(1)]
		rerootState(t, tr, "p1", bpdu.RoleRoot, StateForwarding)
		rerootState(t, tr, "p2", bpdu.RoleAlternate, StateDiscarding)
		rerootState(t, tr, "p3", bpdu.RoleDesignated, StateForwarding)
		if !tr.ports["p3"].agreed {
			t.Fatal("fixture p3 has no agreement")
		}
		fx := l.LinkChange(now.Add(time.Second), "p1", false, true, 0)
		rerootState(t, tr, "p2", bpdu.RoleRoot, StateForwarding)
		rerootState(t, tr, "p3", bpdu.RoleDesignated, StateForwarding)
		if p := tr.ports["p3"]; p.agreed || p.synced {
			t.Errorf("MSTI p3 agreed=%v synced=%v, want false/false", p.agreed, p.synced)
		}
		rerootAgreement(t, fx, "p2", true, false)
	})
}

func TestRerootCutsARecentRoot(t *testing.T) {
	t.Parallel()
	l, now := rerootPair(t)
	fx := l.Receive(now.Add(time.Second), "p2", rerootBPDU(0, 0))
	tr := l.cist()
	rerootState(t, tr, "p1", bpdu.RoleDesignated, StateDiscarding)
	rerootState(t, tr, "p2", bpdu.RoleRoot, StateForwarding)
	p := tr.ports["p1"]
	if !p.proposing || !p.synced || p.fwdDelayTimer != now.Add(5*time.Second) {
		t.Errorf("p1 proposing=%v synced=%v ladder=%v, want true/true/%v", p.proposing, p.synced, p.fwdDelayTimer, now.Add(5*time.Second))
	}
	if got := p.recentRootDeadline(now.Add(time.Second)); !got.IsZero() {
		t.Errorf("cut p1 recent-root deadline = %v, want clear", got)
	}
	rerootAgreement(t, fx, "p1", false, false)
}

func TestRerootLeavesAnExpiredRecentRootForwarding(t *testing.T) {
	t.Parallel()
	for _, ageFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "without prior aging", true: "with prior aging"}[ageFirst], func(t *testing.T) {
			t.Parallel()
			l, now := rerootPair(t)
			at := now.Add(12 * time.Second)
			l.Advance(at)
			rerootState(t, l.cist(), "p1", bpdu.RoleDesignated, StateForwarding)
			if l.cist().ports["p1"].synced {
				t.Fatal("p1 synced after leaving Root, want false")
			}
			if got := l.cist().ports["p1"].recentRootDeadline(at); got != at.Add(15*time.Second) {
				t.Errorf("p1 recent-root deadline = %v, want %v", got, at.Add(15*time.Second))
			}
			later := at.Add(15 * time.Second)
			if got := l.cist().ports["p1"].recentRootDeadline(later); !got.IsZero() {
				t.Errorf("expired recent-root deadline = %v, want clear", got)
			}
			if ageFirst {
				l.Advance(later)
			}
			l.Receive(later, "p2", rerootBPDU(0, 0))
			rerootState(t, l.cist(), "p1", bpdu.RoleDesignated, StateForwarding)
			rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateForwarding)
		})
	}
}

func TestRerootDelayChangesPreserveExpiry(t *testing.T) {
	t.Parallel()
	delays := []time.Duration{4 * time.Second, 15 * time.Second, 30 * time.Second}
	for _, mode := range []string{"RSTP", "MSTI", "PVST"} {
		for _, before := range delays {
			for _, after := range delays {
				elapsed := map[time.Duration]bool{}
				for _, boundary := range []time.Duration{before, after} {
					for _, offset := range []time.Duration{-time.Second, 0, time.Second} {
						elapsed[boundary+offset] = true
					}
				}
				for e := range elapsed {
					for _, ageFirst := range []bool{false, true} {
						t.Run(fmt.Sprintf("%s/before=%s/after=%s/elapsed=%s/advance=%v", mode, before, after, e, ageFirst), func(t *testing.T) {
							t.Parallel()
							var region *MST
							var pvst *PVST
							switch mode {
							case "MSTI":
								region = &MST{Name: "region", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
							case "PVST":
								pvst = &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}}}
							}
							l, now := rerootBridge(region, pvst)
							l.forwardDelay = before
							tr := l.cist()
							if region != nil {
								tr = l.trees[treeID(1)]
							} else if pvst != nil {
								tr = l.trees[treeID(10)]
							}
							receive := func(at time.Time, port string, priority uint16, cost uint32, delay time.Duration) {
								b := rerootBPDU(priority, cost)
								if region != nil {
									b = syncMSTBPDU(region.ConfigID(), syncMAC(2), cost, cost, bpdu.RoleDesignated, syncMSTFlags{})
									b.RootID.Priority = priority
									b.RegionalRootID.Priority = priority
									b.MSTIs[0].RegionalRootID.Priority = priority | 1
								}
								b.ForwardDelay = delay
								if pvst != nil {
									b.RootID.Priority |= 10
									if _, outcome := l.ReceiveSSTP(at, port, syncSSTP(10), b); outcome != SSTPApplied {
										t.Fatalf("ReceiveSSTP = %v, want applied", outcome)
									}
									return
								}
								l.Receive(at, port, b)
							}
							receive(now, "p1", 4096, 20_000, before)
							receive(now, "p2", 4096, 40_000, before)
							rerootState(t, tr, "p1", bpdu.RoleRoot, StateForwarding)
							rerootState(t, tr, "p2", bpdu.RoleAlternate, StateDiscarding)
							left := now.Add(12 * time.Second)
							l.Advance(left)
							rerootState(t, tr, "p1", bpdu.RoleDesignated, StateForwarding)
							rerootState(t, tr, "p2", bpdu.RoleDesignated, StateDiscarding)
							if tr.ports["p1"].rootDeadline != left.Add(before) {
								t.Fatalf("rootDeadline = %v, want %v", tr.ports["p1"].rootDeadline, left.Add(before))
							}
							at := left.Add(e)
							if ageFirst {
								l.Advance(at)
								if tr.ports["p2"].state == StateForwarding {
									t.Fatal("fixture p2 is Forwarding before selection")
								}
							}
							receive(at, "p2", 0, 0, after)
							want := StateForwarding
							if e < before {
								want = StateDiscarding
							}
							rerootState(t, tr, "p1", bpdu.RoleDesignated, want)
							rerootState(t, tr, "p2", bpdu.RoleRoot, StateForwarding)
						})
					}
				}
			}
		}
	}
}

func TestRerootClearsAnAgreedRecentRoot(t *testing.T) {
	t.Parallel()
	l, now := rerootPair(t)
	l.Advance(now.Add(12 * time.Second))
	agree := syncBPDU(syncMAC(9), syncMAC(3), 32768, 1_000_000, bpdu.RoleRoot)
	agree.SetAgreement(true)
	l.Receive(now.Add(13*time.Second), "p1", agree)
	if !l.cist().ports["p1"].agreed {
		t.Fatal("fixture p1 has no agreement")
	}
	if got := l.cist().ports["p1"].recentRootDeadline(now.Add(13 * time.Second)); !got.IsZero() {
		t.Errorf("agreed p1 recent-root deadline = %v, want clear", got)
	}
	l.Receive(now.Add(14*time.Second), "p2", rerootBPDU(0, 0))
	rerootState(t, l.cist(), "p1", bpdu.RoleDesignated, StateForwarding)
	rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateForwarding)
}

func rerootBackup(t *testing.T) (*Layer, time.Time) {
	t.Helper()
	l, now := rerootBridge(nil, nil)
	b := syncBPDU(syncMAC(9), syncMAC(9), 32768, 0, bpdu.RoleDesignated)
	l.Receive(now, "p2", b)
	rerootState(t, l.cist(), "p2", bpdu.RoleBackup, StateDiscarding)
	return l, now
}

func TestRerootWaitsForRecentBackup(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		leave func(*Layer, time.Time)
		wait  time.Duration
	}{
		{name: "direct selection", wait: 0},
		{name: "link bounce", leave: func(l *Layer, at time.Time) {
			l.LinkChange(at, "p2", false, true, 0)
			l.LinkChange(at.Add(time.Second), "p2", true, true, 1_000_000_000)
		}, wait: 2 * time.Second},
		{name: "expired through Designated", leave: func(l *Layer, at time.Time) {
			b := syncBPDU(syncMAC(0xf0), syncMAC(9), 61440, 0, bpdu.RoleDesignated)
			b.BridgeID = l.BridgeID()
			l.Receive(at, "p2", b)
		}, wait: 5 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l, now := rerootBackup(t)
			left := now.Add(time.Second)
			if tc.leave != nil {
				tc.leave(l, left)
			}
			at := left.Add(tc.wait)
			b := rerootBPDU(4096, 0)
			b.ForwardDelay = 15 * time.Second
			b.SetProposal(true)
			if tc.wait > 4*time.Second {
				aged := l.Clone()
				aged.Advance(at)
				aged.Receive(at, "p2", b)
				rerootState(t, aged.cist(), "p2", bpdu.RoleRoot, StateForwarding)
			}
			l.Receive(at, "p2", b)
			want := StateDiscarding
			if tc.wait > 4*time.Second {
				want = StateForwarding
			}
			rerootState(t, l.cist(), "p2", bpdu.RoleRoot, want)
			if want == StateForwarding {
				return
			}
			deadline := left.Add(4 * time.Second)
			if got := l.recentBackupDeadline(l.cist(), l.cist().ports["p2"], at); got != deadline {
				t.Errorf("p2 recent-backup deadline = %v, want %v", got, deadline)
			}
			// Suppress periodic frames so the backup deadline is the first wake.
			for _, tx := range l.portTx {
				tx.helloWhen = time.Time{}
				tx.newInfo = false
				tx.newInfoMsti = false
			}
			if wake, ok := l.NextWake(); !ok || wake != deadline {
				t.Errorf("NextWake = %v/%v, want %v/true", wake, ok, deadline)
			}
			l.Advance(deadline.Add(-time.Nanosecond))
			rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateDiscarding)
			l.Advance(deadline)
			rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateForwarding)
		})
	}
}

func TestRerootRecordsBackupOnPointToPointChange(t *testing.T) {
	t.Parallel()
	l, now := rerootBridge(nil, nil)
	l.Receive(now, "p1", rerootBPDU(4096, 0))
	l.LinkChange(now, "p2", true, false, 1_000_000_000)
	b := rerootBPDU(4096, 0)
	b.BridgeID = l.BridgeID()
	l.Receive(now, "p2", b)
	rerootState(t, l.cist(), "p2", bpdu.RoleBackup, StateDiscarding)
	at := now.Add(time.Second)
	l.LinkChange(at, "p2", true, true, 10_000_000_000)
	rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateDiscarding)
	if got := l.recentBackupDeadline(l.cist(), l.cist().ports["p2"], at); got != now.Add(5*time.Second) {
		t.Errorf("p2 recent-backup deadline = %v, want %v", got, now.Add(5*time.Second))
	}
	l.Advance(now.Add(5 * time.Second))
	rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateForwarding)
}

func TestRerootCutsWhileTheBackupDelayRuns(t *testing.T) {
	t.Parallel()
	l, now := rerootBridge(nil, nil)
	l.Receive(now, "p1", rerootBPDU(4096, 0))
	b := syncBPDU(syncMAC(1), syncMAC(9), 4096, 20_000, bpdu.RoleDesignated)
	b.BridgeID = l.BridgeID()
	l.Receive(now, "p2", b)
	rerootState(t, l.cist(), "p1", bpdu.RoleRoot, StateForwarding)
	rerootState(t, l.cist(), "p2", bpdu.RoleBackup, StateDiscarding)
	l.Receive(now.Add(time.Second), "p2", rerootBPDU(0, 0))
	rerootState(t, l.cist(), "p1", bpdu.RoleDesignated, StateDiscarding)
	rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateDiscarding)
	if p := l.cist().ports["p1"]; !p.synced || !p.recentRootDeadline(now.Add(time.Second)).IsZero() {
		t.Errorf("cut p1 synced=%v deadline=%v, want true/clear", p.synced, p.recentRootDeadline(now.Add(time.Second)))
	}
}

func TestRerootOnASharedLinkUsesTheLadder(t *testing.T) {
	t.Parallel()
	l, now := rerootPair(t)
	for range 8 {
		now = now.Add(time.Second)
		l.Receive(now, "p1", rerootBPDU(4096, 20_000))
		l.Advance(now)
	}
	l.LinkChange(now, "p2", true, false, 1_000_000_000)
	l.Receive(now, "p2", rerootBPDU(4096, 40_000))
	l.LinkChange(now.Add(time.Second), "p1", false, true, 0)
	rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateDiscarding)
	if got := l.cist().ports["p2"].fwdDelayTimer; got != now.Add(5*time.Second) {
		t.Errorf("p2 ladder = %v, want %v", got, now.Add(5*time.Second))
	}
}

func TestRerootDoesNotCutWhenTheSelectedRootAlreadyForwards(t *testing.T) {
	t.Parallel()
	l, now := rerootBridge(nil, nil)
	l.Receive(now, "p1", rerootBPDU(4096, 0))
	for range 8 {
		now = now.Add(time.Second)
		l.Receive(now, "p1", rerootBPDU(4096, 0))
		l.Advance(now)
	}
	rerootState(t, l.cist(), "p2", bpdu.RoleDesignated, StateForwarding)
	l.Receive(now.Add(time.Second), "p2", rerootBPDU(0, 0))
	rerootState(t, l.cist(), "p1", bpdu.RoleDesignated, StateForwarding)
	rerootState(t, l.cist(), "p2", bpdu.RoleRoot, StateForwarding)
	if got := l.cist().ports["p1"].rootDeadline; got != now.Add(5*time.Second) {
		t.Errorf("p1 recent-root deadline = %v, want %v", got, now.Add(5*time.Second))
	}
}

func TestRerootUsesTheReceivedForwardDelay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		delay time.Duration
		mst   bool
	}{
		{name: "RSTP/3s", delay: 3 * time.Second},
		{name: "RSTP/5s", delay: 5 * time.Second},
		{name: "MSTP/3s", delay: 3 * time.Second, mst: true},
		{name: "MSTP/5s", delay: 5 * time.Second, mst: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var region *MST
			if tc.mst {
				region = &MST{Name: "region", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
			}
			l, now := rerootBridge(region, nil)
			message := func(priority uint16) bpdu.BPDU {
				if region == nil {
					return rerootBPDU(priority, 0)
				}
				b := syncMSTBPDU(region.ConfigID(), syncMAC(2), 0, 0, bpdu.RoleDesignated, syncMSTFlags{})
				b.RootID.Priority = priority
				b.RegionalRootID.Priority = priority
				return b
			}
			base := message(4096)
			base.ForwardDelay = 15 * time.Second
			l.Receive(now, "p1", base)
			for range 30 {
				now = now.Add(time.Second)
				l.Receive(now, "p1", base)
				l.Advance(now)
			}
			l.LinkChange(now, "p3", false, true, 0)
			l.LinkChange(now, "p3", true, true, 1_000_000_000)
			at := now.Add(time.Second)
			l.Receive(at, "p2", message(2048))
			rerootState(t, l.cist(), "p1", bpdu.RoleDesignated, StateForwarding)
			if got := l.cist().ports["p1"].recentRootDeadline(at); got != at.Add(4*time.Second) {
				t.Errorf("p1 recent-root deadline = %v, want received delay deadline %v", got, at.Add(4*time.Second))
			}
			l.Receive(at.Add(tc.delay), "p3", message(0))
			want := StateDiscarding
			if tc.delay > 4*time.Second {
				want = StateForwarding
			}
			rerootState(t, l.cist(), "p1", bpdu.RoleDesignated, want)
			rerootState(t, l.cist(), "p3", bpdu.RoleRoot, StateForwarding)
		})
	}
}

func TestRerootIsScopedToItsPVSTTree(t *testing.T) {
	t.Parallel()
	l, now := rerootBridge(nil, &PVST{Trees: map[vlan.ID]Tree{1: {}, 10: {}, 20: {}}})
	for _, name := range []string{"p1", "p2", "p3"} {
		l.ReceiveSSTP(now, name, syncSSTP(1), rerootBPDU(61440|1, 0))
	}
	l.ReceiveSSTP(now, "p1", syncSSTP(10), rerootBPDU(4096|10, 20_000))
	l.ReceiveSSTP(now, "p2", syncSSTP(10), rerootBPDU(4096|10, 40_000))
	before := map[string]portState{}
	for name, p := range l.trees[treeID(20)].ports {
		before[name] = *p
	}
	l.ReceiveSSTP(now.Add(time.Second), "p2", syncSSTP(10), rerootBPDU(10, 0))
	rerootState(t, l.trees[treeID(10)], "p1", bpdu.RoleDesignated, StateDiscarding)
	rerootState(t, l.trees[treeID(10)], "p2", bpdu.RoleRoot, StateForwarding)
	for name, want := range before {
		if got := *l.trees[treeID(20)].ports[name]; got != want {
			t.Errorf("VLAN 20 %s changed: got %+v, want %+v", name, got, want)
		}
	}
}

func TestRerootLeavesAnMSTIBoundaryOnTheCISTState(t *testing.T) {
	t.Parallel()
	region := MST{Name: "region", Instances: map[bpdu.MSTID]Instance{1: {VLANs: []vlan.ID{10}}}}
	l, now := rerootBridge(&region, nil)
	id := region.ConfigID()
	l.Receive(now, "p1", syncMSTBPDU(id, syncMAC(2), 20_000, 20_000, bpdu.RoleDesignated, syncMSTFlags{}))
	l.Receive(now, "p2", syncMSTBPDU(id, syncMAC(2), 40_000, 40_000, bpdu.RoleDesignated, syncMSTFlags{}))
	for range 8 {
		now = now.Add(time.Second)
		l.Receive(now, "p1", syncMSTBPDU(id, syncMAC(2), 20_000, 20_000, bpdu.RoleDesignated, syncMSTFlags{}))
		l.Receive(now, "p2", syncMSTBPDU(id, syncMAC(2), 40_000, 40_000, bpdu.RoleDesignated, syncMSTFlags{}))
		l.Advance(now)
	}
	l.Receive(now, "p3", syncBPDU(syncMAC(0xf0), syncMAC(0xf0), 61440, 0, bpdu.RoleDesignated))
	if !l.boundary("p3") {
		t.Fatal("fixture p3 is internal, want boundary")
	}
	rerootState(t, l.cist(), "p3", bpdu.RoleDesignated, StateForwarding)
	rerootState(t, l.trees[treeID(1)], "p3", bpdu.RoleDesignated, StateForwarding)
	before := l.cist().ports["p3"].state
	b := syncMSTBPDU(id, syncMAC(2), 40_000, 0, bpdu.RoleDesignated, syncMSTFlags{})
	b.MSTIs[0].RegionalRootID.Priority = 1
	l.Receive(now.Add(time.Second), "p2", b)
	tr := l.trees[treeID(1)]
	rerootState(t, tr, "p1", bpdu.RoleDesignated, StateDiscarding)
	rerootState(t, tr, "p2", bpdu.RoleRoot, StateForwarding)
	if got := tr.ports["p3"].state; got != before || got != l.cist().ports["p3"].state {
		t.Errorf("boundary MSTI state = %v, want CIST %v unchanged from %v", got, l.cist().ports["p3"].state, before)
	}
}
