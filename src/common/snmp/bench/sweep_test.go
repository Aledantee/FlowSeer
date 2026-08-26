//go:build snmp_bench_netsnmp

package bench

// sweep_test.go — the throughput × target-count sweep. It drives the same
// ifTable BulkWalk through all three SNMP clients — FlowSeer (snmp.NewSession),
// gosnmp, and the Net-SNMP C library via the in-process cgo binding
// (netsnmp_native.go, NOT the CLI) — across a range of concurrent target
// counts, and reports achieved throughput. Two axes, per the request:
//
//   - target count: BenchmarkSweep scales the number of concurrent sessions
//     (each against its OWN loopback responder — N independent client→device
//     pipes) and reports saturation ops/sec per client at each fan-out point.
//   - offered throughput: TestSweepOfferedRate paces each target at a fixed
//     walks/sec and reports achieved-vs-offered ops/sec and mean latency, so
//     each client's behaviour under sub-saturation load is visible.
//
// Build-tagged snmp_bench_netsnmp (needs cgo + libnetsnmp). Drive with
// `task bench:sweep`.
//
// Allocation caveat: b.ReportAllocs counts only Go-heap allocations, so the
// allocs/op and B/op columns are real for FlowSeer and gosnmp but NOT for the
// Net-SNMP arm (its allocations are C-side mallocs, invisible to the Go
// allocator). Compare Net-SNMP on ops/sec and ns/op only.

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// raiseFDLimit best-effort raises the soft open-file limit so the
// collector-scale sweeps (one client socket + one responder socket per target,
// so ~2× the target count) do not hit the default 256-fd ceiling at hundreds
// of targets. It only ever raises toward the hard limit, never lowers.
var raiseFDOnce sync.Once

func raiseFDLimit() {
	raiseFDOnce.Do(func() {
		var lim syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
			return
		}
		want := uint64(16384)
		if lim.Max != 0 && lim.Max < want {
			want = lim.Max
		}
		if lim.Cur < want {
			lim.Cur = want
			_ = syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim)
		}
	})
}

// sweepClient is one SNMP client under test. dial builds a warmed session
// against addr and returns a walk closure (one full ifTable BulkWalk, row
// count checked) plus its closer.
type sweepClient struct {
	name string
	dial func(tb testing.TB, addr string) (walk func() error, closeFn func())
}

// sweepClients is the comparand set. FlowSeer reuses the re-dial-tolerant
// dialWarmed (the unconnected-socket first-reply drop); gosnmp and Net-SNMP
// connect reliably and are warmed with a bounded retry.
var sweepClients = []sweepClient{
	{
		name: "flowseer",
		dial: func(tb testing.TB, addr string) (func() error, func()) {
			sess := dialWarmed(tb, addr, walkOp)
			return func() error { return walkOp(sess) }, func() { _ = sess.Close() }
		},
	},
	{
		name: "gosnmp",
		dial: func(tb testing.TB, addr string) (func() error, func()) {
			c := dialGosnmp(tb, addr)
			walk := func() error {
				res, err := c.BulkWalkAll(ifTableStr)
				if err != nil {
					return err
				}
				if len(res) != benchRows*2 {
					return fmt.Errorf("gosnmp walk returned %d rows, want %d", len(res), benchRows*2)
				}
				return nil
			}
			warmOrFatal(tb, walk)
			return walk, func() { _ = c.Conn.Close() }
		},
	},
	{
		name: "netsnmp",
		dial: func(tb testing.TB, addr string) (func() error, func()) {
			sess, err := nsOpen(addr, "public")
			if err != nil {
				tb.Fatalf("nsOpen: %v", err)
			}
			root := oidSubs(ifTableSnmp)
			walk := func() error {
				// max-rep 50 matches FlowSeer (walkBulkMaxRepetitions) and
				// gosnmp (defaultMaxRepetitions) so all three issue the same
				// number of GETBULK round-trips per walk — a fair comparison.
				n, err := sess.bulkWalk(root, walkBulkMaxRep)
				if err != nil {
					return err
				}
				if n != benchRows*2 {
					return fmt.Errorf("net-snmp walk returned %d rows, want %d", n, benchRows*2)
				}
				return nil
			}
			warmOrFatal(tb, walk)
			return walk, sess.close
		},
	},
}

// warmOrFatal runs one warmup walk, retrying up to coldRedials times to absorb
// a transient first-reply drop, and fails the test if it never succeeds.
func warmOrFatal(tb testing.TB, walk func() error) {
	tb.Helper()
	var err error
	for i := 0; i < coldRedials; i++ {
		if err = walk(); err == nil {
			return
		}
	}
	tb.Fatalf("sweep warmup walk failed after %d tries: %v", coldRedials, err)
}

// sweepDial builds targets warmed walk closures for cl, each against its own
// responder, and registers their cleanup.
func sweepDial(tb testing.TB, cl sweepClient, targets int) []func() error {
	raiseFDLimit()
	walks := make([]func() error, targets)
	closers := make([]func(), targets)
	for i := 0; i < targets; i++ {
		walks[i], closers[i] = cl.dial(tb, startResponder(tb))
	}
	tb.Cleanup(func() {
		for _, c := range closers {
			c()
		}
	})
	return walks
}

// sweepTargets are the fan-out points: 1 is single-session high-rate, the rest
// are concurrent fan-out.
var sweepTargets = []int{1, 8, 64}

// BenchmarkSweep reports saturation throughput (ops/sec) for each client at
// each target count: the maximum aggregate BulkWalk rate the client sustains
// driving N concurrent sessions flat-out.
func BenchmarkSweep(b *testing.B) {
	for _, cl := range sweepClients {
		for _, n := range sweepTargets {
			b.Run(fmt.Sprintf("client=%s/targets=%d", cl.name, n), func(b *testing.B) {
				walks := sweepDial(b, cl, n)
				b.ReportAllocs()
				b.ResetTimer()

				var (
					wg       sync.WaitGroup
					claimed  atomic.Int64
					failures atomic.Int64
					total    = int64(b.N)
				)
				for _, walk := range walks {
					wg.Add(1)
					go func(walk func() error) {
						defer wg.Done()
						for claimed.Add(1) <= total {
							if err := walk(); err != nil {
								failures.Add(1)
								return
							}
						}
					}(walk)
				}
				wg.Wait()
				b.StopTimer()

				if f := failures.Load(); f > 0 {
					b.Fatalf("%d walks failed at targets=%d", f, n)
				}
				if secs := b.Elapsed().Seconds(); secs > 0 {
					b.ReportMetric(float64(b.N)/secs, "ops/s")
				}
			})
		}
	}
}

// saturationTargets sweeps into collector scale: a production poller fans out
// to hundreds of devices, so the region that matters is well past 64.
var saturationTargets = []int{1, 64, 256, 512, 1024}

// TestSweepSaturation measures max sustained throughput (ops/sec) per client at
// each target count, building the sessions once and driving them flat-out for
// a fixed window. Unlike BenchmarkSweep it does not rebuild sessions per b.N
// step, so it scales to the 1024-target collector regime without quadratic
// setup cost. This is where Net-SNMP's one-OS-thread-per-blocked-session model
// is expected to diverge from the Go clients' netpoller multiplexing.
func TestSweepSaturation(t *testing.T) {
	if testing.Short() {
		t.Skip("collector-scale saturation sweep skipped in -short")
	}
	const window = 2 * time.Second
	for _, cl := range sweepClients {
		for _, n := range saturationTargets {
			t.Run(fmt.Sprintf("%s/targets=%d", cl.name, n), func(t *testing.T) {
				walks := sweepDial(t, cl, n)

				var (
					wg        sync.WaitGroup
					done      = make(chan struct{})
					completed atomic.Int64
					failed    atomic.Int64
					latNanos  atomic.Int64
				)
				start := time.Now()
				for _, walk := range walks {
					wg.Add(1)
					go func(walk func() error) {
						defer wg.Done()
						for {
							select {
							case <-done:
								return
							default:
							}
							t0 := time.Now()
							if err := walk(); err != nil {
								failed.Add(1)
								return
							}
							latNanos.Add(int64(time.Since(t0)))
							completed.Add(1)
						}
					}(walk)
				}
				time.Sleep(window)
				close(done)
				wg.Wait()
				elapsed := time.Since(start).Seconds()

				if f := failed.Load(); f > 0 {
					t.Fatalf("%d walks failed at targets=%d", f, n)
				}
				ops := completed.Load()
				var meanLatMs float64
				if ops > 0 {
					meanLatMs = float64(latNanos.Load()) / float64(ops) / 1e6
				}
				t.Logf("targets=%-4d %8.0f ops/s  mean-latency=%6.2f ms  (%d walks in %.1fs)",
					n, float64(ops)/elapsed, meanLatMs, ops, elapsed)
			})
		}
	}
}

// TestSweepOfferedRate drives the offered-throughput axis: each target is
// paced to a fixed walks/sec for a fixed window, and the test logs the
// achieved aggregate ops/sec (vs offered) and mean per-walk latency for each
// client. Below saturation, achieved ≈ offered and latency is the signal;
// above it, achieved plateaus — exposing each client's ceiling under load.
func TestSweepOfferedRate(t *testing.T) {
	const window = 2 * time.Second
	targets := []int{8, 64}
	ratesPerTarget := []int{50, 250} // walks/sec/target

	for _, cl := range sweepClients {
		for _, n := range targets {
			for _, rate := range ratesPerTarget {
				name := fmt.Sprintf("%s/targets=%d/rate=%d", cl.name, n, rate)
				t.Run(name, func(t *testing.T) {
					walks := sweepDial(t, cl, n)

					var (
						wg        sync.WaitGroup
						done      = make(chan struct{})
						completed atomic.Int64
						failed    atomic.Int64
						latNanos  atomic.Int64
					)
					interval := time.Second / time.Duration(rate)
					start := time.Now()
					for _, walk := range walks {
						wg.Add(1)
						go func(walk func() error) {
							defer wg.Done()
							tick := time.NewTicker(interval)
							defer tick.Stop()
							for {
								select {
								case <-done:
									return
								case <-tick.C:
									t0 := time.Now()
									if err := walk(); err != nil {
										failed.Add(1)
										return
									}
									latNanos.Add(int64(time.Since(t0)))
									completed.Add(1)
								}
							}
						}(walk)
					}
					time.Sleep(window)
					close(done)
					wg.Wait()
					elapsed := time.Since(start).Seconds()

					if f := failed.Load(); f > 0 {
						t.Fatalf("%d walks failed", f)
					}
					ops := completed.Load()
					offered := float64(n * rate)
					achieved := float64(ops) / elapsed
					var meanLatMs float64
					if ops > 0 {
						meanLatMs = float64(latNanos.Load()) / float64(ops) / 1e6
					}
					t.Logf("offered=%.0f ops/s  achieved=%.0f ops/s (%.0f%%)  mean-latency=%.2f ms  (n=%d targets, %d walks)",
						offered, achieved, 100*achieved/offered, meanLatMs, n, ops)
				})
			}
		}
	}
}
