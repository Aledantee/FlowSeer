package lane

import (
	"context"
	"sync"
)

// CoalesceKey identifies the target of a pollable read for coalescing
// purposes. OperationKind distinguishes capabilities and, deliberately, a
// mutation's own kind from a read's — a Coalescer key is only ever built
// for a TypedRead in this module, never for a MutationIntent, so the two
// never share a Ticket even when Target names the same interface.
//
// PolicyKey and PolicyVersion are part of the identity because authority is
// checked by the act of acquiring a credential, every time. A joiner is
// answered from the session the owner opened, so two reads that coalesce
// are two reads served under one acquisition: if they were admitted under
// different access policies, the joiner's own policy was never checked
// against anything.
type CoalesceKey struct {
	Device        string
	OperationKind string
	Target        string
	PolicyKey     string
	PolicyVersion uint64
}

// Ticket is the shared result one coalesced group of callers waits on.
// Safe for concurrent use: Wait may be called from multiple goroutines.
type Ticket struct {
	done   chan struct{}
	once   sync.Once
	result any
	err    error
}

// Wait blocks until the Ticket's owner calls [Coalescer.Finish] for its key
// or ctx is done, whichever comes first.
func (t *Ticket) Wait(ctx context.Context) (any, error) {
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
// ready to use. Safe for concurrent use: Start and Finish may both be
// called from multiple goroutines.
type Coalescer struct {
	mu       sync.Mutex
	inflight map[CoalesceKey]*Ticket
}

// Start reports whether the caller is the first for key. When isNew is
// true, the caller owns the work and must call the returned finish exactly
// once; every subsequent Start for the same key before then returns the
// same Ticket with isNew false, and that caller should Wait on it instead.
//
// The terminator is returned rather than named by key, because a key does
// not identify a ticket over time. A finish that resolved its key would
// deliver this work's result to whichever group happened to be waiting when
// it ran, which after a second Start for the same key is a different group
// entirely. finish is bound to the ticket it created, is a no-op after the
// first call, and removes the map entry only while that entry is still its
// own.
func (c *Coalescer) Start(key CoalesceKey) (t *Ticket, finish func(result any, err error), isNew bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.inflight == nil {
		c.inflight = make(map[CoalesceKey]*Ticket)
	}

	if existing, ok := c.inflight[key]; ok {
		return existing, nil, false
	}

	t = &Ticket{done: make(chan struct{})}
	c.inflight[key] = t

	return t, func(result any, err error) { c.finish(key, t, result, err) }, true
}

// finish delivers result and err to every caller waiting on t, once.
func (c *Coalescer) finish(key CoalesceKey, t *Ticket, result any, err error) {
	c.mu.Lock()
	if c.inflight[key] == t {
		delete(c.inflight, key)
	}
	c.mu.Unlock()

	t.once.Do(func() {
		t.result = result
		t.err = err
		close(t.done)
	})
}
