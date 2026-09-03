//go:build snmp_bench_netsnmp

package bench

import "testing"

// TestNetSnmpNativeSmoke confirms the cgo libnetsnmp binding links and walks
// the loopback responder, returning the same row count the Go clients see.
func TestNetSnmpNativeSmoke(t *testing.T) {
	addr := startResponder(t)
	sess, err := nsOpen(addr, "public")
	if err != nil {
		t.Fatalf("nsOpen: %v", err)
	}
	defer sess.close()

	got, err := sess.bulkWalk(oidSubs(ifTableSnmp), walkBulkMaxRep)
	if err != nil {
		t.Fatalf("bulkWalk: %v", err)
	}
	if got != benchRows*2 {
		t.Fatalf("net-snmp walk returned %d rows, want %d", got, benchRows*2)
	}
}

// BenchmarkNetSnmpTraversal counts the same varbinds as the Go callback arms.
// Its allocation metrics exclude libnetsnmp's C heap.
func BenchmarkNetSnmpTraversal(b *testing.B) {
	addr := startResponder(b)
	sess, err := nsOpen(addr, "public")
	if err != nil {
		b.Fatal(err)
	}
	defer sess.close()
	b.ResetTimer()
	for range b.N {
		n, err := sess.bulkWalk(oidSubs(ifTableSnmp), walkBulkMaxRep)
		if err != nil || n != benchRows*2 {
			b.Fatalf("count=%d err=%v", n, err)
		}
	}
}
