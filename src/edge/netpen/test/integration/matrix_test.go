package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
)

// TestValidationMatrixCompleteness walks the catalog and asserts every
// (behavior, mode) pair appears in VALIDATION_MATRIX.md. This is the
// single-source guard: the catalog is the source of truth,
// and the matrix cannot drift from it.
//
// It checks the first two columns, including the explicit base mode, without
// treating prose elsewhere in the file as evidence for a missing matrix row.
func TestValidationMatrixCompleteness(t *testing.T) {
	matrixPath := filepath.Join(".", "VALIDATION_MATRIX.md")
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read %s: %v", matrixPath, err)
	}
	matrix := string(data)

	entries := catalog.Entries()
	for _, e := range entries {
		mode := e.Mode
		if mode == "" {
			mode = "base"
		}
		prefix := "| " + e.Name + " | " + mode + " |"
		if !strings.Contains(matrix, "\n"+prefix) {
			t.Errorf("behavior %q mode %q missing from VALIDATION_MATRIX.md", e.Name, mode)
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
	_, section, ok := strings.Cut(matrix, "\n## AE6 superset attacks (R4)\n")
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
