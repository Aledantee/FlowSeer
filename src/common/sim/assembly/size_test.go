package assembly_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	maxFileLines = 1200
	maxFuncLines = 150
)

// oneUnitReason is what counts as a stated reason to exceed maxFuncLines. The
// doc comment of the declaration must say "one unit because" followed by at
// least three more words, so a bare "one unit" does not pass.
var oneUnitReason = regexp.MustCompile(`(?i)\bone unit because\s+\S+(\s+\S+){2,}`)

// sizeFindings reports every limit the named source exceeds: its line count,
// each function declaration, and each package-level function literal, the last
// two unless their doc comment states a reason under oneUnitReason.
func sizeFindings(name string, source []byte) ([]string, error) {
	var findings []string
	lines := strings.Count(string(source), "\n")
	if len(source) > 0 && source[len(source)-1] != '\n' {
		lines++
	}
	if lines > maxFileLines {
		findings = append(findings, fmt.Sprintf("%s: %d lines, limit %d", name, lines, maxFileLines))
	}
	positions := token.NewFileSet()
	file, err := parser.ParseFile(positions, name, source, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	check := func(label string, node ast.Node, doc *ast.CommentGroup) {
		length := positions.Position(node.End()).Line - positions.Position(node.Pos()).Line + 1
		if length > maxFuncLines && (doc == nil || !oneUnitReason.MatchString(doc.Text())) {
			findings = append(findings, fmt.Sprintf("%s:%d: %s has %d lines, limit %d without a stated reason to keep one unit", name, positions.Position(node.Pos()).Line, label, length, maxFuncLines))
		}
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			check(decl.Name.Name, decl, decl.Doc)
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, expr := range value.Values {
					if literal, ok := expr.(*ast.FuncLit); ok {
						check(value.Names[0].Name, literal, decl.Doc)
					}
				}
			}
		}
	}
	return findings, nil
}

func TestSourceSizeLimits(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		findings, err := sizeFindings(name, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, finding := range findings {
			t.Error(finding)
		}
	}
	if checked == 0 {
		t.Fatal("no production source checked")
	}
}

// TestSizeGateFiresOnFixtureSources runs the gate over sources at and just past
// each limit. The fixture sizes are written out rather than derived from the
// limits, so a limit raised or a check removed makes a fixture pass the gate.
func TestSizeGateFiresOnFixtureSources(t *testing.T) {
	body := func(n int) string { return strings.Repeat("\t_ = 0\n", n) }
	function := func(doc string, bodyLines int) string {
		// The declaration spans bodyLines plus its signature and closing brace.
		return doc + "func f() {\n" + body(bodyLines) + "}\n"
	}
	literal := func(doc string, bodyLines int) string {
		return doc + "var f = func() {\n" + body(bodyLines) + "}\n"
	}
	reason := "// f is kept as one unit because its cases share one decoder state.\n"
	for _, tc := range []struct {
		name   string
		source string
		want   int
	}{
		{"file at the limit", "package p\n" + strings.Repeat("\n", 1199), 0},
		{"file over the limit", "package p\n" + strings.Repeat("\n", 1200), 1},
		{"function at the limit", "package p\n" + function("", 148), 0},
		{"function over the limit", "package p\n" + function("", 149), 1},
		{"function with a bare one unit", "package p\n// f is one unit.\n" + function("", 149), 1},
		{"function with a reason", "package p\n" + function(reason, 149), 0},
		{"literal at the limit", "package p\n" + literal("", 148), 0},
		{"literal over the limit", "package p\n" + literal("", 149), 1},
		{"literal with a bare one unit", "package p\n" + literal("// f is one unit.\n", 149), 1},
		{"literal with a reason", "package p\n" + literal(reason, 149), 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := sizeFindings("fixture.go", []byte(tc.source))
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != tc.want {
				t.Errorf("findings = %q, want %d", findings, tc.want)
			}
		})
	}
}
