//go:build linux || freebsd

package rawsocket

import (
	"errors"

	"golang.org/x/sys/unix"
)

// isRetryable lets Linux local sockets and Linux/FreeBSD mirror sockets
// retry a receive timeout (EAGAIN/EWOULDBLOCK) or interruption (EINTR).
// The FreeBSD local reader uses bpfdev.ReadBatch, which handles its own
// timeout and interruption in src/common/net/bpfdev/device_freebsd.go.
func isRetryable(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EINTR)
}
