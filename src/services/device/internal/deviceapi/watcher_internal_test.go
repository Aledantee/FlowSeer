package deviceapi

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// panicKeyWatcher panics the first time Updates is read, which is the first
// line inside the goroutine this package's Watch spawns.
type panicKeyWatcher struct{}

func (panicKeyWatcher) Updates() <-chan jetstream.KeyValueEntry {
	panic("kv watch fell over")
}

func (panicKeyWatcher) Stop() error { return nil }

// fakeKV embeds the interface so only Watch needs a real implementation; every
// other jetstream.KeyValue method panics on its own if a test ever reaches
// it, which none here do.
type fakeKV struct {
	jetstream.KeyValue
	watcher jetstream.KeyWatcher
}

func (f fakeKV) Watch(context.Context, string, ...jetstream.WatchOpt) (jetstream.KeyWatcher, error) {
	return f.watcher, nil
}

// recordSink collects the slog records emitted during a test, so it can
// assert on the log record spawn.Go leaves behind without depending on a
// handler's formatting.
type recordSink struct {
	mu      sync.Mutex
	records []slog.Record
}

func (s *recordSink) append(record slog.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record.Clone())
}

func (s *recordSink) all() []slog.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]slog.Record(nil), s.records...)
}

type recordingHandler struct{ sink *recordSink }

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h recordingHandler) Handle(_ context.Context, record slog.Record) error {
	h.sink.append(record)
	return nil
}

func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

func withRecordingLogger(t *testing.T) *recordSink {
	t.Helper()
	sink := &recordSink{}
	previous := slog.Default()
	slog.SetDefault(slog.New(recordingHandler{sink: sink}))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return sink
}

// TestAWatchPanicIsLoggedAndStopStillWorks is evidence for the converted
// goroutine in KVWatcher.Watch: it forces watcher.Updates() into a real
// panic and checks that the process survives, that the recovered panic
// reaches the observability floor (the only reporting this site has — Watch
// hands its caller no failure sink), and that stop still returns without
// blocking, since a caller has nothing else to release the goroutine with.
func TestAWatchPanicIsLoggedAndStopStillWorks(t *testing.T) {
	sink := withRecordingLogger(t)
	w := NewKVWatcher(fakeKV{watcher: panicKeyWatcher{}})

	_, stop, err := w.Watch(context.Background(), "dev-1")
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		found := false
		for _, record := range sink.all() {
			if record.Message == "goroutine panicked" {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no panic log record appeared after watcher.Updates() panicked")
		}
		time.Sleep(10 * time.Millisecond)
	}

	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop() blocked after the watch goroutine had already panicked and exited")
	}
}
