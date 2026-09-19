package netmodel_test

import (
	"testing"
	"time"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestTopologyShadowingConformance(t *testing.T) {
	reg := netsimtest.DefaultRegistry()
	c, ok := reg.Get("topology-shadowing/partial-model-unknown-port")
	if !ok {
		t.Fatal("topology-shadowing/partial-model-unknown-port case not found in default registry")
	}

	res := netsimtest.AssertCase(t, c)

	if res.ModelResult == nil {
		t.Fatal("res.ModelResult is nil")
	}
	if res.ModelResult.Readiness() != analysis.Incomplete {
		t.Errorf("res.ModelResult.Readiness() = %v, want %v", res.ModelResult.Readiness(), analysis.Incomplete)
	}

	// Localized scope: unknown port is incomplete, but sibling known-up port is complete.
	unknownScope := analysis.PortScope("shadow-sw1", "1/1/2")
	knownScope := analysis.PortScope("shadow-sw1", "1/1/1")

	if res.ModelResult.Metadata.StatusFor(unknownScope) != analysis.Incomplete {
		t.Errorf("StatusFor(%s) = %v, want %v", unknownScope, res.ModelResult.Metadata.StatusFor(unknownScope), analysis.Incomplete)
	}
	if res.ModelResult.Metadata.StatusFor(knownScope) != analysis.Complete {
		t.Errorf("StatusFor(%s) = %v, want %v", knownScope, res.ModelResult.Metadata.StatusFor(knownScope), analysis.Complete)
	}

	// Verify switch constructed from partial specification.
	if res.Switch == nil {
		t.Fatal("res.Switch is nil")
	}

	now := time.Unix(1700000000, 0)
	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}

	// The unknown sibling is a possible flood egress, so its state is relevant
	// even when the frame arrived on the known-up port.
	fwdKnown := res.Switch.Forward(now, "1/1/1", frame)
	if fwdKnown.Metadata.Status() != analysis.Incomplete {
		t.Errorf("fwdKnown.Metadata.Status() = %v, want %v", fwdKnown.Metadata.Status(), analysis.Incomplete)
	}

	// Unknown port forwarding drops with port-down and has Incomplete status.
	fwdUnknown := res.Switch.Forward(now, "1/1/2", frame)
	if fwdUnknown.Outcome != trace.Dropped {
		t.Errorf("fwdUnknown.Outcome = %v, want %v", fwdUnknown.Outcome, trace.Dropped)
	}
	if fwdUnknown.Reason != port.ReasonPortDown {
		t.Errorf("fwdUnknown.Reason = %v, want %v", fwdUnknown.Reason, port.ReasonPortDown)
	}
	if fwdUnknown.Metadata.Status() != analysis.Incomplete {
		t.Errorf("fwdUnknown.Metadata.Status() = %v, want %v", fwdUnknown.Metadata.Status(), analysis.Incomplete)
	}
}

func TestModelLoadErrorVsPartialResultSeparation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "telemetry"}

	// Empty interface slice is an unconstructible error.
	_, err := netmodel.Load(now, src, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil {
		t.Error("Load with nil interfaces did not return an error")
	}

	// Duplicate interface name is an unconstructible error.
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"
	p1A := interfacev1.Interface_builder{Name: &p1Name, AdminStatus: &adminUp, OperStatus: &operUp}.Build()
	p1B := interfacev1.Interface_builder{Name: &p1Name, AdminStatus: &adminUp, OperStatus: &operUp}.Build()

	_, err = netmodel.Load(now, src, []*interfacev1.Interface{p1A, p1B}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err == nil {
		t.Error("Load with duplicate interface names did not return an error")
	}

	// Partial valid input (unspecified oper status) returns a constructible result without an error.
	operUnknown := interfacev1.OperStatus_OPER_STATUS_UNKNOWN
	p2Name := "1/1/2"
	p2 := interfacev1.Interface_builder{Name: &p2Name, AdminStatus: &adminUp, OperStatus: &operUnknown}.Build()

	res, err := netmodel.Load(now, src, []*interfacev1.Interface{p1A, p2}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load with partial input returned unexpected error: %v", err)
	}
	if res.Readiness() != analysis.Incomplete {
		t.Errorf("res.Readiness() = %v, want %v", res.Readiness(), analysis.Incomplete)
	}

	// Spec is constructible without error.
	sw, err := vswitch.NewWithSpec(res.Spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec failed on partial model: %v", err)
	}
	if sw.Ports().Len() != 2 {
		t.Errorf("sw.Ports().Len() = %d, want 2", sw.Ports().Len())
	}
}

func TestDeterministicReportAndMetadataOrdering(t *testing.T) {
	now := time.Unix(1700000000, 0)
	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "telemetry"}

	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	pvid10 := uint32(10)
	frameAdmAll := switchingv1.FrameAdmission_FRAME_ADMISSION_ALL
	ingressFiltFalse := false

	buildInputs := func() ([]*interfacev1.Interface, []*switchingv1.Vlan) {
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
		operUnknown := interfacev1.OperStatus_OPER_STATUS_UNKNOWN
		p2 := interfacev1.Interface_builder{
			Name:        &p2Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUnknown,
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
		vname10 := "prod"
		vlans := []*switchingv1.Vlan{
			switchingv1.Vlan_builder{Id: &vid10, Name: &vname10}.Build(),
		}
		return []*interfacev1.Interface{p1, p2}, vlans
	}

	ifaces1, vlans1 := buildInputs()
	res1, err := netmodel.Load(now, src, ifaces1, vlans1, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("first Load failed: %v", err)
	}

	ifaces2, vlans2 := buildInputs()
	res2, err := netmodel.Load(now, src, ifaces2, vlans2, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("second Load failed: %v", err)
	}

	// Verify deterministic report ordering and equality.
	if len(res1.Report.Defaults) != len(res2.Report.Defaults) {
		t.Errorf("Defaults length mismatch: %d vs %d", len(res1.Report.Defaults), len(res2.Report.Defaults))
	}
	for i := range res1.Report.Defaults {
		if res1.Report.Defaults[i].Field != res2.Report.Defaults[i].Field ||
			res1.Report.Defaults[i].Value != res2.Report.Defaults[i].Value {
			t.Errorf("Defaults at index %d mismatch: %v vs %v", i, res1.Report.Defaults[i], res2.Report.Defaults[i])
		}
	}

	issues1 := res1.Metadata.Issues()
	issues2 := res2.Metadata.Issues()
	if len(issues1) != len(issues2) {
		t.Fatalf("Issues length mismatch: %d vs %d", len(issues1), len(issues2))
	}
	for i := range issues1 {
		if issues1[i].Code != issues2[i].Code || issues1[i].Status != issues2[i].Status {
			t.Errorf("Issues at index %d mismatch: %v vs %v", i, issues1[i], issues2[i])
		}
	}
}
