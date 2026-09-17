package mutation_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	dispatchv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/mutation"
)

// TestSubmitLatchLetsOnlyOneOfCancelAndCommandWin is the latch's own
// evidence, and it has to be a concurrency test because the window the
// latch closes cannot be reached any other way. Execute checks its wait
// context immediately before the latch, so every ordering a single-threaded
// test can arrange is caught by that check first; what is left is an
// acknowledgement that lands after the context check and before the command
// goes out, which only a real race produces.
//
// The invariant is that "central released this REJECTED" and "the command
// reached the device" are mutually exclusive. Both goroutines are released
// from one barrier so they contend for the machine's lock, and every
// iteration asserts the pair. With the latch's canceled check removed this
// fails: Acknowledge returns nil while the command also went out, which
// records that nothing happened to a device that was changed.
//
// It is probabilistic in the direction that matters least. A run that never
// lands in the window passes for both the fixed and the broken code, so the
// race detector's scheduling perturbation is doing real work here; a run
// that does land fails outright for the broken code. Measured against the
// latch actually removed, five runs caught it at iterations 0, 63, 0, 0 and
// 0 — the window is wide, and the count below is well past what is needed
// rather than a hope.
func TestSubmitLatchLetsOnlyOneOfCancelAndCommandWin(t *testing.T) {
	const iterations = 500

	for i := range iterations {
		var commands atomic.Int64
		deliverer := newFakeDeliverer()
		deps := baseDeps(deliverer, fakeSubmission())
		deps.Submit = func(context.Context, *edgev1.SubmissionGrant, *accessv1.InterfaceDescriptionChange) error {
			commands.Add(1)
			return nil
		}

		m, err := mutation.Admitted(mutationRequest(1, "uplink to core"), deps)
		if err != nil {
			t.Fatalf("Admitted() error: %v", err)
		}
		checkpoint := &dispatchv1.CheckpointRequest{}
		checkpoint.SetSequence(1)
		if _, err := m.Checkpoint(context.Background(), checkpoint); err != nil {
			t.Fatalf("Checkpoint() error: %v", err)
		}

		ack := &dispatchv1.TerminalResultAck{}
		ack.SetSequence(1)
		ack.SetDisposition(accessv1.Disposition_DISPOSITION_REJECTED)

		start := make(chan struct{})
		var ackErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_ = m.Execute(context.Background())
		}()
		go func() {
			defer wg.Done()
			<-start
			ackErr = m.Acknowledge(context.Background(), ack)
		}()
		close(start)
		wg.Wait()

		released := ackErr == nil
		sent := commands.Load() > 0
		if released && sent {
			t.Fatalf("iteration %d: the acknowledgement released the mutation REJECTED and the command still reached the device; exactly one must win", i)
		}
		if !released && !sent {
			// Legitimate: the acknowledgement lost the latch and was
			// refused, and Execute failed for its own reasons. Nothing to
			// assert beyond the pair above.
			continue
		}
	}
}
