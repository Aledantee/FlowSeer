package gnmi

import (
	"context"
	"errors"
	"io"
	"iter"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/pump"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

// defaultEventBuffer sizes the stream's event channel: enough to
// absorb a burst of updates between consumer reads without pinning
// much memory.
const defaultEventBuffer = 256

// SubscribeMode selects the subscription lifetime. POLL is unsupported.
type SubscribeMode int

const (
	// ModeStream subscribes long-lived: the device owns the cadence.
	ModeStream SubscribeMode = iota
	// ModeOnce requests one full snapshot terminated by
	// sync_response — the bounded-traversal backend.
	ModeOnce
)

// SubscribeOptions tunes one subscription. Fields and paths must not
// be modified concurrently with [Session.Subscribe].
type SubscribeOptions struct {
	// Mode is ModeStream or ModeOnce.
	Mode SubscribeMode
	// Paths are the subscribed subtrees. At least one is required.
	Paths []yang.Path
	// SampleInterval asks for SAMPLE cadence in Stream mode when
	// > 0; otherwise TARGET_DEFINED leaves cadence to the peer.
	SampleInterval time.Duration
	// Origin sets the gNMI path origin on the subscription prefix
	// (e.g. "openconfig") for peers that require it.
	Origin string
	// Buffer overrides the event channel size; <= 0 uses the
	// default.
	Buffer int
}

// SubscribeEvent is one element of a subscription stream — one
// notification batch, or the sync marker. Batch granularity is
// deliberate: a row changing several leaves arrives as one event, so
// consumers can emit exactly one change per row.
// Referenced slices and values must remain unchanged during concurrent reads.
type SubscribeEvent struct {
	// Sync marks the device's sync_response: the initial state is
	// complete (the Watcher's cold-start-complete signal). No other
	// field is set.
	Sync bool
	// Updates are the notification's updates, prefixes resolved.
	Updates []Update
	// Deletes are the notification's deleted paths, prefixes
	// resolved.
	Deletes []yang.Path
	// Timestamp is the notification's device timestamp.
	Timestamp time.Time
}

// Stream is a pump-backed subscription. Consume via [Stream.Iter],
// check [Stream.Err] after the loop, and [Stream.Close] to terminate
// early. A dropped stream never resumes itself: reconnecting is the
// caller's action, and a re-created stream cold-starts.
// The zero value is unusable. One goroutine may iterate while other
// goroutines call [Stream.Close] or [Stream.Err].
type Stream struct {
	pump *pump.Pump[SubscribeEvent]
}

// Subscribe opens a subscription. The stream lives until the
// server ends it (ONCE after sync), the caller closes it, or ctx is
// canceled.
func (s *Session) Subscribe(ctx context.Context, opts SubscribeOptions) (*Stream, error) {
	if s.isClosed() {
		return nil, ErrSessionClosed
	}
	if len(opts.Paths) == 0 {
		return nil, errs.New().Code(ErrCodeRPC).Msg("subscribe requires at least one path")
	}

	buf := opts.Buffer
	if buf <= 0 {
		buf = defaultEventBuffer
	}
	st := &Stream{pump: pump.New[SubscribeEvent](ctx, buf)}

	sctx, cancel := context.WithCancel(s.withCreds(ctx))
	sc, err := s.client.Subscribe(sctx)
	if err != nil {
		err = s.mapError(sctx, "Subscribe", err)
		cancel()
		return nil, err
	}
	if err := sc.Send(&gpb.SubscribeRequest{Request: &gpb.SubscribeRequest_Subscribe{Subscribe: s.subscriptionList(opts)}}); err != nil {
		err = s.mapError(sctx, "Subscribe", err)
		cancel()
		return nil, err
	}

	// A blocked Recv only returns when the stream context dies, so
	// Close (which signals the pump) must also cancel sctx. cancel is
	// deferred rather than called only on the Stopped branch: a panic
	// here must still cancel sctx, or the receive goroutine stays parked
	// in Recv for the life of the process. Close itself does not join it,
	// so the cost is a leaked goroutine rather than a hang.
	spawn.Go(sctx, "gnmi subscribe cancel watcher", func() {
		defer cancel()
		select {
		case <-st.pump.Stopped():
		case <-sctx.Done():
		}
	})

	// receive returns when the stream is finished; the caller below makes the
	// single terminal pump call. Done is not deferred inside fn: fn's defers
	// run before the recover, so a deferred Done would close the data channel
	// before the sink recorded the panic, and a consumer draining to the close
	// would read a nil Err() for a subscription that panicked. On the panic
	// path Fail is the terminal call, and it records before it closes.
	receive := func() {
		for {
			select {
			case <-st.pump.Stopped():
				return
			default:
			}
			resp, err := sc.Recv()
			if err != nil {
				select {
				case <-st.pump.Stopped():
					// Caller-initiated Close: clean termination, not
					// an error.
					return
				default:
				}
				if errors.Is(err, io.EOF) {
					// ONCE streams end cleanly after sync; STREAM
					// peers ending the stream is a terminal event the
					// consumer must see.
					st.pump.Done()
					return
				}
				if sctx.Err() != nil {
					st.pump.Fail(sctx.Err())
					return
				}
				st.pump.Fail(s.mapError(sctx, "Subscribe", err))
				return
			}
			if !st.deliver(resp) {
				return
			}
		}
	}
	spawn.Go(sctx, "gnmi subscribe receive", func() {
		defer cancel()
		receive()
		st.pump.Done()
	}, spawn.ReportTo(st.pump.Fail))
	return st, nil
}

// subscriptionList builds the SubscriptionList for the options.
func (s *Session) subscriptionList(opts SubscribeOptions) *gpb.SubscriptionList {
	list := &gpb.SubscriptionList{Encoding: s.encoding}
	if opts.Origin != "" {
		list.Prefix = &gpb.Path{Origin: opts.Origin}
	}
	if opts.Mode == ModeOnce {
		list.Mode = gpb.SubscriptionList_ONCE
	} else {
		list.Mode = gpb.SubscriptionList_STREAM
	}
	for _, p := range opts.Paths {
		sub := &gpb.Subscription{Path: ToProtoPath(p)}
		if opts.Mode == ModeStream {
			if opts.SampleInterval > 0 {
				sub.Mode = gpb.SubscriptionMode_SAMPLE
				sub.SampleInterval = uint64(opts.SampleInterval.Nanoseconds())
			} else {
				sub.Mode = gpb.SubscriptionMode_TARGET_DEFINED
			}
		}
		list.Subscription = append(list.Subscription, sub)
	}
	return list
}

// deliver fans one SubscribeResponse into one event; false means the
// consumer signaled termination.
func (st *Stream) deliver(resp *gpb.SubscribeResponse) bool {
	switch r := resp.GetResponse().(type) {
	case *gpb.SubscribeResponse_SyncResponse:
		return st.pump.Send(SubscribeEvent{Sync: true})
	case *gpb.SubscribeResponse_Update:
		n := r.Update
		ev := SubscribeEvent{Timestamp: time.Unix(0, n.GetTimestamp())}
		prefix := FromProtoPath(n.GetPrefix())
		for _, u := range n.GetUpdate() {
			upd, err := decodeUpdate(prefix, u, ev.Timestamp)
			if err != nil {
				st.pump.Fail(err)
				return false
			}
			ev.Updates = append(ev.Updates, upd)
		}
		for _, d := range n.GetDelete() {
			ev.Deletes = append(ev.Deletes, joinPaths(prefix, FromProtoPath(d)))
		}
		return st.pump.Send(ev)
	}
	return true
}

// Iter returns the range-over-function form of the stream. Check
// [Stream.Err] after the loop: nil means clean termination (ONCE
// completion, Close, or a server-side clean end).
func (st *Stream) Iter() iter.Seq[SubscribeEvent] {
	return func(yield func(SubscribeEvent) bool) {
		for ev := range st.pump.Data() {
			if !yield(ev) {
				st.pump.SignalStop()
				for range st.pump.Data() { //nolint:revive // drain so the producer never blocks
				}
				return
			}
		}
	}
}

// Err returns the stream's terminal error, or nil while running or
// after clean termination.
func (st *Stream) Err() error { return st.pump.Err() }

// Close terminates the stream early. Idempotent; buffered events
// remain readable.
func (st *Stream) Close() error {
	st.pump.SignalStop()
	st.pump.Cancel()
	return nil
}
