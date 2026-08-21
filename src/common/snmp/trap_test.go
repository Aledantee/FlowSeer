package snmp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// makeTrap constructs a deterministic Trap for use as pump input.
func makeTrap(t *testing.T, i int) Trap {
	t.Helper()
	oid := makeOID(t, fmt.Sprintf("1.3.6.1.6.3.1.1.5.%d", i+1))
	return Trap{
		Source:    net.IPv4(127, 0, 0, byte(i+1)),
		Community: "public",
		Version:   V2c,
		VarBinds: []VarBind{
			Counter32Var{Header: Header{OID: oid, Kind: KindCounter32}, Value: uint32(100 + i)},
		},
		Received: time.Unix(int64(1_700_000_000+i), 0),
	}
}

// TestTrapStream_PushHappyPath: Push 5 traps into a freshly-constructed
// stream and verify range yields all 5 in order, with Err() == nil after
// Done.
func TestTrapStream_PushHappyPath(t *testing.T) {
	ts := NewTrapStream(context.Background(), 16)
	const n = 5
	want := make([]Trap, n)
	for i := 0; i < n; i++ {
		want[i] = makeTrap(t, i)
		ts.Push(want[i])
	}
	ts.pump.Done()

	got := make([]Trap, 0, n)
	for tr := range ts.Iter() {
		got = append(got, tr)
	}
	if len(got) != n {
		t.Fatalf("yielded %d traps, want %d", len(got), n)
	}
	for i := range got {
		if got[i].Community != want[i].Community {
			t.Errorf("trap[%d].Community = %q, want %q", i, got[i].Community, want[i].Community)
		}
		if !got[i].Source.Equal(want[i].Source) {
			t.Errorf("trap[%d].Source = %s, want %s", i, got[i].Source, want[i].Source)
		}
		if !got[i].Received.Equal(want[i].Received) {
			t.Errorf("trap[%d].Received = %s, want %s", i, got[i].Received, want[i].Received)
		}
	}
	if err := ts.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
	if got := ts.Dropped(); got != 0 {
		t.Errorf("Dropped = %d, want 0", got)
	}
}

// TestTrapStream_NextHappyPath exercises the Scanner-style triple over
// the same input.
func TestTrapStream_NextHappyPath(t *testing.T) {
	ts := NewTrapStream(context.Background(), 8)
	const n = 4
	for i := 0; i < n; i++ {
		ts.Push(makeTrap(t, i))
	}
	ts.pump.Done()

	count := 0
	for ts.Next() {
		got := ts.Current()
		if got.Community != "public" {
			t.Errorf("trap[%d].Community = %q, want %q", count, got.Community, "public")
		}
		count++
	}
	if count != n {
		t.Errorf("Next yielded %d, want %d", count, n)
	}
	if err := ts.Err(); err != nil {
		t.Errorf("Err = %v, want nil", err)
	}
}

// TestTrapStream_DropOldest verifies the drop-oldest backpressure: with
// a buffer of 2 and 5 pushes, the consumer sees the last 2 traps and
// Dropped() reports 3.
func TestTrapStream_DropOldest(t *testing.T) {
	ts := NewTrapStream(context.Background(), 2)
	const n = 5
	for i := 0; i < n; i++ {
		ts.Push(makeTrap(t, i))
	}
	ts.pump.Done()

	got := make([]Trap, 0, n)
	for tr := range ts.Iter() {
		got = append(got, tr)
	}
	if len(got) != 2 {
		t.Fatalf("yielded %d traps with buffer=2, want 2", len(got))
	}
	if dropped := ts.Dropped(); dropped != 3 {
		t.Errorf("Dropped = %d, want 3", dropped)
	}
	// The two remaining traps should be the last two pushed (Source IPs
	// 127.0.0.4 and 127.0.0.5).
	wantIPs := []net.IP{net.IPv4(127, 0, 0, 4), net.IPv4(127, 0, 0, 5)}
	for i, tr := range got {
		if !tr.Source.Equal(wantIPs[i]) {
			t.Errorf("yielded[%d].Source = %s, want %s", i, tr.Source, wantIPs[i])
		}
	}
}

// TestTrapStream_DropCounterAtomic launches concurrent producers and a
// reader of Dropped(); under -race we want no data race and a final
// counter that matches the observed loss.
func TestTrapStream_DropCounterAtomic(t *testing.T) {
	ts := NewTrapStream(context.Background(), 1)
	const total = 1000

	// Launch a poller of Dropped() that runs concurrently with Pushes.
	stop := make(chan struct{})
	var pollMax atomic.Uint64
	var pollWG sync.WaitGroup
	pollWG.Add(1)
	go func() {
		defer pollWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				cur := ts.Dropped()
				for {
					prev := pollMax.Load()
					if cur <= prev || pollMax.CompareAndSwap(prev, cur) {
						break
					}
				}
				// Yield so we don't completely starve the producers.
				runtime.Gosched()
			}
		}
	}()

	// One goroutine consumes (slowly) to ensure the channel cycles.
	consumed := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range ts.Iter() {
			consumed++
		}
	}()

	// Push from N goroutines concurrently.
	var pushWG sync.WaitGroup
	const producers = 4
	per := total / producers
	for p := 0; p < producers; p++ {
		pushWG.Add(1)
		go func(base int) {
			defer pushWG.Done()
			for i := 0; i < per; i++ {
				ts.Push(makeTrap(t, base+i))
			}
		}(p * per)
	}
	pushWG.Wait()
	ts.pump.Done()
	close(stop)
	pollWG.Wait()
	<-done

	final := ts.Dropped()
	// Sanity: consumed + dropped == producers*per, modulo a single
	// in-flight trap that may be lost via the double-select race.
	if uint64(consumed)+final != uint64(producers*per) {
		// Allow a tolerance of 1 for the double-select race.
		if diff := int64(uint64(producers*per)) - int64(uint64(consumed)+final); diff < -1 || diff > 1 {
			t.Errorf("consumed(%d) + dropped(%d) = %d, want ~%d", consumed, final, uint64(consumed)+final, producers*per)
		}
	}
	if pollMax.Load() > final {
		t.Errorf("polled Dropped peak %d exceeds final %d (monotonicity)", pollMax.Load(), final)
	}
}

// TestTrapStream_Close_Idempotent verifies that multiple Close calls
// neither panic nor return a non-nil error.
func TestTrapStream_Close_Idempotent(t *testing.T) {
	ts := NewTrapStream(context.Background(), 4)
	if err := ts.Close(); err != nil {
		t.Errorf("first Close = %v, want nil", err)
	}
	if err := ts.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
	// Done after Close also must not panic.
	ts.pump.Done()
}

// TestTrapStream_Close_TerminatesPump exercises Close while a pump
// goroutine is pushing forever; the consumer's range must exit and the
// goroutine count must return to baseline.
func TestTrapStream_Close_TerminatesPump(t *testing.T) {
	baseline := runtime.NumGoroutine()
	ts := NewTrapStream(context.Background(), 1)

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		// Done is deferred so the consumer's range loop exits even if
		// Close raced ahead of the next Push. Push silently returns
		// once the stream is closing, so an in-flight Push doesn't
		// race the close.
		defer ts.pump.Done()
		i := 0
		for {
			select {
			case <-ts.stopped():
				return
			default:
			}
			ts.Push(makeTrap(t, i%10))
			i++
		}
	}()

	consumed := 0
	consumerDone := make(chan struct{})
	closeStarted := make(chan struct{})
	go func() {
		defer close(consumerDone)
		for range ts.Iter() {
			consumed++
			if consumed == 5 {
				close(closeStarted)
			}
		}
	}()

	<-closeStarted
	if err := ts.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}

	select {
	case <-consumerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("consumer did not terminate within 2s of Close")
	}
	select {
	case <-pumpDone:
	case <-time.After(1 * time.Second):
		t.Fatal("pump did not exit within 1s of Close")
	}

	if n := waitForPumpExit(t, baseline, 500*time.Millisecond); n > baseline+2 {
		t.Errorf("goroutine count after Close = %d, baseline = %d", n, baseline)
	}
}

// TestTrapStream_RegisterEngine_WithoutHandler verifies the documented
// behavior for a stream that the Backend never marked v3-capable.
func TestTrapStream_RegisterEngine_WithoutHandler(t *testing.T) {
	ts := NewTrapStream(context.Background(), 0)
	defer func() { _ = ts.Close() }()
	err := ts.RegisterEngine(USMConfig{Username: "u"})
	if !errors.Is(err, ErrTrapStreamNotV3Capable) {
		t.Errorf("RegisterEngine without handler = %v, want ErrTrapStreamNotV3Capable", err)
	}
}

// TestTrapStream_RegisterEngine_WithHandler verifies the handler is
// invoked when installed, and that its error is surfaced.
func TestTrapStream_RegisterEngine_WithHandler(t *testing.T) {
	ts := NewTrapStream(context.Background(), 0)
	defer func() { _ = ts.Close() }()

	var got []USMConfig
	ts.installEngineHandler(func(c USMConfig) error {
		got = append(got, c)
		return nil
	})
	if err := ts.RegisterEngine(USMConfig{Username: "u1"}); err != nil {
		t.Fatalf("RegisterEngine: %v", err)
	}
	if len(got) != 1 || got[0].Username != "u1" {
		t.Errorf("handler saw %+v, want one entry with Username=u1", got)
	}

	sentinel := errors.New("table-full")
	ts.installEngineHandler(func(_ USMConfig) error { return sentinel })
	if err := ts.RegisterEngine(USMConfig{Username: "u2"}); !errors.Is(err, sentinel) {
		t.Errorf("RegisterEngine = %v, want %v", err, sentinel)
	}
}

// TestTrapStream_Traps_RangeBreakSignalsPump verifies that a consumer
// that breaks out of the Traps range loop terminates the pump.
func TestTrapStream_Traps_RangeBreakSignalsPump(t *testing.T) {
	baseline := runtime.NumGoroutine()
	ts := NewTrapStream(context.Background(), 1)

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		// Done closes the data channel exactly once, signaling the
		// consumer's range loop to exit after draining the buffer.
		defer ts.pump.Done()
		i := 0
		for {
			select {
			case <-ts.stopped():
				return
			default:
			}
			ts.Push(makeTrap(t, i%10))
			i++
		}
	}()

	count := 0
	for range ts.Iter() {
		count++
		if count == 3 {
			break
		}
	}
	if count != 3 {
		t.Errorf("loop visited %d, want 3", count)
	}

	select {
	case <-pumpDone:
	case <-time.After(1 * time.Second):
		t.Fatal("pump did not exit within 1s of range break")
	}
	if n := waitForPumpExit(t, baseline, 500*time.Millisecond); n > baseline+2 {
		t.Errorf("goroutine count after break = %d, baseline = %d", n, baseline)
	}
}

// TestTrapStream_Fail records a terminal error; range exits and Err()
// reports the sentinel.
func TestTrapStream_Fail(t *testing.T) {
	sentinel := errors.New("trap-pump-go-boom")
	ts := NewTrapStream(context.Background(), 0)
	ts.pump.Fail(sentinel)

	count := 0
	for range ts.Iter() {
		count++
	}
	if count != 0 {
		t.Errorf("yielded %d after Fail, want 0", count)
	}
	if err := ts.Err(); !errors.Is(err, sentinel) {
		t.Errorf("Err = %v, want %v", err, sentinel)
	}
}

// TestTrapStream_ContextCancel surfaces ctx.Err() via the watcher
// goroutine and exits the consumer loop.
func TestTrapStream_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ts := NewTrapStream(ctx, 0)
	cancel()

	count := 0
	for range ts.Iter() {
		count++
	}
	if err := ts.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("Err = %v, want context.Canceled", err)
	}
	_ = count
}

func TestTrapOptions_Apply(t *testing.T) {
	_, ipnet, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatalf("ParseCIDR: %v", err)
	}
	cfg := ApplyTrapOptions(
		WithAllowedSources(*ipnet),
		WithMaxTrapsPerSecond(100),
		WithUSMTable([]USMConfig{{Username: "u", AuthProtocol: AuthSHA, AuthPassphrase: "p"}}),
		WithTrapBufferSize(512),
	)
	if len(cfg.AllowedSources()) != 1 {
		t.Errorf("AllowedSources len = %d, want 1", len(cfg.AllowedSources()))
	}
	if cfg.MaxTrapsPerSecond() != 100 {
		t.Errorf("MaxTrapsPerSecond = %d, want 100", cfg.MaxTrapsPerSecond())
	}
	if len(cfg.USMTable()) != 1 {
		t.Errorf("USMTable len = %d, want 1", len(cfg.USMTable()))
	}
	if cfg.BufferSize() != 512 {
		t.Errorf("BufferSize = %d, want 512", cfg.BufferSize())
	}
}

func TestTrapStream_DefaultBuffer(t *testing.T) {
	ts := NewTrapStream(context.Background(), -1)
	defer func() { _ = ts.Close() }()
	if cap(ts.pump.Data()) != defaultTrapBuffer {
		t.Errorf("buffer cap = %d, want %d", cap(ts.pump.Data()), defaultTrapBuffer)
	}
}
