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
	t.Run("outcomes", func(t *testing.T) {
		if trace.Forwarded != "Forwarded" || trace.Flooded != "Flooded" || trace.Dropped != "Dropped" {
			t.Errorf("outcomes = %q %q %q, want their names", trace.Forwarded, trace.Flooded, trace.Dropped)
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

		tr := trace.Trace{
			Steps:   []trace.Step{step},
			Outcome: trace.Flooded,
			Reason:  testReasonMiss,
		}

		if len(tr.Steps) != 1 {
			t.Fatalf("len(tr.Steps) = %d, want 1", len(tr.Steps))
		}
		if tr.Outcome != trace.Flooded {
			t.Errorf("tr.Outcome = %q, want %q", tr.Outcome, trace.Flooded)
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
