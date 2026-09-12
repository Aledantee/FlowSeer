package bridge_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

var (
	testTime0 = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}
	macC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	macD = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}
)

func mustVLAN(id uint16) *vlan.ID {
	v := vlan.ID(id)

	return &v
}

func buildTestPorts(t *testing.T, count int) port.Table {
	t.Helper()
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, count, port.Port{
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build test ports: %v", err)
	}

	return tbl
}

func TestDropsWithoutAnEgressCarryAReason(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	macB := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0xbb}
	macA := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0xaa}

	t.Run("a hit on a port outside the classified VLAN", func(t *testing.T) {
		// 1/1/2 is a trunk with PVID 1 and no untagged set, the ordinary shape:
		// an untagged frame from it is learned under VLAN 1, where it is no member.
		cfg := bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{1: "default", 20: "twenty"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(1), Untagged: []vlan.ID{1}},
				"1/1/2": {PVID: mustVLAN(1), Tagged: []vlan.ID{20}},
			},
		}}
		b := bridge.New(cfg, buildTestPorts(t, 2))
		b.Forward(now, "1/1/2", ethernet.Frame{Dst: macA, Src: macB})
		res := b.Forward(now, "1/1/1", ethernet.Frame{Dst: macB, Src: macA})
		if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonNotMember {
			t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, bridge.ReasonNotMember)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/2" || res.Egress[0].Dropped != bridge.ReasonNotMember {
			t.Errorf("Egress = %+v, want 1/1/2 dropped with %s", res.Egress, bridge.ReasonNotMember)
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop {
			t.Errorf("last step = %+v, want a drop", last)
		}
	})

	t.Run("a flood with no other forwarding port", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		res := bridge.New(bridge.Config{}, tbl).Forward(now, "1/1/1", ethernet.Frame{Dst: macB, Src: macA})
		if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonNoEgress {
			t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, bridge.ReasonNoEgress)
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop {
			t.Errorf("last step = %+v, want a drop", last)
		}
	})
}

func TestSeedNamingALagMemberIsStoredUnderTheLag(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
	b.Range("1/1/%d", 5, 6, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	tbl, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	macB := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0xbb}
	br := bridge.New(bridge.Config{}, tbl)
	br.Learn([]bridge.Seed{
		{MAC: macB, Port: "1/1/6", LearnedAt: now},
		{MAC: netaddr.MAC{0x01, 0x00, 0x5e, 0, 0, 1}, Port: "1/1/1", LearnedAt: now},
	})
	entries := br.Entries()
	if len(entries) != 1 || entries[0].Port != "lag1" {
		t.Fatalf("Entries() = %+v, want one entry on lag1 and the group seed ignored", entries)
	}

	res := br.Forward(now, "1/1/5", ethernet.Frame{Dst: macB, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa}})
	if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonSamePort {
		t.Errorf("a frame to macB from the other member = %s/%s, want Dropped/%s", res.Outcome, res.Reason, bridge.ReasonSamePort)
	}
}

func TestAdmissionIsCheckedBeforeThePVID(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	cfg := bridge.Config{VLAN: &bridge.VLAN{
		Table:       map[vlan.ID]string{20: "twenty"},
		Switchports: map[string]bridge.Switchport{"1/1/1": {Admission: bridge.TaggedOnly, Tagged: []vlan.ID{20}}},
	}}
	res := bridge.New(cfg, buildTestPorts(t, 2)).Forward(now, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0, 0, 0, 0, 0, 0xbb}, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa},
	})
	if res.Reason != bridge.ReasonAdmission {
		t.Errorf("an untagged frame on a tagged-only trunk without a PVID dropped with %q, want %q", res.Reason, bridge.ReasonAdmission)
	}
}

func TestDiffIgnoresSetOrderAndTheAgingDefault(t *testing.T) {
	a := bridge.Config{VLAN: &bridge.VLAN{
		Table:       map[vlan.ID]string{10: "", 20: ""},
		Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{10, 20}}},
	}}
	b := bridge.Config{AgingTime: bridge.DefaultAgingTime, VLAN: &bridge.VLAN{
		Table:       map[vlan.ID]string{10: "", 20: ""},
		Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{20, 10}}},
	}}
	if changes := bridge.Diff(a, b); len(changes) != 0 {
		t.Errorf("Diff = %+v, want no change between configurations that build the same bridge", changes)
	}
}

func TestUntaggedIngressClassifiesToPVIDAndLearns(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "engineering",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {
					PVID:     mustVLAN(10),
					Untagged: []vlan.ID{10},
				},
				"1/1/2": {
					PVID:     mustVLAN(10),
					Untagged: []vlan.ID{10},
				},
				"1/1/3": {
					PVID:     mustVLAN(10),
					Untagged: []vlan.ID{10},
				},
			},
		},
	}

	br := bridge.New(cfg, ports)
	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("payload"),
	}

	res := br.Forward(testTime0, "1/1/1", frame)

	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Flooded)
	}
	if res.FID != 10 {
		t.Errorf("res.FID = %d, want 10", res.FID)
	}

	egressPorts := make([]string, len(res.Egress))
	for i, eg := range res.Egress {
		egressPorts[i] = eg.Port
	}
	if slices.Contains(egressPorts, "1/1/1") {
		t.Errorf("egress ports %v contains ingress port 1/1/1", egressPorts)
	}
	if !slices.Contains(egressPorts, "1/1/2") || !slices.Contains(egressPorts, "1/1/3") {
		t.Errorf("egress ports = %v, want 1/1/2 and 1/1/3", egressPorts)
	}

	entries := br.Entries()
	if len(entries) != 1 {
		t.Fatalf("len(entries) = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.FID != 10 || e.MAC != macA || e.Port != "1/1/1" || e.Static {
		t.Errorf("learned entry = %+v, want dynamic (10, %s) -> 1/1/1", e, macA)
	}
}

func TestAdmissionAndIngressFiltering(t *testing.T) {
	ports := buildTestPorts(t, 4)

	untaggedFrame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}
	priorityTaggedFrame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: 0, PCP: 3}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}
	tagged20Frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: 20}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}

	cases := []struct {
		name        string
		admission   bridge.Admission
		filtering   bool
		member      bool
		frame       ethernet.Frame
		wantOutcome trace.Outcome
		wantReason  trace.Reason
		wantFID     vlan.ID
	}{
		{
			name:        "tagged-only drops untagged frame",
			admission:   bridge.TaggedOnly,
			filtering:   false,
			member:      true,
			frame:       untaggedFrame,
			wantOutcome: trace.Dropped,
			wantReason:  bridge.ReasonAdmission,
		},
		{
			name:        "tagged-only drops priority-tagged frame",
			admission:   bridge.TaggedOnly,
			filtering:   false,
			member:      true,
			frame:       priorityTaggedFrame,
			wantOutcome: trace.Dropped,
			wantReason:  bridge.ReasonAdmission,
		},
		{
			name:        "untagged-only drops frame tagged 20",
			admission:   bridge.UntaggedAndPriorityTaggedOnly,
			filtering:   false,
			member:      true,
			frame:       tagged20Frame,
			wantOutcome: trace.Dropped,
			wantReason:  bridge.ReasonAdmission,
		},
		{
			name:        "untagged-only classifies priority-tagged frame to PVID",
			admission:   bridge.UntaggedAndPriorityTaggedOnly,
			filtering:   false,
			member:      true,
			frame:       priorityTaggedFrame,
			wantOutcome: trace.Flooded,
			wantFID:     10,
		},
		{
			name:        "admit all admits untagged frame",
			admission:   bridge.All,
			filtering:   false,
			member:      true,
			frame:       untaggedFrame,
			wantOutcome: trace.Flooded,
			wantFID:     10,
		},
		{
			name:        "admit all admits priority-tagged frame",
			admission:   bridge.All,
			filtering:   false,
			member:      true,
			frame:       priorityTaggedFrame,
			wantOutcome: trace.Flooded,
			wantFID:     10,
		},
		{
			name:        "admit all admits tagged frame",
			admission:   bridge.All,
			filtering:   false,
			member:      true,
			frame:       tagged20Frame,
			wantOutcome: trace.Flooded,
			wantFID:     20,
		},
		{
			name:        "ingress filtering enabled drops non-member tagged frame",
			admission:   bridge.All,
			filtering:   true,
			member:      false,
			frame:       tagged20Frame,
			wantOutcome: trace.Dropped,
			wantReason:  bridge.ReasonIngressFilter,
		},
		{
			name:        "ingress filtering disabled forwards non-member tagged frame",
			admission:   bridge.All,
			filtering:   false,
			member:      false,
			frame:       tagged20Frame,
			wantOutcome: trace.Flooded,
			wantFID:     20,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ingressTagged []vlan.ID
			if tc.member {
				ingressTagged = []vlan.ID{20}
			}

			cfg := bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{
						10: "vlan10",
						20: "vlan20",
					},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {
							PVID:             mustVLAN(10),
							Tagged:           ingressTagged,
							Untagged:         []vlan.ID{10},
							IngressFiltering: tc.filtering,
							Admission:        tc.admission,
						},
						"1/1/2": {
							PVID:     mustVLAN(20),
							Tagged:   []vlan.ID{20},
							Untagged: []vlan.ID{10},
						},
					},
				},
			}

			br := bridge.New(cfg, ports)
			res := br.Forward(testTime0, "1/1/1", tc.frame)

			if res.Outcome != tc.wantOutcome {
				t.Errorf("res.Outcome = %q, want %q", res.Outcome, tc.wantOutcome)
			}
			if tc.wantReason != "" && res.Reason != tc.wantReason {
				t.Errorf("res.Reason = %q, want %q", res.Reason, tc.wantReason)
			}
			if tc.wantOutcome != trace.Dropped && res.FID != tc.wantFID {
				t.Errorf("res.FID = %d, want %d", res.FID, tc.wantFID)
			}
		})
	}
}

func TestEgressTagForm(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				20: "vlan20",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/2": {
					Tagged: []vlan.ID{20},
				},
				"1/1/3": {
					Untagged: []vlan.ID{20},
				},
				"1/1/4": {
					Tagged: []vlan.ID{20},
				},
			},
		},
	}

	br := bridge.New(cfg, ports)
	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: 20, PCP: 5, DEI: true}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("payload"),
	}

	res := br.Forward(testTime0, "1/1/4", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Flooded)
	}

	var egress2, egress3 *bridge.Egress
	for i := range res.Egress {
		if res.Egress[i].Port == "1/1/2" {
			egress2 = &res.Egress[i]
		}
		if res.Egress[i].Port == "1/1/3" {
			egress3 = &res.Egress[i]
		}
	}

	if egress2 == nil {
		t.Fatal("missing egress on 1/1/2")
	}
	if len(egress2.Frame.Tags) != 1 {
		t.Fatalf("1/1/2 tag count = %d, want 1", len(egress2.Frame.Tags))
	}
	tag2 := egress2.Frame.Tags[0]
	if tag2.VID != 20 || tag2.PCP != 5 || !tag2.DEI {
		t.Errorf("1/1/2 tag = %+v, want VID 20, PCP 5, DEI true", tag2)
	}

	if egress3 == nil {
		t.Fatal("missing egress on 1/1/3")
	}
	if len(egress3.Frame.Tags) != 0 {
		t.Errorf("1/1/3 tag count = %d, want 0", len(egress3.Frame.Tags))
	}
}

func TestKnownUnicastForwardingAndSamePortDrop(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	br := bridge.New(cfg, ports)
	initialFrame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("init"),
	}
	br.Forward(testTime0, "1/1/1", initialFrame)

	returnFrame := ethernet.Frame{
		Dst:       macA,
		Src:       macB,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("reply"),
	}
	resForward := br.Forward(testTime0, "1/1/2", returnFrame)
	if resForward.Outcome != trace.Forwarded {
		t.Fatalf("resForward.Outcome = %q, want %q", resForward.Outcome, trace.Forwarded)
	}
	if len(resForward.Egress) != 1 || resForward.Egress[0].Port != "1/1/1" {
		t.Errorf("resForward.Egress = %+v, want single egress on 1/1/1", resForward.Egress)
	}

	resSamePort := br.Forward(testTime0, "1/1/1", returnFrame)
	if resSamePort.Outcome != trace.Dropped {
		t.Fatalf("resSamePort.Outcome = %q, want %q", resSamePort.Outcome, trace.Dropped)
	}
	if resSamePort.Reason != bridge.ReasonSamePort {
		t.Errorf("resSamePort.Reason = %q, want %q", resSamePort.Reason, bridge.ReasonSamePort)
	}
}

func TestDownPortNeitherIngressesNorEgresses(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	br := bridge.New(cfg, ports)
	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}

	resIngressDown := br.Forward(testTime0, "1/1/3", frame)
	if resIngressDown.Outcome != trace.Dropped {
		t.Fatalf("resIngressDown.Outcome = %q, want %q", resIngressDown.Outcome, trace.Dropped)
	}
	if resIngressDown.Reason != port.ReasonPortDown {
		t.Errorf("resIngressDown.Reason = %q, want %q", resIngressDown.Reason, port.ReasonPortDown)
	}

	resFlood := br.Forward(testTime0, "1/1/1", frame)
	for _, eg := range resFlood.Egress {
		if eg.Port == "1/1/3" {
			t.Errorf("down port 1/1/3 unexpectedly present in flood egress")
		}
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

	br := bridge.New(cfg, ports)
	learnFrame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("learn"),
	}
	br.Forward(testTime0, "1/1/1", learnFrame)

	br.Learn([]bridge.Seed{
		{
			FID:       10,
			MAC:       macC,
			Port:      "1/1/2",
			Static:    true,
			LearnedAt: testTime0,
		},
	})

	t299 := testTime0.Add(299 * time.Second)
	br.Age(t299)

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
	br.Age(t301)

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

func TestReservedDestinationAddressesDropped(t *testing.T) {
	ports := buildTestPorts(t, 2)
	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	br := bridge.New(cfg, ports)
	reservedMAC := netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}
	frame := ethernet.Frame{
		Dst:       reservedMAC,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("bpdu"),
	}

	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Dropped)
	}
	if res.Reason != bridge.ReasonReservedAddress {
		t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonReservedAddress)
	}
	if len(br.Entries()) != 0 {
		t.Errorf("len(br.Entries()) = %d, want 0", len(br.Entries()))
	}
}

func TestLAGMemberResolutionAndForwarding(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "1/1/6", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"lag1":  {Tagged: []vlan.ID{10}},
			},
		},
	}

	br := bridge.New(cfg, ports)
	br.SetSelector(stubSelector{member: "1/1/5", ok: true})
	frameFromMember := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: 10}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("from-lag"),
	}

	resMember := br.Forward(testTime0, "1/1/6", frameFromMember)
	if resMember.Ingress != "lag1" {
		t.Errorf("resMember.Ingress = %q, want lag1", resMember.Ingress)
	}

	frameFromPhysical := ethernet.Frame{
		Dst:       macC,
		Src:       macB,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("to-lag"),
	}
	resFlood := br.Forward(testTime0, "1/1/1", frameFromPhysical)
	if resFlood.Outcome != trace.Flooded {
		t.Fatalf("resFlood.Outcome = %q, want Flooded", resFlood.Outcome)
	}

	var lagEgress *bridge.Egress
	for i := range resFlood.Egress {
		if resFlood.Egress[i].Port == "lag1" {
			lagEgress = &resFlood.Egress[i]
		}
	}
	if lagEgress == nil {
		t.Fatal("missing egress on lag1")
	}
	if lagEgress.Member != "1/1/5" {
		t.Errorf("lagEgress.Member = %q, want lowest member 1/1/5", lagEgress.Member)
	}
}

type stubSelector struct {
	member string
	ok     bool
}

func (s stubSelector) Select(_ string, _ ethernet.Frame, _ vlan.ID) (string, bool) {
	return s.member, s.ok
}

func TestSelectorOnLAGEgress(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "1/1/6", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"lag1":  {Tagged: []vlan.ID{10}},
			},
		},
	}

	frame := ethernet.Frame{
		Dst:       macC,
		Src:       macB,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}

	t.Run("selector returning second member fills Egress.Member", func(t *testing.T) {
		br := bridge.New(cfg, ports)
		br.SetSelector(stubSelector{member: "1/1/6", ok: true})
		res := br.Forward(testTime0, "1/1/1", frame)
		if res.Outcome != trace.Flooded {
			t.Fatalf("res.Outcome = %q, want Flooded", res.Outcome)
		}
		if len(res.Egress) != 1 {
			t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
		}
		if got, want := res.Egress[0].Member, "1/1/6"; got != want {
			t.Errorf("Egress.Member = %q, want %q", got, want)
		}
	})

	t.Run("selector returning false records Egress.Dropped no-member", func(t *testing.T) {
		br := bridge.New(cfg, ports)
		br.SetSelector(stubSelector{member: "", ok: false})
		res := br.Forward(testTime0, "1/1/1", frame)
		if res.Outcome != trace.Dropped {
			t.Fatalf("res.Outcome = %q, want Dropped", res.Outcome)
		}
		if len(res.Egress) != 1 {
			t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
		}
		if got, want := res.Egress[0].Dropped, bridge.ReasonNoMember; got != want {
			t.Errorf("Egress.Dropped = %q, want %q", got, want)
		}
		if res.Reason != bridge.ReasonNoMember {
			t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonNoMember)
		}
	})

	t.Run("no selector on bridge with LAG port records no-member", func(t *testing.T) {
		br := bridge.New(cfg, ports)
		res := br.Forward(testTime0, "1/1/1", frame)
		if res.Outcome != trace.Dropped {
			t.Fatalf("res.Outcome = %q, want Dropped", res.Outcome)
		}
		if len(res.Egress) != 1 {
			t.Fatalf("len(res.Egress) = %d, want 1", len(res.Egress))
		}
		if got, want := res.Egress[0].Dropped, bridge.ReasonNoMember; got != want {
			t.Errorf("Egress.Dropped = %q, want %q", got, want)
		}
		if res.Reason != bridge.ReasonNoMember {
			t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonNoMember)
		}
	})
}

func TestBridgeWithoutVLANRelaysByAddressAlone(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{}

	br := bridge.New(cfg, ports)
	frameTagged := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: 20}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("tagged"),
	}

	resFlood := br.Forward(testTime0, "1/1/1", frameTagged)
	if resFlood.Outcome != trace.Flooded {
		t.Fatalf("resFlood.Outcome = %q, want Flooded", resFlood.Outcome)
	}
	if len(resFlood.Egress) != 3 {
		t.Fatalf("egress count = %d, want 3", len(resFlood.Egress))
	}
	for _, eg := range resFlood.Egress {
		if len(eg.Frame.Tags) != 1 || eg.Frame.Tags[0].VID != 20 {
			t.Errorf("egress port %s frame tags = %+v, want untouched VID 20", eg.Port, eg.Frame.Tags)
		}
	}

	entries := br.Entries()
	if len(entries) != 1 || entries[0].FID != 0 || entries[0].MAC != macA || entries[0].Port != "1/1/1" {
		t.Fatalf("FDB entries = %+v, want single FID 0 entry on 1/1/1", entries)
	}

	frameUntagged := ethernet.Frame{
		Dst:       macA,
		Src:       macC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("reply"),
	}
	resUnicast := br.Forward(testTime0, "1/1/3", frameUntagged)
	if resUnicast.Outcome != trace.Forwarded {
		t.Fatalf("resUnicast.Outcome = %q, want Forwarded", resUnicast.Outcome)
	}
	if len(resUnicast.Egress) != 1 || resUnicast.Egress[0].Port != "1/1/1" {
		t.Errorf("resUnicast.Egress = %+v, want single egress to 1/1/1", resUnicast.Egress)
	}
	if len(resUnicast.Egress[0].Frame.Tags) != 0 {
		t.Errorf("resUnicast tags = %+v, want untagged", resUnicast.Egress[0].Frame.Tags)
	}
}

func TestValidationRules(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	tests := []struct {
		name          string
		cfg           bridge.Config
		wantAttrKey   string
		wantAttrValue any
	}{
		{
			name: "switchport naming absent port",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/99": {Tagged: []vlan.ID{10}},
					},
				},
			},
			wantAttrKey:   "port",
			wantAttrValue: "1/1/99",
		},
		{
			name: "switchport naming LAG member",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/2": {Tagged: []vlan.ID{10}},
					},
				},
			},
			wantAttrKey:   "member",
			wantAttrValue: "1/1/2",
		},
		{
			name: "VLAN in both tagged and untagged sets",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {
							Tagged:   []vlan.ID{10},
							Untagged: []vlan.ID{10},
						},
					},
				},
			},
			wantAttrKey:   "vlan",
			wantAttrValue: vlan.ID(10),
		},
		{
			name: "VLAN table ID outside valid range",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{0: "invalid"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Tagged: []vlan.ID{10}},
					},
				},
			},
			wantAttrKey:   "vlan",
			wantAttrValue: vlan.ID(0),
		},
		{
			name: "PVID outside valid range",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: mustVLAN(0)},
					},
				},
			},
			wantAttrKey:   "vlan",
			wantAttrValue: vlan.ID(0),
		},
		{
			name: "switchport with no VLAN table",
			cfg: bridge.Config{
				VLAN: &bridge.VLAN{
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Tagged: []vlan.ID{10}},
					},
				},
			},
			wantAttrKey:   "port",
			wantAttrValue: "1/1/1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(ports)
			if err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			attrs := errs.Attributes(err)
			val, ok := attrs[tc.wantAttrKey]
			if !ok {
				t.Fatalf("missing attribute %q in error attributes %v", tc.wantAttrKey, attrs)
			}
			if val != tc.wantAttrValue {
				t.Errorf("attribute %q = %v (%T), want %v (%T)", tc.wantAttrKey, val, val, tc.wantAttrValue, tc.wantAttrValue)
			}
		})
	}
}

func TestOversizedFrameDropsAtEgress(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 1500})
	builder.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: 9000})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

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

	br := bridge.New(cfg, ports)
	oversizedPayload := make([]byte, 1600)
	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   oversizedPayload,
	}

	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want Flooded", res.Outcome)
	}

	var egress2, egress3 *bridge.Egress
	for i := range res.Egress {
		if res.Egress[i].Port == "1/1/2" {
			egress2 = &res.Egress[i]
		}
		if res.Egress[i].Port == "1/1/3" {
			egress3 = &res.Egress[i]
		}
	}

	if egress2 == nil {
		t.Fatal("missing egress on 1/1/2")
	}
	if egress2.Dropped != port.ReasonMTUExceeded {
		t.Errorf("1/1/2 Dropped = %q, want %q", egress2.Dropped, port.ReasonMTUExceeded)
	}

	if egress3 == nil {
		t.Fatal("missing egress on 1/1/3")
	}
	if egress3.Dropped != "" {
		t.Errorf("1/1/3 Dropped = %q, want empty", egress3.Dropped)
	}
}

func TestSTaggedFrameCarriedThroughBothEgressForms(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{20: "vlan20"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(20), Untagged: []vlan.ID{20}},
				"1/1/2": {Tagged: []vlan.ID{20}},
				"1/1/3": {Untagged: []vlan.ID{20}},
			},
		},
	}

	br := bridge.New(cfg, ports)
	sTaggedFrame := ethernet.Frame{
		Dst: macB,
		Src: macA,
		Tags: []vlan.Tag{
			{
				TPID: uint16(ethernet.EtherTypeProviderBridging),
				VID:  100,
				PCP:  3,
			},
		},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("payload"),
	}

	res := br.Forward(testTime0, "1/1/1", sTaggedFrame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want Flooded", res.Outcome)
	}

	var egressTagged, egressUntagged *bridge.Egress
	for i := range res.Egress {
		if res.Egress[i].Port == "1/1/2" {
			egressTagged = &res.Egress[i]
		}
		if res.Egress[i].Port == "1/1/3" {
			egressUntagged = &res.Egress[i]
		}
	}

	if egressTagged == nil || egressUntagged == nil {
		t.Fatalf("missing egress results: tagged=%v, untagged=%v", egressTagged, egressUntagged)
	}

	if len(egressTagged.Frame.Tags) != 2 {
		t.Fatalf("tagged port tag count = %d, want 2", len(egressTagged.Frame.Tags))
	}
	if egressTagged.Frame.Tags[0].TPID != uint16(ethernet.EtherTypeDot1Q) || egressTagged.Frame.Tags[0].VID != 20 {
		t.Errorf("outer tag = %+v, want C-TAG VID 20", egressTagged.Frame.Tags[0])
	}
	if egressTagged.Frame.Tags[1].TPID != uint16(ethernet.EtherTypeProviderBridging) || egressTagged.Frame.Tags[1].VID != 100 {
		t.Errorf("inner tag = %+v, want S-TAG VID 100", egressTagged.Frame.Tags[1])
	}

	if len(egressUntagged.Frame.Tags) != 1 {
		t.Fatalf("untagged port tag count = %d, want 1", len(egressUntagged.Frame.Tags))
	}
	if egressUntagged.Frame.Tags[0].TPID != uint16(ethernet.EtherTypeProviderBridging) || egressUntagged.Frame.Tags[0].VID != 100 {
		t.Errorf("untagged port tag = %+v, want S-TAG VID 100", egressUntagged.Frame.Tags[0])
	}
}

func TestFDBHitWithDownPortDropsPortDown(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			},
		},
	}

	br := bridge.New(cfg, ports)
	br.Learn([]bridge.Seed{
		{
			FID:       10,
			MAC:       macB,
			Port:      "1/1/2",
			Static:    true,
			LearnedAt: testTime0,
		},
	})

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("hit-down"),
	}

	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("res.Outcome = %q, want Dropped", res.Outcome)
	}
	if res.Reason != port.ReasonPortDown {
		t.Errorf("res.Reason = %q, want %q", res.Reason, port.ReasonPortDown)
	}
	if len(res.Egress) != 1 || res.Egress[0].Dropped != port.ReasonPortDown {
		t.Errorf("res.Egress = %+v, want 1 entry with ReasonPortDown", res.Egress)
	}
}

func TestDiff(t *testing.T) {
	cfgA := bridge.Config{
		AgingTime: 300 * time.Second,
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10",
				20: "vlan20",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {
					PVID:     mustVLAN(10),
					Untagged: []vlan.ID{10},
				},
				"1/1/2": {
					PVID:     mustVLAN(10),
					Untagged: []vlan.ID{10},
				},
			},
		},
	}

	cfgB := bridge.Config{
		AgingTime: 600 * time.Second,
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10-renamed",
				30: "vlan30",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/2": {
					PVID:             mustVLAN(20),
					Tagged:           []vlan.ID{30},
					Untagged:         []vlan.ID{20},
					IngressFiltering: true,
					Admission:        bridge.TaggedOnly,
				},
				"1/1/3": {
					PVID:     mustVLAN(30),
					Untagged: []vlan.ID{30},
				},
			},
		},
	}

	changes := bridge.Diff(cfgA, cfgB)

	hasChange := func(field string, kind, key string) bool {
		for _, ch := range changes {
			if ch.Field == field && ch.Subject.Kind == kind && ch.Subject.Key == key {
				return true
			}
		}

		return false
	}

	if !hasChange("aging_time", "bridge", "") {
		t.Errorf("missing aging_time change")
	}
	if !hasChange("name", "vlan", "10") {
		t.Errorf("missing vlan 10 name rename")
	}
	if !hasChange("", "vlan", "20") {
		t.Errorf("missing vlan 20 removal")
	}
	if !hasChange("", "vlan", "30") {
		t.Errorf("missing vlan 30 addition")
	}
	if !hasChange("", "port", "1/1/1") {
		t.Errorf("missing port 1/1/1 removal")
	}
	if !hasChange("", "port", "1/1/3") {
		t.Errorf("missing port 1/1/3 addition")
	}
	if !hasChange("pvid", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 pvid change")
	}
	if !hasChange("untagged_vlan_ids", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 untagged_vlan_ids change")
	}
	if !hasChange("tagged_vlan_ids", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 tagged_vlan_ids change")
	}
	if !hasChange("ingress_filtering", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 ingress_filtering change")
	}
	if !hasChange("frame_admission", "port", "1/1/2") {
		t.Errorf("missing port 1/1/2 frame_admission change")
	}
}

type testGate struct {
	learns   map[string]bool
	forwards map[string]bool
}

func (g testGate) Learns(p string) bool {
	if g.learns == nil {
		return true
	}

	return g.learns[p]
}

func (g testGate) Forwards(p string) bool {
	if g.forwards == nil {
		return true
	}

	return g.forwards[p]
}

func TestGateBlocksIngressWithoutLearning(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := bridge.New(bridge.Config{}, ports)
	br.SetGate(testGate{
		learns:   map[string]bool{"1/1/1": false, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	})

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Dropped)
	}
	if res.Reason != bridge.ReasonPortBlocked {
		t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonPortBlocked)
	}
	if len(br.Entries()) != 0 {
		t.Errorf("len(br.Entries()) = %d, want 0", len(br.Entries()))
	}
}

func TestGateLearningOnlyIngressLearnsThenDrops(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := bridge.New(bridge.Config{}, ports)
	br.SetGate(testGate{
		learns:   map[string]bool{"1/1/1": true, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	})

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Dropped)
	}
	if res.Reason != bridge.ReasonPortBlocked {
		t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonPortBlocked)
	}
	entries := br.Entries()
	if len(entries) != 1 || entries[0].MAC != macA || entries[0].Port != "1/1/1" {
		t.Fatalf("Entries() = %+v, want learned entry for macA on 1/1/1", entries)
	}
}

func TestGateBlocksEgress(t *testing.T) {
	ports := buildTestPorts(t, 3)
	br := bridge.New(bridge.Config{}, ports)
	br.SetGate(testGate{
		learns:   map[string]bool{"1/1/1": true, "1/1/2": true, "1/1/3": true},
		forwards: map[string]bool{"1/1/1": true, "1/1/2": false, "1/1/3": true},
	})

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Flooded)
	}
	var egress2, egress3 *bridge.Egress
	for i := range res.Egress {
		if res.Egress[i].Port == "1/1/2" {
			egress2 = &res.Egress[i]
		}
		if res.Egress[i].Port == "1/1/3" {
			egress3 = &res.Egress[i]
		}
	}
	if egress2 == nil || egress2.Dropped != bridge.ReasonPortBlocked {
		t.Errorf("egress on 1/1/2 = %+v, want dropped with %q", egress2, bridge.ReasonPortBlocked)
	}
	if egress3 == nil || egress3.Dropped != "" {
		t.Errorf("egress on 1/1/3 = %+v, want forwarded", egress3)
	}
}

func TestFlushPorts(t *testing.T) {
	ports := buildTestPorts(t, 3)
	br := bridge.New(bridge.Config{}, ports)
	br.Forward(testTime0, "1/1/1", ethernet.Frame{Dst: macB, Src: macA, EtherType: ethernet.EtherTypeIPv4})
	br.Forward(testTime0, "1/1/2", ethernet.Frame{Dst: macA, Src: macB, EtherType: ethernet.EtherTypeIPv4})

	if len(br.Entries()) != 2 {
		t.Fatalf("initial entries count = %d, want 2", len(br.Entries()))
	}

	br.FlushPorts([]string{"1/1/1"})
	entries := br.Entries()
	if len(entries) != 1 || entries[0].Port != "1/1/2" {
		t.Fatalf("after FlushPorts Entries() = %+v, want 1 entry on 1/1/2", entries)
	}
}

func TestSetOperStatusRemovesPortFromFloodSet(t *testing.T) {
	ports := buildTestPorts(t, 3)
	br := bridge.New(bridge.Config{}, ports)

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	resBefore := br.Forward(testTime0, "1/1/1", frame)
	if len(resBefore.Egress) != 2 {
		t.Fatalf("egress count before = %d, want 2", len(resBefore.Egress))
	}

	br.SetOperStatus("1/1/2", port.Down)

	resAfter := br.Forward(testTime0, "1/1/1", frame)
	if len(resAfter.Egress) != 1 || resAfter.Egress[0].Port != "1/1/3" {
		t.Fatalf("egress after SetOperStatus = %+v, want only 1/1/3", resAfter.Egress)
	}
}

func TestEgressEmptyPort(t *testing.T) {
	t.Run("vlan known unicast forwards rather than same-port", func(t *testing.T) {
		ports := buildTestPorts(t, 2)
		cfg := bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				},
			},
		}
		br := bridge.New(cfg, ports)
		br.Learn([]bridge.Seed{
			{MAC: macB, Port: "1/1/1", FID: 10},
		})

		frame := ethernet.Frame{
			Dst:       macB,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("routed"),
		}
		in := bridge.Ingress{
			Port: "",
			FID:  10,
		}

		res := br.Egress(in, frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("res.Outcome = %v, want %v (reason: %s)", res.Outcome, trace.Forwarded, res.Reason)
		}
		if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/1" {
			t.Fatalf("res.Egress = %+v, want 1 entry on 1/1/1", res.Egress)
		}
		if res.Egress[0].Dropped != "" {
			t.Errorf("res.Egress[0].Dropped = %q, want empty", res.Egress[0].Dropped)
		}
	})

	t.Run("vlan flood reaches all members including port with pcp preserved", func(t *testing.T) {
		ports := buildTestPorts(t, 2)
		cfg := bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Tagged: []vlan.ID{10}},
					"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				},
			},
		}
		br := bridge.New(cfg, ports)

		frame := ethernet.Frame{
			Dst:       macB,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("routed"),
		}
		in := bridge.Ingress{
			Port: "",
			FID:  10,
			PCP:  vlan.PCP(5),
		}

		res := br.Egress(in, frame)
		if res.Outcome != trace.Flooded {
			t.Fatalf("res.Outcome = %v, want %v (reason: %s)", res.Outcome, trace.Flooded, res.Reason)
		}
		if len(res.Egress) != 2 {
			t.Fatalf("len(res.Egress) = %d, want 2", len(res.Egress))
		}

		var taggedEgress *bridge.Egress
		for i := range res.Egress {
			if res.Egress[i].Port == "1/1/1" {
				taggedEgress = &res.Egress[i]
			}
		}
		if taggedEgress == nil {
			t.Fatal("missing egress on 1/1/1")
		}
		if len(taggedEgress.Frame.Tags) != 1 {
			t.Fatalf("taggedEgress.Frame.Tags = %+v, want 1 tag", taggedEgress.Frame.Tags)
		}
		if got, want := taggedEgress.Frame.Tags[0].VID, vlan.ID(10); got != want {
			t.Errorf("tag VID = %d, want %d", got, want)
		}
		if got, want := taggedEgress.Frame.Tags[0].PCP, vlan.PCP(5); got != want {
			t.Errorf("tag PCP = %d, want %d", got, want)
		}
	})

	t.Run("non-vlan flood reaches all ports with tags untouched", func(t *testing.T) {
		ports := buildTestPorts(t, 2)
		br := bridge.New(bridge.Config{}, ports)

		origTags := []vlan.Tag{{TPID: 0x8100, VID: 100, PCP: 3, DEI: true}}
		frame := ethernet.Frame{
			Dst:       macB,
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Tags:      origTags,
			Payload:   []byte("routed"),
		}
		in := bridge.Ingress{
			Port: "",
			FID:  0,
		}

		res := br.Egress(in, frame)
		if res.Outcome != trace.Flooded {
			t.Fatalf("res.Outcome = %v, want %v (reason: %s)", res.Outcome, trace.Flooded, res.Reason)
		}
		if len(res.Egress) != 2 {
			t.Fatalf("len(res.Egress) = %d, want 2", len(res.Egress))
		}
		for _, eg := range res.Egress {
			if !slices.Equal(eg.Frame.Tags, origTags) {
				t.Errorf("port %s tags = %+v, want %+v", eg.Port, eg.Frame.Tags, origTags)
			}
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
	br := bridge.New(cfg, ports)

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

	wantEvictedDetail := fmt.Sprintf("evicted %s 1/1/1", macA)
	foundEvictedStep := false
	for _, step := range resC.Steps {
		if step.Layer == port.LayerRelay && step.Op == trace.OpLearn && step.Detail == wantEvictedDetail {
			foundEvictedStep = true
			break
		}
	}
	if !foundEvictedStep {
		t.Errorf("resC.Steps did not contain learn step with detail %q; steps = %+v", wantEvictedDetail, resC.Steps)
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
	br := bridge.New(cfg, ports)

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
	br := bridge.New(cfg, ports)

	br.Learn([]bridge.Seed{
		{FID: 10, MAC: macD, Port: "1/1/3", Static: true, LearnedAt: testTime0},
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

	br.Age(testTime0.Add(301 * time.Second))
	counters = br.Counters()
	if counters.Expired != 1 {
		t.Errorf("Counters().Expired = %d, want 1", counters.Expired)
	}
	entries := br.Entries()
	if len(entries) != 1 {
		t.Fatalf("len(Entries()) after Age = %d, want 1", len(entries))
	}
	if entries[0].MAC != macD || !entries[0].Static {
		t.Errorf("Entries()[0] = %+v, want D with Static: true", entries[0])
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

func TestValidateRefusesNegativeMaxEntries(t *testing.T) {
	ports := buildTestPorts(t, 2)

	t.Run("with nil VLAN", func(t *testing.T) {
		cfg := bridge.Config{MaxEntries: -1}
		err := cfg.Validate(ports)
		if err == nil {
			t.Fatal("Validate with nil VLAN and MaxEntries -1 succeeded, want error")
		}
		if attrs := errs.Attributes(err); attrs["max_entries"] != -1 {
			t.Errorf("error attrs = %+v, want max_entries = -1", attrs)
		}
	})

	t.Run("with VLAN table", func(t *testing.T) {
		cfg := bridge.Config{
			MaxEntries: -1,
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "ten"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Untagged: []vlan.ID{10}},
				},
			},
		}
		err := cfg.Validate(ports)
		if err == nil {
			t.Fatal("Validate with VLAN table and MaxEntries -1 succeeded, want error")
		}
		if attrs := errs.Attributes(err); attrs["max_entries"] != -1 {
			t.Errorf("error attrs = %+v, want max_entries = -1", attrs)
		}
	})
}

func TestDiffReportsMaxEntriesChange(t *testing.T) {
	a := bridge.Config{MaxEntries: 0}
	b := bridge.Config{MaxEntries: 2}
	changes := bridge.Diff(a, b)
	if len(changes) != 1 {
		t.Fatalf("Diff returned %d changes, want 1", len(changes))
	}
	ch := changes[0]
	if ch.Field != "max_entries" || ch.From != bridge.IntFact(0) || ch.To != bridge.IntFact(2) || ch.Layer != port.LayerRelay {
		t.Errorf("Diff change = %+v, want max_entries From: 0 To: 2 at LayerRelay", ch)
	}
}

func TestFloodVLANFloodsWithoutLearning(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		FloodVLANs: []vlan.ID{10},
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
	br := bridge.New(cfg, ports)

	frameA := ethernet.Frame{Dst: macB, Src: macA}
	resA := br.Forward(testTime0, "1/1/1", frameA)

	if entries := br.Entries(); len(entries) != 0 {
		t.Fatalf("Entries() = %+v, want empty", entries)
	}
	if learned := br.Counters().Learned; learned != 0 {
		t.Errorf("Counters().Learned = %d, want 0", learned)
	}
	if resA.Outcome != trace.Flooded {
		t.Errorf("frame A outcome = %s, want Flooded", resA.Outcome)
	}
	if len(resA.Egress) != 2 || resA.Egress[0].Port != "1/1/2" || resA.Egress[1].Port != "1/1/3" {
		t.Errorf("frame A egress = %+v, want flooded to 1/1/2 and 1/1/3", resA.Egress)
	}

	frameB := ethernet.Frame{Dst: macA, Src: macB}
	resB := br.Forward(testTime0.Add(time.Second), "1/1/2", frameB)

	if resB.Outcome != trace.Flooded {
		t.Errorf("frame B outcome = %s, want Flooded", resB.Outcome)
	}
	if len(resB.Egress) != 2 || resB.Egress[0].Port != "1/1/1" || resB.Egress[1].Port != "1/1/3" {
		t.Errorf("frame B egress = %+v, want flooded to 1/1/1 and 1/1/3", resB.Egress)
	}

	foundLookupStep := false
	for _, step := range resB.Steps {
		if step.Layer == port.LayerRelay && step.Op == trace.OpLookup && step.Detail == "flood vlan" {
			foundLookupStep = true
			break
		}
	}
	if !foundLookupStep {
		t.Errorf("frame B steps = %+v, want lookup step with detail %q", resB.Steps, "flood vlan")
	}
}

func TestProtectedPortsDropTrafficBetweenProtectedPorts(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		ProtectedPorts: []string{"1/1/1", "1/1/2"},
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
	br := bridge.New(cfg, ports)

	br.Learn([]bridge.Seed{
		{FID: 10, MAC: macB, Port: "1/1/2", LearnedAt: testTime0},
		{FID: 10, MAC: macC, Port: "1/1/3", LearnedAt: testTime0},
	})

	resUnicastProtected := br.Forward(testTime0, "1/1/1", ethernet.Frame{Dst: macB, Src: macA})
	if resUnicastProtected.Outcome != trace.Dropped || resUnicastProtected.Reason != bridge.ReasonProtected {
		t.Errorf("unicast between protected ports = %s/%s, want Dropped/%s",
			resUnicastProtected.Outcome, resUnicastProtected.Reason, bridge.ReasonProtected)
	}
	if len(resUnicastProtected.Egress) != 1 || resUnicastProtected.Egress[0].Port != "1/1/2" || resUnicastProtected.Egress[0].Dropped != bridge.ReasonProtected {
		t.Errorf("unicast between protected ports egress = %+v, want 1/1/2 dropped with %s",
			resUnicastProtected.Egress, bridge.ReasonProtected)
	}

	resFlood := br.Forward(testTime0, "1/1/1", ethernet.Frame{Dst: macD, Src: macA})
	if resFlood.Outcome != trace.Flooded {
		t.Errorf("flood outcome = %s, want Flooded", resFlood.Outcome)
	}
	var foundProtectedCandidate, reachedUnprotected bool
	for _, eg := range resFlood.Egress {
		if eg.Port == "1/1/2" && eg.Dropped == bridge.ReasonProtected {
			foundProtectedCandidate = true
		}
		if eg.Port == "1/1/3" && eg.Dropped == "" {
			reachedUnprotected = true
		}
	}
	if !foundProtectedCandidate || !reachedUnprotected {
		t.Errorf("flood egress = %+v, want 1/1/2 dropped protected and 1/1/3 forwarded", resFlood.Egress)
	}

	resToUnprotected := br.Forward(testTime0, "1/1/1", ethernet.Frame{Dst: macC, Src: macA})
	if resToUnprotected.Outcome != trace.Forwarded {
		t.Errorf("unicast from protected to unprotected outcome = %s, want Forwarded", resToUnprotected.Outcome)
	}
	if len(resToUnprotected.Egress) != 1 || resToUnprotected.Egress[0].Port != "1/1/3" || resToUnprotected.Egress[0].Dropped != "" {
		t.Errorf("unicast to 1/1/3 egress = %+v, want forwarded to 1/1/3", resToUnprotected.Egress)
	}

	resFromUnprotected := br.Forward(testTime0, "1/1/3", ethernet.Frame{
		Dst:  macB,
		Src:  macC,
		Tags: []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}},
	})
	if resFromUnprotected.Outcome != trace.Forwarded {
		t.Errorf("unicast from unprotected to protected outcome = %s, want Forwarded", resFromUnprotected.Outcome)
	}
	if len(resFromUnprotected.Egress) != 1 || resFromUnprotected.Egress[0].Port != "1/1/2" || resFromUnprotected.Egress[0].Dropped != "" {
		t.Errorf("unicast from 1/1/3 to 1/1/2 egress = %+v, want forwarded to 1/1/2", resFromUnprotected.Egress)
	}
}

func TestForwardBPDUFloodsReservedBridgeAddresses(t *testing.T) {
	ports := buildTestPorts(t, 4)
	bpduMAC := netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}
	frame := ethernet.Frame{Dst: bpduMAC, Src: macA}

	t.Run("forward bpdu false drops reserved address", func(t *testing.T) {
		cfg := bridge.Config{
			ForwardBPDU: false,
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
		br := bridge.New(cfg, ports)
		res := br.Forward(testTime0, "1/1/1", frame)
		if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonReservedAddress {
			t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, bridge.ReasonReservedAddress)
		}
		if len(br.Entries()) != 0 {
			t.Errorf("len(Entries()) = %d, want 0", len(br.Entries()))
		}
	})

	t.Run("forward bpdu true floods and learns source", func(t *testing.T) {
		cfg := bridge.Config{
			ForwardBPDU: true,
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
		br := bridge.New(cfg, ports)
		res := br.Forward(testTime0, "1/1/1", frame)
		if res.Outcome != trace.Flooded {
			t.Errorf("Forward = %s, want Flooded", res.Outcome)
		}
		if len(res.Egress) != 2 || res.Egress[0].Port != "1/1/2" || res.Egress[1].Port != "1/1/3" {
			t.Errorf("Egress = %+v, want flooded to 1/1/2 and 1/1/3", res.Egress)
		}
		entries := br.Entries()
		if len(entries) != 1 || entries[0].MAC != macA || entries[0].Port != "1/1/1" {
			t.Errorf("Entries() = %+v, want macA learned on 1/1/1", entries)
		}
	})
}

func TestValidateFloodVLANsAndProtectedPorts(t *testing.T) {
	ports := buildTestPorts(t, 4)

	tableVLAN := &bridge.VLAN{
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
	}

	cases := []struct {
		name string
		vlan *bridge.VLAN
	}{
		{name: "with nil VLAN", vlan: nil},
		{name: "with VLAN table", vlan: tableVLAN},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgFlood := bridge.Config{
				FloodVLANs: []vlan.ID{0},
				VLAN:       tc.vlan,
			}
			if err := cfgFlood.Validate(ports); err == nil {
				t.Error("Validate with FloodVLANs {0} succeeded, want error")
			}

			cfgProt := bridge.Config{
				ProtectedPorts: []string{"1/1/9"},
				VLAN:           tc.vlan,
			}
			if err := cfgProt.Validate(ports); err == nil {
				t.Error("Validate with ProtectedPorts {1/1/9} succeeded, want error")
			}
		})
	}

	t.Run("protected port naming a LAG member is refused", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
		tbl, err := b.Build()
		if err != nil {
			t.Fatal(err)
		}
		cfg := bridge.Config{ProtectedPorts: []string{"1/1/2"}}
		if err := cfg.Validate(tbl); err == nil {
			t.Error("Validate with protected port naming LAG member succeeded, want error")
		}
	})
}

func TestDiffReportsFloodVLANsProtectedPortsAndForwardBPDU(t *testing.T) {
	t.Run("flood_vlans order change reports nothing", func(t *testing.T) {
		a := bridge.Config{FloodVLANs: []vlan.ID{10, 20}}
		b := bridge.Config{FloodVLANs: []vlan.ID{20, 10}}
		if changes := bridge.Diff(a, b); len(changes) != 0 {
			t.Errorf("Diff = %+v, want no changes when only flood_vlans order changes", changes)
		}
	})

	t.Run("flood_vlans change is reported sorted", func(t *testing.T) {
		a := bridge.Config{FloodVLANs: []vlan.ID{20, 10}}
		b := bridge.Config{FloodVLANs: []vlan.ID{30, 10}}
		changes := bridge.Diff(a, b)
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "flood_vlans" || ch.Layer != port.LayerRelay {
			t.Errorf("change = %+v, want flood_vlans at LayerRelay", ch)
		}
		wantFrom := []vlan.ID{10, 20}
		wantTo := []vlan.ID{10, 30}
		if !slices.Equal(ch.From.(bridge.VLANsFact).IDs(), wantFrom) || !slices.Equal(ch.To.(bridge.VLANsFact).IDs(), wantTo) {
			t.Errorf("From = %v, To = %v, want From = %v, To = %v", ch.From, ch.To, wantFrom, wantTo)
		}
	})

	t.Run("protected_ports order change reports nothing", func(t *testing.T) {
		a := bridge.Config{ProtectedPorts: []string{"1/1/2", "1/1/1"}}
		b := bridge.Config{ProtectedPorts: []string{"1/1/1", "1/1/2"}}
		if changes := bridge.Diff(a, b); len(changes) != 0 {
			t.Errorf("Diff = %+v, want no changes when only protected_ports order changes", changes)
		}
	})

	t.Run("protected_ports change is reported sorted", func(t *testing.T) {
		a := bridge.Config{ProtectedPorts: []string{"1/1/2", "1/1/1"}}
		b := bridge.Config{ProtectedPorts: []string{"1/1/3", "1/1/1"}}
		changes := bridge.Diff(a, b)
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "protected_ports" || ch.Layer != port.LayerRelay {
			t.Errorf("change = %+v, want protected_ports at LayerRelay", ch)
		}
		wantFrom := []string{"1/1/1", "1/1/2"}
		wantTo := []string{"1/1/1", "1/1/3"}
		if !slices.Equal(ch.From.(bridge.StringsFact).Strings(), wantFrom) || !slices.Equal(ch.To.(bridge.StringsFact).Strings(), wantTo) {
			t.Errorf("From = %v, To = %v, want From = %v, To = %v", ch.From, ch.To, wantFrom, wantTo)
		}
	})

	t.Run("forward_bpdu change is reported", func(t *testing.T) {
		a := bridge.Config{ForwardBPDU: false}
		b := bridge.Config{ForwardBPDU: true}
		changes := bridge.Diff(a, b)
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "forward_bpdu" || ch.From != bridge.BoolFact(false) || ch.To != bridge.BoolFact(true) || ch.Layer != port.LayerRelay {
			t.Errorf("change = %+v, want forward_bpdu From: false To: true at LayerRelay", ch)
		}
	})
}

func TestTunnelPortIngressAndEgress(t *testing.T) {
	ports := buildTestPorts(t, 4)
	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{
				10: "vlan10",
				20: "vlan20",
			},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
				"1/1/3": {Tagged: []vlan.ID{10, 20}},
				"1/1/4": {
					Tunnel: &bridge.Tunnel{
						VID:          10,
						CustomerVIDs: []vlan.ID{100, 200},
					},
				},
			},
		},
	}

	t.Run("customer tagged frame to tagged port emits service tag", func(t *testing.T) {
		br := bridge.New(cfg, ports)
		br.Learn([]bridge.Seed{
			{MAC: macB, Port: "1/1/3", FID: 10, Static: true},
		})

		frame := ethernet.Frame{
			Src: macA,
			Dst: macB,
			Tags: []vlan.Tag{
				{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 100},
			},
		}
		res := br.Forward(testTime0, "1/1/4", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("Forward = %s/%s, want Forwarded", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 1 {
			t.Fatalf("len(Egress) = %d, want 1", len(res.Egress))
		}
		eg := res.Egress[0]
		if eg.Port != "1/1/3" {
			t.Errorf("Egress port = %q, want 1/1/3", eg.Port)
		}
		wantTags := []vlan.Tag{
			{TPID: bridge.DefaultServiceTPID, VID: 10},
			{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 100},
		}
		if !slices.Equal(eg.Frame.Tags, wantTags) {
			t.Errorf("Egress tags = %+v, want %+v", eg.Frame.Tags, wantTags)
		}
	})

	t.Run("customer tagged frame to untagged port pops service tag", func(t *testing.T) {
		br := bridge.New(cfg, ports)
		br.Learn([]bridge.Seed{
			{MAC: macB, Port: "1/1/1", FID: 10, Static: true},
		})

		frame := ethernet.Frame{
			Src: macA,
			Dst: macB,
			Tags: []vlan.Tag{
				{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 100},
			},
		}
		res := br.Forward(testTime0, "1/1/4", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("Forward = %s/%s, want Forwarded", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 1 {
			t.Fatalf("len(Egress) = %d, want 1", len(res.Egress))
		}
		eg := res.Egress[0]
		if eg.Port != "1/1/1" {
			t.Errorf("Egress port = %q, want 1/1/1", eg.Port)
		}
		wantTags := []vlan.Tag{
			{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 100},
		}
		if !slices.Equal(eg.Frame.Tags, wantTags) {
			t.Errorf("Egress tags = %+v, want %+v", eg.Frame.Tags, wantTags)
		}
	})

	t.Run("customer tag outside permitted list is dropped", func(t *testing.T) {
		br := bridge.New(cfg, ports)
		br.Learn([]bridge.Seed{
			{MAC: macB, Port: "1/1/3", FID: 10, Static: true},
		})

		frame := ethernet.Frame{
			Src: macA,
			Dst: macB,
			Tags: []vlan.Tag{
				{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 300},
			},
		}
		res := br.Forward(testTime0, "1/1/4", frame)
		if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonCustomerVLAN {
			t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, bridge.ReasonCustomerVLAN)
		}
	})

	t.Run("untagged frame on tunnel port classifies to tunnel VID", func(t *testing.T) {
		br := bridge.New(cfg, ports)
		br.Learn([]bridge.Seed{
			{MAC: macB, Port: "1/1/3", FID: 10, Static: true},
		})

		frame := ethernet.Frame{
			Src: macA,
			Dst: macB,
		}
		res := br.Forward(testTime0, "1/1/4", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("Forward = %s/%s, want Forwarded", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 1 {
			t.Fatalf("len(Egress) = %d, want 1", len(res.Egress))
		}
		eg := res.Egress[0]
		if eg.Port != "1/1/3" {
			t.Errorf("Egress port = %q, want 1/1/3", eg.Port)
		}
		wantTags := []vlan.Tag{
			{TPID: bridge.DefaultServiceTPID, VID: 10},
		}
		if !slices.Equal(eg.Frame.Tags, wantTags) {
			t.Errorf("Egress tags = %+v, want %+v", eg.Frame.Tags, wantTags)
		}
	})

	t.Run("tagged frame from trunk to tunnel port pops service tag", func(t *testing.T) {
		br := bridge.New(cfg, ports)
		br.Learn([]bridge.Seed{
			{MAC: macA, Port: "1/1/4", FID: 10, Static: true},
		})

		frame := ethernet.Frame{
			Src: macB,
			Dst: macA,
			Tags: []vlan.Tag{
				{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10},
				{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 100},
			},
		}
		res := br.Forward(testTime0, "1/1/3", frame)
		if res.Outcome != trace.Forwarded {
			t.Fatalf("Forward = %s/%s, want Forwarded", res.Outcome, res.Reason)
		}
		if len(res.Egress) != 1 {
			t.Fatalf("len(Egress) = %d, want 1", len(res.Egress))
		}
		eg := res.Egress[0]
		if eg.Port != "1/1/4" {
			t.Errorf("Egress port = %q, want 1/1/4", eg.Port)
		}
		wantTags := []vlan.Tag{
			{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 100},
		}
		if !slices.Equal(eg.Frame.Tags, wantTags) {
			t.Errorf("Egress tags = %+v, want %+v", eg.Frame.Tags, wantTags)
		}
	})

	t.Run("ingress-filtering tunnel port admits its frames", func(t *testing.T) {
		filtCfg := cfg.Clone()
		sw := filtCfg.VLAN.Switchports["1/1/4"]
		sw.IngressFiltering = true
		filtCfg.VLAN.Switchports["1/1/4"] = sw

		br := bridge.New(filtCfg, ports)
		br.Learn([]bridge.Seed{
			{MAC: macB, Port: "1/1/3", FID: 10, Static: true},
		})

		frame := ethernet.Frame{
			Src: macA,
			Dst: macB,
			Tags: []vlan.Tag{
				{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 100},
			},
		}
		res := br.Forward(testTime0, "1/1/4", frame)
		if res.Outcome != trace.Forwarded {
			t.Errorf("Forward = %s/%s, want Forwarded", res.Outcome, res.Reason)
		}
	})
}

func TestPriorityTagPolicyOnUntaggedEgress(t *testing.T) {
	ports := buildTestPorts(t, 4)

	baseCfg := func(policy bridge.PriorityTagPolicy) bridge.Config {
		return bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "vlan10",
					20: "vlan20",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}, PriorityTags: policy},
					"1/1/3": {Tagged: []vlan.ID{10, 20}},
					"1/1/4": {},
				},
			},
		}
	}

	t.Run("frame with nonzero priority", func(t *testing.T) {
		cases := []struct {
			policy   bridge.PriorityTagPolicy
			wantTags []vlan.Tag
		}{
			{
				policy: bridge.PriorityTagsAlways,
				wantTags: []vlan.Tag{
					{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 0, PCP: 5},
				},
			},
			{
				policy: bridge.PriorityTagsIfNonzero,
				wantTags: []vlan.Tag{
					{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 0, PCP: 5},
				},
			},
			{
				policy:   bridge.PriorityTagsNever,
				wantTags: nil,
			},
			{
				policy:   "",
				wantTags: nil,
			},
		}

		for _, tc := range cases {
			t.Run(string(tc.policy), func(t *testing.T) {
				br := bridge.New(baseCfg(tc.policy), ports)
				br.Learn([]bridge.Seed{
					{MAC: macB, Port: "1/1/2", FID: 10, Static: true},
				})

				frame := ethernet.Frame{
					Src: macA,
					Dst: macB,
					Tags: []vlan.Tag{
						{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10, PCP: 5},
					},
				}
				res := br.Forward(testTime0, "1/1/3", frame)
				if res.Outcome != trace.Forwarded {
					t.Fatalf("Forward = %s/%s, want Forwarded", res.Outcome, res.Reason)
				}
				if len(res.Egress) != 1 {
					t.Fatalf("len(Egress) = %d, want 1", len(res.Egress))
				}
				eg := res.Egress[0]
				if eg.Port != "1/1/2" {
					t.Errorf("Egress port = %q, want 1/1/2", eg.Port)
				}
				if !slices.Equal(eg.Frame.Tags, tc.wantTags) {
					t.Errorf("Egress tags = %+v, want %+v", eg.Frame.Tags, tc.wantTags)
				}
			})
		}
	})

	t.Run("frame with zero priority", func(t *testing.T) {
		cases := []struct {
			policy   bridge.PriorityTagPolicy
			wantTags []vlan.Tag
		}{
			{
				policy: bridge.PriorityTagsAlways,
				wantTags: []vlan.Tag{
					{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 0, PCP: 0},
				},
			},
			{
				policy:   bridge.PriorityTagsIfNonzero,
				wantTags: nil,
			},
			{
				policy:   bridge.PriorityTagsNever,
				wantTags: nil,
			},
			{
				policy:   "",
				wantTags: nil,
			},
		}

		for _, tc := range cases {
			t.Run(string(tc.policy), func(t *testing.T) {
				br := bridge.New(baseCfg(tc.policy), ports)
				br.Learn([]bridge.Seed{
					{MAC: macB, Port: "1/1/2", FID: 10, Static: true},
				})

				frame := ethernet.Frame{
					Src: macA,
					Dst: macB,
					Tags: []vlan.Tag{
						{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10, PCP: 0},
					},
				}
				res := br.Forward(testTime0, "1/1/3", frame)
				if res.Outcome != trace.Forwarded {
					t.Fatalf("Forward = %s/%s, want Forwarded", res.Outcome, res.Reason)
				}
				if len(res.Egress) != 1 {
					t.Fatalf("len(Egress) = %d, want 1", len(res.Egress))
				}
				eg := res.Egress[0]
				if eg.Port != "1/1/2" {
					t.Errorf("Egress port = %q, want 1/1/2", eg.Port)
				}
				if !slices.Equal(eg.Frame.Tags, tc.wantTags) {
					t.Errorf("Egress tags = %+v, want %+v", eg.Frame.Tags, tc.wantTags)
				}
			})
		}
	})
}

func TestDiffTunnelAndPriorityTags(t *testing.T) {
	t.Run("tunnel added", func(t *testing.T) {
		a := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {},
			},
		}}
		b := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10}},
			},
		}}
		changes := bridge.Diff(a, b)
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "tunnel" || ch.Layer != port.LayerVlan {
			t.Errorf("change = %+v, want field tunnel at LayerVlan", ch)
		}
		if ch.From != nil {
			t.Errorf("From = %v, want nil", ch.From)
		}
		to, ok := ch.To.(bridge.Tunnel)
		if !ok || to.VID != 10 || len(to.CustomerVIDs) != 0 || to.TPID != bridge.DefaultServiceTPID {
			t.Errorf("To = %+v (%T), want Tunnel{VID: 10} with the TPID in effect", ch.To, ch.To)
		}
	})

	t.Run("customer list order change reports nothing", func(t *testing.T) {
		a := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10, CustomerVIDs: []vlan.ID{100, 200}}},
			},
		}}
		b := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tunnel: &bridge.Tunnel{VID: 10, CustomerVIDs: []vlan.ID{200, 100}}},
			},
		}}
		if changes := bridge.Diff(a, b); len(changes) != 0 {
			t.Errorf("Diff = %+v, want no changes when only customer VIDs order changes", changes)
		}
	})

	t.Run("priority_tags change is reported", func(t *testing.T) {
		a := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PriorityTags: bridge.PriorityTagsNever},
			},
		}}
		b := bridge.Config{VLAN: &bridge.VLAN{
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {PriorityTags: bridge.PriorityTagsAlways},
			},
		}}
		changes := bridge.Diff(a, b)
		if len(changes) != 1 {
			t.Fatalf("len(changes) = %d, want 1", len(changes))
		}
		ch := changes[0]
		if ch.Field != "priority_tags" || ch.From != bridge.PriorityTagsNever || ch.To != bridge.PriorityTagsAlways {
			t.Errorf("change = %+v, want priority_tags From: Never To: Always", ch)
		}
	})
}

func TestValidateTunnelSwitchportAndPriorityTags(t *testing.T) {
	ports := buildTestPorts(t, 4)

	baseVLAN := func(sw bridge.Switchport) *bridge.VLAN {
		return &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": sw,
			},
		}
	}

	t.Run("tunnel with PVID is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel: &bridge.Tunnel{VID: 10},
				PVID:   mustVLAN(10),
			}),
		}
		if err := cfg.Validate(ports); err == nil {
			t.Error("Validate with tunnel and PVID succeeded, want error")
		}
	})

	t.Run("tunnel with tagged VLANs is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel: &bridge.Tunnel{VID: 10},
				Tagged: []vlan.ID{10},
			}),
		}
		if err := cfg.Validate(ports); err == nil {
			t.Error("Validate with tunnel and Tagged succeeded, want error")
		}
	})

	t.Run("tunnel with untagged VLANs is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel:   &bridge.Tunnel{VID: 10},
				Untagged: []vlan.ID{10},
			}),
		}
		if err := cfg.Validate(ports); err == nil {
			t.Error("Validate with tunnel and Untagged succeeded, want error")
		}
	})

	t.Run("tunnel with invalid VID is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel: &bridge.Tunnel{VID: 0},
			}),
		}
		if err := cfg.Validate(ports); err == nil {
			t.Error("Validate with tunnel VID 0 succeeded, want error")
		}
	})

	t.Run("tunnel with invalid customer VID is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				Tunnel: &bridge.Tunnel{VID: 10, CustomerVIDs: []vlan.ID{0}},
			}),
		}
		if err := cfg.Validate(ports); err == nil {
			t.Error("Validate with customer VID 0 succeeded, want error")
		}
	})

	t.Run("unknown PriorityTags is refused", func(t *testing.T) {
		cfg := bridge.Config{
			VLAN: baseVLAN(bridge.Switchport{
				PriorityTags: "Sometimes",
			}),
		}
		if err := cfg.Validate(ports); err == nil {
			t.Error("Validate with PriorityTags 'Sometimes' succeeded, want error")
		}
	})
}

func TestSwitchportCloneWithTunnel(t *testing.T) {
	orig := bridge.Switchport{
		Tunnel: &bridge.Tunnel{
			VID:          10,
			CustomerVIDs: []vlan.ID{100, 200},
			TPID:         0x88A8,
		},
		PriorityTags: bridge.PriorityTagsAlways,
	}

	cloned := orig.Clone()
	orig.Tunnel.VID = 20
	orig.Tunnel.CustomerVIDs[0] = 300

	if cloned.Tunnel.VID != 10 {
		t.Errorf("cloned.Tunnel.VID = %d, want 10", cloned.Tunnel.VID)
	}
	if cloned.Tunnel.CustomerVIDs[0] != 100 {
		t.Errorf("cloned.Tunnel.CustomerVIDs[0] = %d, want 100", cloned.Tunnel.CustomerVIDs[0])
	}
	if cloned.PriorityTags != bridge.PriorityTagsAlways {
		t.Errorf("cloned.PriorityTags = %s, want %s", cloned.PriorityTags, bridge.PriorityTagsAlways)
	}
}

type testGroupResolver struct {
	ports   []string
	decided bool
}

func (r testGroupResolver) Resolve(vlan.ID, ethernet.Frame) ([]string, bool) {
	return r.ports, r.decided
}

func TestGroupResolverSelectsReplicationPorts(t *testing.T) {
	ports := buildTestPorts(t, 4)
	br := bridge.New(bridge.Config{VLAN: &bridge.VLAN{
		Table: map[vlan.ID]string{10: "ten"},
		Switchports: map[string]bridge.Switchport{
			"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/4": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
		},
	}}, ports)
	br.SetGroupResolver(testGroupResolver{ports: []string{"1/1/2", "1/1/4"}, decided: true})

	res := br.Forward(testTime0, "1/1/1", ethernet.Frame{
		Dst:       netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01},
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("group"),
	})

	var got []string
	for _, egress := range res.Egress {
		if egress.Dropped == "" {
			got = append(got, egress.Port)
		}
	}
	if !slices.Equal(got, []string{"1/1/2", "1/1/4"}) {
		t.Errorf("forwarded ports = %v, want [1/1/2 1/1/4]", got)
	}
	if !slices.ContainsFunc(res.Steps, func(step trace.Step) bool {
		return step.Op == trace.OpReplicate && step.Detail == "group members"
	}) {
		t.Errorf("steps = %+v, want group members replication", res.Steps)
	}
}

func TestGroupResolverEmptyDecisionUsesUnregisteredReason(t *testing.T) {
	br := bridge.New(bridge.Config{}, buildTestPorts(t, 2))
	br.SetGroupResolver(testGroupResolver{decided: true})

	res := br.Forward(testTime0, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0x01, 0x00, 0x5e, 0x02, 0x02, 0x02},
		Src: macA,
	})
	if res.Outcome != trace.Dropped || res.Reason != trace.Reason("unregistered") {
		t.Errorf("Forward = %s/%s, want Dropped/unregistered", res.Outcome, res.Reason)
	}
}

func TestEgressToUsesFloodReplicationRules(t *testing.T) {
	ports := buildTestPorts(t, 3)
	br := bridge.New(bridge.Config{VLAN: &bridge.VLAN{
		Table: map[vlan.ID]string{10: "ten"},
		Switchports: map[string]bridge.Switchport{
			"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/2": {Tagged: []vlan.ID{10}},
			"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
		},
	}}, ports)
	br.SetGate(testGate{forwards: map[string]bool{
		"1/1/1": true,
		"1/1/2": false,
		"1/1/3": true,
	}})

	res := br.EgressTo(bridge.Ingress{Port: "1/1/1", FID: 10, PCP: 5}, ethernet.Frame{
		Dst: netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01},
		Src: macA,
	}, []string{"1/1/2", "1/1/3"}, bridge.ReasonNoEgress)
	if len(res.Egress) != 2 {
		t.Fatalf("EgressTo egress = %+v, want two candidates", res.Egress)
	}
	if got := res.Egress[0]; got.Port != "1/1/2" || got.Dropped != bridge.ReasonPortBlocked || len(got.Frame.Tags) != 1 || got.Frame.Tags[0].VID != 10 {
		t.Errorf("tagged blocked egress = %+v, want tagged VLAN 10 and port-blocked", got)
	}
	if got := res.Egress[1]; got.Port != "1/1/3" || got.Dropped != "" || len(got.Frame.Tags) != 0 {
		t.Errorf("untagged forwarded egress = %+v, want untagged forwarding", got)
	}
}

func TestLearnSeedsCountAndKeepTheBound(t *testing.T) {
	ports := buildTestPorts(t, 4)
	br := bridge.New(bridge.Config{MaxEntries: 2}, ports)

	br.Learn([]bridge.Seed{
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

	br.Learn([]bridge.Seed{{MAC: macB, Port: "1/1/3", LearnedAt: testTime0.Add(3 * time.Second)}})
	if got := br.Counters(); got.Moved != 1 || got.Learned != 3 || got.Evicted != 1 {
		t.Fatalf("Counters() after a seed moved B = %+v, want Moved 1 and nothing else changed", got)
	}

	// A removal frees a slot, so the next new seed evicts nothing.
	if !br.Forget(0, macC) {
		t.Fatal("Forget(C) = false, want true")
	}
	br.Learn([]bridge.Seed{{MAC: macD, Port: "1/1/4", LearnedAt: testTime0.Add(4 * time.Second)}})
	if got := br.Counters(); got.Evicted != 1 || got.Learned != 4 {
		t.Fatalf("Counters() after Forget then a new seed = %+v, want Learned 4, Evicted 1", got)
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()

	cfg := bridge.Config{
		FloodVLANs:     []vlan.ID{20, 10, 20},
		ProtectedPorts: []string{"1/1/2", "1/1/1", "1/1/2"},
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {
					Tagged:   []vlan.ID{30, 20, 20},
					Untagged: []vlan.ID{10, 10},
					Tunnel: &bridge.Tunnel{
						VID:          100,
						CustomerVIDs: []vlan.ID{200, 100, 200},
					},
				},
			},
		},
	}

	norm := cfg.Normalize()
	if norm.AgingTime != bridge.DefaultAgingTime {
		t.Errorf("AgingTime: got %v, want %v", norm.AgingTime, bridge.DefaultAgingTime)
	}
	wantFlood := []vlan.ID{10, 20}
	if !slices.Equal(norm.FloodVLANs, wantFlood) {
		t.Errorf("FloodVLANs: got %v, want %v", norm.FloodVLANs, wantFlood)
	}
	wantProt := []string{"1/1/1", "1/1/2"}
	if !slices.Equal(norm.ProtectedPorts, wantProt) {
		t.Errorf("ProtectedPorts: got %v, want %v", norm.ProtectedPorts, wantProt)
	}

	sw := norm.VLAN.Switchports["1/1/1"]
	if sw.Admission != bridge.All {
		t.Errorf("Admission: got %v, want %v", sw.Admission, bridge.All)
	}
	if sw.PriorityTags != bridge.PriorityTagsNever {
		t.Errorf("PriorityTags: got %v, want %v", sw.PriorityTags, bridge.PriorityTagsNever)
	}
	wantTagged := []vlan.ID{20, 30}
	if !slices.Equal(sw.Tagged, wantTagged) {
		t.Errorf("Tagged: got %v, want %v", sw.Tagged, wantTagged)
	}
	wantUntagged := []vlan.ID{10}
	if !slices.Equal(sw.Untagged, wantUntagged) {
		t.Errorf("Untagged: got %v, want %v", sw.Untagged, wantUntagged)
	}
	if sw.Tunnel.TPID != bridge.DefaultServiceTPID {
		t.Errorf("Tunnel TPID: got 0x%04x, want 0x%04x", sw.Tunnel.TPID, bridge.DefaultServiceTPID)
	}
	wantCust := []vlan.ID{100, 200}
	if !slices.Equal(sw.Tunnel.CustomerVIDs, wantCust) {
		t.Errorf("CustomerVIDs: got %v, want %v", sw.Tunnel.CustomerVIDs, wantCust)
	}
}

func TestValidateEnumDomains(t *testing.T) {
	t.Parallel()

	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	cfg := bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Admission: "InvalidAdmission"},
			},
		},
	}
	if err := cfg.Validate(ports); err == nil {
		t.Fatal("Validate() succeeded for invalid Admission, want error")
	}
}
