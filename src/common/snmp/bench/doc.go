// Package bench compares FlowSeer SNMP clients with gosnmp and Net-SNMP.
// Its separate Go module keeps benchmark dependencies out of the main module.
// Run commands from this directory:
//
//	cd src/common/snmp/bench
//	go test -run '^$' -bench Get -benchmem -count 10 > new.txt
//	benchstat -col /impl new.txt
//
// # Local benchmarks
//
// The default suite compares v2c Get, GetNext, GetBulk, and BulkWalk against
// a loopback UDP responder. BenchmarkTableWalk measures generated IF-MIB row
// assembly; BenchmarkDecodeFallback measures decoder success and error paths.
// Steady-state benchmarks open and warm one session before resetting the timer.
// BenchmarkColdStart includes opening a session and its first Get, with bounded
// redials for transient failures. The responder shares the benchmark process,
// so its allocations contribute to B/op and allocs/op.
//
// Optional build tags select the larger local workloads:
//
//   - snmp_bench_fanout: concurrent sessions and requests per session.
//   - snmp_bench_gc: idle heap and sustained allocation measurements.
//   - snmp_bench_netsnmp: the native Net-SNMP comparison and target-count sweep.
//
// The native comparison requires cgo and libnetsnmp headers and libraries.
// Its default search paths use Homebrew's net-snmp prefix; other installations
// can supply CGO_CFLAGS and CGO_LDFLAGS. Go allocation metrics exclude C malloc
// traffic, so compare the Net-SNMP arm using throughput and latency.
//
// # Live-agent benchmarks
//
// The macro tier requires the snmp_bench_macro tag and FLOWSEER_BENCH_AGENT.
// It skips when the agent is unset. For a configured agent:
//
//	FLOWSEER_BENCH_AGENT=127.0.0.1:1161 go test -tags snmp_bench_macro -run TestMacro -bench Macro -benchmem
//
// FLOWSEER_BENCH_COMMUNITY defaults to public; FLOWSEER_BENCH_WALK_ROOT defaults
// to ifTable. FLOWSEER_BENCH_V3_USER enables the v3 engine-discovery benchmark;
// macro_test.go documents the authentication and privacy settings. TestMacroNetSnmp
// also requires hyperfine, snmpget, and snmpbulkwalk. Its timings include process
// startup on every invocation and are wall-clock CLI measurements.
//
// # Profiles and baseline
//
// Capture a profile before changing an allocation or CPU hot spot:
//
//	go test -run '^$' -bench 'BulkWalk/impl=flowseer' -benchtime=2s -memprofile mem.prof -cpuprofile cpu.prof -o bench.test
//	go tool pprof -top -alloc_objects bench.test mem.prof
//
// testdata/baseline-micro.txt contains a fixed FlowSeer benchmark snapshot.
// bench-gate.sh compares fresh FlowSeer samples with it through benchstat.
// Its intended hard metrics are B/op and allocs/op; GATE_NS=1 also gates ns/op
// on comparable local hardware. Refreshing the baseline requires a reviewed
// change; the gate never rewrites it. DecodeFallback has no baseline entry.
// The Taskfile.yml in this directory provides longer benchmark and profile runs.
package bench
