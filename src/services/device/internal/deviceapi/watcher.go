package deviceapi

import (
	"context"

	"github.com/nats-io/nats.go/jetstream"
)

// KVWatcher wakes a waiting read from the lane bucket's own key watch, so a
// read closed by a report that landed on another central replica wakes this
// one. Without it a read still answers, from the poll, just later.
type KVWatcher struct {
	kv jetstream.KeyValue
}

// NewKVWatcher watches the lane bucket kv.
func NewKVWatcher(kv jetstream.KeyValue) *KVWatcher { return &KVWatcher{kv: kv} }

// Watch returns a channel that receives when the device's record changes, and
// the function that stops the watch. The channel is never closed by the
// watcher: a caller selects on it alongside its own context, and a closed
// channel would spin that select. Updates are dropped rather than queued,
// since a waiter re-reads the record on every wakeup and only needs to know
// that something changed.
func (w *KVWatcher) Watch(ctx context.Context, deviceID string) (<-chan struct{}, func(), error) {
	watcher, err := w.kv.Watch(ctx, deviceID, jetstream.UpdatesOnly(), jetstream.IgnoreDeletes())
	if err != nil {
		return nil, nil, err
	}

	changed := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		updates := watcher.Updates()
		for {
			select {
			case <-done:
				return
			case entry, ok := <-updates:
				if !ok {
					return
				}
				if entry == nil {
					continue // the watcher's end-of-initial-values marker
				}
				select {
				case changed <- struct{}{}:
				default: // a wakeup is already pending; one is enough
				}
			}
		}
	}()

	stop := func() {
		close(done)
		_ = watcher.Stop()
	}
	return changed, stop, nil
}
