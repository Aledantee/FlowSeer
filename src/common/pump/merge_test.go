package pump

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

func TestMergeZeroSourcesCompletesImmediately(t *testing.T) {
	t.Parallel()
	merged := Merge[int](context.Background(), 0)
	t.Cleanup(merged.Cancel)

	select {
	case _, ok := <-merged.Data():
		if ok {
			t.Fatal("Data() remained open for zero sources")
		}
	default:
		t.Fatal("Data() did not close before Merge returned")
	}
	if err := merged.Err(); err != nil {
		t.Fatalf("Err() = %v, want nil", err)
	}
}

func TestMergeForwardsEverySourceInOrder(t *testing.T) {
	t.Parallel()
	type item struct {
		source int
		value  int
	}

	synctest.Test(t, func(t *testing.T) {
		first := New[item](context.Background(), 3)
		second := New[item](context.Background(), 3)
		t.Cleanup(first.Cancel)
		t.Cleanup(second.Cancel)

		for value := range 3 {
			if !first.Send(item{source: 1, value: value}) {
				t.Fatalf("first source Send(%d) failed", value)
			}
			if !second.Send(item{source: 2, value: value}) {
				t.Fatalf("second source Send(%d) failed", value)
			}
		}
		first.Done()
		second.Done()

		merged := Merge(context.Background(), 6, first, second)
		t.Cleanup(merged.Cancel)
		got := map[int][]int{}
		for value := range merged.Data() {
			got[value.source] = append(got[value.source], value.value)
		}

		want := []int{0, 1, 2}
		for source := 1; source <= 2; source++ {
			if !slices.Equal(got[source], want) {
				t.Errorf("source %d values = %v, want %v", source, got[source], want)
			}
		}
		if err := merged.Err(); err != nil {
			t.Errorf("Err() = %v, want nil", err)
		}
	})
}

func TestMergeSourceErrorDrainsAndStopsRemainingSources(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		failed := New[int](context.Background(), 2)
		remaining := New[int](context.Background(), 0)
		t.Cleanup(failed.Cancel)
		t.Cleanup(remaining.Cancel)
		sourceErr := errors.New("source failed")

		if !failed.Send(1) || !failed.Send(2) {
			t.Fatal("failed source did not accept buffered values")
		}
		failed.Fail(sourceErr)

		merged := Merge(context.Background(), 2, failed, remaining)
		t.Cleanup(merged.Cancel)
		var got []int
		for value := range merged.Data() {
			got = append(got, value)
		}

		if !slices.Equal(got, []int{1, 2}) {
			t.Errorf("merged values = %v, want [1 2]", got)
		}
		if err := merged.Err(); !errors.Is(err, sourceErr) {
			t.Errorf("Err() = %v, want %v", err, sourceErr)
		}
		select {
		case <-remaining.Stopped():
		default:
			t.Error("remaining source was not stopped before merged data closed")
		}
		select {
		case <-remaining.Context().Done():
			t.Error("Merge canceled the remaining source context")
		default:
		}
	})
}

func TestMergeSourceErrorDeliversValueAlreadyReceived(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		failed := New[int](context.Background(), 0)
		inFlight := New[int](context.Background(), 1)
		t.Cleanup(failed.Cancel)
		t.Cleanup(inFlight.Cancel)
		sourceErr := errors.New("source failed")

		if !inFlight.Send(7) {
			t.Fatal("in-flight source Send failed")
		}
		merged := Merge(context.Background(), 0, failed, inFlight)
		t.Cleanup(merged.Cancel)
		synctest.Wait()

		failed.Fail(sourceErr)
		<-inFlight.Stopped()
		if got, ok := <-merged.Data(); !ok || got != 7 {
			t.Errorf("merged value = (%d, %v), want (7, true)", got, ok)
		}
		for range merged.Data() {
		}

		if err := merged.Err(); !errors.Is(err, sourceErr) {
			t.Errorf("Err() = %v, want %v", err, sourceErr)
		}
	})
}

func TestMergeConsumerTerminationStopsSources(t *testing.T) {
	t.Parallel()
	consumerErr := errors.New("consumer failed")
	tests := map[string]struct {
		stop    func(*Pump[int])
		wantErr error
	}{
		"CloseData": {
			stop: (*Pump[int]).CloseData,
		},
		"Done": {
			stop: (*Pump[int]).Done,
		},
		"Fail": {
			stop:    func(p *Pump[int]) { p.Fail(consumerErr) },
			wantErr: consumerErr,
		},
		"SignalStop": {
			stop: (*Pump[int]).SignalStop,
		},
		"Cancel": {
			stop:    (*Pump[int]).Cancel,
			wantErr: context.Canceled,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				source := New[int](context.Background(), 1)
				t.Cleanup(source.Cancel)
				merged := Merge(context.Background(), 1, source)
				t.Cleanup(merged.Cancel)

				test.stop(merged)
				synctest.Wait()

				select {
				case <-source.Stopped():
				default:
					t.Error("source was not stopped")
				}
				select {
				case _, ok := <-source.Data():
					if !ok {
						t.Error("Merge closed the source data channel")
					}
				default:
				}
				select {
				case <-source.Context().Done():
					t.Error("Merge canceled the source context")
				default:
				}
				select {
				case _, ok := <-merged.Data():
					if ok {
						t.Error("merged data remained open")
					}
				default:
					t.Error("merged data remained open")
				}
				if err := merged.Err(); !errors.Is(err, test.wantErr) {
					t.Errorf("Err() = %v, want %v", err, test.wantErr)
				}

				if source.Send(1) {
					t.Error("source Send succeeded after merged termination")
				}
				if delivered, dropped := source.TrySendDropOldest(1); delivered || dropped != 0 {
					t.Errorf("source TrySendDropOldest() = (%v, %d), want (false, 0)", delivered, dropped)
				}
			})
		})
	}
}

func TestMergeContextCancellationFailsAndStopsSources(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		source := New[int](context.Background(), 0)
		t.Cleanup(source.Cancel)
		merged := Merge(ctx, 0, source)
		t.Cleanup(merged.Cancel)

		cancel()
		synctest.Wait()

		if err := merged.Err(); !errors.Is(err, context.Canceled) {
			t.Errorf("Err() = %v, want %v", err, context.Canceled)
		}
		select {
		case <-source.Stopped():
		default:
			t.Error("source was not stopped")
		}
		if _, ok := <-merged.Data(); ok {
			t.Error("merged data remained open")
		}
	})
}

func TestMergeSourceErrorPrecedesContextCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		failed := New[int](context.Background(), 0)
		remaining := New[int](context.Background(), 0)
		t.Cleanup(failed.Cancel)
		t.Cleanup(remaining.Cancel)
		sourceErr := errors.New("source failed first")
		merged := Merge(ctx, 0, failed, remaining)
		t.Cleanup(merged.Cancel)

		failed.Fail(sourceErr)
		<-remaining.Stopped()
		cancel()
		for range merged.Data() {
		}

		if err := merged.Err(); !errors.Is(err, sourceErr) {
			t.Errorf("Err() = %v, want %v", err, sourceErr)
		}
	})
}

func TestMergeContextCancellationPrecedesSourceError(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		failed := New[int](context.Background(), 0)
		t.Cleanup(failed.Cancel)
		failed.Fail(errors.New("late source failure"))
		merged := Merge(ctx, 0, failed)
		t.Cleanup(merged.Cancel)

		for range merged.Data() {
		}

		if err := merged.Err(); !errors.Is(err, context.Canceled) {
			t.Errorf("Err() = %v, want %v", err, context.Canceled)
		}
	})
}

// TestMergeForwarderPanicIsRecoveredAndDoesNotBlockRemainingSources is
// evidence for this change: it forces a real panic in the converted forward
// goroutine — a nil *Pump[int] source panics on its first field access
// inside Data() — and checks that the process survives, that the merge still
// drains the other source, and that the panic reaches the merged pump as a
// terminal error.
//
// That last assertion is the load-bearing one. The coordinator reads a nil on
// results as a source that finished cleanly, so a forwarder that reported its
// panic as nil would leave Merge indistinguishable from a successful merge —
// a consumer would see the channel close with Err() == nil and conclude every
// source ran to completion.
//
// A nil source panics twice: once in the forwarder that dereferences it, and
// again in the coordinator, whose stopSources calls SignalStop on it after the
// first report arrives. The error that reaches Err() is therefore the
// coordinator's, and the test names it rather than claiming the forwarder's.
// The discrimination still holds: a forwarder reporting nil never causes the
// second panic at all, so Err() would be nil and this fails.
func TestMergeForwarderPanicIsRecoveredAndDoesNotBlockRemainingSources(t *testing.T) {
	t.Parallel()
	var nilSource *Pump[int]
	good := New[int](context.Background(), 1)
	t.Cleanup(good.Cancel)
	if !good.Send(1) {
		t.Fatal("good source Send failed")
	}
	good.Done()

	merged := Merge(context.Background(), 1, nilSource, good)
	t.Cleanup(merged.Cancel)

	var got []int
	done := make(chan struct{})
	go func() {
		defer close(done)
		for value := range merged.Data() {
			got = append(got, value)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("merged.Data() never closed after the forwarder panicked")
	}

	if !slices.Equal(got, []int{1}) {
		t.Errorf("merged values = %v, want [1]", got)
	}
	err := merged.Err()
	if err == nil {
		t.Fatal("merged.Err() is nil after a forwarder panicked; the panic was reported as a clean source")
	}
	if errs.Attributes(err)["panic"] == nil {
		t.Errorf("merged.Err() carries no recovered panic value: %v", err)
	}
	if got, want := err.Error(), "Merge.coordinate panicked"; !strings.HasPrefix(got, want) {
		t.Errorf("merged.Err() = %q, want a %q report", got, want)
	}
}

func TestMergeFirstSourceErrorWins(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		first := New[int](context.Background(), 0)
		second := New[int](context.Background(), 0)
		t.Cleanup(first.Cancel)
		t.Cleanup(second.Cancel)
		firstErr := errors.New("first source failure")
		merged := Merge(context.Background(), 0, first, second)
		t.Cleanup(merged.Cancel)

		first.Fail(firstErr)
		<-second.Stopped()
		second.Fail(errors.New("second source failure"))
		for range merged.Data() {
		}

		if err := merged.Err(); !errors.Is(err, firstErr) {
			t.Errorf("Err() = %v, want %v", err, firstErr)
		}
	})
}
