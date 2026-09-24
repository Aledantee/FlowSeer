package netsimload

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/stream"
	"go.aledante.io/FlowSeer/src/edge/netsimload/packetio"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

type sourceItem struct {
	at    time.Duration
	frame ethernet.Frame
}

type scriptedSource struct {
	items []sourceItem
	index int
}

func (s *scriptedSource) Next() (time.Duration, ethernet.Frame, bool) {
	if s.index >= len(s.items) {
		return 0, ethernet.Frame{}, false
	}
	item := s.items[s.index]
	s.index++
	return item.at, item.frame, true
}

func (s *scriptedSource) Clone() stream.Source {
	clone := *s
	clone.items = append([]sourceItem(nil), s.items...)
	return &clone
}

type fakeClock struct {
	now   time.Time
	waits []time.Time
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Wait(ctx context.Context, deadline time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	c.waits = append(c.waits, deadline)
	c.now = deadline
	return nil
}

type cancellationClock struct {
	now time.Time
}

func (c *cancellationClock) Now() time.Time {
	return c.now
}

func (c *cancellationClock) Wait(ctx context.Context, _ time.Time) error {
	<-ctx.Done()
	return ctx.Err()
}

type fakeSender struct {
	mu       sync.Mutex
	writes   [][]byte
	writeErr error
	closeErr error
	closed   bool
}

func (s *fakeSender) Send(_ context.Context, frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	s.writes = append(s.writes, append([]byte(nil), frame...))
	return nil
}

func (s *fakeSender) Close() error {
	s.closed = true
	return s.closeErr
}

type fakeReceiver struct {
	frames         chan rawsocket.Frame
	interfaceDrops uint64
	statsErr       error
	closeErr       error
	closeOnce      sync.Once
	closed         chan struct{}
}

func newFakeReceiver(frames ...rawsocket.Frame) *fakeReceiver {
	channel := make(chan rawsocket.Frame, len(frames)+1)
	for _, frame := range frames {
		channel <- frame
	}
	return &fakeReceiver{frames: channel, closed: make(chan struct{})}
}

func (r *fakeReceiver) Receive(ctx context.Context) <-chan rawsocket.Frame {
	go func() {
		<-ctx.Done()
		r.close()
	}()
	return r.frames
}

func (r *fakeReceiver) Stats() (uint64, uint64, error) {
	return 0, r.interfaceDrops, r.statsErr
}

func (r *fakeReceiver) Close() error {
	r.close()
	return r.closeErr
}

func (r *fakeReceiver) close() {
	r.closeOnce.Do(func() {
		close(r.closed)
		close(r.frames)
	})
}

func testDependencies(clock Clock, sender packetioSender, receiver *fakeReceiver, resolver func(string) (InterfaceInfo, error)) Dependencies {
	return Dependencies{
		Clock:            clock,
		ResolveInterface: resolver,
		OpenSender: func(string) (packetio.Sender, error) {
			return sender, nil
		},
		OpenReceiver: func(string) (rawsocket.Source, error) {
			return receiver, nil
		},
	}
}

type packetioSender interface {
	Send(context.Context, []byte) error
	Close() error
}

func TestRunPacesOneEpochAndBreaksEqualOffsetsByFlowID(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	sender := &fakeSender{}
	receiver := newFakeReceiver()
	frame := ethernet.Frame{Payload: make([]byte, SignatureSize)}
	config := Config{
		TXInterface: "tx0",
		RXInterface: "rx0",
		Flows: []FlowSource{
			{ID: 2, Source: &scriptedSource{items: []sourceItem{{frame: frame}}}},
			{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: frame}, {at: time.Second, frame: frame}}}},
		},
	}
	resolver := func(name string) (InterfaceInfo, error) {
		return InterfaceInfo{Name: name, Up: true}, nil
	}
	deps := testDependencies(clock, sender, receiver, resolver)

	observation, err := RunWith(context.Background(), config, deps)
	if err != nil {
		t.Fatalf("RunWith: %v", err)
	}
	if len(sender.writes) != 3 {
		t.Fatalf("writes = %d, want 3", len(sender.writes))
	}
	var flowIDs []fabric.FlowID
	for _, wire := range sender.writes {
		signature, decodeErr := DecodeWireSignature(wire)
		if decodeErr != nil {
			t.Fatalf("DecodeWireSignature: %v", decodeErr)
		}
		flowIDs = append(flowIDs, signature.FlowID)
	}
	want := []fabric.FlowID{1, 2, 1}
	for i := range want {
		if flowIDs[i] != want[i] {
			t.Fatalf("write %d flow = %d, want %d", i, flowIDs[i], want[i])
		}
	}
	if len(clock.waits) != 4 || clock.waits[0] != time.Unix(1700000000, 0) || clock.waits[2] != time.Unix(1700000001, 0) {
		t.Fatalf("waits = %v, want send deadlines followed by drain", clock.waits)
	}
	if observation.Flows[1].Sent != 2 || observation.Flows[2].Sent != 1 {
		t.Fatalf("observation = %+v", observation)
	}
}

func TestRunRejectsInterfacesAndPreflightBeforeOpening(t *testing.T) {
	frame := ethernet.Frame{Payload: make([]byte, SignatureSize)}
	cases := []struct {
		name     string
		config   Config
		resolver func(string) (InterfaceInfo, error)
		want     string
	}{
		{
			name:   "missing transmit",
			config: Config{RXInterface: "rx0", Flows: []FlowSource{{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: frame}}}}}},
			want:   "required",
		},
		{
			name:     "same interface",
			config:   Config{TXInterface: "same", RXInterface: "same", Flows: []FlowSource{{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: frame}}}}}},
			resolver: func(string) (InterfaceInfo, error) { return InterfaceInfo{Up: true}, nil },
			want:     "must differ",
		},
		{
			name:     "down interface",
			config:   Config{TXInterface: "tx0", RXInterface: "rx0", Flows: []FlowSource{{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: frame}}}}}},
			resolver: func(name string) (InterfaceInfo, error) { return InterfaceInfo{Name: name}, nil },
			want:     "down",
		},
		{
			name:     "unknown interface",
			config:   Config{TXInterface: "tx0", RXInterface: "rx0", Flows: []FlowSource{{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: frame}}}}}},
			resolver: func(name string) (InterfaceInfo, error) { return InterfaceInfo{}, errors.New("unknown " + name) },
			want:     "resolve interface",
		},
		{
			name:     "short frame preflight",
			config:   Config{TXInterface: "tx0", RXInterface: "rx0", Flows: []FlowSource{{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: ethernet.Frame{Payload: make([]byte, SignatureSize-1)}}}}}}},
			resolver: func(name string) (InterfaceInfo, error) { return InterfaceInfo{Name: name, Up: true}, nil },
			want:     "need at least",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			receiver := newFakeReceiver()
			deps := Dependencies{
				Clock:            &fakeClock{now: time.Unix(1700000000, 0)},
				ResolveInterface: tc.resolver,
				OpenSender: func(string) (packetio.Sender, error) {
					opened++
					return &fakeSender{}, nil
				},
				OpenReceiver: func(string) (rawsocket.Source, error) {
					opened++
					return receiver, nil
				},
			}
			_, err := RunWith(context.Background(), tc.config, deps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("RunWith error = %v, want substring %q", err, tc.want)
			}
			if opened != 0 {
				t.Fatalf("openers called %d times", opened)
			}
		})
	}
}

func TestRunStopsOnSendErrorAndReportsOnlySuccessfulSends(t *testing.T) {
	sendErr := errors.New("send failed")
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	sender := &fakeSender{writeErr: sendErr}
	receiver := newFakeReceiver()
	frame := ethernet.Frame{Payload: make([]byte, SignatureSize)}
	config := Config{TXInterface: "tx0", RXInterface: "rx0", Flows: []FlowSource{{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: frame}, {at: time.Second, frame: frame}}}}}}
	resolver := func(name string) (InterfaceInfo, error) { return InterfaceInfo{Name: name, Up: true}, nil }

	observation, err := RunWith(context.Background(), config, testDependencies(clock, sender, receiver, resolver))
	if !errors.Is(err, sendErr) {
		t.Fatalf("RunWith error = %v, want %v", err, sendErr)
	}
	if got := observation.Flows[1].Sent; got != 0 {
		t.Fatalf("successful sends = %d, want 0", got)
	}
}

func TestRunStopsOnReceiveErrorBeforeSending(t *testing.T) {
	receiveErr := errors.New("capture failed")
	clock := &cancellationClock{now: time.Unix(1700000000, 0)}
	sender := &fakeSender{}
	receiver := newFakeReceiver(rawsocket.Frame{Err: receiveErr})
	frame := ethernet.Frame{Payload: make([]byte, SignatureSize)}
	config := Config{TXInterface: "tx0", RXInterface: "rx0", Flows: []FlowSource{{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: frame}}}}}}
	resolver := func(name string) (InterfaceInfo, error) { return InterfaceInfo{Name: name, Up: true}, nil }

	observation, err := RunWith(context.Background(), config, testDependencies(clock, sender, receiver, resolver))
	if !errors.Is(err, receiveErr) {
		t.Fatalf("RunWith error = %v, want %v", err, receiveErr)
	}
	if len(sender.writes) != 0 || observation.Flows[1].Sent != 0 {
		t.Fatalf("sender was used after receive failure: writes=%d observation=%+v", len(sender.writes), observation)
	}
}
