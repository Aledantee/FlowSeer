package stp_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

func edgeTestBPDU(t *testing.T, priority uint16, role bpdu.Role, proposal, agreement bool) bpdu.BPDU {
	t.Helper()
	b := legacyConfigBPDU(t, priority, "02:00:00:00:00:0a")
	b.Type = bpdu.TypeRapid
	b.SetRole(role)
	b.SetProposal(proposal)
	b.SetAgreement(agreement)
	return b
}

func TestPVSTOtherTreeCannotScheduleEdgeDelay(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:02"),
		Ports:   map[string]stp.Port{"p1": {AutoEdge: true}},
		PVST:    pvstTrees(nil, 1, 10),
	}, mustPortTable(t, "p1"))
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	l.Receive(start.Add(time.Second), "p1", edgeTestBPDU(t, 61440, bpdu.RoleRoot, false, true))
	if info := l.PortInfo("p1"); info.State != stp.StateForwarding {
		t.Fatalf("CIST port = %+v, want Forwarding", info)
	}
	if info := l.VLANPortInfo(10, "p1"); info.State != stp.StateDiscarding {
		t.Fatalf("VLAN 10 port = %+v, want Discarding", info)
	}
	l.Advance(start.Add(4 * time.Second))
	if wake, ok := l.NextWake(); ok && wake.Before(start.Add(4*time.Second)) {
		t.Fatalf("NextWake after CIST edge delay = %v, want no earlier than 4 seconds", wake)
	}
	for i := range 5 {
		wake, ok := l.NextWake()
		if !ok {
			t.Fatal("NextWake reported no timer")
		}
		l.Advance(wake)
		if next, ok := l.NextWake(); ok && !next.After(wake) {
			t.Fatalf("wake %d at %v followed by %v", i, wake, next)
		}
	}
}

func TestRepeatedProposalDoesNotRestartOtherPortsEdgeDelay(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {AutoEdge: true}},
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	l.LinkChange(start, "p2", true, true, 1_000_000_000)
	proposal := edgeTestBPDU(t, 4096, bpdu.RoleDesignated, true, false)
	l.Receive(start.Add(time.Second), "p1", proposal)
	l.Receive(start.Add(2*time.Second), "p1", proposal)
	l.Advance(start.Add(3 * time.Second))
	if info := l.PortInfo("p2"); !info.Edge {
		t.Fatalf("p2 = %+v, want edge at link-up plus 3 seconds", info)
	}
}

func TestNonCISTProposalCannotRestartEdgeDelay(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {AutoEdge: true}},
		PVST:    pvstTrees(nil, 1, 10),
	}, mustPortTable(t, "p1", "p2"))
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	l.LinkChange(start, "p2", true, true, 1_000_000_000)
	agreement := edgeTestBPDU(t, 61440, bpdu.RoleRoot, false, true)
	l.ReceiveSSTP(start.Add(time.Second), "p2", stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, agreement)
	if info := l.VLANPortInfo(10, "p2"); info.State != stp.StateForwarding {
		t.Fatalf("VLAN 10 before cut = %+v", info)
	}
	proposal := edgeTestBPDU(t, 4096, bpdu.RoleDesignated, true, false)
	l.ReceiveSSTP(start.Add(5*time.Second), "p1", stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, proposal)
	if info := l.VLANPortInfo(10, "p2"); info.State != stp.StateDiscarding {
		t.Fatalf("VLAN 10 after cut = %+v, want Discarding", info)
	}
	if info := l.PortInfo("p2"); info.State != stp.StateDiscarding {
		t.Fatalf("CIST after VLAN 10 cut = %+v, want Discarding", info)
	}
	l.Advance(start.Add(5 * time.Second))
	if info := l.PortInfo("p2"); !info.Edge {
		t.Fatalf("CIST after overdue edge delay = %+v, want edge", info)
	}
	for i := range 8 {
		wake, ok := l.NextWake()
		if !ok {
			break
		}
		l.Advance(wake)
		if next, ok := l.NextWake(); ok && !next.After(wake) {
			t.Fatalf("wake %d at %v followed by %v", i, wake, next)
		}
	}

	agreed := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:02"),
		Ports:   map[string]stp.Port{"p1": {}, "p2": {AutoEdge: true}},
		PVST:    pvstTrees(nil, 1, 10),
	}, mustPortTable(t, "p1", "p2"))
	agreed.LinkChange(start, "p1", true, true, 1_000_000_000)
	agreed.LinkChange(start, "p2", true, true, 1_000_000_000)
	agreed.Receive(start.Add(time.Second), "p2", agreement)
	agreed.ReceiveSSTP(start.Add(time.Second), "p2", stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, agreement)
	if info := agreed.PortInfo("p2"); info.State != stp.StateForwarding {
		t.Fatalf("agreed CIST port = %+v, want Forwarding", info)
	}
	agreed.Advance(start.Add(5 * time.Second))
	agreed.ReceiveSSTP(start.Add(5*time.Second), "p1", stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, proposal)
	agreed.Advance(start.Add(8 * time.Second))
	if next, ok := agreed.NextWake(); ok && !next.After(start.Add(8*time.Second)) {
		t.Fatalf("agreed CIST after VLAN 10 cut left wake %v", next)
	}
}

func TestMcheckDoesNotExposeExpiredEdgeDelay(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	l := mustNewSTP(t, stp.Config{
		Address: mustMAC(t, "02:00:00:00:00:02"),
		Ports:   map[string]stp.Port{"p1": {AutoEdge: true}},
	}, mustPortTable(t, "p1"))
	l.LinkChange(start, "p1", true, true, 1_000_000_000)
	legacy := legacyConfigBPDU(t, 61440, "02:00:00:00:00:0a")
	l.Receive(start.Add(4*time.Second), "p1", legacy)
	l.Mcheck(start.Add(8*time.Second), "p1")
	if wake, ok := l.NextWake(); ok && wake.Before(start.Add(8*time.Second)) {
		t.Fatalf("NextWake after Mcheck = %v, want no earlier than Mcheck", wake)
	}
}

func TestEdgeDelayCallSequences(t *testing.T) {
	for _, mode := range []string{"rstp", "mstp", "pvst"} {
		for _, seed := range []uint64{1, 7, 29, 101} {
			t.Run(fmt.Sprintf("%s/%d", mode, seed), func(t *testing.T) {
				start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
				cfg := stp.Config{
					Address: mustMAC(t, "02:00:00:00:00:02"),
					Ports:   map[string]stp.Port{"p1": {}, "p2": {AutoEdge: true}, "quiet": {AutoEdge: true}},
				}
				if mode == "mstp" {
					cfg.MST = &stp.MST{Name: "region", Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{10}}}}
				}
				if mode == "pvst" {
					cfg.PVST = pvstTrees(nil, 1, 10)
				}
				l := mustNewSTP(t, cfg, mustPortTable(t, "p1", "p2", "quiet"))
				rng := rand.New(rand.NewPCG(seed, seed+11))
				var calls []string
				fail := func(format string, args ...any) {
					t.Fatalf("seed=%d mode=%s: %s\ncalls:\n%s", seed, mode, fmt.Sprintf(format, args...), strings.Join(calls, "\n"))
				}
				log := func(at time.Time, operation string) {
					calls = append(calls, fmt.Sprintf("%s %s", at.Sub(start), operation))
				}
				log(start, "LinkChange quiet up point-to-point")
				l.LinkChange(start, "quiet", true, true, 1_000_000_000)
				now := start
				quietChecked := false
				for step := range 320 {
					wake, hasWake := l.NextWake()
					if hasWake && wake.Before(now) {
						fail("step %d NextWake %v before %v", step, wake, now)
					}
					next := now.Add(time.Duration(rng.IntN(200)+50) * time.Millisecond)
					if hasWake && !wake.After(next) {
						now = wake
						log(now, "Advance at NextWake")
						l.Advance(now)
						if later, ok := l.NextWake(); ok && !later.After(now) {
							fail("step %d Advance at wake %v left wake %v", step, now, later)
						}
					} else {
						now = next
						port := []string{"p1", "p2"}[rng.IntN(2)]
						switch operation := rng.IntN(9); operation {
						case 0, 1:
							up, pointToPoint := rng.IntN(4) != 0, rng.IntN(2) == 0
							log(now, fmt.Sprintf("LinkChange %s up=%t point-to-point=%t", port, up, pointToPoint))
							l.LinkChange(now, port, up, pointToPoint, 1_000_000_000)
						case 2, 3, 4:
							role := []bpdu.Role{bpdu.RoleDesignated, bpdu.RoleRoot}[rng.IntN(2)]
							priority := []uint16{4096, 61440}[rng.IntN(2)]
							proposal, agreement := rng.IntN(2) == 0, rng.IntN(2) == 0
							b := edgeTestBPDU(t, priority, role, proposal, agreement)
							log(now, fmt.Sprintf("Receive %s RST priority=%d role=%v proposal=%t agreement=%t", port, priority, role, proposal, agreement))
							l.Receive(now, port, b)
						case 5:
							b := legacyConfigBPDU(t, 4096, "02:00:00:00:00:0a")
							if rng.IntN(2) == 0 {
								b.Type = bpdu.TypeTopologyChangeNotification
							}
							log(now, fmt.Sprintf("Receive %s legacy type=%v", port, b.Type))
							l.Receive(now, port, b)
						case 6:
							log(now, fmt.Sprintf("Mcheck %s", port))
							l.Mcheck(now, port)
						case 7:
							switch mode {
							case "mstp":
								b := edgeTestBPDU(t, 4096, bpdu.RoleDesignated, true, false)
								cid := cfg.MST.ConfigID()
								b.Version, b.ConfigID, b.RegionalRootID, b.RemainingHops = 3, &cid, b.RootID, 20
								b.MSTIs = []bpdu.MSTIRecord{{MSTID: 1, RegionalRootID: b.RootID, RemainingHops: 20}}
								log(now, fmt.Sprintf("Receive %s MST with MSTI", port))
								l.Receive(now, port, b)
							case "pvst":
								b := edgeTestBPDU(t, 4096, bpdu.RoleDesignated, true, false)
								log(now, fmt.Sprintf("ReceiveSSTP %s VLAN 10", port))
								l.ReceiveSSTP(now, port, stp.SSTPArrival{ArrivalVID: 10, TLVVID: 10, Admitted: true}, b)
							default:
								log(now, "Advance")
								l.Advance(now)
							}
						case 8:
							log(now, "Advance")
							l.Advance(now)
						}
					}
					if next, ok := l.NextWake(); ok && next.Before(now) {
						fail("step %d NextWake %v before %v", step, next, now)
					}
					if !quietChecked && !now.Before(start.Add(3*time.Second)) {
						log(now, "Advance at or after quiet edge delay")
						l.Advance(now)
						if info := l.PortInfo("quiet"); !info.Edge || info.Role != bpdu.RoleDesignated {
							fail("quiet port at first Advance after edge delay = %+v", info)
						}
						quietChecked = true
					}
				}
				if !quietChecked {
					fail("quiet port never reached edge delay")
				}
			})
		}
	}
}
