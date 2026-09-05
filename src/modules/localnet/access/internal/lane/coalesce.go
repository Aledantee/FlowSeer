package lane

import (
	"context"
	"sync"
)

// CoalesceKey identifies the target of a pollable read for coalescing
// purposes. OperationKind distinguishes capabilities and, deliberately, a
// mutation's own kind from a read's — a Coalescer key is only ever built
// for a TypedRead in this module, never for a MutationIntent, so the two
// never share a ticket even when Target names the same interface.
type CoalesceKey struct {
	Device        string
	OperationKind string
	Target        string
}

// ticket is the shared result one coalesced group of callers waits on.
type ticket struct {
	done   chan struct{}
	result any
	err    error
}

// Wait blocks until the ticket's owner calls [Coalescer.Finish] for its key
// or ctx is done, whichever comes first.
func (t *ticket) Wait(ctx context.Context) (any, error) {
	select {
	case <-t.done:
		return t.result, t.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Coalescer deduplicates concurrent identical reads: the first caller for a
// key does the work and calls Finish; every other caller for the same key
// while that work is in flight receives the same result. The zero value is
// ready to use.
type Coalescer struct {
	mu       sync.Mutex
	inflight map[CoalesceKey]*ticket
}

// Start reports whether the caller is the first for key. When isNew is
// true, the caller must do the work and call Finish exactly once with key;
// every subsequent Start for the same key before Finish returns the same
// ticket with isNew false, and the caller should Wait on it instead.
func (c *Coalescer) Start(key CoalesceKey) (t *ticket, isNew bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.inflight == nil {
		c.inflight = make(map[CoalesceKey]*ticket)
	}

	if existing, ok := c.inflight[key]; ok {
		return existing, false
	}

	t = &ticket{done: make(chan struct{})}
	c.inflight[key] = t

	return t, true
}

// Finish delivers result and err to every caller waiting on key's ticket
// and removes key from the in-flight set so the next Start for key admits
// fresh work.
func (c *Coalescer) Finish(key CoalesceKey, result any, err error) {
	c.mu.Lock()
	t, ok := c.inflight[key]
	if ok {
		delete(c.inflight, key)
	}
	c.mu.Unlock()

	if !ok {
		return
	}

	t.result = result
	t.err = err
	close(t.done)
}
