// Package conformance gates the two mechanically decidable halves of the
// panic rule: where a panic may sit, and where a goroutine may start.
//
// The other halves stay review rules. Whether a panic is handled needs a
// repository-wide call graph and a judgment about which recover owns it,
// and whether a caller documents an inherited panic is a judgment about
// prose. docs/code-style.md says which is which.
package conformance

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// spawnDir is the one package allowed to start a goroutine, compared for
// equality rather than as a prefix. There is no src/common/spawnpool
// today, and prefix matching would admit one the day somebody adds it —
// an allowlist that grows by accident is what this gate exists to stop.
const spawnDir = "src/common/spawn"

// TestPanicPolicy is the gate. It walks first-party source under src/ and
// reports every panic outside a Must function and every go statement
// outside the supervised helper.
//
// It parses files by path rather than importing them, so one test in the
// root module inspects the nested modules too (src/edge/netpen, the bench
// and differential modules), which a root `go test ./...` never builds.
// Today the only sanctioned panics down there are netpen's mustMAC
// helpers and catalog.MustRegister; netpen spawns through spawn.Go, which
// this gate does not see, and the bench modules hold neither.
func TestPanicPolicy(t *testing.T) {
	root := repoRoot(t)
	srcRoot := filepath.Join(root, "src")

	scanned := 0
	err := filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// testdata holds fixtures standing in for foreign code — a
			// gNMI target, a malformed MIB — and the rule is about
			// first-party code.
			if d.Name() == "testdata" {
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
}

func TestPanicPlacementPolicy(t *testing.T) {
	tests := []struct {
		name  string
		decl  string
		valid bool
	}{
		{name: "exported must helper", decl: "func MustOID(s string) OID { panic(s) }", valid: true},
		{name: "unexported must helper", decl: "func mustMAC(s string) MAC { panic(s) }", valid: true},
		{name: "generic must helper", decl: "func mustGetLayer[T any]() T { panic(\"x\") }", valid: true},
		{name: "must method", decl: "func (r *Registry) MustRegister() { panic(\"x\") }", valid: true},
		{name: "plain function", decl: "func decode() { panic(\"x\") }"},
		{name: "method with a must-less name", decl: "func (c *cutter) raise() { panic(\"x\") }"},
		{name: "name that merely contains must", decl: "func remustered() { panic(\"x\") }"},
		{name: "init", decl: "func init() { panic(\"x\") }"},
		{name: "outside any function declaration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := panicPlacementViolation(parseDecl(t, tt.decl)) == ""
			if got != tt.valid {
				t.Errorf("got valid=%t, want %t", got, tt.valid)
			}
		})
	}
}

func TestGoStatementPolicy(t *testing.T) {
	tests := []struct {
		name  string
		dir   string
		valid bool
	}{
		{name: "the supervised helper", dir: spawnDir, valid: true},
		{name: "a sibling whose name starts the same", dir: "src/common/spawnpool"},
		{name: "a package under the helper", dir: "src/common/spawn/internal"},
		{name: "a service", dir: "src/services/device"},
		{name: "an edge application", dir: "src/edge/netpen/runner"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goStmtViolation(tt.dir) == ""
			if got != tt.valid {
				t.Errorf("got valid=%t, want %t", got, tt.valid)
			}
		})
	}
}

// fileViolations reports every placement and boundary finding in one
// parsed file, each naming the file and line.
//
// Declarations are walked one at a time so the enclosing declaration of a
// node is never the previous one: a panic in a func literal held by a var
// block sits outside any function declaration, which is a finding.
func fileViolations(fset *token.FileSet, file *ast.File, rel string) []string {
	dir := filepath.ToSlash(filepath.Dir(rel))

	var findings []string
	for _, decl := range file.Decls {
		enclosing, _ := decl.(*ast.FuncDecl)

		ast.Inspect(decl, func(n ast.Node) bool {
			var reason string
			switch node := n.(type) {
			case *ast.CallExpr:
				id, ok := node.Fun.(*ast.Ident)
				if !ok || id.Name != "panic" {
					return true
				}
				reason = panicPlacementViolation(enclosing)
			case *ast.GoStmt:
				reason = goStmtViolation(dir)
			default:
				return true
			}

			if reason != "" {
				findings = append(findings, rel+":"+line(fset, n)+": "+reason)
			}

			return true
		})
	}

	return findings
}

// panicPlacementViolation reports why a panic inside enclosing breaks the
// placement rule, or "" when it does not. enclosing is nil for a panic
// outside any function declaration, which is a violation with nowhere to
// put the prefix.
//
// The prefix is matched against the declared name, so a generic helper's
// type parameters do not defeat it, and a name that merely contains
// "must" does not satisfy it.
func panicPlacementViolation(enclosing *ast.FuncDecl) string {
	if enclosing == nil {
		return "panic outside any function declaration; a panic belongs in a Must or must function, where the call site can see it"
	}
	name := enclosing.Name.Name
	if strings.HasPrefix(name, "Must") || strings.HasPrefix(name, "must") {
		return ""
	}

	return "panic in " + name + ", whose name does not begin with Must or must; rename it or return an error"
}

// goStmtViolation reports why a go statement in dir breaks the goroutine
// boundary, or "" when it does not.
//
// It sees the go keyword, not sync.WaitGroup.Go, which is a call
// expression with no keyword to match. That blind spot is a review catch;
// docs/code-style.md says so rather than leaving the gate believed in.
func goStmtViolation(dir string) string {
	if dir == spawnDir {
		return ""
	}

	return "go statement outside " + spawnDir + "; a panic in a spawned goroutine is unhandled whatever recover its spawner sits under, so launch it through spawn.Go"
}

// parseDecl returns the function declaration src declares, or nil when
// src declares none.
func parseDecl(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()

	if src == "" {
		return nil
	}

	file, err := goparser.ParseFile(token.NewFileSet(), "fixture.go", "package p\n"+src, 0)
	if err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("fixture declares %T, not a function", file.Decls[0])
	}

	return fn
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
