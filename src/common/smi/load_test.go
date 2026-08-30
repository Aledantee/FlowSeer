package smi_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.aledante.io/FlowSeer/src/common/smi"
)

// fixtureDir writes the modules every ordering test loads and returns
// their paths in a fixed order.
func fixtureDir(t *testing.T) (string, []string) {
	t.Helper()

	dir := t.TempDir()
	paths := []string{
		writeMIB(t, dir, "BASE-MIB", baseMIB),
		writeMIB(t, dir, "LEAF-MIB", leafMIB),
		writeMIB(t, dir, "TABLE-MIB", tableMIB),
		writeMIB(t, dir, "TC-MIB", tcMIB),
		writeMIB(t, dir, "MIXED-MIB", mixedMIB),
	}

	return dir, paths
}

// mixedMIB raises diagnostics from both passes and in an order the two
// passes do not agree on: the parser's missing-clause conditions sit
// near the top of the file and resolution's unresolved declarations sit
// below them, so an unsorted merge puts the file out of source order.
const mixedMIB = `MIXED-MIB DEFINITIONS ::= BEGIN

broken OBJECT-IDENTITY
    ::= { iso 3 6 1 4 1 400 }

first OBJECT IDENTIFIER ::= { broken 1 }

second OBJECT IDENTIFIER ::= { broken 2 }

END
`

// Parallelism is an implementation detail of the load and must not reach
// the result: the same files loaded in any order, with any pool size,
// produce the same diagnostics in the same order.
func TestDiagnosticOrderSurvivesFileOrderAndPoolSize(t *testing.T) {
	dir, paths := fixtureDir(t)

	reference := ""
	for _, workers := range []int{1, 2, runtime.NumCPU()} {
		reversed := slices.Clone(paths)
		slices.Reverse(reversed)

		rotated := append(slices.Clone(paths[2:]), paths[:2]...)

		for _, order := range [][]string{paths, reversed, rotated} {
			set, err := smi.LoadFiles(slices.Clone(order), smi.Options{
				SearchPaths: []string{dir},
				Workers:     workers,
			})
			if err != nil {
				t.Fatalf("loading with %d workers: %v", workers, err)
			}

			var b strings.Builder
			for _, r := range set.Render() {
				b.WriteString(r.String())
				b.WriteByte('\n')
			}

			assertCanonicalOrder(t, set)

			if reference == "" {
				reference = b.String()

				continue
			}
			if b.String() != reference {
				t.Fatalf("diagnostic order changed with %d workers and order %v", workers, order)
			}
		}
	}
}

// assertCanonicalOrder checks the order the set promises: by file, then
// by where in the file the condition was found. A load that merged its
// workers' output in completion order would still be deterministic on
// one machine, so being deterministic is not enough to assert.
func assertCanonicalOrder(t *testing.T, set *smi.ModuleSet) {
	t.Helper()

	diags := set.Diagnostics()
	for i := 1; i < len(diags); i++ {
		prev, cur := diags[i-1].Position(), diags[i].Position()
		if prev.File > cur.File || (prev.File == cur.File && prev.Offset > cur.Offset) {
			t.Fatalf("diagnostic %d at %s:%d follows %s:%d, which is not source order",
				i, cur.File, cur.Offset, prev.File, prev.Offset)
		}
	}
}

// The model is immutable once a load returns, so any number of readers
// may walk it at once. Run under -race, this is the test that catches a
// lazily-filled field somebody adds later.
func TestConcurrentReadersSeeTheSameModel(t *testing.T) {
	dir, paths := fixtureDir(t)

	set, err := smi.LoadFiles(paths, smi.Options{SearchPaths: []string{dir}})
	if err != nil {
		t.Fatalf("loading: %v", err)
	}

	const readers = 8

	results := make([]string, readers)
	var wg sync.WaitGroup
	for i := range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = digest(set)
		}()
	}
	wg.Wait()

	for i, got := range results {
		if got != results[0] {
			t.Fatalf("reader %d saw a different model", i)
		}
	}
}

// The parser must be able to depend on nothing in the SNMP runtime, so
// that a model-driven decode path there can depend on the parser. The
// rule is easy to break by reaching for snmp.OID, and nothing but a test
// notices.
func TestSMIImportsNothingFromTheSNMPRuntime(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	root := filepath.Dir(here)

	const forbidden = "go.aledante.io/FlowSeer/src/common/snmp"

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}

		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if p == forbidden || strings.HasPrefix(p, forbidden+"/") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s imports %s; the parser must not depend on the SNMP runtime", rel, p)
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}
