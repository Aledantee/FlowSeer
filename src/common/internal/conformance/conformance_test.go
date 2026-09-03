package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRowStatusRequirements(t *testing.T) {
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

func TestCollectMarkersRequiresWholeID(t *testing.T) {
	tests := []struct {
		name   string
		marker string
		want   bool
	}{
		{name: "exact", marker: "Covers conformance matrix row: example-row", want: true},
		{name: "unknown suffix", marker: "Covers conformance matrix row: example-row-extra"},
		{name: "underscore suffix", marker: "Covers conformance matrix row: example-row_extra"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			content := "package example\n// " + tc.marker + "\nfunc TestExample() {}\n"
			if err := os.WriteFile(filepath.Join(dir, "example_test.go"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			markers := CollectMarkers(t, []Row{{ID: "example-row"}}, []string{dir})
			if got := markers["example-row"]; got != tc.want {
				t.Errorf("CollectMarkers()[example-row] = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMarkdownKeepsMultilineTextInCells(t *testing.T) {
	rows := []Row{{
		ID: "example-row", Status: Covered,
		Clause: "first\r\nsecond", Provenance: "a|b", Behavior: "one\ntwo\rthree",
	}}
	got := string(Markdown("Example", rows, []Family{{Prefix: "example-", Title: "Rows"}}))
	want := "| `example-row` | first second | a\\|b | one two three | covered |\n"
	if !strings.Contains(got, want) {
		t.Errorf("Markdown() = %q, want row %q", got, want)
	}
}

func TestRunCompleteForbidsPending(t *testing.T) {
	inner := &testing.T{}
	RunComplete(inner, []Row{{ID: "p", Status: Pending, Unit: "example/component"}})
	if !inner.Failed() {
		t.Fatal("RunComplete accepted a pending row")
	}
	clean := &testing.T{}
	RunComplete(clean, []Row{{ID: "c", Status: Covered}})
	if clean.Failed() {
		t.Fatal("RunComplete rejected a fully-covered corpus")
	}
}
