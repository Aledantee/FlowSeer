//go:build snmp_bench_fanout || snmp_bench_gc || snmp_bench_netsnmp

package bench

// fanout_helpers_test.go holds the fan-out building blocks shared by the
// throughput harness (fanout_test.go, tag snmp_bench_fanout), the
// GC/idle-footprint harness (gcprofile_test.go, tag snmp_bench_gc), and the
// 3-way throughput/target sweep (sweep_test.go, tag snmp_bench_netsnmp). The
// build constraint makes them visible under any of those tags without
// duplicating the re-dial-tolerant session setup.

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// getOp performs one scalar Get — the lightest op, so its throughput is the
// cleanest high-rate signal.
func getOp(s snmp.Session) error {
	_, err := s.Get(context.Background(), []snmp.OID{scalarSnmpOID})
	return err
}

// walkOp performs one full ifTable BulkWalk and checks the row count, so a
// fan-out walk that silently truncates under load is caught.
func walkOp(s snmp.Session) error {
	w := s.BulkWalk(context.Background(), ifTableSnmp)
	n := 0
	for range w.Iter() {
		n++
	}
	if err := w.Err(); err != nil {
		return err
	}
	if n != benchRows*2 {
		return fmt.Errorf("walk returned %d rows, want %d", n, benchRows*2)
	}
	return nil
}

// dialWarmed dials a session against addr and runs warmup on it, re-dialing up
// to coldRedials times to absorb the documented unconnected-socket first-reply
// drop (see micro_test.go). It is the one home of that retry contract, shared
// by every bench harness that needs a warmed session (fanoutSessions, the GC
// idle/burst harnesses). It takes testing.TB so both *testing.B and *testing.T
// callers can use it.
func dialWarmed(tb testing.TB, addr string, warmup func(snmp.Session) error) snmp.Session {
	tb.Helper()
	var lastErr error
	for attempt := 0; attempt < coldRedials; attempt++ {
		sess := dialNative(tb, addr)
		if lastErr = warmup(sess); lastErr == nil {
			return sess
		}
		_ = sess.Close()
	}
	tb.Fatalf("warm session against %s after %d re-dials: %v", addr, coldRedials, lastErr)
	return nil // unreachable: Fatalf stops the goroutine
}

// fanoutSessions builds conc warmed sessions, each against its OWN responder
// instance, and registers their cleanup. One responder per session is the
// honest fleet-polling model: N independent client→device pipes. A single
// shared responder would funnel every datagram through one UDP socket (the
// kernel serializes recv on a socket), making the responder — not the client
// fan-out — the throughput ceiling.
func fanoutSessions(b *testing.B, conc int, warmup func(snmp.Session) error) []snmp.Session {
	sessions := make([]snmp.Session, conc)
	for i := range sessions {
		sessions[i] = dialWarmed(b, startResponder(b), warmup)
	}
	b.Cleanup(func() {
		for _, s := range sessions {
			_ = s.Close()
		}
	})
	return sessions
}

// driveFanout spreads b.N operations across the given sessions, one goroutine
// per session, claiming work units cooperatively so a slow session does not
// stall the others. It runs entirely within whatever timer state the caller
// has set, and fails the benchmark if any op errors. Callers wrap it with
// ResetTimer/StopTimer (and, for the GC harness, MemStats snapshots).
func driveFanout(b *testing.B, sessions []snmp.Session, op func(snmp.Session) error) {
	var (
		wg       sync.WaitGroup
		claimed  atomic.Int64
		failures atomic.Int64
		total    = int64(b.N)
	)
	for i := range sessions {
		wg.Add(1)
		go func(s snmp.Session) {
			defer wg.Done()
			for claimed.Add(1) <= total {
				if err := op(s); err != nil {
					failures.Add(1)
					return
				}
			}
		}(sessions[i])
	}
	wg.Wait()
	if f := failures.Load(); f > 0 {
		b.Fatalf("%d operations failed across %d sessions", f, len(sessions))
	}
}
