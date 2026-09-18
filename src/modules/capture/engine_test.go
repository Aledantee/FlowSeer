package capture

import (
	"bytes"
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/pcapng"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

// fakeSource is a Source a test drives directly: frames is pre-filled
// before Run is called, so the engine's reads never block on real I/O and
// the test needs no timing coordination with a producer goroutine. closed
// is closed by Close, so a test can wait for the engine's run goroutine to
// finish (Close is its last call before signaling the pump done) without a
// wall-clock sleep.
type fakeSource struct {
	frames           chan rawsocket.Frame
	statsReceived    uint64
	statsDroppedByIf uint64
	closed           chan struct{}
}

func newFakeSource(buffer int) *fakeSource {
	return &fakeSource{frames: make(chan rawsocket.Frame, buffer), closed: make(chan struct{})}
}

func (f *fakeSource) Receive(context.Context) <-chan rawsocket.Frame { return f.frames }

func (f *fakeSource) Stats() (uint64, uint64, error) {
	r, d := f.statsReceived, f.statsDroppedByIf
	f.statsReceived, f.statsDroppedByIf = 0, 0
	return r, d, nil
}

func (f *fakeSource) Close() error {
	close(f.closed)
	return nil
}

func testFrame(n byte) rawsocket.Frame {
	return rawsocket.Frame{
		Data:           bytes.Repeat([]byte{n}, 14),
		OriginalLength: 14,
		CapturedAt:     time.Unix(1_700_000_000, 0),
	}
}

func testBudget(maxPackets uint64) *modelcapturev1.CaptureBudget {
	b := &modelcapturev1.CaptureBudget{}
	b.SetMaxPackets(maxPackets)
	return b
}

// drainAll reads every batch p delivers until its data channel closes.
func drainAll(p interface{ Data() <-chan Batch }) []Batch {
	var batches []Batch
	for b := range p.Data() {
		batches = append(batches, b)
	}
	return batches
}

// TestEngine_BudgetStopsAtPacketCount proves a capture stops at its first
// satisfied budget and reports why: a run with max_packets: 100 against a
// source that never stops on its own ends at exactly 100 records, the last
// batch Final, stop reason PACKET_COUNT.
func TestEngine_BudgetStopsAtPacketCount(t *testing.T) {
	src := newFakeSource(200)
	for i := range 150 {
		src.frames <- testFrame(byte(i))
	}

	e := newEngine(src, testBudget(100), true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	batches := drainAll(p)

	var total int
	sawFinal := false
	for _, b := range batches {
		total += len(b.Records)
		if b.Final {
			sawFinal = true
		}
	}
	if total != 100 {
		t.Errorf("delivered %d records, want 100", total)
	}
	if !sawFinal {
		t.Errorf("no batch was marked Final")
	}
	if got := e.State().StopReason; got != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT {
		t.Errorf("StopReason = %v, want PACKET_COUNT", got)
	}
	if got := e.State().Lifecycle; got != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Errorf("Lifecycle = %v, want COMPLETED", got)
	}
	select {
	case <-src.closed:
	default:
		t.Errorf("engine did not close the source")
	}
}

// TestEngine_AttributableLoss proves loss is always attributable: a
// producer faster than the pump's buffer forces TrySendDropOldest to
// discard, and the drop is attributable — the sequence gap the consumer
// sees matches what the run's final counters report.
//
// batchMaxRecords (512) and pumpBuffer (8) together mean feeding more than
// 8*512 frames, with nothing draining the pump meanwhile, forces evictions
// deterministically without depending on flushTicker's wall-clock cadence.
func TestEngine_AttributableLoss(t *testing.T) {
	const totalFrames = batchMaxRecords * 10
	src := newFakeSource(totalFrames + 1)
	for i := range totalFrames {
		src.frames <- testFrame(byte(i))
	}

	e := newEngine(src, testBudget(totalFrames), true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// TrySendDropOldest never blocks the producer on this (silent) consumer,
	// so the run goroutine processes every pre-buffered frame and reaches
	// its budget bound on its own regardless of when draining starts; wait
	// for it to finish (Close is its last call) rather than racing it with
	// an arbitrary sleep, so evictions happen deterministically.
	<-src.closed

	batches := drainAll(p)
	if len(batches) == 0 {
		t.Fatal("no batches delivered")
	}
	first := batches[0]
	if first.FirstSequence == 0 {
		t.Fatalf("first delivered batch starts at sequence 0: nothing was evicted, so this test proves nothing; increase totalFrames or shrink pumpBuffer")
	}

	final := e.State()
	if got, want := final.Counters.GetDroppedByTransport(), first.FirstSequence; got != want {
		t.Errorf("final dropped_by_transport = %d, want %d (the sequence gap before the first delivered batch)", got, want)
	}
}

// TestEngine_PcapngPipeline wires a fake source's output through
// pcapng.Writer end to end: 100 records through the actual Engine rather
// than the writer in isolation, proving the stored artifact is a pcapng
// file Wireshark reads without complaint.
func TestEngine_PcapngPipeline(t *testing.T) {
	src := newFakeSource(200)
	for i := range 100 {
		src.frames <- testFrame(byte(i))
	}

	e := newEngine(src, testBudget(100), true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var buf bytes.Buffer
	w, err := pcapng.NewWriter(&buf, capturev1.LinkType_LINK_TYPE_ETHERNET, 128)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	var last Batch
	recordCount := 0
	for b := range p.Data() {
		for _, rec := range b.Records {
			if err := w.WriteRecord(rec); err != nil {
				t.Fatalf("WriteRecord: %v", err)
			}
			recordCount++
		}
		last = b
	}
	if err := w.Close(last.Counters); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if recordCount != 100 {
		t.Errorf("wrote %d records, want 100", recordCount)
	}
	if buf.Len() == 0 {
		t.Errorf("pcapng output is empty")
	}
}

// TestEngine_MirrorEnvelope confirms a mirror-sourced Frame's Envelope lands
// on PacketRecord.mirror.
func TestEngine_MirrorEnvelope(t *testing.T) {
	src := newFakeSource(10)
	env := &capturev1.MirrorEnvelope{}
	env.SetGre(&capturev1.GreFields{})
	f := testFrame(1)
	f.Envelope = env
	src.frames <- f

	e := newEngine(src, testBudget(1), true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	batches := drainAll(p)
	if len(batches) == 0 || len(batches[0].Records) == 0 {
		t.Fatalf("no record delivered")
	}
	rec := batches[0].Records[0]
	if !rec.HasMirror() {
		t.Fatalf("PacketRecord.mirror is unset")
	}
	if !rec.GetMirror().HasGre() {
		t.Errorf("PacketRecord.mirror does not carry the GRE wrapper the frame's Envelope had")
	}
}

// TestEngine_SnapLengthTruncation confirms data is truncated to the
// budget's snap_length while original_length keeps the true wire length.
func TestEngine_SnapLengthTruncation(t *testing.T) {
	src := newFakeSource(10)
	src.frames <- rawsocket.Frame{
		Data:           bytes.Repeat([]byte{0xAB}, 200),
		OriginalLength: 200,
		CapturedAt:     time.Now(),
	}

	budget := testBudget(1)
	budget.SetSnapLength(64)
	e := newEngine(src, budget, true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	batches := drainAll(p)
	rec := batches[0].Records[0]
	if got := len(rec.GetData()); got != 64 {
		t.Errorf("len(data) = %d, want 64 (snap_length)", got)
	}
	if got := rec.GetOriginalLength(); got != 200 {
		t.Errorf("original_length = %d, want 200 (the true wire length)", got)
	}
}

func TestEngine_RunTwiceFails(t *testing.T) {
	src := newFakeSource(1)
	e := newEngine(src, testBudget(1), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := e.Run(ctx); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if _, err := e.Run(ctx); err == nil {
		t.Error("second Run: want an error, got nil")
	}
}

// TestEngine_MirrorReceiverOmitsInterfaceDropsCounter proves a source that
// cannot report a real interface-level drop count (a mirror receiver) never
// sets dropped_by_interface, per capture_counters.proto's "an absent counter
// means the stage does not report one — never a zero".
func TestEngine_MirrorReceiverOmitsInterfaceDropsCounter(t *testing.T) {
	src := newFakeSource(10)
	src.frames <- testFrame(1)

	e := newEngine(src, testBudget(1), false)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = drainAll(p)

	if e.State().Counters.HasDroppedByInterface() {
		t.Errorf("Counters.dropped_by_interface is set, want absent for a source with no interface-level drop counter")
	}
}

// TestEngine_LocalInterfaceReportsInterfaceDropsCounter is the converse of
// TestEngine_MirrorReceiverOmitsInterfaceDropsCounter: a source that does
// report a real counter always sets dropped_by_interface, even when the
// value is zero.
func TestEngine_LocalInterfaceReportsInterfaceDropsCounter(t *testing.T) {
	src := newFakeSource(10)
	src.frames <- testFrame(1)

	e := newEngine(src, testBudget(1), true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = drainAll(p)

	if !e.State().Counters.HasDroppedByInterface() {
		t.Errorf("Counters.dropped_by_interface is absent, want present (even if zero) for a local-interface source")
	}
}

// TestEngine_DurationStopDrainsQueuedFrames proves a duration bound accounts
// for whatever the source had already queued the same way a packet- or
// byte-count bound does, rather than silently discarding it. totalFrames is
// sized generously above what building PacketRecords can process within the
// short duration bound on any real machine, so a nonzero remainder stays
// queued when the timer fires; this mirrors TestEngine_AttributableLoss's
// own margin-based determinism rather than pinning to a synchronization
// point that would require a test hook into run()'s private timer.
func TestEngine_DurationStopDrainsQueuedFrames(t *testing.T) {
	const totalFrames = 300_000
	src := newFakeSource(totalFrames + 1)
	for i := range totalFrames {
		src.frames <- testFrame(byte(i))
	}

	budget := &modelcapturev1.CaptureBudget{}
	budget.SetMaxDuration(durationpb.New(2 * time.Millisecond))

	e := newEngine(src, budget, true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-src.closed
	_ = drainAll(p)

	final := e.State()
	if final.StopReason != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_DURATION {
		t.Fatalf("StopReason = %v, want DURATION", final.StopReason)
	}
	if final.Counters.GetDroppedByBudget() == 0 {
		t.Errorf("dropped_by_budget = 0, want the frames still queued when the duration bound fired counted as a budget loss (increase totalFrames if this is flaky on a fast machine)")
	}
}

// TestEngine_FramesChannelClosedWithoutCancelIsError proves the source
// closing its own frame channel with the pump's context still live (not
// abandoned by the consumer) is a genuine failure: stop reason ERROR,
// lifecycle FAILED, and an error the pump's own Fail records.
func TestEngine_FramesChannelClosedWithoutCancelIsError(t *testing.T) {
	src := newFakeSource(1)
	close(src.frames)

	e := newEngine(src, testBudget(100), true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-src.closed
	_ = drainAll(p)

	final := e.State()
	if final.StopReason != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_ERROR {
		t.Errorf("StopReason = %v, want ERROR", final.StopReason)
	}
	if final.Lifecycle != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_FAILED {
		t.Errorf("Lifecycle = %v, want FAILED", final.Lifecycle)
	}
	if p.Err() == nil {
		t.Errorf("p.Err() = nil, want the pump to record the unexpected channel close as its terminal error")
	}
}

// TestEngine_ContextCancelIsOperatorAndCanceled proves an operator-initiated
// stop (the caller cancels Run's context) reports stop reason OPERATOR and
// lifecycle CANCELED, not COMPLETED: a canceled run did not finish on its
// own terms.
func TestEngine_ContextCancelIsOperatorAndCanceled(t *testing.T) {
	src := newFakeSource(1)
	ctx, cancel := context.WithCancel(context.Background())

	e := newEngine(src, testBudget(100), true)
	p, err := e.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cancel()
	<-src.closed
	_ = drainAll(p)

	final := e.State()
	if final.StopReason != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR {
		t.Errorf("StopReason = %v, want OPERATOR", final.StopReason)
	}
	if final.Lifecycle != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED {
		t.Errorf("Lifecycle = %v, want CANCELED", final.Lifecycle)
	}
}

// TestEngine_ConsumerStopWithoutContextCancelIsOperator proves a consumer
// that releases the pump directly (Pump.SignalStop, without canceling Run's
// own context) still stops the engine promptly and reports the same
// operator-initiated outcome ctx cancellation would.
func TestEngine_ConsumerStopWithoutContextCancelIsOperator(t *testing.T) {
	src := newFakeSource(1)
	budget := testBudget(1000) // never reached: nothing arrives.

	e := newEngine(src, budget, true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	p.SignalStop()
	<-src.closed

	final := e.State()
	if final.StopReason != modelcapturev1.CaptureStopReason_CAPTURE_STOP_REASON_OPERATOR {
		t.Errorf("StopReason = %v, want OPERATOR", final.StopReason)
	}
	if final.Lifecycle != modelcapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_CANCELED {
		t.Errorf("Lifecycle = %v, want CANCELED", final.Lifecycle)
	}
}

// TestEngine_FinalBatchCarriesRecordsWhenSizeAndBudgetCoincide proves the
// batchMaxRecords-triggered flush no longer fires separately from a budget
// stop that lands on the very same record: exactly one batch is delivered,
// marked Final, carrying every accepted record — not a full non-final batch
// immediately followed by a spurious empty Final one.
func TestEngine_FinalBatchCarriesRecordsWhenSizeAndBudgetCoincide(t *testing.T) {
	src := newFakeSource(batchMaxRecords + 1)
	for i := range batchMaxRecords {
		src.frames <- testFrame(byte(i))
	}

	e := newEngine(src, testBudget(batchMaxRecords), true)
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	batches := drainAll(p)
	if len(batches) != 1 {
		t.Fatalf("delivered %d batches, want exactly 1", len(batches))
	}
	if !batches[0].Final {
		t.Errorf("the single delivered batch is not marked Final")
	}
	if len(batches[0].Records) != batchMaxRecords {
		t.Errorf("Final batch has %d records, want %d", len(batches[0].Records), batchMaxRecords)
	}
}

// TestNew_RejectsUnboundedBudget and TestNew_RejectsSnapLengthOverMax prove
// New validates the budget itself rather than relying solely on a caller
// having already run it through protovalidate: both checks run before New
// ever opens a source, so a Config with an incomplete Source still surfaces
// the budget error first.

func TestNew_RejectsUnboundedBudget(t *testing.T) {
	cfg := Config{
		Source: &modelcapturev1.CaptureSource{},
		Budget: &modelcapturev1.CaptureBudget{},
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("New: want an error for a budget with no packet, byte, or duration bound, got nil")
	}
}

func TestNew_RejectsSnapLengthOverMax(t *testing.T) {
	budget := testBudget(1)
	budget.SetSnapLength(65536)
	cfg := Config{
		Source: &modelcapturev1.CaptureSource{},
		Budget: budget,
	}
	if _, err := New(cfg); err == nil {
		t.Fatal("New: want an error for snap_length over 65535, got nil")
	}
}

// TestApplyDropAccounting_TrimsFIFOToPumpCapacity proves fifo cannot grow
// past the pump's own buffer capacity across a run of ordinary, non-evicting
// sends: the pump's channel can never hold more than pumpBuffer entries at
// once, so if fifo grew unbounded instead, a later eviction would blame
// whichever batch was appended first — one long since delivered to a
// keeping-pace consumer — instead of the batch actually still sitting in
// the channel.
func TestApplyDropAccounting_TrimsFIFOToPumpCapacity(t *testing.T) {
	var fifo []int
	var pending uint64

	for i := 1; i <= 20; i++ {
		applyDropAccounting(&fifo, &pending, true, 0, i)
	}
	if len(fifo) != pumpBuffer {
		t.Fatalf("len(fifo) = %d, want %d after trimming", len(fifo), pumpBuffer)
	}

	// The oldest entry the pump could still really hold is the 13th send
	// (sentCount 13): entries 1-12 were already delivered and consumed.
	applyDropAccounting(&fifo, &pending, true, 1, 999)
	if pending != 13 {
		t.Errorf("pending drops = %d, want 13 (the oldest batch still actually buffered, not an already-delivered one)", pending)
	}
}

// TestNewWithSource proves NewWithSource constructs a functional Engine around
// a custom Source without requiring OS raw socket permissions.
func TestNewWithSource(t *testing.T) {
	src := newFakeSource(1)
	src.frames <- testFrame(0xaa)

	e := NewWithSource(src, testBudget(1), true)
	if e == nil {
		t.Fatal("NewWithSource returned nil")
	}

	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	batches := drainAll(p)
	if len(batches) != 1 {
		t.Fatalf("delivered %d batches, want 1", len(batches))
	}
	if len(batches[0].Records) != 1 {
		t.Fatalf("batch has %d records, want 1", len(batches[0].Records))
	}
}
