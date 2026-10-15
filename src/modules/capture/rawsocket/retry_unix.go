//go:build linux || freebsd

package rawsocket

import (
	"errors"

	"golang.org/x/sys/unix"
)

// isRetryable reports whether err means "no data this poll, try again"
// rather than a real failure: SO_RCVTIMEO's own timeout (EAGAIN/EWOULDBLOCK)
// or a signal interrupting the blocked receive (EINTR). signal(7) states
// that a socket with SO_RCVTIMEO set always returns EINTR on a signal,
// never restarting the call regardless of SA_RESTART, and the Go runtime's
// own asynchronous goroutine preemption (SIGURG) and profiling signals make
// that a routine event during a long poll, not an edge case.
func isRetryable(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR)
}
