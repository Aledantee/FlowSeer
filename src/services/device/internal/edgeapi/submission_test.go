package edgeapi_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	connect "connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
)

// fakeLanes stands in for the journal's read of one device's lane record, so a
// test can move the record under an open stream the way a report on another
// replica would.
type fakeLanes struct {
	mu     sync.Mutex
	record *storev1.DeviceLaneRecord
	err    error
}

func (f *fakeLanes) Record(context.Context, string) (*storev1.DeviceLaneRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.record, f.err
}

func (f *fakeLanes) set(record *storev1.DeviceLaneRecord, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record, f.err = record, err
}

func laneRecord(sequence uint64, phase accessv1.OperationPhase, admittedAt time.Time) *storev1.DeviceLaneRecord {
	state := accessv1.MutationState_builder{
		Intent:   accessv1.MutationIntent_builder{IdempotencyKey: proto.String("k1")}.Build(),
		Sequence: proto.Uint64(sequence),
		Phase:    phase.Enum(),
	}.Build()
	if phase == accessv1.OperationPhase_OPERATION_PHASE_ABANDONED {
		state.SetDisposition(accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)
	}
	return storev1.DeviceLaneRecord_builder{
		Device:        inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDeviceID)}.Build()}.Build(),
		HighWatermark: sequence,
		Mutation:      state,
		AdmittedAt:    timestamppb.New(admittedAt),
	}.Build()
}

// submissionStream collects what the handler sends and lets a test wait for a
// pulse rather than sleep for one.
type submissionStream struct {
	mu   sync.Mutex
	sent []*edgev1.OpenDeviceSubmissionResponse
	got  chan struct{}
}

func newSubmissionStream() *submissionStream {
	return &submissionStream{got: make(chan struct{}, 64)}
}

func (c *submissionStream) Send(msg *edgev1.OpenDeviceSubmissionResponse) error {
	c.mu.Lock()
	c.sent = append(c.sent, msg)
	c.mu.Unlock()
	select {
	case c.got <- struct{}{}:
	default:
	}
	return nil
}

func (c *submissionStream) messages() []*edgev1.OpenDeviceSubmissionResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*edgev1.OpenDeviceSubmissionResponse(nil), c.sent...)
}

func (c *submissionStream) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if len(c.messages()) >= n {
			return
		}
		select {
		case <-c.got:
		case <-deadline:
			t.Fatalf("only %d messages arrived, want %d", len(c.messages()), n)
		}
	}
}

// openSubmission runs the handler on its own goroutine and returns the stream
// it writes to, a cancel, and a channel carrying the error it ended with.
func openSubmission(t *testing.T, h *harness, edgeID string, sequence uint64) (*submissionStream, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(enrollCtx(edgeID, nil))
	collector := newSubmissionStream()
	done := make(chan error, 1)
	go func() {
		done <- h.edge.OpenSubmission(ctx, edgev1.OpenDeviceSubmissionRequest_builder{
			DeviceId:  proto.String(testDeviceID),
			BindingId: proto.String(testBindingID),
			Sequence:  proto.Uint64(sequence),
		}.Build(), collector)
	}()
	t.Cleanup(cancel)
	return collector, cancel, done
}

// waitEnd returns the error the stream ended with. A regression that leaves the
// stream running reads as a failure naming the case rather than as a hang the
// whole package's timeout reports.
func waitEnd(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end")
		return nil
	}
}

func checkpointedHarness(t *testing.T) (*harness, string) {
	t.Helper()
	h, edgeID, _, _ := enrolledHarness(t)
	h.creds.material = shellMaterial()
	h.lanes.set(laneRecord(42, accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED, testClock), nil)
	return h, edgeID
}

func TestSubmissionDeliversTheGrantOnceAndThenOnlyPulses(t *testing.T) {
	h, edgeID := checkpointedHarness(t)
	stream, cancel, done := openSubmission(t, h, edgeID, 42)

	stream.waitFor(t, 3)
	cancel()
	if err := waitEnd(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("stream ended with %v, want the cancellation", err)
	}

	messages := stream.messages()
	if !messages[0].HasGrant() {
		t.Fatal("the first message is not the grant")
	}
	grant := messages[0].GetGrant()
	if err := protovalidate.Validate(grant); err != nil {
		t.Fatalf("grant fails its schema rules: %v", err)
	}
	if got := grant.GetCredential().GetCredential().GetKey(); got != "icx7150-lab-ssh" {
		t.Errorf("credential key = %q, want the policy's submission credential", got)
	}
	if got := grant.GetSshHostKeySha256(); got != testHostKey {
		t.Errorf("ssh_host_key_sha256 = %q, want the policy's pin for a shell login", got)
	}
	if got, want := grant.GetDeadline().AsTime(), testClock.Add(30*time.Second); !got.Equal(want) {
		t.Errorf("deadline = %v, want the mutation's horizon %v", got, want)
	}

	for i, msg := range messages[1:] {
		if msg.HasGrant() {
			t.Fatalf("message %d is a second grant; a one-use secret was issued twice", i+1)
		}
		if err := protovalidate.Validate(msg.GetPulse()); err != nil {
			t.Fatalf("pulse fails its schema rules: %v", err)
		}
		if got := msg.GetPulse().GetAuthority(); got != edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED {
			t.Errorf("pulse %d authority = %v, want authorized", i+1, got)
		}
	}
}

func TestSubmissionSaysAuthorizedRatherThanGoingQuiet(t *testing.T) {
	h, edgeID := checkpointedHarness(t)
	stream, cancel, _ := openSubmission(t, h, edgeID, 42)
	defer cancel()

	// The edge fails closed on silence, so a central that pulses nothing blocks
	// every write. Authority has to arrive as a value, repeatedly.
	stream.waitFor(t, 4)
	pulses := 0
	for _, msg := range stream.messages() {
		if msg.HasPulse() && msg.GetPulse().GetAuthority() == edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_AUTHORIZED {
			pulses++
		}
	}
	if pulses < 3 {
		t.Fatalf("%d authorized pulses arrived; the cadence must keep a positive pulse recent", pulses)
	}
}

func TestSubmissionRevokesWithAPulseAndEndsWithAReason(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record *storev1.DeviceLaneRecord
	}{
		{"abandoned", laneRecord(42, accessv1.OperationPhase_OPERATION_PHASE_ABANDONED, testClock)},
		{"released", laneRecord(42, accessv1.OperationPhase_OPERATION_PHASE_RELEASED, testClock)},
		{"lane freed", storev1.DeviceLaneRecord_builder{
			Device:        inventoryv1.DeviceGlobalRef_builder{Device: inventoryv1.DeviceLocalRef_builder{Id: proto.String(testDeviceID)}.Build()}.Build(),
			HighWatermark: 42,
		}.Build()},
		{"another mutation", laneRecord(43, accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED, testClock)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, edgeID := checkpointedHarness(t)
			stream, cancel, done := openSubmission(t, h, edgeID, 42)
			defer cancel()

			stream.waitFor(t, 2)
			h.lanes.set(tc.record, nil)

			err := waitEnd(t, done)
			if err == nil {
				t.Fatal("the stream ended cleanly; the edge reads a bare end as non-authorized and learns nothing")
			}
			if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
				t.Errorf("end code = %v, want failed_precondition (%v)", got, err)
			}

			messages := stream.messages()
			last := messages[len(messages)-1]
			if !last.HasPulse() || last.GetPulse().GetAuthority() != edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED {
				t.Fatal("authority was withdrawn without a REVOKED pulse; silence is not a revocation")
			}
		})
	}
}

func TestSubmissionEndsWithAReasonWhenCentralCannotTell(t *testing.T) {
	h, edgeID := checkpointedHarness(t)
	stream, cancel, done := openSubmission(t, h, edgeID, 42)
	defer cancel()

	stream.waitFor(t, 2)
	h.lanes.set(nil, errors.New("bucket unreachable"))

	err := waitEnd(t, done)
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Fatalf("end code = %v, want unavailable (%v)", got, err)
	}
	for _, msg := range stream.messages() {
		if msg.HasPulse() && msg.GetPulse().GetAuthority() == edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED {
			t.Fatal("a read failure was reported as a revocation; it is neither an authorization nor one")
		}
	}
}

func TestSubmissionOpensOnlyForACheckpointedSequence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		phase accessv1.OperationPhase
	}{
		{"intent recorded", accessv1.OperationPhase_OPERATION_PHASE_INTENT_RECORDED},
		{"admitted", accessv1.OperationPhase_OPERATION_PHASE_ADMITTED},
		{"observing", accessv1.OperationPhase_OPERATION_PHASE_OBSERVING},
		// The retry path rests on the journal never writing RECOVERING to the
		// mutation's own phase, only to last_reported_phase. If that changes,
		// the second grant a retry needs would be refused here rather than
		// silently: this row is what says so.
		{"recovering", accessv1.OperationPhase_OPERATION_PHASE_RECOVERING},
		{"verified", accessv1.OperationPhase_OPERATION_PHASE_VERIFIED},
		{"acknowledged", accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED},
		{"abandoned", accessv1.OperationPhase_OPERATION_PHASE_ABANDONED},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, edgeID := checkpointedHarness(t)
			h.lanes.set(laneRecord(42, tc.phase, testClock), nil)

			stream, cancel, done := openSubmission(t, h, edgeID, 42)
			defer cancel()
			err := waitEnd(t, done)
			if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
				t.Fatalf("open code = %v, want failed_precondition (%v)", got, err)
			}
			if len(stream.messages()) != 0 {
				t.Fatal("a refused open still sent the grant")
			}
		})
	}
}

// The schema forbids a terminal disposition beside POSSIBLY_APPLIED, but the
// journal writes without validating, so the gate refuses the state rather than
// trusting every journal arm to move the phase along with the disposition.
func TestSubmissionRefusesATerminalDispositionWhateverThePhaseSays(t *testing.T) {
	h, edgeID := checkpointedHarness(t)
	record := laneRecord(42, accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED, testClock)
	record.GetMutation().SetDisposition(accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED)
	h.lanes.set(record, nil)

	stream, cancel, done := openSubmission(t, h, edgeID, 42)
	defer cancel()
	err := waitEnd(t, done)
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("open code = %v, want failed_precondition (%v)", got, err)
	}
	if len(stream.messages()) != 0 {
		t.Fatal("a refused open still sent the grant")
	}
}

// Retiring an edge has to reach a write already in flight, not only its next
// call. The verifier refuses a retired edge at step 7, but an open stream makes
// no further calls for it to refuse.
func TestRetiringAnEdgeRevokesAnOpenSubmission(t *testing.T) {
	h, edgeID := checkpointedHarness(t)
	stream, cancel, done := openSubmission(t, h, edgeID, 42)
	defer cancel()

	stream.waitFor(t, 2)
	if _, err := h.admin.RetireEdge(context.Background(), connect.NewRequest(
		edgev1.RetireEdgeRequest_builder{Edge: edgeRefFor(edgeID)}.Build())); err != nil {
		t.Fatalf("RetireEdge: %v", err)
	}

	err := waitEnd(t, done)
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("end code = %v, want failed_precondition (%v)", got, err)
	}
	messages := stream.messages()
	last := messages[len(messages)-1]
	if !last.HasPulse() || last.GetPulse().GetAuthority() != edgev1.SubmissionAuthority_SUBMISSION_AUTHORITY_REVOKED {
		t.Fatal("a retired edge's stream ended without a REVOKED pulse")
	}
}

func TestSubmissionRefusesASequenceTheLaneDoesNotHold(t *testing.T) {
	h, edgeID := checkpointedHarness(t)

	stream, cancel, done := openSubmission(t, h, edgeID, 43)
	defer cancel()
	err := waitEnd(t, done)
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("open code = %v, want failed_precondition (%v)", got, err)
	}
	if len(stream.messages()) != 0 {
		t.Fatal("a refused open still sent the grant")
	}
}

// A mutation that reached recovery keeps the record at POSSIBLY_APPLIED, since
// a RECOVERING report moves the last reported phase and not the mutation's own.
// That is what lets the retry open a second grant for the same sequence.
func TestSubmissionAdmitsASecondGrantForASequenceInRecovery(t *testing.T) {
	h, edgeID := checkpointedHarness(t)
	record := laneRecord(42, accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED, testClock)
	record.SetLastReportedPhase(accessv1.OperationPhase_OPERATION_PHASE_RECOVERING)
	h.lanes.set(record, nil)

	for attempt := range 2 {
		stream, cancel, _ := openSubmission(t, h, edgeID, 42)
		stream.waitFor(t, 1)
		if !stream.messages()[0].HasGrant() {
			t.Fatalf("attempt %d did not receive a grant", attempt)
		}
		cancel()
	}
}

func TestSubmissionRefusesAGrantPastTheHorizon(t *testing.T) {
	h, edgeID := checkpointedHarness(t)
	h.now = testClock.Add(31 * time.Second)

	stream, cancel, done := openSubmission(t, h, edgeID, 42)
	defer cancel()
	err := waitEnd(t, done)
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("open code = %v, want failed_precondition (%v)", got, err)
	}
	if len(stream.messages()) != 0 {
		t.Fatal("a refused open still sent the grant")
	}
}

func TestSubmissionRefusesADeviceTheEdgeDoesNotHost(t *testing.T) {
	h, edgeID := checkpointedHarness(t)
	ctx := enrollCtx(edgeID, nil)
	err := h.edge.OpenSubmission(ctx, edgev1.OpenDeviceSubmissionRequest_builder{
		DeviceId:  proto.String("0192e6a0-0000-7000-8000-0000000000d9"),
		BindingId: proto.String(testBindingID),
		Sequence:  proto.Uint64(42),
	}.Build(), newSubmissionStream())
	wantConnectCode(t, err, connect.CodePermissionDenied)
}

func TestSubmissionRefusesAContextWithNoVerifiedEdge(t *testing.T) {
	h, _ := checkpointedHarness(t)
	err := h.edge.OpenSubmission(context.Background(), &edgev1.OpenDeviceSubmissionRequest{}, newSubmissionStream())
	wantConnectCode(t, err, connect.CodeUnauthenticated)
}
