package evidence_test

import (
	"testing"
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/modules/localnet/access/internal/evidence"
)

const (
	deviceA = "11111111-1111-1111-1111-111111111111"
	fpA     = "fp-a"
	fpB     = "fp-b"
)

func TestConsultReturnsNoPolicyWhenKindUnconfigured(t *testing.T) {
	store := evidence.NewStore(evidence.Policy{})

	_, ok, err := store.Consult(deviceA, fpA, evidence.KindInterfaceDescriptionChange, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, time.Now())
	if ok {
		t.Fatal("expected ok=false for an unconfigured kind")
	}
	if code, _ := errs.CodeOf(err); code != evidence.ErrCodeNoPolicy {
		t.Fatalf("expected ErrCodeNoPolicy, got %v", err)
	}
}

func TestRecordReturnsNoPolicyWhenKindUnconfigured(t *testing.T) {
	store := evidence.NewStore(evidence.Policy{})

	err := store.Record(deviceA, fpA, evidence.KindInterfaceDescriptionChange, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, accessv1.Completeness_COMPLETENESS_COMPLETE, time.Now())
	if code, _ := errs.CodeOf(err); code != evidence.ErrCodeNoPolicy {
		t.Fatalf("expected ErrCodeNoPolicy, got %v", err)
	}
}

func TestConsultHonorsLifetimeExpiry(t *testing.T) {
	store := evidence.NewStore(evidence.Policy{evidence.KindInterfaceRead: 2 * time.Minute})
	t0 := time.Now()

	if err := store.Record(deviceA, fpA, evidence.KindInterfaceRead, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, accessv1.Completeness_COMPLETENESS_COMPLETE, t0); err != nil {
		t.Fatalf("Record: %v", err)
	}

	if _, ok, err := store.Consult(deviceA, fpA, evidence.KindInterfaceRead, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, t0.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("expected fresh evidence to be usable, got ok=%v err=%v", ok, err)
	}

	if _, ok, err := store.Consult(deviceA, fpA, evidence.KindInterfaceRead, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, t0.Add(3*time.Minute)); err != nil || ok {
		t.Fatalf("expected stale evidence to be unusable, got ok=%v err=%v", ok, err)
	}
}

func TestInvalidateFingerprintClearsEveryRouteForDevice(t *testing.T) {
	store := evidence.NewStore(evidence.Policy{
		evidence.KindInterfaceRead:              time.Hour,
		evidence.KindInterfaceDescriptionChange: time.Hour,
	})
	now := time.Now()

	if err := store.Record(deviceA, fpA, evidence.KindInterfaceRead, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, accessv1.Completeness_COMPLETENESS_COMPLETE, now); err != nil {
		t.Fatalf("Record SNMP: %v", err)
	}
	if err := store.Record(deviceA, fpA, evidence.KindInterfaceDescriptionChange, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH, accessv1.Completeness_COMPLETENESS_COMPLETE, now); err != nil {
		t.Fatalf("Record SSH: %v", err)
	}

	store.InvalidateFingerprint(deviceA, fpB)

	if _, ok, _ := store.Consult(deviceA, fpA, evidence.KindInterfaceRead, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, now); ok {
		t.Fatal("expected SNMP evidence under the old fingerprint to be invalidated")
	}
	if _, ok, _ := store.Consult(deviceA, fpA, evidence.KindInterfaceDescriptionChange, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH, now); ok {
		t.Fatal("expected SSH evidence under the old fingerprint to be invalidated")
	}
}

func TestInvalidateFingerprintKeepsCurrentFingerprintEvidence(t *testing.T) {
	store := evidence.NewStore(evidence.Policy{evidence.KindInterfaceRead: time.Hour})
	now := time.Now()

	if err := store.Record(deviceA, fpB, evidence.KindInterfaceRead, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, accessv1.Completeness_COMPLETENESS_COMPLETE, now); err != nil {
		t.Fatalf("Record: %v", err)
	}

	store.InvalidateFingerprint(deviceA, fpB)

	if _, ok, err := store.Consult(deviceA, fpB, evidence.KindInterfaceRead, inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SNMP, now); err != nil || !ok {
		t.Fatalf("expected current-fingerprint evidence to survive, got ok=%v err=%v", ok, err)
	}
}
