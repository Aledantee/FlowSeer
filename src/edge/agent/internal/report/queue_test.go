package report_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	integrationv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/integration/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/report"
)

// centralFake accepts or refuses reports and records what it received.
type centralFake struct {
	mu sync.Mutex
	// attempts counts every call, refused ones included. A test that waits
	// only for accepted reports cannot tell "refused" from "never tried",
	// and the refusal cases are exactly the ones that need the difference.
	attempts  int
	received  []*integrationv1.ReportRequest
	refuse    bool
	reportErr error
	accepted  chan struct{}
	// duringReport runs inside a call, so a test can admit a report while an
	// earlier one for the same operation is still in flight.
	duringReport func()
}

func (c *centralFake) Report(
	_ context.Context, req *connect.Request[integrationv1.ReportRequest],
) (*connect.Response[integrationv1.ReportResponse], error) {
	c.mu.Lock()
	c.attempts++
	refuse := c.refuse
	reportErr := c.reportErr
	if !refuse {
		c.received = append(c.received, req.Msg)
	}
	notify := c.accepted
	during := c.duringReport
	c.mu.Unlock()

	if during != nil {
		during()
	}
	if refuse {
		return nil, errors.New("central is unavailable")
	}
	if reportErr != nil {
		return nil, reportErr
	}
	if notify != nil {
		select {
		case notify <- struct{}{}:
		default:
		}
	}
	return connect.NewResponse(&integrationv1.ReportResponse{}), nil
}

func (c *centralFake) got() []*integrationv1.ReportRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*integrationv1.ReportRequest(nil), c.received...)
}

func (c *centralFake) attemptCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts
}

func (c *centralFake) setRefuse(refuse bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refuse = refuse
}

type confirmSpy struct {
	mu        sync.Mutex
	confirmed [][2]any
}

func (s *confirmSpy) Confirmed(device string, sequence uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.confirmed = append(s.confirmed, [2]any{device, sequence})
}

func (s *confirmSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.confirmed)
}

func resultReport(device string, sequence uint64, description string) *integrationv1.ReportRequest {
	observation := &accessv1.InterfaceObservation{}
	observation.SetInterfaceName("ethernet 1/1/1")
	observation.SetDescription(description)

	result := &integrationv1.ExecuteResult{}
	result.SetSequence(sequence)
	result.SetObservation(observation)

	req := &integrationv1.ReportRequest{}
	req.SetDeviceId(device)
	req.SetResult(result)
	return req
}

func onboardedReport(device string) *integrationv1.ReportRequest {
	onboarded := &integrationv1.Onboarded{}
	onboarded.SetFirmwareFingerprint("fw-A")
	req := &integrationv1.ReportRequest{}
	req.SetDeviceId(device)
	req.SetOnboarded(onboarded)
	return req
}

func newQueue(t *testing.T, central *centralFake, confirm report.Confirmer, ceiling int) *report.Queue {
	t.Helper()
	q, err := report.New(report.Config{Client: central, Confirm: confirm, Ceiling: ceiling})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return q
}

// drainOnce runs the loop until central has accepted at least want reports,
// then stops it. It always waits for at least one attempt: waiting only on
// accepted reports means a test expecting none cancels before a pass has
// happened, and then asserts about a queue nothing has touched.
func drainOnce(t *testing.T, q *report.Queue, central *centralFake, want int) {
	t.Helper()
	central.mu.Lock()
	central.accepted = make(chan struct{}, 1)
	before := central.attempts
	central.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx, 5*time.Millisecond) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if central.attemptCount() > before && len(central.got()) >= want {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if central.attemptCount() == before {
		t.Fatal("the queue never attempted a send; this test would assert about a queue nothing touched")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the queue loop never stopped")
	}
}

// TestALaterReportSupersedesAnEarlierOne is the queue's shape. A mutation
// reports admission, verification and release; central needs the last one,
// and a queue that accumulated would re-send three where one is the answer.
func TestALaterReportSupersedesAnEarlierOne(t *testing.T) {
	central := &centralFake{refuse: true}
	q := newQueue(t, central, nil, 0)

	q.Report(context.Background(), resultReport("dev-1", 3, "first"))
	q.Report(context.Background(), resultReport("dev-1", 3, "second"))
	q.Report(context.Background(), resultReport("dev-1", 3, "third"))

	if got := q.Pending(); got != 1 {
		t.Fatalf("Pending() = %d, want 1: the three reports are about one operation", got)
	}

	central.setRefuse(false)
	drainOnce(t, q, central, 1)

	received := central.got()
	if len(received) != 1 {
		t.Fatalf("central received %d reports, want 1", len(received))
	}
	if got := received[0].GetResult().GetObservation().GetDescription(); got != "third" {
		t.Errorf("central received %q, want the latest report", got)
	}
}

// TestAReportIsKeptUntilCentralAnswers is the re-send. Report cannot fail, so
// this loop is the only thing that knows whether central has it; removing an
// entry on a failed send would be a report the edge believes it made and
// central never received.
func TestAReportIsKeptUntilCentralAnswers(t *testing.T) {
	central := &centralFake{refuse: true}
	confirm := &confirmSpy{}
	q := newQueue(t, central, confirm, 0)

	q.Report(context.Background(), resultReport("dev-1", 3, "value"))
	drainOnce(t, q, central, 0)

	if got := q.Pending(); got != 1 {
		t.Fatalf("Pending() = %d after a refused send, want 1", got)
	}
	if confirm.count() != 0 {
		t.Error("an unsent report was confirmed")
	}

	central.setRefuse(false)
	drainOnce(t, q, central, 1)

	if got := q.Pending(); got != 0 {
		t.Errorf("Pending() = %d after central accepted, want 0", got)
	}
	if confirm.count() != 1 {
		t.Errorf("confirmed %d operations, want 1: the registry and this queue share one drain condition", confirm.count())
	}

	// Contact lost again, with a fresh report. The queue does not remember
	// that central was reachable a moment ago: every report is held until
	// this one is confirmed on its own.
	central.setRefuse(true)
	q.Report(context.Background(), resultReport("dev-1", 4, "after"))
	drainOnce(t, q, central, 1)

	if got := q.Pending(); got != 1 {
		t.Errorf("Pending() = %d after contact was lost again, want 1", got)
	}
	if confirm.count() != 1 {
		t.Errorf("confirmed %d operations, want still 1: nothing new was accepted", confirm.count())
	}
}

func TestPermanentReportRefusalsAreVisibleAndRetained(t *testing.T) {
	for _, code := range []connect.Code{connect.CodePermissionDenied, connect.CodeInvalidArgument} {
		t.Run(code.String(), func(t *testing.T) {
			var logs bytes.Buffer
			central := &centralFake{reportErr: connect.NewError(code, errors.New("report rejected"))}
			confirm := &confirmSpy{}
			q, err := report.New(report.Config{
				Client:  central,
				Confirm: confirm,
				Logger:  slog.New(slog.NewJSONHandler(&logs, nil)),
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			q.Report(context.Background(), resultReport("dev-1", 3, "value"))
			drainOnce(t, q, central, 0)

			if got := q.Pending(); got != 1 {
				t.Errorf("Pending() = %d, want 1: a refusal is not confirmation", got)
			}
			if confirm.count() != 0 {
				t.Errorf("confirmed %d operations, want 0: central refused the report", confirm.count())
			}
			if got := logs.String(); !strings.Contains(got, `"level":"ERROR"`) {
				t.Fatalf("permanent refusal was not operator-visible at ERROR: %s", got)
			}
		})
	}
}

// TestTheCeilingDropsOldestAndNeverOnboarded pins what the edge sacrifices
// under pressure. Almost every report is one central will ask for again — its
// outbox re-derives what it holds no report for — but Onboarded is
// edge-initiated, so central does not know to ask, and losing it leaves a
// record believing this edge still holds state it lost at restart.
func TestTheCeilingDropsOldestAndNeverOnboarded(t *testing.T) {
	central := &centralFake{refuse: true}
	q := newQueue(t, central, nil, 3)

	q.Report(context.Background(), onboardedReport("dev-1"))
	q.Report(context.Background(), resultReport("dev-1", 1, "oldest"))
	q.Report(context.Background(), resultReport("dev-1", 2, "middle"))
	q.Report(context.Background(), resultReport("dev-1", 3, "newest"))

	if got := q.Pending(); got != 3 {
		t.Fatalf("Pending() = %d, want the ceiling of 3", got)
	}
	if got := q.Dropped(); got != 1 {
		t.Fatalf("Dropped() = %d, want 1", got)
	}

	central.setRefuse(false)
	drainOnce(t, q, central, 3)

	var sawOnboarded, sawOldest bool
	for _, received := range central.got() {
		if received.HasOnboarded() {
			sawOnboarded = true
		}
		if received.GetResult().GetObservation().GetDescription() == "oldest" {
			sawOldest = true
		}
	}
	if !sawOnboarded {
		t.Error("the onboarding report was dropped; central does not know to ask for it")
	}
	if sawOldest {
		t.Error("the oldest droppable report survived; the ceiling dropped something else")
	}
}

// TestOneRefusedReportDoesNotHoldTheRestHostage: a central refusing one
// device's report may accept another's, and a pass that stopped at the first
// error would hold everything behind the oldest problem.
func TestOneRefusedReportDoesNotHoldTheRestHostage(t *testing.T) {
	central := &selectiveCentral{refuseDevice: "dev-1"}
	q, err := report.New(report.Config{Client: central})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	q.Report(context.Background(), resultReport("dev-1", 1, "blocked"))
	q.Report(context.Background(), resultReport("dev-2", 1, "fine"))

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_ = q.Run(ctx, 5*time.Millisecond)

	if got := q.Pending(); got != 1 {
		t.Errorf("Pending() = %d, want 1: dev-2's report should have gone through", got)
	}
}

type selectiveCentral struct {
	mu           sync.Mutex
	refuseDevice string
	accepted     int
}

func (c *selectiveCentral) Report(
	_ context.Context, req *connect.Request[integrationv1.ReportRequest],
) (*connect.Response[integrationv1.ReportResponse], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if req.Msg.GetDeviceId() == c.refuseDevice {
		return nil, errors.New("central refuses this device")
	}
	c.accepted++
	return connect.NewResponse(&integrationv1.ReportResponse{}), nil
}

// TestAnUnknownArmSupersedesNothing covers the branch that exists because
// this build does not know every report a future one might send.
//
// Superseding is a judgment — that a later report answers the same question
// as an earlier one — and this is the branch that has already admitted it
// cannot make it. Keying two unknown reports together would lose one per
// device in a build that adds two arms at once, silently, and the loss would
// look like central never asking for it.
func TestAnUnknownArmSupersedesNothing(t *testing.T) {
	central := &centralFake{refuse: true}
	confirm := &confirmSpy{}
	q := newQueue(t, central, confirm, 0)

	// Two reports with no arm this build recognizes, and a real one for the
	// same device, which they must not displace either.
	q.Report(context.Background(), unknownReport("dev-1"))
	q.Report(context.Background(), unknownReport("dev-1"))
	q.Report(context.Background(), resultReport("dev-1", 3, "real"))

	if got := q.Pending(); got != 3 {
		t.Fatalf("Pending() = %d, want 3: neither unknown report may replace the other or the real one", got)
	}

	central.setRefuse(false)
	drainOnce(t, q, central, 3)

	if got := len(central.got()); got != 3 {
		t.Errorf("central received %d reports, want 3", got)
	}
	// And an unknown report confirms no operation: its sequence slot holds
	// the queue's own counter, not a sequence anybody dispatched.
	if confirm.count() != 1 {
		t.Errorf("confirmed %d operations, want 1: only the real report names one", confirm.count())
	}
}

// unknownReport is a report whose arm this build does not recognize, which is
// what an edge older than its central receives.
func unknownReport(device string) *integrationv1.ReportRequest {
	req := &integrationv1.ReportRequest{}
	req.SetDeviceId(device)
	return req
}

func progressReport(device string, sequence uint64) *integrationv1.ReportRequest {
	result := &integrationv1.ExecuteResult{}
	result.SetSequence(sequence)
	result.SetPhaseReached(accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED)
	result.SetProgress(&integrationv1.Progress{})

	req := &integrationv1.ReportRequest{}
	req.SetDeviceId(device)
	req.SetResult(result)
	return req
}

func checkpointAckReport(device string, sequence uint64) *integrationv1.ReportRequest {
	ack := &integrationv1.CheckpointAck{}
	ack.SetSequence(sequence)

	req := &integrationv1.ReportRequest{}
	req.SetDeviceId(device)
	req.SetCheckpointAck(ack)
	return req
}

func refusedReport(device string, sequence uint64) *integrationv1.ReportRequest {
	refused := &integrationv1.Refused{}
	refused.SetSequence(sequence)
	refused.SetKind(integrationv1.DispatchKind_DISPATCH_KIND_EXECUTE)
	refused.SetCode("access/unknown-device")

	req := &integrationv1.ReportRequest{}
	req.SetDeviceId(device)
	req.SetRefused(refused)
	return req
}

// TestOnlyAReportThatEndsAnOperationConfirmsIt is the seam between this queue
// and the dispatch registry, and the reason it is drawn where it is.
//
// The registry answers a duplicate dispatch instead of running the operation
// again, and it holds the operation until this queue says central has the
// answer. A progress report and an acknowledgement are both taken by central
// while the operation is still running: confirming on either releases the
// registry mid-flight, and a re-dispatch of that sequence — which central
// derives whenever an Onboarded report clears the record's confirmations — is
// then admitted a second time and applied to the device twice.
func TestOnlyAReportThatEndsAnOperationConfirmsIt(t *testing.T) {
	for _, tc := range []struct {
		name    string
		report  *integrationv1.ReportRequest
		confirm bool
	}{
		{"a progress report leaves the operation running", progressReport("dev-1", 7), false},
		{"a checkpoint acknowledgement leaves it running", checkpointAckReport("dev-1", 7), false},
		{"an observation ends it", resultReport("dev-1", 7, "up"), true},
		{"a refusal ends it", refusedReport("dev-1", 7), true},
		{"an onboarding report is about no operation", onboardedReport("dev-1"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			central := &centralFake{}
			confirm := &confirmSpy{}
			q := newQueue(t, central, confirm, 0)

			q.Report(context.Background(), tc.report)
			drainOnce(t, q, central, 1)

			if got := confirm.count(); (got > 0) != tc.confirm {
				t.Errorf("Confirmed called %d times, want confirmed=%v", got, tc.confirm)
			}
			if got := q.Pending(); got != 0 {
				t.Errorf("Pending() = %d, want 0: central accepted the report either way", got)
			}
		})
	}
}

// TestADeviceWithAnOwedReportSendsNoLaterOne pins the ordering across passes.
//
// Within a pass the sort keeps the order; across passes only this does. The
// case that needs it is Onboarded, which is not phase-ordered and clears
// central's confirmations for the device when it lands — delivered after the
// reports it precedes, it re-opens operations those reports had settled.
func TestADeviceWithAnOwedReportSendsNoLaterOne(t *testing.T) {
	central := &firstReportRefused{}
	q, err := report.New(report.Config{Client: central})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	q.Report(context.Background(), onboardedReport("dev-1"))
	q.Report(context.Background(), resultReport("dev-1", 1, "later"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = q.Run(ctx, 5*time.Millisecond)

	// The refused attempt, then the accepted one, then the report that was
	// waiting on it. Without the hold the result goes out in the pass that
	// refused the Onboarded, and central sees it first.
	onboardedSoFar := 0
	for _, got := range central.seen() {
		if got.HasOnboarded() {
			onboardedSoFar++
			continue
		}
		if got.HasResult() && onboardedSoFar < 2 {
			t.Fatalf("the later report was sent after %d onboarding attempts, while the earlier one was still owed", onboardedSoFar)
		}
	}
}

// firstReportRefused refuses whatever it is offered first and nothing after,
// so a test can watch what the queue sends while one report is still owed.
type firstReportRefused struct {
	mu       sync.Mutex
	received []*integrationv1.ReportRequest
	refused  bool
}

func (c *firstReportRefused) Report(
	_ context.Context, req *connect.Request[integrationv1.ReportRequest],
) (*connect.Response[integrationv1.ReportResponse], error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.received = append(c.received, req.Msg)
	if !c.refused {
		c.refused = true
		return nil, errors.New("central is unavailable")
	}
	return connect.NewResponse(&integrationv1.ReportResponse{}), nil
}

func (c *firstReportRefused) seen() []*integrationv1.ReportRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*integrationv1.ReportRequest(nil), c.received...)
}

// TestReportsGoOutOldestFirst pins the delivery order the drain documents:
// central applies reports in phase order, and a record that receives a
// release before the admission it follows discards it as stale.
func TestReportsGoOutOldestFirst(t *testing.T) {
	central := &centralFake{}
	q := newQueue(t, central, nil, 0)

	for _, device := range []string{"dev-1", "dev-2", "dev-3"} {
		q.Report(context.Background(), resultReport(device, 1, device))
	}
	drainOnce(t, q, central, 3)

	var order []string
	for _, got := range central.got() {
		order = append(order, got.GetDeviceId())
	}
	want := []string{"dev-1", "dev-2", "dev-3"}
	if !slices.Equal(order, want) {
		t.Errorf("delivery order = %v, want %v", order, want)
	}
}

// TestAReportArrivingMidSendIsNotConfirmedByTheSendItRaced covers the guard
// that only deletes the entry that was actually sent.
//
// A send takes a copy and releases the lock; a newer report for the same
// operation can be admitted while it is in flight. Deleting by key alone
// would drop that newer report on the older one's success, and central would
// never see it — the queue believing it had delivered something it had not.
func TestAReportArrivingMidSendIsNotConfirmedByTheSendItRaced(t *testing.T) {
	central := &centralFake{}
	q := newQueue(t, central, nil, 0)

	// Admitted while the first is being sent, from inside the send itself.
	central.duringReport = func() {
		q.Report(context.Background(), resultReport("dev-1", 1, "newer"))
	}
	q.Report(context.Background(), resultReport("dev-1", 1, "older"))

	drainOnce(t, q, central, 1)
	central.duringReport = nil

	if got := q.Pending(); got != 1 {
		t.Fatalf("Pending() = %d, want 1: the report admitted mid-send was dropped by the send it raced", got)
	}
}
