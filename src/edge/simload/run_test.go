package simload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/flow"
	"go.aledante.io/FlowSeer/src/common/sim/stream"
	"go.aledante.io/FlowSeer/src/edge/simload/packetio"
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
	now            time.Time
	waits          []time.Time
	onWait         func(context.Context, time.Time) error
	ignoreCanceled bool
	lateBy         time.Duration
}

func (c *fakeClock) Now() time.Time {
	return c.now
}

func (c *fakeClock) Wait(ctx context.Context, deadline time.Time) error {
	if c.onWait != nil {
		if err := c.onWait(ctx, deadline); err != nil {
			return err
		}
	}
	if !c.ignoreCanceled {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	c.waits = append(c.waits, deadline)
	c.now = deadline.Add(c.lateBy)
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
	onSend   func(context.Context, []byte) error
	closeErr error
	closed   bool
}

func (s *fakeSender) Send(ctx context.Context, frame []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.onSend != nil {
		if err := s.onSend(ctx, frame); err != nil {
			return err
		}
	}
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
	beforeClose    func()
	onStats        func()
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
		if r.beforeClose != nil {
			r.beforeClose()
		}
		r.close()
	}()
	return r.frames
}

func (r *fakeReceiver) Stats() (uint64, uint64, error) {
	if r.onStats != nil {
		r.onStats()
	}
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
	clock := &fakeClock{now: time.Unix(1700000000, 0), lateBy: 100 * time.Microsecond}
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
	var flowIDs []flow.ID
	for _, wire := range sender.writes {
		signature, decodeErr := DecodeWireSignature(wire)
		if decodeErr != nil {
			t.Fatalf("DecodeWireSignature: %v", decodeErr)
		}
		flowIDs = append(flowIDs, signature.FlowID)
	}
	want := []flow.ID{1, 2, 1}
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
	for i, wire := range sender.writes {
		signature, decodeErr := DecodeWireSignature(wire)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if want := clock.waits[i].Add(clock.lateBy); !signature.SubmittedAt.Equal(want) {
			t.Fatalf("write %d submitted at %s, want send clock %s", i, signature.SubmittedAt, want)
		}
	}
}

func TestRunReturnsStatisticsErrorAndInterfaceDrops(t *testing.T) {
	statsErr := errors.New("packet statistics failed")
	config := oneFrameConfig()
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"statistics error", statsErr},
		{"drops", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := newFakeReceiver()
			receiver.statsErr = tc.err
			receiver.interfaceDrops = 7
			observation, err := RunWith(context.Background(), config, testDependencies(&fakeClock{now: time.Unix(1700000000, 0)}, &fakeSender{}, receiver, upInterface))
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("RunWith error = %v, want %v", err, tc.err)
			}
			if tc.err == nil && err != nil {
				t.Fatalf("RunWith: %v", err)
			}
			if observation.InterfaceDrops != 7 {
				t.Fatalf("interface drops = %d, want 7", observation.InterfaceDrops)
			}
		})
	}
}

func TestRunStopsPromptlyWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	clock.onWait = func(_ context.Context, _ time.Time) error {
		if len(clock.waits) == 1 {
			cancel()
		}
		return nil
	}
	sender := &fakeSender{}
	config := oneFrameConfig()
	config.Flows[0].Source = &scriptedSource{items: []sourceItem{{frame: testFrame()}, {at: time.Second, frame: testFrame()}}}
	observation, err := RunWith(ctx, config, testDependencies(clock, sender, newFakeReceiver(), upInterface))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunWith error = %v, want cancellation", err)
	}
	if len(sender.writes) != 1 || observation.Flows[1].Sent != 1 {
		t.Fatalf("writes = %d, observation = %+v; want one send", len(sender.writes), observation)
	}
}

func TestRunDrainsFromLastSubmission(t *testing.T) {
	start := time.Unix(1700000000, 0)
	clock := &fakeClock{now: start, lateBy: 100 * time.Millisecond}
	config := oneFrameConfig()
	config.Drain = 3 * time.Second
	config.Flows[0].Source = &scriptedSource{items: []sourceItem{{at: 2 * time.Second, frame: testFrame()}}}
	_, err := RunWith(context.Background(), config, testDependencies(clock, &fakeSender{}, newFakeReceiver(), upInterface))
	if err != nil {
		t.Fatal(err)
	}
	if len(clock.waits) != 2 || !clock.waits[1].Equal(start.Add(5*time.Second+100*time.Millisecond)) {
		t.Fatalf("waits = %v, want send at +2s and drain until +5.1s", clock.waits)
	}
}

func TestRunReportsCloseErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		receiverErr error
		senderErr   error
	}{
		{"receiver", errors.New("receiver close failed"), nil},
		{"sender", nil, errors.New("sender close failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := newFakeReceiver()
			receiver.closeErr = tc.receiverErr
			sender := &fakeSender{closeErr: tc.senderErr}
			_, err := RunWith(context.Background(), oneFrameConfig(), testDependencies(&fakeClock{now: time.Unix(1700000000, 0)}, sender, receiver, upInterface))
			want := tc.receiverErr
			if want == nil {
				want = tc.senderErr
			}
			if !errors.Is(err, want) {
				t.Fatalf("RunWith error = %v, want %v", err, want)
			}
			if !sender.closed {
				t.Fatal("sender was not closed")
			}
			select {
			case <-receiver.closed:
			default:
				t.Fatal("receiver did not finish")
			}
		})
	}
}

func TestRunReturnsReceiverErrorWhenSendSeesCancellation(t *testing.T) {
	receiveErr := errors.New("capture failed during send")
	receiver := newFakeReceiver()
	sender := &fakeSender{onSend: func(ctx context.Context, _ []byte) error {
		receiver.frames <- rawsocket.Frame{Err: receiveErr}
		<-ctx.Done()
		return ctx.Err()
	}}
	_, err := RunWith(context.Background(), oneFrameConfig(), testDependencies(&fakeClock{now: time.Unix(1700000000, 0)}, sender, receiver, upInterface))
	if !errors.Is(err, receiveErr) {
		t.Fatalf("RunWith error = %v, want receiver error", err)
	}
}

func TestRunSeesReceiverErrorAfterNonblockingWait(t *testing.T) {
	receiveErr := errors.New("capture failed during wait")
	receiver := newFakeReceiver()
	clock := &fakeClock{now: time.Unix(1700000000, 0), ignoreCanceled: true}
	triggered := false
	clock.onWait = func(ctx context.Context, _ time.Time) error {
		if !triggered {
			triggered = true
			receiver.frames <- rawsocket.Frame{Err: receiveErr}
			<-ctx.Done()
		}
		return nil
	}
	sender := &fakeSender{}
	_, err := RunWith(context.Background(), oneFrameConfig(), testDependencies(clock, sender, receiver, upInterface))
	if !errors.Is(err, receiveErr) || len(sender.writes) != 0 {
		t.Fatalf("RunWith error = %v, writes = %d; want capture error before send", err, len(sender.writes))
	}
}

func TestAccumulatorLockReleasedOnPanic(t *testing.T) {
	var mu sync.Mutex
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		withAccumulator(&mu, func() { panic("record failed") })
	}()
	if !panicked {
		t.Fatal("record function did not panic")
	}
	if !mu.TryLock() {
		t.Fatal("accumulator lock remained held after record panicked")
	}
	defer mu.Unlock()
}

func TestRunUsesStreamBurstAndGapOffsets(t *testing.T) {
	start := time.Unix(1700000000, 0)
	clock := &fakeClock{now: start}
	spec := stream.Spec{Frame: testFrame(), Rate: stream.Rate{FramesPerSecond: 1000}, Count: 4, Burst: 2, Gap: time.Second}
	source, err := spec.Source()
	if err != nil {
		t.Fatal(err)
	}
	config := oneFrameConfig()
	config.Flows[0].Source = source
	_, err = RunWith(context.Background(), config, testDependencies(clock, &fakeSender{}, newFakeReceiver(), upInterface))
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{0, time.Millisecond, time.Second + 2*time.Millisecond, time.Second + 3*time.Millisecond}
	if len(clock.waits) != len(want)+1 {
		t.Fatalf("waits = %v", clock.waits)
	}
	for i, offset := range want {
		if !clock.waits[i].Equal(start.Add(offset)) {
			t.Fatalf("wait %d = %s, want %s", i, clock.waits[i], start.Add(offset))
		}
	}
}

func oneFrameConfig() Config {
	return Config{TXInterface: "tx0", RXInterface: "rx0", Flows: []FlowSource{{ID: 1, Source: &scriptedSource{items: []sourceItem{{frame: testFrame()}}}}}}
}

func testFrame() ethernet.Frame {
	return ethernet.Frame{Payload: make([]byte, SignatureSize)}
}

func upInterface(name string) (InterfaceInfo, error) {
	return InterfaceInfo{Name: name, Up: true}, nil
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

func streamConfig(t *testing.T, ids ...flow.ID) Config {
	t.Helper()
	config := Config{TXInterface: "tx0", RXInterface: "rx0"}
	for _, id := range ids {
		source, err := (stream.Spec{Frame: ethernet.Frame{EtherType: 0x88b5, Payload: make([]byte, SignatureSize)}, Rate: stream.Rate{FramesPerSecond: 1000}, Count: 1}).Source()
		if err != nil {
			t.Fatal(err)
		}
		config.Flows = append(config.Flows, FlowSource{ID: id, Source: source})
	}
	return config
}

func awaitSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("signal did not arrive within one second")
	}
}

func TestExecuteDrainsAcceptedFramesBeforeFreeze(t *testing.T) {
	for _, mode := range []string{"normal", "parent cancellation", "receiver failure", "cancellation after data"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			receiver := newFakeReceiver()
			receiver.frames = make(chan rawsocket.Frame, 1)
			if mode == "cancellation after data" || mode == "receiver failure" {
				receiver.frames = make(chan rawsocket.Frame, 3)
			}
			entered, release, sent := make(chan struct{}), make(chan struct{}), make(chan struct{})
			receiver.beforeClose = func() { <-sent }
			var releaseOnce sync.Once
			t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
			var hookOnce sync.Once
			hook := func() { hookOnce.Do(func() { close(entered); <-release }) }
			failureBlocked := make(chan struct{})
			if mode == "receiver failure" {
				calls := 0
				hook = func() {
					calls++
					if calls == 1 {
						close(entered)
					}
					if calls == 2 {
						close(failureBlocked)
						<-release
					}
				}
			}
			start := time.Unix(1700000000, 0)
			receiveErr := errors.New("first capture failure")
			clock := &fakeClock{now: start}
			clock.onWait = func(_ context.Context, _ time.Time) error {
				if len(clock.waits) == 1 {
					awaitSignal(t, sent)
					if mode == "parent cancellation" {
						cancel()
						return context.Canceled
					}
				}
				return nil
			}
			sender := &fakeSender{onSend: func(_ context.Context, wire []byte) error {
				awaitSignal(t, entered)
				signed, err := Sign(ethernet.Frame{EtherType: 0x88b5, Payload: make([]byte, SignatureSize)}, 7, 0, start)
				if err != nil {
					return err
				}
				expected, err := signed.Encode()
				if err != nil {
					return err
				}
				if !bytes.Equal(wire, expected) {
					return errors.New("unexpected signed send")
				}
				if mode == "receiver failure" {
					receiver.frames <- rawsocket.Frame{Err: receiveErr}
					awaitSignal(t, failureBlocked)
				}
				receiver.frames <- rawsocket.Frame{Data: wire, CapturedAt: start.Add(time.Microsecond)}
				if mode == "cancellation after data" {
					receiver.frames <- rawsocket.Frame{Err: context.Canceled}
				}
				if mode == "receiver failure" {
					receiver.frames <- rawsocket.Frame{Err: errors.New("later capture failure")}
				}
				close(sent)
				return nil
			}}
			if mode == "receiver failure" {
				clock.onWait = func(runCtx context.Context, _ time.Time) error {
					if len(clock.waits) == 1 {
						<-runCtx.Done()
						return runCtx.Err()
					}
					return nil
				}
			}
			type result struct {
				observation Observation
				err         error
			}
			done := make(chan result, 1)
			go func() {
				observation, err := executeWithReceiveHook(ctx, streamConfig(t, 7), clock, sender, receiver, hook)
				done <- result{observation, err}
			}()
			awaitSignal(t, receiver.closed)
			releaseOnce.Do(func() { close(release) })
			select {
			case got := <-done:
				wantErr := error(nil)
				if mode == "parent cancellation" {
					wantErr = context.Canceled
				}
				if mode == "receiver failure" {
					wantErr = receiveErr
				}
				if !errors.Is(got.err, wantErr) {
					t.Fatalf("error = %v, want %v", got.err, wantErr)
				}
				row := got.observation.Flows[7]
				if row.Sent != 1 || row.UniqueReceived != 1 || row.Missing != 0 || row.LateAfterClose != 0 || row.Latency.Count != 1 || row.Latency.Sum != time.Microsecond {
					t.Fatalf("observation = %+v, want sent=1 unique=1 missing=0 late=0 latency=1us", row)
				}
			case <-time.After(time.Second):
				t.Fatal("execute did not finish within one second")
			}
		})
	}
}

func TestExecuteStatisticsFailureIgnoresCancellationFrame(t *testing.T) {
	statsErr := errors.New("statistics failed")
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			receiver := newFakeReceiver()
			receiver.statsErr = statsErr
			if terminal {
				receiver.beforeClose = func() { receiver.frames <- rawsocket.Frame{Err: context.Canceled} }
			}
			_, err := RunWith(context.Background(), streamConfig(t, 7), testDependencies(&fakeClock{now: time.Unix(1700000000, 0)}, &fakeSender{}, receiver, upInterface))
			if !errors.Is(err, statsErr) || !strings.Contains(err.Error(), "read capture statistics") {
				t.Fatalf("error = %v, want wrapped statistics failure", err)
			}
		})
	}
}

func TestExecuteErrorPrecedence(t *testing.T) {
	sendErr, statsErr := errors.New("send failed"), errors.New("statistics failed")
	receiveClose, sendClose := errors.New("receive close"), errors.New("send close")
	for _, tc := range []struct {
		name                                          string
		send, stats, receiverClose, senderClose, want error
	}{
		{"run", sendErr, statsErr, receiveClose, sendClose, sendErr},
		{"statistics", nil, statsErr, receiveClose, sendClose, statsErr},
		{"both closes", nil, nil, receiveClose, sendClose, receiveClose},
		{"sender close", nil, nil, nil, sendClose, sendClose},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := newFakeReceiver()
			receiver.statsErr, receiver.closeErr = tc.stats, tc.receiverClose
			sender := &fakeSender{writeErr: tc.send, closeErr: tc.senderClose}
			_, err := RunWith(context.Background(), streamConfig(t, 7), testDependencies(&fakeClock{now: time.Unix(1700000000, 0)}, sender, receiver, upInterface))
			if !errors.Is(err, tc.want) || !sender.closed {
				t.Fatalf("error = %v, closed = %t, want %v and closed", err, sender.closed, tc.want)
			}
		})
	}
}

func TestRunSenderFedReceiverReportsInterleavedFlows(t *testing.T) {
	receiver := newFakeReceiver()
	receiver.frames = make(chan rawsocket.Frame, 8)
	var order []flow.ID
	sender := &fakeSender{onSend: func(_ context.Context, wire []byte) error {
		signature, err := DecodeWireSignature(wire)
		if err != nil {
			return err
		}
		order = append(order, signature.FlowID)
		receiver.frames <- rawsocket.Frame{Data: append([]byte(nil), wire...), CapturedAt: signature.SubmittedAt.Add(time.Microsecond)}
		return nil
	}}
	config := streamConfig(t, 9, 7)
	for i := range config.Flows {
		source, err := (stream.Spec{Frame: ethernet.Frame{EtherType: 0x88b5, Payload: make([]byte, SignatureSize)}, Rate: stream.Rate{FramesPerSecond: 1000}, Count: 2}).Source()
		if err != nil {
			t.Fatal(err)
		}
		config.Flows[i].Source = source
	}
	observation, err := RunWith(context.Background(), config, testDependencies(&fakeClock{now: time.Unix(1700000000, 0)}, sender, receiver, upInterface))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(order, []flow.ID{7, 9, 7, 9}) {
		t.Fatalf("order = %v, want [7 9 7 9]", order)
	}
	report, err := NewReport(map[flow.ID]flow.Stats{7: {Offered: 2, Delivered: map[string]uint64{"host": 2}}, 9: {Offered: 2, Delivered: map[string]uint64{"host": 2}}}, analysis.Metadata{}, observation, "host")
	if err != nil {
		t.Fatal(err)
	}
	if report.Contract != "simload/report/v1" || len(report.Normalized) != 2 {
		t.Fatalf("report = %+v", report)
	}
	for _, id := range []flow.ID{7, 9} {
		row := observation.Flows[id]
		if row.Sent != 2 || row.UniqueReceived != 2 || row.Missing != 0 || row.LateAfterClose != 0 || row.Latency.Count != 2 {
			t.Fatalf("flow %d = %+v", id, row)
		}
	}
	for _, row := range report.Normalized {
		if row.LabOffered != 2 || row.LabDelivered != 2 || row.LabUnreceived != 0 {
			t.Fatalf("normalized row = %+v", row)
		}
	}
}

func TestExecuteRejectsPrematureChannelClosureWithoutTerminalFrame(t *testing.T) {
	receiver := newFakeReceiver()
	receiver.close()
	statisticsRead := make(chan struct{})
	receiver.onStats = func() { close(statisticsRead) }
	clock := &fakeClock{now: time.Unix(1700000000, 0)}
	clock.onWait = func(context.Context, time.Time) error {
		awaitSignal(t, statisticsRead)
		return nil
	}
	sender := &fakeSender{}
	observation, err := RunWith(context.Background(), streamConfig(t, 7), testDependencies(clock, sender, receiver, upInterface))
	if err == nil || !strings.Contains(err.Error(), "capture receiver closed before the run completed") || observation.Flows[7].Sent != 0 || !sender.closed {
		t.Fatalf("error = %v, observation = %+v, closed = %t, want closure error, zero sends and closed sender", err, observation, sender.closed)
	}
}
