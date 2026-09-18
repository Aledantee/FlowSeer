package capture

import (
	"context"

	modelcapturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/capture/v1"
	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/rawsocket"
)

// Config is what Engine needs to run one capture: where to read packets
// from, the filter to apply, and the budget to stop at. It deliberately does
// not carry a CaptureSessionConfig's name, description, or authorization —
// session bookkeeping the engine never reads, following net/capture/v1's own
// README ("the session that owns a capture is an entity outside this
// package").
type Config struct {
	// Source is required.
	Source *modelcapturev1.CaptureSource
	// Filter is optional; an absent or empty filter accepts every packet.
	Filter *capturev1.CaptureFilter
	// Budget is required.
	Budget *modelcapturev1.CaptureBudget
}

// Batch is one bounded slice of a run's packets, in delivery order,
// mirroring CapturePacketChunk minus the session ref a library does not own
// (a host wraps a Batch with the ref it does have to build one).
type Batch struct {
	FirstSequence uint64
	Records       []*capturev1.PacketRecord
	// Counters is a snapshot taken when this batch was built.
	Counters *capturev1.CaptureCounters
	// Final reports whether this is the last batch of a completed run. A
	// run that failed delivers no final batch at all; its consumer learns
	// that from the pump closing with an error.
	Final bool
}

// State is the engine's live snapshot, mirroring the fields of
// CaptureSessionState the engine itself produces (lifecycle, stop reason,
// counters, link type) without the ref and artifact fields a library does
// not own.
type State struct {
	Lifecycle  modelcapturev1.CaptureLifecycle
	StopReason modelcapturev1.CaptureStopReason
	Counters   *capturev1.CaptureCounters
	LinkType   capturev1.LinkType
}

// Source is what Engine reads frames from. rawsocket.OpenLocalInterface and
// rawsocket.OpenMirrorReceiver both return types satisfying it structurally;
// this package never imports rawsocket.Source itself, so a test can supply
// a fake without touching a real socket.
type Source interface {
	// Receive returns a channel that delivers frames until ctx is canceled
	// or the source is closed, at which point the channel is closed.
	Receive(ctx context.Context) <-chan rawsocket.Frame
	// Stats reports the packet and interface-drop counts since the last
	// call.
	Stats() (received, droppedByInterface uint64, err error)
	// Close releases the source. It is idempotent.
	Close() error
}
