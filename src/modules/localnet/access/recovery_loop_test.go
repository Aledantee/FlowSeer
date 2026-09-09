package access_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/lane"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// fakeWait releases each poll on demand, so a test drives the recovery loop
// tick by tick instead of spending the horizon in real time. It reports
// every wait it served, which is how a test can tell "the loop is still
// running" from "the loop exited".
type fakeWait struct {
	release chan struct{}
	served  atomic.Int64
}

func newFakeWait() *fakeWait { return &fakeWait{release: make(chan struct{})} }

func (w *fakeWait) wait(ctx context.Context, _ time.Duration) bool {
	select {
	case <-w.release:
		w.served.Add(1)
		return true
	case <-ctx.Done():
		return false
	}
}

// tick releases exactly one poll and waits for it to have been served.
func (w *fakeWait) tick(t *testing.T) {
	t.Helper()
	before := w.served.Load()
	select {
	case w.release <- struct{}{}:
	case <-time.After(3 * time.Second):
		t.Fatal("the recovery loop is not waiting; it has exited")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if w.served.Load() > before {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the released poll never ran")
}

// recoveringLane builds a lane whose device never shows the mutation
// applied, so every mutation submitted to it enters recovery.
type recoveringLane struct {
	lane     *access.Lane
	wait     *fakeWait
	reporter *recordingReporter
	reads    atomic.Int64
	submits  atomic.Int64

	// holdRead, when non-nil, blocks every device read until it is closed.
	// It is how a test parks a recovery poll inside Attempt while it still
	// holds the drain lock.
	holdMu   sync.Mutex
	holdRead chan struct{}
	inRead   chan struct{}

	// applied flips the device to showing the mutation's own description,
	// so a later recovery poll observes it as having landed after all.
	applied atomic.Bool
}

func newRecoveringLane(t *testing.T, horizon, interval time.Duration) *recoveringLane {
	t.Helper()
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	r := &recoveringLane{wait: newFakeWait(), reporter: &recordingReporter{}}
	r.lane = access.NewLane(access.Config{
		QueueCapacity:        4,
		Audit:                noopDeliverer{},
		Telemetry:            view,
		Clock:                time.Now,
		Reporter:             r.reporter,
		OperationTimeout:     2 * time.Second,
		RecoveryPollInterval: interval,
		Wait:                 r.wait.wait,
	})

	err = r.lane.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: horizon},
		OpenSNMP:      probeFactory(),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			r.reads.Add(1)
			r.holdMu.Lock()
			gate, entered := r.holdRead, r.inRead
			r.holdMu.Unlock()
			if gate != nil {
				if entered != nil {
					select {
					case entered <- struct{}{}:
					default:
					}
				}
				<-gate
			}
			if r.applied.Load() {
				return completeObservation("uplink to core"), nil
			}
			return completeObservation("stale description"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error {
			r.submits.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
	return r
}

// recoverySequence is the sequence every mutation in these tests is
// admitted at. There is only ever one open mutation per device, so a second
// value would prove nothing the first does not.
const recoverySequence = 1

// submitted is one Submit call's whole outcome, since an abandonment is a
// result rather than an error and a test has to be able to tell them apart.
type submitted struct {
	result *integrationv1.ExecuteResult
	err    error
}

// submitMutation admits a mutation and drives it to the point of entering
// recovery, returning the channel its outcome will eventually arrive on.
func (r *recoveringLane) submitMutation(t *testing.T) chan submitted {
	t.Helper()
	done := make(chan submitted, 1)
	go func() {
		result, err := r.lane.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(recoverySequence),
		})
		done <- submitted{result, err}
	}()
	deliverCheckpoint(t, r.lane, recoverySequence)
	waitForPhase(t, r.reporter, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
	return done
}

func TestZeroRecoveryMinGapDoesNotCountBackToBackObservations(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)
	done := r.submitMutation(t)

	r.wait.tick(t)
	r.wait.tick(t)
	if got := r.submits.Load(); got != 1 {
		t.Errorf("device submissions after back-to-back recovery observations = %d, want 1", got)
	}

	if err := deliverAck(t, r.lane, terminalAck(recoverySequence, accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)); err != nil {
		t.Fatalf("HandleTerminalAck() error: %v", err)
	}
	<-done
}

// TestAReadAdmittedWhileARecoveryPollHoldsTheLockIsStillServed is the
// release rule's own test, and it only tests anything because the read is
// admitted while a poll is genuinely parked inside Attempt holding
// ds.draining. An earlier version of this test submitted the read between
// polls, when nothing held the lock, so the read's own drain goroutine won
// its TryLock immediately — and it passed with the release rule removed.
//
// The rule matters because a submitter whose TryLock loses exits at once
// and never comes back. With the poll parked on the lock, that read has no
// drainer at all unless the poll drains on release.
func TestAReadAdmittedWhileARecoveryPollHoldsTheLockIsStillServed(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)
	mutationDone := r.submitMutation(t)

	// Park the next poll inside its device read, still holding the lock.
	r.holdMu.Lock()
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	r.holdRead, r.inRead = gate, entered
	r.holdMu.Unlock()

	go func() {
		select {
		case r.wait.release <- struct{}{}:
		case <-time.After(3 * time.Second):
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no poll reached the device read")
	}

	// Admitted now, with the poll holding the drain lock: this Submit's own
	// drain goroutine loses the TryLock and exits without doing anything.
	readDone := make(chan error, 1)
	go func() {
		req := readRequest()
		req.SetSequence(9)
		_, err := r.lane.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   req,
			Priority:  lane.PriorityNormal,
		})
		readDone <- err
	}()
	time.Sleep(50 * time.Millisecond)

	// Release the poll. Nothing else will ever look at that read.
	r.holdMu.Lock()
	r.holdRead, r.inRead = nil, nil
	r.holdMu.Unlock()
	close(gate)

	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("the read admitted during recovery failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read admitted while the poll held the drain lock was never served: the poll released the lock without draining")
	}

	// The mutation is still recovering; the read did not disturb it.
	select {
	case got := <-mutationDone:
		t.Fatalf("the mutation ended early with result %v, error %v", got.result, got.err)
	default:
	}
}

// TestRecoveryAbandonsAtTheHorizonAndAnswersItsCaller proves the loop
// terminates and that termination reaches the caller. A recovery that
// abandoned the mutation but left Submit blocked would be the same defect
// as never abandoning at all, from the caller's side.
func TestRecoveryAbandonsAtTheHorizonAndAnswersItsCaller(t *testing.T) {
	// A horizon short enough to have elapsed by the first poll, with a
	// poll interval long enough that the loop's own budget — the horizon
	// plus one interval — does not run out first. fakeWait ignores the
	// interval's length, so it costs the test nothing.
	r := newRecoveringLane(t, 20*time.Millisecond, time.Minute)
	done := r.submitMutation(t)

	time.Sleep(40 * time.Millisecond)
	r.wait.tick(t)

	select {
	case got := <-done:
		// An abandonment is a terminal result, not an error: the caller
		// has to report it to central, and central disposes the mutation
		// from what it says. Failing the call instead would leave the host
		// with an error to log and nothing to send.
		if got.err != nil {
			t.Fatalf("Submit() error = %v, want the abandonment as a result", got.err)
		}
		if got.result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
			t.Errorf("PhaseReached = %v, want ABANDONED", got.result.GetPhaseReached())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the abandoning poll never answered the caller")
	}

	phases := r.reporter.phases()
	if phases[len(phases)-1] != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		t.Errorf("reported phases = %v, want the last to be ABANDONED", phases)
	}

	// The lane is held: an abandonment is resolved by an explicit call,
	// never by the next mutation simply being admitted.
	_, err := r.lane.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   mutationRequest(2),
	})
	if code, _ := errs.CodeOf(err); code != access.ErrCodeDesynchronized {
		t.Errorf("Submit() after an abandonment: code = %v, want %v", code, access.ErrCodeDesynchronized)
	}
}

// TestCloseStopsARecoveryPollAndAnswersItsCaller covers shutdown. A poll
// runs under a context detached from any caller's, for up to the horizon,
// so a Lane closed without canceling its polls leaves goroutines
// contending for the lock of a lane nobody is using — and a caller blocked
// forever on a mutation nothing will ever decide.
func TestCloseStopsARecoveryPollAndAnswersItsCaller(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)
	done := r.submitMutation(t)

	r.wait.tick(t)

	if _, err := r.lane.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatalf("Submit() error = nil (result %v), want the closed lane to have surfaced something", got.result)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close() left the recovering mutation's caller blocked with no poll to answer it")
	}
}

// TestAnAcknowledgementEndsARecoveringMutation is the other exit: central
// decides while the loop is still polling. The poll must not be the only
// way out, since an operator abandoning a mutation is exactly the case
// where the device is unreachable and the polls are getting nowhere.
func TestAnAcknowledgementEndsARecoveringMutation(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)
	done := r.submitMutation(t)

	r.wait.tick(t)

	if err := deliverAck(t, r.lane, terminalAck(1, accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)); err != nil {
		t.Fatalf("HandleTerminalAck() error: %v", err)
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the acknowledgement did not answer the recovering mutation's caller")
	}

	phases := r.reporter.phases()
	if phases[len(phases)-1] != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		t.Errorf("reported phases = %v, want the last to be ABANDONED", phases)
	}
}

// TestARecoveringMutationIsAnsweredExactlyOnce is the once-guard's test.
// Three parties can end a mutation — the drain loop, the poll, and the
// acknowledgement — and sub.result holds one buffered send, so a second
// would block whichever goroutine made it forever. Here the poll and the
// acknowledgement race deliberately.
func TestARecoveringMutationIsAnsweredExactlyOnce(t *testing.T) {
	r := newRecoveringLane(t, 20*time.Millisecond, time.Minute)
	done := r.submitMutation(t)
	time.Sleep(40 * time.Millisecond)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = deliverAck(t, r.lane, terminalAck(1, accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED))
	}()
	go func() {
		defer wg.Done()
		select {
		case r.wait.release <- struct{}{}:
		case <-time.After(2 * time.Second):
		}
	}()
	wg.Wait()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("nobody answered the caller")
	}

	// A second answer would have blocked its sender rather than arriving,
	// so the check is that everything the loop still owns finishes: Close
	// must return and the lane must be usable.
	if _, err := r.lane.Close(context.Background()); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

// TestARecoveryThatDecidesNothingStillAnswersItsCaller covers the poll's
// own budget, which is the terminator of last resort. Every other exit
// depends on something happening: the device answering, the horizon
// elapsing on a poll that runs, central acknowledging, Close being called.
// If none of them does, the mutation is an obligation with nobody left
// holding it, and Submit never returns.
//
// Here the budget is spent without a single poll running — the loop is
// released only after its own context is already dead — and the caller is
// still answered.
func TestARecoveryThatDecidesNothingStillAnswersItsCaller(t *testing.T) {
	// Budget is the horizon plus one interval, so this loop has about
	// twenty milliseconds and no poll will be released within it.
	r := newRecoveringLane(t, 10*time.Millisecond, 10*time.Millisecond)
	done := r.submitMutation(t)

	select {
	case got := <-done:
		if got.err == nil {
			t.Fatal("Submit() error = nil, want the exhausted recovery reported to the caller")
		}
		if code, _ := errs.CodeOf(got.err); code != access.ErrCodeRecoveryAmbiguous {
			t.Errorf("code = %v, want %v", code, access.ErrCodeRecoveryAmbiguous)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a recovery that decided nothing left its caller blocked forever")
	}
}

// TestARecoveryThatVerifiedButWasNeverAcknowledgedSaysSo is the difference
// between two facts the caller must not have conflated: "the effect is
// unknown" and "the effect is known and central never acknowledged it".
//
// MarkVerified leaves the machine at VERIFIED, which is a real resting
// place and not terminal — RELEASED and ABANDONED are. So a poll that
// verifies and then runs out of budget waiting for an acknowledgement must
// not be answered as recovery-ambiguous: the effect was established, the
// evidence went to central, and telling the caller the opposite of what the
// lane knows is worse than telling it nothing.
//
// Asserting the result carries the verified phase, not merely that no error
// came back: "did not error" would pass for a lane that answered anything.
func TestARecoveryThatVerifiedButWasNeverAcknowledgedSaysSo(t *testing.T) {
	r := newRecoveringLane(t, 50*time.Millisecond, 2*time.Second)
	done := r.submitMutation(t)

	// The device now shows the change after all, so the next poll verifies.
	r.applied.Store(true)
	r.wait.tick(t)
	waitForPhase(t, r.reporter, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)

	// No acknowledgement ever arrives; the poll's budget is the terminator.
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Submit() error = %v, want the verified result: the effect was established", got.err)
		}
		if got.result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_VERIFIED {
			t.Errorf("PhaseReached = %v, want VERIFIED", got.result.GetPhaseReached())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the exhausted poll never answered the caller")
	}
}

// TestASuccessfulRecoveryReleasesTheDeviceHold covers what happens after.
// The hold is engaged when a mutation's effect becomes unknown; once a poll
// establishes that it applied and central acknowledges it, the ambiguity
// the hold existed for is gone. Leaving it engaged would mean every
// recovery that succeeds still needs an operator to unblock the device,
// which is the opposite of what recovering means.
func TestASuccessfulRecoveryReleasesTheDeviceHold(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)
	done := r.submitMutation(t)

	r.applied.Store(true)
	r.wait.tick(t)
	waitForPhase(t, r.reporter, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)

	if err := deliverAck(t, r.lane, terminalAck(recoverySequence, accessv1.Disposition_DISPOSITION_VERIFIED)); err != nil {
		t.Fatalf("HandleTerminalAck() error: %v", err)
	}
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Submit() error: %v", got.err)
		}
		if got.result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
			t.Errorf("PhaseReached = %v, want RELEASED", got.result.GetPhaseReached())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the acknowledgement never answered the caller")
	}

	// The next mutation must be admitted: nothing is unresolved any more.
	r.applied.Store(false)
	next := make(chan error, 1)
	go func() {
		_, err := r.lane.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(2),
		})
		next <- err
	}()
	select {
	case err := <-next:
		if code, _ := errs.CodeOf(err); code == access.ErrCodeDesynchronized {
			t.Fatal("the device is still held after a recovery that verified: a successful recovery must clear its own hold")
		}
	case <-time.After(2 * time.Second):
		// Admitted and running, which is the point.
	}
}

// resumedRequest is central re-dispatching a mutation after an edge
// restart, carrying the admission time its horizon runs from.
func resumedRequest(sequence uint64, admittedAt time.Time) *integrationv1.ExecuteRequest {
	req := mutationRequest(sequence)
	req.SetResume(true)
	req.SetAdmittedAt(timestamppb.New(admittedAt))
	return req
}

// TestAResumedMutationKeepsTheHorizonItWasAdmittedUnder is the test the
// silent failure needs. A resumed mutation whose horizon restarts at the
// edge's restart never abandons: every run gets a fresh horizon, every run
// dies before it elapses, and the device is held forever by a mutation that
// is always just about to time out. Nothing reports it and it only happens
// in the field.
//
// The admission time here is already past the horizon, so a correct
// implementation abandons on its first poll. That boundary is the only
// observable that separates the two: at any point before it, a fresh
// horizon and a carried one behave identically.
func TestAResumedMutationKeepsTheHorizonItWasAdmittedUnder(t *testing.T) {
	r := newRecoveringLane(t, time.Minute, time.Minute)

	done := make(chan submitted, 1)
	go func() {
		result, err := r.lane.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			// Admitted an hour ago, under a one-minute horizon.
			Request: resumedRequest(recoverySequence, time.Now().Add(-time.Hour)),
		})
		done <- submitted{result, err}
	}()

	// No checkpoint is delivered: central already holds it confirmed, and a
	// resumed mutation that waited for one would hang.
	waitForPhase(t, r.reporter, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
	r.wait.tick(t)

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Submit() error = %v, want the abandonment as a result", got.err)
		}
		if got.result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
			t.Fatalf("PhaseReached = %v, want ABANDONED on the first poll: the carried admission time is already past the horizon", got.result.GetPhaseReached())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the resumed mutation never ended; its horizon restarted")
	}
}

// TestAResumedMutationRefusesARejectedAcknowledgement is the latch under
// resume. Central re-dispatches with resume only past a confirmed
// checkpoint, so the command may already have reached the device on the run
// that died. Disposing it REJECTED would record that nothing happened about
// a device that may be holding the change.
func TestAResumedMutationRefusesARejectedAcknowledgement(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)

	done := make(chan submitted, 1)
	go func() {
		result, err := r.lane.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   resumedRequest(recoverySequence, time.Now()),
		})
		done <- submitted{result, err}
	}()
	waitForPhase(t, r.reporter, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)

	err := deliverAck(t, r.lane, terminalAck(recoverySequence, accessv1.Disposition_DISPOSITION_REJECTED))
	if err == nil {
		t.Fatal("HandleTerminalAck(REJECTED) error = nil, want it refused: a resumed command may already have landed")
	}
	if code, _ := errs.CodeOf(err); code != access.ErrCodeOutOfOrder {
		t.Errorf("refusal code = %v, want %v", code, access.ErrCodeOutOfOrder)
	}

	// An abandonment is accepted, which is how central ends one of these.
	if err := deliverAck(t, r.lane, terminalAck(recoverySequence, accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)); err != nil {
		t.Fatalf("HandleTerminalAck(INDETERMINATE_ABANDONED) error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the acknowledgement never answered the caller")
	}
}

// TestAResumeWithNoAdmissionTimeIsRefused covers the input that cannot be
// given a correct horizon. Refusing is the point: the plausible fallback is
// the clock, and that is exactly the restart-the-horizon bug.
func TestAResumeWithNoAdmissionTimeIsRefused(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)

	req := mutationRequest(recoverySequence)
	req.SetResume(true)
	_, err := r.lane.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   req,
	})
	if err == nil {
		t.Fatal("Submit() error = nil, want a resume with no admission time refused")
	}
}

// TestASecondMutationQueuedBehindAFailedOneIsRefusedAtDequeue is the
// admission check's blind spot. Submit checks the hold when the item is
// queued; the hold is engaged by the item ahead of this one in the same
// queue, after that check has already passed. Without a re-check at dequeue
// both run, and the second runs over a device whose state the first left
// unknown — the one thing a hold exists to prevent.
func TestASecondMutationQueuedBehindAFailedOneIsRefusedAtDequeue(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)

	// Park the first mutation's baseline read so the second can be queued
	// behind it while no hold is engaged yet.
	r.holdMu.Lock()
	gate := make(chan struct{})
	entered := make(chan struct{}, 1)
	r.holdRead, r.inRead = gate, entered
	r.holdMu.Unlock()

	first := make(chan submitted, 1)
	go func() {
		result, err := r.lane.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(recoverySequence),
		})
		first <- submitted{result, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the first mutation never reached the device")
	}

	// Queued now: Submit's own hold check passes, because nothing is held.
	second := make(chan submitted, 1)
	go func() {
		result, err := r.lane.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(recoverySequence + 1),
		})
		second <- submitted{result, err}
	}()
	time.Sleep(50 * time.Millisecond)

	r.holdMu.Lock()
	r.holdRead, r.inRead = nil, nil
	r.holdMu.Unlock()
	close(gate)

	// The first enters recovery and engages the hold. The second is
	// dequeued after that and must be refused rather than executed.
	deliverCheckpoint(t, r.lane, recoverySequence)

	select {
	case got := <-second:
		if code, _ := errs.CodeOf(got.err); code != access.ErrCodeDesynchronized {
			t.Fatalf("the queued mutation returned result %v, error %v; want %v", got.result, got.err, access.ErrCodeDesynchronized)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued mutation was never dequeued")
	}
}

// TestTheBaselineReadIsActuallyTaken exists because it was not. The
// baseline went through Machine.Observe, which refuses from ADMITTED for a
// mutation, so it failed on every mutation and the failure was swallowed by
// the "a device that cannot be read may still accept the command" branch.
// Recovery therefore never had anything to corroborate against, and
// corroboration-driven retry was unreachable — with no symptom, because a
// nil baseline is exactly what an unreadable device also produces.
//
// The observable is the device being read before the command, not the
// baseline value itself: a lane that took the baseline and discarded it
// would still be wrong, but a lane that never read the device cannot have
// one at all.
func TestTheBaselineReadIsActuallyTaken(t *testing.T) {
	r := newRecoveringLane(t, time.Hour, time.Minute)

	readsBeforeCommand := make(chan int64, 1)
	r.holdMu.Lock()
	r.holdRead, r.inRead = nil, nil
	r.holdMu.Unlock()

	done := make(chan submitted, 1)
	go func() {
		result, err := r.lane.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(recoverySequence),
		})
		done <- submitted{result, err}
	}()

	// The mutation parks at its checkpoint wait, which is after the
	// baseline read and before anything is sent to the device.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r.submits.Load() == 0 && r.reads.Load() > 0 {
			readsBeforeCommand <- r.reads.Load()
			break
		}
		time.Sleep(time.Millisecond)
	}

	select {
	case n := <-readsBeforeCommand:
		if n < 1 {
			t.Fatalf("the device was read %d times before the command, want at least the baseline", n)
		}
	default:
		t.Fatal("the device was never read before the command: no baseline was taken")
	}

	deliverCheckpoint(t, r.lane, recoverySequence)
	waitForPhase(t, r.reporter, accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
	if err := deliverAck(t, r.lane, terminalAck(recoverySequence, accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)); err != nil {
		t.Fatalf("HandleTerminalAck() error: %v", err)
	}
	<-done
}

// The recovery poll interval is derived from the device's horizon so that
// every device gets the same number of looks, whatever its apply latency.
//
// The attempt count is what this asserts, because it is what the derivation
// exists to fix. A flat interval made the count an accident of two unrelated
// numbers, and a device whose horizon happened to sit near that interval got
// one attempt — so one unlucky moment inside the window cost the whole
// budget. Asserting the interval alone would not catch a change that kept the
// arithmetic and moved the count.
func TestRecoveryLooksTheSameNumberOfTimesWhateverTheHorizon(t *testing.T) {
	t.Parallel()

	l := access.NewLane(access.Config{Clock: time.Now})

	for _, horizon := range []time.Duration{
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
	} {
		interval := access.RecoveryPollIntervalForTest(l, horizon)
		// The loop waits one interval before each attempt, inside a budget of
		// the horizon plus one interval. The last wait expires with the
		// budget rather than completing — Wait reports false and the loop
		// ends — so the attempts that actually run are the intervals that fit
		// inside the horizon itself. Observed directly before this change:
		// a 30s horizon at a 30s interval polled exactly once.
		attempts := int(horizon / interval)
		if attempts < 6 {
			t.Errorf("a horizon of %v gives %d recovery attempts at an interval of %v, want at least 6",
				horizon, attempts, interval)
		}
	}

	// A very short horizon is floored rather than polled as fast as the loop
	// can run: the horizon says how quickly a change becomes visible, not how
	// often the device wants to be asked.
	if got := access.RecoveryPollIntervalForTest(l, time.Second); got != 2*time.Second {
		t.Errorf("a one-second horizon polls every %v, want the two-second floor", got)
	}

	// And a very long one is capped, which buys it more looks rather than
	// longer gaps. An hour-long horizon divided six ways would leave a change
	// that appeared a minute in unnoticed for nine more.
	if got := access.RecoveryPollIntervalForTest(l, time.Hour); got != 30*time.Second {
		t.Errorf("an hour-long horizon polls every %v, want the thirty-second cap", got)
	}

	// An explicit interval is still the deployment's to set.
	fixed := access.NewLane(access.Config{Clock: time.Now, RecoveryPollInterval: 45 * time.Second})
	if got := access.RecoveryPollIntervalForTest(fixed, 10*time.Minute); got != 45*time.Second {
		t.Errorf("a configured interval was overridden to %v, want the configured 45s", got)
	}
}
