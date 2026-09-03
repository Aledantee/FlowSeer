package pump

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// waitTimeout bounds every blocking assertion so a regression hangs the
// test, not the suite.
const waitTimeout = 5 * time.Second

func TestFailFirstErrorWins(t *testing.T) {
	t.Parallel()
	p := New[int](context.Background(), 1)
	first := errors.New("first")
	second := errors.New("second")

	p.Fail(first)
	p.Fail(second)
	p.Fail(nil)

	if got := p.Err(); !errors.Is(got, first) {
		t.Fatalf("Err() = %v, want the first recorded error %v", got, first)
	}
}

func TestFailConcurrentLatchesExactlyOne(t *testing.T) {
	t.Parallel()
	p := New[int](context.Background(), 1)
	errs := make([]error, 8)
	for i := range errs {
		errs[i] = errors.New("worker error")
	}

	var wg sync.WaitGroup
	for _, err := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.Fail(err)
		}()
	}
	wg.Wait()

	got := p.Err()
	if got == nil {
		t.Fatal("Err() = nil after concurrent Fail calls")
	}
	found := false
	for _, err := range errs {
		if errors.Is(got, err) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Err() = %v, not one of the recorded errors", got)
	}
}

func TestDoneAndFailIdempotent(t *testing.T) {
	t.Parallel()
	p := New[int](context.Background(), 1)

	// Any interleaving of Done/Fail after the first close must be a
	// no-op — no double-close panic, no error overwrite.
	p.Done()
	p.Done()
	p.Fail(errors.New("late"))
	p.CloseData()
	p.SignalStop()

	// Fail after Done still records the error: stop/close are
	// idempotent, the error latch is independent.
	if err := p.Err(); err == nil {
		t.Fatal("Err() = nil, want the error recorded by Fail after Done")
	}
	if _, ok := p.Recv(); ok {
		t.Fatal("Recv() delivered a value from a closed empty pump")
	}
}

func TestSendAfterDoneReturnsFalse(t *testing.T) {
	t.Parallel()
	p := New[int](context.Background(), 4)
	p.Done()
	if p.Send(1) {
		t.Fatal("Send succeeded after Done")
	}
	if delivered, dropped := p.TrySendDropOldest(1); delivered || dropped != 0 {
		t.Fatalf("TrySendDropOldest after Done = (%v, %d), want (false, 0)", delivered, dropped)
	}
}

func TestSendAfterCloseDataReturnsFalse(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Send", "TrySendDropOldest"} {
		t.Run(name, func(t *testing.T) {
			p := New[int](context.Background(), 1)
			t.Cleanup(p.Cancel)
			p.CloseData()
			defer func() {
				if got := recover(); got != nil {
					t.Errorf("send after CloseData panicked: %v", got)
				}
			}()
			if name == "TrySendDropOldest" {
				if delivered, dropped := p.TrySendDropOldest(1); delivered || dropped != 0 {
					t.Errorf("TrySendDropOldest() = (%v, %d), want (false, 0)", delivered, dropped)
				}
				return
			}
			if p.Send(1) {
				t.Error("Send succeeded after CloseData")
			}
		})
	}
}

func TestCloseDataUnblocksSend(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p := New[int](context.Background(), 1)
		t.Cleanup(p.Cancel)
		if !p.Send(1) {
			t.Fatal("initial Send failed")
		}

		result := make(chan bool, 1)
		go func() { result <- p.Send(2) }()
		synctest.Wait()
		p.CloseData()
		if <-result {
			t.Error("blocked Send succeeded after CloseData")
		}
		if got, ok := p.Recv(); !ok || got != 1 {
			t.Errorf("Recv() = (%d, %v), want (1, true)", got, ok)
		}
		if _, ok := p.Recv(); ok {
			t.Error("Recv succeeded after draining the closed pump")
		}
	})
}

func TestTrySendDropOldestFullBuffer(t *testing.T) {
	t.Parallel()
	p := New[int](context.Background(), 2)

	for i := range 2 {
		if delivered, dropped := p.TrySendDropOldest(i); !delivered || dropped != 0 {
			t.Fatalf("fill send %d = (%v, %d), want (true, 0)", i, delivered, dropped)
		}
	}

	// Buffer full: the oldest value (0) is dropped and 2 is delivered.
	delivered, dropped := p.TrySendDropOldest(2)
	if !delivered || dropped != 1 {
		t.Fatalf("overflow send = (%v, %d), want (true, 1)", delivered, dropped)
	}

	want := []int{1, 2}
	for _, expect := range want {
		got, ok := p.Recv()
		if !ok || got != expect {
			t.Fatalf("Recv() = (%d, %v), want (%d, true)", got, ok, expect)
		}
	}
}

func TestSendUnblocksOnStop(t *testing.T) {
	t.Parallel()
	p := New[int](context.Background(), 1)
	if !p.Send(1) {
		t.Fatal("buffered send failed")
	}

	// The next Send blocks on the full buffer; SignalStop must release
	// it with a false return within one send attempt.
	result := make(chan bool, 1)
	go func() { result <- p.Send(2) }()

	// Give the producer a moment to actually block, then stop.
	time.Sleep(10 * time.Millisecond)
	p.SignalStop()

	select {
	case ok := <-result:
		if ok {
			t.Fatal("Send returned true after stop")
		}
	case <-time.After(waitTimeout):
		t.Fatal("Send did not unblock after SignalStop")
	}
}

func TestSendUnblocksOnContextCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	p := New[int](ctx, 1)
	if !p.Send(1) {
		t.Fatal("buffered send failed")
	}

	result := make(chan bool, 1)
	go func() { result <- p.Send(2) }()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case ok := <-result:
		if ok {
			t.Fatal("Send returned true after context cancel")
		}
	case <-time.After(waitTimeout):
		t.Fatal("Send did not unblock after context cancel")
	}

	// Cancellation propagates to the stop signal so producers watching
	// Stopped() also terminate.
	select {
	case <-p.Stopped():
	case <-time.After(waitTimeout):
		t.Fatal("Stopped() not closed after context cancel")
	}
}

func TestSendConcurrentWithCloseNoRace(t *testing.T) {
	t.Parallel()
	// Hammer Send against Fail/Done so the race detector can prove the
	// sendMu discipline prevents a send-on-closed-channel panic.
	for range 50 {
		p := New[int](context.Background(), 1)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := range 100 {
				if !p.Send(i) {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			p.Fail(errors.New("boom"))
		}()
		go func() {
			for range p.Data() {
			}
		}()
		wg.Wait()
	}
}

func TestNilContextDefaultsToBackground(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // deliberately passing nil to exercise the documented fallback.
	p := New[int](nil, 1)
	if p.Context() == nil {
		t.Fatal("Context() = nil")
	}
	if !p.Send(1) {
		t.Fatal("Send failed on a fresh pump")
	}
}

func TestRecvDrainsBufferedAfterDone(t *testing.T) {
	t.Parallel()
	p := New[int](context.Background(), 4)
	for i := range 3 {
		if !p.Send(i) {
			t.Fatalf("send %d failed", i)
		}
	}
	p.Done()

	for want := range 3 {
		got, ok := p.Recv()
		if !ok || got != want {
			t.Fatalf("Recv() = (%d, %v), want (%d, true)", got, ok, want)
		}
	}
	if _, ok := p.Recv(); ok {
		t.Fatal("Recv() delivered a value past the buffered items")
	}
}
