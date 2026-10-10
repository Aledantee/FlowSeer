package netmodel_test

import (
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

func TestNetmodel_LoadErrors(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP

	t.Run("empty interfaces", func(t *testing.T) {
		_, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{})
		if err == nil {
			t.Fatal("expected error on empty interface list")
		}
	})

	t.Run("duplicate interface name", func(t *testing.T) {
		p1Name := "1/1/1"
		p1 := interfacev1.Interface_builder{Name: &p1Name, AdminStatus: &adminUp, OperStatus: &operUp}.Build()
		p2 := interfacev1.Interface_builder{Name: &p1Name, AdminStatus: &adminUp, OperStatus: &operUp}.Build()
		_, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: []*interfacev1.Interface{p1, p2}})
		if err == nil {
			t.Fatal("expected error on duplicate interface name")
		}
	})

	t.Run("lag_parent_interface_name naming non-existent", func(t *testing.T) {
		p1Name := "1/1/1"
		lagParent := "lag99"
		p1 := interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParentInterfaceName: &lagParent,
			}.Build(),
		}.Build()
		_, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: []*interfacev1.Interface{p1}})
		if err == nil {
			t.Fatal("expected error on non-existent lag parent")
		}
	})

	t.Run("lag_parent_interface_name naming non-LAG", func(t *testing.T) {
		p1Name := "1/1/1"
		p2Name := "1/1/2"
		p1 := interfacev1.Interface_builder{Name: &p1Name, AdminStatus: &adminUp, OperStatus: &operUp}.Build()
		p2 := interfacev1.Interface_builder{
			Name:        &p2Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParentInterfaceName: &p1Name,
			}.Build(),
		}.Build()
		_, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: []*interfacev1.Interface{p1, p2}})
		if err == nil {
			t.Fatal("expected error on lag parent that is not a LAG")
		}
	})
}

func TestNetmodel_DefaultsAndEdgeCases(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP

	// 1. Explicit MTU 0 reported and treated as unlimited
	p1Name := "1/1/1"
	mtuZero := uint32(0)
	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Mtu:         &mtuZero,
	}.Build()

	// 2. Switchport without PVID and with multiple untagged VLANs (no PVID)
	p2Name := "1/1/2"
	p2 := interfacev1.Interface_builder{
		Name:        &p2Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				UntaggedVlanIds: []uint32{10, 20},
			}.Build(),
		}.Build(),
	}.Build()

	// 3. PseBudget without power_nanowatts (loads as budget 0)
	grp1 := uint32(1)
	budgetWithoutPower := phyv1.PseBudget_builder{
		PseGroup: &grp1,
	}.Build()

	// 4. FDB entry with STATIC kind
	staticKind := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	statusActive := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	vid10 := uint32(10)
	fdbStatic := switchingv1.FdbEntry_builder{
		NetworkInstance: ptr("default"),
		VlanId:          &vid10,
		InterfaceName:   &p2Name,
		Kind:            &staticKind,
		Status:          &statusActive,
		Mac:             addrv1.Eui48Address_builder{Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}}.Build(),
	}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: []*interfacev1.Interface{p1, p2}, FDB: []*switchingv1.FdbEntry{fdbStatic}, Budgets: []*phyv1.PseBudget{budgetWithoutPower}})
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	seeds := res.Spec.Seeds
	report := res.Report

	// MTU 0 treated as unlimited in Port
	port1, ok := cfg.Ports.Port("1/1/1")
	if !ok || port1.MTU != 0 {
		t.Errorf("got port 1/1/1 MTU %d, want 0", port1.MTU)
	}

	// Explicit zero is observed input, not a loader default.
	hasMtuDefault := false
	hasBudgetDefault := false
	for _, d := range report.Defaults {
		if d.Port == "1/1/1" && d.Field == "mtu" && d.Value == "0" {
			hasMtuDefault = true
		}
		if d.Port == "1" && d.Field == "power_nanowatts" && d.Value == "0" {
			hasBudgetDefault = true
		}
	}
	if hasMtuDefault {
		t.Errorf("explicit MTU zero reported as a default: %+v", report.Defaults)
	}
	if !hasBudgetDefault {
		t.Errorf("got defaults %+v, want power_nanowatts reported", report.Defaults)
	}

	// Switchport with multiple untagged VLANs has no PVID
	sw2 := cfg.Bridge.VLAN.Switchports["1/1/2"]
	if sw2.PVID != nil {
		t.Errorf("got sw2 PVID %v, want nil", sw2.PVID)
	}

	// Seed should be marked static
	if len(seeds) != 1 || seeds[0].Lifetime != bridge.Static {
		t.Errorf("got seeds %+v, want static seed", seeds)
	}

	// An entry of a bridge without VLAN awareness has no FdbEntry shape, since
	// the message requires a VLAN id.
	entryFID0 := []bridge.Entry{
		{
			FID:       0,
			MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			Port:      "1/1/1",
			Lifetime:  bridge.Aging,
			LearnedAt: testTime,
		},
	}
	if _, err := netmodel.FdbEntries(entryFID0); err == nil {
		t.Error("FdbEntries(FID 0) error = nil, want an error")
	}
}
