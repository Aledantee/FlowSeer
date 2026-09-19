package subscribeloop_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/subscribeloop"
)

// fakeMsg is a message type unrelated to any generated proto, so tests here
// prove Run works for a type the loop was not written against.
type fakeMsg struct {
	id string
}

func msg(id string) *fakeMsg { return &fakeMsg{id: id} }

// fakeStream serves a queued batch of messages, then ends the stream, with or
// without an error.
type fakeStream struct {
	messages []*fakeMsg
	endErr   error
	pos      int
}

func (s *fakeStream) Receive() bool {
	if s.pos >= len(s.messages) {
		return false
	}
	s.pos++
	return true
}

func (s *fakeStream) Msg() *fakeMsg { return s.messages[s.pos-1] }
func (s *fakeStream) Err() error    { return s.endErr }
func (s *fakeStream) Close() error  { return nil }

// fakeCentral opens fakeStreams from a queued sequence of attempts, or
// refuses to open when failOpen is set, and counts how many times it was
// opened.
type fakeCentral struct {
	mu       sync.Mutex
	opens    int
	batches  [][]*fakeMsg
	endErrs  []error
	failOpen bool
	// onOpen, when set, runs as the stream is served, so a test can order
	// the open against what the loop did before it.
	onOpen func()
}

func (c *fakeCentral) open(context.Context) (subscribeloop.Stream[fakeMsg], error) {
	c.mu.Lock()
	c.opens++
	if c.onOpen != nil {
		c.onOpen()
	}
	open := c.opens
	failOpen := c.failOpen
	var batch []*fakeMsg
	if open-1 < len(c.batches) {
		batch = c.batches[open-1]
	}
	var endErr error
	if open-1 < len(c.endErrs) {
		endErr = c.endErrs[open-1]
	}
	c.mu.Unlock()

	if failOpen {
		return nil, errors.New("central is down")
	}
	return &fakeStream{messages: batch, endErr: endErr}, nil
}

func (c *fakeCentral) openCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.opens
}

type handlerFake struct {
	mu   sync.Mutex
	seen []string
	err  error
}

func (h *handlerFake) Handle(_ context.Context, message *fakeMsg) error {
	h.mu.Lock()
	h.seen = append(h.seen, message.id)
	h.mu.Unlock()
	return h.err
}

func (h *handlerFake) devices() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.seen...)
}

// recordingHandler captures every attribute of every record logged, so a
// test can read back an event's attributes by the otel.event.name it was
// logged under.
type recordingHandler struct {
	mu      sync.Mutex
	entries []map[string]any
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	entry := make(map[string]any, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		entry[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	h.entries = append(h.entries, entry)
	h.mu.Unlock()
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// eventNames returns every otel.event.name attribute value logged, in order.
func (h *recordingHandler) eventNames() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var names []string
	for _, e := range h.entries {
		if name, ok := e["otel.event.name"].(string); ok {
			names = append(names, name)
		}
	}
	return names
}

// find returns the attributes of the first record logged for the named
// event.
func (h *recordingHandler) find(event string) (map[string]any, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.entries {
		if e["otel.event.name"] == event {
			return e, true
		}
	}
	return nil, false
}

// testEvents is the naming a test uses when naming is not what it is about.
// Run refuses a Config that names nothing, so every case needs one.
var testEvents = subscribeloop.Events{
	Connected:          "test.connected",
	Disconnected:       "test.disconnected",
	Dropped:            "test.dropped",
	ResyncFailed:       "test.resync_failed",
	ConnectionCountKey: "test.connections",
	MessageCountKey:    "test.messages",
}

// runFor drives the loop until it has made n attempts, then stops it. It
// returns the backoff the loop asked to wait for before each of those
// attempts, which is the only place that sequence is observable.
func runFor(t *testing.T, cfg subscribeloop.Config[fakeMsg], contact *subscribeloop.Contact, attempts int) []time.Duration {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if cfg.Events == (subscribeloop.Events{}) {
		cfg.Events = testEvents
	}
	var (
		mu       sync.Mutex
		backoffs []time.Duration
		waited   atomic.Int64
	)
	cfg.Wait = func(ctx context.Context, d time.Duration) bool {
		mu.Lock()
		backoffs = append(backoffs, d)
		mu.Unlock()
		if waited.Add(1) >= int64(attempts) {
			return false
		}
		return ctx.Err() == nil
	}
	done := make(chan error, 1)
	go func() { done <- subscribeloop.Run(ctx, cfg, contact) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the loop never stopped")
	}

	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(backoffs)
}

// TestAClientThatNeverConnectsIsVisibleAsANumber is the failure a
// reconnecting loop hides. A client dead since its first attempt looks
// exactly like one with nothing to do: no messages, no errors reaching
// anyone, a quiet fleet. Connections is what tells them apart.
func TestAClientThatNeverConnectsIsVisibleAsANumber(t *testing.T) {
	central := &fakeCentral{failOpen: true}
	contact := &subscribeloop.Contact{}
	handler := &handlerFake{}

	runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: handler,
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 3)

	if got := contact.Connections(); got != 0 {
		t.Errorf("Connections() = %d, want 0: no stream was ever served", got)
	}
	if got := contact.Failures(); got == 0 {
		t.Error("Failures() = 0: a loop that never connected must not look idle")
	}
}

// TestTheStreamIsReopenedAfterItEnds covers the ordinary case: the stream
// closes and the loop comes back. Counting opens on the peer proves the
// reconnect happened rather than that the loop merely survived.
func TestTheStreamIsReopenedAfterItEnds(t *testing.T) {
	central := &fakeCentral{batches: [][]*fakeMsg{
		{msg("m-1")},
		{msg("m-2")},
	}}
	contact := &subscribeloop.Contact{}
	handler := &handlerFake{}

	runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: handler,
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 2)

	if got := central.openCount(); got != 2 {
		t.Errorf("the stream was opened %d times, want 2", got)
	}
	if got := contact.Connections(); got != 2 {
		t.Errorf("Connections() = %d, want 2", got)
	}
	if devices := handler.devices(); !slices.Equal(devices, []string{"m-1", "m-2"}) {
		t.Errorf("handled %v, want m-1 then m-2 across the reconnect", devices)
	}
	if got := contact.Messages(); got != 2 {
		t.Errorf("Messages() = %d, want 2: both messages arrived", got)
	}
}

// TestAStreamThatBreaksBeforeDeliveringAnythingIsNotContact holds the
// condition that separates a served stream from a refused one. Connections is
// meant to prove the peer answered, so a stream that ended in an error with
// nothing on it must leave the number alone — only the failure count moves.
//
// The opener cannot be the one to prove this. When opening fails the loop
// returns before it reaches the count at all, so the assertion passes whether
// or not the condition is there; it takes a stream that opened and then broke
// for the condition to be the only thing holding the number down.
func TestAStreamThatBreaksBeforeDeliveringAnythingIsNotContact(t *testing.T) {
	central := &fakeCentral{endErrs: []error{errors.New("stream broke")}}
	contact := &subscribeloop.Contact{}

	runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: &handlerFake{},
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 1)

	if got := central.openCount(); got != 1 {
		t.Fatalf("the stream was opened %d times, want 1", got)
	}
	if got := contact.Connections(); got != 0 {
		t.Errorf("Connections() = %d, want 0: a stream that broke with nothing on it is not contact", got)
	}
	if got := contact.Failures(); got != 1 {
		t.Errorf("Failures() = %d, want 1", got)
	}
}

// TestAHandlerErrorDoesNotDropTheStream: one message failing must not cost
// every other message on the same stream.
func TestAHandlerErrorDoesNotDropTheStream(t *testing.T) {
	central := &fakeCentral{batches: [][]*fakeMsg{
		{msg("m-1"), msg("m-2"), msg("m-3")},
	}}
	contact := &subscribeloop.Contact{}
	handler := &handlerFake{err: errors.New("cannot apply")}

	runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: handler,
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 1)

	if got := len(handler.devices()); got != 3 {
		t.Errorf("handled %d messages, want all 3: one failure must not end the stream", got)
	}
	// One stream, so one connection however many messages crossed it: the
	// count is of contact with the peer, not of what the peer said.
	if got := contact.Connections(); got != 1 {
		t.Errorf("Connections() = %d, want 1: three messages arrived on one stream", got)
	}
}

// TestAPeerThatServesAndClosesLooksHealthyExceptForMessages is the case
// Contact's doc has no story for on its own. A peer accepting every stream
// and closing it immediately leaves Connections climbing and Failures at
// zero, which reads as a working loop — and the backoff is meanwhile
// doubling to its ceiling, because it resets on a delivered message and none
// arrive. Messages is the number that shows nothing is wrong: it must stay
// at zero here or the doc's advice is wrong.
func TestAPeerThatServesAndClosesLooksHealthyExceptForMessages(t *testing.T) {
	central := &fakeCentral{}
	contact := &subscribeloop.Contact{}
	handler := &handlerFake{}

	runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: handler,
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 3)

	if got := contact.Connections(); got != 3 {
		t.Errorf("Connections() = %d, want 3: the peer served every stream", got)
	}
	if got := contact.Failures(); got != 0 {
		t.Errorf("Failures() = %d, want 0: nothing failed", got)
	}
	if got := contact.Messages(); got != 0 {
		t.Errorf("Messages() = %d, want 0: this is the number that shows nothing is arriving", got)
	}
}

// TestEveryAttemptResyncsBeforeTheStreamOpens covers the ordering a resync
// hook depends on: whatever it prepares has to be ready before the stream
// that needs it is open, on every attempt and not only the first.
func TestEveryAttemptResyncsBeforeTheStreamOpens(t *testing.T) {
	var (
		mu    sync.Mutex
		order []string
	)
	record := func(what string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, what)
	}

	central := &fakeCentral{onOpen: func() { record("open") }}
	central.batches = [][]*fakeMsg{{msg("m-1")}, {msg("m-2")}}
	contact := &subscribeloop.Contact{}

	runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: &handlerFake{},
		Resync:     func(context.Context) error { record("resync"); return nil },
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 2)

	if got := central.openCount(); got != 2 {
		t.Fatalf("the stream was opened %d times, want 2", got)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"resync", "open", "resync", "open"}
	if !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

// TestAFailedResyncStillOpensTheStream. Whatever kept the resync from
// answering will most likely stop the stream too; if it does not, a loop
// that can still serve what it already holds is worth more than one that
// stops for what it does not.
func TestAFailedResyncStillOpensTheStream(t *testing.T) {
	central := &fakeCentral{}
	contact := &subscribeloop.Contact{}

	runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: &handlerFake{},
		Resync:     func(context.Context) error { return errors.New("resync did not answer") },
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 2)

	if got := central.openCount(); got != 2 {
		t.Errorf("the stream was opened %d times, want 2: a failed resync must not stop the attempt", got)
	}
	if got := contact.Connections(); got != 2 {
		t.Errorf("Connections() = %d, want 2", got)
	}
}

// TestTheBackoffClimbsUntilAMessageArrives pins the rule that decides how
// hard a peer that is not serving gets retried. It doubles from the floor and
// holds at the ceiling, and only a delivered message puts it back at the
// floor — a successful open does not, because a peer that accepts a stream
// and immediately closes it opens successfully every time, and resetting on
// that would retry it as fast as the machine allows.
func TestTheBackoffClimbsUntilAMessageArrives(t *testing.T) {
	// Four streams served empty, then one that delivers, then one more empty.
	central := &fakeCentral{batches: [][]*fakeMsg{nil, nil, nil, nil, {msg("m-1")}, nil}}
	contact := &subscribeloop.Contact{}

	backoffs := runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: &handlerFake{},
		MinBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond,
	}, contact, 6)

	ms := time.Millisecond
	// The fifth wait is the assertion: the attempt before it delivered, so
	// the backoff earned by the four empty ones is dropped. The third and
	// fourth are the ceiling holding.
	want := []time.Duration{1 * ms, 2 * ms, 4 * ms, 4 * ms, 1 * ms, 2 * ms}
	if !slices.Equal(backoffs, want) {
		t.Errorf("backoffs = %v, want %v", backoffs, want)
	}
}

// TestAnUnnamedLoopIsRefused. A Config that names no events logs records with
// an empty event name and numbers under an empty key, which nothing
// downstream rejects and no query finds: the caller would learn of it from a
// dashboard that stayed empty. Refused at the start instead.
func TestAnUnnamedLoopIsRefused(t *testing.T) {
	err := subscribeloop.Run(t.Context(), subscribeloop.Config[fakeMsg]{
		Open:    (&fakeCentral{}).open,
		Handler: &handlerFake{},
	}, &subscribeloop.Contact{})
	if err == nil {
		t.Fatal("Run() = nil, want a refusal for a Config that names no events")
	}
	if code, ok := errs.CodeOf(err); !ok || code != subscribeloop.ErrCodeRun {
		t.Errorf("error code = %q ok=%v, want %q", code, ok, subscribeloop.ErrCodeRun)
	}
}

// TestEventNamesAndCountsComeFromConfig: the loop logs the caller's event
// names, not names of its own, and the two counts it computes itself land
// under the caller's attribute keys. It also proves LogAttrs reaches the
// dropped event, which is the one place a per-message attribute is added.
func TestEventNamesAndCountsComeFromConfig(t *testing.T) {
	streamErr := errors.New("stream broke")
	central := &fakeCentral{
		batches: [][]*fakeMsg{{msg("m-1")}},
		endErrs: []error{streamErr},
	}
	contact := &subscribeloop.Contact{}
	handler := &handlerFake{err: errors.New("cannot apply")}
	rec := &recordingHandler{}
	events := subscribeloop.Events{
		Connected:          "test.connected",
		Disconnected:       "test.disconnected",
		Dropped:            "test.dropped",
		ResyncFailed:       "test.resync_failed",
		ConnectionCountKey: "test.connections",
		MessageCountKey:    "test.messages",
	}

	runFor(t, subscribeloop.Config[fakeMsg]{
		Open: central.open, Handler: handler,
		Resync: func(context.Context) error { return errors.New("resync broke") },
		Events: events,
		LogAttrs: func(m *fakeMsg) []slog.Attr {
			return []slog.Attr{slog.String("test.message_id", m.id)}
		},
		Logger:     slog.New(rec),
		MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond,
	}, contact, 1)

	names := rec.eventNames()
	for _, want := range []string{events.ResyncFailed, events.Connected, events.Dropped, events.Disconnected} {
		if !slices.Contains(names, want) {
			t.Errorf("event names = %v, want to contain %q", names, want)
		}
	}

	if attrs, ok := rec.find(events.Connected); !ok {
		t.Errorf("no %q event logged", events.Connected)
	} else if got, _ := attrs[events.ConnectionCountKey].(int64); got != 1 {
		t.Errorf("%s = %v, want 1", events.ConnectionCountKey, attrs[events.ConnectionCountKey])
	}

	if attrs, ok := rec.find(events.Disconnected); !ok {
		t.Errorf("no %q event logged", events.Disconnected)
	} else if got, _ := attrs[events.MessageCountKey].(int64); got != 1 {
		t.Errorf("%s = %v, want 1", events.MessageCountKey, attrs[events.MessageCountKey])
	}

	if attrs, ok := rec.find(events.Dropped); !ok {
		t.Errorf("no %q event logged", events.Dropped)
	} else if got, _ := attrs["test.message_id"].(string); got != "m-1" {
		t.Errorf("test.message_id = %q, want %q: LogAttrs must reach the dropped event", got, "m-1")
	}
}
