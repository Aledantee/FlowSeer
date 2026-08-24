package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/netpen/catalog"
)

// TestValidationMatrixCompleteness walks the catalog and asserts every
// (behavior, mode) pair appears in VALIDATION_MATRIX.md. This is the
// single-source guard (KTD8 spirit): the catalog is the source of truth,
// and the matrix cannot drift from it.
//
// The test checks that each behavior name appears as the first column of
// a matrix row. Mode pairs are checked by looking for the mode string on
// the same line. This is deliberately a structural (not semantic) check:
// it catches drift (a new behavior added to the catalog but missing from
// the matrix) without coupling the test to the matrix's column layout.
func TestValidationMatrixCompleteness(t *testing.T) {
	matrixPath := filepath.Join(".", "VALIDATION_MATRIX.md")
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read %s: %v", matrixPath, err)
	}
	matrix := string(data)

	entries := catalog.Entries()
	for _, e := range entries {
		// Each behavior name must appear as the first field of a
		// table row (line starting with "| <name>").
		prefix := "| " + e.Name + " "
		if !strings.Contains(matrix, prefix) {
			t.Errorf("behavior %q missing from VALIDATION_MATRIX.md", e.Name)
			continue
		}
		// For mode-bearing entries, the mode must appear on the
		// same row. We check that a row with the behavior name and
		// the mode string exists.
		if e.Mode != "" {
			found := false
			for _, line := range strings.Split(matrix, "\n") {
				if strings.HasPrefix(line, prefix) && strings.Contains(line, "| "+e.Mode+" |") {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("behavior %q mode %q missing from VALIDATION_MATRIX.md", e.Name, e.Mode)
			}
		}
	}
}

// TestSupersetAttacksInMatrix asserts the eight R4 superset attacks
// all appear in the AE6 section of the matrix.
func TestSupersetAttacksInMatrix(t *testing.T) {
	matrixPath := filepath.Join(".", "VALIDATION_MATRIX.md")
	data, err := os.ReadFile(matrixPath)
	if err != nil {
		t.Fatalf("read %s: %v", matrixPath, err)
	}
	matrix := string(data)

	supersets := []string{"ospf", "eigrp", "wpad", "etherchannel", "mld", "raflood", "lldpspoof", "glbp"}
	for _, s := range supersets {
		if !strings.Contains(matrix, "| "+s+" |") {
			t.Errorf("superset attack %q missing from VALIDATION_MATRIX.md AE6 section", s)
		}
	}
}
