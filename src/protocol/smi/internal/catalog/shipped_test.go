package catalog_test

import (
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi/internal/catalog"
)

// updateShipped rewrites the golden after a row is appended. Run with
//
//	go test ./src/protocol/smi/internal/catalog -run TestShippedCodes -update-shipped
//
// which is one added line per new code and nothing else. A diff that
// removes a line is the thing this test exists to catch.
var updateShipped = flag.Bool("update-shipped", false,
	"rewrite testdata/shipped-codes.txt from the current table")

var shippedPath = filepath.Join("testdata", "shipped-codes.txt")

// TestShippedCodes enforces the table's append-only rule.
//
// Every other test of the table reads the table, so they all agree with
// whatever it currently says: a code deleted from it and from the
// generated constants leaves them green. A code is the identity callers
// match diagnostics on and store against, so the golden is a second
// record that a removal has to argue with. It only ever fails one way —
// appending rows stays free.
func TestShippedCodes(t *testing.T) {
	entries := catalog.Entries()
	current := make([]string, len(entries))
	for i, e := range entries {
		current[i] = e.Code
	}
	slices.Sort(current)

	if *updateShipped {
		body := strings.Join(current, "\n") + "\n"
		if err := os.WriteFile(shippedPath, []byte(body), 0o644); err != nil {
			t.Fatalf("rewriting %s: %v", shippedPath, err)
		}
		t.Logf("rewrote %s with %d codes", shippedPath, len(current))

		return
	}

	shipped := readShipped(t)

	var missing []string
	for _, code := range shipped {
		if !slices.Contains(current, code) {
			missing = append(missing, code)
		}
	}

	if len(missing) > 0 {
		t.Errorf("%s lists %d code(s) the table no longer has: %s\n"+
			"A shipped code may not be removed or renamed. Restore the row, or add the "+
			"replacement alongside it and leave the old one in place.",
			shippedPath, len(missing), strings.Join(missing, ", "))
	}
}

// TestShippedGoldenIsSorted keeps the golden in the one order that makes
// an appended code a single added line.
func TestShippedGoldenIsSorted(t *testing.T) {
	if !slices.IsSorted(readShipped(t)) {
		t.Errorf("%s is not sorted; rerun with -update-shipped", shippedPath)
	}
}

func readShipped(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(shippedPath)
	if err != nil {
		t.Fatalf("reading %s: %v", shippedPath, err)
	}

	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(raw)), "\n") {
		if code := strings.TrimSpace(line); code != "" {
			out = append(out, code)
		}
	}

	return out
}
