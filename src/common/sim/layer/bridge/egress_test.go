package bridge_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

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
		b := mustNewBridge(t, cfg, buildTestPorts(t, 2))
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
		res := mustNewBridge(t, bridge.Config{}, tbl).Forward(now, "1/1/1", ethernet.Frame{Dst: macB, Src: macA})
		if res.Outcome != trace.Dropped || res.Reason != bridge.ReasonNoEgress {
			t.Errorf("Forward = %s/%s, want Dropped/%s", res.Outcome, res.Reason, bridge.ReasonNoEgress)
		}
		if last := res.Steps[len(res.Steps)-1]; last.Op != trace.OpDrop {
			t.Errorf("last step = %+v, want a drop", last)
		}
	})
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

	br := mustNewBridge(t, cfg, ports)
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

	br := mustNewBridge(t, cfg, ports)
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

	br := mustNewBridge(t, cfg, ports)
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

func TestLAGMemberResolutionAndForwarding(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "1/1/6", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
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

	br := mustNewBridge(t, cfg, ports)
	br.SetSelector(stubSelector{member: "1/1/5", ok: true}, analysis.ProtocolScope("sw1", "lag", "0"))
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

func TestSelectorOnLAGEgress(t *testing.T) {
	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	builder.Add(port.Port{Name: "1/1/5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "1/1/6", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	builder.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up})
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
		br := mustNewBridge(t, cfg, ports)
		br.SetSelector(stubSelector{member: "1/1/6", ok: true}, analysis.ProtocolScope("sw1", "lag", "0"))
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
		br := mustNewBridge(t, cfg, ports)
		br.SetSelector(stubSelector{member: "", ok: false}, analysis.ProtocolScope("sw1", "lag", "0"))
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
		br := mustNewBridge(t, cfg, ports)
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

	br := mustNewBridge(t, cfg, ports)
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

	br := mustNewBridge(t, cfg, ports)
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

	br := mustNewBridge(t, cfg, ports)
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

	br := mustNewBridge(t, cfg, ports)
	mustLearn(t, br, []bridge.Seed{
		{
			FID:       10,
			MAC:       macB,
			Port:      "1/1/2",
			Lifetime:  bridge.Static,
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
		br := mustNewBridge(t, cfg, ports)
		mustLearn(t, br, []bridge.Seed{
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
		br := mustNewBridge(t, cfg, ports)

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
		br := mustNewBridge(t, bridge.Config{}, ports)

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
	br := mustNewBridge(t, cfg, ports)

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
		if step.Layer == bridge.LayerName && step.Op == trace.OpLookup && step.RuleID == "flood-vlan" {
			foundLookupStep = true
			break
		}
	}
	if !foundLookupStep {
		t.Errorf("frame B steps = %+v, want lookup step with RuleID %q", resB.Steps, "flood-vlan")
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
	br := mustNewBridge(t, cfg, ports)

	mustLearn(t, br, []bridge.Seed{
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
		br := mustNewBridge(t, cfg, ports)
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
		br := mustNewBridge(t, cfg, ports)
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
		br := mustNewBridge(t, cfg, ports)
		mustLearn(t, br, []bridge.Seed{
			{MAC: macB, Port: "1/1/3", FID: 10, Lifetime: bridge.Static},
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
			{TPID: 0x88A8, VID: 10},
			{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 100},
		}
		if !slices.Equal(eg.Frame.Tags, wantTags) {
			t.Errorf("Egress tags = %+v, want %+v", eg.Frame.Tags, wantTags)
		}
	})

	t.Run("customer tagged frame to untagged port pops service tag", func(t *testing.T) {
		br := mustNewBridge(t, cfg, ports)
		mustLearn(t, br, []bridge.Seed{
			{MAC: macB, Port: "1/1/1", FID: 10, Lifetime: bridge.Static},
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
		br := mustNewBridge(t, cfg, ports)
		mustLearn(t, br, []bridge.Seed{
			{MAC: macB, Port: "1/1/3", FID: 10, Lifetime: bridge.Static},
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
		br := mustNewBridge(t, cfg, ports)
		mustLearn(t, br, []bridge.Seed{
			{MAC: macB, Port: "1/1/3", FID: 10, Lifetime: bridge.Static},
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
			{TPID: 0x88A8, VID: 10},
		}
		if !slices.Equal(eg.Frame.Tags, wantTags) {
			t.Errorf("Egress tags = %+v, want %+v", eg.Frame.Tags, wantTags)
		}
	})

	t.Run("tagged frame from trunk to tunnel port pops service tag", func(t *testing.T) {
		br := mustNewBridge(t, cfg, ports)
		mustLearn(t, br, []bridge.Seed{
			{MAC: macA, Port: "1/1/4", FID: 10, Lifetime: bridge.Static},
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

		br := mustNewBridge(t, filtCfg, ports)
		mustLearn(t, br, []bridge.Seed{
			{MAC: macB, Port: "1/1/3", FID: 10, Lifetime: bridge.Static},
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
				br := mustNewBridge(t, baseCfg(tc.policy), ports)
				mustLearn(t, br, []bridge.Seed{
					{MAC: macB, Port: "1/1/2", FID: 10, Lifetime: bridge.Static},
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
				br := mustNewBridge(t, baseCfg(tc.policy), ports)
				mustLearn(t, br, []bridge.Seed{
					{MAC: macB, Port: "1/1/2", FID: 10, Lifetime: bridge.Static},
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

func TestEgressToUsesFloodReplicationRules(t *testing.T) {
	ports := buildTestPorts(t, 3)
	br := mustNewBridge(t, bridge.Config{VLAN: &bridge.VLAN{
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
	}}, analysis.ProtocolScope("sw1", "stp", "0"))

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
