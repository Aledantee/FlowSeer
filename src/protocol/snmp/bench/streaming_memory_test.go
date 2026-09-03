package bench

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
)

// TestStreamingResponderProcess keeps fixture and server storage outside the
// measured process. The parent closes stdin to release the child.
func TestStreamingResponderProcess(t *testing.T) {
	rows, err := strconv.Atoi(os.Getenv("FLOWSEER_HEAP_ROWS"))
	if err != nil {
		return
	}
	addr, _ := startResponderMIB(t, buildScaleMIB(rows, false, 32))
	fmt.Println(addr)
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func separateResponder(t *testing.T, rows int) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStreamingResponderProcess$")
	cmd.Env = append(os.Environ(), fmt.Sprintf("FLOWSEER_HEAP_ROWS=%d", rows))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if err := cmd.Wait(); err != nil {
			t.Error(err)
		}
	})
	scan := bufio.NewScanner(stdout)
	if !scan.Scan() {
		t.Fatal("responder did not publish an address")
	}
	return scan.Text()
}

func TestStreamingRetainedHeap(t *testing.T) {
	if os.Getenv("FLOWSEER_MEASURE_HEAP") != "1" {
		t.Skip("opt-in isolated retained-heap measurement")
	}
	peaks := map[int]uint64{}
	for _, rows := range []int{1000, 100000} {
		t.Run(strconv.Itoa(rows), func(t *testing.T) {
			addr := separateResponder(t, rows)
			sess := dialNative(t, addr)
			defer func() { _ = sess.Close() }()
			for range 3 {
				w := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfInOctets, ifmib.IfOutOctets)
				for range w.Iter() {
					break
				}
				if w.Err() != nil {
					t.Fatal(w.Err())
				}
			}
			runtime.GC()
			runtime.GC() // Expire sync.Pool victims before fixing the baseline.
			var before, now runtime.MemStats
			runtime.ReadMemStats(&before)
			peak := uint64(0)
			w := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfInOctets, ifmib.IfOutOctets)
			n := 0
			for range w.Iter() {
				n++
				// Sample a full batch, its last row, and subsequent batch boundaries.
				if n <= 101 || n%1000 == 1 {
					runtime.GC()
					runtime.ReadMemStats(&now)
					if now.HeapAlloc > before.HeapAlloc {
						peak = max(peak, now.HeapAlloc-before.HeapAlloc)
					}
				}
			}
			if w.Err() != nil || n != rows {
				t.Fatalf("rows=%d err=%v", n, w.Err())
			}
			runtime.KeepAlive(sess)
			peaks[rows] = peak
			t.Logf("rows=%d peak-retained-delta=%d bytes", rows, peak)
		})
	}
	if peaks[100000] > 2*peaks[1000] {
		t.Fatalf("heap scaling exceeds 2x: %v", peaks)
	}
}

func TestStreamingSparseWire(t *testing.T) {
	for _, payload := range []int{8, 512} {
		addr, counts := startResponderMIB(t, buildScaleMIB(1000, true, payload))
		sess := dialNative(t, addr)
		w := ifmib.IfTable.Walk(context.Background(), sess, ifmib.IfDescr, ifmib.IfInOctets, ifmib.IfOutOctets, ifmib.IfOutErrors)
		rows := 0
		for idx, row := range w.Iter() {
			rows++
			if int(idx.At(0)) != rows {
				t.Fatalf("index %v row %d", idx, rows)
			}
			if row.Observed(ifmib.IfOutErrors) {
				t.Fatal("absent column marked observed")
			}
			if row.Observed(ifmib.IfInOctets) != (rows >= 2 && rows <= 1000) {
				t.Fatal("missing-first/disjoint input mismatch")
			}
			if row.Observed(ifmib.IfOutOctets) != (rows > 1000) {
				t.Fatal("disjoint output mismatch")
			}
		}
		if rows != 2000 || w.Err() != nil {
			t.Fatalf("rows=%d requests=%d err=%v", rows, counts.requests.Load(), w.Err())
		}
		_ = sess.Close()
	}
}

func TestStreamingEarlyRequestBound(t *testing.T) {
	for _, rows := range []int{50, 1000, 10000, 100000} {
		addr, counts := startResponderMIB(t, buildScaleMIB(rows, false, 32))
		sess := dialNative(t, addr)
		for _, width := range []int{2, 20} {
			counts.requests.Store(0)
			w := ifmib.IfTable.Walk(context.Background(), sess, scaleColumns(width)...)
			n := 0
			for range w.Iter() {
				n++
				break
			}
			if n != 1 || w.Err() != nil || counts.requests.Load() > int64((width+9)/10) {
				t.Fatalf("rows=%d width=%d requests=%d err=%v", rows, width, counts.requests.Load(), w.Err())
			}
		}
		_ = sess.Close()
	}
}
