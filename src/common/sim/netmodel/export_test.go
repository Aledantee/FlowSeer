package netmodel_test

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	instancev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/instance/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestPoeExportRefusesANonNumericGroup(t *testing.T) {
	cfg := phy.Config{PoE: &phy.PoE{
		Groups: map[string]phy.Group{"g1": {PowerNanowatts: 60_000_000_000}},
		Ports:  map[string]phy.PsePort{"1/1/1": {Group: "g1", MaxClass: 8, Enabled: true, PDClass: phy.Class(4)}},
	}}
	if _, _, err := netmodel.Poe(cfg, cfg.Allocate()); err == nil {
		t.Error("Poe(group g1) error = nil, want an error")
	}
}

func TestPoeExportStatusFollowsTheDenial(t *testing.T) {
	cfg := phy.Config{PoE: &phy.PoE{
		Groups: map[string]phy.Group{"1": {PowerNanowatts: 30_000_000_000}},
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

	sw.Advance(t0.Add(4 * time.Second))

	inferiorBridgeID := bpdu.BridgeID{
		Priority: 61440,
		Address:  netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c},
	}
	inferiorBPDU := bpdu.BPDU{
		Type:         bpdu.TypeConfiguration,
		RootID:       inferiorBridgeID,
		BridgeID:     inferiorBridgeID,
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	frame := mustEncode(t, inferiorBPDU, inferiorBridgeID.Address)
	sw.Receive(t0.Add(4*time.Second), "1/1/1", frame)

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

	swA := fab.Device("A").(*vswitch.Switch)

	aggs, portStates := netmodel.Lacp(swA)

	if len(aggs) != 1 {
		t.Fatalf("got %d AggregatorState rows, want 1", len(aggs))
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
		t.Fatalf("got %d PortState rows, want 2", len(portStates))
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
		wantActorState := []lacpv1.LacpStateBit{
			lacpv1.LacpStateBit_LACP_STATE_BIT_ACTIVITY,
			lacpv1.LacpStateBit_LACP_STATE_BIT_TIMEOUT,
			lacpv1.LacpStateBit_LACP_STATE_BIT_AGGREGATION,
			lacpv1.LacpStateBit_LACP_STATE_BIT_SYNCHRONIZATION,
			lacpv1.LacpStateBit_LACP_STATE_BIT_COLLECTING,
			lacpv1.LacpStateBit_LACP_STATE_BIT_DISTRIBUTING,
		}
		if got := ps.GetActor().GetState(); !slices.Equal(got, wantActorState) {
			t.Errorf("port %s actor state = %v, want %v", ps.GetInterfaceName(), got, wantActorState)
		}
		if ps.GetPartner() == nil || ps.GetPartner().GetSystemId() == nil {
			t.Fatalf("port %s partner or partner system_id is nil", ps.GetInterfaceName())
		}
		if !bytes.Equal(ps.GetPartner().GetSystemId().GetOctets(), macB[:]) {
			t.Errorf("port %s partner system_id = %x, want %x", ps.GetInterfaceName(), ps.GetPartner().GetSystemId().GetOctets(), macB[:])
		}
	}

	clock := fab.Snapshot().Clock
	swA.Advance(clock.Add(3 * time.Second))
	_, expiredPorts := netmodel.Lacp(swA)
	for _, ps := range expiredPorts {
		if err := protovalidate.Validate(ps); err != nil {
			t.Fatalf("expired port %s: %v", ps.GetInterfaceName(), err)
		}
		state := ps.GetActor().GetState()
		if ps.GetEnabled() || !slices.Contains(state, lacpv1.LacpStateBit_LACP_STATE_BIT_EXPIRED) ||
			slices.Contains(state, lacpv1.LacpStateBit_LACP_STATE_BIT_COLLECTING) || slices.Contains(state, lacpv1.LacpStateBit_LACP_STATE_BIT_DISTRIBUTING) {
			t.Errorf("expired port %s enabled = %t, state = %v, want Expired without Collecting or Distributing", ps.GetInterfaceName(), ps.GetEnabled(), state)
		}
	}
	swA.Advance(clock.Add(6 * time.Second))
	_, defaultedPorts := netmodel.Lacp(swA)
	for _, ps := range defaultedPorts {
		if err := protovalidate.Validate(ps); err != nil {
			t.Fatalf("defaulted port %s: %v", ps.GetInterfaceName(), err)
		}
		state := ps.GetActor().GetState()
		wantPartnerState := []lacpv1.LacpStateBit{lacpv1.LacpStateBit_LACP_STATE_BIT_SYNCHRONIZATION, lacpv1.LacpStateBit_LACP_STATE_BIT_COLLECTING}
		if !slices.Contains(state, lacpv1.LacpStateBit_LACP_STATE_BIT_DEFAULTED) || slices.Contains(state, lacpv1.LacpStateBit_LACP_STATE_BIT_EXPIRED) ||
			!slices.Equal(ps.GetPartner().GetState(), wantPartnerState) {
			t.Errorf("defaulted port %s actor state = %v, partner state = %v, want Defaulted with administrative Partner", ps.GetInterfaceName(), state, ps.GetPartner().GetState())
		}
	}
}

func TestLacpExport_SelectedMembersFollowAttachment(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	macA := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0b}
	config := func(mac netaddr.MAC, mismatch bool) *lag.Config {
		members := map[string]lag.Member{}
		if mismatch {
			members["1/1/2"] = lag.Member{Key: 2}
		}
		return &lag.Config{LAGs: map[string]lag.LAG{"lag1": {
			MinLinks: 2,
			LACP: lag.LACPConfig{
				Mode:           lag.Active,
				Fast:           true,
				SystemID:       mac,
				SystemPriority: 32768,
				Key:            1,
			},
			Members: members,
		}}}
	}
	fab := newLacpFabric(t, t0, macA, macB, config(macA, false), config(macB, true))
	target := t0.Add(5 * time.Second)
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

	aggs, states := netmodel.Lacp(fab.Device("A").(*vswitch.Switch))
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregators, want 1", len(aggs))
	}
	if !slices.Equal(aggs[0].GetSelectedMembers(), []string{"1/1/1"}) {
		t.Fatalf("aggregator selected_members = %v, want [1/1/1]", aggs[0].GetSelectedMembers())
	}
	for _, state := range states {
		switch state.GetInterfaceName() {
		case "1/1/1":
			if !state.GetAttached() || state.GetEnabled() {
				t.Errorf("member 1/1/1 attached=%v enabled=%v, want true/false", state.GetAttached(), state.GetEnabled())
			}
		case "1/1/2":
			if state.GetAttached() {
				t.Errorf("member 1/1/2 attached=true, want false")
			}
		}
	}
}

// TestExportNamesTheDefaultInstance holds the single-bridge export: the switch
// reports one DEFAULT instance named "default", and every VLAN and FDB row it
// exports names that instance.
func TestExportNamesTheDefaultInstance(t *testing.T) {
	accessPort := func(name string, pvid uint32) *interfacev1.Interface {
		return interfacev1.Interface_builder{
			Name:        ptr(name),
			AdminStatus: interfacev1.AdminStatus_ADMIN_STATUS_UP.Enum(),
			OperStatus:  interfacev1.OperStatus_OPER_STATUS_UP.Enum(),
			Physical: interfacev1.PhysicalInterface_builder{
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:             ptr(pvid),
					UntaggedVlanIds:  []uint32{pvid},
					FrameAdmission:   switchingv1.FrameAdmission_FRAME_ADMISSION_ALL.Enum(),
					IngressFiltering: ptr(false),
				}.Build(),
			}.Build(),
		}.Build()
	}
	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: ptr(uint32(10)), Name: ptr("ten"), NetworkInstance: ptr("default")}.Build(),
		switchingv1.Vlan_builder{Id: ptr(uint32(20)), NetworkInstance: ptr("default")}.Build(),
	}
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: []*interfacev1.Interface{accessPort("1/1/1", 10), accessPort("1/1/2", 10), accessPort("1/1/3", 20)}, VLANs: vlans})
	if err != nil {
		t.Fatalf("netmodel.Load: %v", err)
	}
	sw, err := vswitch.New(res.Spec.Config)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}
	sw.Receive(testTime, "1/1/1", ethernet.Frame{
		Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		Src:     netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Payload: []byte("untagged packet"),
	})

	instances := netmodel.NetworkInstances(sw)
	if len(instances) != 1 {
		t.Fatalf("got %d network instances, want 1", len(instances))
	}
	if got := instances[0]; got.GetName() != "default" ||
		got.GetKind() != instancev1.NetworkInstanceKind_NETWORK_INSTANCE_KIND_DEFAULT {
		t.Errorf("got instance %q of kind %v, want \"default\" of kind DEFAULT", got.GetName(), got.GetKind())
	}
	if err := protovalidate.Validate(instances[0]); err != nil {
		t.Errorf("network instance is invalid: %v", err)
	}

	exportedVlans := netmodel.Vlans(sw)
	if len(exportedVlans) != 2 {
		t.Fatalf("got %d VLAN rows, want 2", len(exportedVlans))
	}
	if exportedVlans[0].GetId() != 10 || exportedVlans[0].GetName() != "ten" ||
		exportedVlans[1].GetId() != 20 || exportedVlans[1].HasName() {
		t.Errorf("got VLAN rows %v, want 10 named ten then 20 without a name", exportedVlans)
	}
	for _, v := range exportedVlans {
		if v.GetNetworkInstance() != "default" {
			t.Errorf("VLAN %d names instance %q, want \"default\"", v.GetId(), v.GetNetworkInstance())
		}
		if err := protovalidate.Validate(v); err != nil {
			t.Errorf("VLAN %d is invalid: %v", v.GetId(), err)
		}
	}

	fdb, err := netmodel.FdbEntries(sw.Entries())
	if err != nil {
		t.Fatalf("FdbEntries: %v", err)
	}
	if len(fdb) == 0 {
		t.Fatal("got no FDB rows, want the learned source address")
	}
	for _, e := range fdb {
		if e.GetNetworkInstance() != "default" {
			t.Errorf("FDB row for VLAN %d names instance %q, want \"default\"", e.GetVlanId(), e.GetNetworkInstance())
		}
		if err := protovalidate.Validate(e); err != nil {
			t.Errorf("FDB row is invalid: %v", err)
		}
	}
}

func TestExportWithoutSwitch(t *testing.T) {
	if got := netmodel.NetworkInstances(nil); got != nil {
		t.Errorf("NetworkInstances(nil) = %v, want nil", got)
	}
	if got := netmodel.Vlans(nil); got != nil {
		t.Errorf("Vlans(nil) = %v, want nil", got)
	}
}
