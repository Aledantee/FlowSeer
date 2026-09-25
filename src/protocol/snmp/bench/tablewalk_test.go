package bench

import (
	"context"
	"fmt"
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/snmp"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
)

// BenchmarkTableWalk measures the generated-bindings table walk end to
// end: session BulkWalk + generated row assembly + typed column decode.
// This is the layer the mibgen fused-decode path optimizes; the
// session-level BenchmarkBulkWalk deliberately excludes it.
func BenchmarkTableWalk(b *testing.B) {
	addr := startResponder(b)

	b.Run("impl=flowseer", func(b *testing.B) {
		ctx := context.Background()
		sess := dialNative(b, addr)
		closeOnCleanup(b, sess)

		walkTable := func() int {
			n := 0
			tw := ifmib.IfTable.Walk(ctx, sess, ifmib.IfInOctets, ifmib.IfOutOctets)
			for _, row := range tw.Iter() {
				if row.IfInOctets == 0 && row.IfOutOctets == 0 {
					b.Fatal("row decoded to zero counters")
				}
				n++
			}
			if err := tw.Err(); err != nil {
				b.Fatalf("table walk: %v", err)
			}
			return n
		}
		if got := walkTable(); got != benchRows {
			b.Fatalf("walk returned %d rows, want %d", got, benchRows)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			walkTable()
		}
	})
}

func BenchmarkTableWalkScale(b *testing.B) {
	for _, rows := range []int{50, 1000, 10000, 100000} {
		b.Run(fmt.Sprintf("rows=%d", rows), func(b *testing.B) {
			addr, counts := startResponderMIB(b, buildScaleMIB(rows, false, 32))
			for _, width := range []int{2, 18} {
				b.Run(fmt.Sprintf("cols=%d", width), func(b *testing.B) {
					cols := scaleColumns(width)
					for _, stop := range []int{0, 1, 100} {
						b.Run(fmt.Sprintf("stop=%d", stop), func(b *testing.B) {
							sess := dialNative(b, addr)
							defer func() { _ = sess.Close() }()
							run := func() {
								w := ifmib.IfTable.Walk(context.Background(), sess, cols...)
								n := 0
								for range w.Iter() {
									n++
									if stop > 0 && n >= stop {
										break
									}
								}
								if w.Err() != nil {
									b.Fatal(w.Err())
								}
								want := rows
								if stop > 0 {
									want = min(rows, stop)
								}
								if n != want {
									b.Fatalf("got %d rows want %d", n, want)
								}
							}
							run()
							counts.requests.Store(0)
							counts.bytes.Store(0)
							b.ReportAllocs()
							b.ResetTimer()
							for range b.N {
								run()
							}
							b.StopTimer()
							b.ReportMetric(float64(counts.requests.Load())/float64(b.N), "requests/op")
							b.ReportMetric(float64(counts.bytes.Load())/float64(b.N), "wire-B/op")
						})
					}
				})
			}
		})
	}
}

func scaleColumns(width int) []snmp.AnyColumn {
	if width == 2 {
		return []snmp.AnyColumn{ifmib.IfInOctets, ifmib.IfOutOctets}
	}
	return []snmp.AnyColumn{ifmib.IfIndex, ifmib.IfDescr, ifmib.IfType, ifmib.IfMTU, ifmib.IfSpeed, ifmib.IfPhysAddress, ifmib.IfAdminStatus, ifmib.IfOperStatus, ifmib.IfLastChange, ifmib.IfInOctets, ifmib.IfInUcastPkts, ifmib.IfInDiscards, ifmib.IfInErrors, ifmib.IfInUnknownProtos, ifmib.IfOutOctets, ifmib.IfOutUcastPkts, ifmib.IfOutDiscards, ifmib.IfOutErrors}
}

func TestScaleColumnsWideFixtureServesEveryColumn(t *testing.T) {
	cols := scaleColumns(18)
	if len(cols) != 18 {
		t.Fatalf("wide scale columns = %d, want 18", len(cols))
	}

	addr, _ := startResponderMIB(t, buildScaleMIB(1, false, 32))
	sess := dialNative(t, addr)
	t.Cleanup(func() { _ = sess.Close() })

	w := ifmib.IfTable.Walk(context.Background(), sess, cols...)
	rows := 0
	for _, row := range w.Iter() {
		rows++
		for _, col := range cols {
			if !row.Observed(col) {
				t.Errorf("column %s was not observed", col.OID())
			}
		}
	}
	if err := w.Err(); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("rows = %d, want 1", rows)
	}
}
