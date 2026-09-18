package dispatch_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	dispatchv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/dispatch/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/dispatch"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
)

// laneFake records what the demultiplexer asked of the lane, and can hold a
// Submit open so a test can dispatch a duplicate while one is running.
type laneFake struct {
	mu          sync.Mutex
	submits     []uint64
	checkpoints []uint64
	acks        []uint64
	holds       []uint64

	submitErr   error
	submitPanic any
	otherErr    error
	release     chan struct{}
	entered     chan struct{}
}

func (l *laneFake) Submit(_ context.Context, opts access.SubmitOptions) (*dispatchv1.ExecuteResult, error) {
	l.mu.Lock()
	l.submits = append(l.submits, opts.Request.GetSequence())
	gate, entered := l.release, l.entered
	l.mu.Unlock()

	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		<-gate
	}
	if l.submitPanic != nil {
		panic(l.submitPanic)
	}
	if l.submitErr != nil {
		return nil, l.submitErr
	}
	result := &dispatchv1.ExecuteResult{}
	result.SetSequence(opts.Request.GetSequence())
	result.SetPhaseReached(1)
	result.SetProgress(&dispatchv1.Progress{})
	return result, nil
}

func (l *laneFake) HandleCheckpoint(_ string, req *dispatchv1.CheckpointRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.checkpoints = append(l.checkpoints, req.GetSequence())
	return l.otherErr
}

func (l *laneFake) HandleTerminalAck(_ context.Context, _ string, ack *dispatchv1.TerminalResultAck) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.acks = append(l.acks, ack.GetSequence())
	return l.otherErr
}

func (l *laneFake) ResolveHold(_ context.Context, _ string, resolved *dispatchv1.HoldResolved) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.holds = append(l.holds, resolved.GetSequence())
	return l.otherErr
}

func (l *laneFake) submitted() []uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]uint64(nil), l.submits...)
}

type outboundFake struct {
	mu      sync.Mutex
	reports []*dispatchv1.ReportRequest
}

func (o *outboundFake) Report(_ context.Context, report *dispatchv1.ReportRequest) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reports = append(o.reports, report)
}

func (o *outboundFake) sent() []*dispatchv1.ReportRequest {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]*dispatchv1.ReportRequest(nil), o.reports...)
}

func executeDispatch(device string, sequence uint64) *dispatchv1.SubscribeResponse {
	request := &dispatchv1.ExecuteRequest{}
	request.SetSequence(sequence)
	request.SetRead(readIntent())

	message := &dispatchv1.SubscribeResponse{}
	message.SetDeviceId(device)
	message.SetExecute(request)
	return message
}

// TestEachArmReachesItsLaneCall pins the routing. A dispatch delivered to the
// wrong call is not a crash, it is central's message quietly doing nothing.
func TestEachArmReachesItsLaneCall(t *testing.T) {
	lane := &laneFake{}
	out := &outboundFake{}
	demux := dispatch.NewDemux(lane, out, nil)

	checkpoint := &dispatchv1.SubscribeResponse{}
	checkpoint.SetDeviceId("dev-1")
	checkpointReq := &dispatchv1.CheckpointRequest{}
	checkpointReq.SetSequence(4)
	checkpoint.SetCheckpoint(checkpointReq)

	ack := &dispatchv1.SubscribeResponse{}
	ack.SetDeviceId("dev-1")
	ackMsg := &dispatchv1.TerminalResultAck{}
	ackMsg.SetSequence(5)
	ack.SetTerminalAck(ackMsg)

	hold := &dispatchv1.SubscribeResponse{}
	hold.SetDeviceId("dev-1")
	holdMsg := &dispatchv1.HoldResolved{}
	holdMsg.SetSequence(6)
	hold.SetHoldResolved(holdMsg)

	for _, message := range []*dispatchv1.SubscribeResponse{checkpoint, ack, hold} {
		if err := demux.Handle(context.Background(), message); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}

	lane.mu.Lock()
	defer lane.mu.Unlock()
	if len(lane.checkpoints) != 1 || lane.checkpoints[0] != 4 {
		t.Errorf("checkpoints = %v, want [4]", lane.checkpoints)
	}
	if len(lane.acks) != 1 || lane.acks[0] != 5 {
		t.Errorf("terminal acks = %v, want [5]", lane.acks)
	}
	if len(lane.holds) != 1 || lane.holds[0] != 6 {
		t.Errorf("hold resolutions = %v, want [6]", lane.holds)
	}
}

// TestADuplicateDispatchIsAnsweredFromTheReportNotRun is the case the
// registry exists for. Central re-dispatches a sequence it has no report
// for; running it again would apply the operation to the device twice.
func TestADuplicateDispatchIsAnsweredFromTheReportNotRun(t *testing.T) {
	lane := &laneFake{}
	out := &outboundFake{}
	demux := dispatch.NewDemux(lane, out, nil)

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	demux.Wait()

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle the duplicate: %v", err)
	}
	demux.Wait()

	if got := lane.submitted(); len(got) != 1 {
		t.Fatalf("the lane saw %v, want the operation submitted exactly once", got)
	}

	// Another device's sequence 3 is a different operation and is admitted.
	// Without this the registry could be keyed on sequence alone and every
	// assertion above would still hold.
	if err := demux.Handle(context.Background(), executeDispatch("dev-2", 3)); err != nil {
		t.Fatalf("Handle for a second device: %v", err)
	}
	// And a second sequence on the first device, which the mirror image of
	// the same mistake — a registry keyed on device alone — would refuse.
	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 4)); err != nil {
		t.Fatalf("Handle a second sequence: %v", err)
	}
	demux.Wait()
	if got := lane.submitted(); len(got) != 3 {
		t.Fatalf("the lane saw %v, want three distinct operations admitted", got)
	}

	reports := out.sent()
	if len(reports) != 4 {
		t.Fatalf("sent %d reports, want 4: three operations and the duplicate answered from the first", len(reports))
	}
	if reports[1].GetResult().GetSequence() != 3 || reports[1].GetDeviceId() != "dev-1" {
		t.Errorf("the duplicate's answer is %v, want dev-1 sequence 3", reports[1])
	}
}

// TestADuplicateWhileRunningSaysNothing: an operation still in flight has no
// report yet, and inventing one would tell central something this edge does
// not know. It must also not be submitted a second time.
func TestADuplicateWhileRunningSaysNothing(t *testing.T) {
	lane := &laneFake{release: make(chan struct{}), entered: make(chan struct{}, 1)}
	out := &outboundFake{}
	demux := dispatch.NewDemux(lane, out, nil)

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	select {
	case <-lane.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the operation never reached the lane")
	}

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle the duplicate: %v", err)
	}
	if got := out.sent(); len(got) != 0 {
		t.Errorf("sent %d reports while the operation was still running, want 0", len(got))
	}

	close(lane.release)
	demux.Wait()
	if got := lane.submitted(); len(got) != 1 {
		t.Errorf("the lane saw %v, want the operation submitted exactly once", got)
	}
}

func TestAConfirmedProgressReportDoesNotReadmitARunningOperation(t *testing.T) {
	lane := &laneFake{release: make(chan struct{}), entered: make(chan struct{}, 2)}
	demux := dispatch.NewDemux(lane, &outboundFake{}, nil)

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	select {
	case <-lane.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("the operation never reached the lane")
	}

	demux.Confirmed("dev-1", 3)
	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle after confirming a progress report: %v", err)
	}

	close(lane.release)
	demux.Wait()
	if got := lane.submitted(); len(got) != 1 {
		t.Errorf("the lane saw %v, want the running operation submitted exactly once", got)
	}
}

// TestALaneRefusalReachesCentralWithItsOwnCode: which codes are retryable is
// central's policy, so the lane's code goes on the wire unchanged. An edge
// that translated them would be deciding on central's behalf with less to go
// on.
func TestALaneRefusalReachesCentralWithItsOwnCode(t *testing.T) {
	lane := &laneFake{otherErr: errs.New().Code(access.ErrCodeNoPendingWait).Msg("no mutation is waiting")}
	out := &outboundFake{}
	demux := dispatch.NewDemux(lane, out, nil)

	checkpoint := &dispatchv1.SubscribeResponse{}
	checkpoint.SetDeviceId("dev-1")
	req := &dispatchv1.CheckpointRequest{}
	req.SetSequence(9)
	checkpoint.SetCheckpoint(req)

	if err := demux.Handle(context.Background(), checkpoint); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reports := out.sent()
	if len(reports) != 1 {
		t.Fatalf("sent %d reports, want 1 refusal", len(reports))
	}
	refused := reports[0].GetRefused()
	if refused == nil {
		t.Fatal("the report is not a refusal")
	}
	if refused.GetCode() != string(access.ErrCodeNoPendingWait) {
		t.Errorf("code = %q, want %q", refused.GetCode(), string(access.ErrCodeNoPendingWait))
	}
	if refused.GetKind() != dispatchv1.DispatchKind_DISPATCH_KIND_CHECKPOINT {
		t.Errorf("kind = %v, want CHECKPOINT", refused.GetKind())
	}
	if refused.GetSequence() != 9 {
		t.Errorf("sequence = %d, want 9", refused.GetSequence())
	}
}

func TestAnUncodedLaneRefusalDoesNotClaimTheDeviceIsUnknown(t *testing.T) {
	lane := &laneFake{otherErr: errors.New("lane refused without a classification")}
	out := &outboundFake{}
	demux := dispatch.NewDemux(lane, out, nil)

	checkpoint := &dispatchv1.SubscribeResponse{}
	checkpoint.SetDeviceId("dev-1")
	req := &dispatchv1.CheckpointRequest{}
	req.SetSequence(9)
	checkpoint.SetCheckpoint(req)

	if err := demux.Handle(context.Background(), checkpoint); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	reports := out.sent()
	if len(reports) != 1 || reports[0].GetRefused() == nil {
		t.Fatalf("sent %d reports, want one refusal", len(reports))
	}
	got := reports[0].GetRefused().GetCode()
	if got == "" {
		t.Fatal("uncoded lane refusal reached central without a valid refusal code")
	}
	if got == string(access.ErrCodeUnknownDevice) {
		t.Fatalf("uncoded lane refusal reached central as %q, which central may dispose REJECTED", got)
	}
}

func TestALaneRefusalDoesNotLeaveTheOperationInFlight(t *testing.T) {
	lane := &laneFake{submitErr: errs.New().Code(access.ErrCodeNoPendingWait).Msg("lane refused")}
	demux := dispatch.NewDemux(lane, &outboundFake{}, nil)

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	demux.Wait()
	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle after refusal: %v", err)
	}
	demux.Wait()

	if got := lane.submitted(); len(got) != 2 {
		t.Errorf("the lane saw %v, want the refused operation admitted again", got)
	}
}

// TestASubmitPanicDoesNotLeaveTheOperationInFlight is evidence for the
// converted goroutine in execute: it forces a real panic out of Submit and
// checks that the process survives, that central is told the dispatch was
// refused rather than hearing nothing, and that the sequence is admittable
// again rather than stuck "in flight" forever. A registry entry left admitted
// with nothing to clear it is silent, where a dropped connection is not.
func TestASubmitPanicDoesNotLeaveTheOperationInFlight(t *testing.T) {
	lane := &laneFake{submitPanic: "the lane fell over"}
	out := &outboundFake{}
	demux := dispatch.NewDemux(lane, out, nil)

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	demux.Wait()

	reports := out.sent()
	if len(reports) != 1 || reports[0].GetRefused() == nil {
		t.Fatalf("sent %d reports, want one refusal reporting the panic", len(reports))
	}
	if seq := reports[0].GetRefused().GetSequence(); seq != 3 {
		t.Errorf("refused sequence = %d, want 3", seq)
	}

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle after panic: %v", err)
	}
	demux.Wait()

	if got := lane.submitted(); len(got) != 2 {
		t.Errorf("the lane saw %v, want the panicked operation admitted again", got)
	}
}

// readIntent is the simplest dispatch body: a typed read of one interface.
func readIntent() *accessv1.TypedRead {
	iface := &accessv1.InterfaceReadIntent{}
	iface.SetInterfaceName("ethernet 1/1/1")
	read := &accessv1.TypedRead{}
	read.SetInterface(iface)
	return read
}

// TestAnOperationIsForgottenOnlyOnceCentralConfirmsIt pins the lifetime.
// Forgetting when the operation finished would mean the next re-dispatch ran
// it on the device again —
// and a completed operation is the case most likely to be re-dispatched,
// because central re-dispatches exactly what it has no report for.
func TestAnOperationIsForgottenOnlyOnceCentralConfirmsIt(t *testing.T) {
	lane := &laneFake{}
	out := &outboundFake{}
	demux := dispatch.NewDemux(lane, out, nil)

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	demux.Wait()

	// Still remembered after completion: a re-dispatch is answered, not run.
	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle the duplicate: %v", err)
	}
	demux.Wait()
	if got := lane.submitted(); len(got) != 1 {
		t.Fatalf("the lane saw %v after completion, want one submission", got)
	}

	// Once central confirms the report, the entry goes and the sequence is
	// free again — which is what stops the map growing with every operation
	// this edge has ever run.
	demux.Confirmed("dev-1", 3)
	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 3)); err != nil {
		t.Fatalf("Handle after confirmation: %v", err)
	}
	demux.Wait()
	if got := lane.submitted(); len(got) != 2 {
		t.Errorf("the lane saw %v after confirmation, want the sequence admitted again", got)
	}
}

// TestAnUncodedLaneErrorIsRefusedUnderThisAgentsOwnCode covers the branch that
// has to invent a code, and why it must not borrow one of the lane's.
//
// Central classifies a refusal by its code and acts terminally on some of
// them: access/unknown-device resolves against the registry and disposes the
// mutation REJECTED when the device is no longer listed. The lane returns
// uncoded errors — a mutation with no sequence, a resume with no admission
// time, a canceled context at shutdown — and reporting one of those under a
// code central acts on would dispose a mutation for a cause that never
// happened. A code central does not name falls to its default and stays
// retryable.
func TestAnUncodedLaneErrorIsRefusedUnderThisAgentsOwnCode(t *testing.T) {
	lane := &laneFake{submitErr: errors.New("the lane declined without a code")}
	out := &outboundFake{}
	demux := dispatch.NewDemux(lane, out, nil)

	if err := demux.Handle(context.Background(), executeDispatch("dev-1", 4)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	demux.Wait()

	reports := out.sent()
	if len(reports) != 1 {
		t.Fatalf("sent %d reports, want 1 refusal", len(reports))
	}
	refused := reports[0].GetRefused()
	if refused == nil {
		t.Fatal("the report is not a refusal")
	}
	if got := refused.GetCode(); got != string(dispatch.ErrCodeUncodedRefusal) {
		t.Errorf("code = %q, want %q", got, string(dispatch.ErrCodeUncodedRefusal))
	}
	if got := refused.GetCode(); got == string(access.ErrCodeUnknownDevice) {
		t.Error("an uncoded error was refused as unknown-device; central would dispose the mutation on it")
	}
}
