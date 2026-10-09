package vswitch_test

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestBPDUOnUntrackedUpPortIsDropped(t *testing.T) {
	sw := newAutoSTPSwitch(t, map[string]stp.Port{"1/1/1": {}})
	sw.Start(fixedTime)
	b := bpdu.BPDU{Type: bpdu.TypeRapid, RootID: bpdu.BridgeID{Priority: 4096, Address: macH1}, BridgeID: bpdu.BridgeID{Priority: 4096, Address: macH1}, PortID: 0x8001, HelloTime: stp.DefaultHelloTime, MaxAge: stp.DefaultMaxAge, ForwardDelay: stp.DefaultForwardDelay}
	f := mustEncode(t, b, macH1)
	for _, out := range []device.Output{sw.Peek(fixedTime, "1/1/3", f), sw.Receive(fixedTime, "1/1/3", f)} {
		if out.Outcome != trace.Dropped || out.Reason != port.ReasonPortDown || !traceHasRuleID(out.Steps, stp.RuleStatusDown) {
			t.Fatalf("untracked BPDU = %+v, want port-down under stp status rule", out)
		}
	}
}

func TestBPDURecordsEveryChangedTree(t *testing.T) {
	for _, scenario := range []string{"msti-only", "vlan-one-in-msti", "shared-msti", "msti-outside-table", "msti-vlan-unaware"} {
		t.Run(scenario, func(t *testing.T) {
			vids := []vlan.ID{10}
			if scenario == "vlan-one-in-msti" {
				vids = []vlan.ID{1}
			}
			if scenario == "shared-msti" {
				vids = []vlan.ID{10, 20}
			}
			region := &stp.MST{Name: "trace-region", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{1: {VLANs: vids}}}
			table := map[vlan.ID]string{1: "native", 10: "ten", 20: "twenty"}
			cfg := vswitch.Config{MAC: macRouter, Ports: mustTable(t, port.NewBuilder().Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})), Bridge: &bridge.Config{VLAN: &bridge.VLAN{Table: table, Switchports: map[string]bridge.Switchport{"p1": {Tagged: []vlan.ID{1, 10, 20}}}}}, STP: &stp.Config{Address: macRouter, Priority: 32768, Ports: map[string]stp.Port{"p1": {}}, MST: region}}
			if scenario == "msti-outside-table" {
				delete(table, 10)
				cfg.Bridge.VLAN.Switchports["p1"] = bridge.Switchport{Tagged: []vlan.ID{1, 20}}
			}
			if scenario == "msti-vlan-unaware" {
				cfg.Bridge.VLAN = nil
			}

			sw := mustSwitch(t, cfg)
			sw.Start(fixedTime)
			beforeCIST := sw.Roles()["p1"]
			beforeMSTI := sw.TreeRoles()[vids[0]]["p1"]
			if beforeMSTI.Role != bpdu.RoleDesignated {
				t.Fatalf("before MSTI = %+v, want Designated", beforeMSTI)
			}
			id := region.ConfigID()
			cistPriority := uint16(61440)
			if scenario != "msti-only" {
				cistPriority = 4096
			}
			b := bpdu.BPDU{Version: 3, Type: bpdu.TypeRapid, RootID: bpdu.BridgeID{Priority: cistPriority, Address: macH1}, BridgeID: bpdu.BridgeID{Priority: cistPriority, Address: macH1}, RegionalRootID: bpdu.BridgeID{Priority: cistPriority, Address: macH1}, ConfigID: &id, RemainingHops: 20, PortID: 0x8001, HelloTime: stp.DefaultHelloTime, MaxAge: stp.DefaultMaxAge, ForwardDelay: stp.DefaultForwardDelay, MSTIs: []bpdu.MSTIRecord{{MSTID: 1, RegionalRootID: bpdu.BridgeID{Priority: 4097, Address: macH1}, BridgePriority: 0x10, PortPriority: 128, RemainingHops: 20}}}
			b.SetRole(bpdu.RoleDesignated)
			b.MSTIs[0].Flags = b.Flags
			out := sw.Receive(fixedTime, "p1", mustEncode(t, b, macH1))
			afterCIST := sw.Roles()["p1"]
			afterMSTI := sw.TreeRoles()[vids[0]]["p1"]
			if afterMSTI.Role != bpdu.RoleRoot {
				t.Fatalf("after MSTI = %+v, want Root", afterMSTI)
			}
			if scenario == "msti-only" && beforeCIST.Role != afterCIST.Role {
				t.Fatalf("CIST role changed: %v to %v", beforeCIST.Role, afterCIST.Role)
			}
			if scenario == "vlan-one-in-msti" && beforeCIST.Role == afterCIST.Role {
				t.Fatal("CIST role did not change")
			}
			var outputs []trace.Fact
			for _, step := range out.Steps {
				if step.RuleID == stp.RuleBPDUAdmit {
					outputs = step.Outputs
				}
			}
			want := []trace.Fact{stp.PortTransitionFact("p1", "bpdu", beforeMSTI, afterMSTI)}
			if beforeCIST.Role != afterCIST.Role || beforeCIST.State != afterCIST.State {
				want = append(want, stp.PortTransitionFact("p1", "bpdu", beforeCIST, afterCIST))
			}
			for _, fact := range want {
				count := 0
				for _, got := range outputs {
					if trace.EqualFact(got, fact) {
						count++
					}
				}
				if count != 1 {
					t.Errorf("transition %s occurs %d times, want 1: %+v", fact.Canonical(), count, outputs)
				}
			}
			if len(outputs) != 1+len(want) {
				t.Errorf("outputs = %d, want decision plus %d transitions", len(outputs), len(want))
			}
		})
	}
}

// TestBPDURecordsTreeTransitionsInVLANOrder proves a BPDU that moves several
// instances lists their transitions in the order of the VLANs that name them,
// not the instance numbers or the iteration order of a map.
func TestBPDURecordsTreeTransitionsInVLANOrder(t *testing.T) {
	// The instance numbers run opposite to the VLAN ids they carry.
	instances := map[bpdu.MSTID]stp.Instance{1: {VLANs: []vlan.ID{40}}, 2: {VLANs: []vlan.ID{30}}, 3: {VLANs: []vlan.ID{20}}, 4: {VLANs: []vlan.ID{10}}}
	region := &stp.MST{Name: "order-region", Revision: 1, Instances: instances}
	table := map[vlan.ID]string{1: "native", 10: "ten", 20: "twenty", 30: "thirty", 40: "forty"}
	cfg := vswitch.Config{
		MAC:   macRouter,
		Ports: mustTable(t, port.NewBuilder().Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{Table: table, Switchports: map[string]bridge.Switchport{
			"p1": {Tagged: []vlan.ID{1, 10, 20, 30, 40}},
		}}},
		STP: &stp.Config{Address: macRouter, Priority: 32768, Ports: map[string]stp.Port{"p1": {}}, MST: region},
	}
	sw := mustSwitch(t, cfg)
	sw.Start(fixedTime)
	before := sw.TreeRoles()

	id := region.ConfigID()
	b := bpdu.BPDU{Version: 3, Type: bpdu.TypeRapid, RootID: bpdu.BridgeID{Priority: 61440, Address: macH1}, BridgeID: bpdu.BridgeID{Priority: 61440, Address: macH1}, RegionalRootID: bpdu.BridgeID{Priority: 61440, Address: macH1}, ConfigID: &id, RemainingHops: 20, PortID: 0x8001, HelloTime: stp.DefaultHelloTime, MaxAge: stp.DefaultMaxAge, ForwardDelay: stp.DefaultForwardDelay}
	b.SetRole(bpdu.RoleDesignated)
	for mstid := bpdu.MSTID(1); mstid <= 4; mstid++ {
		b.MSTIs = append(b.MSTIs, bpdu.MSTIRecord{MSTID: mstid, RegionalRootID: bpdu.BridgeID{Priority: 4097, Address: macH1}, BridgePriority: 0x10, PortPriority: 128, RemainingHops: 20, Flags: b.Flags})
	}
	out := sw.Receive(fixedTime, "p1", mustEncode(t, b, macH1))
	after := sw.TreeRoles()

	var outputs []trace.Fact
	for _, step := range out.Steps {
		if step.RuleID == stp.RuleBPDUAdmit {
			outputs = step.Outputs
		}
	}
	var want []trace.Fact
	for _, vid := range []vlan.ID{10, 20, 30, 40} {
		if before[vid]["p1"].Role == after[vid]["p1"].Role && before[vid]["p1"].State == after[vid]["p1"].State {
			t.Fatalf("VLAN %d tree did not move: %+v", vid, after[vid]["p1"])
		}
		want = append(want, stp.PortTransitionFact("p1", "bpdu", before[vid]["p1"], after[vid]["p1"]))
	}
	if len(outputs) != 1+len(want) {
		t.Fatalf("outputs = %d, want the decision and %d transitions", len(outputs), len(want))
	}
	for i, fact := range want {
		if !trace.EqualFact(outputs[1+i], fact) {
			t.Errorf("transition %d = %s, want %s", i, outputs[1+i].Canonical(), fact.Canonical())
		}
	}
}

func TestBPDUConsumedWithEmissionDrained(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
		Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{Name: "control", SelectAll: true, OutputPort: "1/1/3"}}},
	}

	sw := mustSwitch(t, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetProposal(true)

	frame := mustEncode(t, b, macRoot)

	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", frame))
	if res.Outcome != trace.Consumed {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Consumed)
	}
	if len(res.Steps) != 2 || res.Steps[0].Layer != stp.LayerName || res.Steps[0].Op != trace.OpClassify {
		t.Errorf("res.Steps = %+v, want spanning-tree classification followed by the mirror decision", res.Steps)
	}
	if !traceHasFactType(res.Steps, "stp.bpdu_decision") {
		t.Errorf("BPDU trace has no spanning-tree decision fact: %+v", res.Steps)
	}
	if step, ok := mirrorCopyStep(res.Steps); !ok || step.Op != trace.OpReplicate {
		t.Errorf("BPDU trace has no admitted mirror decision: %+v", res.Steps)
	}
	if copies := takeCopies(callOutputs, sw); len(copies) != 1 || copies[0].Mirror != "control" || copies[0].Port != "1/1/3" {
		t.Errorf("output copies = %+v, want received BPDU mirrored to 1/1/3", copies)
	}

	emissions := takeEmissions(callOutputs, sw)
	if len(emissions) == 0 {
		t.Fatalf("output emissions returned 0 emissions, want at least 1")
	}
}

func TestBPDUOnLAGMemberConsumedOnLAG(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"lag1":  {},
				"1/1/3": {},
			},
		},
	}

	sw := mustSwitch(t, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
	}
	b.SetRole(bpdu.RoleDesignated)

	frame := mustEncode(t, b, macRoot)

	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", frame))
	if res.Outcome != trace.Consumed {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Consumed)
	}
	if res.Ingress != "lag1" {
		t.Errorf("res.Ingress = %q, want %q", res.Ingress, "lag1")
	}
}

func TestPeekLeavesSTPLayerUntouched(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	sw := mustSwitch(t, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetProposal(true)

	frame := mustEncode(t, b, macRoot)

	rolesBefore := sw.Roles()
	res := sw.Peek(now, "1/1/1", frame)
	if res.Outcome != trace.Consumed {
		t.Fatalf("Peek outcome = %q, want %q", res.Outcome, trace.Consumed)
	}

	if len(res.Emissions) != 0 {
		t.Errorf("Peek generated emissions: %+v, want none", res.Emissions)
	}

	roles := sw.Roles()
	if roles["1/1/1"].Role == bpdu.RoleRoot {
		t.Errorf("Peek modified role of 1/1/1 to Root")
	}
	if !reflect.DeepEqual(roles, rolesBefore) {
		t.Errorf("roles after Peek = %+v, want %+v", roles, rolesBefore)
	}
}

func TestSwitchTreeRoles(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	// 1. Switch without STP returns nil.
	swNoSTP := mustSwitch(t, vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
	})
	if got := swNoSTP.TreeRoles(); got != nil {
		t.Fatalf("swNoSTP.TreeRoles() = %v, want nil", got)
	}

	// 2. Switch with plain RSTP returns VLAN 1.
	swRSTP := mustSwitch(t, vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	})
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, swRSTP, swRSTP.Start(now))
	takeEmissions(callOutputs, swRSTP)

	treeRolesRSTP := swRSTP.TreeRoles()
	if treeRolesRSTP == nil {
		t.Fatalf("swRSTP.TreeRoles() = nil, want map with VLAN 1")
	}
	if _, ok := treeRolesRSTP[1]; !ok {
		t.Fatalf("swRSTP.TreeRoles() missing VLAN 1: %+v", treeRolesRSTP)
	}
	if info, ok := treeRolesRSTP[1]["1/1/1"]; !ok || info.Role != bpdu.RoleDesignated {
		t.Errorf("swRSTP.TreeRoles()[1][\"1/1/1\"] = %+v, want designated role", info)
	}

	// 3. Switch with MST returns configured instance VLANs.
	swMST := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "VLAN10", 20: "VLAN20"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Tagged: []vlan.ID{10, 20}},
					"1/1/2": {Tagged: []vlan.ID{10, 20}},
				},
			},
		},
		STP: &stp.Config{
			Priority: 32768,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
			MST: &stp.MST{
				Name: "region-1",
				Instances: map[bpdu.MSTID]stp.Instance{
					1: {VLANs: []vlan.ID{10}},
					2: {VLANs: []vlan.ID{20}},
				},
			},
		},
	})
	captureOutput(callOutputs, swMST, swMST.Start(now))
	takeEmissions(callOutputs, swMST)
	treeRolesMST := swMST.TreeRoles()
	if treeRolesMST == nil {
		t.Fatalf("swMST.TreeRoles() = nil, want per-VLAN roles")
	}
	for _, vid := range []vlan.ID{1, 10, 20} {
		if _, ok := treeRolesMST[vid]; !ok {
			t.Errorf("swMST.TreeRoles() missing VID %d: %+v", vid, treeRolesMST)
		}
	}
}

func TestUndecodableBPDUIncrementsBadBPDUs(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	sw := mustSwitch(t, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	badFrame := ethernet.Frame{
		Dst:     netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
		Src:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02},
		Payload: []byte{0x01, 0x02, 0x03},
	}

	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", badFrame))
	if res.Outcome != trace.Dropped {
		t.Fatalf("Receive outcome = %q, want %q", res.Outcome, trace.Dropped)
	}
	if res.Reason != stp.ReasonUnsupportedBPDU {
		t.Errorf("Receive reason = %q, want %q", res.Reason, stp.ReasonUnsupportedBPDU)
	}
	if got := sw.Roles()["1/1/1"].BadBPDUs; got != 1 {
		t.Fatalf("Roles()[\"1/1/1\"].BadBPDUs = %d, want 1", got)
	}

	resPeek := sw.Peek(now, "1/1/1", badFrame)
	if resPeek.Outcome != trace.Dropped {
		t.Fatalf("Peek outcome = %q, want %q", resPeek.Outcome, trace.Dropped)
	}
	if resPeek.Reason != stp.ReasonUnsupportedBPDU {
		t.Errorf("Peek reason = %q, want %q", resPeek.Reason, stp.ReasonUnsupportedBPDU)
	}
	if got := sw.Roles()["1/1/1"].BadBPDUs; got != 1 {
		t.Errorf("Roles()[\"1/1/1\"].BadBPDUs after Peek = %d, want 1", got)
	}
}

func TestSwitchMigrationToLegacySTPAndMcheck(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	sw := mustSwitch(t, cfg)
	t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(t0))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Advance(t0.Add(2*time.Second)))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Advance(t0.Add(4*time.Second)))
	takeEmissions(callOutputs, sw)

	inferiorBridgeID := bpdu.BridgeID{
		Priority: 61440,
		Address:  netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c},
	}
	inferiorBPDU := bpdu.BPDU{
		Type:         bpdu.TypeConfiguration,
		RootID:       inferiorBridgeID,
		BridgeID:     inferiorBridgeID,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	frame := mustEncode(t, inferiorBPDU, inferiorBridgeID.Address)
	captureOutput(callOutputs, sw, sw.Receive(t0.Add(4*time.Second), "1/1/1", frame))

	emissions := takeEmissions(callOutputs, sw)
	if len(emissions) == 0 {
		t.Fatal("output emissions returned 0 emissions, want reply emission")
	}

	var replyEmission *device.Emission
	for i := range emissions {
		if emissions[i].Port == "1/1/1" {
			replyEmission = &emissions[i]
			break
		}
	}
	if replyEmission == nil {
		t.Fatalf("no emission found on port 1/1/1: %+v", emissions)
	}

	replyBPDU, err := bpdu.Decode(replyEmission.Frame)
	if err != nil {
		t.Fatalf("bpdu.Decode(replyEmission.Frame) failed: %v", err)
	}
	if replyBPDU.Type != bpdu.TypeConfiguration {
		t.Errorf("replyBPDU.Type = %v, want %v (Configuration)", replyBPDU.Type, bpdu.TypeConfiguration)
	}
	if sw.Roles()["1/1/1"].SendRSTP {
		t.Errorf("Roles()[\"1/1/1\"].SendRSTP = true, want false")
	}
	captureOutput(callOutputs, sw, sw.Mcheck(t0.Add(10*time.Second), "1/1/1"))
	if !sw.Roles()["1/1/1"].SendRSTP {
		t.Errorf("Roles()[\"1/1/1\"].SendRSTP after Mcheck = false, want true")
	}
}

// TestBPDUOnDownPortIsDropped proves that the intercept applies the
// relay's port check: a BPDU on a port that is not up never reaches the
// layer, and its trace says so instead of Consumed.
func TestBPDUOnDownPortIsDropped(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	sw := mustSwitch(t, vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
		},
	})
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	b := bpdu.BPDU{
		RootID:    bpdu.BridgeID{Priority: 4096, Address: macRoot},
		BridgeID:  bpdu.BridgeID{Priority: 4096, Address: macRoot},
		PortID:    0x8001,
		HelloTime: stp.DefaultHelloTime,
		MaxAge:    stp.DefaultMaxAge,
	}
	b.SetRole(bpdu.RoleDesignated)

	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", mustEncode(t, b, macRoot)))
	if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
		t.Fatalf("BPDU on down port = %s/%s, want Dropped/%s", res.Outcome, res.Reason, port.ReasonPortDown)
	}
	step, ok := findStep(res.Steps, trace.RuleID("port.status.down"))
	if !ok {
		t.Fatalf("no literal port.status.down step in trace: %+v", res.Steps)
	}
	if step.Layer != stp.LayerName {
		t.Errorf("step layer = %v, want %v", step.Layer, stp.LayerName)
	}
	if root, _, _ := sw.Root(); root.Address == macRoot {
		t.Error("layer adopted a root from a BPDU on a down port")
	}
}

func TestSSTPOnDownPortIsDropped(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	p10 := vlan.ID(10)
	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &p10, Untagged: []vlan.ID{10}},
				},
			},
		},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
		},
	})
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	frame := pvstSSTPFrame(t, 10, 10, peer)
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", frame))
	if res.Outcome != trace.Dropped || res.Reason != port.ReasonPortDown {
		t.Fatalf("SSTP on down port = %s/%s, want Dropped/%s", res.Outcome, res.Reason, port.ReasonPortDown)
	}
	step, ok := findStep(res.Steps, trace.RuleID("port.status.down"))
	if !ok {
		t.Fatalf("no literal port.status.down step in trace: %+v", res.Steps)
	}
	if step.Layer != stp.LayerName {
		t.Errorf("step layer = %v, want %v", step.Layer, stp.LayerName)
	}
}

func TestForwardBPDUWithSpanningTree(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	macSelf := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01}
	macRoot := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			ForwardBPDU: true,
		},
		STP: &stp.Config{
			Priority: 32768,
			Address:  macSelf,
			Ports: map[string]stp.Port{
				"1/1/1": {AdminEdge: true},
				"1/1/2": {AdminEdge: true},
				"1/1/3": {AdminEdge: true},
			},
		},
	}

	sw := mustSwitch(t, cfg)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    stp.DefaultHelloTime,
		MaxAge:       stp.DefaultMaxAge,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetProposal(true)

	bpduFrame := mustEncode(t, b, macRoot)
	resBPDU := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", bpduFrame))
	if resBPDU.Outcome != trace.Consumed {
		t.Errorf("BPDU outcome = %s, want Consumed", resBPDU.Outcome)
	}

	otherReserved := netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e}
	otherFrame := ethernet.Frame{
		Dst: otherReserved,
		Src: macRoot,
	}
	resOther := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", otherFrame))
	if resOther.Outcome != trace.Flooded {
		t.Errorf("reserved frame outcome = %s, want Flooded", resOther.Outcome)
	}
	if len(resOther.Egress) != 2 || resOther.Egress[0].Port != "1/1/2" || resOther.Egress[1].Port != "1/1/3" {
		t.Errorf("reserved frame egress = %+v, want flooded to 1/1/2 and 1/1/3", resOther.Egress)
	}
}

// switchCable names a link between two switches in a convergence test.
type switchCable struct {
	swA, swB     int
	portA, portB string
}

// convergeSwitches starts every switch, then alternates delivering the BPDU
// frames each emits to its cabled peer and advancing every switch's earliest
// NextWake, until two consecutive rounds report the same snapshot with an
// empty delivery queue. snapshot renders whatever a test needs to see
// stabilize; the scheduler does not look inside it. It mirrors stp's own
// convergeLayers, driving the same state machine one layer further out
// through vswitch.Switch's BPDU framing and port table.
func convergeSwitches(t *testing.T, start time.Time, switches []*vswitch.Switch, cables []switchCable, snapshot func() string) time.Time {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Helper()

	now := start

	findPeer := func(srcSw int, srcPort string) (int, string, bool) {
		for _, c := range cables {
			if c.swA == srcSw && c.portA == srcPort {
				return c.swB, c.portB, true
			}
			if c.swB == srcSw && c.portB == srcPort {
				return c.swA, c.portA, true
			}
		}
		return 0, "", false
	}

	type packet struct {
		targetSw   int
		targetPort string
		frame      ethernet.Frame
	}

	var queue []packet
	drain := func(srcSw int) {
		for _, em := range takeEmissions(callOutputs, switches[srcSw]) {
			if peerSw, peerPort, ok := findPeer(srcSw, em.Port); ok {
				queue = append(queue, packet{targetSw: peerSw, targetPort: peerPort, frame: em.Frame})
			}
		}
	}

	for i, sw := range switches {
		captureOutput(callOutputs, sw, sw.Start(now))
		drain(i)
	}

	var prevSnapshot string
	stableRounds := 0

	for round := 0; round < 400; round++ {
		current := snapshot()
		if current == prevSnapshot {
			stableRounds++
			if stableRounds >= 2 && len(queue) == 0 {
				break
			}
		} else {
			stableRounds = 0
			prevSnapshot = current
		}

		if len(queue) > 0 {
			batch := queue
			queue = nil
			for _, pkt := range batch {
				captureOutput(callOutputs, switches[pkt.targetSw], switches[pkt.targetSw].Receive(now, pkt.targetPort, pkt.frame))
				drain(pkt.targetSw)
			}

			continue
		}

		var earliestWake time.Time
		hasWake := false
		for _, sw := range switches {
			if w, ok := sw.NextWake(); ok {
				if !hasWake || w.Before(earliestWake) {
					earliestWake = w
					hasWake = true
				}
			}
		}
		if !hasWake {
			break
		}

		now = earliestWake
		for i, sw := range switches {
			captureOutput(callOutputs, sw, sw.Advance(now))
			drain(i)
		}
	}

	return now
}

// TestMSTITopologyChangeFlushesOnlyItsOwnVLAN proves end to end that a topology change confined to one MST instance flushes, on the
// switch's other ports, only the FIDs of the VLANs that instance carries,
// leaving a different VLAN's entries on the same port untouched. It runs
// through vswitch.Switch rather than stp.Layer directly, so it exercises the
// stp.FlushTarget-to-bridge.FlushTarget translation in applySTPEffects and
// the FID filtering in Bridge.Flush, not just the layer's own Effects.
//
// sw2's MSTI 1 carries an inflated path cost on l1 (its own instance
// override, same shape as stp's TestMSTInstancesSelectIndependentRoots), so
// once the two switches converge, MSTI 1's root port is l2 while the CIST
// and MSTI 2 both keep l1 (equal cost on both links resolves to the lower
// port ID). Failing l2 causes MSTI 1's alternate port on l1 to start forwarding
// as the new root port, raising a topology change on MSTI 1 alone that propagates
// to active port p3. The CIST and MSTI 2 already forwarded on l1, so neither transitions.
func TestMSTITopologyChangeFlushesOnlyItsOwnVLAN(t *testing.T) {
	start := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	mac1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	mac2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	region := func(instances map[bpdu.MSTID]stp.Instance) *stp.MST {
		return &stp.MST{Name: "region-1", Revision: 1, Instances: instances}
	}
	pvid := vlan.ID(10)
	trunk := bridge.Switchport{PVID: &pvid, Tagged: []vlan.ID{10, 20}}
	vlanTable := map[vlan.ID]string{10: "ten", 20: "twenty"}

	sw1 := mustSwitch(t, vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "l1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "l2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		MAC: mac1,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       vlanTable,
			Switchports: map[string]bridge.Switchport{"l1": trunk, "l2": trunk},
		}},
		STP: &stp.Config{
			Priority: 4096,
			Address:  mac1,
			Ports:    map[string]stp.Port{"l1": {}, "l2": {}},
			MST: region(map[bpdu.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10}},
				2: {VLANs: []vlan.ID{20}},
			}),
		},
	})

	sw2 := mustSwitch(t, vswitch.Config{
		Ports: mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "l1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "l2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "p3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
		MAC: mac2,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table:       vlanTable,
			Switchports: map[string]bridge.Switchport{"l1": trunk, "l2": trunk, "p3": trunk},
		}},
		STP: &stp.Config{
			Priority: 32768,
			Address:  mac2,
			Ports:    map[string]stp.Port{"l1": {}, "l2": {}, "p3": {}},
			MST: region(map[bpdu.MSTID]stp.Instance{
				1: {VLANs: []vlan.ID{10}, Ports: map[string]stp.InstancePort{"l1": {PathCost: 200_000}}},
				2: {VLANs: []vlan.ID{20}},
			}),
		},
	})
	if _, err := sw2.LinkChange(start, "p3", port.Up, device.PointToPointTrue, 1_000_000_000); err != nil {
		t.Fatal(err)
	}

	switches := []*vswitch.Switch{sw1, sw2}
	cables := []switchCable{
		{swA: 0, portA: "l1", swB: 1, portB: "l1"},
		{swA: 0, portA: "l2", swB: 1, portB: "l2"},
	}

	rounds := 0
	snapshot := func() string {
		rounds++
		var b strings.Builder
		for _, sw := range switches {
			roles := sw.Roles()
			for _, name := range []string{"l1", "l2", "p3"} {
				if r, ok := roles[name]; ok {
					fmt.Fprintf(&b, "%v/%v;", r.Role, r.State)
				}
			}
		}
		if r, ok := sw2.Roles()["p3"]; !ok || r.State != stp.StateForwarding {
			fmt.Fprintf(&b, "round-%d;", rounds)
		}
		return b.String()
	}

	now := convergeSwitches(t, start, switches, cables, snapshot)

	// The CIST and MSTI 1 choose different root ports before l2 fails.
	cist := sw2.Roles()["l1"]
	if cist.Role != bpdu.RoleRoot || cist.State != stp.StateForwarding {
		t.Fatalf("sw2 CIST on l1 = role %v state %v, want Root Forwarding", cist.Role, cist.State)
	}
	msti := sw2.TreeRoles()[10]
	if got := msti["l1"]; got.Role != bpdu.RoleAlternate || got.State != stp.StateDiscarding {
		t.Fatalf("sw2 MSTI 1 on l1 = role %v state %v, want Alternate Discarding", got.Role, got.State)
	}
	if got := msti["l2"]; got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
		t.Fatalf("sw2 MSTI 1 on l2 = role %v state %v, want Root Forwarding", got.Role, got.State)
	}

	reg := region(map[bpdu.MSTID]stp.Instance{
		1: {VLANs: []vlan.ID{10}},
		2: {VLANs: []vlan.ID{20}},
	})
	cfgID := reg.ConfigID()
	peerMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03}
	p3Agr := bpdu.BPDU{
		Version:              3,
		Type:                 bpdu.TypeRapid,
		RootID:               bpdu.BridgeID{Priority: 4096, Address: mac1},
		RootPathCost:         0,
		InternalRootPathCost: 20000,
		BridgeID:             bpdu.BridgeID{Priority: 32768, Address: peerMAC},
		PortID:               0x8001,
		HelloTime:            2 * time.Second,
		MaxAge:               20 * time.Second,
		ForwardDelay:         15 * time.Second,
		ConfigID:             &cfgID,
		RegionalRootID:       bpdu.BridgeID{Priority: 4096, Address: mac1},
		RemainingHops:        20,
		MSTIs: []bpdu.MSTIRecord{
			{
				MSTID:                1,
				RegionalRootID:       bpdu.BridgeID{Priority: 32769, Address: mac1},
				InternalRootPathCost: 300000,
				BridgePriority:       0x80,
				PortPriority:         128,
				RemainingHops:        20,
			},
			{
				MSTID:                2,
				RegionalRootID:       bpdu.BridgeID{Priority: 32770, Address: mac1},
				InternalRootPathCost: 300000,
				BridgePriority:       0x80,
				PortPriority:         128,
				RemainingHops:        20,
			},
		},
	}
	p3Agr.SetRole(bpdu.RoleRoot)
	p3Agr.SetAgreement(true)
	var recBPDU bpdu.BPDU
	recBPDU.SetRole(bpdu.RoleRoot)
	recBPDU.SetAgreement(true)
	p3Agr.MSTIs[0].Flags = recBPDU.Flags
	p3Agr.MSTIs[1].Flags = recBPDU.Flags
	frame, err := bpdu.Encode(p3Agr, peerMAC)
	if err != nil {
		t.Fatalf("encode p3 agreement: %v", err)
	}
	sw2.Receive(now, "p3", frame)

	macVLAN10 := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x10}
	macVLAN20 := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x20}
	mustSwitchLearn(t, sw2, []bridge.Seed{
		{FID: 10, MAC: macVLAN10, Port: "p3", LearnedAt: now},
		{FID: 20, MAC: macVLAN20, Port: "p3", LearnedAt: now},
	})
	if _, err := sw2.LinkChange(now, "l2", port.Down, device.PointToPointTrue, 1_000_000_000); err != nil {
		t.Fatal(err)
	}
	msti = sw2.TreeRoles()[10]
	if got := msti["l1"]; got.Role != bpdu.RoleRoot || got.State != stp.StateForwarding {
		t.Fatalf("sw2 MSTI 1 on l1 after l2 down = role %v state %v, want Root Forwarding", got.Role, got.State)
	}

	entries := sw2.Entries()
	if len(entries) != 1 || entries[0].FID != 20 || entries[0].Port != "p3" {
		t.Fatalf("after MSTI 1's topology change, Entries() = %+v, want only the VLAN 20 entry on p3 kept", entries)
	}
}

// pvstSwitchConfig builds a one-trunk PVST switch carrying VLAN 1 and VLAN 10,
// with the trunk's untagged VLAN set to pvid and every other carried VLAN
// tagged. That is the shape Cisco's tagging rules turn on: VLAN 1 untagged on
// a native-VLAN-1 trunk, tagged on any other.
func pvstSwitchConfig(t *testing.T, pvid vlan.ID, carried ...vlan.ID) vswitch.Config {
	t.Helper()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	table := make(map[vlan.ID]string, len(carried))
	var tagged []vlan.ID
	for _, vid := range carried {
		table[vid] = fmt.Sprintf("VLAN%d", vid)
		if vid != pvid {
			tagged = append(tagged, vid)
		}
	}
	slices.Sort(tagged)

	trees := make(map[vlan.ID]stp.Tree, len(carried))
	for _, vid := range carried {
		trees[vid] = stp.Tree{}
	}

	return vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: table,
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{pvid}, Tagged: tagged},
			},
		}},
		STP: &stp.Config{
			Priority: 4096,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports:    map[string]stp.Port{"1/1/1": {}},
			PVST:     &stp.PVST{Trees: trees},
		},
	}
}

// emittedBPDUShape renders one drained emission as the group address it went
// to, the VLAN tag it actually carries on the wire, and the VLAN its payload
// names. The tag is the part the switch decides. The payload VLAN comes from
// the layer.
func emittedBPDUShape(t *testing.T, em device.Emission) string {
	t.Helper()

	tag := "untagged"
	if len(em.Frame.Tags) == 1 {
		tag = fmt.Sprintf("tag=%d", em.Frame.Tags[0].VID)
	} else if len(em.Frame.Tags) > 1 {
		tag = fmt.Sprintf("tags=%d", len(em.Frame.Tags))
	}

	if em.Frame.Dst != bpdu.GroupAddressSSTP() {
		return fmt.Sprintf("ieee/%s", tag)
	}
	_, tlvVID, err := bpdu.DecodeSSTP(em.Frame)
	if err != nil {
		t.Fatalf("decode drained SSTP emission: %v", err)
	}

	return fmt.Sprintf("sstp/%s/payload=%d", tag, tlvVID)
}

func emittedBPDUShapes(t *testing.T, emissions []device.Emission) []string {
	t.Helper()

	shapes := make([]string, 0, len(emissions))
	for _, em := range emissions {
		shapes = append(shapes, emittedBPDUShape(t, em))
	}
	slices.Sort(shapes)

	return shapes
}

// TestPVSTBPDUsRideTheirOwnVLANsEgressTagging proves Cisco's tagging
// rules on a PVST trunk: each VLAN's BPDU leaves the way any other frame on
// that VLAN would, and VLAN 1's tree adds one untagged IEEE-addressed frame
// whatever the native VLAN is. That second frame is what an RSTP or MSTP
// neighbor converges with, so it must not pick up a tag.
func TestPVSTBPDUsRideTheirOwnVLANsEgressTagging(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	t.Run("native vlan 1", func(t *testing.T) {
		callOutputs := make(map[*vswitch.Switch]device.Output)
		t.Parallel()

		sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
		captureOutput(callOutputs, sw, sw.Start(now))
		takeEmissions(callOutputs, sw)
		captureOutput(callOutputs, sw, sw.Advance(now.Add(2*time.Second)))

		got := emittedBPDUShapes(t, takeEmissions(callOutputs, sw))
		want := []string{"ieee/untagged", "sstp/tag=10/payload=10", "sstp/untagged/payload=1"}
		if !slices.Equal(got, want) {
			t.Fatalf("emissions = %v, want %v", got, want)
		}
	})

	t.Run("native vlan 20", func(t *testing.T) {
		callOutputs := make(map[*vswitch.Switch]device.Output)
		t.Parallel()

		sw := mustSwitch(t, pvstSwitchConfig(t, 20, 1, 10, 20))
		captureOutput(callOutputs, sw, sw.Start(now))
		takeEmissions(callOutputs, sw)
		captureOutput(callOutputs, sw, sw.Advance(now.Add(2*time.Second)))

		got := emittedBPDUShapes(t, takeEmissions(callOutputs, sw))
		want := []string{
			"ieee/untagged",
			"sstp/tag=1/payload=1",
			"sstp/tag=10/payload=10",
			"sstp/untagged/payload=20",
		}
		if !slices.Equal(got, want) {
			t.Fatalf("emissions = %v, want %v", got, want)
		}
	})
}

// TestPVSTBPDUIsWithheldFromAPortThatDoesNotCarryTheVLAN proves that the
// emission goes through the bridge's ordinary origination rules rather than
// around them: a tree configured for a VLAN the trunk does not carry puts no
// frame on that trunk.
func TestPVSTBPDUIsWithheldFromAPortThatDoesNotCarryTheVLAN(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	// VLAN 30 has a tree but no place in the bridge's VLAN table, so the
	// trunk cannot carry it.
	cfg.STP.PVST.Trees[30] = stp.Tree{}

	sw := mustSwitch(t, cfg)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Advance(now.Add(2*time.Second)))

	for _, shape := range emittedBPDUShapes(t, takeEmissions(callOutputs, sw)) {
		if strings.HasSuffix(shape, "payload=30") {
			t.Fatalf("a VLAN 30 BPDU left a trunk that does not carry VLAN 30: %s", shape)
		}
	}
}

// TestNewRefusesAPVSTSwitchCarryingAVLANWithNoTree proves that New refuses a
// PVST switch whose bridge carries a VLAN with no tree of its own: such a VLAN has
// no defined forwarding state, which is the false answer per-VLAN spanning
// tree exists to remove.
func TestNewRefusesAPVSTSwitchCarryingAVLANWithNoTree(t *testing.T) {
	t.Parallel()

	cfg := pvstSwitchConfig(t, 1, 1, 10, 20)
	delete(cfg.STP.PVST.Trees, 20)

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want a refusal naming the VLAN with no tree")
	}
	attrs := errs.Attributes(err)
	if got := attrs["field"]; got != "stp.pvst.trees" {
		t.Errorf("error field = %v, want %q", got, "stp.pvst.trees")
	}
	if got := attrs["vlan"]; got != vlan.ID(20) {
		t.Errorf("error vlan = %v, want 20", got)
	}

	cfg.STP.PVST.Trees[20] = stp.Tree{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with every carried VLAN covered = %v, want nil", err)
	}
}

// pvstSSTPFrame builds an SSTP BPDU that a peer bridge would send for vid,
// tagged for the wire as tagVID names.
func pvstSSTPFrame(t *testing.T, vid vlan.ID, tagVID vlan.ID, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := bpdu.EncodeSSTP(bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096 | uint16(vid), Address: src},
		BridgeID:     bpdu.BridgeID{Priority: 4096 | uint16(vid), Address: src},
		PortID:       0x8001,
		Version:      2,
		Type:         bpdu.TypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, vid, src)
	if err != nil {
		t.Fatalf("EncodeSSTP for vlan %d: %v", vid, err)
	}
	if tagVID != 0 {
		frame.Tags = []vlan.Tag{{VID: tagVID}}
	}

	return frame
}

// pvstSSTPTCNFrame builds the no-TLV SSTP TCN shape and tags it for the
// arrival VLAN selected by the switch.
func pvstSSTPTCNFrame(t *testing.T, tagVID vlan.ID, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := bpdu.EncodeSSTP(bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification}, 0, src)
	if err != nil {
		t.Fatalf("EncodeSSTP TCN: %v", err)
	}
	if tagVID != 0 {
		frame.Tags = []vlan.Tag{{VID: tagVID}}
	}

	return frame
}

// TestPVSTPVIDInconsistencyTraceNamesBothVLANs proves that a PVID
// inconsistency is readable from the trace rather than only from the port
// state: the admit step carries the VLAN the BPDU named and the VLAN the
// bridge classified it into, which is the whole finding.
func TestPVSTPVIDInconsistencyTraceNamesBothVLANs(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	// A BPDU naming VLAN 20, tagged for VLAN 10, so the bridge classifies it
	// into VLAN 10 and the two disagree.
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", pvstSSTPFrame(t, 20, 10, peer)))

	step, ok := findStep(res.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in the trace: %+v", res.Steps)
	}

	var canonical string
	for _, fact := range step.Inputs {
		if fact.TypeID() == "stp.sstp.vlans" {
			canonical = fact.Canonical()
		}
	}
	want := "tlv=20,arrival=10,consistent=false"
	if canonical != want {
		t.Fatalf("stp.sstp.vlans fact = %q, want %q", canonical, want)
	}
}

func TestPVSTConfigurationBPDUTraceNamesBothVLANs(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	frame, err := bpdu.EncodeSSTP(bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: peer},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: peer},
		PortID:       0x8001,
		Version:      0,
		Type:         bpdu.TypeConfiguration,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 20, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP Configuration: %v", err)
	}
	frame.Tags = []vlan.Tag{{VID: 10}}

	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", frame))
	step, ok := findStep(res.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in Configuration trace: %+v", res.Steps)
	}
	for _, fact := range step.Inputs {
		if fact.TypeID() == "stp.sstp.vlans" {
			if got, want := fact.Canonical(), "tlv=20,arrival=10,consistent=false"; got != want {
				t.Fatalf("Configuration stp.sstp.vlans fact = %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatal("Configuration trace has no stp.sstp.vlans fact")
}

// TestPVSTSSTPTCNFlushesOnlyTheArrivalVLAN proves that an SSTP TCN, which has
// no payload VLAN, flushes only its tagged arrival VLAN while the trace records
// that distinction.
func TestPVSTSSTPTCNFlushesOnlyTheArrivalVLAN(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	pvid := vlan.ID(1)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{1: "VLAN1", 10: "VLAN10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{1}, Tagged: []vlan.ID{10}},
				"1/1/2": {PVID: &pvid, Untagged: []vlan.ID{1}, Tagged: []vlan.ID{10}},
			},
		}},
		STP: &stp.Config{
			Priority: 4096,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: map[string]stp.Port{
				"1/1/1": {PointToPoint: stp.PointToPointForceFalse},
				"1/1/2": {PointToPoint: stp.PointToPointForceFalse},
			},
			PVST: &stp.PVST{Trees: map[vlan.ID]stp.Tree{1: {}, 10: {}}},
		},
	})
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Advance(now.Add(15*time.Second)))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Advance(now.Add(30*time.Second)))
	takeEmissions(callOutputs, sw)

	mac1 := netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	mac10 := netaddr.MAC{0x02, 0, 0, 0, 0, 10}
	mustSwitchLearn(t, sw, []bridge.Seed{
		{FID: 1, MAC: mac1, Port: "1/1/2", LearnedAt: now},
		{FID: 10, MAC: mac10, Port: "1/1/2", LearnedAt: now},
	})

	res := captureOutput(callOutputs, sw, sw.Receive(now.Add(31*time.Second), "1/1/1", pvstSSTPTCNFrame(t, 10, netaddr.MAC{0, 0xaa, 0xbb, 0xcc, 0xdd, 2})))
	if res.Outcome != trace.Consumed {
		t.Fatalf("TCN outcome = %s, want %s, trace=%+v", res.Outcome, trace.Consumed, res.Steps)
	}
	step, ok := findStep(res.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in TCN trace: %+v", res.Steps)
	}
	var canonical string
	for _, fact := range step.Inputs {
		if fact.TypeID() == "stp.sstp.vlans" {
			canonical = fact.Canonical()
		}
	}
	if canonical != "tlv=none,arrival=10" {
		t.Fatalf("TCN stp.sstp.vlans fact = %q, want %q", canonical, "tlv=none,arrival=10")
	}

	entries := sw.Entries()
	for _, entry := range entries {
		if entry.FID == 10 {
			t.Fatalf("VLAN 10 entry survived TCN flush: %+v", entries)
		}
	}
	foundVLAN1 := false
	for _, entry := range entries {
		if entry.FID == 1 && entry.MAC == mac1 {
			foundVLAN1 = true
		}
	}
	if !foundVLAN1 {
		t.Fatalf("VLAN 1 entry was flushed by VLAN 10 TCN: %+v", entries)
	}

	// The existing RST shape keeps the named-VLAN trace contract.
	rst := captureOutput(callOutputs, sw, sw.Receive(now.Add(31*time.Second), "1/1/1", pvstSSTPFrame(t, 10, 10, netaddr.MAC{0, 0xaa, 0xbb, 0xcc, 0xdd, 3})))
	rstStep, ok := findStep(rst.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in RST trace: %+v", rst.Steps)
	}
	canonical = ""
	for _, fact := range rstStep.Inputs {
		if fact.TypeID() == "stp.sstp.vlans" {
			canonical = fact.Canonical()
		}
	}
	if canonical != "tlv=10,arrival=10,consistent=true" {
		t.Fatalf("RST stp.sstp.vlans fact = %q, want %q", canonical, "tlv=10,arrival=10,consistent=true")
	}
}

// A VLAN whose tree stops at a boundary port carries the issue. VLAN 1
// converges through the IEEE-addressed common tree and carries no boundary issue.
func TestPVSTBoundaryIssueIsRaisedPerVLANAndNotForVLAN1(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peerMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	// An MST BPDU from the neighbor marks the port a boundary.
	mstFrame, err := bpdu.Encode(bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: peerMAC},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: peerMAC},
		PortID:       0x8001,
		Version:      3,
		Type:         bpdu.TypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     new(stp.MST{Name: "elsewhere", Revision: 7}.ConfigID()),
	}, peerMAC)
	if err != nil {
		t.Fatalf("Encode MST BPDU: %v", err)
	}
	captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", mstFrame))
	takeEmissions(callOutputs, sw)

	vlan10 := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", ethernet.Frame{
		Src:  netaddr.MAC{2, 0, 0, 0, 0, 0x10},
		Dst:  netaddr.MAC{2, 0, 0, 0, 0, 0x11},
		Tags: []vlan.Tag{{VID: 10}},
	}))
	if !hasIssueCode(vlan10.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Fatalf("vlan 10 journey carries no %s issue: %+v", vswitch.IssuePVSTBoundary, vlan10.Metadata.Issues())
	}
	for _, issue := range vlan10.Metadata.Issues() {
		if issue.Code != vswitch.IssuePVSTBoundary {
			continue
		}
		want := analysis.ProtocolScope("", string(stp.LayerName), "1/1/1/10")
		if issue.Scope.Compare(want) != 0 {
			t.Errorf("issue scope = %s, want %s", issue.Scope, want)
		}
		if issue.Status != analysis.Unsupported {
			t.Errorf("issue status = %s, want %s", issue.Status, analysis.Unsupported)
		}
	}

	vlan1 := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 0x01},
		Dst: netaddr.MAC{2, 0, 0, 0, 0, 0x02},
	}))
	if hasIssueCode(vlan1.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Errorf("vlan 1 journey carries a %s issue, want none: %+v",
			vswitch.IssuePVSTBoundary, vlan1.Metadata.Issues())
	}
}

// TestPVSTBoundaryIssueIsRaisedOnAnMSTPBridgeToo is the reciprocal: a bridge
// that does not run per-VLAN trees and meets one reports the same boundary
// for the VLANs it cannot answer about.
func TestPVSTBoundaryIssueIsRaisedOnAnMSTPBridgeToo(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peerMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.STP.PVST = nil
	cfg.STP.MST = &stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{
		1: {VLANs: []vlan.ID{10}},
	}}

	sw := mustSwitch(t, cfg)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)
	captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", pvstSSTPFrame(t, 10, 10, peerMAC)))
	takeEmissions(callOutputs, sw)

	vlan10 := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", ethernet.Frame{
		Src:  netaddr.MAC{2, 0, 0, 0, 0, 0x10},
		Dst:  netaddr.MAC{2, 0, 0, 0, 0, 0x11},
		Tags: []vlan.Tag{{VID: 10}},
	}))
	if !hasIssueCode(vlan10.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Fatalf("vlan 10 journey on the MSTP bridge carries no %s issue: %+v",
			vswitch.IssuePVSTBoundary, vlan10.Metadata.Issues())
	}

	vlan1 := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 0x01},
		Dst: netaddr.MAC{2, 0, 0, 0, 0, 0x02},
	}))
	if hasIssueCode(vlan1.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Errorf("vlan 1 journey carries a %s issue, want none: %+v",
			vswitch.IssuePVSTBoundary, vlan1.Metadata.Issues())
	}
}

// TestPVSTForeignVLANDoesNotRewriteVLAN1sRoot proves that a BPDU tagged
// for a VLAN the bridge does not admit on the ingress port never reaches a
// tree lookup: the layer's SSTPNotAdmitted outcome stops it before treeFor
// runs, so a foreign VLAN's BPDU cannot fall back to the CIST, which under
// PVST is VLAN 1's own tree.
func TestPVSTForeignVLANDoesNotRewriteVLAN1sRoot(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	rootBefore, _, portBefore := sw.Root()

	// VLAN 20 is not in the bridge's VLAN table, so it is not admitted on
	// any port. Its root priority is far better than this switch's own, so
	// if the BPDU falls back to the CIST (VLAN 1's tree under PVST) instead
	// of being refused, the peer displaces this switch as VLAN 1's root.
	foreign, err := bpdu.EncodeSSTP(bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 0, Address: peer},
		BridgeID:     bpdu.BridgeID{Priority: 0, Address: peer},
		PortID:       0x8001,
		Version:      2,
		Type:         bpdu.TypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 20, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP: %v", err)
	}
	foreign.Tags = []vlan.Tag{{VID: 20}}
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", foreign))

	if _, ok := findStep(res.Steps, "stp.sstp.vlan-not-admitted"); !ok {
		t.Fatalf("no stp.sstp.vlan-not-admitted step in the trace: %+v", res.Steps)
	}
	if res.Reason != stp.ReasonVLANNotAdmitted {
		t.Errorf("trace reason = %q, want %q", res.Reason, stp.ReasonVLANNotAdmitted)
	}

	rootAfter, _, portAfter := sw.Root()
	if rootAfter != rootBefore || portAfter != portBefore {
		t.Errorf("a VLAN 20 BPDU not admitted on the ingress port changed VLAN 1's root from %v/%q to %v/%q",
			rootBefore, portBefore, rootAfter, portAfter)
	}
}

// TestPVSTPriorityTagIsNotReadAsVLAN0 proves that a tag whose VID is 0
// (a priority tag, which PriorityTags: Always emits on an otherwise
// untagged BPDU) resolves the same as no tag at all. Reading VID 0 as a VLAN
// selection would classify the frame into VLAN 0 where the bridge classifies
// into the PVID, so two identically configured PVST peers would disagree
// about VLAN 1 on every single BPDU and permanently block it.
func TestPVSTPriorityTagIsNotReadAsVLAN0(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	// VLAN 1's own BPDU, tagged with a priority tag (VID 0) the way
	// PriorityTags: Always emits it.
	frame := pvstSSTPFrame(t, 1, 0, peer)
	frame.Tags = []vlan.Tag{{VID: 0}}
	captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", frame))

	info := sw.Roles()["1/1/1"]
	if info.BlockReason == stp.BlockReasonPVIDInconsistent {
		t.Errorf("a correctly addressed VLAN 1 BPDU with a priority tag was read as VLAN 0 and blocked VLAN 1: %+v", info)
	}
}

// TestValidateRefusesPVSTOnAVLANUnawareBridge proves that PVST requires
// a VLAN-aware bridge: on a bridge with no VLAN table, untaggedVID returns 0
// for every port, so each end of a link reads the other's BPDU as arriving
// on the wrong VLAN and blocks VLAN 1 permanently. There is nothing
// per-VLAN about a bridge with no VLAN table to run trees over.
func TestValidateRefusesPVSTOnAVLANUnawareBridge(t *testing.T) {
	t.Parallel()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports:    map[string]stp.Port{"1/1/1": {}},
			PVST:     &stp.PVST{Trees: map[vlan.ID]stp.Tree{1: {}}},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want refusal of PVST on a VLAN-unaware bridge")
	}
	if got, want := errs.Attributes(err)["field"], "stp.pvst"; got != want {
		t.Errorf("error field = %v, want %q", got, want)
	}
}

// TestValidateAcceptsPVSTWithImplicitVLAN1Tree proves that validation accepts
// the ordinary switch shape, a bridge carrying VLAN 1 alongside other VLANs
// with a PVST configuration that never names VLAN 1's tree explicitly.
// Normalize fills it in, so the covered-VLAN check must see it too.
func TestValidateAcceptsPVSTWithImplicitVLAN1Tree(t *testing.T) {
	t.Parallel()

	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	pvid := vlan.ID(1)

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{1: "default", 10: "VLAN10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{1}, Tagged: []vlan.ID{10}},
			},
		}},
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports:    map[string]stp.Port{"1/1/1": {}},
			PVST:     &stp.PVST{Trees: map[vlan.ID]stp.Tree{10: {}}},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for the ordinary shape where VLAN 1's tree is implicit", err)
	}
}

// TestPVSTBoundaryNotRaisedForAnFID0Drop is evidence that recordPVSTBoundaries
// only records against a classified journey: res.FID is 0 on every path
// where classification never ran, including a pre-classification drop such
// as a reserved group address, and that has nothing to do with spanning
// tree.
func TestPVSTBoundaryNotRaisedForAnFID0Drop(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peerMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	// An MST BPDU from the neighbor marks the port a boundary.
	mstFrame, err := bpdu.Encode(bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: peerMAC},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: peerMAC},
		PortID:       0x8001,
		Version:      3,
		Type:         bpdu.TypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     new(stp.MST{Name: "elsewhere", Revision: 7}.ConfigID()),
	}, peerMAC)
	if err != nil {
		t.Fatalf("Encode MST BPDU: %v", err)
	}
	captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", mstFrame))
	takeEmissions(callOutputs, sw)

	// A frame to a reserved group address is dropped before classification,
	// so FID is 0.
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", ethernet.Frame{
		Src: netaddr.MAC{2, 0, 0, 0, 0, 1},
		Dst: netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e},
	}))
	if hasIssueCode(res.Metadata.Issues(), vswitch.IssuePVSTBoundary) {
		t.Errorf("an FID-0 journey carries a %s issue, want none: %+v",
			vswitch.IssuePVSTBoundary, res.Metadata.Issues())
	}
}

// TestPVSTAdmitBeforeSnapshotUsesTheArrivalVLANsTree is evidence that the
// admit step's before/after snapshot both come from the tree the BPDU
// actually affects. Taking before from the CIST (VLAN 1's tree under PVST)
// while after comes from the arrival VLAN's tree renders a VLAN 10 BPDU
// that changes nothing on VLAN 10 as a spurious transition, once VLAN 1's
// tree and VLAN 10's tree disagree about port state.
func TestPVSTAdmitBeforeSnapshotUsesTheArrivalVLANsTree(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	// A peer BPDU with a far better (lower) priority than this switch's own
	// makes the peer root of VLAN 10, moving that port's VLAN 10 role away
	// from the default Designated. Nothing is ever sent for VLAN 1, so the
	// CIST stays at its default Designated/Forwarding, and the two trees now
	// disagree about this port's role.
	converge, err := bpdu.EncodeSSTP(bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 0, Address: peer},
		BridgeID:     bpdu.BridgeID{Priority: 0, Address: peer},
		PortID:       0x8001,
		Version:      2,
		Type:         bpdu.TypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 10, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP: %v", err)
	}
	converge.Tags = []vlan.Tag{{VID: 10}}
	captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", converge))
	takeEmissions(callOutputs, sw)

	// The identical BPDU again: VLAN 10's own before and after must match,
	// since nothing about VLAN 10 changes on this second delivery.
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", converge))

	step, ok := findStep(res.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in the trace: %+v", res.Steps)
	}

	var decision trace.Fact
	for _, fact := range step.Outputs {
		if fact.TypeID() == "stp.bpdu_decision" {
			decision = fact
		}
	}
	if decision == nil {
		t.Fatalf("no stp.bpdu_decision output in the admit step: %+v", step.Outputs)
	}

	canonical := decision.Canonical()
	beforeAt := strings.Index(canonical, ";before=")
	afterAt := strings.Index(canonical, ";after=")
	if beforeAt < 0 || afterAt < 0 || afterAt < beforeAt {
		t.Fatalf("stp.bpdu_decision canonical %q does not carry before/after sections", canonical)
	}
	beforePart := canonical[beforeAt+len(";before=") : afterAt]
	afterPart := canonical[afterAt+len(";after="):]

	// The received-BPDU counter is the one field that must move: it counts the
	// frame this very step is reporting on, and it is a link property both
	// snapshots read from the same place. Everything else describes the tree
	// the snapshot was taken from, which is what this test is about.
	rxCount := regexp.MustCompile(`rx_bpdus=\d+`)
	beforePart = rxCount.ReplaceAllString(beforePart, "rx_bpdus=N")
	afterPart = rxCount.ReplaceAllString(afterPart, "rx_bpdus=N")

	if beforePart != afterPart {
		t.Errorf("a second identical VLAN 10 BPDU reported before != after:\nbefore=%s\nafter=%s", beforePart, afterPart)
	}
}

// TestSSTPFrameConsultsTheSTPScopeWhenSpanningTreeIsMissing is evidence that
// a per-VLAN BPDU reaching a switch whose spanning tree state never loaded is
// answered the way an IEEE-addressed one is: the result names the spanning
// tree scope it depended on, rather than reporting a confident drop.
func TestSSTPFrameConsultsTheSTPScopeWhenSpanningTreeIsMissing(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peerMAC := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}
	vid := vlan.ID(1)

	stpScope := analysis.ProtocolScope("sw1", string(stp.LayerName), "0")
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{
			MAC: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{1: "VLAN1"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid, Untagged: []vlan.ID{1}},
				},
			}},
		},
		NodeID: "sw1",
		Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
			Code:    "test.stp.protocol",
			Status:  analysis.Unsupported,
			Scope:   stpScope,
			Message: "the spanning tree protocol state is incomplete",
		}}, analysis.EvidenceCatalog{}, nil),
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	// Tagged for a VLAN the access port does not carry, so the bridge drops
	// the frame at classification, before any gate runs. That is the path
	// where the spanning tree scope would otherwise go unnamed.
	res := sw.Receive(now, "1/1/1", pvstSSTPFrame(t, 99, 99, peerMAC))
	if res.Outcome != trace.Dropped {
		t.Fatalf("outcome = %s, want %s", res.Outcome, trace.Dropped)
	}

	want := analysis.FieldScope(stpScope, "ports", "1/1/1")
	if !slices.ContainsFunc(res.ConsultedScopes(), func(s analysis.Scope) bool {
		return s.Compare(want) == 0
	}) {
		t.Fatalf("consulted scopes = %v, want one naming %s", res.ConsultedScopes(), want)
	}
}

// BPDU guard applies to the link before tree admission. A decoded rogue
// BPDU disables the port even when its VLAN is not admitted.
func TestSSTPForAnUnadmittedVLANStillFiresBPDUGuard(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.STP.Ports["1/1/1"] = stp.Port{BPDUGuard: true}

	sw := mustSwitch(t, cfg)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	// VLAN 30 is not in the bridge's VLAN table, so it is not admitted on
	// this port. A gate that refused the frame before the layer saw it would
	// prevent BPDU guard. The layer's link half runs
	// regardless.
	rogue, err := bpdu.EncodeSSTP(bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 0, Address: peer},
		BridgeID:     bpdu.BridgeID{Priority: 0, Address: peer},
		PortID:       0x8001,
		Version:      2,
		Type:         bpdu.TypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 30, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP: %v", err)
	}
	rogue.Tags = []vlan.Tag{{VID: 30}}
	captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", rogue))

	info := sw.Roles()["1/1/1"]
	if info.BlockReason != stp.BlockReasonBPDUGuard {
		t.Errorf("block reason = %q, want %q: an SSTP BPDU tagged for an unadmitted VLAN did not fire BPDU guard", info.BlockReason, stp.BlockReasonBPDUGuard)
	}
}

// TestSSTPRefusalTracesADecodedFrame is evidence that a VLAN refusal is
// rendered as what it is: a frame that decoded and was judged against the
// bridge's admission rule, not a malformed frame. The decode fact reports
// valid=true, and the VLAN fact names both the TLV's VLAN and the VLAN the
// switch resolved on arrival.
func TestSSTPRefusalTracesADecodedFrame(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	before := sw.Roles()["1/1/1"]

	foreign := pvstSSTPFrame(t, 30, 30, peer)
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", foreign))

	step, ok := findStep(res.Steps, "stp.sstp.vlan-not-admitted")
	if !ok {
		t.Fatalf("no stp.sstp.vlan-not-admitted step in the trace: %+v", res.Steps)
	}

	var decodeCanonical, vlansCanonical string
	for _, fact := range step.Inputs {
		switch fact.TypeID() {
		case "stp.bpdu_decision":
			decodeCanonical = fact.Canonical()
		case "stp.sstp.vlans":
			vlansCanonical = fact.Canonical()
		}
	}
	if !strings.Contains(decodeCanonical, "valid=true") {
		t.Errorf("decode fact = %q, want valid=true: a refusal on VLAN grounds is not a decode failure", decodeCanonical)
	}
	if want := "tlv=30,arrival=30,consistent=true"; vlansCanonical != want {
		t.Errorf("stp.sstp.vlans fact = %q, want %q", vlansCanonical, want)
	}

	after := sw.Roles()["1/1/1"]
	if after.BadBPDUs != before.BadBPDUs {
		t.Errorf("BadBPDUs = %d, want %d unchanged: a decoded frame refused on VLAN grounds must not count as a bad BPDU", after.BadBPDUs, before.BadBPDUs)
	}
}

// sstpStepShape summarizes an SSTP step's structural shape: which layer and
// op it belongs to, its rule and subject, and the kind of fact each input and
// output carries. It deliberately excludes the facts' own canonical values,
// since those report port state that mutation changes and Peek does not —
// the trace shape this unit promises is independent of mutation is the shape
// alone, not the counters a mutating call went on to move.
func sstpStepShape(step trace.Step) string {
	inputs := make([]string, 0, len(step.Inputs))
	for _, f := range step.Inputs {
		inputs = append(inputs, f.TypeID())
	}
	outputs := make([]string, 0, len(step.Outputs))
	for _, f := range step.Outputs {
		outputs = append(outputs, f.TypeID())
	}

	return fmt.Sprintf("layer=%s;op=%s;rule=%s;subject=%s/%s;inputs=%v;outputs=%v",
		step.Layer, step.Op, step.RuleID, step.Subject.Kind, step.Subject.Key, inputs, outputs)
}

// TestSSTPTraceShapeIsTheSameWithAndWithoutMutation is the claim nothing else
// pins: interceptSSTP renders the same step shape whether or not mutate is
// set, because the outcome the non-mutating path derives from admitted and
// TracksVLAN is exactly the outcome the mutating path reaches by calling
// ReceiveSSTP. Every reachable outcome but SSTPPortDown is covered: a port
// admitted into the bridge's VLAN table always has a per-VLAN spanning tree
// under a validly constructed switch (Config.Validate refuses a carried VLAN
// with no tree), so SSTPUntrackedVLAN, which requires an admitted VLAN with
// no tree, is not reachable through Switch.Receive and is not one of the
// cases below; stp.Layer's own tests cover it directly.
func TestSSTPTraceShapeIsTheSameWithAndWithoutMutation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cases := []struct {
		name   string
		cfg    func(t *testing.T) vswitch.Config
		frame  func(t *testing.T) ethernet.Frame
		ruleID trace.RuleID
	}{
		{
			name: "applied",
			cfg:  func(t *testing.T) vswitch.Config { return pvstSwitchConfig(t, 1, 1, 10) },
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 10, 10, peer)
			},
			ruleID: "stp.sstp.admit",
		},
		{
			name: "bpdu-guard",
			cfg: func(t *testing.T) vswitch.Config {
				cfg := pvstSwitchConfig(t, 1, 1, 10)
				cfg.STP.Ports["1/1/1"] = stp.Port{BPDUGuard: true}
				return cfg
			},
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 10, 10, peer)
			},
			ruleID: "stp.sstp.admit",
		},
		{
			name: "pvst-boundary",
			cfg: func(t *testing.T) vswitch.Config {
				cfg := pvstSwitchConfig(t, 1, 1, 10)
				cfg.STP.PVST = nil
				cfg.STP.MST = &stp.MST{Name: "region-1", Revision: 1, Instances: map[bpdu.MSTID]stp.Instance{
					1: {VLANs: []vlan.ID{10}},
				}}
				return cfg
			},
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 10, 10, peer)
			},
			ruleID: "stp.sstp.admit",
		},
		{
			name: "vlan-not-admitted",
			cfg:  func(t *testing.T) vswitch.Config { return pvstSwitchConfig(t, 1, 1, 10) },
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 30, 30, peer)
			},
			ruleID: "stp.sstp.vlan-not-admitted",
		},
		{
			name: "pvid-inconsistent",
			cfg:  func(t *testing.T) vswitch.Config { return pvstSwitchConfig(t, 1, 1, 10) },
			frame: func(t *testing.T) ethernet.Frame {
				return pvstSSTPFrame(t, 20, 10, peer)
			},
			ruleID: "stp.sstp.admit",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			callOutputs := make(map[*vswitch.Switch]device.Output)
			t.Parallel()

			forwardSw := mustSwitch(t, c.cfg(t))
			captureOutput(callOutputs, forwardSw, forwardSw.Start(now))
			takeEmissions(callOutputs, forwardSw)
			before := forwardSw.TreeRoles()
			forwardRes := captureOutput(callOutputs, forwardSw, forwardSw.Receive(now, "1/1/1", c.frame(t)))

			peekSw := mustSwitch(t, c.cfg(t))
			captureOutput(callOutputs, peekSw, peekSw.Start(now))
			takeEmissions(callOutputs, peekSw)
			peekRes := peekSw.Peek(now, "1/1/1", c.frame(t))

			forwardStep, ok := findStep(forwardRes.Steps, c.ruleID)
			if !ok {
				t.Fatalf("Receive: no %s step in the trace: %+v", c.ruleID, forwardRes.Steps)
			}
			peekStep, ok := findStep(peekRes.Steps, c.ruleID)
			if !ok {
				t.Fatalf("Peek: no %s step in the trace: %+v", c.ruleID, peekRes.Steps)
			}

			wantTransitions := make(map[stp.TreeRef]trace.Fact)
			for vid, ports := range before {
				old := ports["1/1/1"]
				info := forwardSw.TreeRoles()[vid]["1/1/1"]
				if old.Role != info.Role || old.State != info.State {
					wantTransitions[old.Tree] = stp.PortTransitionFact("1/1/1", "bpdu", old, info)
				}
			}
			if len(forwardStep.Outputs) != 1+len(wantTransitions) || len(peekStep.Outputs) != 1 {
				t.Fatalf("Receive/Peek output counts = %d/%d, want %d/1", len(forwardStep.Outputs), len(peekStep.Outputs), 1+len(wantTransitions))
			}
			for _, want := range wantTransitions {
				count := 0
				for _, got := range forwardStep.Outputs[1:] {
					if trace.EqualFact(got, want) {
						count++
					}
				}
				if count != 1 {
					t.Errorf("transition %s occurs %d times, want 1", want.Canonical(), count)
				}
			}
			forwardStep.Outputs = forwardStep.Outputs[:1]
			if got, want := sstpStepShape(peekStep), sstpStepShape(forwardStep); got != want {
				t.Errorf("Peek step shape = %s, want %s (Receive's shape)", got, want)
			}
			if forwardRes.Outcome != peekRes.Outcome {
				t.Errorf("Receive outcome = %s, Peek outcome = %s, want equal", forwardRes.Outcome, peekRes.Outcome)
			}
			if forwardRes.Reason != peekRes.Reason {
				t.Errorf("Receive reason = %q, Peek reason = %q, want equal", forwardRes.Reason, peekRes.Reason)
			}
		})
	}
}

// A guard action does not admit the frame's VLAN. The trace records
// vlan-not-admitted when the ingress port does not carry that VLAN.
func TestSSTPGuardedFrameOnAnUnadmittedVLANTracesAsNotAdmitted(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.STP.Ports["1/1/1"] = stp.Port{BPDUGuard: true}

	sw := mustSwitch(t, cfg)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	rogue := pvstSSTPFrame(t, 30, 30, peer)
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", rogue))

	if res.Outcome != trace.Dropped {
		t.Errorf("Outcome = %s, want %s: bpdu guard firing on an unadmitted VLAN is not an admit", res.Outcome, trace.Dropped)
	}
	if res.Reason != stp.ReasonVLANNotAdmitted {
		t.Errorf("Reason = %q, want %q", res.Reason, stp.ReasonVLANNotAdmitted)
	}

	step, ok := findStep(res.Steps, "stp.sstp.vlan-not-admitted")
	if !ok {
		t.Fatalf("no stp.sstp.vlan-not-admitted step in the trace: %+v", res.Steps)
	}

	var decodeCanonical, outputCanonical string
	for _, fact := range step.Inputs {
		if fact.TypeID() == "stp.bpdu_decision" {
			decodeCanonical = fact.Canonical()
		}
	}
	for _, fact := range step.Outputs {
		if fact.TypeID() == "stp.bpdu_decision" && strings.HasPrefix(fact.Canonical(), "bpdu=") {
			outputCanonical = fact.Canonical()
		}
	}
	if !strings.Contains(decodeCanonical, "valid=true") {
		t.Errorf("decode fact = %q, want valid=true: bpdu guard firing is not a decode failure", decodeCanonical)
	}

	// VLAN 30 has no tree on this bridge, so before and after both read as
	// the zero PortInfo; that is consistent with a refusal on VLAN grounds,
	// unlike rendering the same zero state under a rule that claims the
	// frame was admitted.
	beforeIdx := strings.Index(outputCanonical, ";before=")
	afterIdx := strings.Index(outputCanonical, ";after=")
	if beforeIdx < 0 || afterIdx < 0 || afterIdx <= beforeIdx {
		t.Fatalf("decision fact = %q, want before/after segments", outputCanonical)
	}
	beforeSeg := outputCanonical[beforeIdx+len(";before=") : afterIdx]
	afterSeg := outputCanonical[afterIdx+len(";after="):]
	if beforeSeg != afterSeg {
		t.Errorf("decision fact before != after: %q vs %q, want equal for an unadmitted, untracked VLAN", beforeSeg, afterSeg)
	}

	// BPDU guard disables the port independently of the VLAN admission trace.
	info := sw.Roles()["1/1/1"]
	if info.BlockReason != stp.BlockReasonBPDUGuard {
		t.Errorf("block reason = %q, want %q", info.BlockReason, stp.BlockReasonBPDUGuard)
	}
}

// An operationally-up port omitted from stp.Config.Ports is unlinked in
// the spanning tree layer and reports port-down.
func TestSSTPOnAPortTheLayerDoesNotTrackTracesAsPortDown(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.Ports = mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	// "1/1/2" stays out of cfg.STP.Ports on purpose: the layer never
	// configured it even though the port table calls it up.

	sw := mustSwitch(t, cfg)
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	frame := pvstSSTPFrame(t, 10, 10, peer)
	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/2", frame))

	if res.Outcome != trace.Dropped {
		t.Errorf("Outcome = %s, want %s: a port the layer never configured did not process this frame at all", res.Outcome, trace.Dropped)
	}
	if res.Reason != port.ReasonPortDown {
		t.Errorf("Reason = %q, want %q", res.Reason, port.ReasonPortDown)
	}

	step, ok := findStep(res.Steps, "port.status.down")
	if !ok {
		t.Fatalf("no port.status.down step in the trace: %+v", res.Steps)
	}
	if step.Layer != stp.LayerName {
		t.Errorf("step layer = %s, want %s", step.Layer, stp.LayerName)
	}
}

// An outer service tag does not select an SSTP arrival VLAN. The frame
// uses the port's untagged VLAN, matching bridge ingress classification.
func TestSSTPClassifiesAQinQTaggedFrameLikeBridgeIngress(t *testing.T) {
	callOutputs := make(map[*vswitch.Switch]device.Output)
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	sw := mustSwitch(t, pvstSwitchConfig(t, 1, 1, 10))
	captureOutput(callOutputs, sw, sw.Start(now))
	takeEmissions(callOutputs, sw)

	frame, err := bpdu.EncodeSSTP(bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096 | 1, Address: peer},
		BridgeID:     bpdu.BridgeID{Priority: 4096 | 1, Address: peer},
		PortID:       0x8001,
		Version:      2,
		Type:         bpdu.TypeRapid,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}, 1, peer)
	if err != nil {
		t.Fatalf("EncodeSSTP: %v", err)
	}
	frame.Tags = []vlan.Tag{{TPID: uint16(ethernet.EtherTypeProviderBridging), VID: 10}}

	res := captureOutput(callOutputs, sw, sw.Receive(now, "1/1/1", frame))

	step, ok := findStep(res.Steps, "stp.sstp.admit")
	if !ok {
		t.Fatalf("no stp.sstp.admit step in the trace: %+v", res.Steps)
	}
	if res.FID != 1 {
		t.Errorf("FID = %d, want 1: a QinQ outer tag is not a VLAN selection, so the frame lands on the port's untagged VLAN the same way bridge.Ingress classifies it", res.FID)
	}

	var vlansCanonical string
	for _, fact := range step.Inputs {
		if fact.TypeID() == "stp.sstp.vlans" {
			vlansCanonical = fact.Canonical()
		}
	}
	if want := "tlv=1,arrival=1,consistent=true"; vlansCanonical != want {
		t.Errorf("stp.sstp.vlans fact = %q, want %q", vlansCanonical, want)
	}
}

// TestSSTPPortDownTracesTheSameUnderPeekAndForward pins the agreement a
// read-only inspection owes a committing one. Peek derives its outcome from
// what the switch can observe without mutating, and the port-down case is the
// one the layer alone knows: a port the port table calls up that the spanning
// tree layer has not seen a link-up event for. A Peek that renders it as
// admitted describes a journey the switch would not have taken.
func TestSSTPPortDownTracesTheSameUnderPeekAndReceive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	peer := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x02}

	cfg := pvstSwitchConfig(t, 1, 1, 10)
	cfg.Ports = mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	// Start is deliberately not called: "1/1/1" is a configured spanning tree
	// port that the layer has not yet seen a link transition for. That is the
	// case a port-name lookup alone cannot tell from a configured, linked
	// port, so it is the one that distinguishes the two.
	sw := mustSwitch(t, cfg)

	frame := pvstSSTPFrame(t, 10, 10, peer)

	peeked := sw.Peek(now, "1/1/1", frame)
	forwarded := sw.Receive(now, "1/1/1", frame)

	if peeked.Outcome != forwarded.Outcome {
		t.Errorf("Peek outcome = %s, Receive outcome = %s, want agreement", peeked.Outcome, forwarded.Outcome)
	}
	if peeked.Reason != forwarded.Reason {
		t.Errorf("Peek reason = %q, Receive reason = %q, want agreement", peeked.Reason, forwarded.Reason)
	}
	if _, ok := findStep(peeked.Steps, "port.status.down"); !ok {
		t.Errorf("no port.status.down step under Peek: %+v", peeked.Steps)
	}
}

func TestSSTPRecordsTreeOutsideBridgeTable(t *testing.T) {
	cfg := pvstSwitchConfig(t, 1, 1, 10)
	delete(cfg.Bridge.VLAN.Table, 10)
	cfg.Bridge.VLAN.Switchports["1/1/1"] = bridge.Switchport{PVID: new(vlan.ID(1)), Untagged: []vlan.ID{1}}
	cfg.STP.Ports["1/1/1"] = stp.Port{BPDUGuard: true}
	sw := mustSwitch(t, cfg)
	sw.Start(fixedTime)
	before := sw.TreeRoles()[10]["1/1/1"]
	if before.Role != bpdu.RoleDesignated {
		t.Fatalf("before role = %v, want Designated", before.Role)
	}
	out := sw.Receive(fixedTime, "1/1/1", pvstSSTPFrame(t, 10, 10, macH1))
	after := sw.TreeRoles()[10]["1/1/1"]
	if after.Role != bpdu.RoleDisabled {
		t.Fatalf("after role = %v, want Disabled", after.Role)
	}
	step, ok := findStep(out.Steps, stp.RuleSSTPVLANNotAdmitted)
	if !ok {
		t.Fatalf("steps = %+v, want SSTP VLAN refusal", out.Steps)
	}
	if len(step.Outputs) != 3 || !trace.EqualFact(step.Outputs[2], stp.PortTransitionFact("1/1/1", "bpdu", before, after)) {
		t.Errorf("outputs = %+v, want decision then VLAN 1 and VLAN 10 transitions", step.Outputs)
	}
}
