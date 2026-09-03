// Package bench holds the FlowSeer SNMP performance comparison suite.
//
// It is a standalone go.module (see go.mod) whose sole reason to exist is
// to compare the FlowSeer SNMP implementation against the predecessor
// gosnmp client and against Net-SNMP, without letting gosnmp re-enter the
// main module's dependency graph.
//
// # Tiers
//
// Tier 1 (micro, this file's neighbors in micro_test.go) runs in-process
// against a loopback UDP responder (responder_test.go) and compares the
// FlowSeer client (snmp.NewSession) against a gosnmp client on identical
// SNMP v2c operations. It reports ns/op, allocs/op, and B/op via the
// standard testing.B harness; compare the two with benchstat.
//
// Tier 2 (macro, macro_test.go, build tag snmp_bench_macro) runs against a
// live agent and adds Net-SNMP via subprocess; see that file.
//
// # Warmup and cold-start
//
// Steady-state benchmarks construct one session, perform one untimed
// warmup round-trip, then call b.ResetTimer before the measured loop, so
// the reported ns/op excludes session construction and first-call cache
// effects. Cold-start cost is measured by separate benchmarks
// (BenchmarkColdStart*) whose timed loop constructs a fresh session and
// performs the first operation each iteration.
//
// The micro tier's cold-start is v2c only — socket open plus the first
// round-trip. The SNMPv3/USM engine-discovery handshake, which dominates
// real cold-start, is measured in the macro tier against a live v3 agent
// (the loopback responder here speaks v2c only).
//
// # Running
//
//	cd src/common/snmp/bench
//	go test -bench . -benchmem            # Tier 1 micro
//	go test -bench Get -benchmem -count 10 | tee new.txt && benchstat new.txt
//
// BenchmarkTableWalkScale measures typed row assembly at 50 through 100,000
// rows, selecting 2 or 20 columns and stopping after 1, 100, or all rows. Its
// wire-B/op includes requests and responses; B/op also includes local responder
// work, but excludes fixture construction. Responses truncate at 7,000 varbind
// bytes to fit the host's UDP limit, exercising partial repetitions.
//
// BenchmarkBulkWalkStreaming compares raw traversal with GoSNMP's callback API;
// BenchmarkBulkWalk retains the GoSNMP collection comparison. Neither assembles
// typed rows. Net-SNMP's optional cgo traversal also counts varbinds; its C heap
// allocations are outside Go's benchmark memory accounting.
//
// Isolate the responder in a child process to measure client retained memory:
//
//	FLOWSEER_MEASURE_HEAP=1 go test -run '^TestStreamingRetainedHeap$' -v
//
// This opt-in test subtracts a warmed session's post-GC HeapAlloc from samples
// during the walk. It excludes fixture and consumer storage, and checks that
// the 100,000-row peak is no more than twice the 1,000-row peak.
//
// # Profiling (profile-first workflow)
//
// The profile-first rule is enforced by tooling, not discipline alone: never
// optimize a site you
// have not first seen in a profile. task bench:profile writes mem.prof +
// cpu.prof and the test binary to the gitignored profiles/ dir, then prints
// the top allocation sites:
//
//	task bench:profile                            # alloc-dense micro BulkWalk
//	FANOUT=1 task bench:profile                   # fan-out walk workload
//
// Attribute allocations / CPU to concrete functions:
//
//	go tool pprof -top -alloc_objects profiles/bench.test profiles/mem.prof
//	go tool pprof -list 'decodeOID|NewOID' profiles/bench.test profiles/mem.prof
//	go tool pprof -http=: profiles/bench.test profiles/cpu.prof
//
// On the committed baseline the BulkWalk mem profile attributes ~37% of all
// allocations to decodeOID + NewOID (the per-varbind OID slice is built once,
// then defensively copied — a pooling target), with cloneBytes, nullVarbinds,
// and reactor.register accounting for most of the remainder. Re-run after any
// optimisation to confirm the targeted site actually shrank (the before/after
// profile the profile-first rule demands).
//
// # Committed baseline and the no-consumer premise
//
// testdata/baseline-micro.txt is a committed, benchstat-format snapshot of
// the FlowSeer arm of the micro suite (task bench:micro COUNT=10). It is the
// fixed reference every later change is measured against — task bench:gate
// diffs a fresh run against it, and task bench:stat summarizes it. Refreshing
// the baseline is a deliberate, reviewed commit (re-run the command, commit
// the new .txt); it is never automatic, so a regression cannot silently
// rebaseline.
//
// Premise: as of this baseline the common/snmp package has no production
// consumer — only generated MIB code imports it and the integration poller is
// a stub. These benchmarks therefore measure library-isolated potential, not
// validated production pain. See the "Committed baseline & premise" section of
// docs/benchmarks/2026-06-16-snmp-native-vs-gosnmp-vs-netsnmp.md for the full
// premise and the determinism rationale behind the perf-gate.
//
// # Perf-gate
//
// task bench:gate (implemented by bench-gate.sh) makes the baseline an
// enforced invariant. It runs the FlowSeer-arm micro suite, benchstat-compares
// against testdata/baseline-micro.txt, and hard-fails only on a statistically
// significant regression in the deterministic metrics — allocs/op and B/op.
// ns/op is advisory by default (GATE_NS=1 makes it hard locally); the
// throughput/GC/RSS metrics from bench:fanout and bench:gc are never gated
// here. Gating keys off benchstat's own significance verdict, so high-variance
// benchmarks (ColdStart, ±680% ns/op) do not false-trip.
//
//	task bench:gate                  # full suite, COUNT=10
//	BENCH=Get task bench:gate        # gate one benchmark (fast)
//	GATE_NS=1 task bench:gate        # also hard-fail ns/op (local hardware only)
//
// To refresh the baseline after an intended change: re-run task bench:micro
// COUNT=10, keep the FlowSeer arm (grep impl=flowseer plus the goos/goarch/
// pkg/cpu preamble), and commit the new testdata/baseline-micro.txt. The gate
// never rewrites the baseline. A pre-commit hook (.githooks/pre-commit) runs
// the gate on common/snmp/ changes when opted in with SNMP_PERF_GATE=1.
package bench
