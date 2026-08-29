package yang_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/yang"
)

// scriptedFetch serves successive payloads (or errors) per tick.
type scriptedFetch struct {
	mu       sync.Mutex
	payloads []any // []byte or error; the last entry repeats
	calls    int
}

func (s *scriptedFetch) fetch(context.Context) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	if idx >= len(s.payloads) {
		idx = len(s.payloads) - 1
	}
	s.calls++
	switch v := s.payloads[idx].(type) {
	case error:
		return nil, v
	case []byte:
		return v, nil
	case nil:
		return nil, nil
	default:
		panic("bad script entry")
	}
}

type serverKey struct{ Name string }

func serverCodec() yang.RowCodec[testServer, serverKey] {
	return yang.StructRowCodec(serverSchema(), func(r *testServer) serverKey {
		if r.Name == nil {
			return serverKey{}
		}
		return serverKey{Name: *r.Name}
	})
}

func serversXML(entries ...string) []byte {
	doc := `<data><servers xmlns="urn:test:main">`
	for _, e := range entries {
		doc += e
	}
	return []byte(doc + `</servers></data>`)
}

func serverXML(name string, port int) string {
	return fmt.Sprintf(`<server><name>%s</name><port>%d</port></server>`, name, port)
}

// eventChan pipes the watcher's events without ever breaking the
// iterator (breaking the range signals Close).
func eventChan[Row any, Key comparable](w *yang.TickWatcher[Row, Key]) <-chan yang.WatchEvent[Row, Key] {
	ch := make(chan yang.WatchEvent[Row, Key], 256)
	go func() {
		defer close(ch)
		for ev := range w.Iter() {
			ch <- ev
		}
	}()
	return ch
}

// collectEvents drains n events with a deadline.
func collectEvents[Row any, Key comparable](t *testing.T, ch <-chan yang.WatchEvent[Row, Key], n int) []yang.WatchEvent[Row, Key] {
	t.Helper()
	out := make([]yang.WatchEvent[Row, Key], 0, n)
	for len(out) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatalf("watcher ended after %d/%d events", len(out), n)
			}
			out = append(out, ev)
		case <-time.After(10 * time.Second):
			t.Fatalf("collected %d/%d events before timeout", len(out), n)
		}
	}
	return out
}

// TestTickWatcherAE4 checks that one leaf change between ticks
// emits exactly one Modify for that row, and Close terminates
// promptly.
func TestTickWatcherAE4(t *testing.T) {
	fetch := &scriptedFetch{payloads: []any{
		serversXML(serverXML("edge-1", 8080), serverXML("edge-2", 8080)),
		serversXML(serverXML("edge-1", 9090), serverXML("edge-2", 8080)),
	}}
	codec := serverCodec()
	w := yang.NewTickWatcher(context.Background(), codec, fetch.fetch, codec.DecodeXML,
		yang.WatchConfig{Interval: 20 * time.Millisecond})
	defer func() { _ = w.Close() }()

	events := collectEvents(t, eventChan(w), 3)
	if events[0].Kind != yang.Added || events[1].Kind != yang.Added {
		t.Fatalf("cold start = %v/%v, want Added/Added", events[0].Kind, events[1].Kind)
	}
	mod := events[2]
	if mod.Kind != yang.Modified || mod.Key != (serverKey{Name: "edge-1"}) {
		t.Fatalf("event = %+v, want one Modified for edge-1", mod)
	}
	if *mod.Row.Port != 9090 {
		t.Errorf("modified row port = %d", *mod.Row.Port)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Err(); err != nil {
		t.Errorf("clean close latched %v", err)
	}
}

func TestTickWatcherRemoveAndKeyOrder(t *testing.T) {
	// The second tick drops edge-2 and reorders edge-1's key element
	// after another leaf: identity must survive payload order.
	fetch := &scriptedFetch{payloads: []any{
		serversXML(serverXML("edge-1", 1), serverXML("edge-2", 2)),
		serversXML(`<server><port>1</port><name>edge-1</name></server>`),
	}}
	codec := serverCodec()
	w := yang.NewTickWatcher(context.Background(), codec, fetch.fetch, codec.DecodeXML,
		yang.WatchConfig{Interval: 20 * time.Millisecond})
	defer func() { _ = w.Close() }()

	events := collectEvents(t, eventChan(w), 3)
	if events[2].Kind != yang.Removed || events[2].Key != (serverKey{Name: "edge-2"}) {
		t.Fatalf("event = %+v, want Removed edge-2 (and no spurious edge-1 change)", events[2])
	}
}

func TestTickWatcherTransientFailureAndLatch(t *testing.T) {
	boom := errors.New("transport hiccup")
	fetch := &scriptedFetch{payloads: []any{
		serversXML(serverXML("edge-1", 1)),
		boom,
		serversXML(serverXML("edge-1", 2)),
		boom, // repeats forever from here
	}}
	codec := serverCodec()
	w := yang.NewTickWatcher(context.Background(), codec, fetch.fetch, codec.DecodeXML,
		yang.WatchConfig{Interval: 15 * time.Millisecond, MaxConsecutiveTickFailures: 3})
	defer func() { _ = w.Close() }()

	events := collectEvents(t, eventChan(w), 2)
	if events[1].Kind != yang.Modified {
		t.Fatalf("watcher did not survive one failed tick: %+v", events[1])
	}
	if w.LastTickErr() == nil {
		t.Error("transient failure not recorded in LastTickErr")
	}

	// The repeating failure eventually latches.
	deadline := time.After(10 * time.Second)
	for w.Err() == nil {
		select {
		case <-deadline:
			t.Fatal("watcher never latched after consecutive failures")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if code, ok := errs.CodeOf(w.Err()); !ok || code != yang.ErrCodeWatch {
		t.Errorf("latched code = %v", code)
	}
}

// TestTickWatcherSyntheticRow drives a presence-container subtree:
// appearing emits Added, a leaf change emits one Modified, and
// disappearing (nil payload) emits Removed.
func TestTickWatcherSyntheticRow(t *testing.T) {
	extraSchema := &yang.Schema{
		Module: "test-main", Namespace: "urn:test:main", Name: "extra", Presence: true,
		Fields: []yang.Field{{GoName: "Note", Name: "note", Type: &yang.Type{Kind: yang.TypeString}}},
	}
	codec := yang.ContainerRowCodec[testExtra](extraSchema)

	fetch := &scriptedFetch{payloads: []any{
		[]byte(`<data><extra xmlns="urn:test:main"><note>a</note></extra></data>`),
		[]byte(`<data><extra xmlns="urn:test:main"><note>b</note></extra></data>`),
		nil, // presence container gone
	}}
	w := yang.NewTickWatcher(context.Background(), codec, fetch.fetch, codec.DecodeXML,
		yang.WatchConfig{Interval: 15 * time.Millisecond})
	defer func() { _ = w.Close() }()

	events := collectEvents(t, eventChan(w), 3)
	kinds := []yang.ChangeKind{events[0].Kind, events[1].Kind, events[2].Kind}
	want := []yang.ChangeKind{yang.Added, yang.Modified, yang.Removed}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if *events[1].Row.Note != "b" {
		t.Errorf("modified note = %q", *events[1].Row.Note)
	}
}

func TestWalkerYieldsAllRowsOnce(t *testing.T) {
	fetch := &scriptedFetch{payloads: []any{
		serversXML(serverXML("a", 1), serverXML("b", 2)),
	}}
	codec := serverCodec()
	w := yang.NewWalker(context.Background(), fetch.fetch, codec.DecodeXML, 0)

	var names []string
	for row := range w.Iter() {
		names = append(names, *row.Name)
	}
	if err := w.Err(); err != nil {
		t.Fatalf("Err: %v", err)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("rows = %v", names)
	}
}

func TestWalkerFetchFailureLatches(t *testing.T) {
	fetch := &scriptedFetch{payloads: []any{errors.New("boom")}}
	codec := serverCodec()
	w := yang.NewWalker(context.Background(), fetch.fetch, codec.DecodeXML, 0)
	for range w.Iter() {
		t.Fatal("rows yielded from a failed fetch")
	}
	if w.Err() == nil {
		t.Fatal("fetch failure not latched")
	}
}
