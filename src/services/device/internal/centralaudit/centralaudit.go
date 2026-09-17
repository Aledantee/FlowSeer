// Package centralaudit writes the audit records central originates itself.
//
// Most of the audit stream comes from an edge, which delivers its records over
// AuditService and central stores them unchanged. A few things happen at
// central with no edge involved: central rejects a dispatch the edge refused,
// and — once the poll lands — central detects drift. Those need a record for
// the same reason the edge's do, and this is where central writes them, on the
// same stream and under the same duplicate window.
package centralaudit

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

// ErrCodePublish is a failure to place a central-originated record on the
// audit stream. The caller has already written the state the record describes,
// so this is a lost trace, not a lost decision.
var ErrCodePublish = errs.NewCode("centralaudit/publish")

// Attribute keys on a central-originated record. They are contract: an
// auditor reads them, so they are named here once rather than spelled at each
// call site.
const (
	// AttrDisposition is how the mutation ended, as the Disposition enum's own
	// name.
	AttrDisposition = "disposition"
	// AttrRefusalCode is the code the edge refused the dispatch with.
	AttrRefusalCode = "refusal_code"
	// AttrExpected is the description central expected on a drifted interface.
	AttrExpected = "expected"
	// AttrObserved is the description the device actually carried.
	AttrObserved = "observed"
)

// Publisher places one record on the audit stream under a message id. The
// JetStream publisher central serves AuditService with implements it.
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string) error
}

// Emitter writes central's own audit records. Safe for concurrent use.
type Emitter struct {
	publisher Publisher
	tenant    string
	clock     func() time.Time
}

// New builds an emitter publishing to tenant's audit subjects. A nil clock
// uses the wall clock.
func New(publisher Publisher, tenant string, clock func() time.Time) *Emitter {
	if clock == nil {
		clock = time.Now
	}
	return &Emitter{publisher: publisher, tenant: tenant, clock: clock}
}

// DispatchRejected records central disposing a mutation because the edge
// refused its dispatch for a reason that cannot be retried.
//
// It exists so the rejection survives the lane closing. An operator who
// resubmits the idempotency key afterwards reads the disposition from the
// record, but the reason — the code the edge refused with, a changed firmware
// epoch first among them — lives nowhere else, and "the sequence ended" with
// no cause is the answer an incident starts from rather than ends at.
func (e *Emitter) DispatchRejected(ctx context.Context, device *inventoryv1.DeviceGlobalRef, state *accessv1.MutationState, from accessv1.OperationPhase, refusalCode string) error {
	detail := &eventv1.PhaseTransitioned{}
	if from != accessv1.OperationPhase_OPERATION_PHASE_UNSPECIFIED {
		detail.SetFrom(from)
	}
	detail.SetTo(state.GetPhase())

	event := &eventv1.DeviceOperationEvent{}
	event.SetDevice(device)
	event.SetEventId(uuid.NewString())
	event.SetSequence(state.GetSequence())
	event.SetOccurredAt(timestamppb.New(e.clock()))
	event.SetAttributes(map[string]*structpb.Value{
		AttrDisposition: structpb.NewStringValue(state.GetDisposition().String()),
		AttrRefusalCode: structpb.NewStringValue(refusalCode),
	})
	event.SetPhaseTransitioned(detail)

	return e.emit(ctx, device.GetDevice().GetId(), event)
}

// DriftDetected records central finding a managed interface carrying
// something other than what central expects, with no mutation of its own in
// flight to explain it.
//
// The record carries no sequence: at the moment of detection there is no
// mutation this is about — the reconciliation intent central admits next gets
// its own sequence, and this is the observation that caused it. What the
// record has to survive is the difference itself, so both values ride as
// attributes: an auditor reading "drift on ethernet 1/1/1" and nothing else
// cannot tell a typo from a device someone else is administering.
func (e *Emitter) DriftDetected(ctx context.Context, device *inventoryv1.DeviceGlobalRef, iface, expected, observed string) error {
	detail := &eventv1.DriftDetected{}
	detail.SetFieldName(iface)

	event := &eventv1.DeviceOperationEvent{}
	event.SetDevice(device)
	event.SetEventId(uuid.NewString())
	event.SetOccurredAt(timestamppb.New(e.clock()))
	event.SetAttributes(map[string]*structpb.Value{
		AttrExpected: structpb.NewStringValue(expected),
		AttrObserved: structpb.NewStringValue(observed),
	})
	event.SetDriftDetected(detail)

	return e.emit(ctx, device.GetDevice().GetId(), event)
}

func (e *Emitter) emit(ctx context.Context, deviceID string, event *eventv1.DeviceOperationEvent) error {
	data, err := proto.Marshal(event)
	if err != nil {
		return errs.From(err).Code(ErrCodePublish).Attr("device", deviceID).Msg("marshal audit event")
	}
	if err := e.publisher.Publish(ctx, edgebus.AuditSubject(e.tenant, deviceID), data, event.GetEventId()); err != nil {
		return errs.From(err).Code(ErrCodePublish).Attr("device", deviceID).
			Attr("event", event.GetEventId()).Msg("publish audit event")
	}
	return nil
}
