package yang

import (
	"context"
	"iter"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/pump"
)

// watch.go is the shared row plumbing under the protocol libraries'
// Collection Primitives, mirroring the SNMP library's shape: a generic
// bounded Walker and a tick-diff Watcher engine, both driven by a
// protocol-supplied fetch function and a [ListDescriptor]'s row
// codec. The NETCONF and RESTCONF libraries wrap these; gNMI's
// stream-fed Watcher, built on Subscribe STREAM, lives in the gnmi
// package.

// ChangeKind classifies a watch event.
type ChangeKind int

// The event kinds. Cold start emits Added for every row.
const (
	Added ChangeKind = iota + 1
	Modified
	Removed
)

// String returns the kind's name.
func (k ChangeKind) String() string {
	switch k {
	case Added:
		return "added"
	case Modified:
		return "modified"
	case Removed:
		return "removed"
	}
	return "invalid"
}

// WatchEvent is one row change.
type WatchEvent[Row any, Key comparable] struct {
	Kind ChangeKind
	Key  Key
	// Row is the current row for Added and Modified, and the last
	// known row for Removed.
	Row Row
}

// FetchFunc reads the watched subtree's current wire payload. The
// Walker and Watcher own the cadence; the protocol library owns the
// bytes.
type FetchFunc func(ctx context.Context) ([]byte, error)

// ErrCodeWatch marks a Walker/Watcher terminal failure (fetch or
// decode) after the configured tolerance.
var ErrCodeWatch = errs.NewCode("yang/watch")

// Walker is a bounded traversal: one fetch, every row decoded and
// yielded, then completion. Consume via [Walker.Iter] and check
// [Walker.Err] after the loop; [Walker.Close] terminates early.
type Walker[Row any] struct {
	pump *pump.Pump[Row]
}

// NewWalker starts a bounded traversal of desc's subtree: fetch
// reads the payload, decode is the descriptor's wire decoder
// (RowCodec.DecodeXML or DecodeJSON). buffer <= 0 uses a small
// default.
func NewWalker[Row any](ctx context.Context, fetch FetchFunc, decode func([]byte) ([]Row, error), buffer int) *Walker[Row] {
	return NewRowWalker(ctx, func(walkCtx context.Context) ([]Row, error) {
		payload, err := fetch(walkCtx)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeWatch).Msg("walker fetch failed")
		}
		rows, err := decode(payload)
		if err != nil {
			return nil, errs.From(err).Code(ErrCodeWatch).Msg("walker decode failed")
		}
		return rows, nil
	}, buffer)
}

// NewRowWalker starts a bounded traversal whose producer yields
// decoded rows directly — the seam for protocols whose reads are not
// one payload (gNMI Subscribe ONCE).
func NewRowWalker[Row any](ctx context.Context, produce func(ctx context.Context) ([]Row, error), buffer int) *Walker[Row] {
	if buffer <= 0 {
		buffer = 64
	}
	w := &Walker[Row]{pump: pump.New[Row](ctx, buffer)}
	go func() {
		defer w.pump.Done()
		rows, err := produce(w.pump.Context())
		if err != nil {
			w.pump.Fail(errs.From(err).Code(ErrCodeWatch).Msg("walker traversal failed"))
			return
		}
		for _, row := range rows {
			if !w.pump.Send(row) {
				return
			}
		}
	}()
	return w
}

// Iter yields the rows. Check [Walker.Err] after the loop.
func (w *Walker[Row]) Iter() iter.Seq[Row] {
	return func(yield func(Row) bool) {
		for row := range w.pump.Data() {
			if !yield(row) {
				w.pump.SignalStop()
				for range w.pump.Data() { //nolint:revive // drain so the producer never blocks
				}
				return
			}
		}
	}
}

// Err returns the walk's terminal error, or nil on natural
// completion.
func (w *Walker[Row]) Err() error { return w.pump.Err() }

// Close terminates the walk early. Idempotent.
func (w *Walker[Row]) Close() error {
	w.pump.SignalStop()
	w.pump.Cancel()
	return nil
}

// WatchConfig tunes a tick Watcher.
type WatchConfig struct {
	// Interval is the tick cadence. Zero means 30s.
	Interval time.Duration
	// MaxConsecutiveTickFailures latches the Watcher after this many
	// failed ticks in a row. Zero means 5.
	MaxConsecutiveTickFailures int
	// Buffer sizes the event channel. <= 0 means 256.
	Buffer int
}

// withDefaults returns a copy with zero values defaulted.
func (c WatchConfig) withDefaults() WatchConfig {
	if c.Interval <= 0 {
		c.Interval = 30 * time.Second
	}
	if c.MaxConsecutiveTickFailures <= 0 {
		c.MaxConsecutiveTickFailures = 5
	}
	if c.Buffer <= 0 {
		c.Buffer = 256
	}
	return c
}

// TickWatcher re-reads a bounded subtree every tick and diffs rows by
// identity: cold start emits Added per row, later ticks emit
// Added/Modified/Removed per changed row. Transient tick errors go to
// [TickWatcher.LastTickErr]; the configured consecutive-failure
// threshold latches the Watcher terminally. A dropped session never
// resumes: the caller reconnects and a re-created Watcher cold-starts.
type TickWatcher[Row any, Key comparable] struct {
	pump  *pump.Pump[WatchEvent[Row, Key]]
	codec RowCodec[Row, Key]

	mu          sync.Mutex
	lastTickErr error
}

// NewTickWatcher starts the tick loop. decode selects the wire form
// (the descriptor's DecodeXML or DecodeJSON); fetch is the protocol
// read.
func NewTickWatcher[Row any, Key comparable](ctx context.Context, codec RowCodec[Row, Key], fetch FetchFunc, decode func([]byte) ([]Row, error), cfg WatchConfig) *TickWatcher[Row, Key] {
	cfg = cfg.withDefaults()
	w := &TickWatcher[Row, Key]{
		pump:  pump.New[WatchEvent[Row, Key]](ctx, cfg.Buffer),
		codec: codec,
	}
	go w.run(fetch, decode, cfg)
	return w
}

// run drives cold start and the steady-state tick loop.
func (w *TickWatcher[Row, Key]) run(fetch FetchFunc, decode func([]byte) ([]Row, error), cfg WatchConfig) {
	defer w.pump.CloseData()

	snapshot, ok := w.coldStart(fetch, decode, cfg)
	if !ok {
		return
	}

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-w.pump.Stopped():
			return
		case <-w.pump.Context().Done():
			return
		case <-ticker.C:
		}

		rows, err := w.read(fetch, decode)
		if err != nil {
			failures++
			w.recordTickErr(err)
			if failures >= cfg.MaxConsecutiveTickFailures {
				w.pump.Fail(errs.From(err).
					Code(ErrCodeWatch).
					Attr("consecutive_failures", failures).
					Msg("watcher latched after consecutive tick failures"))
				return
			}
			continue
		}
		failures = 0

		next := make(map[Key]Row, len(rows))
		for _, row := range rows {
			key := w.codec.Key(row)
			next[key] = row
			prev, existed := snapshot[key]
			switch {
			case !existed:
				if !w.pump.Send(WatchEvent[Row, Key]{Kind: Added, Key: key, Row: row}) {
					return
				}
			case !w.codec.Equal(prev, row):
				if !w.pump.Send(WatchEvent[Row, Key]{Kind: Modified, Key: key, Row: row}) {
					return
				}
			}
		}
		for key, prev := range snapshot {
			if _, still := next[key]; !still {
				if !w.pump.Send(WatchEvent[Row, Key]{Kind: Removed, Key: key, Row: prev}) {
					return
				}
			}
		}
		snapshot = next
	}
}

// coldStart performs the first read, retrying within the failure
// budget, and emits Added for every row.
func (w *TickWatcher[Row, Key]) coldStart(fetch FetchFunc, decode func([]byte) ([]Row, error), cfg WatchConfig) (map[Key]Row, bool) {
	var rows []Row
	failures := 0
	for {
		var err error
		rows, err = w.read(fetch, decode)
		if err == nil {
			break
		}
		failures++
		w.recordTickErr(err)
		if failures >= cfg.MaxConsecutiveTickFailures {
			w.pump.Fail(errs.From(err).Code(ErrCodeWatch).Msg("watcher cold start failed"))
			return nil, false
		}
		select {
		case <-w.pump.Stopped():
			return nil, false
		case <-w.pump.Context().Done():
			return nil, false
		case <-time.After(cfg.Interval):
		}
	}

	snapshot := make(map[Key]Row, len(rows))
	for _, row := range rows {
		key := w.codec.Key(row)
		snapshot[key] = row
		if !w.pump.Send(WatchEvent[Row, Key]{Kind: Added, Key: key, Row: row}) {
			return nil, false
		}
	}
	return snapshot, true
}

// read is one fetch+decode.
func (w *TickWatcher[Row, Key]) read(fetch FetchFunc, decode func([]byte) ([]Row, error)) ([]Row, error) {
	payload, err := fetch(w.pump.Context())
	if err != nil {
		return nil, err
	}
	if payload == nil {
		// An absent subtree is an empty row set (presence container
		// gone, RESTCONF 404): every snapshot row becomes Removed.
		return nil, nil
	}
	return decode(payload)
}

// recordTickErr stores the most recent transient failure.
func (w *TickWatcher[Row, Key]) recordTickErr(err error) {
	w.mu.Lock()
	w.lastTickErr = err
	w.mu.Unlock()
}

// LastTickErr returns the most recent transient tick failure, or nil.
// A non-nil value with a live Watcher means a tick was skipped, not
// that the stream ended; [TickWatcher.Err] is the terminal signal.
func (w *TickWatcher[Row, Key]) LastTickErr() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastTickErr
}

// Iter yields events until the Watcher terminates. Check
// [TickWatcher.Err] after the loop.
func (w *TickWatcher[Row, Key]) Iter() iter.Seq[WatchEvent[Row, Key]] {
	return func(yield func(WatchEvent[Row, Key]) bool) {
		for ev := range w.pump.Data() {
			if !yield(ev) {
				w.pump.SignalStop()
				for range w.pump.Data() { //nolint:revive // drain so the producer never blocks
				}
				return
			}
		}
	}
}

// Err returns the Watcher's terminal error, or nil.
func (w *TickWatcher[Row, Key]) Err() error { return w.pump.Err() }

// Close terminates the Watcher. Idempotent; buffered events remain
// readable.
func (w *TickWatcher[Row, Key]) Close() error {
	w.pump.SignalStop()
	w.pump.Cancel()
	return nil
}
