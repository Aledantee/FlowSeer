// Package conformance joins protocol quirks to their citing tests and renders
// CONFORMANCE.md. A test cites a row with a comment such as:
//
//	// Covers conformance matrix row: gn-proto-only-encoding
//
// [RunIntegrity] checks row consistency and citations. [RunComplete] additionally
// rejects pending rows when called by a protocol's build-tagged completeness test.
package conformance

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// Status records whether a row has test coverage or an accepted gap. Its zero
// value is invalid; [ValidateRow] rejects values outside the declared constants.
type Status string

const (
	// Pending allows incomplete coverage in [RunIntegrity] but fails [RunComplete].
	Pending Status = "pending"
	// Covered requires a citing test marker and a recorded adversarial input.
	Covered Status = "covered"
	// AcceptedRisk requires a rationale and an explicit allowlist entry.
	AcceptedRisk Status = "accepted-risk"
)

// Row records a protocol quirk, its source, and the coverage expected from the
// library's tests. The zero value is invalid. Callers may share rows for concurrent
// reads but must not mutate them while a validation or rendering call uses them.
type Row struct {
	// ID is the stable kebab-case key cited by test markers.
	ID string
	// Clause identifies the RFC clause or vendor contract the row enforces.
	Clause string
	// Provenance identifies the documentation or hardware observation behind the quirk.
	Provenance string
	// Behavior states the expected response, including any tolerance for invalid input.
	Behavior string
	// Adversarial describes the input exercised by the citing test; [Covered] requires it.
	Adversarial string
	// Unit identifies the component responsible for coverage in pending-row errors.
	Unit string
	// Status selects the coverage requirements enforced by [ValidateRow].
	Status Status
	// Accepted explains why test coverage is absent; [AcceptedRisk] requires it.
	Accepted string
}

// Family groups rows by ID prefix for CONFORMANCE.md rendering. The zero value
// matches every row with an empty heading. Callers may share families for concurrent
// reads but must not mutate them during validation or rendering.
type Family struct {
	// Prefix selects rows by a case-sensitive prefix of [Row.ID]; empty matches all rows.
	Prefix string
	// Title is the section heading in CONFORMANCE.md.
	Title string
}

// ValidateRow returns an error when r's status requirements are unmet or its
// status is unknown. hasMarker reports whether a test cites r.ID. A nil allowlist
// permits no [AcceptedRisk] rows; only entries set to true grant acceptance.
func ValidateRow(r Row, hasMarker bool, allowlist map[string]bool) error {
	switch r.Status {
	case Covered:
		if !hasMarker {
			return fmt.Errorf("row %q is covered but no `// Covers conformance matrix row: %s` marker was found", r.ID, r.ID)
		}
		if strings.TrimSpace(r.Adversarial) == "" {
			return fmt.Errorf("row %q is covered but catalogs no adversarial input", r.ID)
		}
	case AcceptedRisk:
		if strings.TrimSpace(r.Accepted) == "" {
			return fmt.Errorf("row %q is accepted-risk but has no rationale", r.ID)
		}
		if !allowlist[r.ID] {
			return fmt.Errorf("row %q is accepted-risk but not on the allowlist (add it as a reviewable diff)", r.ID)
		}
	case Pending:
		// Allowed here; the completeness gate forbids it.
	default:
		return fmt.Errorf("row %q has unknown status %q", r.ID, r.Status)
	}
	return nil
}

var markerRe = regexp.MustCompile(`Covers conformance matrix row:\s*([A-Za-z0-9\-]+)(?:\s|$)`)

// CollectMarkers returns the known row IDs cited in comments in each directory's
// _test.go files. It does not recurse or filter by build tags. Marker IDs are
// whitespace-delimited tokens matched in full; unknown IDs are ignored. Directory
// and Go parsing errors fail t immediately.
func CollectMarkers(t *testing.T, rows []Row, dirs []string) map[string]bool {
	t.Helper()
	known := make(map[string]bool, len(rows))
	for _, r := range rows {
		known[r.ID] = true
	}
	found := make(map[string]bool)
	fset := token.NewFileSet()
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != dir {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments|parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			for _, cg := range f.Comments {
				for _, m := range markerRe.FindAllStringSubmatch(cg.Text(), -1) {
					if id := strings.TrimSpace(m[1]); known[id] {
						found[id] = true
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", dir, err)
		}
	}
	return found
}

// CorpusDirs returns the directories [RunIntegrity] scans for citation markers:
// the directory of the file that calls it, plus that directory's
// test/integration subdirectory. It resolves the path from its immediate
// caller's source location, so it must be called directly from a test file in
// the package whose corpus is being checked, never through another helper.
func CorpusDirs(t *testing.T) []string {
	t.Helper()
	_, here, _, ok := runtime.Caller(1)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(here)
	return []string{dir, filepath.Join(dir, "test", "integration")}
}

// RunIntegrity reports duplicate IDs, unmet status requirements, and rows without
// a matching family through t. It collects citations from dirs with [CollectMarkers],
// whose directory and parsing errors fail t immediately. Pending rows are allowed.
func RunIntegrity(t *testing.T, rows []Row, allowlist map[string]bool, families []Family, dirs []string) {
	t.Helper()
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		if seen[r.ID] {
			t.Errorf("duplicate corpus row id %q", r.ID)
		}
		seen[r.ID] = true
	}
	markers := CollectMarkers(t, rows, dirs)
	for _, r := range rows {
		if err := ValidateRow(r, markers[r.ID], allowlist); err != nil {
			t.Error(err)
		}
		inFamily := false
		for _, fam := range families {
			if strings.HasPrefix(r.ID, fam.Prefix) {
				inFamily = true
				break
			}
		}
		if !inFamily {
			t.Errorf("row %q matches no family prefix — it would vanish from CONFORMANCE.md", r.ID)
		}
	}
}

// RunComplete reports pending rows through t, including their owning units.
// Call it alongside [RunIntegrity], which checks all other status requirements.
func RunComplete(t *testing.T, rows []Row) {
	t.Helper()
	var pending []string
	for _, r := range rows {
		if r.Status == Pending {
			pending = append(pending, r.ID+" (owner "+r.Unit+")")
		}
	}
	if len(pending) > 0 {
		t.Errorf("%d corpus row(s) still pending — every row must be covered or allowlisted accepted-risk before merge:\n  %v",
			len(pending), pending)
	}
}

// Markdown renders rows as CONFORMANCE.md in the supplied family and row order.
// It flattens line breaks and escapes pipes in prose cells. Call [RunIntegrity]
// separately to validate the corpus; rows without a matching family are omitted
// from the tables. Concurrent calls are safe while inputs remain unchanged.
func Markdown(title string, rows []Row, families []Family) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s\n\n", title)
	b.WriteString("<!-- Generated from conformance_corpus_test.go; run the corpus golden test with -update-conformance to refresh. Do not edit by hand. -->\n\n")
	counts := map[Status]int{}
	for _, r := range rows {
		counts[r.Status]++
	}
	fmt.Fprintf(&b, "Rows: %d covered, %d accepted-risk, %d pending.\n\n",
		counts[Covered], counts[AcceptedRisk], counts[Pending])
	for _, fam := range families {
		fmt.Fprintf(&b, "## %s\n\n", fam.Title)
		b.WriteString("| ID | Clause | Provenance | Behavior | Status |\n")
		b.WriteString("| --- | --- | --- | --- | --- |\n")
		for _, r := range rows {
			if !strings.HasPrefix(r.ID, fam.Prefix) {
				continue
			}
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s | %s |\n",
				r.ID, cell(r.Clause), cell(r.Provenance), cell(r.Behavior), r.Status)
		}
		b.WriteString("\n")
	}
	return b.Bytes()
}

var cellReplacer = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "|", "\\|")

func cell(s string) string {
	return cellReplacer.Replace(s)
}

// VerifyMarkdown reports a test error if path's contents differ from content.
// With update, it creates or overwrites path instead. File errors fail t
// immediately. Callers must serialize updates to the same path.
func VerifyMarkdown(t *testing.T, path string, content []byte, update bool) {
	t.Helper()
	if update {
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		t.Logf("updated %s (%d bytes)", path, len(content))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (rerun with -update-conformance if first time): %v", path, err)
	}
	if !bytes.Equal(want, content) {
		t.Errorf("%s is stale — rerun the corpus golden test with -update-conformance", path)
	}
}
