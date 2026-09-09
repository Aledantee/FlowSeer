package capture

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	apicapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/common/pump"
	"go.aledante.io/FlowSeer/src/modules/capture/filter"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

const (
	// defaultSnapLength is CaptureBudget.snap_length's documented default:
	// two VLAN tags, an IPv6 header, and a TCP header with options fit
	// within it, and the payload does not.
	defaultSnapLength = 128

	// batchMaxRecords bounds a Batch well under CapturePacketChunk's
	// 4096-item cap, so a host wrapping one into that message never has to
	// split it.
	batchMaxRecords = 512

	// batchFlushInterval bounds how long a record can sit in a
	// not-yet-full batch before it is sent anyway, and is also the cadence
	// State() snapshots at.
	batchFlushInterval = 100 * time.Millisecond

	// pumpBuffer is the number of batches the delivery pump holds before
	// TrySendDropOldest starts evicting the oldest one.
	pumpBuffer = 8
)

// Engine turns a Config into a stream of Batch values: it opens (or, in a
// test, receives) a Source, drains it under a compiled filter and a budget,
// and delivers what it accepts through the pump.Pump Run returns. Its
// exported methods are safe for concurrent use; Run may be called only
// once.
type Engine struct {
	source                Source
	budget                *apicapturev1.CaptureBudget
	snapLength            uint32
	reportsInterfaceDrops bool

	mu      sync.Mutex
	started bool
	state   State
}

// New validates cfg, compiles its filter once, and opens the source cfg.Source
// names.
func New(cfg Config) (*Engine, error) {
	if cfg.Source == nil {
		return nil, fmt.Errorf("capture: Source is required")
	}
	if cfg.Budget == nil {
		return nil, fmt.Errorf("capture: Budget is required")
	}
	if !cfg.Budget.HasMaxPackets() && !cfg.Budget.HasMaxBytes() && !cfg.Budget.HasMaxDuration() {
		return nil, fmt.Errorf("capture: Budget must bound packets, bytes, or duration")
	}
	if sl := cfg.Budget.GetSnapLength(); sl > 65535 {
		return nil, fmt.Errorf("capture: Budget snap_length %d exceeds 65535", sl)
	}

	prog, err := filter.Compile(cfg.Filter)
	if err != nil {
		return nil, fmt.Errorf("compile filter: %w", err)
	}
	raw, err := filter.Assemble(prog)
	if err != nil {
		return nil, fmt.Errorf("assemble filter: %w", err)
	}

	var src Source
	var reportsInterfaceDrops bool
	switch {
	case cfg.Source.HasLocalInterface():
		li := cfg.Source.GetLocalInterface()
		s, err := rawsocket.OpenLocalInterface(li.GetInterfaceName(), li.GetPromiscuous(), raw)
		if err != nil {
			return nil, fmt.Errorf("open local interface: %w", err)
		}
		src = s
		reportsInterfaceDrops = true
	case cfg.Source.HasMirrorReceiver():
		mr := cfg.Source.GetMirrorReceiver()
		s, err := rawsocket.OpenMirrorReceiver(mr.GetEncapsulations(), mr.GetUdpPort(), mr.GetBindInterface(), raw)
		if err != nil {
			return nil, fmt.Errorf("open mirror receiver: %w", err)
		}
		src = s
	default:
		return nil, fmt.Errorf("capture: Source names neither local_interface nor mirror_receiver")
	}

	return newEngine(src, cfg.Budget, reportsInterfaceDrops), nil
}

// newEngine builds an Engine around an already-open source, so a test
// supplies a fake Source without going through New's real socket-opening
// path. reportsInterfaceDrops distinguishes a local-interface source, whose
// Stats reports a real kernel drop count, from a mirror receiver, whose
// Stats always returns zero for droppedByInterface because no such counter
// exists at that layer: per capture_counters.proto, an absent counter means
// the stage does not report one, so a mirror-sourced run never sets
// dropped_by_interface rather than reporting a misleading zero.
func newEngine(src Source, budget *apicapturev1.CaptureBudget, reportsInterfaceDrops bool) *Engine {
	snapLength := budget.GetSnapLength()
	if snapLength == 0 {
		snapLength = defaultSnapLength
	}
	return &Engine{
		source:                src,
		budget:                budget,
		snapLength:            snapLength,
		reportsInterfaceDrops: reportsInterfaceDrops,
		state: State{
			Lifecycle: apicapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_PENDING,
			LinkType:  capturev1.LinkType_LINK_TYPE_ETHERNET,
			Counters:  &capturev1.CaptureCounters{},
		},
	}
}

// Run starts the capture: one goroutine, owned by the returned pump's own
// context, reads frames from the source, builds records, batches them, and
// delivers batches through TrySendDropOldest until a budget bound is
// reached, the source reports a terminal error, or ctx is canceled. Run may
// be called only once; a second call returns an error.
func (e *Engine) Run(ctx context.Context) (*pump.Pump[Batch], error) {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return nil, fmt.Errorf("capture: Run called more than once")
	}
	e.started = true
	e.state.Lifecycle = apicapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_RUNNING
	e.mu.Unlock()

	p := pump.New[Batch](ctx, pumpBuffer)
	go e.run(p)
	return p, nil
}

// State returns the engine's live snapshot without draining the pump.
func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func (e *Engine) setState(mutate func(*State)) {
	e.mu.Lock()
	mutate(&e.state)
	e.mu.Unlock()
}

// run is the engine's sole goroutine, started by Run. It closes the source
// before signaling the pump done, so a caller that observes the pump
// finished (its data channel closed) can rely on the source's resources —
// its socket included — already being released, not release racing that
// signal.
func (e *Engine) run(p *pump.Pump[Batch]) {
	frames := e.source.Receive(p.Context())

	var (
		seq                     uint64
		acceptedPackets         uint64
		acceptedBytes           uint64
		received                uint64
		droppedByInterface      uint64
		droppedByBudget         uint64
		totalDroppedByTransport uint64
		pendingTransportDrops   uint64
		pumpFIFO                []int

		batch         []*capturev1.PacketRecord
		batchFirstSeq uint64

		stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_UNSPECIFIED
		runErr     error
	)

	var durationC <-chan time.Time
	if d := e.budget.GetMaxDuration(); d != nil {
		timer := time.NewTimer(d.AsDuration())
		defer timer.Stop()
		durationC = timer.C
	}

	flushTicker := time.NewTicker(batchFlushInterval)
	defer flushTicker.Stop()

	buildCounters := func() *capturev1.CaptureCounters {
		c := &capturev1.CaptureCounters{}
		c.SetReceived(received)
		c.SetAccepted(acceptedPackets)
		if e.reportsInterfaceDrops {
			c.SetDroppedByInterface(droppedByInterface)
		}
		c.SetDroppedByBudget(droppedByBudget)
		c.SetDroppedByTransport(totalDroppedByTransport)
		return c
	}

	pollStats := func() {
		r, d, err := e.source.Stats()
		if err != nil {
			return // best effort: a stats failure does not stop the capture.
		}
		received += r
		droppedByInterface += d
	}

	// flush sends the current batch (or, when final, an empty trailing one)
	// through the pump. A drop TrySendDropOldest reports is attributed to
	// dropped_by_transport on the *next* flush's counters snapshot, per the
	// streaming frame transport direction: this flush cannot report a drop
	// its own send just caused.
	flush := func(final bool) {
		if len(batch) == 0 && !final {
			return
		}
		totalDroppedByTransport += pendingTransportDrops
		pendingTransportDrops = 0

		counters := buildCounters()
		b := Batch{FirstSequence: batchFirstSeq, Records: batch, Counters: counters, Final: final}
		delivered, dropped := p.TrySendDropOldest(b)
		applyDropAccounting(&pumpFIFO, &pendingTransportDrops, delivered, dropped, len(batch))

		batch = nil
		batchFirstSeq = seq

		e.setState(func(s *State) { s.Counters = counters })
	}

runLoop:
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				// The source closed its channel on its own. Receive's own
				// contract closes it after ctx cancellation too (a terminal
				// error frame is best effort and may be omitted when the
				// channel is full), so a canceled pump context here means
				// this is the same operator-initiated stop the
				// p.Context().Done() case below reports, not a failure.
				if p.Context().Err() != nil {
					stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR
				} else {
					runErr = fmt.Errorf("capture: source closed its frame channel unexpectedly")
					stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR
				}
				break runLoop
			}
			if f.Err != nil {
				// A canceled context surfaces here too, racing the
				// p.Context().Done() case below: treat it the same way
				// either case would, an operator-initiated stop rather
				// than a failure, so which case wins the select cannot
				// change the outcome.
				if errors.Is(f.Err, context.Canceled) || errors.Is(f.Err, context.DeadlineExceeded) {
					stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR
				} else {
					runErr = f.Err
					stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR
				}
				break runLoop
			}

			rec := buildRecord(seq, f, e.snapLength)
			seq++
			acceptedPackets++
			acceptedBytes += uint64(len(rec.GetData()))
			batch = append(batch, rec)

			switch {
			case e.budget.HasMaxPackets() && acceptedPackets >= e.budget.GetMaxPackets():
				stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT
			case e.budget.HasMaxBytes() && acceptedBytes >= e.budget.GetMaxBytes():
				stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_BYTE_COUNT
			default:
				if len(batch) >= batchMaxRecords {
					flush(false)
				}
				continue
			}

			// The budget bound just triggered: count whatever the source
			// had already queued rather than silently discarding it, then
			// stop taking any more. The batch this record landed in is
			// left for the trailing flush(true) below to send as the Final
			// batch, rather than flushed here on size alone: a batch that
			// happened to also reach batchMaxRecords on this exact record
			// would otherwise already be gone, leaving flush(true) to send
			// a spurious empty Final batch behind it.
			droppedByBudget += drainNonBlocking(frames)
			break runLoop

		case <-durationC:
			stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_DURATION
			// Same reasoning as the budget-bound case above: whatever the
			// source already queued is this bound's own loss, not silently
			// discarded.
			droppedByBudget += drainNonBlocking(frames)
			break runLoop

		case <-flushTicker.C:
			pollStats()
			flush(false)

		case <-p.Context().Done():
			stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR
			break runLoop

		case <-p.Stopped():
			// The consumer released the pump directly (CloseData or
			// SignalStop) without canceling ctx: a distinct signal from
			// p.Context().Done() that TrySendDropOldest already silently
			// obeys by this point, but the receive loop above would
			// otherwise never notice and keep draining the source for
			// nothing.
			stopReason = apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR
			break runLoop
		}
	}

	pollStats()
	flush(true)

	// flush(true) is the last send this run will ever make, so a drop it
	// causes has no later chunk to be reported on. The stream's own
	// self-consistency is unavoidably short by that amount — a property of
	// any "report on the next chunk" protocol's final message, not a bug
	// here — but State() is a separate side channel with no such
	// constraint, so it still folds this in.
	totalDroppedByTransport += pendingTransportDrops
	pendingTransportDrops = 0

	e.setState(func(s *State) {
		switch {
		case runErr != nil:
			s.Lifecycle = apicapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED
		case stopReason == apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR:
			s.Lifecycle = apicapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELLED
		default:
			s.Lifecycle = apicapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED
		}
		s.StopReason = stopReason
		s.Counters = buildCounters()
	})

	_ = e.source.Close()

	if runErr != nil {
		p.Fail(runErr)
	} else {
		p.Done()
	}
	p.Cancel()
}

// buildRecord truncates f's data to snapLength (CaptureBudget's own default
// applies before this is called) and copies it: the source may reuse f.Data
// after this call, per rawsocket.Frame's own documentation.
func buildRecord(seq uint64, f rawsocket.Frame, snapLength uint32) *capturev1.PacketRecord {
	data := f.Data
	if uint32(len(data)) > snapLength {
		data = data[:snapLength]
	}
	owned := make([]byte, len(data))
	copy(owned, data)

	rec := &capturev1.PacketRecord{}
	rec.SetSequence(seq)
	rec.SetCapturedAt(timestamppb.New(f.CapturedAt))
	rec.SetOriginalLength(f.OriginalLength)
	rec.SetData(owned)
	if f.Envelope != nil {
		rec.SetMirror(f.Envelope)
	}
	return rec
}

// drainNonBlocking counts every frame already queued on frames without
// blocking, treating a queued terminal error frame as not a drop (it named
// no packet).
func drainNonBlocking(frames <-chan rawsocket.Frame) uint64 {
	var n uint64
	for {
		select {
		case f := <-frames:
			if f.Err == nil {
				n++
			}
		default:
			return n
		}
	}
}

// applyDropAccounting mirrors TrySendDropOldest's own buffer occupancy in
// fifo (the record count of every batch this engine believes is still
// buffered in the pump) and folds an eviction's record count into
// pendingDrops, per every case TrySendDropOldest's own doc comment
// enumerates. The engine is the pump's only producer, so no other producer
// can race the buffer between this call's two pump-internal attempts.
//
// A normal, non-evicting send (delivered && dropped == 0) cannot tell
// whether the channel had room because the buffer was not yet full or
// because a consumer had already read older batches off it: either way
// nothing was lost, but fifo has no way to observe a consumer's own reads
// directly and would otherwise grow without bound over a long, healthy run
// (fed only by an ever-shrinking fraction of eventually-evicted entries,
// never a shrinking whole). The pump's channel can never hold more than
// pumpBuffer entries at once, so fifo's own size is capped there too:
// trimming from the front after every append discards exactly the entries
// a keeping-pace consumer must already have read, without ever touching an
// entry an eviction could still blame.
func applyDropAccounting(fifo *[]int, pendingDrops *uint64, delivered bool, dropped int, sentCount int) {
	switch {
	case delivered && dropped == 0:
		*fifo = append(*fifo, sentCount)
		if len(*fifo) > pumpBuffer {
			*fifo = (*fifo)[len(*fifo)-pumpBuffer:]
		}
	case delivered && dropped == 1:
		if len(*fifo) > 0 {
			*pendingDrops += uint64((*fifo)[0])
			*fifo = (*fifo)[1:]
		}
		*fifo = append(*fifo, sentCount)
	case !delivered && dropped == 1:
		*pendingDrops += uint64(sentCount)
	case !delivered && dropped == 2:
		if len(*fifo) > 0 {
			*pendingDrops += uint64((*fifo)[0])
			*fifo = (*fifo)[1:]
		}
		*pendingDrops += uint64(sentCount)
	case !delivered && dropped == 0:
		// The pump is stopped: a clean shutdown, not a loss event.
	}
}
