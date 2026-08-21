package conformance

import (
	"strings"
	"testing"
)

// TestValidateRowBites proves the gate predicate rejects each
// malformed shape and accepts the well-formed ones — the corpus
// scenario "integrity gate fails on covered row without citing
// marker, empty adversarial input" made independent of any real
// corpus.
func TestValidateRowBites(t *testing.T) {
	allow := map[string]bool{"ok-risk": true}
	tests := []struct {
		name      string
		row       Row
		hasMarker bool
		wantErr   string
	}{
		{
			name:    "covered without marker",
			row:     Row{ID: "x", Status: Covered, Adversarial: "input"},
			wantErr: "no `// Covers conformance matrix row: x` marker",
		},
		{
			name:      "covered without adversarial input",
			row:       Row{ID: "x", Status: Covered},
			hasMarker: true,
			wantErr:   "catalogs no adversarial input",
		},
		{
			name:    "accepted-risk without rationale",
			row:     Row{ID: "ok-risk", Status: AcceptedRisk},
			wantErr: "no rationale",
		},
		{
			name:    "accepted-risk off allowlist",
			row:     Row{ID: "rogue", Status: AcceptedRisk, Accepted: "because"},
			wantErr: "not on the allowlist",
		},
		{
			name:    "unknown status",
			row:     Row{ID: "x", Status: "done"},
			wantErr: "unknown status",
		},
		{
			name:      "well-formed covered",
			row:       Row{ID: "x", Status: Covered, Adversarial: "input"},
			hasMarker: true,
		},
		{
			name: "well-formed accepted-risk",
			row:  Row{ID: "ok-risk", Status: AcceptedRisk, Accepted: "because"},
		},
		{
			name: "pending allowed by integrity",
			row:  Row{ID: "x", Status: Pending},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRow(tc.row, tc.hasMarker, allow)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRow() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ValidateRow() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestRunCompleteForbidsPending covers the deleted-row/pending shape
// of the merge gate.
func TestRunCompleteForbidsPending(t *testing.T) {
	inner := &testing.T{}
	RunComplete(inner, []Row{{ID: "p", Status: Pending, Unit: "U10"}})
	if !inner.Failed() {
		t.Fatal("RunComplete accepted a pending row")
	}
	clean := &testing.T{}
	RunComplete(clean, []Row{{ID: "c", Status: Covered}})
	if clean.Failed() {
		t.Fatal("RunComplete rejected a fully-covered corpus")
	}
}
