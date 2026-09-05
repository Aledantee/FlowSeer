package ssh

import (
	"context"
	"errors"
	"sync"
)

// errRingClosed marks a ring drained to a clean end (e.g. the
// underlying stream reached EOF) with no caller-supplied error. It
// lets waitFor always return a non-nil error on closure, so a match
// consuming zero bytes is never confused with "the ring closed
// cleanly."
var errRingClosed = errors.New("ssh: stream closed")

// ring is a bounded, drop-oldest byte buffer with a match-based wait.
// It retains at most cap bytes of the most recently written data
// (the tail matters for prompt and pagination detection, not the
// history), while totalWritten counts every byte ever written so a
// caller can report a truncated stream's true size. The zero value is
// not usable; construct via newRing. Safe for concurrent use.
type ring struct {
	mu   sync.Mutex
	cond *sync.Cond

	buf   []byte
	limit int

	totalWritten int64
	truncated    bool

	closed bool
	err    error
}

// newRing constructs a ring bounded to limit bytes of retained tail.
// A non-positive limit panics: callers always default it first.
func newRing(limit int) *ring {
	if limit <= 0 {
		panic("ssh: ring limit must be positive")
	}
	r := &ring{limit: limit}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// write appends p, trimming the retained buffer down to limit bytes
// from the front (drop-oldest) when it would grow past that, and
// wakes any waiter. write is a no-op after close.
func (r *ring) write(p []byte) {
	if len(p) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.totalWritten += int64(len(p))
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.limit {
		drop := len(r.buf) - r.limit
		r.buf = r.buf[drop:]
		r.truncated = true
	}
	r.cond.Broadcast()
}

// closeWithErr marks the ring closed, recording err as the reason a
// waiter should stop (nil means clean EOF), and wakes every waiter.
// Idempotent: only the first call's err is kept.
func (r *ring) closeWithErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	if err == nil {
		err = errRingClosed
	}
	r.err = err
	r.cond.Broadcast()
}

// stats reports the total bytes ever written and whether the
// retained tail has ever been trimmed.
func (r *ring) stats() (total int64, truncated bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.totalWritten, r.truncated
}

// drain returns everything currently retained and clears the ring's
// buffer, without affecting totalWritten or truncated. It lets a
// caller that never scans a stream for a match (stderr) still get a
// per-window snapshot: a command calls drain right after it completes
// to collect only the stderr bytes that arrived during its own wait.
func (r *ring) drain() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.buf
	r.buf = nil
	return out
}

// waitFor blocks until match reports a positive consumed length
// against the currently retained buffer, ctx is done, or the ring is
// closed. On a match it removes the consumed prefix from the
// retained buffer (later writes and future matches never see it
// again) and returns a copy of it. match must not retain buf beyond
// the call.
func (r *ring) waitFor(ctx context.Context, match func(buf []byte) (consumed int, ok bool)) ([]byte, error) {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			r.mu.Lock()
			r.cond.Broadcast()
			r.mu.Unlock()
		case <-stop:
		}
	}()

	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		if n, ok := match(r.buf); ok {
			out := append([]byte(nil), r.buf[:n]...)
			r.buf = r.buf[n:]
			return out, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if r.closed {
			return nil, r.err
		}
		r.cond.Wait()
	}
}
