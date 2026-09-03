//go:build snmp_bench_gc

package bench

// gcprofile_test.go — GC-pressure and idle-footprint instrumentation.
// It answers two questions the throughput harness cannot:
//
//   - Idle footprint: what does an *idle* session cost in live memory?
//     This is the number any receive-buffer pool must not regress —
//     pooling that cuts fan-out allocs but inflates the per-session resting
//     cost is a bad trade. TestIdleFootprintScaling measures the marginal
//     per-session live-heap cost and asserts it stays positive and monotonic.
//
//   - GC pressure: over a sustained fan-out walk, what is the allocation
//     rate, GC frequency, and pause time? BenchmarkGCSustained reports these
//     as advisory custom metrics (they are inherently noisy, so the perf-gate
//     treats them as advisory, never build-breaking).
//
// Build-tagged (snmp_bench_gc); drive it with `task bench:gc`. The session
// helpers are shared with the fan-out harness via fanout_helpers_test.go.
//
// Implementation choice: testing.B +
// runtime.ReadMemStats + b.ReportMetric is sufficient for the sustained-run
// metrics; no custom runner is needed. Idle footprint is a plain Test because
// it carries sanity assertions rather than a per-op number.

import (
	"runtime"
	"syscall"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

const mib = 1 << 20

// maxRSSBytes returns the process's peak resident set size. ru_maxrss is in
// bytes on darwin and in kilobytes on linux; normalize to bytes. It is a
// high-water mark, not the instantaneous RSS, so it is reported only as an
// advisory gauge alongside the runtime heap deltas that actually drive the
// pooling decision.
func maxRSSBytes() uint64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	if runtime.GOOS == "linux" {
		return uint64(ru.Maxrss) * 1024
	}
	return uint64(ru.Maxrss)
}

// idleHeap opens n sessions against the shared responder, warms each once,
// lets them settle, forces a GC, and returns the live heap and stack in use
// while they are still referenced. A single shared responder keeps the
// peer-side cost fixed across n, so the slope across n isolates the client
// session's resting cost. n==0 returns the runtime baseline.
func idleHeap(tb testing.TB, addr string, n int) (heapInuse, stackInuse uint64) {
	tb.Helper()
	sessions := make([]snmp.Session, 0, n)
	for i := 0; i < n; i++ {
		sessions = append(sessions, dialWarmed(tb, addr, getOp))
	}

	// Force two GCs so finalizers from any prior batch are fully reclaimed
	// before snapshotting the live heap.
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	runtime.KeepAlive(sessions)

	for _, s := range sessions {
		_ = s.Close()
	}
	return ms.HeapInuse, ms.StackInuse
}

// TestIdleFootprintScaling reports per-session idle live-heap cost and asserts
// it is non-zero and grows monotonically with the session count. The
// assertions are sanity bounds, not fixed thresholds — the absolute numbers
// are hardware- and allocator-dependent and are logged for the record.
func TestIdleFootprintScaling(t *testing.T) {
	addr := startResponder(t)
	counts := []int{0, 64, 256}
	heaps := make([]uint64, len(counts))
	for i, n := range counts {
		h, s := idleHeap(t, addr, n)
		heaps[i] = h
		t.Logf("idle N=%-4d heapInuse=%6.2f MiB stackInuse=%6.2f MiB",
			n, float64(h)/mib, float64(s)/mib)
	}

	for i := 1; i < len(heaps); i++ {
		if heaps[i] <= heaps[i-1] {
			t.Errorf("idle heap not monotonic in session count: N=%d heap=%d <= N=%d heap=%d",
				counts[i], heaps[i], counts[i-1], heaps[i-1])
		}
	}

	last := len(counts) - 1
	perSession := (float64(heaps[last]) - float64(heaps[last-1])) / float64(counts[last]-counts[last-1])
	t.Logf("marginal idle heap ≈ %.0f B/session (N=%d→%d)", perSession, counts[last-1], counts[last])
	if perSession <= 0 {
		t.Errorf("marginal per-session idle heap not positive: %.0f B", perSession)
	}
}

// BenchmarkGCSustained drives a sustained concurrent BulkWalk load and reports
// allocation rate, GC frequency, mean GC pause, and peak RSS over the run.
// All four are advisory metrics; the deterministic allocs/op + B/op
// from b.ReportAllocs are what the perf-gate enforces.
func BenchmarkGCSustained(b *testing.B) {
	const conc = 32
	sessions := fanoutSessions(b, conc, walkOp)

	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	b.ReportAllocs()
	b.ResetTimer()
	driveFanout(b, sessions, walkOp)
	b.StopTimer()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	secs := b.Elapsed().Seconds()
	numGC := after.NumGC - before.NumGC
	if secs > 0 {
		b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/secs/mib, "MB-alloc/s")
		b.ReportMetric(float64(numGC)/secs, "GC/s")
	}
	if numGC > 0 {
		b.ReportMetric(float64(after.PauseTotalNs-before.PauseTotalNs)/float64(numGC), "ns-pause/GC")
	}
	b.ReportMetric(float64(maxRSSBytes())/mib, "rss-max-MiB")
}

// TestBurstyAllocRate checks the bursty/idle regime: a wake → burst → idle
// cycle should allocate materially during the active window and ~nothing
// while idle. It asserts the active-window allocation rate is strictly
// greater than the idle-window rate (a sanity bound, not a fixed threshold),
// catching a regression where idle sessions leak steady allocations.
func TestBurstyAllocRate(t *testing.T) {
	const (
		conc     = 16
		burstOps = 8
		idleGap  = 50 * time.Millisecond
	)
	addr := startResponder(t)

	sessions := make([]snmp.Session, conc)
	for i := range sessions {
		sessions[i] = dialWarmed(t, addr, getOp)
	}
	t.Cleanup(func() {
		for _, s := range sessions {
			_ = s.Close()
		}
	})

	alloc := func() uint64 {
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return ms.TotalAlloc
	}

	// Active burst window.
	a0 := alloc()
	burstStart := time.Now()
	done := make(chan error, conc)
	for _, s := range sessions {
		go func(s snmp.Session) {
			for j := 0; j < burstOps; j++ {
				if err := walkOp(s); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(s)
	}
	for range sessions {
		if err := <-done; err != nil {
			t.Errorf("burst walk: %v", err)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	burstRate := float64(alloc()-a0) / time.Since(burstStart).Seconds()

	// Idle window.
	i0 := alloc()
	idleStart := time.Now()
	time.Sleep(idleGap)
	idleRate := float64(alloc()-i0) / time.Since(idleStart).Seconds()

	t.Logf("burst alloc rate=%.1f MiB/s  idle alloc rate=%.3f MiB/s", burstRate/mib, idleRate/mib)
	if burstRate <= idleRate {
		t.Errorf("expected burst alloc rate > idle rate; got burst=%.1f idle=%.1f B/s", burstRate, idleRate)
	}
}
