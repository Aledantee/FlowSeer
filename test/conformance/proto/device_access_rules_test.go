package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
)

const (
	idempotencyKey = "0192e6a0-0000-7000-8000-00000000a001"
	fingerprint    = "ICX7150-24P SPS10010g"
)

func operatorActor() *accessv1.Actor {
	return accessv1.Actor_builder{
		Operator: accessv1.OperatorRef_builder{Subject: proto.String("zitadel|2837")}.Build(),
	}.Build()
}

func descriptionChange(name, description string) *accessv1.InterfaceDescriptionChange {
	return accessv1.InterfaceDescriptionChange_builder{
		InterfaceName: proto.String(name),
		Description:   proto.String(description),
	}.Build()
}

func mutationIntent() accessv1.MutationIntent_builder {
	return accessv1.MutationIntent_builder{
		Device:                      deviceRef(deviceID),
		IdempotencyKey:              proto.String(idempotencyKey),
		Actor:                       operatorActor(),
		AccessPolicy:                accessPolicyHandle("icx7150-lab", 3),
		ExpectedFirmwareFingerprint: proto.String(fingerprint),
		InterfaceDescription:        descriptionChange("ethernet 1/1/1", "uplink to core"),
	}
}

func mutationState(phase accessv1.OperationPhase) accessv1.MutationState_builder {
	state := accessv1.MutationState_builder{
		Intent:          mutationIntent().Build(),
		Phase:           phase.Enum(),
		ResponsibleEdge: edgeRef(),
	}
	if phase != accessv1.OperationPhase_OPERATION_PHASE_INTENT_RECORDED {
		state.Sequence = proto.Uint64(42)
	}

	return state
}

func interfaceObservation() accessv1.InterfaceObservation_builder {
	return accessv1.InterfaceObservation_builder{
		InterfaceName: proto.String("ethernet 1/1/1"),
		Description:   proto.String("uplink to core"),
		AdminStatus:   interfacev1.AdminStatus_ADMIN_STATUS_UP.Enum(),
		OperStatus:    interfacev1.OperStatus_OPER_STATUS_UP.Enum(),
		Provenance:    provenance().Build(),
		Completeness:  accessv1.Completeness_COMPLETENESS_COMPLETE.Enum(),
	}
}

func typedRead(name string) *accessv1.TypedRead {
	return accessv1.TypedRead_builder{
		AccessPolicy: accessPolicyHandle("icx7150-lab", 3),
		Interface:    interfaceReadIntent(name),
	}.Build()
}

func interfaceReadIntent(name string) *accessv1.InterfaceReadIntent {
	return accessv1.InterfaceReadIntent_builder{InterfaceName: proto.String(name)}.Build()
}

func TestInterfaceReadIntentRules(t *testing.T) {
	tests := []validationCase{
		{name: "named interface is valid", message: interfaceReadIntent("ethernet 1/1/1"), wantValid: true},
		{name: "empty interface name is rejected", message: interfaceReadIntent("")},
	}

	runValidationCases(t, tests)
}

func TestTypedReadRules(t *testing.T) {
	tests := []validationCase{
		{
			name:      "interface read is valid",
			message:   typedRead("ethernet 1/1/1"),
			wantValid: true,
		},
		{name: "read without an arm is rejected", message: accessv1.TypedRead_builder{AccessPolicy: accessPolicyHandle("icx7150-lab", 3)}.Build()},
		{name: "read without an access policy is rejected", message: accessv1.TypedRead_builder{Interface: interfaceReadIntent("ethernet 1/1/1")}.Build()},
	}

	runValidationCases(t, tests)
}

func TestActorRules(t *testing.T) {
	tests := []validationCase{
		{name: "operator actor is valid", message: operatorActor(), wantValid: true},
		{
			name: "system actor is valid",
			message: accessv1.Actor_builder{
				System: accessv1.SystemActor_builder{
					Reason: accessv1.SystemReason_SYSTEM_REASON_RECONCILIATION.Enum(),
				}.Build(),
			}.Build(),
			wantValid: true,
		},
		{name: "actor without a principal is rejected", message: accessv1.Actor_builder{}.Build()},
		{
			name: "operator without a subject is rejected",
			message: accessv1.Actor_builder{
				Operator: accessv1.OperatorRef_builder{}.Build(),
			}.Build(),
		},
		{
			name: "system actor without a reason is rejected",
			message: accessv1.Actor_builder{
				System: accessv1.SystemActor_builder{}.Build(),
			}.Build(),
		},
	}

	runValidationCases(t, tests)
}

func TestInterfaceDescriptionChangeRules(t *testing.T) {
	tests := []validationCase{
		{name: "printable description is valid", message: descriptionChange("ethernet 1/1/1", "uplink to core"), wantValid: true},
		{name: "empty description clears and is valid", message: descriptionChange("ethernet 1/1/1", ""), wantValid: true},
		{name: "control character is rejected", message: descriptionChange("ethernet 1/1/1", "up\x1blink")},
		{name: "newline is rejected", message: descriptionChange("ethernet 1/1/1", "up\nlink")},
		{name: "non-ascii is rejected", message: descriptionChange("ethernet 1/1/1", "Verknüpfung")},
		{name: "65 characters are rejected", message: descriptionChange("ethernet 1/1/1", "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklm")},
		{name: "empty interface name is rejected", message: descriptionChange("", "uplink")},
		{
			name: "absent description is rejected",
			message: accessv1.InterfaceDescriptionChange_builder{
				InterfaceName: proto.String("ethernet 1/1/1"),
			}.Build(),
		},
	}

	runValidationCases(t, tests)
}

func TestInterfaceObservationRules(t *testing.T) {
	noProvenance := interfaceObservation()
	noProvenance.Provenance = nil

	partial := interfaceObservation()
	partial.Completeness = accessv1.Completeness_COMPLETENESS_PARTIAL.Enum()

	noCompleteness := interfaceObservation()
	noCompleteness.Completeness = nil

	noAdmin := interfaceObservation()
	noAdmin.AdminStatus = nil

	unspecifiedOper := interfaceObservation()
	unspecifiedOper.OperStatus = interfacev1.OperStatus_OPER_STATUS_UNSPECIFIED.Enum()

	noEdge := interfaceObservation()
	withoutEdge := provenance()
	withoutEdge.Edge = nil
	noEdge.Provenance = withoutEdge.Build()

	noFirmwareFingerprint := interfaceObservation()
	withoutFingerprint := provenance()
	withoutFingerprint.FirmwareFingerprint = nil
	noFirmwareFingerprint.Provenance = withoutFingerprint.Build()

	badProvenance := interfaceObservation()
	incomplete := provenance()
	incomplete.Binding = nil
	badProvenance.Provenance = incomplete.Build()

	emptyPartial := accessv1.InterfaceObservation_builder{
		InterfaceName: proto.String("ethernet 1/1/1"),
		Completeness:  accessv1.Completeness_COMPLETENESS_PARTIAL.Enum(),
	}

	tests := []validationCase{
		{name: "complete observation is valid", message: interfaceObservation().Build(), wantValid: true},
		{name: "partial observation is valid", message: partial.Build(), wantValid: true},
		{
			name:      "partial observation with no compared field set is valid",
			message:   emptyPartial.Build(),
			wantValid: true,
		},
		{name: "provenance is required", message: noProvenance.Build()},
		{name: "completeness is required", message: noCompleteness.Build()},
		{name: "admin status is required", message: noAdmin.Build()},
		{name: "unspecified oper status is rejected", message: unspecifiedOper.Build()},
		{name: "observation without an edge is rejected", message: noEdge.Build()},
		{name: "observation without a firmware fingerprint is rejected", message: noFirmwareFingerprint.Build()},
		{name: "invalid provenance fails the observation", message: badProvenance.Build()},
	}

	runValidationCases(t, tests)
}

func TestMutationIntentRules(t *testing.T) {
	noChange := mutationIntent()
	noChange.InterfaceDescription = nil

	noKey := mutationIntent()
	noKey.IdempotencyKey = nil

	badKey := mutationIntent()
	badKey.IdempotencyKey = proto.String("order-42")

	noActor := mutationIntent()
	noActor.Actor = nil

	noPolicy := mutationIntent()
	noPolicy.AccessPolicy = nil

	noFingerprint := mutationIntent()
	noFingerprint.ExpectedFirmwareFingerprint = nil

	noDevice := mutationIntent()
	noDevice.Device = nil

	tests := []validationCase{
		{name: "complete intent is valid", message: mutationIntent().Build(), wantValid: true},
		{name: "intent without a change is rejected", message: noChange.Build()},
		{name: "idempotency key is required", message: noKey.Build()},
		{name: "idempotency key must be a uuid", message: badKey.Build()},
		{name: "actor is required", message: noActor.Build()},
		{name: "access policy is required", message: noPolicy.Build()},
		{name: "expected fingerprint is required", message: noFingerprint.Build()},
		{name: "device is required", message: noDevice.Build()},
	}

	runValidationCases(t, tests)
}

func TestMutationStateDispositionRule(t *testing.T) {
	terminal := map[accessv1.OperationPhase]bool{
		accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED: true,
		accessv1.OperationPhase_OPERATION_PHASE_RELEASED:     true,
		accessv1.OperationPhase_OPERATION_PHASE_ABANDONED:    true,
	}

	var tests []validationCase
	for phase := accessv1.OperationPhase(1); phase <= accessv1.OperationPhase_OPERATION_PHASE_ABANDONED; phase++ {
		withDisposition := mutationState(phase)
		withDisposition.Disposition = accessv1.Disposition_DISPOSITION_VERIFIED.Enum()

		tests = append(tests,
			validationCase{name: phase.String() + " without disposition", message: mutationState(phase).Build(), wantValid: !terminal[phase]},
			validationCase{name: phase.String() + " with disposition", message: withDisposition.Build(), wantValid: terminal[phase]},
		)
	}

	runValidationCases(t, tests)
}

func TestMutationStateRules(t *testing.T) {
	held := mutationState(accessv1.OperationPhase_OPERATION_PHASE_ABANDONED)
	held.Disposition = accessv1.Disposition_DISPOSITION_INDETERMINATE_ABANDONED.Enum()
	held.BlockReason = accessv1.BlockReason_BLOCK_REASON_RECOVERY_HOLD.Enum()
	held.BlockedSince = timestamppb.New(edgeIssuedAt)

	reasonWithoutSince := mutationState(accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
	reasonWithoutSince.BlockReason = accessv1.BlockReason_BLOCK_REASON_UNACKNOWLEDGED.Enum()

	sinceWithoutReason := mutationState(accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
	sinceWithoutReason.BlockedSince = timestamppb.New(edgeIssuedAt)

	unspecifiedReason := mutationState(accessv1.OperationPhase_OPERATION_PHASE_VERIFIED)
	unspecifiedReason.BlockReason = accessv1.BlockReason_BLOCK_REASON_UNSPECIFIED.Enum()
	unspecifiedReason.BlockedSince = timestamppb.New(edgeIssuedAt)

	sequenceZero := mutationState(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	sequenceZero.Sequence = proto.Uint64(0)

	noSequence := mutationState(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	noSequence.Sequence = nil

	intentRecorded := mutationState(accessv1.OperationPhase_OPERATION_PHASE_INTENT_RECORDED)

	intentRecordedWithSequence := mutationState(accessv1.OperationPhase_OPERATION_PHASE_INTENT_RECORDED)
	intentRecordedWithSequence.Sequence = proto.Uint64(1)

	noPhase := mutationState(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	noPhase.Phase = nil

	noEdge := mutationState(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	noEdge.ResponsibleEdge = nil

	noIntent := mutationState(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	noIntent.Intent = nil

	badIntent := mutationState(accessv1.OperationPhase_OPERATION_PHASE_ADMITTED)
	partialIntent := mutationIntent()
	partialIntent.Actor = nil
	badIntent.Intent = partialIntent.Build()

	tests := []validationCase{
		{name: "abandoned state in recovery hold is valid", message: held.Build(), wantValid: true},
		{name: "block reason without blocked_since is rejected", message: reasonWithoutSince.Build()},
		{name: "blocked_since without block reason is rejected", message: sinceWithoutReason.Build()},
		{name: "unspecified block reason is rejected", message: unspecifiedReason.Build()},
		{name: "sequence zero is rejected", message: sequenceZero.Build()},
		{name: "sequence is required once admitted", message: noSequence.Build()},
		{name: "intent recorded without a sequence is valid", message: intentRecorded.Build(), wantValid: true},
		{name: "intent recorded with a sequence is rejected", message: intentRecordedWithSequence.Build()},
		{name: "phase is required", message: noPhase.Build()},
		{name: "responsible edge is required", message: noEdge.Build()},
		{name: "intent is required", message: noIntent.Build()},
		{name: "invalid intent fails the state", message: badIntent.Build()},
	}

	runValidationCases(t, tests)
}

// TestMutationStatePhaseNumbersMatchDocs pins the phase numbers every CEL
// rule that compares against a raw ordinal expects, so renumbering the enum
// cannot silently move the terminal set (mutation_state.disposition_matches_phase,
// device_access_status.unresolved_is_open) or the pre-admission phase
// (mutation_state.sequence_matches_phase).
func TestMutationStatePhaseNumbersMatchDocs(t *testing.T) {
	want := map[accessv1.OperationPhase]int32{
		accessv1.OperationPhase_OPERATION_PHASE_INTENT_RECORDED: 1,
		accessv1.OperationPhase_OPERATION_PHASE_ACKNOWLEDGED:    7,
		accessv1.OperationPhase_OPERATION_PHASE_RELEASED:        8,
		accessv1.OperationPhase_OPERATION_PHASE_ABANDONED:       9,
	}
	for phase, number := range want {
		if int32(phase) != number {
			t.Errorf("%s is %d, a CEL rule expects %d", phase, int32(phase), number)
		}
	}
}

// TestCompletenessNumberMatchesDocs pins the ordinal
// interface_observation.complete_sets_every_compared_field compares
// against, so renumbering Completeness cannot silently invert which value
// requires description, admin_status, oper_status, and provenance.
func TestCompletenessNumberMatchesDocs(t *testing.T) {
	if got := int32(accessv1.Completeness_COMPLETENESS_COMPLETE); got != 1 {
		t.Errorf("Completeness_COMPLETENESS_COMPLETE is %d, a CEL rule expects 1", got)
	}
}
