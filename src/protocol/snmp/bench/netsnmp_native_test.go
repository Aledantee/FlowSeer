//go:build snmp_bench_netsnmp

package bench

import (
	"sync/atomic"
	"testing"

	g "github.com/gosnmp/gosnmp"
)

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

func TestNetSnmpNativeRejectsNonProgress(t *testing.T) {
	for _, empty := range []bool{true, false} {
		name := "repeated_oid"
		if empty {
			name = "empty_response"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int64
			decoder := &g.GoSNMP{}
			addr := startResponderWithHandler(t, func(pkt []byte) []byte {
				request, err := decoder.SnmpDecodePacket(pkt)
				if err != nil {
					t.Errorf("decode request: %v", err)
					return nil
				}
				request.PDUType = g.GetResponse
				request.Variables = nil
				if !empty {
					request.Variables = []g.SnmpPDU{{Name: ifTableStr, Type: g.Integer, Value: 1}}
				}
				// A terminating fourth reply keeps the regression probe bounded.
				if requests.Add(1) > 3 {
					request.Variables = []g.SnmpPDU{{Name: ifTableStr, Type: g.EndOfMibView}}
				}
				response, err := request.MarshalMsg()
				if err != nil {
					t.Errorf("encode response: %v", err)
				}
				return response
			})
			sess, err := nsOpen(addr, "public")
			if err != nil {
				t.Fatalf("nsOpen: %v", err)
			}
			t.Cleanup(sess.close)
			if got, err := sess.bulkWalk(oidSubs(ifTableSnmp), walkBulkMaxRep); err == nil {
				t.Errorf("bulkWalk got count %d and nil error, want non-progress error", got)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("got %d requests, want 1", got)
			}
		})
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
