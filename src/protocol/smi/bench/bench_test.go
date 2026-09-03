package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/smi"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/frame"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/lex"
	"go.aledante.io/FlowSeer/src/protocol/smi/internal/parse"
)

// framed is one source held next to the frames cut from it.
//
// The parse-stage benchmarks want to time parsing and not the framing
// that produced their input, so they frame once outside the timer. They
// still need the byte count for b.SetBytes, and frame.File does not
// expose the source it aliases, so the two travel together.
type framed struct {
	src   []byte
	file  *frame.File
	bytes int64
}

// cutFixture frames a pinned corpus fixture.
func cutFixture(tb testing.TB, f corpusFixture) *frame.File {
	tb.Helper()

	return frame.Cut(readFixture(tb, f), frame.Options{File: f.path})
}

// frameSource frames an in-memory source.
func frameSource(name string, src []byte) framed {
	return framed{
		src:   src,
		file:  frame.Cut(src, frame.Options{File: name}),
		bytes: int64(len(src)),
	}
}

// BenchmarkLex measures tokenization alone, which is the one stage whose
// cost is a pure function of the byte count and so the one whose
// throughput number is directly meaningful.
func BenchmarkLex(b *testing.B) {
	for _, f := range corpusFixtures {
		src := readFixture(b, f)
		b.Run("fixture="+f.name, func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				sink(lex.Lex(src, lex.Options{File: f.path}))
			}
		})
	}
}

// BenchmarkFrame measures cutting a file into declaration frames. It
// includes lexing, because frame.Cut owns the token stream and may lex a
// file twice to settle which comment rule reads it — so this number is
// read against BenchmarkLex rather than alone, and a file that needs the
// second pass costs roughly twice the lexing.
func BenchmarkFrame(b *testing.B) {
	for _, f := range corpusFixtures {
		src := readFixture(b, f)
		b.Run("fixture="+f.name, func(b *testing.B) {
			b.SetBytes(int64(len(src)))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				sink(frame.Cut(src, frame.Options{File: f.path}))
			}
		})
	}
}

// BenchmarkParseDecl measures parsing one declaration and its fixed OID
// anchor, per macro kind.
//
// The macros do not cost the same: an OBJECT-TYPE reads a SYNTAX and its
// constraints, a MODULE-COMPLIANCE walks a nested group structure, and a
// bare OBJECT IDENTIFIER assignment reads an arc list and stops. Timing
// them separately means a regression in one clause parser names that
// macro instead of moving a whole-file average by a percent.
func BenchmarkParseDecl(b *testing.B) {
	for _, d := range declSamples {
		src := []byte(oneDeclModule(d.body))
		f := frameSource("bench-decl-"+d.macro, src)
		b.Run("macro="+d.macro, func(b *testing.B) {
			assertFrames(b, f.file, 2) // the anchor arc plus the declaration
			warmParse(f.file)
			b.SetBytes(f.bytes)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				sink(parse.Parse(f.file))
			}
		})
	}
}

// BenchmarkParseFile measures parsing a whole pre-framed file, which is
// the composition BenchmarkParseDecl decomposes: the same work at corpus
// scale, where slab growth and the per-file diagnostic budget show up
// and a single declaration cannot.
func BenchmarkParseFile(b *testing.B) {
	for _, f := range corpusFixtures {
		fr := framed{src: readFixture(b, f), bytes: int64(len(readFixture(b, f)))}
		fr.file = cutFixture(b, f)
		b.Run("fixture="+f.name, func(b *testing.B) {
			warmParse(fr.file)
			b.SetBytes(fr.bytes)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				sink(parse.Parse(fr.file))
			}
		})
	}
}

// BenchmarkRecovery measures the path that justifies this parser.
//
// Every other benchmark here runs a source the grammar accepts, and so
// never reaches the sync sets, the typed bail-out or the per-line
// diagnostic suppression. Vendor MIBs reach all three, and a recovery
// path that is correct but quadratic would pass every correctness test
// in the repository and still make the corpus unreadable. So this parses
// a module built entirely of malformed declarations, at a density no
// real file reaches, and is read against BenchmarkParseFile: the ratio
// is what recovery costs over accepting.
func BenchmarkRecovery(b *testing.B) {
	src := []byte(recoveryModule(b))
	f := frameSource("bench-recovery", src)
	b.Run("decls=1k", func(b *testing.B) {
		warmParse(f.file)
		b.SetBytes(f.bytes)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			sink(parse.Parse(f.file))
		}
	})
}

// TestRecoveryModuleIsMalformed checks the recovery benchmark's input is
// what it claims.
//
// Two ways this benchmark could quietly stop measuring anything: the
// synthesized source could frame into far fewer declarations than
// intended, if one fixture's damage ran past its own frame; or the
// fixtures could stop being malformed, leaving a benchmark named
// Recovery that runs the happy path. Both would still report a number.
func TestRecoveryModuleIsMalformed(t *testing.T) {
	src := []byte(recoveryModule(t))
	f := frame.Cut(src, frame.Options{File: "bench-recovery"})

	frames := 0
	for _, m := range f.Modules {
		frames += len(m.Frames)
	}
	// The anchor arc plus one frame per copy. A fixture whose damage
	// escaped its frame would merge neighbors and undershoot.
	if want := recoveryDeclCount + 1; frames != want {
		t.Errorf("framed into %d declarations, want %d", frames, want)
	}

	res := parse.Parse(f)
	t.Logf("%d bytes, %d declarations, %d diagnostics", len(src), frames, len(res.Diagnostics))
	if len(res.Diagnostics) == 0 {
		t.Error("the recovery corpus parsed clean, so this benchmark measures the happy path")
	}
}

// BenchmarkPipeline measures reading, framing and parsing a file without
// resolving it.
//
// Comparing it with BenchmarkLoad helps locate loading overhead. The
// difference includes module discovery and parsing imports as well as
// resolution, whose unexported entry point cannot be timed directly here.
func BenchmarkPipeline(b *testing.B) {
	for _, f := range corpusFixtures {
		path := fixturePath(b, f)
		b.Run("fixture="+f.name, func(b *testing.B) {
			b.SetBytes(int64(len(readFixture(b, f))))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				src, err := os.ReadFile(path)
				if err != nil {
					b.Fatalf("reading %s: %v", path, err)
				}
				sink(parse.Parse(frame.Cut(src, frame.Options{File: path})))
			}
		})
	}
}

// BenchmarkLoad measures the full public entry point on one file:
// read, frame, parse, resolve.
//
// It follows IMPORTS, so what it actually loads is the fixture plus
// whatever its imports reach on the search path — which is the honest
// cost of loading a MIB and the reason the byte count it reports is the
// fixture's own rather than the total. Read the throughput as relative,
// not absolute.
func BenchmarkLoad(b *testing.B) {
	for _, f := range corpusFixtures {
		path := fixturePath(b, f)
		opts := smi.Options{SearchPaths: []string{filepath.Dir(path)}}
		b.Run("fixture="+f.name, func(b *testing.B) {
			b.SetBytes(int64(len(readFixture(b, f))))
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				set, err := smi.LoadFiles([]string{path}, opts)
				if err != nil {
					b.Fatalf("LoadFiles(%s): %v", path, err)
				}
				sink(set)
			}
		})
	}
}

// BenchmarkEndToEnd measures the load mibgen performs, over the modules
// mibgen.yaml configures.
//
// This is the number that gates `go generate` latency, and the only one
// here a developer feels directly. It reads the config rather than
// hardcoding the module list, so adding a module moves this benchmark
// instead of leaving it reporting a stale set.
// SetBytes uses the size of files directly under the search paths; it is
// a relative throughput denominator, not the number of bytes Load reads.
func BenchmarkEndToEnd(b *testing.B) {
	names, paths := loadMibgenConfig(b)
	opts := smi.Options{SearchPaths: paths}

	total := int64(0)
	for _, p := range paths {
		entries, err := os.ReadDir(p)
		if err != nil {
			b.Fatalf("reading search path %s: %v", p, err)
		}
		for _, e := range entries {
			if info, err := e.Info(); err == nil && !info.IsDir() {
				total += info.Size()
			}
		}
	}

	b.Run(fmt.Sprintf("modules=%d", len(names)), func(b *testing.B) {
		b.SetBytes(total)
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			set, err := smi.Load(names, opts)
			if err != nil {
				b.Fatalf("Load: %v", err)
			}
			sink(set)
		}
	})
}

// sink keeps a benchmark's result reachable so the compiler cannot
// delete the work that produced it.
var sinkValue any

func sink(v any) { sinkValue = v }

// warmParse runs one untimed parse so the file's interner is populated
// before the measured loop. Without it the first iteration pays for
// every distinct identifier in the file and the reported mean drifts
// with the iteration count, which is exactly the instability a gate
// cannot tolerate.
func warmParse(f *frame.File) { sink(parse.Parse(f)) }

// assertFrames fails a benchmark whose input did not frame the way its
// author assumed. A single-declaration benchmark that quietly framed
// into one unrecognized blob would report a number and measure nothing.
func assertFrames(tb testing.TB, f *frame.File, want int) {
	tb.Helper()

	got := 0
	for _, m := range f.Modules {
		got += len(m.Frames)
	}
	if got != want {
		tb.Fatalf("source framed into %d declarations, want %d", got, want)
	}
}

// declSample is one macro's declaration, written out so the per-macro
// benchmark measures the clause parsers that macro actually uses.
type declSample struct {
	macro string
	body  string
}

// declSamples covers the macro kinds with a clause parser of their own.
// The plain assignment forms are included because they are the bulk of a
// vendor OID file and their cost is the arc-list reader, which nothing
// else exercises.
var declSamples = []declSample{
	{macro: "OBJECT-TYPE", body: `
benchObject OBJECT-TYPE
    SYNTAX      INTEGER { up(1), down(2), testing(3), unknown(4) }
    UNITS       "packets"
    MAX-ACCESS  read-only
    STATUS      current
    DESCRIPTION "A scalar with an enumeration, units and a default."
    REFERENCE   "RFC 2578"
    DEFVAL      { up }
    ::= { benchAnchor 1 }
`},
	{macro: "TEXTUAL-CONVENTION", body: `
BenchTC ::= TEXTUAL-CONVENTION
    DISPLAY-HINT "255a"
    STATUS       current
    DESCRIPTION  "A convention over a size-constrained octet string."
    REFERENCE    "RFC 2579"
    SYNTAX       OCTET STRING (SIZE (0..255))
`},
	{macro: "OBJECT-IDENTITY", body: `
benchIdentity OBJECT-IDENTITY
    STATUS      current
    DESCRIPTION "A naming node with an identity."
    ::= { benchAnchor 2 }
`},
	{macro: "NOTIFICATION-TYPE", body: `
benchNotification NOTIFICATION-TYPE
    OBJECTS     { benchObject }
    STATUS      current
    DESCRIPTION "A notification carrying one object."
    ::= { benchAnchor 3 }
`},
	{macro: "TRAP-TYPE", body: `
benchTrap TRAP-TYPE
    ENTERPRISE  benchAnchor
    VARIABLES   { benchObject }
    DESCRIPTION "An SMIv1 trap."
    ::= 4
`},
	{macro: "MODULE-COMPLIANCE", body: `
benchCompliance MODULE-COMPLIANCE
    STATUS      current
    DESCRIPTION "A compliance statement with a refinement."
    MODULE
        MANDATORY-GROUPS { benchGroup }
        OBJECT      benchObject
        SYNTAX      INTEGER { up(1), down(2) }
        MIN-ACCESS  read-only
        DESCRIPTION "Only two states need be supported."
    ::= { benchAnchor 5 }
`},
	{macro: "OBJECT-GROUP", body: `
benchGroup OBJECT-GROUP
    OBJECTS     { benchObject }
    STATUS      current
    DESCRIPTION "One group of one object."
    ::= { benchAnchor 6 }
`},
	{macro: "VALUE-ASSIGNMENT", body: `
benchNode OBJECT IDENTIFIER ::= { benchAnchor 7 }
`},
	{macro: "TYPE-ASSIGNMENT", body: `
BenchAlias ::= OCTET STRING (SIZE (0..64))
`},
}

// benchAnchor is the OID arc every sample declaration hangs off, so a
// sample can be dropped into a module of its own without the arc reader
// falling back to an unresolved name.
const benchAnchor = "benchAnchor OBJECT IDENTIFIER ::= " +
	"{ iso org(3) dod(6) internet(1) private(4) enterprises(1) 47100 }\n"

// oneDeclModule wraps one declaration in the smallest module that frames
// it.
func oneDeclModule(body string) string {
	var sb strings.Builder
	sb.WriteString("BENCH-DECL-MIB DEFINITIONS ::= BEGIN\n\n")
	sb.WriteString(benchAnchor)
	sb.WriteString(body)
	sb.WriteString("\nEND\n")

	return sb.String()
}

// recoveryDeclCount is how many malformed declarations the recovery
// module holds. It is high enough that per-declaration recovery cost
// dominates the module's fixed cost, and that a recovery path with
// super-linear behavior in the number of preceding failures shows up as
// a number nobody can miss.
const recoveryDeclCount = 1000

// recoveryFixtures are the malformed fixtures the recovery module is
// built from.
//
// It is a list rather than a directory walk because not every fixture
// under testdata/malformed is usable here: several are file-fatal by
// design — an unterminated comment or string, a missing module header —
// and one of those pasted into the middle would swallow every
// declaration after it, leaving a benchmark that measures a truncated
// file. These are the ones whose damage is confined to their own
// declaration, which is the case recovery exists for.
var recoveryFixtures = []string{
	"binary-string-not-octets",
	"clause-out-of-order",
	"default-not-permitted",
	"display-hint-malformed",
	"display-hint-not-permitted",
	"display-hint-separator",
	"duplicate-clause",
	"missing-clause",
	"negative-size",
	"odd-hex-string",
	"overlapping-range",
	"range-not-ascending",
	"range-outside-base-type",
	"unexpected-token",
	"unknown-clause",
	"unrecognized-declaration",
}

// malformedBodies reads the declaration bodies out of the fixture
// modules, dropping each fixture's module header, its END and the OID
// anchor the recovery module supplies once for all of them.
func malformedBodies(tb testing.TB) []string {
	tb.Helper()

	dir := filepath.Join("..", "testdata", "malformed")
	bodies := make([]string, 0, len(recoveryFixtures))
	for _, name := range recoveryFixtures {
		raw, err := os.ReadFile(filepath.Join(dir, name, "BAD-MIB.mib"))
		if err != nil {
			tb.Fatalf("recovery fixture %s: %v", name, err)
		}

		var kept []string
		for _, line := range strings.Split(string(raw), "\n") {
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.Contains(trimmed, "DEFINITIONS ::= BEGIN"):
			case trimmed == "END":
			case strings.HasPrefix(trimmed, "acme OBJECT IDENTIFIER"):
			default:
				kept = append(kept, line)
			}
		}

		body := strings.TrimSpace(strings.Join(kept, "\n"))
		if body == "" {
			tb.Fatalf("recovery fixture %s reduced to nothing after stripping its module frame", name)
		}
		bodies = append(bodies, body)
	}

	return bodies
}

// recoveryModule builds a module of recoveryDeclCount malformed
// declarations.
//
// Descriptors and OID arcs repeat across the copies. That is fine here
// and only here: this source is parsed, never resolved, and duplicate
// names are a resolution concern. What the parser sees is the same
// number of malformed declarations in a row either way.
func recoveryModule(tb testing.TB) string {
	tb.Helper()
	bodies := malformedBodies(tb)

	var sb strings.Builder
	sb.WriteString("BENCH-RECOVERY-MIB DEFINITIONS ::= BEGIN\n\n")
	sb.WriteString(benchAnchor)
	for i := range recoveryDeclCount {
		sb.WriteString("\n")
		sb.WriteString(bodies[i%len(bodies)])
		sb.WriteString("\n")
	}
	sb.WriteString("\nEND\n")

	return sb.String()
}
