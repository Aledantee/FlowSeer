package report_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	connect "connectrpc.com/connect"

	auditv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/edge/audit/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/access/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/report"
)

type auditFake struct {
	mu       sync.Mutex
	received []*accessv1.DeviceOperationEvent
	err      error
	// blocked, when set, holds Deliver open so a test can observe that the
	// caller is waiting rather than proceeding.
	blocked chan struct{}
	entered chan struct{}
}

func (a *auditFake) Deliver(
	_ context.Context, req *connect.Request[auditv1.DeliverRequest],
) (*connect.Response[auditv1.DeliverResponse], error) {
	a.mu.Lock()
	gate, entered := a.blocked, a.entered
	a.received = append(a.received, req.Msg.GetEvent())
	err := a.err
	a.mu.Unlock()

	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if gate != nil {
		<-gate
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&auditv1.DeliverResponse{}), nil
}

func (a *auditFake) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.received)
}

func operationEvent(id string) *accessv1.DeviceOperationEvent {
	event := &accessv1.DeviceOperationEvent{}
	event.SetEventId(id)
	return event
}

// TestAFailedDeliveryIsReportedToTheLane is the whole contract. The lane
// holds its own state transition until Emit returns, and treats an error as
// "the record is not held" — so a deliverer that swallowed the failure would
// tell the lane a record was written when it was not, and the phase it
// guards would have no account of it anywhere.
func TestAFailedDeliveryIsReportedToTheLane(t *testing.T) {
	audit := &auditFake{err: errors.New("the audit stream refused the record")}
	deliverer := report.NewDeliverer(audit, "edge-1")

	err := deliverer.Emit(context.Background(), operationEvent("event-1"))
	if err == nil {
		t.Fatal("Emit() error = nil, want the refusal surfaced")
	}
	if code, _ := errs.CodeOf(err); code != report.ErrCodeDeliver {
		t.Errorf("code = %v, want %v", code, report.ErrCodeDeliver)
	}
}

// TestEmitBlocksUntilCentralAnswers is what separates this from the report
// queue. A record that was merely enqueued would let the lane release the
// state the record describes before anything durable held it.
func TestEmitBlocksUntilCentralAnswers(t *testing.T) {
	audit := &auditFake{blocked: make(chan struct{}), entered: make(chan struct{}, 1)}
	deliverer := report.NewDeliverer(audit, "edge-1")

	returned := make(chan error, 1)
	go func() { returned <- deliverer.Emit(context.Background(), operationEvent("event-1")) }()

	<-audit.entered
	select {
	case <-returned:
		t.Fatal("Emit() returned while central was still deciding; the lane would release state nothing holds")
	default:
	}

	close(audit.blocked)
	if err := <-returned; err != nil {
		t.Fatalf("Emit() error: %v", err)
	}
	if audit.count() != 1 {
		t.Errorf("central saw %d records, want 1", audit.count())
	}
}

// TestAnAbsentRecordIsNotSuccess covers the one way this deliverer could tell
// the lane a record is durable without anything having been written. The lane
// moves its phase on a nil error, so a nil record must not produce one.
func TestAnAbsentRecordIsNotSuccess(t *testing.T) {
	client := &auditFake{}
	deliverer := report.NewDeliverer(client, "edge-1")

	if err := deliverer.Emit(context.Background(), nil); err == nil {
		t.Fatal("Emit(nil) returned no error; the lane would release a phase with no record behind it")
	}
	if got := client.count(); got != 0 {
		t.Errorf("Deliver called %d times, want 0", got)
	}
}
