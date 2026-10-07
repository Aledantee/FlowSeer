package bridge_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestAdmissionIsCheckedBeforeThePVID(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	cfg := bridge.Config{VLAN: &bridge.VLAN{
		Table:       map[vlan.ID]string{20: "twenty"},
		Switchports: map[string]bridge.Switchport{"1/1/1": {Admission: bridge.TaggedOnly, Tagged: []vlan.ID{20}}},
	}}
	res := mustNewBridge(t, cfg, buildTestPorts(t, 2)).Forward(now, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0, 0, 0, 0, 0, 0xbb}, Src: netaddr.MAC{0, 0, 0, 0, 0, 0xaa},
	})
	if res.Reason != bridge.ReasonAdmission {
		t.Errorf("an untagged frame on a tagged-only trunk without a PVID dropped with %q, want %q", res.Reason, bridge.ReasonAdmission)
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

	br := mustNewBridge(t, cfg, ports)
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
	if e.FID != 10 || e.MAC != macA || e.Port != "1/1/1" || e.Lifetime == bridge.Static {
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

			br := mustNewBridge(t, cfg, ports)
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

	br := mustNewBridge(t, cfg, ports)
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
