//go:build snmp_bench_fanout

package bench

// inflight_test.go verifies the reactor's defining trade: a single FlowSeer
// Session multiplexes many concurrent in-flight requests over ONE socket,
// where gosnmp (one synchronous exchange per connection) needs one socket per
// concurrent request. The collector-scale sweep deliberately drove one request
// per session, so it never exercised this — this test does.
//
// It uses a *concurrent* responder (a goroutine per datagram) so the agent
// side does not serialize the client's pipelined requests; that isolates the
// client's own ability to keep K requests in flight at once.
//
// Build-tagged snmp_bench_fanout; run with
//   go test -tags snmp_bench_fanout -run TestInFlightScaling -v

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	g "github.com/gosnmp/gosnmp"

	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// startConcurrentResponder is startResponder's non-serializing twin: it copies
// each datagram and answers it in its own goroutine, so K concurrent in-flight
// requests are not bottlenecked at the agent. net.UDPConn is safe for
// concurrent writes, so the per-datagram goroutines share the one socket.
func startConcurrentResponder(tb testing.TB, rows int) string {
	tb.Helper()
	mib := buildMIB(rows)
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		tb.Fatalf("listen: %v", err)
	}
	done := make(chan struct{})
	tb.Cleanup(func() {
		_ = conn.Close()
		<-done
	})

	go func() {
		defer close(done)
		var replies sync.WaitGroup
		defer replies.Wait()
		buf := make([]byte, 65535)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return // socket closed at cleanup
			}
			pkt := append([]byte(nil), buf[:n]...) // copy: buf is reused next read
			replies.Add(1)
			go func(pkt []byte, addr *net.UDPAddr) {
				defer replies.Done()
				if resp := handle(mib, pkt); resp != nil {
					_, _ = conn.WriteToUDP(resp, addr)
				}
			}(pkt, addr)
		}
	}()
	return conn.LocalAddr().String()
}

// driveFor runs k worker goroutines, each calling op(worker) in a tight loop
// for the window, and returns the number of successful completions and
// failures. worker is the goroutine index (so a worker can pick its own
// client from a per-worker slice).
func driveFor(window time.Duration, k int, op func(worker int) error) (ops, fails int64) {
	var (
		wg     sync.WaitGroup
		opsC   atomic.Int64
		failsC atomic.Int64
		done   = make(chan struct{})
	)
	for i := 0; i < k; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if err := op(worker); err != nil {
					failsC.Add(1)
					return
				}
				opsC.Add(1)
			}
		}(i)
	}
	time.Sleep(window)
	close(done)
	wg.Wait()
	return opsC.Load(), failsC.Load()
}

// TestInFlightScaling drives K concurrent scalar Gets and reports throughput
// and socket count per client, sweeping the in-flight concurrency K. It shows:
//   - FlowSeer over ONE session/socket scaling with K (the reactor multiplexes);
//   - gosnmp needing K connections/sockets to reach the same concurrency;
//   - gosnmp over one connection unable to multiplex at all (its per-socket
//     ceiling is K=1).
func TestInFlightScaling(t *testing.T) {
	addr := startConcurrentResponder(t, benchRows)
	ctx := context.Background()
	const window = 2 * time.Second
	concs := []int{1, 2, 4, 8, 16, 32}

	rate := func(ops int64) float64 { return float64(ops) / window.Seconds() }

	t.Run("flowseer-1session-1socket", func(t *testing.T) {
		sess := dialNative(t, addr)
		closeOnCleanup(t, sess)
		if _, err := sess.Get(ctx, []snmp.OID{scalarSnmpOID}); err != nil { // warm
			t.Fatalf("warmup: %v", err)
		}
		for _, k := range concs {
			ops, fails := driveFor(window, k, func(int) error {
				_, err := sess.Get(ctx, []snmp.OID{scalarSnmpOID})
				return err
			})
			if fails > 0 {
				t.Fatalf("inflight=%d: %d failures", k, fails)
			}
			t.Logf("inflight=%-3d %9.0f ops/s   sockets=1", k, rate(ops))
		}
	})

	t.Run("gosnmp-Kconns-Ksockets", func(t *testing.T) {
		for _, k := range concs {
			clients := make([]*g.GoSNMP, k)
			for i := range clients {
				clients[i] = dialGosnmp(t, addr)
				if _, err := clients[i].Get([]string{scalarStr}); err != nil { // warm
					t.Fatalf("gosnmp warmup: %v", err)
				}
			}
			ops, fails := driveFor(window, k, func(w int) error {
				_, err := clients[w].Get([]string{scalarStr})
				return err
			})
			for _, c := range clients {
				_ = c.Conn.Close()
			}
			if fails > 0 {
				t.Fatalf("inflight=%d: %d failures", k, fails)
			}
			t.Logf("inflight=%-3d %9.0f ops/s   sockets=%d", k, rate(ops), k)
		}
	})

	t.Run("gosnmp-1conn-serial-1socket", func(t *testing.T) {
		// gosnmp cannot be called concurrently on one connection (it races on
		// the request-id and the socket read), so its single-socket ceiling is
		// one in-flight request: a single serial loop.
		c := dialGosnmp(t, addr)
		closeOnCleanup(t, c.Conn)
		if _, err := c.Get([]string{scalarStr}); err != nil {
			t.Fatalf("warmup: %v", err)
		}
		ops, fails := driveFor(window, 1, func(int) error {
			_, err := c.Get([]string{scalarStr})
			return err
		})
		if fails > 0 {
			t.Fatalf("%d failures", fails)
		}
		t.Logf("serial      %9.0f ops/s   sockets=1 (cannot multiplex)", rate(ops))
	})
}
