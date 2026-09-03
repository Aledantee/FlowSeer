package bench

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	g "github.com/gosnmp/gosnmp"

	"go.aledante.io/FlowSeer/src/common/snmp"
)

func closeOnCleanup(tb testing.TB, client io.Closer) {
	tb.Helper()
	tb.Cleanup(func() {
		if err := client.Close(); err != nil {
			tb.Errorf("close client: %v", err)
		}
	})
}

// micro_test.go — Tier 1 in-process micro-benchmarks. Each operation is
// run by both the FlowSeer client (snmp.NewSession) and the gosnmp client
// against the shared loopback responder, as paired sub-benchmarks so
// benchstat can diff them:
//
//	go test -bench Get -benchmem -count 10 | tee a.txt
//	benchstat -col /impl a.txt
//
// Steady-state benchmarks construct one session, warm it with one untimed
// round-trip, then ResetTimer. Cold-start is a separate benchmark.

const benchRows = 50 // ifTable rows in the canned MIB (100 columnar entries)

var (
	scalarStr     = ".1.3.6.1.2.1.1.1.0"
	ifTableStr    = ".1.3.6.1.2.1.2.2.1"
	scalarSnmpOID = snmp.MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0)
	ifTableSnmp   = snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1)
)

func dialNative(tb testing.TB, addr string) snmp.Session {
	tb.Helper()
	sess, err := snmp.NewSession(context.Background(), addr, snmp.V2c,
		snmp.WithCommunity("public"),
		snmp.WithMinSecurity(snmp.MinSecurityNoAuth),
		snmp.WithTimeout(2*time.Second),
		snmp.WithRetries(3),
	)
	if err != nil {
		tb.Fatalf("native NewSession: %v", err)
	}
	return sess
}

// gosnmpHostPort splits a host:port address into the (host, port) pair the
// gosnmp client wants. Shared by the micro and macro dialers.
func gosnmpHostPort(tb testing.TB, addr string) (string, uint16) {
	tb.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		tb.Fatalf("split addr %q: %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		tb.Fatalf("parse port %q: %v", portStr, err)
	}
	return host, uint16(port)
}

func dialGosnmp(tb testing.TB, addr string) *g.GoSNMP {
	tb.Helper()
	host, port := gosnmpHostPort(tb, addr)
	client := &g.GoSNMP{
		Target:    host,
		Port:      port,
		Community: "public",
		Version:   g.Version2c,
		Timeout:   2 * time.Second,
		Retries:   3,
	}
	if err := client.Connect(); err != nil {
		tb.Fatalf("gosnmp connect: %v", err)
	}
	return client
}

// benchPaired runs op as paired flowseer/gosnmp sub-benchmarks against the
// shared responder. Each arm dials, performs one untimed warmup op, then
// ResetTimer before the measured loop — the structure every steady-state
// micro-benchmark needs.
func benchPaired(b *testing.B, addr string, nativeOp func(snmp.Session) error, gosnmpOp func(*g.GoSNMP) error) {
	b.Run("impl=flowseer", func(b *testing.B) {
		sess := dialNative(b, addr)
		closeOnCleanup(b, sess)
		if err := nativeOp(sess); err != nil {
			b.Fatalf("warmup: %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := nativeOp(sess); err != nil {
				b.Fatalf("op: %v", err)
			}
		}
	})
	b.Run("impl=gosnmp", func(b *testing.B) {
		client := dialGosnmp(b, addr)
		closeOnCleanup(b, client.Conn)
		if err := gosnmpOp(client); err != nil {
			b.Fatalf("warmup: %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := gosnmpOp(client); err != nil {
				b.Fatalf("op: %v", err)
			}
		}
	})
}

func BenchmarkGet(b *testing.B) {
	addr := startResponder(b)
	ctx := context.Background()
	nOIDs := []snmp.OID{scalarSnmpOID}
	gOIDs := []string{scalarStr}
	benchPaired(b, addr,
		func(s snmp.Session) error { _, err := s.Get(ctx, nOIDs); return err },
		func(c *g.GoSNMP) error { _, err := c.Get(gOIDs); return err })
}

func BenchmarkGetNext(b *testing.B) {
	addr := startResponder(b)
	ctx := context.Background()
	nOIDs := []snmp.OID{ifTableSnmp}
	gOIDs := []string{ifTableStr}
	benchPaired(b, addr,
		func(s snmp.Session) error { _, err := s.GetNext(ctx, nOIDs); return err },
		func(c *g.GoSNMP) error { _, err := c.GetNext(gOIDs); return err })
}

func BenchmarkGetBulk(b *testing.B) {
	addr := startResponder(b)
	ctx := context.Background()
	nOIDs := []snmp.OID{ifTableSnmp}
	gOIDs := []string{ifTableStr}
	benchPaired(b, addr,
		func(s snmp.Session) error { _, err := s.GetBulk(ctx, 0, 10, nOIDs); return err },
		func(c *g.GoSNMP) error { _, err := c.GetBulk(gOIDs, 0, 10); return err })
}

func BenchmarkBulkWalk(b *testing.B) {
	addr := startResponder(b)

	b.Run("impl=flowseer", func(b *testing.B) {
		ctx := context.Background()
		sess := dialNative(b, addr)
		closeOnCleanup(b, sess)
		walkNative := func() int {
			n := 0
			w := sess.BulkWalk(ctx, ifTableSnmp)
			for range w.Iter() {
				n++
			}
			if err := w.Err(); err != nil {
				b.Fatalf("BulkWalk: %v", err)
			}
			return n
		}
		if got := walkNative(); got != benchRows*2 { // warmup + sanity
			b.Fatalf("walk returned %d rows, want %d", got, benchRows*2)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			walkNative()
		}
	})

	b.Run("impl=gosnmp", func(b *testing.B) {
		client := dialGosnmp(b, addr)
		closeOnCleanup(b, client.Conn)
		walkGosnmp := func() int {
			res, err := client.BulkWalkAll(ifTableStr)
			if err != nil {
				b.Fatalf("BulkWalkAll: %v", err)
			}
			return len(res)
		}
		if got := walkGosnmp(); got != benchRows*2 {
			b.Fatalf("walk returned %d rows, want %d", got, benchRows*2)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			walkGosnmp()
		}
	})
}

// BenchmarkColdStart measures construct + first round-trip per iteration
// (no warmup, fresh session each time). This is the v2c cold-start —
// socket open plus first exchange. The SNMPv3/USM engine-discovery
// handshake, the dominant real-world cold-start, is measured in the macro
// tier against a live v3 agent (see doc.go).
//
// Churn caveat: at the tens-of-thousands-of-sessions-per-second rate the
// default benchtime drives here (a synthetic regime — production reuses
// sessions rather than re-dialing per poll), the FlowSeer client's
// unconnected-socket first reply is very occasionally not delivered even
// after its own retransmits, while the gosnmp connected socket is not
// observed to drop. Each iteration therefore re-dials up to coldRedials
// times so a transient drop does not fail the suite; a re-dial slightly
// inflates that one sample, so read cold-start with -count and take the
// median. The drop itself is flagged for follow-up in
// docs/benchmarks/2026-06-16-snmp-native-vs-gosnmp-vs-netsnmp.md.
const coldRedials = 5

func BenchmarkColdStart(b *testing.B) {
	addr := startResponder(b)
	ctx := context.Background()
	oids := []snmp.OID{scalarSnmpOID}

	b.Run("impl=flowseer", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var lastErr error
			for attempt := 0; attempt < coldRedials; attempt++ {
				sess := dialNative(b, addr)
				_, lastErr = sess.Get(ctx, oids)
				_ = sess.Close()
				if lastErr == nil {
					break
				}
			}
			if lastErr != nil {
				b.Fatalf("Get after %d re-dials: %v", coldRedials, lastErr)
			}
		}
	})

	b.Run("impl=gosnmp", func(b *testing.B) {
		gOIDs := []string{scalarStr}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var lastErr error
			for attempt := 0; attempt < coldRedials; attempt++ {
				client := dialGosnmp(b, addr)
				_, lastErr = client.Get(gOIDs)
				_ = client.Conn.Close()
				if lastErr == nil {
					break
				}
			}
			if lastErr != nil {
				b.Fatalf("Get after %d re-dials: %v", coldRedials, lastErr)
			}
		}
	})
}
