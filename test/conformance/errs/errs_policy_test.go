// Package conformance gates error construction across first-party Go source.
// Non-test code constructs errors only through src/common/errs.
package conformance

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const unqualified = "\x00unqualified"

// TestErrsPolicy is the gate. It walks first-party source under src/ and
// reports every errors.New and fmt.Errorf call outside src/common/errs.
//
// It parses files by path rather than importing them, so one test in the
// root module inspects the nested modules too (src/edge/netpen, the bench
// modules), which a root `go test ./...` never builds.
func TestErrsPolicy(t *testing.T) {
	root := repoRoot(t)
	srcRoot := filepath.Join(root, "src")
	errsDir := filepath.Join(srcRoot, "common", "errs")

	scanned := 0
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || path == errsDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "src/common/errs" || strings.HasPrefix(rel, "src/common/errs/") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := goparser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		scanned++

		for _, finding := range fileViolations(fset, file, rel) {
			t.Error(finding)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", srcRoot, err)
	}

	if scanned == 0 {
		t.Fatal("scanned no source files; the gate checked nothing")
	}
	t.Logf("scanned %d source files", scanned)
}

func TestErrsPolicyTable(t *testing.T) {
	tests := []struct {
		name         string
		fixtureFile  string
		wantFindings []string
	}{
		{
			name:         "errs.Msg is accepted",
			fixtureFile:  "msg.go",
			wantFindings: nil,
		},
		{
			name:        "fmt.Errorf is rejected",
			fixtureFile: "direct.go",
			wantFindings: []string{
				"testdata/direct.go:6: fmt.Errorf outside src/common/errs",
			},
		},
		{
			name:        "f.Errorf is rejected where the file reads import f \"fmt\"",
			fixtureFile: "aliased.go",
			wantFindings: []string{
				"testdata/aliased.go:6: fmt.Errorf outside src/common/errs",
			},
		},
		{
			name:        "a bare Errorf is rejected in a file that dot-imports fmt",
			fixtureFile: "dotimport.go",
			wantFindings: []string{
				"testdata/dotimport.go:6: fmt.Errorf outside src/common/errs",
			},
		},
		{
			name:         "an Errorf method on an unrelated receiver is accepted",
			fixtureFile:  "receiver.go",
			wantFindings: nil,
		},
		{
			name:        "errors.New is rejected",
			fixtureFile: "errors_direct.go",
			wantFindings: []string{
				"testdata/errors_direct.go:6: errors.New outside src/common/errs",
			},
		},
		{
			name:        "aliased errors.New is rejected",
			fixtureFile: "errors_aliased.go",
			wantFindings: []string{
				"testdata/errors_aliased.go:6: errors.New outside src/common/errs",
			},
		},
		{
			name:        "dot-imported errors.New is rejected",
			fixtureFile: "errors_dotimport.go",
			wantFindings: []string{
				"testdata/errors_dotimport.go:6: errors.New outside src/common/errs",
			},
		},
		{
			name:         "errs.New is accepted",
			fixtureFile:  "errs_builder.go",
			wantFindings: nil,
		},
	}

	testdataDir := filepath.Join(repoRoot(t), "test", "conformance", "errs", "testdata")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(testdataDir, tt.fixtureFile)
			fset := token.NewFileSet()
			file, err := goparser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}

			rel := filepath.ToSlash(filepath.Join("testdata", tt.fixtureFile))
			got := fileViolations(fset, file, rel)
			if !slices.Equal(got, tt.wantFindings) {
				t.Errorf("got findings %v, want %v", got, tt.wantFindings)
			}
		})
	}
}

// fileViolations reports every errors.New and fmt.Errorf call in file,
// resolved through imports rather than bare qualifiers.
func fileViolations(fset *token.FileSet, file *ast.File, rel string) []string {
	fmtQualifiers := importQualifiers(file, "fmt", "fmt")
	errorsQualifiers := importQualifiers(file, "errors", "errors")

	if len(fmtQualifiers) == 0 && len(errorsQualifiers) == 0 {
		return nil
	}

	var findings []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		banned := bannedCall(call, fmtQualifiers, errorsQualifiers)
		if banned != "" {
			findings = append(findings, rel+":"+line(fset, n)+": "+banned+" outside src/common/errs")
		}

		return true
	})

	return findings
}

func importQualifiers(file *ast.File, importPath, defaultName string) map[string]struct{} {
	qualifiers := make(map[string]struct{})
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != importPath {
			continue
		}

		name := defaultName
		if spec.Name != nil {
			name = spec.Name.Name
		}

		if name == "_" {
			continue
		}
		if name == "." {
			name = unqualified
		}

		qualifiers[name] = struct{}{}
	}

	return qualifiers
}

func bannedCall(call *ast.CallExpr, fmtQualifiers, errorsQualifiers map[string]struct{}) string {
	fun := call.Fun
	for {
		p, ok := fun.(*ast.ParenExpr)
		if !ok {
			break
		}
		fun = p.X
	}

	switch f := fun.(type) {
	case *ast.Ident:
		if f.Name == "Errorf" {
			if _, ok := fmtQualifiers[unqualified]; ok {
				return "fmt.Errorf"
			}
		}
		if f.Name == "New" {
			if _, ok := errorsQualifiers[unqualified]; ok {
				return "errors.New"
			}
		}
	case *ast.SelectorExpr:
		pkg, ok := f.X.(*ast.Ident)
		if !ok {
			return ""
		}
		if f.Sel.Name == "Errorf" {
			if _, ok := fmtQualifiers[pkg.Name]; ok {
				return "fmt.Errorf"
			}
		}
		if f.Sel.Name == "New" {
			if _, ok := errorsQualifiers[pkg.Name]; ok {
				return "errors.New"
			}
		}
	}

	return ""
}

func line(fset *token.FileSet, n ast.Node) string {
	return strconv.Itoa(fset.Position(n.Pos()).Line)
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}

	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module go.aledante.io/FlowSeer\n") {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found above the conformance package")
		}

		dir = parent
	}
}
