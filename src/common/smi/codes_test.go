package smi_test

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi"
	"go.aledante.io/FlowSeer/src/common/smi/internal/catalog"
)

// The two generated files: the declarations the lexer, framer and parser
// raise, and package smi's re-export of the same names for a caller
// matching them.
const (
	generatedPath = "internal/diag/zz_generated_codes.go"
	aliasPath     = "zz_generated_codes.go"
)

// generatedFiles pairs each committed file with the renderer that owns
// it, so both drift gates cover both files.
var generatedFiles = []struct {
	path   string
	render func([]catalog.Entry) ([]byte, error)
}{
	{generatedPath, catalog.RenderCodes},
	{aliasPath, catalog.RenderAliases},
}

// The generated files are the only place a code literal and a code name
// exist, so a stale regeneration and a hand-edited constant are the same
// failure.
func TestGeneratedCodesAreCurrent(t *testing.T) {
	for _, gf := range generatedFiles {
		t.Run(gf.path, func(t *testing.T) {
			want, err := gf.render(catalog.Entries())
			if err != nil {
				t.Fatalf("rendering the catalog: %v", err)
			}

			got, err := os.ReadFile(gf.path)
			if err != nil {
				t.Fatalf("reading %s: %v", gf.path, err)
			}

			if !bytes.Equal(got, want) {
				t.Fatalf("%s does not match the catalog.\n"+
					"Run `go generate ./src/common/smi/...` and commit the result.", gf.path)
			}
		})
	}
}

// The drift gate is only worth having if it bites, so a row that differs
// from the committed table must render to different bytes.
func TestGeneratedCodesDriftIsDetected(t *testing.T) {
	rows := catalog.Entries()
	rows[0].Description = "something nobody wrote in the table"

	for _, gf := range generatedFiles {
		t.Run(gf.path, func(t *testing.T) {
			drifted, err := gf.render(rows)
			if err != nil {
				t.Fatalf("rendering the mutated catalog: %v", err)
			}

			committed, err := os.ReadFile(gf.path)
			if err != nil {
				t.Fatalf("reading %s: %v", gf.path, err)
			}

			if bytes.Equal(drifted, committed) {
				t.Error("a changed row rendered to the committed bytes, so the drift gate cannot bite")
			}
		})
	}
}

// errs.NewCode panics on a malformed or duplicate name, so every code
// reaching the process registry is proof the generated literal was
// accepted. The registry is the check; this test only proves the smi
// namespace is fully present in it.
func TestEveryCatalogCodeIsRegistered(t *testing.T) {
	registered := errs.Codes()

	for _, row := range catalog.Entries() {
		if !slices.Contains(registered, errs.Code(row.Code)) {
			t.Errorf("%s is in the catalog but not registered with errs; regenerate %s", row.Code, generatedPath)
		}
	}
}

// Every row must be raisable at its declared arity and must grade on the
// scale, which is what keeps the catalog and the diagnostic machinery
// from disagreeing about a row nothing has raised yet.
func TestEveryCatalogRowRaisesAndRenders(t *testing.T) {
	table := smi.NewLineTable([]byte("first\nsecond\n"))

	for _, row := range catalog.Entries() {
		t.Run(row.Code, func(t *testing.T) {
			args := make([]smi.Arg, row.Arity)
			for i := range args {
				args[i] = smi.ArgString("x")
			}

			d := smi.Raise(smi.Position{File: "T.mib", Offset: 6}, errs.Code(row.Code), args...)

			if got := d.Severity(); !got.Valid() {
				t.Errorf("severity %v is off the scale", got)
			}
			if got := d.Severity(); uint8(got) != row.Severity {
				t.Errorf("severity = %d, want the cataloged %d", uint8(got), row.Severity)
			}

			rendered := d.Render(table)
			if rendered.Line != 2 || rendered.Column != 1 {
				t.Errorf("rendered at %d:%d, want 2:1", rendered.Line, rendered.Column)
			}
			if rendered.Message == "" {
				t.Error("rendered an empty message")
			}
		})
	}
}
