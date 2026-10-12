// Package conformance gates the netpen runner's process ownership: the
// library returns errors and verdicts and leaves exit and signals to the
// host. A runner that exits or subscribes to signals cannot be embedded.
package conformance

import (
	"go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// runnerDir is the one package the policy covers, relative to the repo
// root.
const runnerDir = "src/edge/netpen/runner"

// forbidden maps a package import path to the function names the runner
// may not call from it.
var forbidden = map[string][]string{
	"os":        {"Exit"},
	"os/signal": {"Notify", "NotifyContext"},
}

// TestRunnerOwnsNoProcess walks the runner's non-test sources and reports
// every call to os.Exit or signal.Notify, so process ownership stays with
// the command.
func TestRunnerOwnsNoProcess(t *testing.T) {
	dir := filepath.Join(repoRoot(t), runnerDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		for _, finding := range fileViolations(t, filepath.Join(dir, name)) {
			t.Error(finding)
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no runner sources; the gate checked nothing")
	}
}

// TestRunnerPolicyTripsOnTheFixture proves the gate sees both calls: the
// fixture under testdata exits and subscribes to signals, and the gate
// names each line.
func TestRunnerPolicyTripsOnTheFixture(t *testing.T) {
	got := fileViolations(t, filepath.Join("testdata", "owns_process.go.txt"))
	if len(got) != 2 {
		t.Fatalf("violations = %v, want the exit and the signal subscription", got)
	}
	if !strings.Contains(got[0], "signal.Notify") || !strings.Contains(got[1], "os.Exit") {
		t.Errorf("violations = %v, want signal.Notify then os.Exit in source order", got)
	}
}

// fileViolations parses one file and returns a line per forbidden call,
// resolving the package through the file's imports so an alias cannot
// hide a call.
func fileViolations(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := goparser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	aliases := map[string]string{}
	for _, imp := range file.Imports {
		importPath, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if _, ok := forbidden[importPath]; !ok {
			continue
		}
		alias := filepath.Base(importPath)
		if imp.Name != nil {
			alias = imp.Name.Name
		}
		aliases[alias] = importPath
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		importPath, ok := aliases[pkg.Name]
		if !ok {
			return true
		}
		for _, fn := range forbidden[importPath] {
			if sel.Sel.Name == fn {
				out = append(out, path+":"+strconv.Itoa(fset.Position(call.Pos()).Line)+": "+filepath.Base(importPath)+"."+fn+" in the runner; process ownership belongs to the command")
			}
		}
		return true
	})
	return out
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
