package bridge_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// TestGateBlockedFrameCarriesItsClassifiedFID pins the reorder's first effect: the gate
// now runs after classification, so the drop names the VLAN the frame was
// classified into rather than a zero standing in for "not classified yet".
func TestGateBlockedFrameCarriesItsClassifiedFID(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := mustNewBridge(t, gateVLANConfig(), ports)
	br.SetGate(testGate{
		learns:   map[string]bool{"1/1/1": false, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "stp", "0"))

	// An untagged frame classifies cleanly into the port's PVID.
	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	res := br.Forward(testTime0, "1/1/1", frame)

	if res.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("res.Reason = %q, want %q", res.Reason, bridge.ReasonPortBlocked)
	}
	if res.FID != 10 {
		t.Errorf("res.FID = %d, want 10: the frame was classified before the gate ran", res.FID)
	}

	// The classification step precedes the gate's drop, which is the order the
	// FID above depends on.
	classifyAt, blockAt := -1, -1
	for i, step := range res.Steps {
		if step.Op == trace.OpClassify && classifyAt < 0 {
			classifyAt = i
		}
		if step.RuleID == trace.RuleID("port-blocked") && blockAt < 0 {
			blockAt = i
		}
	}
	if classifyAt < 0 || blockAt < 0 {
		t.Fatalf("steps = %+v, want both a classify step and a port-blocked drop", res.Steps)
	}
	if classifyAt > blockAt {
		t.Errorf("classify step at %d, port-blocked at %d: want classification first", classifyAt, blockAt)
	}
}

// TestClassificationFailureOutranksPortBlocked pins the reason precedence. A frame
// the port would never have admitted is not a spanning-tree question, so the
// classification reason is the useful answer even though the port is blocked.
func TestClassificationFailureOutranksPortBlocked(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := mustNewBridge(t, gateVLANConfig(), ports)
	br.SetGate(testGate{
		learns:   map[string]bool{"1/1/1": false, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "stp", "0"))

	// VLAN 20 is not a member of 1/1/1 and the port filters on ingress.
	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		Tags:      []vlan.Tag{{TPID: 0x8100, VID: 20}},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("data"),
	}
	res := br.Forward(testTime0, "1/1/1", frame)

	if res.Reason != bridge.ReasonIngressFilter {
		t.Errorf("res.Reason = %q, want %q: classification runs ahead of the gate", res.Reason, bridge.ReasonIngressFilter)
	}
}

func TestGateBlocksIngressWithoutLearning(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := mustNewBridge(t, bridge.Config{}, ports)
	br.SetGate(testGate{
		learns:   map[string]bool{"1/1/1": false, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "stp", "0"))

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Dropped)
	}
	if res.Reason != bridge.ReasonPortBlocked {
		t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonPortBlocked)
	}
	if len(br.Entries()) != 0 {
		t.Errorf("len(br.Entries()) = %d, want 0", len(br.Entries()))
	}
}

func TestGateLearningOnlyIngressLearnsThenDrops(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := mustNewBridge(t, bridge.Config{}, ports)
	br.SetGate(testGate{
		learns:   map[string]bool{"1/1/1": true, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "stp", "0"))

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Dropped {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Dropped)
	}
	if res.Reason != bridge.ReasonPortBlocked {
		t.Errorf("res.Reason = %q, want %q", res.Reason, bridge.ReasonPortBlocked)
	}
	entries := br.Entries()
	if len(entries) != 1 || entries[0].MAC != macA || entries[0].Port != "1/1/1" {
		t.Fatalf("Entries() = %+v, want learned entry for macA on 1/1/1", entries)
	}
}

func TestGateBlocksEgress(t *testing.T) {
	ports := buildTestPorts(t, 3)
	br := mustNewBridge(t, bridge.Config{}, ports)
	br.SetGate(testGate{
		learns:   map[string]bool{"1/1/1": true, "1/1/2": true, "1/1/3": true},
		forwards: map[string]bool{"1/1/1": true, "1/1/2": false, "1/1/3": true},
	}, analysis.ProtocolScope("sw1", "stp", "0"))

	frame := ethernet.Frame{
		Dst:       macB,
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test"),
	}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want %q", res.Outcome, trace.Flooded)
	}
	var egress2, egress3 *bridge.Egress
	for i := range res.Egress {
		if res.Egress[i].Port == "1/1/2" {
			egress2 = &res.Egress[i]
		}
		if res.Egress[i].Port == "1/1/3" {
			egress3 = &res.Egress[i]
		}
	}
	if egress2 == nil || egress2.Dropped != bridge.ReasonPortBlocked {
		t.Errorf("egress on 1/1/2 = %+v, want dropped with %q", egress2, bridge.ReasonPortBlocked)
	}
	if egress3 == nil || egress3.Dropped != "" {
		t.Errorf("egress on 1/1/3 = %+v, want forwarded", egress3)
	}
}

// TestMultiGateIngressBothDenyRecordsFirstInstallationOrder is evidence that
// when every consulted gate denies both Learns and Forwards on the ingress
// port, the ingress drop attributes to the first gate installed, in
// installation order, that denies both, matching a single-gate bridge's
// behavior of naming its one gate.
func TestMultiGateIngressBothDenyRecordsFirstInstallationOrder(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := mustNewBridge(t, bridge.Config{}, ports)
	br.SetGate(testSemanticGate{
		name:     "first",
		learns:   map[string]bool{"1/1/1": false, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "stp", "0"))
	br.SetGate(testSemanticGate{
		name:     "second",
		learns:   map[string]bool{"1/1/1": false, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "second", "0"))

	frame := ethernet.Frame{Dst: macB, Src: macA, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("test")}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("res.Reason = %q, want %q", res.Reason, bridge.ReasonPortBlocked)
	}
	step, ok := portBlockedStep(res.Steps)
	if !ok {
		t.Fatalf("steps = %+v, want a port-blocked drop", res.Steps)
	}
	fact, ok := gateFactIn(step.Inputs)
	if !ok {
		t.Fatalf("port-blocked step Inputs = %+v, want a semantic gate fact", step.Inputs)
	}
	if want := testSemanticFact("first:1/1/1:false:false"); fact != want {
		t.Errorf("gate fact = %q, want %q: both gates deny both, so the first installed decides", fact, want)
	}
}

// TestMultiGateIngressSecondGateDeniesBothRecordsSecondsFact is evidence
// against the naive "first gate that denies anything" rule: a port the first
// gate merely holds in Learning (Learns true, Forwards false) alongside a
// second gate that denies both must not be attributed to the first gate,
// since the first gate did not cause the ingress drop.
func TestMultiGateIngressSecondGateDeniesBothRecordsSecondsFact(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := mustNewBridge(t, bridge.Config{}, ports)
	br.SetGate(testSemanticGate{
		name:     "first",
		learns:   map[string]bool{"1/1/1": true, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "stp", "0"))
	br.SetGate(testSemanticGate{
		name:     "second",
		learns:   map[string]bool{"1/1/1": false, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": false, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "second", "0"))

	frame := ethernet.Frame{Dst: macB, Src: macA, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("test")}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Reason != bridge.ReasonPortBlocked {
		t.Fatalf("res.Reason = %q, want %q", res.Reason, bridge.ReasonPortBlocked)
	}
	step, ok := portBlockedStep(res.Steps)
	if !ok {
		t.Fatalf("steps = %+v, want a port-blocked drop", res.Steps)
	}
	fact, ok := gateFactIn(step.Inputs)
	if !ok {
		t.Fatalf("port-blocked step Inputs = %+v, want a semantic gate fact", step.Inputs)
	}
	if want := testSemanticFact("second:1/1/1:false:false"); fact != want {
		t.Errorf("gate fact = %q, want %q: the first gate only denies Forwards, so the gate that denies both decides", fact, want)
	}
	if len(br.Entries()) != 0 {
		t.Errorf("len(br.Entries()) = %d, want 0: the drop happens before learning since Forwards is false on both gates", len(br.Entries()))
	}
}

// TestMultiGateScopeOnlyEntryGatesNothingButIsConsulted is evidence that a
// nil gate installed for a scope, meaning that scope's protocol metadata
// exists without a layer running, gates nothing on its own but still adds its
// scope to the consulted set, exactly as it does for a single-gate bridge.
func TestMultiGateScopeOnlyEntryGatesNothingButIsConsulted(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := mustNewBridge(t, bridge.Config{}, ports)
	scopeOnly := analysis.ProtocolScope("sw1", "stp", "0")
	br.SetGate(nil, scopeOnly)
	br.SetGate(testSemanticGate{
		name:     "second",
		learns:   map[string]bool{"1/1/1": true, "1/1/2": true},
		forwards: map[string]bool{"1/1/1": true, "1/1/2": true},
	}, analysis.ProtocolScope("sw1", "second", "0"))

	frame := ethernet.Frame{Dst: macB, Src: macA, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("test")}
	res := br.Forward(testTime0, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("res.Outcome = %q, want %q: the nil gate allows and the other gate allows", res.Outcome, trace.Flooded)
	}
	want := analysis.FieldScope(scopeOnly, "ports", "1/1/1")
	found := false
	for _, scope := range res.ConsultedScopes() {
		if scope.Compare(want) == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ConsultedScopes() = %+v, want it to contain %+v", res.ConsultedScopes(), want)
	}
}

// TestMultiGateConsultedScopesAreTheUnion is evidence that every installed
// gate's scope, keyed by port, is consulted: forwarding this frame depends on
// every gate's decision, so a change to any one gate's scope could change the
// outcome.
func TestMultiGateConsultedScopesAreTheUnion(t *testing.T) {
	ports := buildTestPorts(t, 2)
	br := mustNewBridge(t, bridge.Config{}, ports)
	firstScope := analysis.ProtocolScope("sw1", "stp", "0")
	secondScope := analysis.ProtocolScope("sw1", "second", "0")
	br.SetGate(testSemanticGate{name: "first"}, firstScope)
	br.SetGate(testSemanticGate{name: "second"}, secondScope)

	frame := ethernet.Frame{Dst: macB, Src: macA, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("test")}
	res := br.Forward(testTime0, "1/1/1", frame)

	want := []analysis.Scope{
		analysis.FieldScope(firstScope, "ports", "1/1/1"),
		analysis.FieldScope(secondScope, "ports", "1/1/1"),
	}
	for _, w := range want {
		found := false
		for _, scope := range res.ConsultedScopes() {
			if scope.Compare(w) == 0 {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ConsultedScopes() = %+v, want it to contain %+v", res.ConsultedScopes(), w)
		}
	}
}

func TestCloneClearsGatesAndKeepsScopes(t *testing.T) {
	br := mustNewBridge(t, bridge.Config{}, buildTestPorts(t, 2))
	scope := analysis.ProtocolScope("sw1", "stp", "0")
	forwards := map[string]bool{"1/1/1": true, "1/1/2": false}
	br.SetGate(testGate{forwards: forwards}, scope)
	frame := ethernet.Frame{Dst: macB, Src: macA, EtherType: ethernet.EtherTypeIPv4}
	if got := br.Forward(testTime0, "1/1/1", frame); len(got.Egress) != 1 || got.Egress[0].Port != "1/1/2" || got.Egress[0].Dropped != bridge.ReasonPortBlocked {
		t.Fatalf("source egress = %+v, want 1/1/2 blocked", got.Egress)
	}
	clone := br.Clone()
	check := func() {
		t.Helper()
		got := clone.Forward(testTime0, "1/1/1", frame)
		if got.Outcome != trace.Flooded || len(got.Egress) != 1 || got.Egress[0].Port != "1/1/2" || got.Egress[0].Dropped != "" {
			t.Errorf("clone result = %+v, want flood to 1/1/2", got)
		}
		for _, name := range []string{"1/1/1", "1/1/2"} {
			want := analysis.FieldScope(scope, "ports", name)
			found := false
			for _, consulted := range got.ConsultedScopes() {
				if consulted.Compare(want) == 0 {
					found = true
				}
			}
			if !found {
				t.Errorf("clone consulted = %+v, want %+v", got.ConsultedScopes(), want)
			}
		}
	}
	check()
	forwards["1/1/2"] = true
	check()
	forwards["1/1/1"] = false
	check()
}
