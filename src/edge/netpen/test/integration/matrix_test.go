package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
)

// matrixColumns is the header the matrix rows follow, in order.
const matrixColumns = "| Behavior | Mode | Impact | Restore | Affects | Observable | Fixture provenance | t1 AE6 | t2 |"

// TestValidationMatrixCompleteness walks the shipped catalog and asserts
// every (behavior, mode) pair appears in VALIDATION_MATRIX.md with the
// catalog's own impact, restore, and affects in the first five columns and
// a non-empty observable column. This is the single-source guard: the
// catalog is the source of truth, and the matrix cannot drift from it.
func TestValidationMatrixCompleteness(t *testing.T) {
	matrixPath := filepath.Join(".", "VALIDATION_MATRIX.md")
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read %s: %v", matrixPath, err)
	}
	matrix := string(data)
	if !strings.Contains(matrix, "\n"+matrixColumns+"\n") {
		t.Fatalf("VALIDATION_MATRIX.md lacks the header %q", matrixColumns)
	}

	for _, e := range catalog.GeneratedEntries {
		mode := e.Mode
		if mode == "" {
			mode = "base"
		}
		restore := string(e.Restore.Kind)
		if e.Restore.Bound > 0 {
			restore += " " + e.Restore.Bound.String()
		}
		prefix := "| " + e.Name + " | " + mode + " | " + string(e.Impact) + " | " + restore + " | " + string(e.Affects) + " | "
		start := strings.Index(matrix, "\n"+prefix)
		if start < 0 {
			t.Errorf("behavior %q mode %q missing from VALIDATION_MATRIX.md with its cost fields: want a row starting %q", e.Name, e.Mode, prefix)
			continue
		}
		row := matrix[start+1:]
		if end := strings.Index(row, "\n"); end >= 0 {
			row = row[:end]
		}
		cells := strings.Split(strings.Trim(row, "|"), "|")
		if len(cells) != 9 {
			t.Errorf("row for %q mode %q has %d cells, want 9: %s", e.Name, e.Mode, len(cells), row)
			continue
		}
		if strings.TrimSpace(cells[5]) == "" {
			t.Errorf("row for %q mode %q has an empty Observable column", e.Name, e.Mode)
		}
	}
}

// TestSupersetAttacksInMatrix asserts the eight superset attacks
// all appear in the superset-validation section of the matrix.
func TestSupersetAttacksInMatrix(t *testing.T) {
	matrixPath := filepath.Join(".", "VALIDATION_MATRIX.md")
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read %s: %v", matrixPath, err)
	}
	matrix := string(data)
	_, section, ok := strings.Cut(matrix, "\n## AE6 superset attacks\n")
	if !ok {
		t.Fatal("AE6 section missing from VALIDATION_MATRIX.md")
	}
	section, _, _ = strings.Cut(section, "\n## ")

	supersets := []string{"ospf", "eigrp", "wpad", "etherchannel", "mld", "raflood", "lldpspoof", "glbp"}
	for _, s := range supersets {
		if !strings.Contains(section, "\n| "+s+" |") {
			t.Errorf("superset attack %q missing from VALIDATION_MATRIX.md AE6 section", s)
		}
	}
}
