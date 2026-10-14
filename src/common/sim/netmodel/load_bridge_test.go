package netmodel_test

import (
	"slices"
	"testing"

	"buf.build/go/protovalidate"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

func TestLoadBridgeLearnsSource(t *testing.T) {
	// An untagged frame on a port with PVID 10 learns its source under VLAN 10.
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	pvid10 := uint32(10)
	frameAdmAll := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltFalse := false

	p1Name := "1/1/1"
	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Pvid:             &pvid10,
				UntaggedVlanIds:  []uint32{10},
				FrameAdmission:   &frameAdmAll,
				IngressFiltering: &ingressFiltFalse,
			}.Build(),
		}.Build(),
	}.Build()

	p2Name := "1/1/2"
	p2 := interfacev1.Interface_builder{
		Name:        &p2Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Pvid:             &pvid10,
				UntaggedVlanIds:  []uint32{10},
				FrameAdmission:   &frameAdmAll,
				IngressFiltering: &ingressFiltFalse,
			}.Build(),
		}.Build(),
	}.Build()

	vid10 := uint32(10)
	vname10 := "vlan10"
	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{NetworkInstance: ptr("default"), Id: &vid10, Name: &vname10}.Build(),
	}

	in := netmodel.Input{Interfaces: []*interfacev1.Interface{p1, p2}, VLANs: vlans}
	validateInput(t, in)
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, in)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}
	cfg := res.Spec.Config

	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	srcMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	frame := ethernet.Frame{
		Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		Src:     srcMAC,
		Payload: []byte("untagged packet"),
	}
	sw.Receive(testTime, "1/1/1", frame)

	entries := sw.Entries()
	if len(entries) != 1 {
		t.Fatalf("FDB entry count = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.FID != 10 || entry.Port != "1/1/1" || entry.MAC != (netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 0x55}) || entry.Lifetime != bridge.Aging {
		t.Errorf("learned FDB entry = %+v, want VLAN 10 source 00:11:22:33:44:55 on 1/1/1 with dynamic lifetime", entry)
	}
}

func TestDot1qTunnelSwitchport(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	mode := switchingv1.SwitchportMode_SWITCHPORT_MODE_DOT1Q_TUNNEL
	pvid10 := uint32(10)

	p1Name := "1/1/1"
	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Mode:          &mode,
				Pvid:          &pvid10,
				TaggedVlanIds: []uint32{100},
			}.Build(),
		}.Build(),
	}.Build()

	p2Name := "1/1/2"
	p2 := interfacev1.Interface_builder{
		Name:        &p2Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Mode:            &mode,
				UntaggedVlanIds: []uint32{10},
			}.Build(),
		}.Build(),
	}.Build()

	p3Name := "1/1/3"
	p3 := interfacev1.Interface_builder{
		Name:        &p3Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Mode:            &mode,
				Pvid:            &pvid10,
				TaggedVlanIds:   []uint32{100},
				UntaggedVlanIds: []uint32{20},
			}.Build(),
		}.Build(),
	}.Build()

	ifaces := []*interfacev1.Interface{p1, p2, p3}
	for _, iface := range ifaces {
		if err := protovalidate.Validate(iface); err != nil {
			t.Fatalf("interface validation failed: %v", err)
		}
	}

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: ifaces})
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	sw1, ok := cfg.Bridge.VLAN.Switchports["1/1/1"]
	if !ok {
		t.Fatal("missing switchport 1/1/1 in config")
	}
	if sw1.Tunnel == nil {
		t.Fatal("expected 1/1/1 switchport to have Tunnel configured")
	}
	if sw1.Tunnel.VID != 10 {
		t.Errorf("Tunnel.VID = %d, want 10", sw1.Tunnel.VID)
	}
	wantCust := []vlan.ID{100}
	if !slices.Equal(sw1.Tunnel.CustomerVIDs, wantCust) {
		t.Errorf("Tunnel.CustomerVIDs = %v, want %v", sw1.Tunnel.CustomerVIDs, wantCust)
	}
	if sw1.PVID != nil {
		t.Errorf("Tunnel port has PVID set: %v", sw1.PVID)
	}
	if len(sw1.Tagged) > 0 {
		t.Errorf("Tunnel port has Tagged set: %v", sw1.Tagged)
	}
	if len(sw1.Untagged) > 0 {
		t.Errorf("Tunnel port has Untagged set: %v", sw1.Untagged)
	}

	hasQinqDefault := false
	for _, d := range report.Defaults {
		if d.Port == "1/1/1" && d.Field == "qinq_ethtype" && d.Value == "0x88A8" {
			hasQinqDefault = true
			break
		}
	}
	if !hasQinqDefault {
		t.Errorf("got defaults %+v, want qinq_ethtype 0x88A8 for 1/1/1", report.Defaults)
	}

	if _, ok := cfg.Bridge.VLAN.Table[10]; !ok {
		t.Error("expected VLAN table to contain tunnel VID 10")
	}
	if _, ok := cfg.Bridge.VLAN.Table[100]; ok {
		t.Error("VLAN table contains customer VID 100, should not")
	}

	hasSkippedP2 := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/2" && s.What == "switchport" && s.Why == "tunnel without pvid" {
			hasSkippedP2 = true
			break
		}
	}
	if !hasSkippedP2 {
		t.Errorf("got skipped %+v, want 1/1/2 switchport skipped with 'tunnel without pvid'", report.Skipped)
	}
	if _, ok := cfg.Bridge.VLAN.Switchports["1/1/2"]; ok {
		t.Error("1/1/2 switchport should not be present in config")
	}

	hasSkippedUntagged := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/3" && s.What == "untagged_vlan_ids" && s.Why == "tunnel port" {
			hasSkippedUntagged = true
			break
		}
	}
	if !hasSkippedUntagged {
		t.Errorf("got skipped %+v, want 1/1/3 untagged_vlan_ids skipped with 'tunnel port'", report.Skipped)
	}
}
