package netmodel_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

var trustTestTime = time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)

type loadInput struct {
	ifaces          []*interfacev1.Interface
	vlans           []*switchingv1.Vlan
	fdb             []*switchingv1.FdbEntry
	budgets         []*phyv1.PseBudget
	bridgeState     *stpv1.BridgeState
	stpPorts        []*stpv1.PortState
	lacpAggregators []*lacpv1.AggregatorState
	lacpPorts       []*lacpv1.PortState
	addrs           []*ipv1.InterfaceAddress
	neighbors       []*ipv1.NeighborEntry
	want            []port.Layer
}

func (in loadInput) load(t *testing.T, src netmodel.SourceContext) netmodel.Result {
	t.Helper()

	result, err := netmodel.Load(
		trustTestTime,
		src,
		in.ifaces,
		in.vlans,
		in.fdb,
		in.budgets,
		in.bridgeState,
		in.stpPorts,
		in.lacpAggregators,
		in.lacpPorts,
		in.addrs,
		in.neighbors,
		in.want,
	)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return result
}

func (in loadInput) validate(t *testing.T) {
	t.Helper()

	messages := make([]proto.Message, 0, len(in.ifaces)+len(in.vlans)+len(in.fdb)+len(in.budgets)+len(in.stpPorts)+len(in.lacpAggregators)+len(in.lacpPorts)+len(in.addrs)+len(in.neighbors)+1)
	for _, message := range in.ifaces {
		messages = append(messages, message)
	}
	for _, message := range in.vlans {
		messages = append(messages, message)
	}
	for _, message := range in.fdb {
		messages = append(messages, message)
	}
	for _, message := range in.budgets {
		messages = append(messages, message)
	}
	if in.bridgeState != nil {
		messages = append(messages, in.bridgeState)
	}
	for _, message := range in.stpPorts {
		messages = append(messages, message)
	}
	for _, message := range in.lacpAggregators {
		messages = append(messages, message)
	}
	for _, message := range in.lacpPorts {
		messages = append(messages, message)
	}
	for _, message := range in.addrs {
		messages = append(messages, message)
	}
	for _, message := range in.neighbors {
		messages = append(messages, message)
	}
	for _, message := range messages {
		if err := protovalidate.Validate(message); err != nil {
			t.Fatalf("fixture validation: %v", err)
		}
	}
}

func assertEqualLoadResults(t *testing.T, got, want netmodel.Result) {
	t.Helper()

	if !got.Spec.Equal(want.Spec) {
		t.Errorf("construction specs differ:\n got: %+v\nwant: %+v", got.Spec, want.Spec)
	}
	if !reflect.DeepEqual(got.Report, want.Report) {
		t.Errorf("reports differ:\n got: %+v\nwant: %+v", got.Report, want.Report)
	}
	if got.Readiness() != want.Readiness() {
		t.Errorf("readiness = %v, want %v", got.Readiness(), want.Readiness())
	}
	if got.Metadata.Scope() != want.Metadata.Scope() {
		t.Errorf("metadata scope = %+v, want %+v", got.Metadata.Scope(), want.Metadata.Scope())
	}
	if issues := got.Metadata.Issues(); !reflect.DeepEqual(issues, want.Metadata.Issues()) {
		t.Errorf("issues differ:\n got: %+v\nwant: %+v", issues, want.Metadata.Issues())
	}
	if evidence := got.Metadata.Evidence().Entries(); !reflect.DeepEqual(evidence, want.Metadata.Evidence().Entries()) {
		t.Errorf("evidence differs:\n got: %+v\nwant: %+v", evidence, want.Metadata.Evidence().Entries())
	}
	if assumptions := got.Metadata.Assumptions(); !reflect.DeepEqual(assumptions, want.Metadata.Assumptions()) {
		t.Errorf("assumptions differ:\n got: %+v\nwant: %+v", assumptions, want.Metadata.Assumptions())
	}
}

func assertEvidenceSource(t *testing.T, result netmodel.Result, src netmodel.SourceContext) {
	t.Helper()

	for _, entry := range result.Metadata.Evidence().Entries() {
		if entry.Evidence.Origin != src.Origin || !strings.HasPrefix(entry.Evidence.Context, src.Context) {
			t.Errorf("evidence source = %+v, want origin %q and context prefix %q", entry.Evidence, src.Origin, src.Context)
		}
	}
}

func TestLoadAnonymousSourceUsesAnonymousNodeScope(t *testing.T) {
	name := "p1"
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	input := loadInput{ifaces: []*interfacev1.Interface{interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()}}
	input.validate(t)

	loaded := input.load(t, netmodel.SourceContext{})
	wantScope := analysis.NodeScope("")
	if got := loaded.Metadata.Scope(); got.Compare(wantScope) != 0 {
		t.Fatalf("metadata scope = %s, want %s", got, wantScope)
	}
	if len(loaded.Report.Defaults) == 0 {
		t.Fatal("anonymous load produced no defaults")
	}
	for _, item := range loaded.Report.Defaults {
		if !wantScope.Contains(item.Scope) {
			t.Errorf("default scope = %s, want scope beneath %s", item.Scope, wantScope)
		}
	}
	issues := loaded.Metadata.Issues()
	if len(issues) == 0 {
		t.Fatal("anonymous load produced no issues")
	}
	for _, issue := range issues {
		if !wantScope.Contains(issue.Scope) {
			t.Errorf("issue scope = %s, want scope beneath %s", issue.Scope, wantScope)
		}
	}

	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec(Load().Spec): %v", err)
	}
	if got := sw.Spec(); !got.Equal(loaded.Spec) {
		t.Errorf("round-trip spec differs:\n got: %+v\nwant: %+v", got, loaded.Spec)
	}
}

func plainPhysicalInterface(name string) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()
}

func switchedPhysicalInterface(name string, taggedVLANs ...uint32) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	admission := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltering := false
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Physical: interfacev1.PhysicalInterface_builder{
			Switchport: switchingv1.SwitchportFacet_builder{
				TaggedVlanIds:    taggedVLANs,
				FrameAdmission:   &admission,
				IngressFiltering: &ingressFiltering,
			}.Build(),
		}.Build(),
	}.Build()
}

func routedPhysicalInterface(name string) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		Ip: ipv1.IpFacet_builder{
			Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
	}.Build()
}

func activeFDBRow(portName string, vid uint32) *switchingv1.FdbEntry {
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	static := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	return switchingv1.FdbEntry_builder{
		VlanId:        &vid,
		InterfaceName: &portName,
		Mac:           addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(),
		Kind:          &static,
		Status:        &active,
	}.Build()
}

func routedVLANInterface(name string, vid uint32, mac []byte) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Mac: addrv1.EuiAddress_builder{
			Eui48: addrv1.Eui48Address_builder{Octets: mac}.Build(),
		}.Build(),
		Vlan: interfacev1.VlanInterface_builder{VlanId: &vid}.Build(),
		Ip: ipv1.IpFacet_builder{
			Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
	}.Build()
}

func lagInterfaces() []*interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	lagName := "lag1"
	memberName := "1/1/1"
	return []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &lagName,
			AdminStatus: &admin,
			OperStatus:  &oper,
			Lag: interfacev1.LagInterface_builder{
				Aggregation: switchingv1.AggregationFacet_builder{}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &memberName,
			AdminStatus: &admin,
			OperStatus:  &oper,
			Physical: interfacev1.PhysicalInterface_builder{
				LagParent: &lagName,
			}.Build(),
		}.Build(),
	}
}

func validBridgeState() *stpv1.BridgeState {
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	priority := uint32(32768)
	return stpv1.BridgeState_builder{
		ProtocolVersion: &protocol,
		BridgeId: stpv1.BridgeId_builder{
			Priority: &priority,
			Address: addrv1.Eui48Address_builder{
				Octets: []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			}.Build(),
		}.Build(),
	}.Build()
}

func TestLoadRejectsUnusableRequestedCapabilities(t *testing.T) {
	result := (loadInput{
		ifaces: []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
		want: []port.Layer{
			port.LayerRelay,
			port.LayerRelay,
			port.LayerMcast,
			port.Layer("future-layer"),
		},
	}).load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "requested-capabilities"})

	if !slices.Equal(result.Report.Capabilities, []port.Layer{port.LayerRelay}) {
		t.Errorf("capabilities = %v, want one supported relay capability", result.Report.Capabilities)
	}
	if result.Readiness() != analysis.Unsupported {
		t.Errorf("readiness = %s, want Unsupported", result.Readiness())
	}
	for _, code := range []analysis.IssueCode{
		netmodel.IssueDuplicateRequestedCapability,
		netmodel.IssueUnsupportedRequestedCapability,
	} {
		if !slices.ContainsFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
			return issue.Code == code && len(issue.Evidence) > 0
		}) {
			t.Errorf("issues = %+v, want evidenced %s", result.Metadata.Issues(), code)
		}
	}
}

func TestLoadDoesNotInferCapabilitiesAfterRejectingEveryRequest(t *testing.T) {
	result := (loadInput{
		ifaces: []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
		want:   []port.Layer{port.LayerMcast, port.Layer("future-layer")},
	}).load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "unsupported-capabilities"})

	if len(result.Report.Capabilities) != 0 {
		t.Errorf("capabilities = %v, want no inferred capabilities for an explicit request", result.Report.Capabilities)
	}
	if result.Readiness() != analysis.Unsupported {
		t.Errorf("readiness = %s, want Unsupported", result.Readiness())
	}
}

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
				ProtocolVersion: test.version,
				BridgeId:        bridgeID,
			}.Build()
			result := (loadInput{
				ifaces:      []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
				bridgeState: state,
				want:        []port.Layer{port.LayerStp},
			}).load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "stp-version"})

			if result.Spec.Config.STP != nil || slices.Contains(result.Report.Capabilities, port.LayerStp) {
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
				ProtocolVersion: &protocol,
				BridgeId:        bridgeID,
			}
			test.set(&builder)
			input := loadInput{
				ifaces:      []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
				bridgeState: builder.Build(),
				want:        []port.Layer{port.LayerStp},
			}
			input.validate(t)

			result := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "zero-stp-timer"})
			if result.Spec.Config.STP != nil || slices.Contains(result.Report.Capabilities, port.LayerStp) {
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
					issue.Scope.Compare(analysis.ProtocolScope("sw1", string(port.LayerStp), "0")) == 0
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

func TestLoadFDBRequiresAuthoritativeKindAndStatus(t *testing.T) {
	vid := uint32(10)
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	dynamic := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC
	unspecifiedKind := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_UNSPECIFIED
	unspecifiedStatus := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_UNSPECIFIED
	unknownKind := switchingv1.FdbEntryKind(99)
	unknownStatus := switchingv1.FdbEntryStatus(99)
	macs := [][]byte{
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x04},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x05},
		{0x00, 0x11, 0x22, 0x33, 0x44, 0x06},
	}
	fdb := []*switchingv1.FdbEntry{
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[0]}.Build(), Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[1]}.Build(), Kind: &unknownKind, Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[2]}.Build(), Kind: &dynamic,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[3]}.Build(), Kind: &dynamic, Status: &unknownStatus,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[4]}.Build(), Kind: &unspecifiedKind, Status: &active,
		}.Build(),
		switchingv1.FdbEntry_builder{
			VlanId: &vid, InterfaceName: &portName,
			Mac: addrv1.Eui48Address_builder{Octets: macs[5]}.Build(), Kind: &dynamic, Status: &unspecifiedStatus,
		}.Build(),
	}

	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "fdb-trust"}
	result := (loadInput{ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)}, fdb: fdb}).load(t, src)

	if len(result.Spec.Seeds) != 0 {
		t.Errorf("seeds = %+v, want none", result.Spec.Seeds)
	}
	if result.Readiness() == analysis.Complete {
		t.Fatal("readiness = Complete, want non-Complete")
	}
	if len(result.Report.Skipped) != len(fdb) {
		t.Errorf("skipped rows = %d, want %d: %+v", len(result.Report.Skipped), len(fdb), result.Report.Skipped)
	}
	assertEvidenceSource(t, result, src)
}

func TestLoadKeepsExplicitActiveFDBKindsAndDeduplicatesEqualRows(t *testing.T) {
	vid := uint32(10)
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE

	for _, tt := range []struct {
		name     string
		kind     switchingv1.FdbEntryKind
		isStatic bool
	}{
		{name: "dynamic", kind: switchingv1.FdbEntryKind_FDB_ENTRY_KIND_DYNAMIC},
		{name: "static", kind: switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC, isStatic: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			row := switchingv1.FdbEntry_builder{
				VlanId: &vid, InterfaceName: &portName,
				Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), Kind: &tt.kind, Status: &active,
			}.Build()
			input := loadInput{
				ifaces: []*interfacev1.Interface{switchedPhysicalInterface(portName, vid)},
				fdb:    []*switchingv1.FdbEntry{row, row},
			}
			input.validate(t)
			result := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

			if len(result.Spec.Seeds) != 1 || result.Spec.Seeds[0].Static != tt.isStatic {
				t.Errorf("seeds = %+v, want one seed with static=%t", result.Spec.Seeds, tt.isStatic)
			}
			if len(result.Report.Conflicts) != 0 || result.Readiness() != analysis.Complete {
				t.Errorf("conflicts = %+v, readiness = %v, want no conflict and Complete", result.Report.Conflicts, result.Readiness())
			}
		})
	}
}

func TestLoadOmitsFDBRowsTheCompletedSwitchCannotConstruct(t *testing.T) {
	portName := "1/1/1"
	vid10 := uint32(10)
	vid20 := uint32(20)
	vlan10 := switchingv1.Vlan_builder{Id: &vid10, Name: ptr("ten")}.Build()

	tests := []struct {
		name      string
		input     loadInput
		wantSeeds int
	}{
		{
			name: "relay absent",
			input: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
				want:   []port.Layer{port.LayerEthernet},
			},
		},
		{
			name: "VLAN awareness absent",
			input: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
				want:   []port.Layer{port.LayerRelay},
			},
		},
		{
			name: "VLAN absent from table",
			input: loadInput{
				ifaces: []*interfacev1.Interface{switchedPhysicalInterface(portName, vid20)},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
			},
		},
		{
			name: "routed port",
			input: loadInput{
				ifaces: []*interfacev1.Interface{routedPhysicalInterface(portName)},
				vlans:  []*switchingv1.Vlan{vlan10},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
			},
		},
		{
			name: "switchport does not admit VLAN",
			input: loadInput{
				ifaces: []*interfacev1.Interface{switchedPhysicalInterface(portName, vid20)},
				vlans:  []*switchingv1.Vlan{vlan10},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
			},
		},
		{
			name: "admitted switchport",
			input: loadInput{
				ifaces: []*interfacev1.Interface{switchedPhysicalInterface(portName, vid10)},
				fdb:    []*switchingv1.FdbEntry{activeFDBRow(portName, vid10)},
			},
			wantSeeds: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.input.validate(t)
			result := test.input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"})

			if got := len(result.Spec.Seeds); got != test.wantSeeds {
				t.Errorf("seed count = %d, want %d: %+v", got, test.wantSeeds, result.Spec.Seeds)
			}
			normalized, err := result.Spec.Normalize()
			if err != nil {
				t.Fatalf("Spec.Normalize: %v", err)
			}
			if !result.Spec.Equal(normalized) {
				t.Errorf("Load returned a non-normalized spec:\n got: %+v\nwant: %+v", result.Spec, normalized)
			}
			if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			if test.wantSeeds > 0 {
				return
			}
			if result.Readiness() == analysis.Complete {
				t.Fatal("readiness = Complete, want scoped FDB omission")
			}
			if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
				return skip.Port == portName && skip.What == "fdb_entry" && len(skip.Evidence) > 0
			}) {
				t.Errorf("skipped = %+v, want evidenced fdb_entry omission on %q", result.Report.Skipped, portName)
			}
			issues := result.Metadata.IssuesFor(analysis.PortScope("sw1", portName))
			if len(issues) == 0 || len(issues[0].Evidence) == 0 {
				t.Errorf("issues = %+v, want evidenced port-scoped issue", issues)
			}
		})
	}
}

func TestLoadConflictsAreOrderIndependent(t *testing.T) {
	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "conflict-permutations"}
	vid := uint32(10)
	portName := "1/1/1"
	otherPortName := "1/1/2"
	routedName := "vlan10"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	static := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC

	tests := []struct {
		name       string
		forward    loadInput
		reverse    loadInput
		conflictOn string
		omitted    func(netmodel.Result) bool
	}{
		{
			name: "forwarding database",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName), plainPhysicalInterface(otherPortName)},
				fdb: []*switchingv1.FdbEntry{
					switchingv1.FdbEntry_builder{VlanId: &vid, Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), InterfaceName: &portName, Kind: &static, Status: &active}.Build(),
					switchingv1.FdbEntry_builder{VlanId: &vid, Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build(), InterfaceName: &otherPortName, Kind: &static, Status: &active}.Build(),
				},
			},
			conflictOn: "fdb_entry",
			omitted: func(result netmodel.Result) bool {
				return len(result.Spec.Seeds) == 0
			},
		},
		{
			name: "vlan",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				vlans: []*switchingv1.Vlan{
					switchingv1.Vlan_builder{Id: &vid, Name: ptr("blue")}.Build(),
					switchingv1.Vlan_builder{Id: &vid, Name: ptr("red")}.Build(),
				},
				want: []port.Layer{port.LayerVlan},
			},
			conflictOn: "vlan",
			omitted: func(result netmodel.Result) bool {
				_, exists := result.Spec.Config.Bridge.VLAN.Table[10]
				return !exists
			},
		},
		{
			name: "power budget",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{plainPhysicalInterface(portName)},
				budgets: []*phyv1.PseBudget{
					phyv1.PseBudget_builder{PseGroup: ptr(uint32(1)), PowerMilliwatts: ptr(uint32(100_000))}.Build(),
					phyv1.PseBudget_builder{PseGroup: ptr(uint32(1)), PowerMilliwatts: ptr(uint32(200_000))}.Build(),
				},
			},
			conflictOn: "pse_budget",
			omitted: func(result netmodel.Result) bool {
				_, exists := result.Spec.Config.Phy.PoE.Groups["1"]
				return !exists
			},
		},
		{
			name: "spanning tree port",
			forward: loadInput{
				ifaces:      []*interfacev1.Interface{plainPhysicalInterface(portName)},
				bridgeState: validBridgeState(),
				stpPorts: []*stpv1.PortState{
					stpv1.PortState_builder{InterfaceName: &portName, Priority: ptr(uint32(64)), AdminPathCost: ptr(uint32(10))}.Build(),
					stpv1.PortState_builder{InterfaceName: &portName, Priority: ptr(uint32(128)), AdminPathCost: ptr(uint32(20))}.Build(),
				},
			},
			conflictOn: "stp_port",
			omitted: func(result netmodel.Result) bool {
				_, exists := result.Spec.Config.STP.Ports[portName]
				return !exists
			},
		},
		{
			name: "link aggregation aggregator",
			forward: loadInput{
				ifaces: lagInterfaces(),
				lacpAggregators: []*lacpv1.AggregatorState{
					lacpv1.AggregatorState_builder{InterfaceName: ptr("lag1"), Mode: ptr(lacpv1.LacpMode_LACP_MODE_ACTIVE), Key: ptr(uint32(10))}.Build(),
					lacpv1.AggregatorState_builder{InterfaceName: ptr("lag1"), Mode: ptr(lacpv1.LacpMode_LACP_MODE_PASSIVE), Key: ptr(uint32(20))}.Build(),
				},
			},
			conflictOn: "lacp_aggregator",
			omitted: func(result netmodel.Result) bool {
				return result.Spec.Config.LAG.LAGs["lag1"].LACP.Mode == lag.Off
			},
		},
		{
			name: "link aggregation member",
			forward: loadInput{
				ifaces: lagInterfaces(),
				lacpPorts: []*lacpv1.PortState{
					lacpv1.PortState_builder{InterfaceName: &portName, PortPriority: ptr(uint32(10)), Key: ptr(uint32(100))}.Build(),
					lacpv1.PortState_builder{InterfaceName: &portName, PortPriority: ptr(uint32(20)), Key: ptr(uint32(200))}.Build(),
				},
			},
			conflictOn: "lacp_port_state",
			omitted: func(result netmodel.Result) bool {
				member := result.Spec.Config.LAG.LAGs["lag1"].Members[portName]
				return (member.Priority != 10 || member.Key != 100) && (member.Priority != 20 || member.Key != 200)
			},
		},
		{
			name: "interface address",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{routedVLANInterface(routedName, vid, []byte{0, 1, 2, 3, 4, 5})},
				addrs: []*ipv1.InterfaceAddress{
					ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24)}.Build(),
					ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 25)}.Build(),
				},
			},
			conflictOn: "ip_address",
			omitted: func(result netmodel.Result) bool {
				return len(result.Spec.Config.Routing.VRFs[routing.DefaultVRF].Interfaces[routedName].Prefixes) == 0
			},
		},
		{
			name: "neighbor",
			forward: loadInput{
				ifaces: []*interfacev1.Interface{routedVLANInterface(routedName, vid, []byte{0, 1, 2, 3, 4, 5})},
				addrs: []*ipv1.InterfaceAddress{
					ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24)}.Build(),
				},
				neighbors: []*ipv1.NeighborEntry{
					ipv1.NeighborEntry_builder{InterfaceName: &routedName, Ip: protoIPv4Addr([4]byte{10, 0, 0, 2}), Mac: protoEUI48([6]byte{0, 1, 2, 3, 4, 6})}.Build(),
					ipv1.NeighborEntry_builder{InterfaceName: &routedName, Ip: protoIPv4Addr([4]byte{10, 0, 0, 2}), Mac: protoEUI48([6]byte{0, 1, 2, 3, 4, 7})}.Build(),
				},
			},
			conflictOn: "ip_neighbor",
			omitted: func(result netmodel.Result) bool {
				return len(result.Spec.Config.Routing.VRFs[routing.DefaultVRF].Neighbors) == 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.forward.validate(t)
			tt.reverse = tt.forward
			tt.reverse.fdb = reversed(tt.forward.fdb)
			tt.reverse.vlans = reversed(tt.forward.vlans)
			tt.reverse.budgets = reversed(tt.forward.budgets)
			tt.reverse.stpPorts = reversed(tt.forward.stpPorts)
			tt.reverse.lacpAggregators = reversed(tt.forward.lacpAggregators)
			tt.reverse.lacpPorts = reversed(tt.forward.lacpPorts)
			tt.reverse.addrs = reversed(tt.forward.addrs)
			tt.reverse.neighbors = reversed(tt.forward.neighbors)

			forward := tt.forward.load(t, src)
			reverse := tt.reverse.load(t, src)
			repeated := tt.forward.load(t, src)
			assertEqualLoadResults(t, forward, reverse)
			assertEqualLoadResults(t, forward, repeated)
			assertEvidenceSource(t, forward, src)

			if forward.Readiness() != analysis.Unstable {
				t.Errorf("readiness = %v, want Unstable", forward.Readiness())
			}
			if len(forward.Report.Conflicts) != 1 || forward.Report.Conflicts[0].What != tt.conflictOn {
				t.Errorf("conflicts = %+v, want one %q conflict", forward.Report.Conflicts, tt.conflictOn)
			}
			if !tt.omitted(forward) {
				t.Errorf("conflicting %s remained executable: %+v", tt.conflictOn, forward.Spec)
			}
			if _, err := vswitch.NewWithSpec(forward.Spec); err != nil {
				t.Errorf("NewWithSpec: %v", err)
			}
		})
	}
}

func TestLoadMalformedNetworkValuesAreScopedPartialRows(t *testing.T) {
	vid := uint32(10)
	routedName := "vlan10"
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	static := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	malformedIP := addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: []byte{10, 0, 0}}.Build(),
	}.Build()
	malformedPrefix := addrv1.IpPrefix_builder{
		V4: addrv1.Ipv4Prefix_builder{
			Address: addrv1.Ipv4Address_builder{Octets: []byte{10, 0, 0}}.Build(),
			Length:  ptr(uint32(24)),
		}.Build(),
	}.Build()

	input := loadInput{
		ifaces: []*interfacev1.Interface{
			routedVLANInterface(routedName, vid, []byte{0, 1, 2, 3, 4}),
			plainPhysicalInterface(portName),
		},
		fdb: []*switchingv1.FdbEntry{
			switchingv1.FdbEntry_builder{
				VlanId: &vid, InterfaceName: &portName,
				Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(), Kind: &static, Status: &active,
			}.Build(),
		},
		addrs: []*ipv1.InterfaceAddress{
			ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: malformedIP, Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24)}.Build(),
			ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: malformedPrefix}.Build(),
		},
		neighbors: []*ipv1.NeighborEntry{
			ipv1.NeighborEntry_builder{InterfaceName: &routedName, Ip: malformedIP, Mac: protoEUI48([6]byte{0, 1, 2, 3, 4, 6})}.Build(),
			ipv1.NeighborEntry_builder{
				InterfaceName: &routedName, Ip: protoIPv4Addr([4]byte{10, 0, 0, 2}),
				Mac: addrv1.EuiAddress_builder{Eui48: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build()}.Build(),
			}.Build(),
		},
	}

	result := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "malformed"})
	if result.Readiness() == analysis.Complete {
		t.Fatal("readiness = Complete, want non-Complete")
	}
	if len(result.Spec.Seeds) != 0 {
		t.Errorf("seeds = %+v, want malformed FDB row omitted", result.Spec.Seeds)
	}
	vrf := result.Spec.Config.Routing.VRFs[routing.DefaultVRF]
	if prefixes := vrf.Interfaces[routedName].Prefixes; len(prefixes) != 0 {
		t.Errorf("prefixes = %v, want malformed rows omitted", prefixes)
	}
	if len(vrf.Neighbors) != 0 {
		t.Errorf("neighbors = %+v, want malformed rows omitted", vrf.Neighbors)
	}

	wants := []struct {
		what string
		port string
	}{
		{what: "interface_mac", port: routedName},
		{what: "fdb_entry", port: portName},
		{what: "ip_address", port: routedName},
		{what: "ip_neighbor", port: routedName},
	}
	for _, want := range wants {
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == want.what && skip.Port == want.port
		}) {
			t.Errorf("skipped = %+v, want scoped %s skip for %s", result.Report.Skipped, want.what, want.port)
		}
	}
	if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
		t.Errorf("NewWithSpec: %v", err)
	}
}

func TestLoadMalformedProtocolMACsArePartial(t *testing.T) {
	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "protocol-mac"}

	t.Run("spanning tree bridge", func(t *testing.T) {
		protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
		priority := uint32(32768)
		bridgeState := stpv1.BridgeState_builder{
			ProtocolVersion: &protocol,
			BridgeId: stpv1.BridgeId_builder{
				Priority: &priority,
				Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(),
			}.Build(),
		}.Build()
		result := (loadInput{
			ifaces:      []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
			bridgeState: bridgeState,
		}).load(t, src)

		if result.Spec.Config.STP != nil {
			t.Errorf("STP config = %+v, want malformed bridge state omitted", result.Spec.Config.STP)
		}
		if result.Readiness() == analysis.Complete {
			t.Fatal("readiness = Complete, want non-Complete")
		}
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == "stp_bridge"
		}) {
			t.Errorf("skipped = %+v, want stp_bridge", result.Report.Skipped)
		}
		if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
			t.Errorf("NewWithSpec: %v", err)
		}
	})

	t.Run("link aggregation system id", func(t *testing.T) {
		lagName := "lag1"
		active := lacpv1.LacpMode_LACP_MODE_ACTIVE
		result := (loadInput{
			ifaces: lagInterfaces(),
			lacpAggregators: []*lacpv1.AggregatorState{
				lacpv1.AggregatorState_builder{
					InterfaceName: &lagName,
					Mode:          &active,
					SystemId:      addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(),
				}.Build(),
			},
		}).load(t, src)

		if result.Spec.Config.LAG.LAGs[lagName].LACP.Mode != lag.Off {
			t.Errorf("LACP mode = %v, want default Off", result.Spec.Config.LAG.LAGs[lagName].LACP.Mode)
		}
		if result.Readiness() == analysis.Complete {
			t.Fatal("readiness = Complete, want non-Complete")
		}
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == "lacp_aggregator" && skip.Port == lagName
		}) {
			t.Errorf("skipped = %+v, want scoped lacp_aggregator", result.Report.Skipped)
		}
		if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
			t.Errorf("NewWithSpec: %v", err)
		}
	})
}

func TestLoadInvalidSTPBridgeSkipsEveryPortRow(t *testing.T) {
	protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
	priority := uint32(32767)
	bridgeState := stpv1.BridgeState_builder{
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
	result := (loadInput{
		ifaces:      []*interfacev1.Interface{plainPhysicalInterface(portA), plainPhysicalInterface(portB)},
		bridgeState: bridgeState,
		stpPorts: []*stpv1.PortState{
			stpv1.PortState_builder{InterfaceName: &portA}.Build(),
			nil,
			stpv1.PortState_builder{InterfaceName: &portB}.Build(),
		},
		want: []port.Layer{port.LayerStp},
	}).load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"})

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
	result := (loadInput{
		ifaces: []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
		stpPorts: []*stpv1.PortState{
			stpv1.PortState_builder{InterfaceName: ptr("1/1/1")}.Build(),
		},
		want: []port.Layer{port.LayerStp},
	}).load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "missing-bridge"})

	if slices.Contains(result.Report.Capabilities, port.LayerStp) {
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
	result := (loadInput{
		ifaces: []*interfacev1.Interface{plainPhysicalInterface(name)},
		stpPorts: []*stpv1.PortState{
			stpv1.PortState_builder{InterfaceName: &name}.Build(),
		},
	}).load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "orphan-stp-row"})

	if slices.Contains(result.Report.Capabilities, port.LayerStp) || result.Spec.Config.STP != nil {
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

func TestLoadUnknownEnumValuesAreScopedUnsupported(t *testing.T) {
	assertUnsupported := func(t *testing.T, result netmodel.Result, code analysis.IssueCode, scope analysis.Scope) {
		t.Helper()
		if result.Readiness() != analysis.Unsupported {
			t.Fatalf("readiness = %s, want %s; issues: %+v", result.Readiness(), analysis.Unsupported, result.Metadata.Issues())
		}
		issues := result.Metadata.Issues()
		issueIndex := slices.IndexFunc(issues, func(issue analysis.Issue) bool {
			return issue.Code == code && issue.Scope.Compare(scope) == 0
		})
		if issueIndex < 0 {
			t.Fatalf("issues = %+v, want %s at %s", issues, code, scope)
		}
		issue := issues[issueIndex]
		if len(issue.Evidence) == 0 {
			t.Fatalf("issue %s has no evidence", code)
		}
		if _, ok := result.Metadata.Evidence().Lookup(issue.Evidence[0]); !ok {
			t.Errorf("issue %s evidence is absent from catalog", code)
		}
	}

	t.Run("frame admission", func(t *testing.T) {
		name := "1/1/1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := switchingv1.FrameAdmission(99)
		result := (loadInput{ifaces: []*interfacev1.Interface{
			interfacev1.Interface_builder{
				Name: &name, AdminStatus: &admin, OperStatus: &oper,
				Physical: interfacev1.PhysicalInterface_builder{
					Switchport: switchingv1.SwitchportFacet_builder{FrameAdmission: &unknown}.Build(),
				}.Build(),
			}.Build(),
		}}).load(t, netmodel.SourceContext{DeviceID: "sw1"})
		assertUnsupported(t, result, netmodel.IssueInvalidFrameAdmission, analysis.PortScope("sw1", name))
		if _, ok := result.Spec.Config.Bridge.VLAN.Switchports[name]; ok {
			t.Error("unknown frame admission was coerced into an executable switchport")
		}
	})

	t.Run("switchport mode", func(t *testing.T) {
		name := "1/1/1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := switchingv1.SwitchportMode(99)
		result := (loadInput{ifaces: []*interfacev1.Interface{
			interfacev1.Interface_builder{
				Name: &name, AdminStatus: &admin, OperStatus: &oper,
				Physical: interfacev1.PhysicalInterface_builder{
					Switchport: switchingv1.SwitchportFacet_builder{Mode: &unknown}.Build(),
				}.Build(),
			}.Build(),
		}}).load(t, netmodel.SourceContext{DeviceID: "sw1"})
		assertUnsupported(t, result, netmodel.IssueInvalidSwitchportMode, analysis.PortScope("sw1", name))
		if _, ok := result.Spec.Config.Bridge.VLAN.Switchports[name]; ok {
			t.Error("unknown switchport mode was coerced into an executable switchport")
		}
	})

	t.Run("ethernet duplex", func(t *testing.T) {
		name := "1/1/1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := phyv1.EthernetDuplex(99)
		result := (loadInput{ifaces: []*interfacev1.Interface{
			interfacev1.Interface_builder{
				Name: &name, AdminStatus: &admin, OperStatus: &oper,
				Physical: interfacev1.PhysicalInterface_builder{
					Ethernet: phyv1.EthernetFacet_builder{ActiveDuplex: &unknown}.Build(),
				}.Build(),
			}.Build(),
		}}).load(t, netmodel.SourceContext{DeviceID: "sw1"})
		assertUnsupported(t, result, netmodel.IssueInvalidEthernetDuplex, analysis.PortScope("sw1", name))
		if observed := result.Spec.Config.Phy.Ethernet[name].Observed; observed != nil {
			t.Errorf("unknown duplex produced observed link state %+v", observed)
		}
	})

	t.Run("PoE priority", func(t *testing.T) {
		name := "1/1/1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := phyv1.PoePriority(99)
		group := uint32(1)
		power := uint32(100_000)
		result := (loadInput{
			ifaces: []*interfacev1.Interface{
				interfacev1.Interface_builder{
					Name: &name, AdminStatus: &admin, OperStatus: &oper,
					Physical: interfacev1.PhysicalInterface_builder{
						Ethernet: phyv1.EthernetFacet_builder{
							Copper: phyv1.CopperFacet_builder{
								PoeSettings: phyv1.PoeSettings_builder{Priority: &unknown}.Build(),
								PoeDetail:   phyv1.PoePortDetail_builder{PseGroup: &group}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			},
			budgets: []*phyv1.PseBudget{
				phyv1.PseBudget_builder{PseGroup: &group, PowerMilliwatts: &power}.Build(),
			},
		}).load(t, netmodel.SourceContext{DeviceID: "sw1"})
		assertUnsupported(t, result, netmodel.IssueInvalidPoePriority, analysis.PortScope("sw1", name))
		if _, ok := result.Spec.Config.Phy.PoE.Ports[name]; ok {
			t.Error("unknown PoE priority was coerced into an executable PSE port")
		}
	})

	t.Run("stp point to point", func(t *testing.T) {
		name := "1/1/1"
		unknown := stpv1.PointToPointMode(99)
		result := (loadInput{
			ifaces:      []*interfacev1.Interface{plainPhysicalInterface(name)},
			bridgeState: validBridgeState(),
			stpPorts: []*stpv1.PortState{
				stpv1.PortState_builder{InterfaceName: &name, PointToPoint: &unknown}.Build(),
			},
		}).load(t, netmodel.SourceContext{DeviceID: "sw1"})
		assertUnsupported(t, result, netmodel.IssueInvalidPointToPointMode, analysis.FieldScope(
			analysis.ProtocolScope("sw1", string(port.LayerStp), "0"), "ports", name,
		))
		if _, ok := result.Spec.Config.STP.Ports[name]; ok {
			t.Error("unknown point-to-point mode was coerced into an executable STP row")
		}
	})

	t.Run("lacp mode", func(t *testing.T) {
		name := "lag1"
		unknown := lacpv1.LacpMode(99)
		result := (loadInput{
			ifaces: lagInterfaces(),
			lacpAggregators: []*lacpv1.AggregatorState{
				lacpv1.AggregatorState_builder{InterfaceName: &name, Mode: &unknown}.Build(),
			},
		}).load(t, netmodel.SourceContext{DeviceID: "sw1"})
		assertUnsupported(t, result, netmodel.IssueInvalidLACPMode, analysis.PortScope("sw1", name))
	})

	t.Run("bond mode", func(t *testing.T) {
		name := "lag1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := switchingv1.BondMode(99)
		ifaces := lagInterfaces()
		ifaces[0] = interfacev1.Interface_builder{
			Name: &name, AdminStatus: &admin, OperStatus: &oper,
			Lag: interfacev1.LagInterface_builder{
				Aggregation: switchingv1.AggregationFacet_builder{BondMode: &unknown}.Build(),
			}.Build(),
		}.Build()
		result := (loadInput{ifaces: ifaces}).load(t, netmodel.SourceContext{DeviceID: "sw1"})
		assertUnsupported(t, result, netmodel.IssueInvalidBondMode, analysis.PortScope("sw1", name))
	})
}

func reversed[T any](values []T) []T {
	result := slices.Clone(values)
	slices.Reverse(result)
	return result
}

func ptr[T any](value T) *T {
	return &value
}

func TestLoadAutoNegotiationSupportedAbsence(t *testing.T) {
	name := "1/1/1"
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	speeds := []uint64{1_000_000_000}

	iface := interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Mtu:         &mtu,
		Physical: interfacev1.PhysicalInterface_builder{
			Ethernet: phyv1.EthernetFacet_builder{
				Capabilities: phyv1.EthernetCapabilities_builder{
					SupportedSpeedsBps: speeds,
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()

	input := loadInput{ifaces: []*interfacev1.Interface{iface}, want: []port.Layer{port.LayerEthernet}}
	input.validate(t)

	res := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})
	eth, ok := res.Spec.Config.Phy.Ethernet[name]
	if !ok {
		t.Fatalf("port %s missing from ethernet config", name)
	}
	if eth.AutoNegotiationSupported != phy.CapabilityUnknown {
		t.Errorf("auto_negotiation_supported = %q, want %q", eth.AutoNegotiationSupported, phy.CapabilityUnknown)
	}

	portScope := analysis.PortScope("sw1", name)
	for _, issue := range res.Metadata.Issues() {
		if issue.Scope.Compare(portScope) == 0 {
			t.Errorf("unexpected issue on port: %+v", issue)
		}
	}
	for _, assumption := range res.Metadata.Assumptions() {
		if assumption.Scope.Compare(portScope) == 0 {
			t.Errorf("unexpected assumption on port: %+v", assumption)
		}
	}
}

func TestLoadPoeStatusMapping(t *testing.T) {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	supported := true
	role := phyv1.PoeRole_POE_ROLE_PSE
	group := uint32(1)
	power := uint32(100_000)

	delivering := phyv1.PoeStatus_POE_STATUS_DELIVERING_POWER
	searching := phyv1.PoeStatus_POE_STATUS_SEARCHING
	disabled := phyv1.PoeStatus_POE_STATUS_DISABLED
	testingStatus := phyv1.PoeStatus_POE_STATUS_TEST
	fault := phyv1.PoeStatus_POE_STATUS_FAULT
	otherFault := phyv1.PoeStatus_POE_STATUS_OTHER_FAULT
	unspecified := phyv1.PoeStatus_POE_STATUS_UNSPECIFIED
	unknownEnum := phyv1.PoeStatus(99)

	tests := []struct {
		name           string
		status         *phyv1.PoeStatus
		powerClass     *uint32
		wantPD         phy.PDState
		wantClass      *uint8
		wantAssumption bool
	}{
		{
			name:           "delivering power with class",
			status:         &delivering,
			powerClass:     ptr(uint32(3)),
			wantPD:         phy.PDAttached,
			wantClass:      phy.Class(3),
			wantAssumption: false,
		},
		{
			name:           "searching without class",
			status:         &searching,
			wantPD:         phy.PDAbsent,
			wantAssumption: true,
		},
		{
			name:           "disabled",
			status:         &disabled,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "test",
			status:         &testingStatus,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "fault",
			status:         &fault,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "other fault",
			status:         &otherFault,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "unspecified",
			status:         &unspecified,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "unrecognized enum",
			status:         &unknownEnum,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "absent status",
			status:         nil,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			portName := "1/1/1"
			facetBuilder := phyv1.PoeFacet_builder{
				Supported:  &supported,
				Role:       &role,
				PowerClass: tt.powerClass,
			}
			if tt.status != nil {
				facetBuilder.Status = tt.status
			}

			iface := interfacev1.Interface_builder{
				Name:        &portName,
				AdminStatus: &admin,
				OperStatus:  &oper,
				Mtu:         &mtu,
				Physical: interfacev1.PhysicalInterface_builder{
					Ethernet: phyv1.EthernetFacet_builder{
						Copper: phyv1.CopperFacet_builder{
							Poe:       facetBuilder.Build(),
							PoeDetail: phyv1.PoePortDetail_builder{PseGroup: &group, PsePort: ptr(uint32(1))}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			input := loadInput{
				ifaces: []*interfacev1.Interface{iface},
				budgets: []*phyv1.PseBudget{
					phyv1.PseBudget_builder{PseGroup: &group, PowerMilliwatts: &power}.Build(),
				},
				want: []port.Layer{port.LayerPoe},
			}
			input.validate(t)

			res := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})
			portScope := analysis.PortScope("sw1", portName)

			poePort, ok := res.Spec.Config.Phy.PoE.Ports[portName]
			if !ok {
				t.Fatalf("port %s missing from PoE ports", portName)
			}
			if poePort.PD != tt.wantPD {
				t.Errorf("port PD = %q, want %q", poePort.PD, tt.wantPD)
			}
			if tt.wantClass == nil {
				if poePort.PDClass != nil {
					t.Errorf("port PDClass = %v, want nil", *poePort.PDClass)
				}
			} else {
				if poePort.PDClass == nil || *poePort.PDClass != *tt.wantClass {
					t.Errorf("port PDClass = %v, want %v", poePort.PDClass, *tt.wantClass)
				}
			}

			for _, issue := range res.Metadata.Issues() {
				if issue.Scope.Compare(portScope) == 0 && (issue.Code == netmodel.IssuePowerClassWithoutDelivery || issue.Code == netmodel.IssueUnsupportedPowerClass) {
					t.Errorf("unexpected issue on port: %+v", issue)
				}
			}

			var searchingAssumptions []analysis.Assumption
			for _, a := range res.Metadata.Assumptions() {
				if a.Scope.Compare(portScope) == 0 && a.Statement == "absence is inferred from the searching status" {
					searchingAssumptions = append(searchingAssumptions, a)
				}
			}
			if tt.wantAssumption {
				if len(searchingAssumptions) != 1 {
					t.Fatalf("searching assumptions count = %d, want 1; assumptions: %+v", len(searchingAssumptions), res.Metadata.Assumptions())
				}
				a := searchingAssumptions[0]
				if len(a.Evidence) == 0 {
					t.Error("assumption has no evidence")
				} else if _, found := res.Metadata.Evidence().Lookup(a.Evidence[0]); !found {
					t.Error("assumption evidence not in catalog")
				}
			} else if len(searchingAssumptions) != 0 {
				t.Errorf("unexpected searching assumptions on port: %+v", searchingAssumptions)
			}
		})
	}
}

func TestLoadPoePowerClassWithoutDelivery(t *testing.T) {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP
	mtu := uint32(0)
	supported := true
	role := phyv1.PoeRole_POE_ROLE_PSE
	group := uint32(1)
	power := uint32(100_000)

	searching := phyv1.PoeStatus_POE_STATUS_SEARCHING
	disabled := phyv1.PoeStatus_POE_STATUS_DISABLED

	tests := []struct {
		name           string
		status         *phyv1.PoeStatus
		wantPD         phy.PDState
		wantAssumption bool
	}{
		{
			name:           "searching with power class",
			status:         &searching,
			wantPD:         phy.PDAbsent,
			wantAssumption: true,
		},
		{
			name:           "disabled with power class",
			status:         &disabled,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
		{
			name:           "absent status with power class",
			status:         nil,
			wantPD:         phy.PDUnknown,
			wantAssumption: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			portName := "1/1/1"
			facetBuilder := phyv1.PoeFacet_builder{
				Supported:  &supported,
				Role:       &role,
				PowerClass: ptr(uint32(3)),
			}
			if tt.status != nil {
				facetBuilder.Status = tt.status
			}

			iface := interfacev1.Interface_builder{
				Name:        &portName,
				AdminStatus: &admin,
				OperStatus:  &oper,
				Mtu:         &mtu,
				Physical: interfacev1.PhysicalInterface_builder{
					Ethernet: phyv1.EthernetFacet_builder{
						Copper: phyv1.CopperFacet_builder{
							Poe:       facetBuilder.Build(),
							PoeDetail: phyv1.PoePortDetail_builder{PseGroup: &group, PsePort: ptr(uint32(1))}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			}.Build()

			input := loadInput{
				ifaces: []*interfacev1.Interface{iface},
				budgets: []*phyv1.PseBudget{
					phyv1.PseBudget_builder{PseGroup: &group, PowerMilliwatts: &power}.Build(),
				},
				want: []port.Layer{port.LayerPoe},
			}
			input.validate(t)

			res := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})
			portScope := analysis.PortScope("sw1", portName)

			poePort, ok := res.Spec.Config.Phy.PoE.Ports[portName]
			if !ok {
				t.Fatalf("port %s missing from PoE ports", portName)
			}
			if poePort.PD != tt.wantPD {
				t.Errorf("port PD = %q, want %q", poePort.PD, tt.wantPD)
			}
			if poePort.PDClass != nil {
				t.Errorf("port PDClass = %v, want nil (skipped)", *poePort.PDClass)
			}

			issues := res.Metadata.Issues()
			foundIssue := false
			for _, issue := range issues {
				if issue.Code == netmodel.IssuePowerClassWithoutDelivery {
					foundIssue = true
					if issue.Scope.Compare(portScope) != 0 {
						t.Errorf("issue scope = %s, want %s", issue.Scope, portScope)
					}
					if len(issue.Evidence) == 0 {
						t.Error("issue has no evidence")
					} else if _, found := res.Metadata.Evidence().Lookup(issue.Evidence[0]); !found {
						t.Error("issue evidence not in catalog")
					}
				}
			}
			if !foundIssue {
				t.Fatalf("missing IssuePowerClassWithoutDelivery in issues: %+v", issues)
			}

			var searchingAssumptions []analysis.Assumption
			for _, a := range res.Metadata.Assumptions() {
				if a.Scope.Compare(portScope) == 0 && a.Statement == "absence is inferred from the searching status" {
					searchingAssumptions = append(searchingAssumptions, a)
				}
			}
			if tt.wantAssumption {
				if len(searchingAssumptions) != 1 {
					t.Fatalf("port assumptions count = %d, want 1; assumptions: %+v", len(searchingAssumptions), res.Metadata.Assumptions())
				}
				if searchingAssumptions[0].Statement != "absence is inferred from the searching status" {
					t.Errorf("assumption statement = %q, want %q", searchingAssumptions[0].Statement, "absence is inferred from the searching status")
				}
			} else if len(searchingAssumptions) != 0 {
				t.Errorf("unexpected searching assumptions on port: %+v", searchingAssumptions)
			}
		})
	}
}
