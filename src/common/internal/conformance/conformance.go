// Package conformance is the shared quirk-corpus machinery the YANG
// protocol libraries transcribe from the SNMP library's discipline
// (R13): append-only rows with provenance and adversarial input,
// `// Covers conformance matrix row: <ID>` markers joining rows to
// citing tests, an always-on integrity gate, a build-tagged
// completeness gate, and a generated CONFORMANCE.md golden.
//
// It lives under src/common/internal so the three libraries share one
// implementation while the package stays outside every public API
// surface.
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
	"strings"
	"testing"
)

// Status is a row's lifecycle state.
type Status string

// The row states. Pending is allowed by the always-on integrity gate
// (dev-branch friendliness) and forbidden by the build-tagged
// completeness gate.
const (
	Pending      Status = "pending"
	Covered      Status = "covered"
	AcceptedRisk Status = "accepted-risk"
)

// Row is one cataloged conformance quirk: a paid-for lesson from an
// RFC erratum, vendor documentation, or lab hardware, transcribed
// into the library's test suite.
type Row struct {
	ID          string // stable kebab-case id, also the marker join key
	Clause      string // RFC clause / vendor doc the row enforces
	Provenance  string // where the quirk was learned
	Behavior    string // expected behavior / tolerance policy
	Adversarial string // the adversarial input the pin drives (required when covered)
	Unit        string // owning plan implementation unit
	Status      Status
	Accepted    string // why-not-covered rationale (required when accepted-risk)
}

// Family groups rows by ID prefix for CONFORMANCE.md rendering.
type Family struct {
	Prefix string
	Title  string
}

// ValidateRow checks one row's internal consistency. Factored out so
// the gate's own tests can prove it bites.
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

var markerRe = regexp.MustCompile(`Covers conformance matrix row:\s*([A-Za-z0-9\-]+)`)

// CollectMarkers scans every _test.go under each dir (non-recursive
// per dir) for citing markers of known row ids.
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

// RunIntegrity is the always-on gate: duplicate ids, per-row
// consistency, and family membership.
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

// RunComplete is the build-tagged merge gate body: no pending rows.
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

// Markdown renders the corpus as the library's CONFORMANCE.md.
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

// cell escapes pipes for table cells.
func cell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

// VerifyMarkdown compares (or, with update, rewrites) the committed
// CONFORMANCE.md.
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
