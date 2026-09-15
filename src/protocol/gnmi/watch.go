package gnmi

import (
	"context"
	"iter"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/pump"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/protocol/yang"
)

// watch.go wires the Collection Primitives onto gNMI: the
// Walker runs Subscribe ONCE and yields the assembled rows; the
// Watcher wraps Subscribe STREAM — the device owns the cadence, the
// sync_response maps to cold-start-complete (initial rows emit as
// Added), and stream termination latches Err. Reconnect is the
// caller's action; a re-created Watcher cold-starts and never
// double-emits modifies.

// WatchOptions tunes the stream-fed primitives. Values can be reused
// concurrently while unmodified.
type WatchOptions struct {
	// SampleInterval requests SAMPLE cadence when > 0; zero leaves
	// the cadence to the device (TARGET_DEFINED / ON_CHANGE).
	SampleInterval time.Duration
	// Origin sets the gNMI path origin for peers that require one.
	Origin string
	// Buffer sizes the event channel. <= 0 uses the default.
	Buffer int
}

// Walk runs a bounded traversal of desc's subtree: one Subscribe
// ONCE, rows assembled from the snapshot's updates and yielded after
// the device's sync.
func Walk[Row any, Key comparable](ctx context.Context, sess *Session, desc yang.ListDescriptor[Row, Key], opts WatchOptions) *yang.Walker[Row] {
	return yang.NewRowWalker(ctx, func(walkCtx context.Context) ([]Row, error) {
		stream, err := sess.Subscribe(walkCtx, SubscribeOptions{
			Mode:   ModeOnce,
			Paths:  []yang.Path{desc.Path},
			Origin: opts.Origin,
			Buffer: opts.Buffer,
		})
		if err != nil {
			return nil, err
		}
		defer func() { _ = stream.Close() }()

		store := newRowStore(desc)
		for ev := range stream.Iter() {
			if ev.Sync {
				break
			}
			for _, u := range ev.Updates {
				if _, _, err := store.applyUpdate(u); err != nil {
					return nil, err
				}
			}
			for _, d := range ev.Deletes {
				store.applyDelete(d)
			}
		}
		if err := stream.Err(); err != nil {
			return nil, err
		}
		rows := make([]Row, 0, len(store.rows))
		for _, id := range store.ids() {
			row, err := store.decodeRow(id)
			if err != nil {
				return nil, err
			}
			rows = append(rows, row)
		}
		return rows, nil
	}, opts.Buffer)
}

// Watcher is the stream-fed change watcher. Consume via
// [Watcher.Iter]; check [Watcher.Err] after the loop; Close
// terminates. The zero value is unusable. One goroutine may iterate
// while other goroutines call [Watcher.Close] or [Watcher.Err].
type Watcher[Row any, Key comparable] struct {
	pump   *pump.Pump[yang.WatchEvent[Row, Key]]
	stream *Stream
}

// Watch subscribes STREAM to desc's subtree and emits row-granular
// change events: updates before sync buffer as initial state and
// emit as Added at sync (the SNMP cold-start contract); afterwards
// each notification batch emits at most one event per affected row.
func Watch[Row any, Key comparable](ctx context.Context, sess *Session, desc yang.ListDescriptor[Row, Key], opts WatchOptions) (*Watcher[Row, Key], error) {
	stream, err := sess.Subscribe(ctx, SubscribeOptions{
		Mode:           ModeStream,
		Paths:          []yang.Path{desc.Path},
		SampleInterval: opts.SampleInterval,
		Origin:         opts.Origin,
		Buffer:         opts.Buffer,
	})
	if err != nil {
		return nil, err
	}

	buf := opts.Buffer
	if buf <= 0 {
		buf = defaultEventBuffer
	}
	w := &Watcher[Row, Key]{pump: pump.New[yang.WatchEvent[Row, Key]](ctx, buf), stream: stream}
	// run's own defers close the pump and the stream unconditionally, but
	// only its explicit calls set an error; ReportTo gives a panic the
	// pump.Fail a silent CloseData would otherwise skip.
	spawn.Go(ctx, "gnmi watch", func() { w.run(stream, desc) }, spawn.ReportTo(w.pump.Fail))
	return w, nil
}

// run consumes the stream and translates batches into row events.
func (w *Watcher[Row, Key]) run(stream *Stream, desc yang.ListDescriptor[Row, Key]) {
	defer func() { _ = stream.Close() }()
	defer w.pump.Cancel()
	defer w.pump.CloseData()

	store := newRowStore(desc)
	emitted := make(map[string]Row) // rows the consumer has seen
	synced := false

	for ev := range stream.Iter() {
		if ev.Sync && !synced {
			synced = true
			for _, id := range store.ids() {
				row, err := store.decodeRow(id)
				if err != nil {
					w.pump.Fail(err)
					return
				}
				emitted[id] = row
				if !w.pump.Send(yang.WatchEvent[Row, Key]{Kind: yang.Added, Key: desc.Codec.Key(row), Row: row}) {
					return
				}
			}
			continue
		}

		affected := make(map[string]bool)
		removed := make(map[string]bool)
		for _, u := range ev.Updates {
			id, ok, err := store.applyUpdate(u)
			if err != nil {
				w.pump.Fail(err)
				return
			}
			if ok {
				affected[id] = true
			}
		}
		for _, d := range ev.Deletes {
			mod, rem := store.applyDelete(d)
			for _, id := range mod {
				affected[id] = true
			}
			for _, id := range rem {
				removed[id] = true
				delete(affected, id)
			}
		}
		if !synced {
			continue
		}
		if !w.emitBatch(store, desc, emitted, affected, removed) {
			return
		}
	}

	if err := stream.Err(); err != nil {
		w.pump.Fail(err)
	}
}

// emitBatch turns one batch's affected/removed sets into events;
// false means the consumer terminated.
func (w *Watcher[Row, Key]) emitBatch(store *rowStore[Row, Key], desc yang.ListDescriptor[Row, Key], emitted map[string]Row, affected, removed map[string]bool) bool {
	for id := range removed {
		prev, seen := emitted[id]
		if !seen {
			continue
		}
		delete(emitted, id)
		if !w.pump.Send(yang.WatchEvent[Row, Key]{Kind: yang.Removed, Key: desc.Codec.Key(prev), Row: prev}) {
			return false
		}
	}
	for _, id := range sortedIDs(affected) {
		row, err := store.decodeRow(id)
		if err != nil {
			w.pump.Fail(err)
			return false
		}
		prev, seen := emitted[id]
		switch {
		case !seen:
			emitted[id] = row
			if !w.pump.Send(yang.WatchEvent[Row, Key]{Kind: yang.Added, Key: desc.Codec.Key(row), Row: row}) {
				return false
			}
		case !desc.Codec.Equal(prev, row):
			emitted[id] = row
			if !w.pump.Send(yang.WatchEvent[Row, Key]{Kind: yang.Modified, Key: desc.Codec.Key(row), Row: row}) {
				return false
			}
		}
	}
	return true
}

// sortedIDs orders a set for deterministic emission.
func sortedIDs(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Iter yields events until the Watcher terminates. Check
// [Watcher.Err] after the loop. Breaking iteration closes the
// subscription and waits for the producer to finish.
func (w *Watcher[Row, Key]) Iter() iter.Seq[yang.WatchEvent[Row, Key]] {
	return func(yield func(yang.WatchEvent[Row, Key]) bool) {
		for ev := range w.pump.Data() {
			if !yield(ev) {
				_ = w.Close()
				for range w.pump.Data() { //nolint:revive // drain so the producer never blocks
				}
				return
			}
		}
	}
}

// Err returns the Watcher's terminal error, or nil.
func (w *Watcher[Row, Key]) Err() error { return w.pump.Err() }

// Close terminates the Watcher. Idempotent.
func (w *Watcher[Row, Key]) Close() error {
	w.pump.SignalStop()
	w.pump.Cancel()
	return w.stream.Close()
}
