package drift_test

import (
	"testing"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/drift"
)

func observation(description string, complete bool) *accessv1.InterfaceObservation {
	obs := &accessv1.InterfaceObservation{}
	obs.SetInterfaceName("ethernet 1/1/1")
	if !complete {
		obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_PARTIAL)
		return obs
	}
	obs.SetDescription(description)
	obs.SetAdminStatus(interfacev1.AdminStatus_ADMIN_STATUS_UP)
	obs.SetOperStatus(interfacev1.OperStatus_OPER_STATUS_UP)
	obs.SetCompleteness(accessv1.Completeness_COMPLETENESS_COMPLETE)
	return obs
}

func lastIntent() *accessv1.InterfaceDescriptionChange {
	change := &accessv1.InterfaceDescriptionChange{}
	change.SetInterfaceName("ethernet 1/1/1")
	change.SetDescription("uplink to core")
	return change
}

func TestNoDriftWhenObservationMatchesLastIntent(t *testing.T) {
	outcome := drift.Evaluate(observation("uplink to core", true), lastIntent(), false,
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	if outcome.Drifted {
		t.Fatal("Evaluate() reported drift when the observation matches the last intent")
	}
}

func TestOperatorManagedBlocksOnDrift(t *testing.T) {
	outcome := drift.Evaluate(observation("manual edit", true), lastIntent(), false,
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	if !outcome.Drifted {
		t.Fatal("Evaluate() reported no drift")
	}
	if !outcome.Blocked {
		t.Error("OPERATOR_MANAGED should block, not auto-admit")
	}
	if outcome.Reconcile != nil {
		t.Error("OPERATOR_MANAGED should not synthesize a reconciliation intent")
	}
}

func TestAuthoritativeAutoAdmitsReconciliation(t *testing.T) {
	outcome := drift.Evaluate(observation("manual edit", true), lastIntent(), false,
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE)
	if !outcome.Drifted {
		t.Fatal("Evaluate() reported no drift")
	}
	if outcome.Blocked {
		t.Error("AUTHORITATIVE should not block")
	}
	if outcome.Reconcile == nil {
		t.Fatal("AUTHORITATIVE should synthesize a reconciliation intent")
	}
	if got := outcome.Reconcile.GetActor().GetSystem().GetReason(); got != accessv1.SystemReason_SYSTEM_REASON_RECONCILIATION {
		t.Errorf("reconciliation actor reason = %v, want SYSTEM_REASON_RECONCILIATION", got)
	}
	if got := outcome.Reconcile.GetInterfaceDescription().GetDescription(); got != "uplink to core" {
		t.Errorf("reconciliation intent restores %q, want %q", got, "uplink to core")
	}
	if outcome.Reconcile.GetIdempotencyKey() == "" {
		t.Error("reconciliation intent has no idempotency key")
	}
}

func TestInFlightMutationSuppressesDrift(t *testing.T) {
	outcome := drift.Evaluate(observation("manual edit", true), lastIntent(), true,
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	if outcome.Drifted {
		t.Fatal("Evaluate() reported drift while a mutation is in flight explaining the difference")
	}
}

func TestPartialObservationNeverDrifts(t *testing.T) {
	outcome := drift.Evaluate(observation("", false), lastIntent(), false,
		inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED)
	if outcome.Drifted {
		t.Fatal("Evaluate() reported drift from a PARTIAL observation, which carries no authority")
	}
}
