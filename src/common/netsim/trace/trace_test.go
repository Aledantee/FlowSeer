package trace_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

const (
	testLayerBridge trace.Layer  = "bridge"
	testReasonMiss  trace.Reason = "fdb-miss"
)

func TestTraceVocabulary(t *testing.T) {
	t.Run("outcomes and aliases", func(t *testing.T) {
		if trace.Forwarded != trace.OutcomeForwarded {
			t.Errorf("Forwarded = %q, want %q", trace.Forwarded, trace.OutcomeForwarded)
		}
		if trace.OutcomeForwarded != "Forwarded" {
			t.Errorf("OutcomeForwarded = %q, want %q", trace.OutcomeForwarded, "Forwarded")
		}
		if trace.Flooded != trace.OutcomeFlooded {
			t.Errorf("Flooded = %q, want %q", trace.Flooded, trace.OutcomeFlooded)
		}
		if trace.OutcomeFlooded != "Flooded" {
			t.Errorf("OutcomeFlooded = %q, want %q", trace.OutcomeFlooded, "Flooded")
		}
		if trace.Dropped != trace.OutcomeDropped {
			t.Errorf("Dropped = %q, want %q", trace.Dropped, trace.OutcomeDropped)
		}
		if trace.OutcomeDropped != "Dropped" {
			t.Errorf("OutcomeDropped = %q, want %q", trace.OutcomeDropped, "Dropped")
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

	t.Run("step and trace construction", func(t *testing.T) {
		step := trace.Step{
			Layer:  testLayerBridge,
			Op:     trace.OpLookup,
			Detail: "unicast miss",
		}

		if step.Operation() != trace.OpLookup {
			t.Errorf("step.Operation() = %q, want %q", step.Operation(), trace.OpLookup)
		}

		tr := trace.Trace{
			Steps:   []trace.Step{step},
			Outcome: trace.Flooded,
			Reason:  testReasonMiss,
		}

		if len(tr.Steps) != 1 {
			t.Fatalf("len(tr.Steps) = %d, want 1", len(tr.Steps))
		}
		if tr.Outcome != trace.OutcomeFlooded {
			t.Errorf("tr.Outcome = %q, want %q", tr.Outcome, trace.OutcomeFlooded)
		}
		if tr.Reason != testReasonMiss {
			t.Errorf("tr.Reason = %q, want %q", tr.Reason, testReasonMiss)
		}
	})

	t.Run("change and subject", func(t *testing.T) {
		change := trace.Change{
			Layer: testLayerBridge,
			Subject: trace.Subject{
				Kind: "port",
				Key:  "1/1/1",
			},
			Field: "admin_status",
			From:  "Down",
			To:    "Up",
		}

		if change.Subject.Kind != "port" || change.Subject.Key != "1/1/1" {
			t.Errorf("change.Subject = %+v, want port:1/1/1", change.Subject)
		}
		if change.Field != "admin_status" || change.From != "Down" || change.To != "Up" {
			t.Errorf("unexpected change fields: %+v", change)
		}
	})
}
