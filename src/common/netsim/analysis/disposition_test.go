package analysis_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
)

func TestDispositionValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		disp    analysis.Disposition
		wantErr bool
	}{
		{name: "equivalent", disp: analysis.Equivalent, wantErr: false},
		{name: "different", disp: analysis.Different, wantErr: false},
		{name: "inconclusive", disp: analysis.Inconclusive, wantErr: false},
		{name: "zero value", disp: analysis.Disposition(""), wantErr: true},
		{name: "unknown value", disp: analysis.Disposition("unknown"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.disp.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDispositionDistinctEncodings(t *testing.T) {
	t.Parallel()

	values := []analysis.Disposition{
		analysis.Equivalent,
		analysis.Different,
		analysis.Inconclusive,
	}

	seen := make(map[string]analysis.Disposition, len(values))
	for _, v := range values {
		str := v.String()
		if str == "" {
			t.Errorf("disposition %v String() is empty", v)
		}
		if prev, ok := seen[str]; ok {
			t.Errorf("disposition %v shares encoding %q with %v", v, str, prev)
		}
		seen[str] = v
	}
}
