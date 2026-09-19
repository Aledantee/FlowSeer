package netmodel_test

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func newLacpFabric(t *testing.T, start time.Time, macA, macB netaddr.MAC, lagA, lagB *lag.Config) *fabric.Fabric {
	t.Helper()

	vid10 := vlan.ID(10)
	buildPorts := func() port.Table {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
		b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return tbl
	}

	bridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "VLAN10",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/4": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"lag1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		}
	}

	cfg := fabric.Config{
		Start: start,
		Switches: map[string]vswitch.Config{
			"A": {
				MAC:    macA,
				Ports:  buildPorts(),
				Bridge: bridgeCfg(),
				LAG:    lagA,
			},
			"B": {
				MAC:    macB,
				Ports:  buildPorts(),
				Bridge: bridgeCfg(),
				LAG:    lagB,
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "A", Port: "1/1/1"}, B: fabric.Endpoint{Node: "B", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "A", Port: "1/1/2"}, B: fabric.Endpoint{Node: "B", Port: "1/1/2"}},
		},
		Uncabled: []fabric.Uncabled{
			{Endpoint: fabric.Endpoint{Node: "A", Port: "1/1/3"}},
			{Endpoint: fabric.Endpoint{Node: "A", Port: "1/1/4"}},
			{Endpoint: fabric.Endpoint{Node: "B", Port: "1/1/3"}},
			{Endpoint: fabric.Endpoint{Node: "B", Port: "1/1/4"}},
		},
		PhyAssumption: gigabitCopper(),
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab
}

func TestLoad_LagCustomConfiguration(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	lagName := "lag1"
	p1Name := "1/1/1"
	p2Name := "1/1/2"

	mode := switchingv1.BondMode_BOND_MODE_BALANCE_SLB
	upDelay := durationpb.New(2 * time.Second)

	lagIface := interfacev1.Interface_builder{
		Name:        &lagName,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Lag: interfacev1.LagInterface_builder{
			Aggregation: switchingv1.AggregationFacet_builder{
				BondMode: &mode,
				UpDelay:  upDelay,
			}.Build(),
		}.Build(),
	}.Build()

	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			LagParent: &lagName,
		}.Build(),
	}.Build()

	p2 := interfacev1.Interface_builder{
		Name:        &p2Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			LagParent: &lagName,
		}.Build(),
	}.Build()

	lacpActive := lacpv1.LacpMode_LACP_MODE_ACTIVE
	fast := true
	key := uint32(7)
	aggState := lacpv1.AggregatorState_builder{
		InterfaceName: &lagName,
		Mode:          &lacpActive,
		Fast:          &fast,
		Key:           &key,
	}.Build()

	for _, m := range []protoreflect.ProtoMessage{lagIface, p1, p2, aggState} {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("validation failed for %v: %v", m, err)
		}
	}

	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(t0, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{lagIface, p1, p2}, nil, nil, nil, nil, nil, []*lacpv1.AggregatorState{aggState}, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config

	if cfg.LAG == nil {
		t.Fatal("expected cfg.LAG to be non-nil")
	}
	l, ok := cfg.LAG.LAGs["lag1"]
	if !ok {
		t.Fatal("missing lag1 in cfg.LAG.LAGs")
	}
	if l.Mode != lag.BalanceSLB {
		t.Errorf("Mode = %v, want %v", l.Mode, lag.BalanceSLB)
	}
	if l.UpDelay != 2*time.Second {
		t.Errorf("UpDelay = %v, want %v", l.UpDelay, 2*time.Second)
	}
	if l.LACP.Mode != lag.Active {
		t.Errorf("LACP.Mode = %v, want %v", l.LACP.Mode, lag.Active)
	}
	if !l.LACP.Fast {
		t.Errorf("LACP.Fast = false, want true")
	}
	if l.LACP.Key != 7 {
		t.Errorf("LACP.Key = %d, want 7", l.LACP.Key)
	}
	if len(l.Members) != 2 {
		t.Errorf("len(Members) = %d, want 2", len(l.Members))
	}
	if _, ok := l.Members["1/1/1"]; !ok {
		t.Errorf("missing member 1/1/1 in Members")
	}
	if _, ok := l.Members["1/1/2"]; !ok {
		t.Errorf("missing member 1/1/2 in Members")
	}
}

func TestLoad_LagDefaultBondMode(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	lagName := "lag1"
	p1Name := "1/1/1"

	lagIface := interfacev1.Interface_builder{
		Name:        &lagName,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Lag: interfacev1.LagInterface_builder{
			Aggregation: switchingv1.AggregationFacet_builder{}.Build(),
		}.Build(),
	}.Build()

	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			LagParent: &lagName,
		}.Build(),
	}.Build()

	for _, m := range []protoreflect.ProtoMessage{lagIface, p1} {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("validation failed for %v: %v", m, err)
		}
	}

	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(t0, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{lagIface, p1}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	if cfg.LAG == nil {
		t.Fatal("expected cfg.LAG to be non-nil")
	}
	l, ok := cfg.LAG.LAGs["lag1"]
	if !ok {
		t.Fatal("missing lag1 in cfg.LAG.LAGs")
	}
	if l.Mode != lag.ActiveBackup {
		t.Errorf("Mode = %v, want %v", l.Mode, lag.ActiveBackup)
	}

	foundDefault := false
	for _, d := range report.Defaults {
		if d.Port == "lag1" && d.Field == "bond_mode" && d.Value == "active-backup" {
			foundDefault = true
			break
		}
	}
	if !foundDefault {
		t.Errorf("expected default bond_mode active-backup in report.Defaults, got: %+v", report.Defaults)
	}
}

func TestLacpExport_Converged(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0b}

	lagCfgA := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:    lag.ActiveBackup,
				Primary: "1/1/1",
				LACP: lag.LACPConfig{
					Mode:           lag.Active,
					Fast:           true,
					SystemPriority: 32768,
					SystemID:       macA,
					Key:            1,
				},
				Members: map[string]lag.Member{
					"1/1/1": {Priority: 32768, Key: 1},
					"1/1/2": {Priority: 32768, Key: 1},
				},
			},
		},
	}
	lagCfgB := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:    lag.ActiveBackup,
				Primary: "1/1/1",
				LACP: lag.LACPConfig{
					Mode:           lag.Active,
					Fast:           true,
					SystemPriority: 32768,
					SystemID:       macB,
					Key:            1,
				},
				Members: map[string]lag.Member{
					"1/1/1": {Priority: 32768, Key: 1},
					"1/1/2": {Priority: 32768, Key: 1},
				},
			},
		},
	}

	fab := newLacpFabric(t, t0, macA, macB, lagCfgA, lagCfgB)

	target := t0.Add(3 * time.Second)
	for {
		snap := fab.Snapshot()
		if !snap.Clock.Before(target) && len(snap.Queue) == 0 {
			break
		}
		if snap.Clock.After(target) {
			break
		}
		if _, ok := fab.Step(); !ok {
			break
		}
	}

	swA := fab.Switch("A")

	aggs, portStates := netmodel.Lacp(swA)

	if len(aggs) != 1 {
		t.Fatalf("expected 1 AggregatorState, got %d", len(aggs))
	}
	agg := aggs[0]
	if err := protovalidate.Validate(agg); err != nil {
		t.Errorf("aggregator state validation failed: %v", err)
	}
	if agg.GetInterfaceName() != "lag1" {
		t.Errorf("agg interface_name = %q, want lag1", agg.GetInterfaceName())
	}
	wantSelected := []string{"1/1/1", "1/1/2"}
	if !slices.Equal(agg.GetSelectedMembers(), wantSelected) {
		t.Errorf("agg selected_members = %v, want %v", agg.GetSelectedMembers(), wantSelected)
	}

	if len(portStates) != 2 {
		t.Fatalf("expected 2 PortState rows, got %d", len(portStates))
	}
	for _, ps := range portStates {
		if err := protovalidate.Validate(ps); err != nil {
			t.Errorf("port state %s validation failed: %v", ps.GetInterfaceName(), err)
		}
		if !ps.GetAttached() {
			t.Errorf("port %s attached = false, want true", ps.GetInterfaceName())
		}
		if !ps.GetEnabled() {
			t.Errorf("port %s enabled = false, want true", ps.GetInterfaceName())
		}
		if ps.GetPartner() == nil || ps.GetPartner().GetSystemId() == nil {
			t.Fatalf("port %s partner or partner system_id is nil", ps.GetInterfaceName())
		}
		if !bytes.Equal(ps.GetPartner().GetSystemId().GetOctets(), macB[:]) {
			t.Errorf("port %s partner system_id = %x, want %x", ps.GetInterfaceName(), ps.GetPartner().GetSystemId().GetOctets(), macB[:])
		}
	}
}

func TestLacp_RoundTrip(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0b}

	lagCfgA := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:    lag.ActiveBackup,
				Primary: "1/1/1",
				LACP: lag.LACPConfig{
					Mode:           lag.Active,
					Fast:           true,
					SystemPriority: 32768,
					SystemID:       macA,
					Key:            1,
				},
				Members: map[string]lag.Member{
					"1/1/1": {Priority: 32768, Key: 1},
					"1/1/2": {Priority: 32768, Key: 1},
				},
			},
		},
	}
	lagCfgB := &lag.Config{
		LAGs: map[string]lag.LAG{
			"lag1": {
				Mode:    lag.ActiveBackup,
				Primary: "1/1/1",
				LACP: lag.LACPConfig{
					Mode:           lag.Active,
					Fast:           true,
					SystemPriority: 32768,
					SystemID:       macB,
					Key:            1,
				},
				Members: map[string]lag.Member{
					"1/1/1": {Priority: 32768, Key: 1},
					"1/1/2": {Priority: 32768, Key: 1},
				},
			},
		},
	}

	fab := newLacpFabric(t, t0, macA, macB, lagCfgA, lagCfgB)

	target := t0.Add(3 * time.Second)
	for {
		snap := fab.Snapshot()
		if !snap.Clock.Before(target) && len(snap.Queue) == 0 {
			break
		}
		if snap.Clock.After(target) {
			break
		}
		if _, ok := fab.Step(); !ok {
			break
		}
	}

	swA := fab.Switch("A")
	aggs, portStates := netmodel.Lacp(swA)

	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	lagName := "lag1"
	p1Name := "1/1/1"
	p2Name := "1/1/2"
	p3Name := "1/1/3"
	p4Name := "1/1/4"

	bondMode := switchingv1.BondMode_BOND_MODE_ACTIVE_BACKUP
	primary := "1/1/1"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &lagName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Lag: interfacev1.LagInterface_builder{
				Aggregation: switchingv1.AggregationFacet_builder{
					BondMode:             &bondMode,
					PrimaryInterfaceName: &primary,
				}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParent: &lagName,
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p2Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParent: &lagName,
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p3Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p4Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
	}

	res, err := netmodel.Load(t0, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, aggs, portStates, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	loadedCfg := res.Spec.Config

	if loadedCfg.LAG == nil {
		t.Fatal("expected loadedCfg.LAG to be non-nil")
	}

	diff := lag.Diff(*swA.Config().LAG, *loadedCfg.LAG)
	if len(diff) != 0 {
		t.Errorf("round-trip LAG diff not empty: %+v", diff)
	}
}
