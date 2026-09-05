package syslog

import (
	"context"
	"sync"
)

type admission struct {
	mu                              sync.Mutex
	bytes, frames, limit, maxFrames int
	changed                         chan struct{}
	closed                          bool
}

func newAdmission(l Limits, initial int) (*admission, error) {
	if initial > l.MaxBytes-l.MaxPayload {
		return nil, ErrLimit
	}
	return &admission{bytes: initial, limit: l.MaxBytes, maxFrames: l.MaxFrames, changed: make(chan struct{})}, nil
}

func (a *admission) acquire(ctx context.Context, n int, frame, wait bool) error {
	for {
		a.mu.Lock()
		if a.closed {
			a.mu.Unlock()
			return ErrClosed
		}
		if err := ctx.Err(); err != nil {
			a.mu.Unlock()
			return err
		}
		if n <= a.limit-a.bytes && (!frame || a.frames < a.maxFrames) {
			a.bytes += n
			if frame {
				a.frames++
			}
			a.mu.Unlock()
			return nil
		}
		changed := a.changed
		a.mu.Unlock()
		if !wait {
			return ErrLimit
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (a *admission) release(n int, frame bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bytes -= n
	if frame {
		a.frames--
	}
	close(a.changed)
	a.changed = make(chan struct{})
}

func (a *admission) stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.closed = true
		close(a.changed)
		a.changed = make(chan struct{})
	}
}

func (a *admission) snapshot() (int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bytes, a.frames
}
