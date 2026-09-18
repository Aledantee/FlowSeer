package audit

import (
	"context"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventaccessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/event/access/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
)

// Deliverer durably delivers one DeviceOperationEvent. A caller (the
// mutation state machine) blocks on Emit's return before reporting the
// phase the event describes as released, per the audit-before-state rule.
type Deliverer interface {
	Emit(ctx context.Context, event *eventaccessv1.DeviceOperationEvent) error
}

// Clock supplies the current time, so a test can control occurred_at.
type Clock func() time.Time

// Common bundles the fields every DeviceOperationEvent carries regardless of
// its detail kind.
type Common struct {
	Device Device
	// Sequence is unset for a lane-level event with no single mutation in
	// scope (LaneFrozen); present and at least 1 otherwise.
	Sequence uint64
	// CorrelationIDs carries identifiers (idempotency key, trace id) without
	// their content, bounded at 8 pairs per the proto's max_pairs.
	CorrelationIDs map[string]string
}

// Device names the device a Common event set concerns, narrowed to what
// this package needs from model/inventory/v1.DeviceGlobalRef so callers do not
// have to import that package just to build one. Tenant scope is ambient
// and never named here, per the schema's own convention.
type Device struct {
	DeviceID string
}

func (d Device) ref() *inventoryv1.DeviceGlobalRef {
	local := &inventoryv1.DeviceLocalRef{}
	local.SetId(d.DeviceID)
	ref := &inventoryv1.DeviceGlobalRef{}
	ref.SetDevice(local)
	return ref
}

// correlationIDsMaxPairs, correlationIDsMaxKeyLen, and
// correlationIDsMaxValueLen mirror operation_event.proto's correlation_ids
// map constraint exactly, so a caller-supplied identifier that would fail
// protovalidate at the sink is bounded here instead — a build-time value
// that is merely long must never turn into an Emit failure that leaves a
// mutation's phase transition stuck, since the audit-before-release rule
// makes every Emit failure block the progress it records.
const (
	correlationIDsMaxPairs    = 8
	correlationIDsMaxKeyLen   = 64
	correlationIDsMaxValueLen = 128
)

// boundCorrelationIDs copies ids into a fresh map, truncating any key or
// value that exceeds the schema's bound and dropping pairs beyond the
// schema's max_pairs, so the caller's own map is never aliased or mutated.
// Returns nil for an empty or nil input, matching newEvent's "unset unless
// non-empty" convention for the field.
func boundCorrelationIDs(ids map[string]string) map[string]string {
	if len(ids) == 0 {
		return nil
	}
	bounded := make(map[string]string, min(len(ids), correlationIDsMaxPairs))
	for k, v := range ids {
		if len(bounded) >= correlationIDsMaxPairs {
			break
		}
		bounded[truncateUTF8(k, correlationIDsMaxKeyLen)] = truncateUTF8(v, correlationIDsMaxValueLen)
	}
	return bounded
}

// truncateUTF8 cuts s to at most maxBytes bytes, backing off to the nearest
// earlier rune boundary rather than a raw byte offset — a plain s[:n] can
// split a multi-byte rune in two, producing a string that fails Go's
// protobuf runtime's UTF-8 validation at marshal time, exactly the failure
// this bound exists to prevent.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}

func newEvent(clock Clock, common Common) *eventaccessv1.DeviceOperationEvent {
	event := &eventaccessv1.DeviceOperationEvent{}
	event.SetDevice(common.ref())
	event.SetEventId(uuid.NewString())
	event.SetOccurredAt(timestamppb.New(clock()))
	if common.Sequence != 0 {
		event.SetSequence(common.Sequence)
	}
	if bounded := boundCorrelationIDs(common.CorrelationIDs); bounded != nil {
		event.SetCorrelationIds(bounded)
	}
	return event
}

func (c Common) ref() *inventoryv1.DeviceGlobalRef { return c.Device.ref() }

// BuildPhaseTransitioned constructs the event for one mutation's phase
// change. from is the zero value's absence (unset) when the mutation was
// just recorded and has no earlier phase.
func BuildPhaseTransitioned(clock Clock, common Common, from, to accessv1.OperationPhase) *eventaccessv1.DeviceOperationEvent {
	detail := &eventaccessv1.PhaseTransitioned{}
	if from != accessv1.OperationPhase_OPERATION_PHASE_UNSPECIFIED {
		detail.SetFrom(from)
	}
	detail.SetTo(to)

	event := newEvent(clock, common)
	event.SetPhaseTransitioned(detail)
	return event
}

// BuildLaneBlocked constructs the event for the device's lane refusing the
// next mutation.
func BuildLaneBlocked(clock Clock, common Common, reason accessv1.BlockReason) *eventaccessv1.DeviceOperationEvent {
	detail := &eventaccessv1.LaneBlocked{}
	detail.SetReason(reason)

	event := newEvent(clock, common)
	event.SetLaneBlocked(detail)
	return event
}

// BuildLaneReleased constructs the event for the device's lane becoming free
// for the next mutation.
func BuildLaneReleased(clock Clock, common Common) *eventaccessv1.DeviceOperationEvent {
	event := newEvent(clock, common)
	event.SetLaneReleased(&eventaccessv1.LaneReleased{})
	return event
}

// BuildRouteSelected constructs the event for one route resolution.
func BuildRouteSelected(clock Clock, common Common, protocol inventoryv1.ManagementProtocol, fellThrough bool) *eventaccessv1.DeviceOperationEvent {
	detail := &eventaccessv1.RouteSelected{}
	detail.SetProtocol(protocol)
	if fellThrough {
		detail.SetFellThrough(true)
	}

	event := newEvent(clock, common)
	event.SetRouteSelected(detail)
	return event
}

// BuildDiscoveryCompleted constructs the event for identity and capability
// discovery finishing for a device.
func BuildDiscoveryCompleted(clock Clock, common Common, firmwareFingerprint string) *eventaccessv1.DeviceOperationEvent {
	detail := &eventaccessv1.DiscoveryCompleted{}
	detail.SetFirmwareFingerprint(firmwareFingerprint)

	event := newEvent(clock, common)
	event.SetDiscoveryCompleted(detail)
	return event
}

// BuildFirmwareEpochChanged constructs the event recording that a device's
// firmware fingerprint moved from previous to next. The mid-operation epoch
// check needs a fresh probe at observation time compared against an earlier
// probe's own output. The lane runs that probe and calls this; this module
// does not compare fingerprints itself.
func BuildFirmwareEpochChanged(clock Clock, common Common, previous, next string) *eventaccessv1.DeviceOperationEvent {
	detail := &eventaccessv1.FirmwareEpochChanged{}
	detail.SetPreviousFingerprint(previous)
	detail.SetNewFingerprint(next)

	event := newEvent(clock, common)
	event.SetFirmwareEpochChanged(detail)
	return event
}

// BuildRecoveryStarted constructs the event for a mutation whose effect
// could not be established entering recovery.
func BuildRecoveryStarted(clock Clock, common Common) *eventaccessv1.DeviceOperationEvent {
	event := newEvent(clock, common)
	event.SetRecoveryStarted(&eventaccessv1.RecoveryStarted{})
	return event
}

// BuildLaneFrozen constructs the event for the lane pausing because the
// hosting edge's own contact could not be confirmed.
func BuildLaneFrozen(clock Clock, common Common) *eventaccessv1.DeviceOperationEvent {
	event := newEvent(clock, common)
	event.SetLaneFrozen(&eventaccessv1.LaneFrozen{})
	return event
}
