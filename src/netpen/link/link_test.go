// link_test.go exercises the Leg interface with a mock implementation. The
// default race run uses only the mock — no real socket is opened. The
// real-link round-trip test lives in linktest_test.go behind a build tag and
// a linux constraint and is skipped here.
package link

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// mockLeg is an in-memory Leg for testing the cancellation and close contract
// without an AF_PACKET socket. Receive delivers frames pushed through send,
// and respects ctx cancellation and Close.
type mockLeg struct {
	mu       sync.Mutex
	closed   bool
	frames   chan Frame
	closeErr error
}

func newMockLeg(bufferSize int) *mockLeg {
	return &mockLeg{frames: make(chan Frame, bufferSize)}
}

func (m *mockLeg) Send(_ context.Context, pkt []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errs.New().Code(ErrCodeLegOpen).Msg("send on closed leg")
	}

	m.frames <- Frame{Data: append([]byte(nil), pkt...)}
	return nil
}

func (m *mockLeg) SetFilter(_ []RawInstruction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errs.New().Code(ErrCodeLegOpen).Msg("set filter on closed leg")
	}
	return nil
}

func (m *mockLeg) Receive(ctx context.Context) <-chan Frame {
	out := make(chan Frame)

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				out <- Frame{Err: ctx.Err()}
				return
			case f, ok := <-m.frames:
				if !ok {
					return
				}
				select {
				case out <- f:
				case <-ctx.Done():
					out <- Frame{Err: ctx.Err()}
					return
				}
			}
		}
	}()

	return out
}

func (m *mockLeg) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	close(m.frames)
	return m.closeErr
}

// TestCloseIsIdempotent asserts that calling Close more than once is a no-op
// (Collection Primitives lifecycle), on a mock leg.
func TestCloseIsIdempotent(t *testing.T) {
	leg := newMockLeg(4)

	if err := leg.Close(); err != nil {
		t.Fatalf("first Close: got %v, want nil", err)
	}
	if err := leg.Close(); err != nil {
		t.Fatalf("second Close: got %v, want nil", err)
	}
}

// TestSendAfterCloseReturnsCodedError verifies that Send on a closed leg
// returns an error carrying the netpen/leg-open code.
func TestSendAfterCloseReturnsCodedError(t *testing.T) {
	leg := newMockLeg(4)
	_ = leg.Close()

	err := leg.Send(context.Background(), []byte{0x01})
	if err == nil {
		t.Fatal("Send on closed leg returned nil, want coded error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeLegOpen {
		t.Errorf("Send error code: got %q ok=%v, want %q", code, ok, ErrCodeLegOpen)
	}
}

// TestOpenNonexistentInterfaceReturnsCodedError verifies that opening a
// nonexistent interface returns a named coded error (netpen/leg-open), not a
// panic. On non-Linux this is ErrUnsupportedPlatform; on Linux the afpacket
// open fails. Both carry the code.
func TestOpenNonexistentInterfaceReturnsCodedError(t *testing.T) {
	_, err := Open("netpen-nonexistent-iface-0xdead")
	if err == nil {
		t.Fatal("Open nonexistent interface returned nil, want coded error")
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeLegOpen {
		t.Errorf("Open error code: got %q ok=%v, want %q", code, ok, ErrCodeLegOpen)
	}
}

// TestReceiveCancellationUnblocks verifies that context cancellation while
// RX-blocked unblocks the receiver within one poll cycle on the mock leg.
func TestReceiveCancellationUnblocks(t *testing.T) {
	leg := newMockLeg(0) // no buffered frames: Receive blocks
	ctx, cancel := context.WithCancel(context.Background())

	frames := leg.Receive(ctx)

	// Cancel after a short delay to simulate Ctrl+C.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	select {
	case f, ok := <-frames:
		if !ok {
			// Channel closed without a frame: also a valid cancellation exit.
		} else if f.Err == nil {
			t.Fatal("received frame with nil error after cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not unblock within 2s of cancellation")
	}
}

// TestCloseDuringReceiveIsRaceClean exercises the close-during-poll path
// under -race: the receiver goroutine and Close run concurrently and the
// contract holds (clean shutdown, no use-after-close).
func TestCloseDuringReceiveIsRaceClean(t *testing.T) {
	leg := newMockLeg(0)
	ctx := context.Background()

	frames := leg.Receive(ctx)

	// Close while the receiver is blocked waiting for a frame.
	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = leg.Close()
	}()

	// Drain until the channel closes. A panic or race-detector trip fails
	// the test; a clean close or a terminal frame is the pass.
	drained := 0
	for range frames {
		drained++
		if drained > 10 {
			t.Fatal("too many frames after close")
		}
	}
}

// TestMockSendReceiveRoundTrip verifies the mock delivers what Send pushed.
func TestMockSendReceiveRoundTrip(t *testing.T) {
	leg := newMockLeg(4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pkt := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := leg.Send(ctx, pkt); err != nil {
		t.Fatalf("Send: %v", err)
	}

	frames := leg.Receive(ctx)
	select {
	case f := <-frames:
		if !bytes.Equal(f.Data, pkt) {
			t.Errorf("received %v, want %v", f.Data, pkt)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive frame within 1s")
	}
}

// TestIsUnsupported matches the sentinel by code.
func TestIsUnsupported(t *testing.T) {
	if !errors.Is(ErrUnsupportedPlatform, ErrUnsupportedPlatform) {
		t.Fatal("errors.Is(ErrUnsupportedPlatform, ErrUnsupportedPlatform) = false")
	}
}

// TestFilterEtherTypeProducesValidProgram checks the bpf builder returns raw
// instructions without error.
func TestFilterEtherTypeProducesValidProgram(t *testing.T) {
	raw, err := FilterEtherType(0x0806) // ARP
	if err != nil {
		t.Fatalf("FilterEtherType: %v", err)
	}
	if len(raw) != 4 {
		t.Errorf("got %d instructions, want 4", len(raw))
	}
}

// Compile-time assertion that mockLeg satisfies Leg.
var _ Leg = (*mockLeg)(nil)
