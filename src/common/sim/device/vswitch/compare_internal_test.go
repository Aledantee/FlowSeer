package vswitch

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestCompareDetectsPCPDifference(t *testing.T) {
	t.Parallel()

	resA := ForwardResult{
		Result: bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Forwarded,
			},
			Egress: []bridge.Egress{{Port: "1/1/2", PCP: 0}},
		},
		Metadata: analysis.Metadata{},
	}
	resB := ForwardResult{
		Result: bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Forwarded,
			},
			Egress: []bridge.Egress{{Port: "1/1/2", PCP: 7}},
		},
		Metadata: analysis.Metadata{},
	}

	cmp := compareResults(resA, resB)
	if cmp.Disposition != analysis.Different {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Different)
	}
	if cmp.Difference.Observable != "pcp" {
		t.Fatalf("Difference.Observable = %q, want %q", cmp.Difference.Observable, "pcp")
	}
}

func TestCompareTracesDoNotDriveDisposition(t *testing.T) {
	t.Parallel()

	resA := ForwardResult{
		Result: bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Flooded,
			},
		},
		Metadata: analysis.Metadata{},
	}
	resB := ForwardResult{
		Result: bridge.Result{
			Trace: trace.Trace{
				Outcome: trace.Flooded,
				Steps: []trace.Step{
					{
						Layer:  trace.Layer("bridge"),
						Op:     trace.OpLookup,
						RuleID: "trace.noop_diagnostic",
					},
				},
			},
		},
		Metadata: analysis.Metadata{},
	}

	cmp := compareResults(resA, resB)
	if trace.Equal(cmp.Current.Trace, cmp.Expected.Trace) {
		t.Fatalf("traces are equal, want different diagnostic steps")
	}
	if cmp.Disposition != analysis.Equivalent {
		t.Fatalf("Disposition = %v, want %v", cmp.Disposition, analysis.Equivalent)
	}
	if cmp.Difference.Observable != "" {
		t.Errorf("Difference.Observable = %q, want empty", cmp.Difference.Observable)
	}
}
