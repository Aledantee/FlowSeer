//go:build snmp_bench_fanout

package bench

// fanout_test.go — the concurrency / fan-out throughput harness.
// Where micro_test.go measures one serial session, this drives N
// concurrent snmp.Sessions against the shared loopback responder and reports
// aggregate ops/sec, so "throughput under fan-out" — the metric that matters
// for fleet polling and had no harness before — becomes measurable.
//
// It is build-tagged (snmp_bench_fanout) so it never runs in the default
// suite; drive it with `task bench:fanout`. The same file carries the
// demux-under-concurrency correctness test (TestFanoutNoCrossDelivery), which
// guards the reactor's request-id correlation when many requests are in
// flight at once.
//
// Three regimes:
//   - single-session high-rate — the conc=1 point of the throughput benches.
//   - massive concurrent fan-out — the high conc points (64, 256).
//   - bursty / idle — BenchmarkFanoutBurst alternates active bursts with idle
//     gaps; its allocation/GC behaviour at the cycle boundary is instrumented
//     by the gcprofile harness.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

// getOp, walkOp, fanoutSessions, and driveFanout live in
// fanout_helpers_test.go so the GC harness can share them.

// fanoutThroughput builds conc warmed sessions (each on its own responder),
// then spreads b.N operations across conc goroutines (one session each) and
// reports aggregate ops/sec. Session construction and the one-op warmup are
// untimed.
func fanoutThroughput(b *testing.B, conc int, op func(snmp.Session) error) {
	sessions := fanoutSessions(b, conc, op)

	b.ReportAllocs()
	b.ResetTimer()
	driveFanout(b, sessions, op)
	b.StopTimer()

	if secs := b.Elapsed().Seconds(); secs > 0 {
		b.ReportMetric(float64(b.N)/secs, "ops/s")
	}
}

// fanoutConc are the concurrency points: 1 is the degenerate single-session
// high-rate regime, the rest are the fan-out regime.
var fanoutConc = []int{1, 8, 64, 256}

// BenchmarkFanoutGet measures aggregate scalar-Get throughput as concurrency
// scales — the high-rate / fan-out throughput curve.
func BenchmarkFanoutGet(b *testing.B) {
	for _, conc := range fanoutConc {
		b.Run(fmt.Sprintf("conc=%d", conc), func(b *testing.B) {
			fanoutThroughput(b, conc, getOp)
		})
	}
}

// BenchmarkFanoutWalk measures aggregate BulkWalk throughput under fan-out —
// the heavier, allocation-dense op the pooling units target.
func BenchmarkFanoutWalk(b *testing.B) {
	for _, conc := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("conc=%d", conc), func(b *testing.B) {
			fanoutThroughput(b, conc, walkOp)
		})
	}
}

// BenchmarkFanoutBurst drives the bursty/idle regime: each iteration wakes
// conc sessions for a short burst of walks, then lets them idle. It reports
// in-burst ops/sec (idle time excluded from the rate) so the active
// throughput is comparable to the steady-state benches while the GC harness
// watches the allocation spike at the wake boundary.
func BenchmarkFanoutBurst(b *testing.B) {
	const (
		conc     = 64
		burstOps = 8 // walks per session per burst
		idleGap  = 2 * time.Millisecond
	)
	sessions := fanoutSessions(b, conc, getOp)

	b.ReportAllocs()
	b.ResetTimer()

	var active time.Duration
	var failures atomic.Int64
	for i := 0; i < b.N; i++ {
		start := time.Now()
		var wg sync.WaitGroup
		for _, s := range sessions {
			wg.Add(1)
			go func(s snmp.Session) {
				defer wg.Done()
				for j := 0; j < burstOps; j++ {
					if err := walkOp(s); err != nil {
						failures.Add(1)
						return
					}
				}
			}(s)
		}
		wg.Wait()
		active += time.Since(start)
		b.StopTimer()
		time.Sleep(idleGap) // idle window — excluded from the timed rate
		b.StartTimer()
	}

	if f := failures.Load(); f > 0 {
		b.Fatalf("%d burst operations failed", f)
	}
	if active.Seconds() > 0 {
		b.ReportMetric(float64(b.N*conc*burstOps)/active.Seconds(), "ops/s")
	}
}

// TestFanoutNoCrossDelivery is the demux-under-concurrency guard: many
// sessions each fire many *concurrent, distinct* Gets, and every reply must
// carry the OID it asked for. A broken request-id correlation would surface
// here as a reply OID that does not match its request — the reactor's
// validate-before-deliver / single-use-waiter invariant under load.
func TestFanoutNoCrossDelivery(t *testing.T) {
	addr := startResponder(t)
	ctx := context.Background()

	const (
		nSessions  = 16
		perSession = benchRows // one distinct ifDescr row per request
	)

	errs := make(chan error, nSessions*perSession)
	var outer sync.WaitGroup
	for sIdx := 0; sIdx < nSessions; sIdx++ {
		outer.Add(1)
		go func() {
			defer outer.Done()
			sess := dialNative(t, addr)
			defer sess.Close()
			if _, err := sess.Get(ctx, []snmp.OID{scalarSnmpOID}); err != nil {
				errs <- fmt.Errorf("warmup: %w", err)
				return
			}
			var inner sync.WaitGroup
			for row := 1; row <= perSession; row++ {
				inner.Add(1)
				go func(row int) {
					defer inner.Done()
					oid := snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 2, uint32(row))
					vbs, err := sess.Get(ctx, []snmp.OID{oid})
					if err != nil {
						errs <- fmt.Errorf("row %d: %w", row, err)
						return
					}
					if len(vbs) != 1 {
						errs <- fmt.Errorf("row %d: got %d varbinds, want 1", row, len(vbs))
						return
					}
					if got := vbs[0].GetHeader().OID; !got.Equal(oid) {
						errs <- fmt.Errorf("row %d: reply OID %s != requested %s (cross-delivery)", row, got, oid)
						return
					}
				}(row)
			}
			inner.Wait()
		}()
	}
	outer.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
