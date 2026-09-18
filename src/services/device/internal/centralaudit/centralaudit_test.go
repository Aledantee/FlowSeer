package centralaudit_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	eventaccessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/access/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
	"go.aledante.io/FlowSeer/src/services/device/internal/centralaudit"
)

const deviceID = "0192e6a0-0000-7000-8000-0000000000d1"

type publisher struct {
	subject string
	msgID   string
	data    []byte
	err     error
}

func (p *publisher) Publish(_ context.Context, subject string, data []byte, msgID string) error {
	p.subject, p.data, p.msgID = subject, data, msgID
	return p.err
}

func deviceRef() *inventoryv1.DeviceGlobalRef {
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(deviceID)
	ref := &inventoryv1.DeviceGlobalRef{}
	ref.SetDevice(local)
	return ref
}

func rejected(sequence uint64) *accessv1.MutationState {
	state := &accessv1.MutationState{}
	state.SetSequence(sequence)
	state.SetPhase(accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED)
	state.SetDisposition(accessv1.Disposition_DISPOSITION_REJECTED)
	return state
}

func TestDispatchRejectedRecordsTheDispositionAndTheReason(t *testing.T) {
	pub := &publisher{}
	clock := func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
	emitter := centralaudit.New(pub, edgebus.DefaultTenant, clock)

	err := emitter.DispatchRejected(context.Background(), deviceRef(), rejected(4),
		accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED, "mutation/firmware-epoch")
	if err != nil {
		t.Fatalf("DispatchRejected: %v", err)
	}

	if want := edgebus.AuditSubject(edgebus.DefaultTenant, deviceID); pub.subject != want {
		t.Errorf("subject = %q, want %q", pub.subject, want)
	}
	event := &eventaccessv1.DeviceOperationEvent{}
	if err := proto.Unmarshal(pub.data, event); err != nil {
		t.Fatalf("unmarshal published event: %v", err)
	}
	if err := protovalidate.Validate(event); err != nil {
		t.Errorf("the published event fails its schema rules: %v", err)
	}
	if pub.msgID != event.GetEventId() {
		t.Errorf("message id = %q, want the event id %q", pub.msgID, event.GetEventId())
	}
	if event.GetSequence() != 4 {
		t.Errorf("sequence = %d, want 4", event.GetSequence())
	}
	if got := event.GetPhaseTransitioned().GetTo(); got != accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED {
		t.Errorf("to = %v, want acknowledged", got)
	}
	if got := event.GetAttributes()[centralaudit.AttrDisposition].GetStringValue(); got != "DISPOSITION_REJECTED" {
		t.Errorf("disposition attribute = %q, want the rejected disposition", got)
	}
	if got := event.GetAttributes()[centralaudit.AttrRefusalCode].GetStringValue(); got != "mutation/firmware-epoch" {
		t.Errorf("refusal code attribute = %q, want the refusing code", got)
	}
}

// A record central could not place is reported to its caller. Swallowing it
// would leave a rejection with no trace and nothing saying so.
func TestDispatchRejectedReportsAFailedPublish(t *testing.T) {
	pub := &publisher{err: errors.New("stream refused the publish")}
	emitter := centralaudit.New(pub, edgebus.DefaultTenant, nil)

	err := emitter.DispatchRejected(context.Background(), deviceRef(), rejected(4),
		accessv1.OperationPhase_OPERATION_PHASE_ADMITTED, "mutation/firmware-epoch")
	if code, _ := errs.CodeOf(err); code != centralaudit.ErrCodePublish {
		t.Fatalf("error code = %v, want a publish failure", code)
	}
}

// A drift record has to carry both values. "Drift on ethernet 1/1/1" and
// nothing else does not tell an auditor a typo from a device someone else is
// administering.
func TestDriftDetectedRecordsBothValues(t *testing.T) {
	pub := &publisher{}
	emitter := centralaudit.New(pub, edgebus.DefaultTenant, nil)

	err := emitter.DriftDetected(context.Background(), deviceRef(), "ethernet 1/1/1", "uplink to core", "temporary")
	if err != nil {
		t.Fatalf("DriftDetected: %v", err)
	}

	event := &eventaccessv1.DeviceOperationEvent{}
	if err := proto.Unmarshal(pub.data, event); err != nil {
		t.Fatalf("unmarshal published event: %v", err)
	}
	if err := protovalidate.Validate(event); err != nil {
		t.Errorf("the published event fails its schema rules: %v", err)
	}
	if got := event.GetDriftDetected().GetFieldName(); got != "ethernet 1/1/1" {
		t.Errorf("field name = %q, want the interface", got)
	}
	if got := event.GetAttributes()[centralaudit.AttrExpected].GetStringValue(); got != "uplink to core" {
		t.Errorf("expected attribute = %q", got)
	}
	if got := event.GetAttributes()[centralaudit.AttrObserved].GetStringValue(); got != "temporary" {
		t.Errorf("observed attribute = %q", got)
	}
	if event.HasSequence() {
		t.Error("a detection carries a sequence; no mutation exists yet at that moment")
	}
}
