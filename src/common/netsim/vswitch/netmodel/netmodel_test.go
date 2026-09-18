package netmodel_test

import (
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

var testTime = time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)

// A LAG forwards as one port: lag1 with members 1/1/5 and 1/1/6, VLAN 10
// tagged; a frame ingressing 1/1/6 traces ingress port lag1, a flood in VLAN
// 10 from 1/1/1 lists one egress lag1 on member 1/1/5, and a member's own
// switchport facet loads as skipped.
func TestLagForwardingAndSkippedFacet(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vid10 := uint32(10)
	frameAdmAll := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltFalse := false
	lagParent := "lag1"

	p1Name := "1/1/1"
	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				TaggedVlanIds:    []uint32{vid10},
				FrameAdmission:   &frameAdmAll,
				IngressFiltering: &ingressFiltFalse,
			}.Build(),
		}.Build(),
	}.Build()

	lagName := "lag1"
	lag := interfacev1.Interface_builder{
		Name:        &lagName,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Lag: interfacev1.LagInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				TaggedVlanIds:    []uint32{vid10},
				FrameAdmission:   &frameAdmAll,
				IngressFiltering: &ingressFiltFalse,
			}.Build(),
		}.Build(),
	}.Build()

	p5Name := "1/1/5"
	p5 := interfacev1.Interface_builder{
		Name:        &p5Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			LagParent: &lagParent,
		}.Build(),
	}.Build()

	p6Name := "1/1/6"
	p6 := interfacev1.Interface_builder{
		Name:        &p6Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			LagParent: &lagParent,
			Switchport: switchingv1.SwitchportFacet_builder{
				TaggedVlanIds:    []uint32{vid10},
				FrameAdmission:   &frameAdmAll,
				IngressFiltering: &ingressFiltFalse,
			}.Build(),
		}.Build(),
	}.Build()

	ifaces := []*interfacev1.Interface{p1, lag, p5, p6}
	for _, iface := range ifaces {
		if err := protovalidate.Validate(iface); err != nil {
			t.Fatalf("interface validation failed: %v", err)
		}
	}

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	// Member's switchport facet must be reported as skipped.
	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/6" && s.What == "switchport" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected 1/1/6 switchport to be skipped, got: %+v", report.Skipped)
	}

	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	// Ingress on member 1/1/6 traces ingress port lag1.
	tag10 := vlan.Tag{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 10}
	ingressFrame := ethernet.Frame{
		Dst:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		Tags:    []vlan.Tag{tag10},
		Payload: []byte("test payload"),
	}
	resIngress := sw.Forward(testTime, "1/1/6", ingressFrame)
	if resIngress.Ingress != "lag1" {
		t.Errorf("ingress port = %q, want %q", resIngress.Ingress, "lag1")
	}

	// Flood in VLAN 10 from 1/1/1 lists one egress lag1 on member 1/1/5.
	floodFrame := ethernet.Frame{
		Dst:     netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:     netaddr.MAC{0x00, 0x11, 0x11, 0x11, 0x11, 0x11},
		Tags:    []vlan.Tag{tag10},
		Payload: []byte("flood payload"),
	}
	resFlood := sw.Forward(testTime, "1/1/1", floodFrame)
	if len(resFlood.Egress) != 1 {
		t.Fatalf("flood egress count = %d, want 1", len(resFlood.Egress))
	}
	eg := resFlood.Egress[0]
	if eg.Port != "lag1" {
		t.Errorf("egress port = %q, want %q", eg.Port, "lag1")
	}
	if eg.Member != "1/1/5" {
		t.Errorf("egress member = %q, want %q", eg.Member, "1/1/5")
	}
}

// Loading infers capabilities and reports every assumption: interfaces with
// switchport facets and no Ethernet facet load with {relay, vlan} and the
// report says so; a SwitchportFacet with untagged_vlan_ids [30], no pvid, and
// no frame_admission loads as PVID 30, admission ALL, both listed as defaults
// by port name; a wanted set of {relay} drops every switchport facet and lists
// each as skipped.
func TestInferCapabilitiesAndReportDefaults(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP

	p1Name := "1/1/1"
	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				UntaggedVlanIds: []uint32{30},
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
				UntaggedVlanIds: []uint32{30},
			}.Build(),
		}.Build(),
	}.Build()

	ifaces := []*interfacev1.Interface{p1, p2}
	for _, iface := range ifaces {
		if err := protovalidate.Validate(iface); err != nil {
			t.Fatalf("interface validation failed: %v", err)
		}
	}

	// Part 1: Infer {relay, vlan} and report defaults.
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	wantCaps := []port.Layer{port.LayerRelay, port.LayerVlan}
	if !slices.Equal(report.Capabilities, wantCaps) {
		t.Errorf("capabilities = %v, want %v", report.Capabilities, wantCaps)
	}
	if report.CapabilitySources[port.LayerRelay] != "always" {
		t.Errorf("relay source = %q, want always", report.CapabilitySources[port.LayerRelay])
	}
	if report.CapabilitySources[port.LayerVlan] != "inferred:switchport" {
		t.Errorf("vlan source = %q, want inferred:switchport", report.CapabilitySources[port.LayerVlan])
	}

	// Verify PVID 30 and admission ALL in config.
	sw1 := cfg.Bridge.VLAN.Switchports["1/1/1"]
	if sw1.PVID == nil || *sw1.PVID != 30 {
		t.Errorf("sw1 PVID = %v, want 30", sw1.PVID)
	}
	if sw1.Admission != "All" {
		t.Errorf("sw1 Admission = %v, want All", sw1.Admission)
	}

	// Verify defaults listed by port name in report.
	hasPvidDefault := false
	hasAdmDefault := false
	for _, d := range report.Defaults {
		if d.Port == "1/1/1" && d.Field == "pvid" && d.Value == "30" {
			hasPvidDefault = true
		}
		if d.Port == "1/1/1" && d.Field == "frame_admission" && d.Value == "ALL" {
			hasAdmDefault = true
		}
	}
	if !hasPvidDefault {
		t.Errorf("defaults missing pvid:30 for 1/1/1: %+v", report.Defaults)
	}
	if !hasAdmDefault {
		t.Errorf("defaults missing frame_admission:ALL for 1/1/1: %+v", report.Defaults)
	}

	// Part 2: Wanted set of {relay} drops every switchport facet and lists each as skipped.
	resRelayOnly, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, []port.Layer{port.LayerRelay})
	if err != nil {
		t.Fatalf("netmodel.Load with relay failed: %v", err)
	}
	cfgRelayOnly := resRelayOnly.Spec.Config
	reportRelayOnly := resRelayOnly.Report
	if cfgRelayOnly.Bridge.VLAN != nil {
		t.Errorf("expected VLAN configuration to be nil when only relay is wanted")
	}
	skippedP1 := false
	skippedP2 := false
	for _, s := range reportRelayOnly.Skipped {
		if s.Port == "1/1/1" && s.What == "switchport" {
			skippedP1 = true
		}
		if s.Port == "1/1/2" && s.What == "switchport" {
			skippedP2 = true
		}
	}
	if !skippedP1 || !skippedP2 {
		t.Errorf("expected both switchport facets skipped, got: %+v", reportRelayOnly.Skipped)
	}
}

// The FDB and PoE state export as net/switching and net/phy rows: one learned
// entry becomes one FdbEntry (vlan_id 10, that MAC, interface_name 1/1/1,
// DYNAMIC, ACTIVE); an allocation becomes a PseBudget per group and a PoeFacet
// per port with allocated_power_milliwatts; every message passes protovalidate.
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
		switchingv1.Vlan_builder{Id: &vid10, Name: &vname10}.Build(),
	}

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{p1, p2}, vlans, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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
	sw.Forward(testTime, "1/1/1", frame)

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
				"1": {PowerMilliwatts: 60_000},
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
	if b0.GetPowerMilliwatts() != 60_000 {
		t.Errorf("budget power_milliwatts = %d, want 60000", b0.GetPowerMilliwatts())
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
	if f1.GetAllocatedPowerMilliwatts() != 30_000 {
		t.Errorf("1/1/1 allocated = %d, want 30000", f1.GetAllocatedPowerMilliwatts())
	}
	if f1.GetStatus() != phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER {
		t.Errorf("1/1/1 status = %v, want DELIVERING_POWER", f1.GetStatus())
	}

	f2 := facets["1/1/2"]
	if f2.GetAllocatedPowerMilliwatts() != 30_000 {
		t.Errorf("1/1/2 allocated = %d, want 30000", f2.GetAllocatedPowerMilliwatts())
	}
	if f2.GetStatus() != phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER {
		t.Errorf("1/1/2 status = %v, want DELIVERING_POWER", f2.GetStatus())
	}

	f3 := facets["1/1/3"]
	if f3.HasAllocatedPowerMilliwatts() {
		t.Errorf("1/1/3 allocated should be unset, got %d", f3.GetAllocatedPowerMilliwatts())
	}
	if f3.HasPowerClass() {
		t.Errorf("1/1/3 power_class should be unset, got %d", f3.GetPowerClass())
	}
	if f3.GetStatus() != phyv1.PoeStatus_POE_STATUS_SEARCHING {
		t.Errorf("1/1/3 status = %v, want SEARCHING", f3.GetStatus())
	}
}

func TestNetmodel_InvalidFdbEntrySkipped(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	name := "1/1/1"
	iface := interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
	}.Build()

	vid := uint32(10)
	ifname := "1/1/1"
	statusInvalid := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_INVALID
	statusActive := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	kindDynamic := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC
	macBytes := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	fdb1 := switchingv1.FdbEntry_builder{
		VlanId:        &vid,
		InterfaceName: &ifname,
		Status:        &statusInvalid,
		Kind:          &kindDynamic,
		Mac:           addrv1.Eui48Address_builder{Octets: macBytes}.Build(),
	}.Build()

	fdb2 := switchingv1.FdbEntry_builder{
		VlanId:        &vid,
		InterfaceName: &ifname,
		Status:        &statusActive,
		Kind:          &kindDynamic,
		Mac:           addrv1.Eui48Address_builder{Octets: macBytes}.Build(),
	}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{iface}, nil, []*switchingv1.FdbEntry{fdb1, fdb2}, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	seeds := res.Spec.Seeds
	report := res.Report

	if len(seeds) != 0 {
		t.Errorf("seeds = %+v, want both unusable rows omitted", seeds)
	}

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/1" && s.What == "fdb_entry" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected invalid FDB entry in Skipped, got %+v", report.Skipped)
	}
	if res.Readiness() == analysis.Complete {
		t.Error("readiness = Complete, want unusable active row reported")
	}
	if _, err := vswitch.NewWithSpec(res.Spec); err != nil {
		t.Errorf("NewWithSpec: %v", err)
	}
}

func TestNetmodel_PortWithoutPoeDetailSkipped(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	name := "1/1/1"
	poeSup := true
	role := phyv1.PoeRole_POE_ROLE_PSE
	group1 := uint32(1)
	power := uint32(100_000)

	budget := phyv1.PseBudget_builder{
		PseGroup:        &group1,
		PowerMilliwatts: &power,
	}.Build()

	iface := interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Copper: phyv1.CopperFacet_builder{
					Poe: phyv1.PoeFacet_builder{
						Supported: &poeSup,
						Role:      &role,
					}.Build(),
					// Deliberately missing PoeDetail
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{iface}, nil, nil, []*phyv1.PseBudget{budget}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/1" && s.What == "poe" && s.Why == "missing poe_detail" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected poe without detail skipped, got %+v", report.Skipped)
	}

	if cfg.Phy != nil && cfg.Phy.PoE != nil && len(cfg.Phy.PoE.Ports) != 0 {
		t.Errorf("expected no PoE ports loaded, got %d", len(cfg.Phy.PoE.Ports))
	}
}

func TestNetmodel_LoadErrors(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP

	t.Run("empty interfaces", func(t *testing.T) {
		_, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		if err == nil {
			t.Fatal("expected error on empty interface list")
		}
	})

	t.Run("duplicate interface name", func(t *testing.T) {
		p1Name := "1/1/1"
		p1 := interfacev1.Interface_builder{Name: &p1Name, AdminStatus: &adminUp, OperStatus: &operUp}.Build()
		p2 := interfacev1.Interface_builder{Name: &p1Name, AdminStatus: &adminUp, OperStatus: &operUp}.Build()
		_, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{p1, p2}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		if err == nil {
			t.Fatal("expected error on duplicate interface name")
		}
	})

	t.Run("lag_parent naming non-existent", func(t *testing.T) {
		p1Name := "1/1/1"
		lagParent := "lag99"
		p1 := interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParent: &lagParent,
			}.Build(),
		}.Build()
		_, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{p1}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
		if err == nil {
			t.Fatal("expected error on non-existent lag parent")
		}
	})

	t.Run("lag_parent naming non-LAG", func(t *testing.T) {
		p1Name := "1/1/1"
		p2Name := "1/1/2"
		p1 := interfacev1.Interface_builder{Name: &p1Name, AdminStatus: &adminUp, OperStatus: &operUp}.Build()
		p2 := interfacev1.Interface_builder{
			Name:        &p2Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParent: &p1Name,
			}.Build(),
		}.Build()
		_, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{p1, p2}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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

	// 3. PseBudget without power_milliwatts (loads as budget 0)
	grp1 := uint32(1)
	budgetWithoutPower := phyv1.PseBudget_builder{
		PseGroup: &grp1,
	}.Build()

	// 4. FDB entry with STATIC kind
	staticKind := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	statusActive := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	vid10 := uint32(10)
	fdbStatic := switchingv1.FdbEntry_builder{
		VlanId:        &vid10,
		InterfaceName: &p2Name,
		Kind:          &staticKind,
		Status:        &statusActive,
		Mac:           addrv1.Eui48Address_builder{Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}}.Build(),
	}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{p1, p2}, nil, []*switchingv1.FdbEntry{fdbStatic}, []*phyv1.PseBudget{budgetWithoutPower}, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	seeds := res.Spec.Seeds
	report := res.Report

	// MTU 0 treated as unlimited in Port
	port1, ok := cfg.Ports.Port("1/1/1")
	if !ok || port1.MTU != 0 {
		t.Errorf("expected port 1/1/1 MTU = 0, got %d", port1.MTU)
	}

	// Explicit zero is observed input, not a loader default.
	hasMtuDefault := false
	hasBudgetDefault := false
	for _, d := range report.Defaults {
		if d.Port == "1/1/1" && d.Field == "mtu" && d.Value == "0" {
			hasMtuDefault = true
		}
		if d.Port == "1" && d.Field == "power_milliwatts" && d.Value == "0" {
			hasBudgetDefault = true
		}
	}
	if hasMtuDefault {
		t.Errorf("explicit MTU zero reported as a default: %+v", report.Defaults)
	}
	if !hasBudgetDefault {
		t.Errorf("expected power_milliwatts default reported, got defaults: %+v", report.Defaults)
	}

	// Switchport with multiple untagged VLANs has no PVID
	sw2 := cfg.Bridge.VLAN.Switchports["1/1/2"]
	if sw2.PVID != nil {
		t.Errorf("expected sw2 PVID nil, got %v", sw2.PVID)
	}

	// Seed should be marked static
	if len(seeds) != 1 || seeds[0].Lifetime != bridge.Static {
		t.Errorf("expected static seed, got: %+v", seeds)
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

func TestPoeExportRefusesANonNumericGroup(t *testing.T) {
	cfg := phy.Config{PoE: &phy.PoE{
		Groups: map[string]phy.Group{"g1": {PowerMilliwatts: 60_000}},
		Ports:  map[string]phy.PsePort{"1/1/1": {Group: "g1", MaxClass: 8, Enabled: true, PDClass: phy.Class(4)}},
	}}
	if _, _, err := netmodel.Poe(cfg, cfg.Allocate()); err == nil {
		t.Error("Poe(group g1) error = nil, want an error")
	}
}

func TestPoeExportStatusFollowsTheDenial(t *testing.T) {
	cfg := phy.Config{PoE: &phy.PoE{
		Groups: map[string]phy.Group{"1": {PowerMilliwatts: 30_000}},
		Ports: map[string]phy.PsePort{
			"1/1/1": {Group: "1", MaxClass: 8, Enabled: false, PD: phy.PDAttached, PDClass: phy.Class(4)},
			"1/1/2": {Group: "1", MaxClass: 3, Enabled: true, PD: phy.PDAttached, PDClass: phy.Class(4)},
			"1/1/3": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityHigh, PD: phy.PDAttached, PDClass: phy.Class(4)},
			"1/1/4": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityLow, PD: phy.PDAttached, PDClass: phy.Class(4)},
			"1/1/5": {Group: "1", MaxClass: 8, Enabled: true, PD: phy.PDAbsent},
		},
	}}
	_, facets, err := netmodel.Poe(cfg, cfg.Allocate())
	if err != nil {
		t.Fatalf("Poe: %v", err)
	}
	want := map[string]phyv1.PoeStatus{
		"1/1/1": phyv1.PoeStatus_POE_STATUS_DISABLED,
		"1/1/2": phyv1.PoeStatus_POE_STATUS_SEARCHING,
		"1/1/3": phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER,
		"1/1/4": phyv1.PoeStatus_POE_STATUS_SEARCHING,
		"1/1/5": phyv1.PoeStatus_POE_STATUS_SEARCHING,
	}
	for name, status := range want {
		if got := facets[name].GetStatus(); got != status {
			t.Errorf("facets[%q].Status = %v, want %v", name, got, status)
		}
		if err := protovalidate.Validate(facets[name]); err != nil {
			t.Errorf("facets[%q] fails validation: %v", name, err)
		}
	}
	for _, name := range []string{"1/1/1", "1/1/2", "1/1/4", "1/1/5"} {
		if facets[name].HasPowerClass() {
			t.Errorf("facets[%q] exported a power_class when not delivering power", name)
		}
	}
	if !facets["1/1/3"].HasPowerClass() || facets["1/1/3"].GetPowerClass() != 4 {
		t.Errorf("facets[1/1/3] power_class = %v, want 4", facets["1/1/3"].GetPowerClass())
	}
}

func TestLoadImpliesRelayForVlanAndKeepsLagPresent(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	lagParent := "lag1"
	name := func(s string) *string { return &s }
	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{Name: name("lag1"), AdminStatus: &adminUp, Lag: interfacev1.LagInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{TaggedVlanIds: []uint32{10}}.Build(),
		}.Build()}.Build(),
		interfacev1.Interface_builder{Name: name("1/1/1"), AdminStatus: &adminUp, Physical: interfacev1.PhysicalInterface_builder{
			LagParent: &lagParent,
		}.Build()}.Build(),
	}
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, []port.Layer{port.LayerVlan})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report
	if cfg.Bridge == nil || cfg.Bridge.VLAN == nil {
		t.Fatal("a wanted vlan layer did not build the bridge")
	}
	if !slices.Equal(report.Capabilities, cfg.Capabilities()) {
		t.Errorf("report.Capabilities = %v, cfg.Capabilities() = %v; want them equal", report.Capabilities, cfg.Capabilities())
	}
	if report.CapabilitySources[port.LayerRelay] != "implied:vlan" || report.CapabilitySources[port.LayerLag] != "present:lag" {
		t.Errorf("CapabilitySources = %v", report.CapabilitySources)
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

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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
		t.Errorf("expected default qinq_ethtype 0x88A8 for 1/1/1, got defaults: %+v", report.Defaults)
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
		t.Errorf("expected 1/1/2 switchport to be skipped with 'tunnel without pvid', got skipped: %+v", report.Skipped)
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
		t.Errorf("expected 1/1/3 untagged_vlan_ids to be skipped with 'tunnel port', got skipped: %+v", report.Skipped)
	}
}

func TestPoeExportAndLoadRoundTrip(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	mtu0 := uint32(0)
	grp := uint32(1)

	loadExported := func(t *testing.T, phyCfg phy.Config, alloc phy.Allocation) netmodel.Result {
		t.Helper()
		budgets, facets, err := netmodel.Poe(phyCfg, alloc)
		if err != nil {
			t.Fatalf("Poe export failed: %v", err)
		}
		for _, b := range budgets {
			if err := protovalidate.Validate(b); err != nil {
				t.Fatalf("budget protovalidate failed: %v", err)
			}
		}
		for name, f := range facets {
			if err := protovalidate.Validate(f); err != nil {
				t.Fatalf("facet %s protovalidate failed: %v", name, err)
			}
		}

		names := make([]string, 0, len(facets))
		for name := range facets {
			names = append(names, name)
		}
		slices.Sort(names)
		ifaces := make([]*interfacev1.Interface, 0, len(facets))
		for _, name := range names {
			pName := name
			iface := interfacev1.Interface_builder{
				Name:        &pName,
				AdminStatus: &adminUp,
				OperStatus:  &operUp,
				Mtu:         &mtu0,
				Physical: interfacev1.PhysicalInterface_builder{
					Ethernet: phyv1.EthernetFacet_builder{
						Copper: phyv1.CopperFacet_builder{
							Poe: facets[pName],
							PoeDetail: phyv1.PoePortDetail_builder{
								PseGroup: &grp,
								PsePort:  ptr(uint32(1)),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()
			if err := protovalidate.Validate(iface); err != nil {
				t.Fatalf("iface %s protovalidate failed: %v", pName, err)
			}
			ifaces = append(ifaces, iface)
		}

		res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, budgets, nil, nil, nil, nil, nil, nil, []port.Layer{port.LayerPoe})
		if err != nil {
			t.Fatalf("netmodel.Load failed: %v", err)
		}
		return res
	}

	t.Run("delivered", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerMilliwatts: 100_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDAttached, PDClass: phy.Class(2)},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerDelivered {
			t.Fatalf("expected PowerDelivered, got %v", alloc.Ports["1/1/1"].State)
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDAttached {
			t.Errorf("PD = %v, want Attached", p.PD)
		}
		if p.PDClass == nil || *p.PDClass != 2 {
			t.Errorf("PDClass = %v, want 2", p.PDClass)
		}
		for _, a := range res.Metadata.Assumptions() {
			if a.Statement == "absence is inferred from the searching status" {
				t.Errorf("unexpected searching assumption: %+v", a)
			}
		}
	})

	t.Run("disabled", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerMilliwatts: 100_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: false, Priority: phy.PriorityCritical, PD: phy.PDAttached, PDClass: phy.Class(2)},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerDenied || alloc.Ports["1/1/1"].Denial != phy.ReasonDisabled {
			t.Fatalf("expected PowerDenied(disabled), got %+v", alloc.Ports["1/1/1"])
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDUnknown {
			t.Errorf("PD = %v, want Unknown", p.PD)
		}
		if p.PDClass != nil {
			t.Errorf("PDClass = %v, want nil", p.PDClass)
		}
		for _, a := range res.Metadata.Assumptions() {
			if a.Statement == "absence is inferred from the searching status" {
				t.Errorf("unexpected searching assumption: %+v", a)
			}
		}
	})

	t.Run("no device", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerMilliwatts: 100_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDAbsent},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerNoDevice {
			t.Fatalf("expected PowerNoDevice, got %v", alloc.Ports["1/1/1"].State)
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDAbsent {
			t.Errorf("PD = %v, want Absent", p.PD)
		}
		if p.PDClass != nil {
			t.Errorf("PDClass = %v, want nil", p.PDClass)
		}

		portScope := analysis.PortScope("sw1", "1/1/1")
		var portAssumptions []analysis.Assumption
		for _, a := range res.Metadata.Assumptions() {
			if a.Scope.Compare(portScope) == 0 && a.Statement == "absence is inferred from the searching status" {
				portAssumptions = append(portAssumptions, a)
			}
		}
		if len(portAssumptions) != 1 {
			t.Fatalf("port assumptions count = %d, want 1", len(portAssumptions))
		}
		if portAssumptions[0].Statement != "absence is inferred from the searching status" {
			t.Errorf("statement = %q, want %q", portAssumptions[0].Statement, "absence is inferred from the searching status")
		}
	})

	t.Run("unknown", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerMilliwatts: 100_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDUnknown},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerUnknown {
			t.Fatalf("expected PowerUnknown, got %v", alloc.Ports["1/1/1"].State)
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDUnknown {
			t.Errorf("PD = %v, want Unknown", p.PD)
		}
		if p.PDClass != nil {
			t.Errorf("PDClass = %v, want nil", p.PDClass)
		}
		for _, a := range res.Metadata.Assumptions() {
			if a.Statement == "absence is inferred from the searching status" {
				t.Errorf("unexpected searching assumption: %+v", a)
			}
		}
	})

	t.Run("budget denial", func(t *testing.T) {
		phyCfg := phy.Config{
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{
					"1": {PowerMilliwatts: 10_000},
				},
				Ports: map[string]phy.PsePort{
					"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, Priority: phy.PriorityCritical, PD: phy.PDAttached, PDClass: phy.Class(4)},
				},
			},
		}
		alloc := phyCfg.Allocate()
		if alloc.Ports["1/1/1"].State != phy.PowerDenied || alloc.Ports["1/1/1"].Denial != phy.ReasonBudget {
			t.Fatalf("expected PowerDenied(budget), got %+v", alloc.Ports["1/1/1"])
		}

		res := loadExported(t, phyCfg, alloc)
		p, ok := res.Spec.Config.Phy.PoE.Ports["1/1/1"]
		if !ok {
			t.Fatal("port 1/1/1 missing from loaded config")
		}
		if p.PD != phy.PDAbsent {
			t.Errorf("PD = %v, want Absent", p.PD)
		}
		if p.PDClass != nil {
			t.Errorf("PDClass = %v, want nil", p.PDClass)
		}

		portScope := analysis.PortScope("sw1", "1/1/1")
		var portAssumptions []analysis.Assumption
		for _, a := range res.Metadata.Assumptions() {
			if a.Scope.Compare(portScope) == 0 && a.Statement == "absence is inferred from the searching status" {
				portAssumptions = append(portAssumptions, a)
			}
		}
		if len(portAssumptions) != 1 {
			t.Fatalf("port assumptions count = %d, want 1", len(portAssumptions))
		}
		if portAssumptions[0].Statement != "absence is inferred from the searching status" {
			t.Errorf("statement = %q, want %q", portAssumptions[0].Statement, "absence is inferred from the searching status")
		}
	})
}
