package netmodel_test

import (
	"slices"
	"testing"

	"buf.build/go/protovalidate"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

// The FDB and PoE state export as net/switching and net/phy rows: one learned
// entry becomes one FdbEntry (vlan_id 10, that MAC, interface_name 1/1/1,
// DYNAMIC, ACTIVE); an allocation becomes a PseBudget per group and a PoeFacet
// per port with allocated_power_nanowatts; every message passes protovalidate.
func TestFdbAndPoeExport(t *testing.T) {
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

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{p1, p2}, vlans, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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
	fdbExport, err := netmodel.FdbEntries(entries)
	if err != nil {
		t.Fatalf("FdbEntries: %v", err)
	}
	if len(fdbExport) != 1 {
		t.Fatalf("exported FDB entry count = %d, want 1", len(fdbExport))
	}
	fe := fdbExport[0]
	if err := protovalidate.Validate(fe); err != nil {
		t.Fatalf("FdbEntry protovalidate failed: %v", err)
	}

	if fe.GetVlanId() != 10 {
		t.Errorf("FdbEntry vlan_id = %d, want 10", fe.GetVlanId())
	}
	if fe.GetInterfaceName() != "1/1/1" {
		t.Errorf("FdbEntry interface_name = %q, want %q", fe.GetInterfaceName(), "1/1/1")
	}
	if fe.GetKind() != switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC {
		t.Errorf("FdbEntry kind = %v, want DYNAMIC", fe.GetKind())
	}
	if fe.GetStatus() != switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE {
		t.Errorf("FdbEntry status = %v, want ACTIVE", fe.GetStatus())
	}
	if !slices.Equal(fe.GetMac().GetOctets(), srcMAC[:]) {
		t.Errorf("FdbEntry MAC = %x, want %x", fe.GetMac().GetOctets(), srcMAC)
	}

	// Group 1 with 60 W and three class-4 ports at critical, high, low
	// allocates two and denies one with budget.
	phyCfg := phy.Config{
		PoE: &phy.PoE{
			Groups: map[string]phy.Group{
				"1": {PowerNanowatts: 60_000_000_000},
			},
			Ports: map[string]phy.PsePort{
				"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDAttached, PDClass: phy.Class(4)},
				"1/1/2": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityHigh, PD: phy.PDAttached, PDClass: phy.Class(4)},
				"1/1/3": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityLow, PD: phy.PDAttached, PDClass: phy.Class(4)},
			},
		},
	}
	alloc := phyCfg.Allocate()

	budgets, facets, err := netmodel.Poe(phyCfg, alloc)
	if err != nil {
		t.Fatalf("Poe: %v", err)
	}
	if len(budgets) != 1 {
		t.Fatalf("exported budgets count = %d, want 1", len(budgets))
	}
	b0 := budgets[0]
	if err := protovalidate.Validate(b0); err != nil {
		t.Fatalf("PseBudget protovalidate failed: %v", err)
	}
	if b0.GetPseGroup() != 1 {
		t.Errorf("budget pse_group = %d, want 1", b0.GetPseGroup())
	}
	if b0.GetPowerNanowatts() != 60_000_000_000 {
		t.Errorf("budget power_nanowatts = %d, want 60000000000", b0.GetPowerNanowatts())
	}

	if len(facets) != 3 {
		t.Fatalf("exported facets count = %d, want 3", len(facets))
	}

	for name, facet := range facets {
		if err := protovalidate.Validate(facet); err != nil {
			t.Fatalf("PoeFacet %s protovalidate failed: %v", name, err)
		}
	}

	f1 := facets["1/1/1"]
	if f1.GetAllocatedPowerNanowatts() != 30_000_000_000 {
		t.Errorf("1/1/1 allocated = %d, want 30000000000", f1.GetAllocatedPowerNanowatts())
	}
	if f1.GetStatus() != phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER {
		t.Errorf("1/1/1 status = %v, want DELIVERING_POWER", f1.GetStatus())
	}

	f2 := facets["1/1/2"]
	if f2.GetAllocatedPowerNanowatts() != 30_000_000_000 {
		t.Errorf("1/1/2 allocated = %d, want 30000000000", f2.GetAllocatedPowerNanowatts())
	}
	if f2.GetStatus() != phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER {
		t.Errorf("1/1/2 status = %v, want DELIVERING_POWER", f2.GetStatus())
	}

	f3 := facets["1/1/3"]
	if f3.HasAllocatedPowerNanowatts() {
		t.Errorf("1/1/3 allocated should be unset, got %d", f3.GetAllocatedPowerNanowatts())
	}
	if f3.HasPowerClass() {
		t.Errorf("1/1/3 power_class should be unset, got %d", f3.GetPowerClass())
	}
	if f3.GetStatus() != phyv1.PoeStatus_POE_STATUS_SEARCHING {
		t.Errorf("1/1/3 status = %v, want SEARCHING", f3.GetStatus())
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

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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
