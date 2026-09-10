package access_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/telemetry"
)

// recordingReporter is a host's Reporter that keeps what it was handed.
// Every method returns at once, as the interface requires, so a test using
// it never hides a blocking implementation's effect on the lane.
type recordingReporter struct {
	mu          sync.Mutex
	results     []*integrationv1.ExecuteResult
	checkpoints []*integrationv1.CheckpointAck
	holds       []*integrationv1.HoldResolvedAck
	devices     []string
	ackDevices  []string
	holdDevices []string
	// onboarded is one entry per onboarding report, "device=fingerprint".
	onboarded []string
}

func (r *recordingReporter) Onboarded(_ context.Context, deviceKey, fingerprint string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onboarded = append(r.onboarded, deviceKey+"="+fingerprint)
}

// onboardings is what this reporter was told about devices being added.
func (r *recordingReporter) onboardings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.onboarded...)
}

func (r *recordingReporter) Reported(_ context.Context, deviceKey string, result *integrationv1.ExecuteResult) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, result)
	r.devices = append(r.devices, deviceKey)
}

func (r *recordingReporter) CheckpointAcked(_ context.Context, deviceKey string, ack *integrationv1.CheckpointAck) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checkpoints = append(r.checkpoints, ack)
	r.ackDevices = append(r.ackDevices, deviceKey)
}

func (r *recordingReporter) HoldResolvedAcked(_ context.Context, deviceKey string, ack *integrationv1.HoldResolvedAck) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.holds = append(r.holds, ack)
	r.holdDevices = append(r.holdDevices, deviceKey)
}

// reportedDevices is the device named alongside each report, in order. A
// report central cannot address is a report it cannot record, so which
// device a report named is part of what the reporter carries.
func (r *recordingReporter) reportedDevices() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.devices...)
}

// ackedDevices is the same for the two acknowledgements.
func (r *recordingReporter) ackedDevices() (checkpoints, holds []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ackDevices...), append([]string(nil), r.holdDevices...)
}

func (r *recordingReporter) reported() []*integrationv1.ExecuteResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*integrationv1.ExecuteResult(nil), r.results...)
}

func (r *recordingReporter) checkpointAcks() []*integrationv1.CheckpointAck {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*integrationv1.CheckpointAck(nil), r.checkpoints...)
}

func (r *recordingReporter) holdAcks() []*integrationv1.HoldResolvedAck {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*integrationv1.HoldResolvedAck(nil), r.holds...)
}

// phases reduces the reported results to the phases they carried, which is
// what most of these tests are actually asserting about.
func (r *recordingReporter) phases() []accessv1.OperationPhase {
	var out []accessv1.OperationPhase
	for _, result := range r.reported() {
		out = append(out, result.GetPhaseReached())
	}
	return out
}

// auditDeliverer mirrors the internal audit.Deliverer an external test
// cannot name, so these helpers can take one as a parameter.
type auditDeliverer interface {
	Emit(context.Context, *eventv1.DeviceOperationEvent) error
}

func laneWithReporter(t *testing.T, reporter access.Reporter, deliverer auditDeliverer, sources ...access.SubmissionCredentialSource) *access.Lane {
	t.Helper()
	view, err := telemetry.NewView(telemetry.ViewConfig{})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	cfg := access.Config{
		QueueCapacity:    4,
		Audit:            deliverer,
		Telemetry:        view,
		Clock:            time.Now,
		Reporter:         reporter,
		OperationTimeout: 2 * time.Second,
	}
	if len(sources) > 0 {
		cfg.SubmissionCredentials = sources[0]
	}
	return access.NewLane(cfg)
}

// addDeviceCountingSubmits registers "dev-1" and counts every command that
// actually reached the device, which is what the latch tests turn on.
func addDeviceCountingSubmits(t *testing.T, l *access.Lane, submits *atomic.Int64) {
	t.Helper()
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
		SubmitOverride: func(context.Context, *accessv1.InterfaceDescriptionChange) error {
			submits.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}
}

// awaitSubmit takes one Submit outcome or fails. Every test here can be
// broken in a way that leaves a mutation parked forever — that is what the
// acknowledgement path exists to prevent — and an unbounded receive would
// turn that regression into a ten-minute package timeout naming no test.
func awaitSubmit(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Submit() never returned; the mutation is parked with nothing left to wake it")
		return nil
	}
}

func terminalAck(sequence uint64, disposition accessv1.Disposition) *integrationv1.TerminalResultAck {
	ack := &integrationv1.TerminalResultAck{}
	ack.SetSequence(sequence)
	ack.SetDisposition(disposition)
	return ack
}

// deliverCheckpoint retries until the mutation has reached its checkpoint
// wait, since Submit and the delivery run on different goroutines.
func deliverCheckpoint(t *testing.T, l *access.Lane, sequence uint64) {
	t.Helper()
	req := &integrationv1.CheckpointRequest{}
	req.SetSequence(sequence)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := l.HandleCheckpoint("dev-1", req); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("checkpoint was never accepted")
}

// deliverAck retries only no-pending-wait, which means the mutation has not
// opened yet. Every other refusal is a real answer this test wants to see.
func deliverAck(t *testing.T, l *access.Lane, ack *integrationv1.TerminalResultAck) error {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := l.HandleTerminalAck(context.Background(), "dev-1", ack)
		code, _ := errs.CodeOf(err)
		if code != access.ErrCodeNoPendingWait || time.Now().After(deadline) {
			return err
		}
		time.Sleep(time.Millisecond)
	}
}

// TestRejectedAcknowledgementReachesAMutationParkedInTheFreezeWait is
// evidence for the cancellable wait context, NOT for the submit latch. It
// was written for the latch and it does not test it: with the latch's
// canceled check removed this test still passes, because the
// acknowledgement cancels the context Execute's waits run under and
// Freeze.Enter returns that cancellation before Execute ever reaches the
// latch. What it does prove is that a REJECTED acknowledgement reaches a
// mutation parked in the freeze wait at all, which is the rest point the
// plan names, and that the command does not go out.
//
// The latch covers a window this test cannot reach — an acknowledgement
// landing after the context check and before the command —
// TestSubmitLatchLetsOnlyOneOfCancelAndCommandWin is where that lives.
func TestRejectedAcknowledgementReachesAMutationParkedInTheFreezeWait(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}

	type outcome struct {
		result *integrationv1.ExecuteResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
		done <- outcome{result, err}
	}()

	deliverCheckpoint(t, l, 1)
	time.Sleep(20 * time.Millisecond)

	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_REJECTED)); err != nil {
		t.Fatalf("HandleTerminalAck(REJECTED) error = %v, want it accepted before the latch", err)
	}

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Submit() error = %v, want the released mutation", got.err)
		}
		if got.result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
			t.Errorf("PhaseReached = %v, want RELEASED", got.result.GetPhaseReached())
		}
		if got.result.GetSubmitted() {
			t.Error("Submitted = true, want false: the command was never sent")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Submit() never returned after the REJECTED acknowledgement")
	}

	if n := submits.Load(); n != 0 {
		t.Errorf("the device received %d commands, want 0: a REJECTED acknowledgement accepted before the latch must stop the command", n)
	}

	phases := reporter.phases()
	if len(phases) == 0 || phases[len(phases)-1] != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("reported phases = %v, want the last to be RELEASED", phases)
	}
}

// TestRejectedAcknowledgementAfterTheLatchIsRefused is the latch's other
// half, and the reason it is a latch rather than a check: once the command
// has gone out, disposing the mutation REJECTED would record that nothing
// happened when something might have. A VERIFIED acknowledgement is then
// accepted, proving the refusal left the mutation open rather than wedged.
func TestRejectedAcknowledgementAfterTheLatchIsRefused(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	type outcome struct {
		result *integrationv1.ExecuteResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
		done <- outcome{result, err}
	}()

	deliverCheckpoint(t, l, 1)

	// Wait until the mutation is past the command and parked on the
	// acknowledgement, which the VERIFIED report is the signal for.
	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)

	err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_REJECTED))
	if err == nil {
		t.Fatal("HandleTerminalAck(REJECTED) error = nil, want it refused after the command was sent")
	}
	if code, _ := errs.CodeOf(err); code != access.ErrCodeOutOfOrder {
		t.Errorf("refusal code = %v, want %v", code, access.ErrCodeOutOfOrder)
	}

	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_VERIFIED)); err != nil {
		t.Fatalf("HandleTerminalAck(VERIFIED) after the refusal error = %v, want nil", err)
	}

	got := <-done
	if got.err != nil {
		t.Fatalf("Submit() error: %v", got.err)
	}
	if got.result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("PhaseReached = %v, want RELEASED", got.result.GetPhaseReached())
	}
	if !got.result.GetSubmitted() {
		t.Error("Submitted = false, want true: the command reached the device")
	}
	if n := submits.Load(); n != 1 {
		t.Errorf("the device received %d commands, want exactly 1", n)
	}
}

func waitForPhase(t *testing.T, reporter *recordingReporter, want accessv1.OperationPhase) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, phase := range reporter.phases() {
			if phase == want {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no report reached %v; got %v", want, reporter.phases())
}

// TestAcknowledgementRefusals covers the three answers central has to be
// able to tell apart, each with nothing changed.
func TestAcknowledgementRefusals(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	// No mutation open at all.
	err := l.HandleTerminalAck(context.Background(), "dev-1", terminalAck(1, accessv1.Disposition_DISPOSITION_VERIFIED))
	if code, _ := errs.CodeOf(err); code != access.ErrCodeNoPendingWait {
		t.Errorf("no open mutation: code = %v, want %v", code, access.ErrCodeNoPendingWait)
	}

	done := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(7),
		})
		done <- err
	}()

	// Open, but addressed at the wrong sequence.
	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	err = l.HandleTerminalAck(context.Background(), "dev-1", terminalAck(8, accessv1.Disposition_DISPOSITION_VERIFIED))
	if code, _ := errs.CodeOf(err); code != access.ErrCodeNoPendingWait {
		t.Errorf("wrong sequence: code = %v, want %v", code, access.ErrCodeNoPendingWait)
	}

	// A disposition the phase does not allow: VERIFIED at ADMITTED, before
	// any observation exists to have verified anything.
	err = l.HandleTerminalAck(context.Background(), "dev-1", terminalAck(7, accessv1.Disposition_DISPOSITION_VERIFIED))
	if code, _ := errs.CodeOf(err); code != access.ErrCodeOutOfOrder {
		t.Errorf("VERIFIED at ADMITTED: code = %v, want %v", code, access.ErrCodeOutOfOrder)
	}

	// Nothing above moved the mutation, so the ordinary path still runs.
	deliverCheckpoint(t, l, 7)
	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
	if err := deliverAck(t, l, terminalAck(7, accessv1.Disposition_DISPOSITION_VERIFIED)); err != nil {
		t.Fatalf("HandleTerminalAck(VERIFIED) error: %v", err)
	}
	if err := awaitSubmit(t, done); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	// Ended, and its own report is on the way.
	err = l.HandleTerminalAck(context.Background(), "dev-1", terminalAck(7, accessv1.Disposition_DISPOSITION_VERIFIED))
	if code, _ := errs.CodeOf(err); code != access.ErrCodeNoPendingWait {
		t.Errorf("after release: code = %v, want %v (the mutation is no longer open)", code, access.ErrCodeNoPendingWait)
	}
}

// failingDeliverer fails the nth Emit and succeeds otherwise, so a test can
// break one specific audit delivery inside the acknowledgement walk.
type failingDeliverer struct {
	mu     sync.Mutex
	seen   int
	failAt int
}

func (d *failingDeliverer) Emit(context.Context, *eventv1.DeviceOperationEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen++
	if d.seen == d.failAt {
		return errors.New("audit delivery unavailable")
	}
	return nil
}

func (d *failingDeliverer) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seen
}

// TestAcknowledgementSurvivesAFailedAuditDelivery is evidence that the
// decision is marked before the records go out and that the mark is what
// makes central's re-send work: the first acknowledgement fails part-way
// through its own audit delivery and returns the error, the identical
// second one is accepted and completes the same walk, and the RELEASED
// report follows it rather than the failure.
func TestAcknowledgementSurvivesAFailedAuditDelivery(t *testing.T) {
	reporter := &recordingReporter{}
	deliverer := &failingDeliverer{}
	l := laneWithReporter(t, reporter, deliverer)
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	done := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
		done <- err
	}()

	deliverCheckpoint(t, l, 1)
	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)

	// Break the very next delivery, which is the acknowledgement's own
	// ACKNOWLEDGED transition record.
	deliverer.mu.Lock()
	deliverer.failAt = deliverer.seen + 1
	deliverer.mu.Unlock()

	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_VERIFIED)); err == nil {
		t.Fatal("HandleTerminalAck() error = nil, want the audit delivery failure surfaced to central")
	}

	for _, phase := range reporter.phases() {
		if phase == accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
			t.Fatal("a RELEASED report went out for an acknowledgement whose audit delivery failed")
		}
	}

	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_VERIFIED)); err != nil {
		t.Fatalf("the re-sent acknowledgement error = %v, want it accepted", err)
	}
	if err := awaitSubmit(t, done); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	phases := reporter.phases()
	if phases[len(phases)-1] != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("reported phases = %v, want the last to be RELEASED", phases)
	}
	if deliverer.count() == 0 {
		t.Error("expected audit records to have been delivered")
	}
}

// TestAbandoningAcknowledgementEndsTheMutationAndHoldsTheLane proves
// INDETERMINATE_ABANDONED is accepted at an open phase and that the hold it
// leaves refuses the next mutation until it is resolved.
func TestAbandoningAcknowledgementEndsTheMutationAndHoldsTheLane(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	done := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
		done <- err
	}()

	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)); err != nil {
		t.Fatalf("HandleTerminalAck(INDETERMINATE_ABANDONED) at ADMITTED error = %v, want it accepted", err)
	}
	if err := awaitSubmit(t, done); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	phases := reporter.phases()
	if phases[len(phases)-1] != accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		t.Errorf("reported phases = %v, want the last to be ABANDONED", phases)
	}

	_, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   mutationRequest(2),
	})
	if code, _ := errs.CodeOf(err); code != access.ErrCodeDesynchronized {
		t.Fatalf("Submit() after an abandonment: code = %v, want %v", code, access.ErrCodeDesynchronized)
	}

	resolved := &integrationv1.HoldResolved{}
	resolved.SetSequence(1)
	if err := l.ResolveHold(context.Background(), "dev-1", resolved); err != nil {
		t.Fatalf("ResolveHold() error: %v", err)
	}
	acks := reporter.holdAcks()
	if len(acks) != 1 || acks[0].GetSequence() != 1 {
		t.Errorf("HoldResolvedAcked acks = %v, want exactly one carrying sequence 1", acks)
	}
}

// TestReportOrderForAVerifiedMutation pins the sequence a host's re-send
// queue sees, and that the terminal report follows the acknowledgement
// rather than preceding it.
func TestReportOrderForAVerifiedMutation(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	done := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(3),
		})
		done <- err
	}()

	deliverCheckpoint(t, l, 3)
	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)

	// Before the acknowledgement, no terminal report exists: this is the
	// assertion that the RELEASED report is caused by the ack.
	for _, phase := range reporter.phases() {
		if phase == accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
			t.Fatal("a RELEASED report went out before the acknowledgement")
		}
	}

	if err := deliverAck(t, l, terminalAck(3, accessv1.Disposition_DISPOSITION_VERIFIED)); err != nil {
		t.Fatalf("HandleTerminalAck() error: %v", err)
	}
	if err := awaitSubmit(t, done); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	want := []accessv1.OperationPhase{
		accessv1.OperationPhase_OPERATION_PHASE_ADMITTED,
		accessv1.OperationPhase_OPERATION_PHASE_VERIFIED,
		accessv1.OperationPhase_OPERATION_PHASE_RELEASED,
	}
	got := reporter.phases()
	if len(got) != len(want) {
		t.Fatalf("reported phases = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reported phases = %v, want %v", got, want)
		}
	}

	// The admission report carries the progress arm, not an error: central
	// must be able to tell "moved" from "failed".
	if !reporter.reported()[0].HasProgress() {
		t.Error("the admission report does not carry the progress arm")
	}
	// The verified report carries the observation that proved it.
	if reporter.reported()[1].GetObservation() == nil {
		t.Error("the VERIFIED report carries no observation")
	}

	// Every report and both acknowledgements name the device. Central's
	// ReportRequest requires one and none of these messages carries it, so a
	// report the host cannot address is a report central never records.
	for _, device := range reporter.reportedDevices() {
		if device != "dev-1" {
			t.Errorf("a report named device %q, want dev-1", device)
		}
	}
	checkpointDevices, _ := reporter.ackedDevices()
	if len(checkpointDevices) != 1 || checkpointDevices[0] != "dev-1" {
		t.Errorf("checkpoint acknowledgements named %v, want one naming dev-1", checkpointDevices)
	}

	acks := reporter.checkpointAcks()
	if len(acks) != 1 || acks[0].GetSequence() != 3 {
		t.Errorf("checkpoint acks = %v, want exactly one carrying sequence 3", acks)
	}
}

// TestCoalescedReadReportsOncePerJoiner proves each joiner is reported and
// answered under its own sequence. Central admitted each read separately
// and is owed an answer for each; that one device call served them all is
// the edge's business.
func TestCoalescedReadReportsOncePerJoiner(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})

	release := make(chan struct{})
	err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		ReadOverride: func(_ context.Context, _ string) (*accessv1.InterfaceObservation, error) {
			// Hold the first read open so the second call coalesces onto
			// it rather than racing to become a second ticket.
			<-release
			return completeObservation("uplink to core"), nil
		},
	})
	if err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	results := make(chan *integrationv1.ExecuteResult, 2)
	var wg sync.WaitGroup
	for _, sequence := range []uint64{4, 5} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := readRequest()
			req.SetSequence(sequence)
			result, err := l.Submit(context.Background(), access.SubmitOptions{
				DeviceKey: "dev-1",
				Request:   req,
			})
			if err != nil {
				t.Errorf("Submit(sequence %d) error: %v", sequence, err)
				return
			}
			results <- result
		}()
	}

	// Give the second caller time to join the first's ticket.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)

	seen := map[uint64]bool{}
	for result := range results {
		seen[result.GetSequence()] = true
	}
	if !seen[4] || !seen[5] {
		t.Errorf("Submit returned sequences %v, want both 4 and 5", seen)
	}

	reportedSequences := map[uint64]int{}
	for _, result := range reporter.reported() {
		reportedSequences[result.GetSequence()]++
	}
	if reportedSequences[4] != 1 || reportedSequences[5] != 1 {
		t.Errorf("reported sequences = %v, want exactly one report each for 4 and 5", reportedSequences)
	}

	// Distinct messages, not one message relabelled: a shared message's
	// sequence would be whichever joiner wrote last.
	reported := reporter.reported()
	if len(reported) == 2 && reported[0] == reported[1] {
		t.Error("both joiners were reported the same message")
	}
}

// TestNilReporterIsANoop proves the lane never depends on a Reporter being
// wired: a host that has not built its re-send queue yet must still be able
// to drive the lane.
func TestNilReporterIsANoop(t *testing.T) {
	l := laneWithReporter(t, nil, noopDeliverer{})
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	// runMutation retries both deliveries until they are accepted, which is
	// what a test with no reporter to watch has to do: the reports are the
	// only signal for where the mutation has reached, and there are none.
	result, err := runMutation(t, l, mutationRequest(1))
	if err != nil {
		t.Fatalf("Submit() error: %v", err)
	}
	if result.GetPhaseReached() != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("PhaseReached = %v, want RELEASED", result.GetPhaseReached())
	}
	if n := submits.Load(); n != 1 {
		t.Errorf("the device received %d commands, want exactly 1", n)
	}
}

// TestRejectedAcknowledgementWhoseAuditDeliveryFailsWaitsForTheResend is
// evidence for the one branch in afterStep that is not about the ordinary
// path: a mutation whose REJECTED acknowledgement was accepted but whose
// audit delivery then failed is not free to fail on its own.
//
// The decision stands — canceled is set, and the command will never go out
// — but the mutation is not yet terminal, so nothing has told central what
// happened. Treating the step error as this operation's own outcome here
// would engage a recovery hold over a mutation central already released,
// and clear ds.current, so central's re-sent acknowledgement would come
// back no-pending-wait and the lane would sit held with nobody able to
// resolve it. Instead it waits for the re-send under a detached context.
//
// Without that branch this test fails at the second acknowledgement, which
// is exactly the shape of the bug.
func TestRejectedAcknowledgementWhoseAuditDeliveryFailsWaitsForTheResend(t *testing.T) {
	reporter := &recordingReporter{}
	deliverer := &failingDeliverer{}
	l := laneWithReporter(t, reporter, deliverer)
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	if err := l.Freeze(context.Background()); err != nil {
		t.Fatalf("Freeze() error: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
		done <- err
	}()

	deliverCheckpoint(t, l, 1)
	time.Sleep(20 * time.Millisecond)

	// Break the acknowledgement's own first audit delivery.
	deliverer.mu.Lock()
	deliverer.failAt = deliverer.seen + 1
	deliverer.mu.Unlock()

	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_REJECTED)); err == nil {
		t.Fatal("HandleTerminalAck() error = nil, want the audit delivery failure surfaced")
	}

	// Central re-sends. The mutation must still be open to receive it.
	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_REJECTED)); err != nil {
		t.Fatalf("the re-sent acknowledgement error = %v, want it accepted; the mutation failed on its own instead of waiting", err)
	}

	if err := awaitSubmit(t, done); err != nil {
		t.Fatalf("Submit() error = %v, want the released mutation", err)
	}
	if n := submits.Load(); n != 0 {
		t.Errorf("the device received %d commands, want 0", n)
	}
	phases := reporter.phases()
	if phases[len(phases)-1] != accessv1.OperationPhase_OPERATION_PHASE_RELEASED {
		t.Errorf("reported phases = %v, want the last to be RELEASED", phases)
	}
}

// Onboarding a device reports the epoch the identity probe found.
//
// This is the one report the lane makes that answers no dispatch, and it is
// the only way central learns two things: that this edge has the device, and
// what firmware it is running. Without it central holds no fingerprint until
// something happens to read the device, and every mutation intent has to name
// one — so an operator's first change to a freshly onboarded device would be
// impossible for a reason nothing explains.
//
// The fingerprint is asserted as the probe's own value rather than as
// "not empty", because a report carrying the wrong epoch is worse than none:
// central would admit intents against a device it has misidentified.
func TestOnboardingReportsTheEpochItProbed(t *testing.T) {
	reporter := &recordingReporter{}
	l := laneWithReporter(t, reporter, noopDeliverer{})

	if err := l.AddDevice(context.Background(), "dev-1", access.DeviceSession{
		DelayedEffect: interfaces.DelayedEffect{Horizon: time.Minute},
		OpenSNMP:      probeFactory(),
		ReadOverride: func(context.Context, string) (*accessv1.InterfaceObservation, error) {
			return completeObservation("uplink to core"), nil
		},
	}); err != nil {
		t.Fatalf("AddDevice() error: %v", err)
	}

	want := []string{"dev-1=" + probedFingerprint()}
	if got := reporter.onboardings(); !slices.Equal(got, want) {
		t.Errorf("onboarding reported %q, want %q", got, want)
	}
}

// laneWithReporterAndLog is laneWithReporter with the telemetry log
// captured, for a test that asserts on an event the lane emits rather than
// on state it happens to leave behind.
func laneWithReporterAndLog(t *testing.T, reporter access.Reporter, log *safeBuilder) *access.Lane {
	t.Helper()
	view, err := telemetry.NewView(telemetry.ViewConfig{
		Logger: slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("NewView() error: %v", err)
	}
	return access.NewLane(access.Config{
		QueueCapacity:    4,
		Audit:            noopDeliverer{},
		Telemetry:        view,
		Clock:            time.Now,
		Reporter:         reporter,
		OperationTimeout: 2 * time.Second,
	})
}

// TestAStaleHoldResolutionIsAcknowledgedWithoutLiftingTheHold covers the
// resolution central re-sends for a sequence this lane was never held for,
// which is ordinary: the acknowledgement travels an asynchronous queue, so
// central goes on owing the row until one lands.
//
// The lane owes two things at once here, and each would be a bug without
// the other. It acknowledges, because an edge that holds nothing for that
// sequence is telling the truth when it says so. And it keeps the hold,
// because the abandonment actually holding the lane has a resolution of its
// own still to come — clearing on a stale sequence would admit a mutation
// over a device state nobody resolved.
func TestAStaleHoldResolutionIsAcknowledgedWithoutLiftingTheHold(t *testing.T) {
	reporter := &recordingReporter{}
	log := &safeBuilder{}
	l := laneWithReporterAndLog(t, reporter, log)
	var submits atomic.Int64
	addDeviceCountingSubmits(t, l, &submits)

	done := make(chan error, 1)
	go func() {
		_, err := l.Submit(context.Background(), access.SubmitOptions{
			DeviceKey: "dev-1",
			Request:   mutationRequest(1),
		})
		done <- err
	}()
	waitForPhase(t, reporter, accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	if err := deliverAck(t, l, terminalAck(1, accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)); err != nil {
		t.Fatalf("HandleTerminalAck(INDETERMINATE_ABANDONED) error: %v", err)
	}
	if err := awaitSubmit(t, done); err != nil {
		t.Fatalf("Submit() error: %v", err)
	}

	resolved := &integrationv1.HoldResolved{}
	resolved.SetSequence(7)
	if err := l.ResolveHold(context.Background(), "dev-1", resolved); err != nil {
		t.Fatalf("ResolveHold() error: %v", err)
	}

	acks := reporter.holdAcks()
	if len(acks) != 1 || acks[0].GetSequence() != 7 {
		t.Fatalf("HoldResolvedAcked acks = %v, want exactly one carrying sequence 7", acks)
	}

	// Named by device, not only by sequence: a View is shared by every
	// device this lane serves, so a record carrying the sequence alone does
	// not say whose lane is stuck.
	logged := log.String()
	if !strings.Contains(logged, "flowseer.device.hold.resolution_ignored") {
		t.Errorf("no resolution_ignored event was emitted; log: %s", logged)
	}
	if !strings.Contains(logged, "flowseer.device.id=dev-1") {
		t.Errorf("the resolution_ignored event does not name the device; log: %s", logged)
	}

	// The hold that is actually holding the lane still refuses the next
	// mutation. Without this the test would pass for a lane that cleared on
	// any sequence at all.
	_, err := l.Submit(context.Background(), access.SubmitOptions{
		DeviceKey: "dev-1",
		Request:   mutationRequest(2),
	})
	if code, _ := errs.CodeOf(err); code != access.ErrCodeDesynchronized {
		t.Fatalf("Submit() after a stale resolution: code = %v, want %v", code, access.ErrCodeDesynchronized)
	}
}
