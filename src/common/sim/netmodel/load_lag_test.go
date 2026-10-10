package netmodel_test

import (
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

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
			LagParentInterfaceName: &lagParent,
		}.Build(),
	}.Build()

	p6Name := "1/1/6"
	p6 := interfacev1.Interface_builder{
		Name:        &p6Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			LagParentInterfaceName: &lagParent,
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

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: ifaces})
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
		t.Errorf("got skips %+v, want 1/1/6 switchport skipped", report.Skipped)
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
	resIngress := sw.Receive(testTime, "1/1/6", ingressFrame)
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
	resFlood := sw.Receive(testTime, "1/1/1", floodFrame)
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
			LagParentInterfaceName: &lagName,
		}.Build(),
	}.Build()

	p2 := interfacev1.Interface_builder{
		Name:        &p2Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical: interfacev1.PhysicalInterface_builder{
			LagParentInterfaceName: &lagName,
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
	res, err := netmodel.Load(t0, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: []*interfacev1.Interface{lagIface, p1, p2}, LACPAggregators: []*lacpv1.AggregatorState{aggState}})
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
			LagParentInterfaceName: &lagName,
		}.Build(),
	}.Build()

	for _, m := range []protoreflect.ProtoMessage{lagIface, p1} {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("validation failed for %v: %v", m, err)
		}
	}

	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(t0, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: []*interfacev1.Interface{lagIface, p1}})
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
		t.Errorf("got report defaults %+v, want bond_mode active-backup", report.Defaults)
	}
}

func TestLoadLacpConfiguration(t *testing.T) {
	macA := netaddr.MAC{0x02, 0, 0, 0, 0, 0x0a}
	aggs := []*lacpv1.AggregatorState{lacpv1.AggregatorState_builder{
		InterfaceName: ptr("lag1"), Mode: lacpv1.LacpMode_LACP_MODE_ACTIVE.Enum(), Fast: ptr(true),
		SystemPriority: ptr(uint32(32768)), SystemId: addrv1.Eui48Address_builder{Octets: []byte{2, 0, 0, 0, 0, 10}}.Build(), Key: ptr(uint32(1)),
	}.Build()}
	var portStates []*lacpv1.PortState
	for _, name := range []string{"1/1/1", "1/1/2"} {
		portStates = append(portStates, lacpv1.PortState_builder{InterfaceName: ptr(name), PortPriority: ptr(uint32(32768)), Key: ptr(uint32(1))}.Build())
	}
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
				LagParentInterfaceName: &lagName,
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p2Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParentInterfaceName: &lagName,
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

	in := netmodel.Input{Interfaces: ifaces, LACPAggregators: aggs, LACPPorts: portStates}
	validateInput(t, in)
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, in)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	loadedCfg := res.Spec.Config

	if loadedCfg.LAG == nil {
		t.Fatal("expected loadedCfg.LAG to be non-nil")
	}

	want := lag.Config{LAGs: map[string]lag.LAG{"lag1": {
		Mode: lag.ActiveBackup, Primary: "1/1/1",
		LACP:    lag.LACPConfig{Mode: lag.Active, Fast: true, SystemPriority: 32768, SystemID: macA, Key: 1},
		Members: map[string]lag.Member{"1/1/1": {Priority: 32768, Key: 1}, "1/1/2": {Priority: 32768, Key: 1}},
	}}}
	diff := lag.Diff(want, *loadedCfg.LAG)
	if len(diff) != 0 {
		t.Errorf("loaded LAG differs from literal expectation: %+v", diff)
	}
}
