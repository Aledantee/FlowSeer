// Package bench holds the FlowSeer SMI parser's performance suite and
// the gate that pins it to a committed baseline.
//
// It is a standalone go.module (see go.mod). Run everything from this
// directory.
//
// # The ladder
//
// The parser is a pipeline, and a number that only says "the parser got
// slower" costs an afternoon to attribute. So each stage is measured on
// its own, and the composed numbers are measured beside them:
//
//	BenchmarkLex       tokens from bytes
//	BenchmarkFrame     lex + cut into declaration frames
//	BenchmarkParseDecl one pre-framed declaration, per macro kind
//	BenchmarkParseFile one pre-framed file, whole
//	BenchmarkRecovery  one pre-framed file of ~1000 malformed declarations
//	BenchmarkPipeline  read + frame + parse, no resolution
//	BenchmarkLoad      read + frame + parse + resolve (smi.LoadFiles)
//	BenchmarkEndToEnd  smi.Load over the 16 modules mibgen.yaml configures
//
// Framing subsumes lexing and cannot be separated from it — frame.Cut
// owns the token stream and may lex a file twice to settle its comment
// rule — so BenchmarkFrame is read against BenchmarkLex rather than on
// its own. Resolution is the one stage with no benchmark of its own,
// because smi's resolve pass is unexported and this module sits outside
// package smi; its cost is BenchmarkLoad minus BenchmarkPipeline, which
// is why Pipeline exists at all. A regression in resolution shows as
// Load moving while Pipeline holds still.
//
// Every benchmark calls b.SetBytes with the source bytes it consumed, so
// the reported throughput is directly comparable across stages and
// across the two fixtures.
//
// # The two fixtures pull in opposite directions
//
// A parser can be fast on many small declarations and slow on one
// enormous one, or the reverse, and a median-sized MIB catches neither.
// So the corpus fixtures are pinned to the two extremes the repository's
// MIB tree actually holds — see corpusFixtures in fixtures_test.go for
// the paths, the SHA-256 of each, and the shape each one covers.
//
// The pins are by content hash. A MIB re-sync that replaces a fixture
// moves the baseline underneath the gate, and a gate whose reference
// silently changed is worse than no gate; a hash mismatch fails loudly
// and names the re-sync as the thing to look at.
//
// # Recovery is a first-class measurement
//
// Recovering from a malformed declaration without abandoning the file is
// this parser's reason to exist, and no other benchmark reaches that
// code. BenchmarkRecovery synthesizes a module of roughly a thousand
// consecutive malformed declarations from the fixtures in
// ../testdata/malformed and parses it, so the recovery path is measured
// under the density it was written for rather than the one malformed
// declaration a real file carries.
//
// # Running
//
//	go test -bench . -benchmem -run '^$' -count=10   # the suite
//	go test ./...                                    # the gate's own tests
//	sh bench-gate.sh                                 # the gate
//
// # The gate
//
// testdata/baseline-micro.txt is a committed benchstat-format snapshot.
// bench-gate.sh runs the suite, benchstat-compares it against that file,
// and hard-fails only on a statistically significant regression in
// allocs/op or B/op that is also at least MIN_DELTA percent (1 by
// default). Those two metrics are near-deterministic on this suite; wall
// time is not, and a gate that false-trips on shared hardware is a gate
// somebody switches off. GATE_NS=1 makes sec/op hard too, for a local
// machine where that is worth something.
//
// The effect-size floor is there because "near-deterministic" is not
// "deterministic": allocated bytes move by a few parts per million with
// the iteration count the harness chose, and benchstat calls that
// significant. The gate's first full run against its own freshly
// captured baseline failed on seven rows, each reading "+0.00%".
//
// The gate never rewrites the baseline. Refreshing it is a deliberate,
// reviewed commit: re-run the suite at COUNT=10, keep the preamble and
// the benchmark rows, commit the new file.
//
// Before comparing, the gate asserts the filtered run holds at least one
// benchmark row. That check exists because the SNMP gate this one is
// modeled on filters its output down to one arm of a two-arm comparison,
// and copying that filter here would have left benchstat a file of
// preamble — a gate that passes while comparing nothing.
package bench
