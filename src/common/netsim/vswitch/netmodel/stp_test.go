package netmodel_test

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func mustEncode(t *testing.T, b stp.BPDU, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := stp.Encode(b, src)
	if err != nil {
		t.Fatalf("stp.Encode: %v", err)
	}
	return frame
}

// gigabitCopper is the physical profile these fabric fixtures assume, since
// they exercise protocol export and loading rather than link negotiation:
// auto-negotiating 10 Mb/s to 1 Gb/s ends over twisted pair.
func gigabitCopper() *fabric.PhyAssumption {
	return &fabric.PhyAssumption{
		Medium: fabric.TwistedPair,
		Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		},
	}
}

func newRingTopology(t *testing.T) (*fabric.Fabric, time.Time, map[string]netaddr.MAC) {
	t.Helper()

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	newPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return tbl
	}

	macs := map[string]netaddr.MAC{
		"sw1": {0, 0, 0, 0, 1, 1},
		"sw2": {0, 0, 0, 0, 1, 2},
		"sw3": {0, 0, 0, 0, 1, 3},
	}

	cfg := fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  newPorts(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 4096,
					Address:  macs["sw1"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"sw2": {
				Ports:  newPorts(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 12288,
					Address:  macs["sw2"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
			"sw3": {
				Ports:  newPorts(),
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 8192,
					Address:  macs["sw3"],
					Ports: map[string]stp.Port{
						"1/1/2": {},
						"1/1/3": {},
					},
				},
			},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "sw2", Port: "1/1/3"},
				B:            fabric.Endpoint{Node: "sw3", Port: "1/1/2"},
				LengthMeters: 1.0,
			},
			{
				A:            fabric.Endpoint{Node: "sw3", Port: "1/1/3"},
				B:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
				LengthMeters: 1.0,
			},
		},
		Uncabled: []fabric.Uncabled{
			{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{Endpoint: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			{Endpoint: fabric.Endpoint{Node: "sw3", Port: "1/1/1"}},
		},
		PhyAssumption: gigabitCopper(),
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	return fab, t0, macs
}

func roleMatches(protoRole stpv1.PortRole, layerRole stp.Role) bool {
	switch layerRole {
	case stp.RoleRoot:
		return protoRole == stpv1.PortRole_PORT_ROLE_ROOT
	case stp.RoleDesignated:
		return protoRole == stpv1.PortRole_PORT_ROLE_DESIGNATED
	case stp.RoleAlternate:
		return protoRole == stpv1.PortRole_PORT_ROLE_ALTERNATE
	case stp.RoleBackup:
		return protoRole == stpv1.PortRole_PORT_ROLE_BACKUP
	case stp.RoleDisabled:
		return protoRole == stpv1.PortRole_PORT_ROLE_DISABLED
	default:
		return protoRole == stpv1.PortRole_PORT_ROLE_UNSPECIFIED
	}
}

func stateMatches(protoState stpv1.ForwardingState, layerState stp.State) bool {
	switch layerState {
	case stp.StateDiscarding:
		return protoState == stpv1.ForwardingState_FORWARDING_STATE_DISCARDING
	case stp.StateLearning:
		return protoState == stpv1.ForwardingState_FORWARDING_STATE_LEARNING
	case stp.StateForwarding:
		return protoState == stpv1.ForwardingState_FORWARDING_STATE_FORWARDING
	default:
		return protoState == stpv1.ForwardingState_FORWARDING_STATE_UNSPECIFIED
	}
}

func TestStpExportAndLoad_RingConvergence(t *testing.T) {
	fab, t0, macs := newRingTopology(t)

	fab.Run(200)

	sw1MAC := macs["sw1"]
	swNames := []string{"sw1", "sw2", "sw3"}

	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP

	for _, name := range swNames {
		sw := fab.Switch(name)
		if sw == nil {
			t.Fatalf("switch %s not found in fabric", name)
		}

		bridgeState, portStates := netmodel.Stp(fab.Snapshot().Clock, sw)
		if bridgeState == nil {
			t.Fatalf("switch %s exported nil BridgeState", name)
		}

		if err := protovalidate.Validate(bridgeState); err != nil {
			t.Errorf("switch %s BridgeState validation failed: %v", name, err)
		}

		desigRoot := bridgeState.GetDesignatedRoot()
		if desigRoot.GetPriority() != 4096 {
			t.Errorf("switch %s designated root priority = %d, want 4096", name, desigRoot.GetPriority())
		}
		if !bytes.Equal(desigRoot.GetAddress().GetOctets(), sw1MAC[:]) {
			t.Errorf("switch %s designated root MAC = %x, want %x", name, desigRoot.GetAddress().GetOctets(), sw1MAC[:])
		}
		if bridgeState.GetTxHoldCount() != 6 {
			t.Errorf("switch %s BridgeState tx_hold_count = %d, want 6", name, bridgeState.GetTxHoldCount())
		}

		roles := sw.Roles()
		if len(portStates) != len(roles) {
			t.Errorf("switch %s port states count = %d, want %d", name, len(portStates), len(roles))
		}

		for _, ps := range portStates {
			if err := protovalidate.Validate(ps); err != nil {
				t.Errorf("switch %s PortState %s validation failed: %v", name, ps.GetInterfaceName(), err)
			}

			info, ok := roles[ps.GetInterfaceName()]
			if !ok {
				t.Errorf("switch %s PortState %s not found in Roles()", name, ps.GetInterfaceName())
				continue
			}

			if !roleMatches(ps.GetRole(), info.Role) {
				t.Errorf("switch %s port %s role = %v, want %v", name, ps.GetInterfaceName(), ps.GetRole(), info.Role)
			}
			if !stateMatches(ps.GetState(), info.State) {
				t.Errorf("switch %s port %s state = %v, want %v", name, ps.GetInterfaceName(), ps.GetState(), info.State)
			}
			if ps.GetOperProtocolVersion() != stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP {
				t.Errorf("switch %s port %s oper_protocol_version = %v, want RSTP", name, ps.GetInterfaceName(), ps.GetOperProtocolVersion())
			}
			if ps.GetRole() == stpv1.PortRole_PORT_ROLE_DESIGNATED && ps.GetTxBpdus() == 0 {
				t.Errorf("switch %s designated port %s tx_bpdus = 0, want > 0", name, ps.GetInterfaceName())
			}
		}

		var ifaces []*interfacev1.Interface
		for _, p := range sw.Ports().Ports() {
			pName := p.Name
			ifaces = append(ifaces, interfacev1.Interface_builder{
				Name:        &pName,
				AdminStatus: &adminUp,
				OperStatus:  &operUp,
			}.Build())
		}

		res, err := netmodel.Load(t0, netmodel.SourceContext{DeviceID: name}, ifaces, nil, nil, nil, bridgeState, portStates, nil, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("switch %s Load failed: %v", name, err)
		}
		loadedCfg := res.Spec.Config

		if loadedCfg.STP == nil {
			t.Fatalf("switch %s loaded STP configuration is nil", name)
		}

		diff := stp.Diff(*sw.Config().STP, *loadedCfg.STP)
		if len(diff) != 0 {
			t.Errorf("switch %s round-trip STP diff not empty: %+v", name, diff)
		}
	}
}

// A port migrated by receiving an inferior Configuration BPDU after its migration
// delay exports oper_protocol_version STP, while unmigrated ports export RSTP,
// and every exported message passes validation.
func TestStpExport_MigratedPort(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	sw, err := vswitch.New(vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority:    32768,
			Address:     netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02},
			TxHoldCount: 6,
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	})
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	sw.Start(t0)
	sw.Drain()

	sw.Wake(t0.Add(4 * time.Second))
	sw.Drain()

	inferiorBridgeID := stp.BridgeID{
		Priority: 61440,
		Address:  netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c},
	}
	inferiorBPDU := stp.BPDU{
		Type:         stp.BPDUTypeConfiguration,
		RootID:       inferiorBridgeID,
		BridgeID:     inferiorBridgeID,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	frame := mustEncode(t, inferiorBPDU, inferiorBridgeID.Address)
	sw.Forward(t0.Add(4*time.Second), "1/1/1", frame)
	sw.Drain()

	bridgeState, portStates := netmodel.Stp(t0.Add(4*time.Second), sw)
	if bridgeState == nil {
		t.Fatal("exported BridgeState is nil")
	}
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("BridgeState validation failed: %v", err)
	}

	for _, ps := range portStates {
		if err := protovalidate.Validate(ps); err != nil {
			t.Fatalf("PortState %s validation failed: %v", ps.GetInterfaceName(), err)
		}
		switch ps.GetInterfaceName() {
		case "1/1/1":
			if got := ps.GetOperProtocolVersion(); got != stpv1.ProtocolVersion_PROTOCOL_VERSION_STP {
				t.Errorf("port 1/1/1 oper_protocol_version = %v, want STP", got)
			}
		case "1/1/2":
			if got := ps.GetOperProtocolVersion(); got != stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP {
				t.Errorf("port 1/1/2 oper_protocol_version = %v, want RSTP", got)
			}
		}
	}
}

func TestStpLoad_LagMemberSkipped(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	lagParent := "lg1"
	p1Name := "1/1/1"
	p2Name := "1/1/2"
	lgName := "lg1"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &lgName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Lag:         interfacev1.LagInterface_builder{}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParent: &lagParent,
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p2Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
	}

	prio := uint32(32768)
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	addr := addrv1.Eui48Address_builder{Octets: []byte{0, 0, 0, 0, 1, 1}}.Build()
	bridgeID := stpv1.BridgeId_builder{Priority: &prio, Address: addr}.Build()
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId:        bridgeID,
	}.Build()
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("bridge state validation failed: %v", err)
	}

	portPrio := uint32(128)
	adminCost0 := uint32(0)
	cost := uint32(20000)
	roleRoot := stpv1.PortRole_PORT_ROLE_ROOT
	fwd := stpv1.ForwardingState_FORWARDING_STATE_FORWARDING

	psMember := stpv1.PortState_builder{
		InterfaceName: &p1Name,
		Priority:      &portPrio,
		AdminPathCost: &adminCost0,
		PathCost:      &cost,
		Role:          &roleRoot,
		State:         &fwd,
	}.Build()
	psRegular := stpv1.PortState_builder{
		InterfaceName: &p2Name,
		Priority:      &portPrio,
		AdminPathCost: &adminCost0,
		PathCost:      &cost,
		Role:          &roleRoot,
		State:         &fwd,
	}.Build()

	for _, ps := range []*stpv1.PortState{psMember, psRegular} {
		if err := protovalidate.Validate(ps); err != nil {
			t.Fatalf("port state %s validation failed: %v", ps.GetInterfaceName(), err)
		}
	}

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, bridgeState, []*stpv1.PortState{psMember, psRegular}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/1" && s.What == "stp_port" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected LAG member 1/1/1 to be skipped in report: %+v", report.Skipped)
	}

	if _, ok := cfg.STP.Ports["1/1/1"]; ok {
		t.Errorf("expected LAG member 1/1/1 to be excluded from STP ports")
	}
	if _, ok := cfg.STP.Ports["1/1/2"]; !ok {
		t.Errorf("expected regular port 1/1/2 to be included in STP ports")
	}
}

func TestStpLoad_AbsentPortSkipped(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"
	pAbsent := "1/1/99"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
	}

	prio := uint32(32768)
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	addr := addrv1.Eui48Address_builder{Octets: []byte{0, 0, 0, 0, 1, 1}}.Build()
	bridgeID := stpv1.BridgeId_builder{Priority: &prio, Address: addr}.Build()
	bridgeState := stpv1.BridgeState_builder{ProtocolVersion: &protocol, BridgeId: bridgeID}.Build()
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("bridge state validation failed: %v", err)
	}

	portPrio := uint32(128)
	adminCost0 := uint32(0)
	cost := uint32(20000)
	roleDesig := stpv1.PortRole_PORT_ROLE_DESIGNATED
	fwd := stpv1.ForwardingState_FORWARDING_STATE_FORWARDING

	psAbsent := stpv1.PortState_builder{
		InterfaceName: &pAbsent,
		Priority:      &portPrio,
		AdminPathCost: &adminCost0,
		PathCost:      &cost,
		Role:          &roleDesig,
		State:         &fwd,
	}.Build()
	if err := protovalidate.Validate(psAbsent); err != nil {
		t.Fatalf("port state validation failed: %v", err)
	}

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, bridgeState, []*stpv1.PortState{psAbsent}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/99" && s.What == "stp_port" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected absent port 1/1/99 to be skipped: %+v", report.Skipped)
	}
	if _, ok := cfg.STP.Ports["1/1/99"]; ok {
		t.Errorf("expected absent port 1/1/99 to be excluded from STP ports")
	}
}

func TestStpLoad_MissingAdminPathCostReported(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
	}

	prio := uint32(32768)
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	addr := addrv1.Eui48Address_builder{Octets: []byte{0, 0, 0, 0, 1, 1}}.Build()
	bridgeID := stpv1.BridgeId_builder{Priority: &prio, Address: addr}.Build()
	bridgeState := stpv1.BridgeState_builder{ProtocolVersion: &protocol, BridgeId: bridgeID}.Build()
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("bridge state validation failed: %v", err)
	}

	portPrio := uint32(128)
	cost := uint32(20000)
	roleDesig := stpv1.PortRole_PORT_ROLE_DESIGNATED
	fwd := stpv1.ForwardingState_FORWARDING_STATE_FORWARDING

	// Deliberately omit AdminPathCost
	psWithoutAdminCost := stpv1.PortState_builder{
		InterfaceName: &p1Name,
		Priority:      &portPrio,
		PathCost:      &cost,
		Role:          &roleDesig,
		State:         &fwd,
	}.Build()
	if err := protovalidate.Validate(psWithoutAdminCost); err != nil {
		t.Fatalf("port state validation failed: %v", err)
	}

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, bridgeState, []*stpv1.PortState{psWithoutAdminCost}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundDefault := false
	for _, d := range report.Defaults {
		if d.Port == "1/1/1" && d.Field == "admin_path_cost" && d.Value == "0" {
			foundDefault = true
			break
		}
	}
	if !foundDefault {
		t.Errorf("expected default for admin_path_cost reported: %+v", report.Defaults)
	}

	if portCfg, ok := cfg.STP.Ports["1/1/1"]; !ok || portCfg.PathCost != 0 {
		t.Errorf("expected STP port 1/1/1 PathCost = 0, got %v (exists=%t)", portCfg.PathCost, ok)
	}
}

func TestStpExport_NoLayerReturnsNil(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	sw, err := vswitch.New(vswitch.Config{Ports: tbl})
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	bState, pStates := netmodel.Stp(time.Time{}, sw)
	if bState != nil || pStates != nil {
		t.Errorf("Stp(sw without stp) = (%v, %v), want (nil, nil)", bState, pStates)
	}
}

// TestStpExportCarriesEffectiveValues is evidence that the export reports
// what the layer runs with: a configuration that left the bridge and port
// priorities zero exports the 802.1D defaults, and the time since the last
// topology change is measured from the caller's clock.
func TestStpExportCarriesEffectiveValues(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	sw, err := vswitch.New(vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Address: netaddr.MAC{0, 0, 0, 0, 1, 1},
			Ports:   map[string]stp.Port{"1/1/1": {}},
		},
	})
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	sw.Start(now)
	sw.Drain()

	bridgeState, portStates := netmodel.Stp(now.Add(5*time.Second), sw)
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("exported bridge state fails validation: %v", err)
	}
	if got := bridgeState.GetBridgeId().GetPriority(); got != uint32(stp.DefaultBridgePriority) {
		t.Errorf("bridge priority = %d, want %d", got, stp.DefaultBridgePriority)
	}
	if got := bridgeState.GetTxHoldCount(); got != uint32(stp.DefaultTxHoldCount) {
		t.Errorf("bridge tx_hold_count = %d, want %d", got, stp.DefaultTxHoldCount)
	}
	if len(portStates) != 1 {
		t.Fatalf("port states = %d, want 1", len(portStates))
	}
	if err := protovalidate.Validate(portStates[0]); err != nil {
		t.Fatalf("exported port state fails validation: %v", err)
	}
	if got := portStates[0].GetPriority(); got != uint32(stp.DefaultPortPriority) {
		t.Errorf("port priority = %d, want %d", got, stp.DefaultPortPriority)
	}
	if bridgeState.GetDesignatedRoot() == portStates[0].GetDesignatedRoot() {
		t.Error("bridge and port rows share one designated root message")
	}

	_, lastTC := sw.TopologyChanges()
	if lastTC.IsZero() {
		if bridgeState.HasTimeSinceTopologyChange() {
			t.Error("time since topology change present without a topology change")
		}
		return
	}
	if got := bridgeState.GetTimeSinceTopologyChange().AsDuration(); got != now.Add(5*time.Second).Sub(lastTC) {
		t.Errorf("time since topology change = %v, want %v", got, now.Add(5*time.Second).Sub(lastTC))
	}
}

// TestLoadSkipsBridgeWithoutAddress guards the loader against a bridge state
// the wire would refuse: without an address the layer cannot be configured,
// so the report says so and no spanning tree layer is built.
func TestLoadSkipsBridgeWithoutAddress(t *testing.T) {
	name := "1/1/1"
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
	}
	prio := uint32(32768)
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId:        stpv1.BridgeId_builder{Priority: &prio}.Build(),
	}.Build()

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, bridgeState, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report
	if cfg.STP != nil {
		t.Errorf("Load built a spanning tree layer from a bridge without an address")
	}
	found := false
	for _, s := range report.Skipped {
		if s.What == "stp_bridge" {
			found = true
		}
	}
	if !found {
		t.Errorf("report.Skipped = %+v, want an stp_bridge entry", report.Skipped)
	}
}

// A bridge state with tx_hold_count 4 and a port state with auto_edge true load
// into spanning tree configuration with TxHoldCount 4 and Port.AutoEdge true.
func TestStpLoad_TxHoldCountAndAutoEdge(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
	}

	prio := uint32(32768)
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	addr := addrv1.Eui48Address_builder{Octets: []byte{0, 0, 0, 0, 1, 1}}.Build()
	bridgeID := stpv1.BridgeId_builder{Priority: &prio, Address: addr}.Build()
	txHold4 := uint32(4)
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId:        bridgeID,
		TxHoldCount:     &txHold4,
	}.Build()
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("bridge state validation failed: %v", err)
	}

	portPrio := uint32(128)
	adminCost0 := uint32(0)
	cost := uint32(20000)
	roleDesig := stpv1.PortRole_PORT_ROLE_DESIGNATED
	fwd := stpv1.ForwardingState_FORWARDING_STATE_FORWARDING
	autoEdgeTrue := true

	ps := stpv1.PortState_builder{
		InterfaceName: &p1Name,
		Priority:      &portPrio,
		AdminPathCost: &adminCost0,
		PathCost:      &cost,
		Role:          &roleDesig,
		State:         &fwd,
		AutoEdge:      &autoEdgeTrue,
	}.Build()
	if err := protovalidate.Validate(ps); err != nil {
		t.Fatalf("port state validation failed: %v", err)
	}

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, bridgeState, []*stpv1.PortState{ps}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := res.Spec.Config

	if cfg.STP == nil {
		t.Fatal("Load returned nil STP config")
	}
	if cfg.STP.TxHoldCount != 4 {
		t.Errorf("TxHoldCount = %d, want 4", cfg.STP.TxHoldCount)
	}
	if !cfg.STP.Ports["1/1/1"].AutoEdge {
		t.Errorf("port 1/1/1 AutoEdge = false, want true")
	}
}

// An absent tx_hold_count on a bridge state loads with default 6 recorded in
// the loader report defaults.
func TestStpLoad_AbsentTxHoldCountReportedDefault(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
	}

	prio := uint32(32768)
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	addr := addrv1.Eui48Address_builder{Octets: []byte{0, 0, 0, 0, 1, 1}}.Build()
	bridgeID := stpv1.BridgeId_builder{Priority: &prio, Address: addr}.Build()
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId:        bridgeID,
	}.Build()
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("bridge state validation failed: %v", err)
	}

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, bridgeState, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	if cfg.STP == nil {
		t.Fatal("Load returned nil STP config")
	}

	foundDefault := false
	for _, d := range report.Defaults {
		if d.Field == "tx_hold_count" && d.Value == "6" {
			foundDefault = true
			break
		}
	}
	if !foundDefault {
		t.Errorf("expected default for tx_hold_count reported: %+v", report.Defaults)
	}
}

func TestStpLoadPreservesExplicitZeroPrioritiesAndReportsTimerDefaults(t *testing.T) {
	name := "1/1/1"
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	zero := uint32(0)
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Priority: &zero,
			Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		}.Build(),
	}.Build()
	portState := stpv1.PortState_builder{
		InterfaceName: &name,
		Priority:      &zero,
	}.Build()

	result, err := netmodel.Load(
		time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "zero-priorities"},
		[]*interfacev1.Interface{plainPhysicalInterface(name)},
		nil, nil, nil, bridgeState, []*stpv1.PortState{portState}, nil, nil, nil, nil,
		[]port.Layer{port.LayerStp},
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Spec.Config.STP == nil {
		t.Fatal("STP config is nil")
	}
	if cfg := result.Spec.Config.STP; cfg.Priority != 0 || !cfg.PriorityPresent {
		t.Errorf("bridge priority = (%d, present=%t), want explicit zero", cfg.Priority, cfg.PriorityPresent)
	}
	if cfg := result.Spec.Config.STP.Ports[name]; cfg.Priority != 0 || !cfg.PriorityPresent {
		t.Errorf("port priority = (%d, present=%t), want explicit zero", cfg.Priority, cfg.PriorityPresent)
	}
	sw, err := vswitch.NewWithSpec(result.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	constructed := sw.Config().STP
	if constructed == nil || constructed.Priority != 0 || constructed.Ports[name].Priority != 0 {
		t.Errorf("constructed STP priorities = %+v, want explicit bridge and port zero", constructed)
	}

	wantDefaults := map[string]string{
		"bridge_hello_time":    "2s",
		"bridge_max_age":       "20s",
		"bridge_forward_delay": "15s",
		"tx_hold_count":        "6",
	}
	for field, value := range wantDefaults {
		if !slices.ContainsFunc(result.Report.Defaults, func(got netmodel.Default) bool {
			return got.Field == field && got.Value == value && len(got.Evidence) > 0
		}) {
			t.Errorf("defaults = %+v, want evidenced %s=%s", result.Report.Defaults, field, value)
		}
		statement := "default value applied for " + field + ": " + value
		if !slices.ContainsFunc(result.Metadata.Assumptions(), func(got analysis.Assumption) bool {
			return got.Statement == statement && len(got.Evidence) > 0
		}) {
			t.Errorf("assumptions = %+v, want %q", result.Metadata.Assumptions(), statement)
		}
	}
	for _, field := range []string{"bridge_priority", "port_priority"} {
		if slices.ContainsFunc(result.Report.Defaults, func(got netmodel.Default) bool { return got.Field == field }) {
			t.Errorf("explicit zero %s was reported as defaulted", field)
		}
	}
}

func TestStpLoadDefaultsOnlyAbsentPriorities(t *testing.T) {
	name := "1/1/1"
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Address: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		}.Build(),
	}.Build()
	portState := stpv1.PortState_builder{InterfaceName: &name}.Build()

	result, err := netmodel.Load(
		time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "absent-priorities"},
		[]*interfacev1.Interface{plainPhysicalInterface(name)},
		nil, nil, nil, bridgeState, []*stpv1.PortState{portState}, nil, nil, nil, nil,
		[]port.Layer{port.LayerStp},
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for field, value := range map[string]string{
		"bridge_priority": "32768",
		"port_priority":   "128",
	} {
		if !slices.ContainsFunc(result.Report.Defaults, func(got netmodel.Default) bool {
			return got.Field == field && got.Value == value && len(got.Evidence) > 0
		}) {
			t.Errorf("defaults = %+v, want evidenced %s=%s", result.Report.Defaults, field, value)
		}
	}

	sw, err := vswitch.NewWithSpec(result.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	cfg := sw.Config().STP
	if cfg == nil {
		t.Fatal("constructed STP config is nil")
	}
	if cfg.Priority != stp.DefaultBridgePriority {
		t.Errorf("constructed bridge priority = %d, want %d", cfg.Priority, stp.DefaultBridgePriority)
	}
	if got := cfg.Ports[name].Priority; got != stp.DefaultPortPriority {
		t.Errorf("constructed port priority = %d, want %d", got, stp.DefaultPortPriority)
	}
}

func TestStpLoadSkipsAdminPathCostAboveMaximum(t *testing.T) {
	t.Parallel()

	name := "1/1/1"
	validName := "1/1/2"
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	priority := uint32(32768)
	adminPathCost := uint32(200_000_001)
	validAdminPathCost := stp.MaxPathCost
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Priority: &priority,
			Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		}.Build(),
	}.Build()
	portState := stpv1.PortState_builder{
		InterfaceName: &name,
		AdminPathCost: &adminPathCost,
	}.Build()
	validPortState := stpv1.PortState_builder{
		InterfaceName: &validName,
		AdminPathCost: &validAdminPathCost,
	}.Build()

	result, err := netmodel.Load(
		time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "invalid-admin-path-cost"},
		[]*interfacev1.Interface{plainPhysicalInterface(name), plainPhysicalInterface(validName)},
		nil, nil, nil, bridgeState, []*stpv1.PortState{portState, validPortState}, nil, nil, nil, nil,
		[]port.Layer{port.LayerStp},
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Spec.Config.STP == nil {
		t.Fatal("STP config is nil")
	}
	if _, ok := result.Spec.Config.STP.Ports[name]; ok {
		t.Errorf("STP config contains port with unsupported admin path cost")
	}
	if got, ok := result.Spec.Config.STP.Ports[validName]; !ok || got.PathCost != stp.MaxPathCost {
		t.Errorf("valid sibling STP port = (%+v, present=%t), want maximum path cost", got, ok)
	}

	scope := analysis.FieldScope(analysis.ProtocolScope("sw1", string(port.LayerStp), "0"), "ports", name)
	if !slices.ContainsFunc(result.Report.Skipped, func(got netmodel.Skipped) bool {
		return got.Scope == scope && got.Port == name && got.What == "stp_port" && len(got.Evidence) > 0
	}) {
		t.Errorf("skipped = %+v, want evidenced port-scoped STP row", result.Report.Skipped)
	}
	if !slices.ContainsFunc(result.Metadata.IssuesFor(scope), func(got analysis.Issue) bool {
		return got.Code == analysis.IssueCode("netmodel.stp.invalid_admin_path_cost") &&
			got.Status == analysis.Unsupported && len(got.Evidence) > 0
	}) {
		t.Errorf("issues = %+v, want evidenced unsupported admin path cost", result.Metadata.IssuesFor(scope))
	}
	if got := result.Metadata.StatusFor(analysis.PortScope("sw1", validName)); got != analysis.Complete {
		t.Errorf("valid sibling status = %v, want Complete", got)
	}
}

func TestStpLoadRecordsFallbackForInvalidTxHoldCount(t *testing.T) {
	t.Parallel()

	name := "1/1/1"
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	priority := uint32(32768)
	invalidTxHoldCount := uint32(11)
	bridgeState := stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Priority: &priority,
			Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		}.Build(),
		TxHoldCount: &invalidTxHoldCount,
	}.Build()

	result, err := netmodel.Load(
		time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "invalid-tx-hold-count"},
		[]*interfacev1.Interface{plainPhysicalInterface(name)},
		nil, nil, nil, bridgeState, nil, nil, nil, nil, nil,
		[]port.Layer{port.LayerStp},
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Spec.Config.STP == nil {
		t.Fatal("STP config is nil")
	}
	if got := result.Spec.Config.STP.TxHoldCount; got != stp.DefaultTxHoldCount {
		t.Errorf("tx hold count = %d, want fallback %d", got, stp.DefaultTxHoldCount)
	}
	if !slices.ContainsFunc(result.Report.Skipped, func(got netmodel.Skipped) bool {
		return got.Scope == analysis.ProtocolScope("sw1", string(port.LayerStp), "0") &&
			got.What == "stp_tx_hold_count" && len(got.Evidence) > 0
	}) {
		t.Errorf("skipped = %+v, want evidenced invalid tx_hold_count", result.Report.Skipped)
	}
	if !slices.ContainsFunc(result.Metadata.Issues(), func(got analysis.Issue) bool {
		return got.Code == netmodel.IssueInvalidTxHoldCount && got.Status == analysis.Unsupported && len(got.Evidence) > 0
	}) {
		t.Errorf("issues = %+v, want evidenced invalid tx_hold_count", result.Metadata.Issues())
	}
	if !slices.ContainsFunc(result.Report.Defaults, func(got netmodel.Default) bool {
		return got.Field == "tx_hold_count" && got.Value == "6" && len(got.Evidence) > 0
	}) {
		t.Errorf("defaults = %+v, want evidenced tx_hold_count fallback", result.Report.Defaults)
	}
	if !slices.ContainsFunc(result.Metadata.Assumptions(), func(got analysis.Assumption) bool {
		return got.Statement == "default value applied for tx_hold_count: 6" && len(got.Evidence) > 0
	}) {
		t.Errorf("assumptions = %+v, want evidenced tx_hold_count fallback", result.Metadata.Assumptions())
	}
}

func TestStpLoadClassifiesInvalidEffectiveTimerRelations(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		helloTime    time.Duration
		maxAge       time.Duration
		forwardDelay time.Duration
	}{
		{name: "explicit minimum violation", helloTime: 3 * time.Second, maxAge: 6 * time.Second, forwardDelay: 4 * time.Second},
		{name: "explicit maximum violation", helloTime: time.Second, maxAge: 7 * time.Second, forwardDelay: 4 * time.Second},
		{name: "explicit hello against default max", helloTime: 10 * time.Second},
		{name: "explicit max against default forward", maxAge: 40 * time.Second},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
			priority := uint32(32768)
			builder := stpv1.BridgeState_builder{
				ProtocolVersion: &protocol,
				BridgeId: stpv1.BridgeId_builder{
					Priority: &priority,
					Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
				}.Build(),
			}
			if test.helloTime != 0 {
				builder.BridgeHelloTime = durationpb.New(test.helloTime)
			}
			if test.maxAge != 0 {
				builder.BridgeMaxAge = durationpb.New(test.maxAge)
			}
			if test.forwardDelay != 0 {
				builder.BridgeForwardDelay = durationpb.New(test.forwardDelay)
			}
			input := loadInput{
				ifaces:      []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
				bridgeState: builder.Build(),
			}
			input.validate(t)
			result := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: test.name})

			if result.Spec.Config.STP != nil {
				t.Errorf("STP config = %+v, want invalid layer omitted", result.Spec.Config.STP)
			}
			if slices.Contains(result.Report.Capabilities, port.LayerStp) {
				t.Errorf("capabilities = %v, want STP omitted", result.Report.Capabilities)
			}
			if !slices.ContainsFunc(result.Report.Skipped, func(skipped netmodel.Skipped) bool {
				return skipped.What == "stp_bridge" && len(skipped.Evidence) > 0
			}) {
				t.Errorf("skipped = %+v, want evidenced STP bridge input", result.Report.Skipped)
			}
			if !slices.ContainsFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
				return issue.Code == netmodel.IssueInvalidSTPBridgeTimer &&
					issue.Status == analysis.Unsupported && len(issue.Evidence) > 0
			}) {
				t.Errorf("issues = %+v, want evidenced Unsupported timer relation", result.Metadata.Issues())
			}
		})
	}
}
