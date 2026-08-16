package bench

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
)

// BenchmarkTableWalk measures the generated-bindings table walk end to
// end: session BulkWalk + generated row assembly + typed column decode.
// This is the layer the mibgen fused-decode path optimises; the
// session-level BenchmarkBulkWalk deliberately excludes it.
func BenchmarkTableWalk(b *testing.B) {
	addr := startResponder(b, benchRows)

	b.Run("impl=flowseer", func(b *testing.B) {
		ctx := context.Background()
		sess := dialNative(b, addr)
		defer sess.Close()

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
