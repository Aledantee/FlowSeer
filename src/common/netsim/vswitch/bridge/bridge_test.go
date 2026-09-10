package bridge_test

import (
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
	if resIngressDown.Reason != bridge.ReasonPortDown {
		t.Errorf("resIngressDown.Reason = %q, want %q", resIngressDown.Reason, bridge.ReasonPortDown)
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
	if egress2.Dropped != bridge.ReasonMTUExceeded {
		t.Errorf("1/1/2 Dropped = %q, want %q", egress2.Dropped, bridge.ReasonMTUExceeded)
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
	if res.Reason != bridge.ReasonPortDown {
		t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonPortDown)
	}
	if len(res.Egress) != 1 || res.Egress[0].Dropped != bridge.ReasonPortDown {
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
