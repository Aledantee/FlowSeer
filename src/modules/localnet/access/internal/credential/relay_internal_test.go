package credential

import (
	"context"
	"testing"
	"time"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
)

// panicOnceStream panics from its first Receive call, simulating a failure
// on the wire read relay drives directly — the frame the converted
// goroutine actually runs on.
type panicOnceStream struct{}

func (panicOnceStream) Receive() bool                             { panic("simulated stream panic") }
func (panicOnceStream) Msg() *edgev1.OpenDeviceSubmissionResponse { return nil }
func (panicOnceStream) Err() error                                { return nil }
func (panicOnceStream) Close() error                              { return nil }

// TestSubmissionHandleRelayPanicRecordsErrInsteadOfStaleAuthority is
// evidence for this change: it forces a real panic on the goroutine
// startRelay starts, then polls Err() — never Authority() alone, since a
// handle that merely stopped updating would still report its last
// authority as current. relay's own normal exit already sets h.err this
// way; what this test proves is that ReportTo reaches the same field when
// relay never gets there itself.
func TestSubmissionHandleRelayPanicRecordsErrInsteadOfStaleAuthority(t *testing.T) {
	h := &submissionHandle{authority: edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED}
	h.startRelay(context.Background(), panicOnceStream{})

	deadline := time.Now().Add(5 * time.Second)
	for h.Err() == nil {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for Err() to report the relay goroutine's panic")
		}
		time.Sleep(time.Millisecond)
	}

	// The handle must not have quietly kept reporting AUTHORIZED as if
	// nothing happened: Err() is what a caller checks, and it is now set.
	if got := h.Authority(); got != edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED {
		t.Fatalf("Authority() changed unexpectedly to %v; relay never read a pulse before it panicked", got)
	}
}
