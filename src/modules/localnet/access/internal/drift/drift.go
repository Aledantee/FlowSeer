package drift

import (
	"github.com/google/uuid"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/capability/interfaces"
)

// Outcome is what [Evaluate] decided.
type Outcome struct {
	// Drifted reports whether the observation differs from lastIntent. When
	// false, every other field is the zero value.
	Drifted bool
	// Blocked reports the OPERATOR_MANAGED case: the lane should block with
	// BLOCK_REASON_DESYNCHRONIZED and wait for an operator resolution.
	Blocked bool
	// Reconcile carries the AUTHORITATIVE case's synthesized intent, to be
	// admitted at the caller's own high priority. Nil unless Drifted is
	// true and Blocked is false.
	Reconcile *accessv1.MutationIntent
}

// Evaluate compares observed against lastIntent — the last acknowledged
// mutation's intent, standing in for central's expected state, per decision
// 6. inFlight, if true, means an admitted mutation on this device already
// explains a difference; Evaluate reports no drift in that case, since a
// mutation's own observation is not drift. A PARTIAL observation never
// drifts either: it carries no authority, per
// [interfaces.ConflictingReads]'s doc comment, so drift detection defers
// the same way rather than acting on an incomplete read.
func Evaluate(observed *accessv1.InterfaceObservation, lastIntent *accessv1.InterfaceDescriptionChange, inFlight bool, mode inventoryv1.DeviceManagementMode) Outcome {
	if inFlight {
		return Outcome{}
	}
	if observed.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE {
		return Outcome{}
	}
	if lastIntent == nil {
		return Outcome{}
	}
	if interfaces.DescriptionApplied(observed, lastIntent) {
		return Outcome{}
	}

	// Only an explicit AUTHORITATIVE mode auto-reconciles; every other
	// value, including UNSPECIFIED (a device whose management mode was
	// never configured), takes the OPERATOR_MANAGED path and blocks for a
	// human decision — the safer default when a mode was never chosen,
	// since decision 6 states both modes as user-directed and neither as
	// an implicit default.
	if mode == inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE {
		return Outcome{Drifted: true, Reconcile: reconciliationIntent(observed, lastIntent)}
	}

	return Outcome{Drifted: true, Blocked: true}
}

// reconciliationIntent builds a SystemActor{RECONCILIATION} intent
// restoring lastIntent's value on the interface observed named. This
// package has no device ref or access-policy handle to fill Device and
// AccessPolicy with, so the caller (the lane orchestrator, which admits
// this intent) must set both before submission; only the fields this
// package can honestly derive from its own inputs are set here.
func reconciliationIntent(observed *accessv1.InterfaceObservation, lastIntent *accessv1.InterfaceDescriptionChange) *accessv1.MutationIntent {
	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName(observed.GetInterfaceName())
	change.SetDescription(lastIntent.GetDescription())

	system := &accessv1.SystemActor{}
	system.SetReason(accessv1.SystemReason_SYSTEM_REASON_RECONCILIATION)
	actor := &accessv1.Actor{}
	actor.SetSystem(system)

	intent := &accessv1.MutationIntent{}
	intent.SetIdempotencyKey(uuid.NewString())
	intent.SetActor(actor)
	intent.SetInterfaceDescription(change)

	if prov := observed.GetProvenance(); prov != nil {
		intent.SetExpectedFirmwareFingerprint(prov.GetFirmwareFingerprint())
	}

	return intent
}
