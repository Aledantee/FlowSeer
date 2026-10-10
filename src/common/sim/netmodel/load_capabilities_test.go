package netmodel_test

import (
	"slices"
	"testing"

	"buf.build/go/protovalidate"

	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

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
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	wantCaps := []trace.Layer{bridge.LayerName, bridge.LayerNameVLAN}
	if !slices.Equal(report.Capabilities, wantCaps) {
		t.Errorf("capabilities = %v, want %v", report.Capabilities, wantCaps)
	}
	if report.CapabilitySources[bridge.LayerName] != "always" {
		t.Errorf("relay source = %q, want always", report.CapabilitySources[bridge.LayerName])
	}
	if report.CapabilitySources[bridge.LayerNameVLAN] != "inferred:switchport" {
		t.Errorf("vlan source = %q, want inferred:switchport", report.CapabilitySources[bridge.LayerNameVLAN])
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
	resRelayOnly, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, []trace.Layer{bridge.LayerName})
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
		t.Errorf("got skips %+v, want both switchport facets skipped", reportRelayOnly.Skipped)
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
			LagParentInterfaceName: &lagParent,
		}.Build()}.Build(),
	}
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, []trace.Layer{bridge.LayerNameVLAN})
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
	if report.CapabilitySources[bridge.LayerName] != "implied:vlan" || report.CapabilitySources[lag.LayerName] != "present:lag" {
		t.Errorf("CapabilitySources = %v", report.CapabilitySources)
	}
}

func TestLoad_RequestLayerFilterAccepted(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vid10 := uint32(10)
	vlan10Name := "vlan10"
	inSetName := "in-set"
	acceptAction := filterv1.FilterAction_FILTER_ACTION_ACCEPT

	inSet := filterv1.FilterRuleSet_builder{
		Name:    &inSetName,
		Default: &acceptAction,
	}.Build()

	vlan10 := interfacev1.Interface_builder{
		Name:        &vlan10Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Vlan: interfacev1.VlanInterface_builder{
			VlanId: &vid10,
		}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
		Filter: filterv1.FilterFacet_builder{
			InSet: &inSetName,
		}.Build(),
	}.Build()

	res, err := netmodel.Load(
		testTime,
		netmodel.SourceContext{DeviceID: "sw1"},
		[]*interfacev1.Interface{vlan10},
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
		[]*filterv1.FilterRuleSet{inSet},
		[]trace.Layer{filter.LayerName},
	)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}

	for _, issue := range res.Metadata.Issues() {
		if issue.Code == netmodel.IssueUnsupportedRequestedCapability {
			t.Errorf("unexpected IssueUnsupportedRequestedCapability for LayerFilter: %+v", issue)
		}
	}

	if !slices.Contains(res.Report.Capabilities, filter.LayerName) {
		t.Errorf("capabilities = %v, want to contain filter.LayerName", res.Report.Capabilities)
	}
}

func TestLoadRejectsUnusableRequestedCapabilities(t *testing.T) {
	result := (loadInput{
		ifaces: []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
		want: []trace.Layer{
			bridge.LayerName,
			bridge.LayerName,
			mcast.LayerName,
			trace.Layer("future-layer"),
		},
	}).load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "requested-capabilities"})

	if !slices.Equal(result.Report.Capabilities, []trace.Layer{bridge.LayerName}) {
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
		want:   []trace.Layer{mcast.LayerName, trace.Layer("future-layer")},
	}).load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "unsupported-capabilities"})

	if len(result.Report.Capabilities) != 0 {
		t.Errorf("capabilities = %v, want no inferred capabilities for an explicit request", result.Report.Capabilities)
	}
	if result.Readiness() != analysis.Unsupported {
		t.Errorf("readiness = %s, want Unsupported", result.Readiness())
	}
}

// TestLoad_CapabilityWalkIgnoresRoutedSubParentSwitchport pins that the
// capability walk does not count a routed sub-interface parent's own
// switchport facet toward VLAN inference: the port walk already treats
// that switchport as absent, so the only switchport facet in this load
// belongs to a port the routing walk owns. The VLAN capability must come
// from the routed interface itself, not from a switchport report the
// finished configuration no longer carries.
func TestLoad_CapabilityWalkIgnoresRoutedSubParentSwitchport(t *testing.T) {
	parentName := "eth1"
	subName := "eth1.10"

	parent := switchedPhysicalInterface(parentName, 5)
	sub := subInterface(subName, parentName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10))

	input := loadInput{ifaces: []*interfacev1.Interface{parent, sub}}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

	if got := loaded.Report.CapabilitySources[bridge.LayerNameVLAN]; got != "implied:routing" {
		t.Errorf("vlan capability source = %q, want implied:routing", got)
	}
}
