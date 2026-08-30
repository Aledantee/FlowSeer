package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/smi/internal/catalog"
	"go.aledante.io/FlowSeer/src/common/smi/internal/diag"
	"go.aledante.io/FlowSeer/src/common/smi/internal/frame"
)

// allocationCeiling and allocationFloor bound what parsing one input may
// allocate, as bytes per input byte plus a fixed allowance for the
// result, the line table and the smallest slab.
//
// Two things set the ceiling. It cannot be tighter than the framer's own
// bound, because Parse runs the framer and then reads what it produced,
// and a file whose comment mode flips is framed twice. And it has to
// survive input that is one token per byte: a source of nothing but
// braces costs a 16-byte token per byte, doubled by the slice growth
// that reaches that size and doubled again by the second framing, which
// measures at roughly 150 bytes per input byte.
//
// The remaining headroom is deliberate. The property under test is that
// the cost stays linear in the input, not that it hits a particular
// constant: a pass that restarted from the top of a declaration, or that
// materialized a string per clause, would blow past this by orders of
// magnitude, while a node growing a field would not and should not fail
// a fuzz run. Tightness belongs to the benchmark gate, which measures
// the real spend rather than bounding the pathological one.
const (
	allocationCeiling = 4096
	allocationFloor   = 256 << 10
)

// The seed corpus budget. Seeds replay on every `go test`, so a corpus
// that grows without a bound turns the fastest test in the package into
// the slowest. Eight kibibytes is far more than any hand-written seed
// needs and still small enough that a minimized crasher fits.
const (
	maxSeedBytes = 8 << 10
	maxSeedFiles = 60
)

// FuzzParse asserts the parser's tolerance contract on arbitrary bytes.
//
// The contract is that any input yields an AST plus diagnostics: never a
// panic, never a hang, never unbounded allocation. The fuzzing engine
// catches the panic and the hang on its own, so what is checked here is
// what it cannot see. Allocation stays bounded against the input length.
// Every diagnostic points at a byte the input actually has, carries a
// severity on the scale and a code the catalog knows, so a corpus report
// can render all of them without one of them being the thing that fails.
// And parsing the same bytes twice yields the same model and the same
// diagnostics, because a parser that depends on map order or on state
// left over from the previous file cannot be baselined at all.
//
// The seeds are hand-written rather than drawn from the corpus. Each
// fatal condition is one seed, since those are the four ways a file is
// abandoned and each takes a different path out; each recovery path is
// another, since those are what the fuzzer mutates into the shapes
// nobody thought to write.
func FuzzParse(f *testing.F) {
	// The four fatal conditions.
	f.Add([]byte("no DEFINITIONS header anywhere in this file\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nx OBJECT-TYPE DESCRIPTION \"runs off the end\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nx OBJECT-TYPE -- runs off the end\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nT ::= INTEGER " + strings.Repeat("(", frame.MaxDepth+8) + "\nEND\n"))

	// The recovery paths: a missing required clause, clauses out of
	// order, a head the framer cannot classify, junk that has to be
	// consumed without reaching the next declaration, a body that is
	// skipped whole, and content after END.
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nx OBJECT-TYPE MAX-ACCESS read-only STATUS current ::= { a 1 }\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nx OBJECT-TYPE SYNTAX INTEGER STATUS current MAX-ACCESS read-only DESCRIPTION \"d\" ::= { a 1 }\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nx WHAT-EVER ::= { a 1 }\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nx OBJECT-TYPE ] ) 7 , , | .. ] } ::= { a 1 }\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nB MACRO ::= BEGIN \"END\" -- END\nEND\nEXPORTS a, b;\nEND\ntrailing\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nx OBJECT-TYPE SYNTAX BITS { a(0), b(2) } DEFVAL { { } } ::= { a 1 }\nEND\n"))

	// The declaration forms that are not macro invocations, and SMIv1,
	// which grades a different set of clauses as required.
	f.Add([]byte("A DEFINITIONS ::= BEGIN\norg OBJECT IDENTIFIER ::= { iso 3 }\nFoo ::= SEQUENCE { a INTEGER, b OCTET STRING }\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nl TRAP-TYPE ENTERPRISE snmp VARIABLES { ifIndex } ::= 2\nx OBJECT-TYPE SYNTAX INTEGER ACCESS read-only STATUS mandatory ::= { a 1 }\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nT ::= TEXTUAL-CONVENTION DISPLAY-HINT \"255a\" STATUS current DESCRIPTION \"d\" SYNTAX OCTET STRING (SIZE (0..255))\nEND\n"))
	f.Add([]byte("A DEFINITIONS ::= BEGIN\nc MODULE-COMPLIANCE STATUS current DESCRIPTION \"d\" MODULE OBJECT x SYNTAX INTEGER ::= { a 1 }\nEND\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		r := Parse(frame.Cut(data, frame.Options{File: testFile}))
		runtime.ReadMemStats(&after)

		spent := after.TotalAlloc - before.TotalAlloc
		budget := uint64(len(data))*allocationCeiling + allocationFloor
		if spent > budget {
			t.Fatalf("parsing %d bytes allocated %d, want at most %d", len(data), spent, budget)
		}

		checkResult(t, data, r)

		again := Parse(frame.Cut(data, frame.Options{File: testFile}))
		checkSameParse(t, r, again)
	})
}

// checkResult asserts everything a Result must hold whatever the input
// was: declaration refs that address a real slab, spans that can be read
// back, and diagnostics that can be rendered.
func checkResult(t *testing.T, data []byte, r *Result) {
	t.Helper()

	for _, m := range r.Modules {
		for _, ref := range m.Decls {
			if ref.Kind >= numDeclKinds {
				t.Fatalf("declaration kind %d is off the set", ref.Kind)
			}

			// Reading a declaration's name and text must be total: an
			// offset that slipped outside the source would panic here
			// rather than silently describe the wrong declaration.
			d := m.Decl(ref)
			_ = d.Name
			_ = r.Text(d.Span)
			_ = r.StringValue(d.Span)
		}
	}

	for _, d := range r.Diagnostics {
		checkDiagnostic(t, data, r, d)
	}
}

// checkDiagnostic asserts the three things a caller needs from a
// diagnostic before it can report one: an offset inside the file it
// names, a severity on the scale, and a code the catalog can render.
func checkDiagnostic(t *testing.T, data []byte, r *Result, d diag.Diagnostic) {
	t.Helper()

	// The end of the input is a legal position, since that is where an
	// unterminated construct is noticed.
	if off := d.Position().Offset; off < 0 || off > len(data) {
		t.Fatalf("%v points at offset %d, outside a %d-byte input", d.Code(), off, len(data))
	}
	if !d.Severity().Valid() {
		t.Fatalf("%v carries severity %d, off the scale", d.Code(), uint8(d.Severity()))
	}
	if !cataloged[d.Code()] {
		t.Fatalf("%v is not in the catalog", d.Code())
	}

	shown := d.Render(r.Lines)
	if shown.Line < 1 || shown.Column < 1 {
		t.Fatalf("%v renders at %d:%d, which is not a place in a file", d.Code(), shown.Line, shown.Column)
	}
}

// checkSameParse asserts two parses of the same bytes agree, model and
// diagnostics both.
func checkSameParse(t *testing.T, first, second *Result) {
	t.Helper()

	if !reflect.DeepEqual(first.Modules, second.Modules) {
		t.Fatalf("reparsing gave a different model:\n first: %+v\nsecond: %+v", first.Modules, second.Modules)
	}
	if len(first.Diagnostics) != len(second.Diagnostics) {
		t.Fatalf("reparsing gave %d diagnostics, want %d", len(second.Diagnostics), len(first.Diagnostics))
	}
	for i := range first.Diagnostics {
		got, want := second.Diagnostics[i].Render(second.Lines), first.Diagnostics[i].Render(first.Lines)
		if got != want {
			t.Fatalf("diagnostic %d reparsed as %v, want %v", i, got, want)
		}
	}
}

// cataloged is the set of codes the table defines, built once so the
// fuzz body does not rebuild it per input.
var cataloged = func() map[errs.Code]bool {
	rows := catalog.Entries()
	m := make(map[errs.Code]bool, len(rows))
	for _, row := range rows {
		m[errs.Code(row.Code)] = true
	}

	return m
}()

// TestDeepSubtypeIsFatalRatherThanRecursive feeds a SIZE constraint
// nested past the depth cap. The parser descends recursively through
// subtype expressions, so the input that would exhaust the goroutine
// stack has to be stopped by the counter rather than by the stack, and
// what reaches the caller is a fatal diagnostic and no module.
func TestDeepSubtypeIsFatalRatherThanRecursive(t *testing.T) {
	depth := frame.MaxDepth * 64

	var b strings.Builder
	b.WriteString("deep OBJECT-TYPE SYNTAX OCTET STRING (SIZE ")
	b.WriteString(strings.Repeat("(", depth))
	b.WriteString("0")
	b.WriteString(strings.Repeat(")", depth))
	b.WriteString(`) MAX-ACCESS read-only STATUS current DESCRIPTION "d" ::= { test 1 }`)

	src := wrap(b.String())
	r := parseSource(t, src)

	if len(r.Diagnostics) == 0 {
		t.Fatal("nesting past the cap raised nothing")
	}
	last := r.Diagnostics[len(r.Diagnostics)-1]
	if last.Code() != diag.ErrCodeLimitExceeded {
		t.Errorf("got %v, want %v", last.Code(), diag.ErrCodeLimitExceeded)
	}
	if last.Severity() != diag.SeverityFatal {
		t.Errorf("got severity %v, want %v", last.Severity(), diag.SeverityFatal)
	}
	if len(r.Modules) != 0 {
		t.Errorf("got %d modules, want none: a limit costs the file", len(r.Modules))
	}

	checkResult(t, []byte(src), r)
}

// TestBraceNestingFinishesQuickly feeds a declaration that is nothing but
// open braces. Nesting like this is where a recovery loop that rescanned
// from the start of the construct would show up as quadratic time, and
// the whole point of bounding recovery at the frame is that it cannot.
func TestBraceNestingFinishesQuickly(t *testing.T) {
	src := wrap("T ::= " + strings.Repeat("{", 200000))

	start := time.Now()
	r := parseSource(t, src)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("parsing %d bytes of braces took %v, want well under a second", len(src), elapsed)
	}
	checkResult(t, []byte(src), r)
}

// TestDiagnosticPerLineFinishesQuickly feeds a file whose every line is a
// broken declaration, which is the worst case the raise path has: one
// diagnostic per line, all the way to the cap.
//
// It finishes because raising a diagnostic stores an offset and nothing
// else. Working out which line that offset sits on is a binary search
// over the line table, and it runs once per diagnostic somebody actually
// prints. A parser that resolved the line where the diagnostic was
// raised would walk the file per diagnostic, and this is the input that
// would turn that into quadratic time.
func TestDiagnosticPerLineFinishesQuickly(t *testing.T) {
	const lines = 4000

	var b strings.Builder
	for i := range lines {
		fmt.Fprintf(&b, "bad%d OBJECT-TYPE WHAT EVER ::= { test %d }\n", i, i)
	}
	src := wrap(b.String())

	start := time.Now()
	r := parseSource(t, src)
	for _, d := range r.Diagnostics {
		_ = d.Render(r.Lines)
	}
	elapsed := time.Since(start)

	if len(r.Diagnostics) < lines {
		t.Fatalf("got %d diagnostics from %d broken lines, want one each", len(r.Diagnostics), lines)
	}
	if elapsed > time.Second {
		t.Errorf("parsing and rendering %d diagnostics took %v, want well under a second", len(r.Diagnostics), elapsed)
	}
}

// TestParseIsIdempotent checks the property the fuzz target leans on
// hardest, against fixtures rather than against mutations, so a
// regression in it names the construct that broke.
func TestParseIsIdempotent(t *testing.T) {
	sources := []string{
		wrap(`x OBJECT-TYPE SYNTAX INTEGER { a(1), b(2) } MAX-ACCESS read-only STATUS current DESCRIPTION "d" DEFVAL { a } ::= { test 1 }`),
		wrap("broken OBJECT-TYPE STATUS current ::= { test 1 }"),
		wrap(`T ::= TEXTUAL-CONVENTION DISPLAY-HINT "1x:" STATUS current DESCRIPTION "d" SYNTAX OCTET STRING (SIZE (0..8))`),
		"A DEFINITIONS ::= BEGIN\nEND\nB DEFINITIONS ::= BEGIN\nx OBJECT IDENTIFIER ::= { iso 3 }\nEND\ntrailing\n",
		"not a mib at all",
	}

	for _, src := range sources {
		first := parseSource(t, src)
		second := parseSource(t, src)
		checkSameParse(t, first, second)
	}
}

// fuzzSeedDirs are every committed seed corpus in the SMI parser, named
// relative to this package.
//
// The budget is checked from one place rather than once per package
// because the cap that matters is the total: three targets each staying
// under their own limit still add up to a corpus that slows every run.
// Keeping the walk here costs a relative path into two sibling packages
// and buys a single number to hold the line on.
var fuzzSeedDirs = []string{
	"testdata/fuzz",
	"../lex/testdata/fuzz",
	"../frame/testdata/fuzz",
}

// TestFuzzSeedCorpusFitsItsBudget holds the committed seeds to a size and
// a count, because every one of them is replayed by every `go test` run
// in the package that owns it.
func TestFuzzSeedCorpusFitsItsBudget(t *testing.T) {
	var files, bytes int
	for _, dir := range fuzzSeedDirs {
		n, size, err := measureSeeds(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		files += n
		bytes += size
	}

	if files > maxSeedFiles {
		t.Errorf("the seed corpora hold %d files, want at most %d", files, maxSeedFiles)
	}
	t.Logf("%d committed seeds, %d bytes", files, bytes)
}

// TestOversizedSeedIsRejected checks the size gate itself against a seed
// one byte over the cap, so that the test above is known to fail when it
// should rather than only known to pass today.
func TestOversizedSeedIsRejected(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "FuzzThing")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatal(err)
	}

	seed := []byte("go test fuzz v1\n[]byte(\"" + strings.Repeat("a", maxSeedBytes) + "\")\n")
	if err := os.WriteFile(filepath.Join(target, "oversized"), seed, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := measureSeeds(dir); err == nil {
		t.Fatalf("a %d-byte seed passed a %d-byte cap", len(seed), maxSeedBytes)
	}
}

// measureSeeds returns how many seed files dir holds and how many bytes
// they take, and reports the first one over the per-seed cap. A missing
// directory is not an error: a target with no committed seed is a target
// that has found no crasher yet.
func measureSeeds(dir string) (files, bytes int, err error) {
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}

			return err
		}
		if entry.IsDir() {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSeedBytes {
			return fmt.Errorf("seed %s is %d bytes, over the %d-byte cap", path, info.Size(), maxSeedBytes)
		}

		files++
		bytes += int(info.Size())

		return nil
	})

	return files, bytes, err
}
