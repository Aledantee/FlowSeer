package rawsocket

import (
	"context"
	"sync"
	"time"

	"golang.org/x/net/bpf"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
)

// pollTimeout lets receive loops check cancellation between blocked reads.
// Linux local sockets and both platforms' mirror sockets use SO_RCVTIMEO.
// The FreeBSD local reader sets bpfdev.ReadTimeout, which configures
// BIOCSRTIMEOUT in src/common/net/bpfdev/device_freebsd.go.
const pollTimeout = 100 * time.Millisecond

// maxFrameLen sizes the socket receive buffer. The Linux local reader uses
// MSG_TRUNC to retain a longer wire length. The FreeBSD local reader ignores
// this buffer and uses bpfdev records' captured and wire lengths.
const maxFrameLen = 65536

// frameReader returns owned frame bytes that remain valid after the next read.
// A read must return within pollTimeout so cancellation and Close can proceed.
// stats reports counts since its previous call.
type frameReader interface {
	readFrame(buf []byte) (data []byte, wireLength int, capturedAt time.Time, retryable bool, err error)
	stats() (packets, drops uint64, err error)
	close() error
}

// localSource wraps a frameReader behind the Source contract.
//
// mu guards reader access and the closed flag. A poll and a Close cannot race
// each other into a use-after-close. done wakes a Receive blocked trying to
// send to an abandoned consumer.
type localSource struct {
	reader frameReader
	vm     *bpf.VM

	mu     sync.Mutex // guards reader and closed
	closed bool
	done   chan struct{}
}

func newLocalSource(reader frameReader) *localSource {
	return &localSource{reader: reader, done: make(chan struct{})}
}

// Receive closes its channel after cancellation or Close, even if the
// consumer stops reading. Terminal error delivery is best effort.
func (s *localSource) Receive(ctx context.Context) <-chan Frame {
	frames := make(chan Frame, 1)

	// frames is closed exactly once, from whichever of these two paths
	// reaches it first: recvLoop's own return (fn's literal statement,
	// after recvLoop is done sending), or the panic sink. Neither is a
	// deferred close inside recvLoop itself. A deferred close there would
	// already have run, during the panic's own unwind, by the time the sink
	// fires, and closing frames twice panics.
	spawn.Go(ctx, "rawsocket.localSource.Receive", func() {
		s.recvLoop(ctx, frames)
		close(frames)
	}, spawn.ReportTo(func(err error) {
		// A terminal Frame here is best effort, same as every other exit
		// from recvLoop. The caller does not depend on it, since an
		// unattributed close it did not itself request already reads as an
		// error (Engine.run: "source closed its frame channel
		// unexpectedly").
		sendTerminal(frames, Frame{Err: err})
		close(frames)
	}))

	return frames
}

// recvLoop is Receive's goroutine body, factored out so Receive can close
// frames itself after recvLoop returns rather than deferring the close
// inside it. See the ordering comment at the call site.
func (s *localSource) recvLoop(ctx context.Context, frames chan<- Frame) {
	buf := make([]byte, maxFrameLen)
	for {
		select {
		case <-ctx.Done():
			sendTerminal(frames, Frame{Err: ctx.Err()})
			return
		case <-s.done:
			return
		default:
		}

		data, originalLength, capturedAt, retryable, open, err := s.receiveOnce(buf)
		if !open {
			// Close always closes done before releasing the lock, so
			// reaching here means the <-s.done case above lost this
			// iteration's select race, not that anything failed: a
			// clean shutdown, no terminal error frame.
			return
		}

		if err != nil {
			if retryable {
				select {
				case <-ctx.Done():
					sendTerminal(frames, Frame{Err: ctx.Err()})
					return
				default:
					continue
				}
			}
			sendTerminal(frames, Frame{Err: err})
			return
		}
		if s.vm != nil {
			accepted, err := s.vm.Run(data)
			if err != nil {
				sendTerminal(frames, Frame{Err: errs.From(err).
					Code(ErrCodeSourceOpen).
					Msg("run local interface filter")})
				return
			}
			if accepted == 0 {
				continue
			}
		}

		f := Frame{Data: data, OriginalLength: uint32(originalLength), CapturedAt: capturedAt}
		select {
		case frames <- f:
		case <-ctx.Done():
			sendTerminal(frames, Frame{Err: ctx.Err()})
			return
		case <-s.done:
			return
		}
	}
}

// Stats reports counts since the last call. It shares a lock with an
// in-flight read, so it can block for up to one poll interval (100ms).
func (s *localSource) Stats() (received, droppedByInterface uint64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return 0, 0, errs.New().
			Code(ErrCodeSourceOpen).
			Msg("stats on closed source")
	}
	packets, drops, err := s.reader.stats()
	if err != nil {
		return 0, 0, errs.From(err).
			Code(ErrCodeSourceOpen).
			Msg("packet statistics")
	}
	return packets, drops, nil
}

// Close waits for an active poll before releasing the socket (up to one
// poll interval, 100ms), then wakes any receiver waiting for its consumer.
// Repeated calls do nothing.
func (s *localSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	err := s.reader.close()
	s.closed = true
	close(s.done)
	return err
}

// receiveOnce performs one guarded read. open is false when the source has
// been closed under the lock, which is a clean shutdown rather than an error.
//
// The lock is released by a defer because this runs on a supervised
// goroutine: a panic in readFrame is recovered, so a release written after the
// call would be skipped and s.mu would stay held for the life of the process.
// Stats and Close both take it, so the capture engine would block there
// forever, a hang in place of the crash the recovery replaced.
func (s *localSource) receiveOnce(buf []byte) (data []byte, wireLength int, capturedAt time.Time, retryable, open bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, 0, time.Time{}, false, false, nil
	}
	data, wireLength, capturedAt, retryable, err = s.reader.readFrame(buf)
	return data, wireLength, capturedAt, retryable, true, err
}

// An abandoned consumer must not strand Receive on the final error frame.
func sendTerminal(frames chan<- Frame, f Frame) {
	select {
	case frames <- f:
	default:
	}
}

var _ Source = (*localSource)(nil)
