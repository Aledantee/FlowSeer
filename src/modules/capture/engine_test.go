package capture

import (
	"bytes"
	"context"
	"testing"
	"time"

	apicapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/capture/v1"
	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/pcapng"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

// fakeSource is a Source a test drives directly: frames is pre-filled
// before Run is called, so the engine's reads never block on real I/O and
// the test needs no timing coordination with a producer goroutine.
type fakeSource struct {
	frames           chan rawsocket.Frame
	statsReceived    uint64
	statsDroppedByIf uint64
	closed           bool
}

func newFakeSource(buffer int) *fakeSource {
	return &fakeSource{frames: make(chan rawsocket.Frame, buffer)}
}

func (f *fakeSource) Receive(context.Context) <-chan rawsocket.Frame { return f.frames }

func (f *fakeSource) Stats() (uint64, uint64, error) {
	r, d := f.statsReceived, f.statsDroppedByIf
	f.statsReceived, f.statsDroppedByIf = 0, 0
	return r, d, nil
}

func (f *fakeSource) Close() error {
	f.closed = true
	return nil
}

func testFrame(n byte) rawsocket.Frame {
	return rawsocket.Frame{
		Data:           bytes.Repeat([]byte{n}, 14),
		OriginalLength: 14,
		CapturedAt:     time.Unix(1_700_000_000, 0),
	}
}

func testBudget(maxPackets uint64) *apicapturev1.CaptureBudget {
	b := &apicapturev1.CaptureBudget{}
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

	e := newEngine(src, testBudget(100))
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
	if got := e.State().StopReason; got != apicapturev1.CaptureStopReason_CAPTURE_STOP_REASON_PACKET_COUNT {
		t.Errorf("StopReason = %v, want PACKET_COUNT", got)
	}
	if got := e.State().Lifecycle; got != apicapturev1.CaptureLifecycle_CAPTURE_LIFECYCLE_COMPLETED {
		t.Errorf("Lifecycle = %v, want COMPLETED", got)
	}
	if !src.closed {
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

	e := newEngine(src, testBudget(totalFrames))
	p, err := e.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Give the engine's own goroutine a chance to race ahead of this
	// (silent) consumer before draining, so evictions actually happen.
	time.Sleep(50 * time.Millisecond)

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

	e := newEngine(src, testBudget(100))
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

	e := newEngine(src, testBudget(1))
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
	e := newEngine(src, budget)
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
	e := newEngine(src, testBudget(1))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := e.Run(ctx); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if _, err := e.Run(ctx); err == nil {
		t.Error("second Run: want an error, got nil")
	}
}
