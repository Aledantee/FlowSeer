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
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestLoadRequiresExplicitRSTPBridgeProtocol(t *testing.T) {
	priority := uint32(32768)
	bridgeID := stpv1.BridgeId_builder{
		Priority: &priority,
		Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
	}.Build()

	tests := []struct {
		name    string
		version *stpv1.ProtocolVersion
		status  analysis.Status
		issue   analysis.IssueCode
	}{
		{name: "missing", status: analysis.Incomplete, issue: netmodel.IssueMissingSTPProtocolVersion},
		{name: "unspecified", version: ptr(stpv1.ProtocolVersion_PROTOCOL_VERSION_UNSPECIFIED), status: analysis.Incomplete, issue: netmodel.IssueMissingSTPProtocolVersion},
		{name: "legacy STP", version: ptr(stpv1.ProtocolVersion_PROTOCOL_VERSION_STP), status: analysis.Unsupported, issue: netmodel.IssueUnsupportedSTPProtocolVersion},
		{name: "unknown", version: ptr(stpv1.ProtocolVersion(99)), status: analysis.Unsupported, issue: netmodel.IssueUnsupportedSTPProtocolVersion},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := stpv1.BridgeState_builder{
				NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
				ProtocolVersion: test.version,
				BridgeId:        bridgeID,
			}.Build()
			result := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "stp-version"}, (netmodel.Input{
				Interfaces:  []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
				BridgeState: state,
				Want:        []trace.Layer{stp.LayerName},
			}))

			if result.Spec.Config.STP != nil || slices.Contains(result.Report.Capabilities, stp.LayerName) {
				t.Errorf("unsupported STP protocol constructed a layer: config=%+v capabilities=%v", result.Spec.Config.STP, result.Report.Capabilities)
			}
			if result.Readiness() != test.status {
				t.Errorf("readiness = %s, want %s", result.Readiness(), test.status)
			}
			if !slices.ContainsFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
				return issue.Code == test.issue && issue.Status == test.status && len(issue.Evidence) > 0
			}) {
				t.Errorf("issues = %+v, want evidenced %s at %s", result.Metadata.Issues(), test.issue, test.status)
			}
		})
	}
}

func TestLoadRejectsPresentZeroSTPBridgeTimers(t *testing.T) {
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	priority := uint32(32768)
	bridgeID := stpv1.BridgeId_builder{
		Priority: &priority,
		Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
	}.Build()
	zero := durationpb.New(0)
	tests := []struct {
		name  string
		field string
		set   func(*stpv1.BridgeState_builder)
	}{
		{
			name:  "hello time",
			field: "bridge_hello_time",
			set: func(builder *stpv1.BridgeState_builder) {
				builder.BridgeHelloTime = zero
			},
		},
		{
			name:  "max age",
			field: "bridge_max_age",
			set: func(builder *stpv1.BridgeState_builder) {
				builder.BridgeMaxAge = zero
			},
		},
		{
			name:  "forward delay",
			field: "bridge_forward_delay",
			set: func(builder *stpv1.BridgeState_builder) {
				builder.BridgeForwardDelay = zero
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			builder := stpv1.BridgeState_builder{
				NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
				ProtocolVersion: &protocol,
				BridgeId:        bridgeID,
			}
			test.set(&builder)
			input := netmodel.Input{
				Interfaces:  []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
				BridgeState: builder.Build(),
				Want:        []trace.Layer{stp.LayerName},
			}
			validateInput(t, input)

			result := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "zero-stp-timer"}, input)
			if result.Spec.Config.STP != nil || slices.Contains(result.Report.Capabilities, stp.LayerName) {
				t.Errorf("present zero %s constructed an STP layer: config=%+v capabilities=%v", test.field, result.Spec.Config.STP, result.Report.Capabilities)
			}
			if result.Readiness() != analysis.Unsupported {
				t.Errorf("readiness = %s, want Unsupported", result.Readiness())
			}
			if slices.ContainsFunc(result.Report.Defaults, func(got netmodel.Default) bool {
				return got.Field == test.field
			}) {
				t.Errorf("present zero %s reported as an absent-field default: %+v", test.field, result.Report.Defaults)
			}

			issueIndex := slices.IndexFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
				return issue.Code == netmodel.IssueInvalidSTPBridgeTimer &&
					issue.Scope.Compare(analysis.ProtocolScope("sw1", string(stp.LayerName), "0")) == 0
			})
			if issueIndex < 0 {
				t.Fatalf("issues = %+v, want %s scoped to sw1", result.Metadata.Issues(), netmodel.IssueInvalidSTPBridgeTimer)
			}
			issue := result.Metadata.Issues()[issueIndex]
			if len(issue.Evidence) == 0 {
				t.Fatalf("issue %s has no evidence", issue.Code)
			}
			if _, ok := result.Metadata.Evidence().Lookup(issue.Evidence[0]); !ok {
				t.Errorf("issue %s evidence is absent from catalog", issue.Code)
			}
		})
	}
}

func TestLoadInvalidSTPBridgeSkipsEveryPortRow(t *testing.T) {
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	priority := uint32(32767)
	bridgeState := stpv1.BridgeState_builder{
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Priority: &priority,
			Address: addrv1.Eui48Address_builder{
				Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			}.Build(),
		}.Build(),
	}.Build()
	portA := "1/1/1"
	portB := "1/1/2"
	result := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"}, (netmodel.Input{
		Interfaces:  []*interfacev1.Interface{plainPhysicalInterface(portA), plainPhysicalInterface(portB)},
		BridgeState: bridgeState,
		STPPorts: []*stpv1.PortState{
			stpv1.PortState_builder{InterfaceName: &portA}.Build(),
			nil,
			stpv1.PortState_builder{InterfaceName: &portB}.Build(),
		},
		Want: []trace.Layer{stp.LayerName},
	}))

	if result.Spec.Config.STP != nil {
		t.Errorf("STP = %+v, want invalid bridge omitted", result.Spec.Config.STP)
	}
	for _, name := range []string{portA, portB} {
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.Port == name && skip.What == "stp_port" && len(skip.Evidence) > 0
		}) {
			t.Errorf("skipped = %+v, want evidenced stp_port omission on %q", result.Report.Skipped, name)
		}
	}
}

func TestLoadRequestedSTPWithoutBridgeStateReportsConstructedCapabilities(t *testing.T) {
	result := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "missing-bridge"}, (netmodel.Input{
		Interfaces: []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
		STPPorts: []*stpv1.PortState{
			stpv1.PortState_builder{InterfaceName: ptr("1/1/1")}.Build(),
		},
		Want: []trace.Layer{stp.LayerName},
	}))

	if slices.Contains(result.Report.Capabilities, stp.LayerName) {
		t.Errorf("reported capabilities = %v, includes unconstructed STP", result.Report.Capabilities)
	}
	if result.Spec.Config.STP != nil {
		t.Errorf("STP config = %+v, want nil", result.Spec.Config.STP)
	}
	if result.Readiness() == analysis.Complete {
		t.Fatal("readiness = Complete, want scoped omission")
	}
	for _, want := range []struct {
		port string
		what string
	}{
		{what: "stp_bridge"},
		{port: "1/1/1", what: "stp_port"},
	} {
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.Port == want.port && skip.What == want.what && len(skip.Evidence) > 0
		}) {
			t.Errorf("skipped = %+v, want evidenced %s omission on %q", result.Report.Skipped, want.what, want.port)
		}
	}
}

func TestLoadOrphanSTPRowsAreScopedOmissions(t *testing.T) {
	name := "1/1/1"
	result := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "orphan-stp-row"}, (netmodel.Input{
		Interfaces: []*interfacev1.Interface{plainPhysicalInterface(name)},
		STPPorts: []*stpv1.PortState{
			stpv1.PortState_builder{InterfaceName: &name}.Build(),
		},
	}))

	if slices.Contains(result.Report.Capabilities, stp.LayerName) || result.Spec.Config.STP != nil {
		t.Errorf("orphan row constructed or reported STP: capabilities=%v config=%+v", result.Report.Capabilities, result.Spec.Config.STP)
	}
	if result.Readiness() == analysis.Complete {
		t.Fatal("readiness = Complete, want scoped orphan-row omission")
	}
	if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
		return skip.Port == name && skip.What == "stp_port" && len(skip.Evidence) > 0
	}) {
		t.Errorf("skipped = %+v, want evidenced stp_port omission on %q", result.Report.Skipped, name)
	}
}

// TestLoad_STPWalkSkipsRoutedPort pins that a routed port, whether routed
// directly or through an accepted sub-interface, is skipped from the
// spanning tree port table. Switch validation refuses a routed port
// configured as an STP port; without the skip the load would fail outright
// for a device that reports spanning tree state on every port including a
// routed one.
func TestLoad_STPWalkSkipsRoutedPort(t *testing.T) {
	for _, test := range []struct {
		name       string
		ifaces     []*interfacev1.Interface
		routedPort string
	}{
		{
			name:       "DirectlyRouted",
			ifaces:     []*interfacev1.Interface{routedPhysicalInterface("eth1")},
			routedPort: "eth1",
		},
		{
			name: "RoutedSubInterfaceParent",
			ifaces: []*interfacev1.Interface{
				plainPhysicalInterface("eth1"),
				subInterface("eth1.10", "eth1", vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10)),
			},
			routedPort: "eth1",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			portName := test.routedPort
			ps := stpv1.PortState_builder{InterfaceName: &portName}.Build()

			input := netmodel.Input{
				Interfaces:  test.ifaces,
				BridgeState: validBridgeState(),
				STPPorts:    []*stpv1.PortState{ps},
			}
			validateInput(t, input)
			loaded := load(t, netmodel.SourceContext{DeviceID: "sw1"}, input)

			if cfg := loaded.Spec.Config; cfg.STP != nil {
				if _, ok := cfg.STP.Ports[test.routedPort]; ok {
					t.Errorf("STP port table must not hold routed port %s", test.routedPort)
				}
			}

			wantScope := analysis.FieldScope(analysis.ProtocolScope("sw1", string(stp.LayerName), "0"), "ports", test.routedPort)
			if !slices.ContainsFunc(loaded.Report.Skipped, func(skip netmodel.Skipped) bool {
				return skip.Port == test.routedPort && skip.What == "stp_port" && skip.Scope.Compare(wantScope) == 0
			}) {
				t.Errorf("skipped = %+v, want stp_port skip at %s", loaded.Report.Skipped, wantScope)
			}

			if err := loaded.Spec.Config.Validate(); err != nil {
				t.Errorf("cfg.Validate failed: %v", err)
			}
		})
	}
}

func TestStpExportAndLoad_RingConvergence(t *testing.T) {
	fab, t0, macs := newRingTopology(t)

	fab.Run(fabric.RunOptions{Budget: 200, Window: 0})

	sw1MAC := macs["sw1"]
	swNames := []string{"sw1", "sw2", "sw3"}

	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP

	for _, name := range swNames {
		sw := fab.Device(name).(*vswitch.Switch)
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

		res, err := netmodel.Load(t0, netmodel.SourceContext{DeviceID: name}, netmodel.Input{Interfaces: ifaces, BridgeState: bridgeState, STPPorts: portStates})
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
				LagParentInterfaceName: &lagParent,
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
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
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
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: ifaces, BridgeState: bridgeState, STPPorts: []*stpv1.PortState{psMember, psRegular}})
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
	bridgeState := stpv1.BridgeState_builder{NetworkInstance: ptr(netmodel.DefaultNetworkInstance), ProtocolVersion: &protocol, BridgeId: bridgeID}.Build()
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
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: ifaces, BridgeState: bridgeState, STPPorts: []*stpv1.PortState{psAbsent}})
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
	bridgeState := stpv1.BridgeState_builder{NetworkInstance: ptr(netmodel.DefaultNetworkInstance), ProtocolVersion: &protocol, BridgeId: bridgeID}.Build()
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
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: ifaces, BridgeState: bridgeState, STPPorts: []*stpv1.PortState{psWithoutAdminCost}})
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
		t.Errorf("got STP port 1/1/1 PathCost %v (exists=%t), want 0", portCfg.PathCost, ok)
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
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
		ProtocolVersion: &protocol,
		BridgeId:        stpv1.BridgeId_builder{Priority: &prio}.Build(),
	}.Build()

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: ifaces, BridgeState: bridgeState})
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
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
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
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: ifaces, BridgeState: bridgeState, STPPorts: []*stpv1.PortState{ps}})
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
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
		ProtocolVersion: &protocol,
		BridgeId:        bridgeID,
	}.Build()
	if err := protovalidate.Validate(bridgeState); err != nil {
		t.Fatalf("bridge state validation failed: %v", err)
	}

	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: ifaces, BridgeState: bridgeState})
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
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
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
		netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "zero-priorities"}, netmodel.Input{Interfaces: []*interfacev1.Interface{plainPhysicalInterface(name)}, BridgeState: bridgeState, STPPorts: []*stpv1.PortState{portState}, Want: []trace.Layer{stp.LayerName}},
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
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Address: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		}.Build(),
	}.Build()
	portState := stpv1.PortState_builder{InterfaceName: &name}.Build()

	result, err := netmodel.Load(
		time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "absent-priorities"}, netmodel.Input{Interfaces: []*interfacev1.Interface{plainPhysicalInterface(name)}, BridgeState: bridgeState, STPPorts: []*stpv1.PortState{portState}, Want: []trace.Layer{stp.LayerName}},
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
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
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
		netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "invalid-admin-path-cost"}, netmodel.Input{Interfaces: []*interfacev1.Interface{plainPhysicalInterface(name), plainPhysicalInterface(validName)}, BridgeState: bridgeState, STPPorts: []*stpv1.PortState{portState, validPortState}, Want: []trace.Layer{stp.LayerName}},
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

	scope := analysis.FieldScope(analysis.ProtocolScope("sw1", string(stp.LayerName), "0"), "ports", name)
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
		NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Priority: &priority,
			Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		}.Build(),
		TxHoldCount: &invalidTxHoldCount,
	}.Build()

	result, err := netmodel.Load(
		time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "invalid-tx-hold-count"}, netmodel.Input{Interfaces: []*interfacev1.Interface{plainPhysicalInterface(name)}, BridgeState: bridgeState, Want: []trace.Layer{stp.LayerName}},
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
		return got.Scope == analysis.ProtocolScope("sw1", string(stp.LayerName), "0") &&
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
				NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
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
			input := netmodel.Input{
				Interfaces:  []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
				BridgeState: builder.Build(),
			}
			validateInput(t, input)
			result := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: test.name}, input)

			if result.Spec.Config.STP != nil {
				t.Errorf("STP config = %+v, want invalid layer omitted", result.Spec.Config.STP)
			}
			if slices.Contains(result.Report.Capabilities, stp.LayerName) {
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
