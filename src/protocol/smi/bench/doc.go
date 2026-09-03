// Package bench measures the FlowSeer SMI parser against pinned fixtures
// and a committed performance baseline. It has its own Go module. The tests
// and gate require benchstat on PATH. Run these commands from this directory:
//
//	go test ./...                              # fixture and gate tests
//	go test -bench . -benchmem -run '^$' -count=10
//	task gate
//
// # Measurements
//
// Each stage is measured separately to help locate a regression:
//
//	BenchmarkLex       tokens from bytes
//	BenchmarkFrame     lex + cut into declaration frames
//	BenchmarkParseDecl one pre-framed declaration and its anchor, per macro kind
//	BenchmarkParseFile one pre-framed file
//	BenchmarkRecovery  one pre-framed file of 1000 malformed declarations
//	BenchmarkPipeline  read + frame + parse, no resolution
//	BenchmarkLoad      read + frame + parse + resolve (smi.LoadFiles)
//	BenchmarkEndToEnd  smi.Load over the modules configured in mibgen.yaml
//
// Framing includes lexing and can lex twice to select the comment rule.
// Compare Frame with Lex to interpret that cost. Load also follows imports
// and searches for modules, so its difference from Pipeline includes that
// work as well as resolution.
//
// SetBytes reports the fixture's source size for the fixture benchmarks.
// Load can read imports beyond that fixture. EndToEnd uses the total size
// of files directly under its configured search paths, which can include
// files the loader does not read. Their throughput is a relative measure,
// not a count of bytes actually processed.
//
// The corpus fixtures cover many small declarations and a few large ones;
// fixtures_test.go pins their paths and SHA-256 hashes. A hash mismatch
// fails because a changed input would invalidate the baseline. Recovery
// repeats declaration-local malformed fixtures from ../testdata/malformed.
// Its test checks the declaration count and that parsing reports errors,
// so swallowed declarations or an entirely valid corpus cannot silently
// change what the benchmark measures.
//
// # The gate
//
// bench-gate.sh compares a fresh run against testdata/baseline-micro.txt
// using benchstat. It rejects statistically significant increases in
// allocs/op or B/op when the increase is at least MIN_DELTA percent
// (default 1). Small allocation differences can be statistically significant
// without being meaningful, which is why the effect-size floor exists.
// Wall time is advisory unless GATE_NS=1; throughput is never gated.
//
// The gate rejects output with no benchmark rows and never rewrites its
// baseline. To refresh the baseline, run the suite with COUNT=10, retain
// the preamble and benchmark rows, and review and commit the new file.
// RAW_IN supplies a saved go test transcript for offline gate tests.
package bench
