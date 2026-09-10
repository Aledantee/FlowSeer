package netmodel_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Transcribed from docs/research/device-inventory/lab/labsw06-ruckus-icx7150.md "Feature inventory" table (lines 121-143).
func icx7150Fixture(t *testing.T) ([]*interfacev1.Interface, []*switchingv1.Vlan, []*phyv1.PseBudget) {
	t.Helper()

	var ifaces []*interfacev1.Interface
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	operDown := interfacev1.OperStatus_OPER_STATUS_DOWN

	autoNegSup := true
	autoNegEnabled := true
	poeSupported := true
	poeRole := phyv1.PoeRole_POE_ROLE_PSE
	poeStatus := phyv1.PoeStatus_POE_STATUS_SEARCHING
	pvid4000 := uint32(4000)
	frameAdmAll := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltFalse := false

	// 24 copper 1G ports 1/1/1..1/1/24 with copper Ethernet facets and PoE facets.
	for i := 1; i <= 24; i++ {
		name := fmt.Sprintf("1/1/%d", i)
		portNum := uint32(i)
		pseGroup := uint32(1)

		iface := interfacev1.Interface_builder{
			Name:        &name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				Ethernet: phyv1.EthernetFacet_builder{
					Capabilities: phyv1.EthernetCapabilities_builder{
						SupportedSpeedsBps:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
						AutoNegotiationSupported: &autoNegSup,
					}.Build(),
					AppliedAutoNegotiation: phyv1.AutoNegotiationFacet_builder{
						Enabled: &autoNegEnabled,
					}.Build(),
					Copper: phyv1.CopperFacet_builder{
						Poe: phyv1.PoeFacet_builder{
							Supported: &poeSupported,
							Role:      &poeRole,
							Status:    &poeStatus,
						}.Build(),
						PoeDetail: phyv1.PoePortDetail_builder{
							PseGroup: &pseGroup,
							PsePort:  &portNum,
						}.Build(),
					}.Build(),
				}.Build(),
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:             &pvid4000,
					TaggedVlanIds:    []uint32{1000},
					UntaggedVlanIds:  []uint32{4000},
					IngressFiltering: &ingressFiltFalse,
					FrameAdmission:   &frameAdmAll,
				}.Build(),
			}.Build(),
		}.Build()

		if err := protovalidate.Validate(iface); err != nil {
			t.Fatalf("port %s validation failed: %v", name, err)
		}
		ifaces = append(ifaces, iface)
	}

	// 2 copper 1G ports 1/2/1..1/2/2 (tagged on 666 and 1000, untagged on 4000).
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("1/2/%d", i)
		iface := interfacev1.Interface_builder{
			Name:        &name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				Ethernet: phyv1.EthernetFacet_builder{
					Capabilities: phyv1.EthernetCapabilities_builder{
						SupportedSpeedsBps:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
						AutoNegotiationSupported: &autoNegSup,
					}.Build(),
					AppliedAutoNegotiation: phyv1.AutoNegotiationFacet_builder{
						Enabled: &autoNegEnabled,
					}.Build(),
					Copper: phyv1.CopperFacet_builder{}.Build(),
				}.Build(),
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:             &pvid4000,
					TaggedVlanIds:    []uint32{666, 1000},
					UntaggedVlanIds:  []uint32{4000},
					IngressFiltering: &ingressFiltFalse,
					FrameAdmission:   &frameAdmAll,
				}.Build(),
			}.Build(),
		}.Build()

		if err := protovalidate.Validate(iface); err != nil {
			t.Fatalf("port %s validation failed: %v", name, err)
		}
		ifaces = append(ifaces, iface)
	}

	// 4 SFP+ ports 1/3/1..1/3/4 (fiber 10G). Members 1/3/2 and 1/3/4 belong to LAG lg1.
	p1Name := "1/3/1"
	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Capabilities: phyv1.EthernetCapabilities_builder{
					SupportedSpeedsBps: []uint64{10_000_000_000},
				}.Build(),
				Fiber: phyv1.FiberFacet_builder{}.Build(),
			}.Build(),
			Switchport: switchingv1.SwitchportFacet_builder{
				Pvid:             &pvid4000,
				TaggedVlanIds:    []uint32{1000},
				UntaggedVlanIds:  []uint32{4000},
				IngressFiltering: &ingressFiltFalse,
				FrameAdmission:   &frameAdmAll,
			}.Build(),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(p1); err != nil {
		t.Fatalf("port 1/3/1 validation failed: %v", err)
	}
	ifaces = append(ifaces, p1)

	lagParent := "lg1"
	p2Name := "1/3/2"
	p2 := interfacev1.Interface_builder{
		Name:        &p2Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Capabilities: phyv1.EthernetCapabilities_builder{
					SupportedSpeedsBps: []uint64{10_000_000_000},
				}.Build(),
				Fiber: phyv1.FiberFacet_builder{}.Build(),
			}.Build(),
			LagParent: &lagParent,
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(p2); err != nil {
		t.Fatalf("port 1/3/2 validation failed: %v", err)
	}
	ifaces = append(ifaces, p2)

	p3Name := "1/3/3"
	p3 := interfacev1.Interface_builder{
		Name:        &p3Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Capabilities: phyv1.EthernetCapabilities_builder{
					SupportedSpeedsBps: []uint64{10_000_000_000},
				}.Build(),
				Fiber: phyv1.FiberFacet_builder{}.Build(),
			}.Build(),
			Switchport: switchingv1.SwitchportFacet_builder{
				Pvid:             &pvid4000,
				TaggedVlanIds:    []uint32{1000},
				UntaggedVlanIds:  []uint32{4000},
				IngressFiltering: &ingressFiltFalse,
				FrameAdmission:   &frameAdmAll,
			}.Build(),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(p3); err != nil {
		t.Fatalf("port 1/3/3 validation failed: %v", err)
	}
	ifaces = append(ifaces, p3)

	p4Name := "1/3/4"
	p4 := interfacev1.Interface_builder{
		Name:        &p4Name,
		AdminStatus: &adminUp,
		OperStatus:  &operDown,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Capabilities: phyv1.EthernetCapabilities_builder{
					SupportedSpeedsBps: []uint64{10_000_000_000},
				}.Build(),
				Fiber: phyv1.FiberFacet_builder{}.Build(),
			}.Build(),
			LagParent: &lagParent,
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(p4); err != nil {
		t.Fatalf("port 1/3/4 validation failed: %v", err)
	}
	ifaces = append(ifaces, p4)

	// LAG lg1
	lg1Name := "lg1"
	lg1 := interfacev1.Interface_builder{
		Name:        &lg1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Lag: interfacev1.LagInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				Pvid:             &pvid4000,
				TaggedVlanIds:    []uint32{1000},
				UntaggedVlanIds:  []uint32{4000},
				IngressFiltering: &ingressFiltFalse,
				FrameAdmission:   &frameAdmAll,
			}.Build(),
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(lg1); err != nil {
		t.Fatalf("port lg1 validation failed: %v", err)
	}
	ifaces = append(ifaces, lg1)

	// ve 1000
	veName := "ve 1000"
	veVlanID := uint32(1000)
	ve := interfacev1.Interface_builder{
		Name:        &veName,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Vlan: interfacev1.VlanInterface_builder{
			VlanId: &veVlanID,
		}.Build(),
	}.Build()
	if err := protovalidate.Validate(ve); err != nil {
		t.Fatalf("port ve 1000 validation failed: %v", err)
	}
	ifaces = append(ifaces, ve)

	// management
	mgmtName := "management"
	mgmt := interfacev1.Interface_builder{
		Name:        &mgmtName,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Management:  interfacev1.ManagementInterface_builder{}.Build(),
	}.Build()
	if err := protovalidate.Validate(mgmt); err != nil {
		t.Fatalf("port management validation failed: %v", err)
	}
	ifaces = append(ifaces, mgmt)

	// VLANs
	vid666 := uint32(666)
	vname666 := "BREACH-TARGET"
	vid1000 := uint32(1000)
	vname1000 := "VLAN1000"
	vid4000 := uint32(4000)
	vname4000 := "DEFAULT-VLAN"
	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid666, Name: &vname666}.Build(),
		switchingv1.Vlan_builder{Id: &vid1000, Name: &vname1000}.Build(),
		switchingv1.Vlan_builder{Id: &vid4000, Name: &vname4000}.Build(),
	}
	for _, v := range vlans {
		if err := protovalidate.Validate(v); err != nil {
			t.Fatalf("vlan %d validation failed: %v", v.GetId(), err)
		}
	}

	// Budget
	pseGroup := uint32(1)
	power370W := uint32(370_000)
	budgets := []*phyv1.PseBudget{
		phyv1.PseBudget_builder{
			PseGroup:        &pseGroup,
			PowerMilliwatts: &power370W,
		}.Build(),
	}
	for _, b := range budgets {
		if err := protovalidate.Validate(b); err != nil {
			t.Fatalf("budget group %d validation failed: %v", b.GetPseGroup(), err)
		}
	}

	return ifaces, vlans, budgets
}

func TestICX7150Load(t *testing.T) {
	ifaces, vlans, budgets := icx7150Fixture(t)
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)

	cfg, _, report, err := netmodel.Load(now, ifaces, vlans, nil, budgets, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}

	if len(report.Skipped) != 0 {
		t.Errorf("expected no skipped facets, got %d: %+v", len(report.Skipped), report.Skipped)
	}

	wantCaps := []port.Layer{
		port.LayerEthernet,
		port.LayerLag,
		port.LayerPoe,
		port.LayerRelay,
		port.LayerVlan,
	}
	if !slices.Equal(report.Capabilities, wantCaps) {
		t.Errorf("capabilities = %v, want %v", report.Capabilities, wantCaps)
	}

	if gotPorts := len(cfg.Ports.Ports()); gotPorts != 33 {
		t.Errorf("ports count = %d, want 33", gotPorts)
	}
}
