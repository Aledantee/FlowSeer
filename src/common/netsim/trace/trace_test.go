package trace_test

import (
	"fmt"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

type testVLANFact struct {
	vid int
}

func (f testVLANFact) TypeID() string {
	return "vlan"
}

func (f testVLANFact) Canonical() string {
	return fmt.Sprintf("%d", f.vid)
}

type testMACFact struct {
	addr string
}

func (f testMACFact) TypeID() string {
	return "mac"
}

func (f testMACFact) Canonical() string {
	return f.addr
}

type testActionFact struct {
	action string
}

func (f testActionFact) TypeID() string {
	return "action"
}

func (f testActionFact) Canonical() string {
	return f.action
}

type testStatusFact struct {
	status string
}

func (f testStatusFact) TypeID() string {
	return "admin_status"
}

func (f testStatusFact) Canonical() string {
	return f.status
}

type misleadingStringFact struct{}

func (misleadingStringFact) TypeID() string {
	return "stable"
}

func (misleadingStringFact) Canonical() string {
	return "canonical"
}

func (misleadingStringFact) String() string {
	return "unstable display"
}

func TestVocabulary(t *testing.T) {
	t.Run("outcomes", func(t *testing.T) {
		if trace.Forwarded != "Forwarded" || trace.Flooded != "Flooded" || trace.Dropped != "Dropped" || trace.Consumed != "Consumed" {
			t.Errorf("unexpected outcomes: %q %q %q %q", trace.Forwarded, trace.Flooded, trace.Dropped, trace.Consumed)
		}
	})

	t.Run("operations", func(t *testing.T) {
		ops := []trace.Op{
			trace.OpClassify,
			trace.OpFilter,
			trace.OpLearn,
			trace.OpLookup,
			trace.OpReplicate,
			trace.OpRewrite,
			trace.OpTransmit,
			trace.OpDrop,
		}

		expected := []string{
			"classify",
			"filter",
			"learn",
			"lookup",
			"replicate",
			"rewrite",
			"transmit",
			"drop",
		}

		for i, op := range ops {
			if string(op) != expected[i] {
				t.Errorf("op[%d] = %q, want %q", i, op, expected[i])
			}
		}
	})
}

func TestSemanticEquality(t *testing.T) {
	t.Run("fact equality", func(t *testing.T) {
		f1 := testVLANFact{vid: 10}
		f2 := testVLANFact{vid: 10}
		f3 := testVLANFact{vid: 20}
		m1 := testMACFact{addr: "00:11:22:33:44:55"}

		if !trace.EqualFact(f1, f2) {
			t.Errorf("EqualFact(f1, f2) = false, want true")
		}
		if trace.EqualFact(f1, f3) {
			t.Errorf("EqualFact(f1, f3) = true, want false")
		}
		if trace.EqualFact(f1, m1) {
			t.Errorf("EqualFact(f1, m1) = true, want false")
		}
		if !trace.EqualFact(nil, nil) {
			t.Errorf("EqualFact(nil, nil) = false, want true")
		}
		if trace.EqualFact(f1, nil) || trace.EqualFact(nil, f1) {
			t.Errorf("EqualFact with one nil returned true, want false")
		}
	})

	t.Run("step equality and canonical input ordering", func(t *testing.T) {
		s1 := trace.Step{
			Layer:   "bridge",
			Op:      trace.OpLookup,
			RuleID:  "fdb-hit",
			Subject: trace.Subject{Kind: "port", Key: "1/1/2"},
			Inputs: []trace.Fact{
				testVLANFact{vid: 10},
				testMACFact{addr: "00:11:22:33:44:55"},
			},
			Outputs: []trace.Fact{
				testActionFact{action: "forward"},
			},
			Evidence: []trace.EvidenceRef{"ev-1", "ev-2"},
		}

		s2 := trace.Step{
			Layer:   "bridge",
			Op:      trace.OpLookup,
			RuleID:  "fdb-hit",
			Subject: trace.Subject{Kind: "port", Key: "1/1/2"},
			Inputs: []trace.Fact{
				testMACFact{addr: "00:11:22:33:44:55"},
				testVLANFact{vid: 10},
			},
			Outputs: []trace.Fact{
				testActionFact{action: "forward"},
			},
			Evidence: []trace.EvidenceRef{"ev-2", "ev-1"},
		}

		if !trace.EqualStep(s1, s2) {
			t.Errorf("EqualStep(s1, s2) = false, want true (canonical ordering should align inputs/evidence)")
		}
		if !s1.Equal(s2) {
			t.Errorf("s1.Equal(s2) = false, want true")
		}

		sDifferentOp := s1
		sDifferentOp.Op = trace.OpDrop
		if trace.EqualStep(s1, sDifferentOp) {
			t.Errorf("EqualStep with different Op returned true, want false")
		}

		sDifferentRule := s1
		sDifferentRule.RuleID = "other-rule"
		if trace.EqualStep(s1, sDifferentRule) {
			t.Errorf("EqualStep with different RuleID returned true, want false")
		}

		sDifferentSubject := s1
		sDifferentSubject.Subject = trace.Subject{Kind: "port", Key: "1/1/3"}
		if trace.EqualStep(s1, sDifferentSubject) {
			t.Errorf("EqualStep with different Subject returned true, want false")
		}
	})

	t.Run("trace equality", func(t *testing.T) {
		step := trace.Step{
			Layer:   "bridge",
			Op:      trace.OpLookup,
			RuleID:  "fdb-hit",
			Subject: trace.Subject{Kind: "port", Key: "1/1/2"},
			Outputs: []trace.Fact{testActionFact{action: "forward"}},
		}

		tr1 := trace.Trace{
			Steps:   []trace.Step{step},
			Outcome: trace.Forwarded,
			Reason:  "unicast-hit",
		}
		tr2 := trace.Trace{
			Steps:   []trace.Step{step},
			Outcome: trace.Forwarded,
			Reason:  "unicast-hit",
		}
		tr3 := trace.Trace{
			Steps:   []trace.Step{step},
			Outcome: trace.Dropped,
			Reason:  "acl-drop",
		}

		if !trace.Equal(tr1, tr2) || !tr1.Equal(tr2) {
			t.Errorf("Equal(tr1, tr2) = false, want true")
		}
		if trace.Equal(tr1, tr3) || tr1.Equal(tr3) {
			t.Errorf("Equal(tr1, tr3) = true, want false")
		}
	})

	t.Run("change equality", func(t *testing.T) {
		c1 := trace.Change{
			Layer:    "port",
			Subject:  trace.Subject{Kind: "port", Key: "1/1/1"},
			Field:    "admin_status",
			From:     testStatusFact{status: "Down"},
			To:       testStatusFact{status: "Up"},
			Evidence: []trace.EvidenceRef{"ev-1"},
		}
		c2 := trace.Change{
			Layer:    "port",
			Subject:  trace.Subject{Kind: "port", Key: "1/1/1"},
			Field:    "admin_status",
			From:     testStatusFact{status: "Down"},
			To:       testStatusFact{status: "Up"},
			Evidence: []trace.EvidenceRef{"ev-1"},
		}
		c3 := trace.Change{
			Layer:    "port",
			Subject:  trace.Subject{Kind: "port", Key: "1/1/1"},
			Field:    "admin_status",
			From:     testStatusFact{status: "Down"},
			To:       testStatusFact{status: "Down"},
			Evidence: []trace.EvidenceRef{"ev-1"},
		}

		if !trace.EqualChange(c1, c2) || !c1.Equal(c2) {
			t.Errorf("EqualChange(c1, c2) = false, want true")
		}
		if trace.EqualChange(c1, c3) || c1.Equal(c3) {
			t.Errorf("EqualChange(c1, c3) = true, want false")
		}
	})
}

func TestDeterministicRendering(t *testing.T) {
	t.Run("render step", func(t *testing.T) {
		step := trace.Step{
			Layer:   "bridge",
			Op:      trace.OpLookup,
			RuleID:  "fdb-miss",
			Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
			Inputs: []trace.Fact{
				testVLANFact{vid: 10},
				testMACFact{addr: "00:11:22:33:44:55"},
			},
			Outputs: []trace.Fact{
				testActionFact{action: "flood"},
			},
			Evidence: []trace.EvidenceRef{"ev-1", "ev-2"},
		}

		rendered := trace.RenderStep(step)
		want := "[bridge:lookup] rule=fdb-miss subject=port:1/1/1 in=[mac=00:11:22:33:44:55, vlan=10] out=[action=flood] evidence=[ev-1, ev-2]"
		if rendered != want {
			t.Errorf("RenderStep(step) =\n%q\nwant:\n%q", rendered, want)
		}
		if step.String() != want {
			t.Errorf("step.String() = %q, want %q", step.String(), want)
		}
	})

	t.Run("render trace", func(t *testing.T) {
		tr := trace.Trace{
			Steps: []trace.Step{
				{
					Layer:   "vlan",
					Op:      trace.OpFilter,
					RuleID:  "ingress-admission",
					Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
					Inputs:  []trace.Fact{testVLANFact{vid: 10}},
				},
				{
					Layer:   "bridge",
					Op:      trace.OpLookup,
					RuleID:  "fdb-miss",
					Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
					Outputs: []trace.Fact{testActionFact{action: "flood"}},
				},
			},
			Outcome: trace.Flooded,
			Reason:  "fdb-miss",
		}

		rendered := trace.Render(tr)
		want := "1. [vlan:filter] rule=ingress-admission subject=port:1/1/1 in=[vlan=10]\n2. [bridge:lookup] rule=fdb-miss subject=port:1/1/1 out=[action=flood]\nOutcome: Flooded (fdb-miss)"
		if rendered != want {
			t.Errorf("Render(tr) =\n%q\nwant:\n%q", rendered, want)
		}
		if tr.String() != want {
			t.Errorf("tr.String() =\n%q\nwant:\n%q", tr.String(), want)
		}
	})

	t.Run("render change", func(t *testing.T) {
		change := trace.Change{
			Layer:    "port",
			Subject:  trace.Subject{Kind: "port", Key: "1/1/1"},
			Field:    "admin_status",
			From:     testStatusFact{status: "Down"},
			To:       testStatusFact{status: "Up"},
			Evidence: []trace.EvidenceRef{"ev-1"},
		}

		rendered := trace.RenderChange(change)
		want := "[port] port:1/1/1 admin_status: Down -> Up (evidence: ev-1)"
		if rendered != want {
			t.Errorf("RenderChange(change) = %q, want %q", rendered, want)
		}
		if change.String() != want {
			t.Errorf("change.String() = %q, want %q", change.String(), want)
		}
	})

	t.Run("render changes", func(t *testing.T) {
		changes := []trace.Change{
			{
				Layer:   "port",
				Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
				Field:   "admin_status",
				From:    testStatusFact{status: "Down"},
				To:      testStatusFact{status: "Up"},
			},
			{
				Layer:   "port",
				Subject: trace.Subject{Kind: "port", Key: "1/1/2"},
				Field:   "admin_status",
				From:    nil,
				To:      testStatusFact{status: "Up"},
			},
		}

		rendered := trace.RenderChanges(changes)
		want := "[port] port:1/1/1 admin_status: Down -> Up\n[port] port:1/1/2 admin_status: <nil> -> Up"
		if rendered != want {
			t.Errorf("RenderChanges(changes) =\n%q\nwant:\n%q", rendered, want)
		}
	})
}

func TestTypedChanges(t *testing.T) {
	t.Run("typed from and to facts", func(t *testing.T) {
		addChange := trace.Change{
			Layer:   "port",
			Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
			Field:   "vlan",
			From:    nil,
			To:      testVLANFact{vid: 10},
		}
		if addChange.From != nil {
			t.Errorf("addChange.From = %v, want nil", addChange.From)
		}
		if !trace.EqualFact(addChange.To, testVLANFact{vid: 10}) {
			t.Errorf("addChange.To = %v, want vlan=10", addChange.To)
		}

		delChange := trace.Change{
			Layer:   "port",
			Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
			Field:   "vlan",
			From:    testVLANFact{vid: 10},
			To:      nil,
		}
		if !trace.EqualFact(delChange.From, testVLANFact{vid: 10}) {
			t.Errorf("delChange.From = %v, want vlan=10", delChange.From)
		}
		if delChange.To != nil {
			t.Errorf("delChange.To = %v, want nil", delChange.To)
		}
	})

	t.Run("canonical sorting of changes", func(t *testing.T) {
		changes := []trace.Change{
			{
				Layer:   "vlan",
				Subject: trace.Subject{Kind: "vlan", Key: "20"},
				Field:   "name",
				From:    nil,
				To:      testStatusFact{status: "engineering"},
			},
			{
				Layer:   "port",
				Subject: trace.Subject{Kind: "port", Key: "1/1/2"},
				Field:   "admin_status",
				From:    testStatusFact{status: "Down"},
				To:      testStatusFact{status: "Up"},
			},
			{
				Layer:   "port",
				Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
				Field:   "admin_status",
				From:    testStatusFact{status: "Down"},
				To:      testStatusFact{status: "Up"},
			},
			{
				Layer:   "bridge",
				Subject: trace.Subject{Kind: "bridge", Key: "br0"},
				Field:   "aging_time",
				From:    nil,
				To:      testStatusFact{status: "300"},
			},
		}

		trace.SortChanges(changes)

		expectedOrder := []string{
			"bridge:bridge:br0:aging_time",
			"port:port:1/1/1:admin_status",
			"port:port:1/1/2:admin_status",
			"vlan:vlan:20:name",
		}

		for i, c := range changes {
			key := fmt.Sprintf("%s:%s:%s", c.Layer, c.Subject.String(), c.Field)
			if key != expectedOrder[i] {
				t.Errorf("changes[%d] = %q, want %q", i, key, expectedOrder[i])
			}
		}
	})
}

func TestOpaqueEvidenceLinks(t *testing.T) {
	ref1 := trace.EvidenceRef("obs:telemetry:counter:port:1/1/1")
	ref2 := trace.EvidenceRef("config:switchport:1/1/1:vlan:10")

	step := trace.Step{
		Layer:    "port",
		Op:       trace.OpFilter,
		RuleID:   "admission",
		Subject:  trace.Subject{Kind: "port", Key: "1/1/1"},
		Evidence: []trace.EvidenceRef{ref2, ref1, ref2}, // unordered and duplicate
	}

	canon := step.Canonical()
	if len(canon.Evidence) != 2 {
		t.Fatalf("len(canon.Evidence) = %d, want 2 (deduplicated)", len(canon.Evidence))
	}
	if canon.Evidence[0] != ref2 || canon.Evidence[1] != ref1 {
		// "config:..." is lexicographically before "obs:..."
		if canon.Evidence[0] != ref2 {
			t.Errorf("canon.Evidence[0] = %q, want %q", canon.Evidence[0], ref2)
		}
	}

	stepDifferentEvidence := step
	stepDifferentEvidence.Evidence = []trace.EvidenceRef{"obs:other"}
	if trace.EqualStep(step, stepDifferentEvidence) {
		t.Errorf("EqualStep with different evidence = true, want false")
	}
}

func TestUnknownRuleIDs(t *testing.T) {
	unknownRule := trace.RuleID("vendor-proprietary-extension:rate-limit-rule-42")

	step := trace.Step{
		Layer:   "traffic",
		Op:      trace.OpDrop,
		RuleID:  unknownRule,
		Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
		Outputs: []trace.Fact{testActionFact{action: "rate-limit-drop"}},
	}

	if step.RuleID != unknownRule {
		t.Errorf("step.RuleID = %q, want %q", step.RuleID, unknownRule)
	}

	rendered := trace.RenderStep(step)
	want := "[traffic:drop] rule=vendor-proprietary-extension:rate-limit-rule-42 subject=port:1/1/1 out=[action=rate-limit-drop]"
	if rendered != want {
		t.Errorf("RenderStep with unknown RuleID =\n%q\nwant:\n%q", rendered, want)
	}

	stepSame := step
	if !trace.EqualStep(step, stepSame) {
		t.Errorf("EqualStep with unknown rule = false, want true")
	}
}

func TestRenderingUsesOnlyTheFactContract(t *testing.T) {
	fact := misleadingStringFact{}
	step := trace.Step{
		Layer:  "test",
		Op:     trace.OpLookup,
		Inputs: []trace.Fact{fact},
	}
	if got, want := trace.RenderStep(step), "[test:lookup] in=[stable=canonical]"; got != want {
		t.Errorf("RenderStep = %q, want %q", got, want)
	}

	change := trace.Change{Layer: "test", Field: "value", From: fact}
	if got, want := trace.RenderChange(change), "[test] value: canonical -> <nil>"; got != want {
		t.Errorf("RenderChange = %q, want %q", got, want)
	}
}

func TestZeroValueBehavior(t *testing.T) {
	t.Run("zero step", func(t *testing.T) {
		var s trace.Step
		canon := s.Canonical()
		if !canon.Equal(s) {
			t.Errorf("s.Canonical().Equal(s) = false, want true")
		}

		if rendered := trace.RenderStep(s); rendered != "[unspecified]" {
			t.Errorf("RenderStep(zero) = %q, want %q", rendered, "[unspecified]")
		}
		if s.String() != "[unspecified]" {
			t.Errorf("s.String() = %q, want %q", s.String(), "[unspecified]")
		}
		if !trace.EqualStep(s, trace.Step{}) {
			t.Errorf("EqualStep(s, Step{}) = false, want true")
		}
	})

	t.Run("zero trace", func(t *testing.T) {
		var tr trace.Trace
		canon := tr.Canonical()
		if !canon.Equal(tr) {
			t.Errorf("tr.Canonical().Equal(tr) = false, want true")
		}

		if rendered := trace.Render(tr); rendered != "Outcome: unspecified" {
			t.Errorf("Render(zero) = %q, want %q", rendered, "Outcome: unspecified")
		}
		if tr.String() != "Outcome: unspecified" {
			t.Errorf("tr.String() = %q, want %q", tr.String(), "Outcome: unspecified")
		}
		if !trace.Equal(tr, trace.Trace{}) {
			t.Errorf("Equal(tr, Trace{}) = false, want true")
		}
	})

	t.Run("zero change", func(t *testing.T) {
		var c trace.Change
		canon := c.Canonical()
		if !canon.Equal(c) {
			t.Errorf("c.Canonical().Equal(c) = false, want true")
		}

		if rendered := trace.RenderChange(c); rendered != "[unspecified]" {
			t.Errorf("RenderChange(zero) = %q, want %q", rendered, "[unspecified]")
		}
		if c.String() != "[unspecified]" {
			t.Errorf("c.String() = %q, want %q", c.String(), "[unspecified]")
		}
		if !trace.EqualChange(c, trace.Change{}) {
			t.Errorf("EqualChange(c, Change{}) = false, want true")
		}
	})

	t.Run("zero subject", func(t *testing.T) {
		var sub trace.Subject
		if s := sub.String(); s != "" {
			t.Errorf("zero subject String() = %q, want empty", s)
		}
		if sub.Compare(trace.Subject{}) != 0 {
			t.Errorf("zero subject Compare = %d, want 0", sub.Compare(trace.Subject{}))
		}
	})
}
