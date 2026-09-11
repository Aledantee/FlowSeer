package netmodel_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Transcribed from docs/research/device-inventory/lab/labsw06-ruckus-icx7150.md "Feature inventory" table (lines 121-143).
func icx7150Fixture(t *testing.T) ([]*interfacev1.Interface, []*switchingv1.Vlan, []*phyv1.PseBudget, *stpv1.BridgeState, []*stpv1.PortState) {
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

	// STP bridge state
	protoRSTP := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	localPrio := uint32(32768)
	baseMAC := addrv1.Eui48Address_builder{
		Octets: []byte{0x38, 0x45, 0x3b, 0x0f, 0xcb, 0xc0},
	}.Build()
	localBridgeID := stpv1.BridgeId_builder{
		Priority: &localPrio,
		Address:  baseMAC,
	}.Build()

	rootPrio := uint32(4096)
	rootMAC := addrv1.Eui48Address_builder{
		Octets: []byte{0x0c, 0xea, 0x14, 0x78, 0xf2, 0x04},
	}.Build()
	designatedRootID := stpv1.BridgeId_builder{
		Priority: &rootPrio,
		Address:  rootMAC,
	}.Build()

	rootPort := "lg1"
	rootCost := uint32(2000)
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion:       &protoRSTP,
		BridgeId:              localBridgeID,
		DesignatedRoot:        designatedRootID,
		RootPortInterfaceName: &rootPort,
		RootPathCost:          &rootCost,
	}.Build()
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("bridge state validation failed: %v", err)
	}

	// STP port states
	portPrio := uint32(128)
	adminCost0 := uint32(0)
	cost1G := uint32(20000)
	cost10G := uint32(2000)
	roleRoot := stpv1.PortRole_PORT_ROLE_ROOT
	roleDesig := stpv1.PortRole_PORT_ROLE_DESIGNATED
	fwdState := stpv1.ForwardingState_FORWARDING_STATE_FORWARDING

	stpPortConfigs := []struct {
		name string
		role stpv1.PortRole
		cost uint32
	}{
		{name: "lg1", role: roleRoot, cost: cost10G},
		{name: "1/1/12", role: roleDesig, cost: cost1G},
		{name: "1/3/1", role: roleDesig, cost: cost10G},
		{name: "1/3/2", role: roleRoot, cost: cost10G},
		{name: "1/3/4", role: roleRoot, cost: cost10G},
	}

	var stpPorts []*stpv1.PortState
	for _, sc := range stpPortConfigs {
		pName := sc.name
		pRole := sc.role
		pCost := sc.cost
		ps := stpv1.PortState_builder{
			InterfaceName:  &pName,
			Priority:       &portPrio,
			AdminPathCost:  &adminCost0,
			PathCost:       &pCost,
			Role:           &pRole,
			State:          &fwdState,
			DesignatedRoot: designatedRootID,
		}.Build()
		if err := protovalidate.Validate(ps); err != nil {
			t.Fatalf("port state %s validation failed: %v", sc.name, err)
		}
		stpPorts = append(stpPorts, ps)
	}

	return ifaces, vlans, budgets, bridgeState, stpPorts
}

func TestICX7150Load(t *testing.T) {
	ifaces, vlans, budgets, bridgeState, stpPorts := icx7150Fixture(t)
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)

	cfg, _, report, err := netmodel.Load(now, ifaces, vlans, nil, budgets, bridgeState, stpPorts, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}

	if len(report.Skipped) != 2 {
		t.Errorf("expected 2 skipped LAG member port states, got %d: %+v", len(report.Skipped), report.Skipped)
	}
	for _, s := range report.Skipped {
		if s.What != "stp_port" || (s.Port != "1/3/2" && s.Port != "1/3/4") {
			t.Errorf("unexpected skipped entry: %+v", s)
		}
	}

	wantCaps := []port.Layer{
		port.LayerEthernet,
		port.LayerLag,
		port.LayerPoe,
		port.LayerRelay,
		port.LayerStp,
		port.LayerVlan,
	}
	if !slices.Equal(report.Capabilities, wantCaps) {
		t.Errorf("capabilities = %v, want %v", report.Capabilities, wantCaps)
	}

	if gotPorts := len(cfg.Ports.Ports()); gotPorts != 33 {
		t.Errorf("ports count = %d, want 33", gotPorts)
	}

	if cfg.STP == nil {
		t.Fatal("expected cfg.STP to be configured")
	}
	if _, ok := cfg.STP.Ports["lg1"]; !ok {
		t.Errorf("expected cfg.STP.Ports[lg1] to exist")
	}
	if rootPort := bridgeState.GetRootPortInterfaceName(); rootPort != "lg1" {
		t.Errorf("root port = %q, want lg1", rootPort)
	}

	// The capture reports no powered device on any port and 0 mW allocated.
	alloc := cfg.Phy.Allocate()
	if g := alloc.Groups["1"]; g.AllocatedMilliwatts != 0 || g.RemainderMilliwatts != 370_000 {
		t.Errorf("group 1 allocation = %+v, want nothing allocated from 370000 mW", g)
	}
	for name, pa := range alloc.Ports {
		if pa.Milliwatts != 0 || pa.Denial != "" {
			t.Errorf("port %s allocation = %+v, want no power and no denial", name, pa)
		}
	}
}
