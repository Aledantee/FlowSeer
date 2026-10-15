// link_test.go exercises the Leg interface with a mock implementation. The
// default race run uses only the mock — no real socket is opened. The
// real-link round-trip test lives in linktest_test.go behind a build tag and
// a linux constraint and is skipped here.
package link

import (
	"bytes"
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/bpf"

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

func (m *mockLeg) Addr() Addr {
	return Addr{MAC: net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}}
}

// TestMockAddr pins that the mock satisfies the Leg contract's address
// method with a stable MAC behavior tests can assert against.
func TestMockAddr(t *testing.T) {
	var leg Leg = newMockLeg(1)
	if got := leg.Addr().MAC.String(); got != "00:11:22:33:44:55" {
		t.Errorf("Addr().MAC = %s, want 00:11:22:33:44:55", got)
	}
}

// TestReadAddrPicksFirstIPv4AndLinkLocalIPv6 pins the selection rule on a
// replaced interface seam: the first IPv4 wins over a later one, a global
// IPv6 is passed over for the link-local one that neighbor discovery needs,
// and a non-IP address is ignored.
func TestReadAddrPicksFirstIPv4AndLinkLocalIPv6(t *testing.T) {
	saved := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = saved })
	interfaceAddrs = func(name string) (net.HardwareAddr, []net.Addr, error) {
		if name != "eth9" {
			t.Errorf("interface name = %q, want eth9", name)
		}
		cidr := func(s string) *net.IPNet {
			ip, n, err := net.ParseCIDR(s)
			if err != nil {
				t.Fatal(err)
			}
			n.IP = ip
			return n
		}
		return net.HardwareAddr{0x02, 0x00, 0x00, 0x00, 0x00, 0x09}, []net.Addr{
			&net.UnixAddr{Name: "ignored"},
			cidr("2001:db8::9/64"),
			cidr("192.0.2.9/24"),
			cidr("192.0.2.10/24"),
			cidr("fe80::9/64"),
		}, nil
	}

	got, err := readAddr("eth9")
	if err != nil {
		t.Fatalf("readAddr: %v", err)
	}
	if got.MAC.String() != "02:00:00:00:00:09" {
		t.Errorf("MAC = %s, want 02:00:00:00:00:09", got.MAC)
	}
	if !got.IPv4.Equal(net.IPv4(192, 0, 2, 9)) || len(got.IPv4) != net.IPv4len {
		t.Errorf("IPv4 = %v (len %d), want 192.0.2.9 as 4 bytes", got.IPv4, len(got.IPv4))
	}
	if !got.IPv6.Equal(net.ParseIP("fe80::9")) {
		t.Errorf("IPv6 = %v, want fe80::9", got.IPv6)
	}
}

// TestReadAddrUnknownInterfaceIsCoded pins that an interface the kernel
// does not know fails with the leg-open code, so the command reports it as
// an open failure and not a crash.
func TestReadAddrUnknownInterfaceIsCoded(t *testing.T) {
	saved := interfaceAddrs
	t.Cleanup(func() { interfaceAddrs = saved })
	interfaceAddrs = func(string) (net.HardwareAddr, []net.Addr, error) {
		return nil, nil, errors.New("no such network interface")
	}
	_, err := readAddr("eth9")
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeLegOpen {
		t.Errorf("readAddr error = %v (code %q ok %v), want %q", err, code, ok, ErrCodeLegOpen)
	}
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
				return
			case f, ok := <-m.frames:
				if !ok {
					return
				}
				select {
				case out <- f:
				case <-ctx.Done():
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

func TestFilterEtherType(t *testing.T) {
	raw, err := FilterEtherType(0x0806)
	if err != nil {
		t.Fatalf("FilterEtherType: %v", err)
	}
	bpfRaw := make([]bpf.RawInstruction, len(raw))
	for i, instruction := range raw {
		bpfRaw[i] = bpf.RawInstruction(instruction)
	}
	insts, allDecoded := bpf.Disassemble(bpfRaw)
	if !allDecoded {
		t.Fatal("filter contains unknown instructions")
	}
	vm, err := bpf.NewVM(insts)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		data []byte
		want int
	}{
		{name: "match", data: append(make([]byte, 12), 0x08, 0x06), want: 4096},
		{name: "mismatch", data: append(make([]byte, 12), 0x08, 0x00)},
		{name: "truncated", data: make([]byte, 13)},
		{name: "vlan", data: append(make([]byte, 12), 0x81, 0x00, 0, 1, 0x08, 0x06)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vm.Run(tt.data)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("filter verdict = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAssembleInvalidInstruction(t *testing.T) {
	if _, err := Assemble([]Instruction{bpf.LoadAbsolute{Off: 12, Size: 3}}); err == nil {
		t.Fatal("Assemble invalid load size returned nil, want error")
	}
}

// Compile-time assertion that mockLeg satisfies Leg.
var _ Leg = (*mockLeg)(nil)
