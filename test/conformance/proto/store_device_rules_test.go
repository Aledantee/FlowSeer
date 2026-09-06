package conformance

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/policy/v1"
	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	storev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/device/v1"
)

func laneRecord() storev1.DeviceLaneRecord_builder {
	return storev1.DeviceLaneRecord_builder{
		Device:        deviceRef(deviceID),
		HighWatermark: 7,
	}
}

func openLaneRecord() storev1.DeviceLaneRecord_builder {
	record := laneRecord()
	record.Mutation = mutationState(accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED).Build()
	record.SubmittedAt = timestamppb.New(edgeIssuedAt)
	record.Dispatched = true
	record.DispatchConfirmed = true
	record.LastReportedPhase = accessv1.OperationPhase_OPERATION_PHASE_ADMITTED.Enum()
	return record
}

func TestDeviceLaneRecordRules(t *testing.T) {
	noTime := openLaneRecord()
	noTime.SubmittedAt = nil

	timeWithoutMutation := laneRecord()
	timeWithoutMutation.SubmittedAt = timestamppb.New(edgeIssuedAt)

	confirmedWithoutMutation := laneRecord()
	confirmedWithoutMutation.DispatchConfirmed = true

	phaseWithoutMutation := laneRecord()
	phaseWithoutMutation.LastReportedPhase = accessv1.OperationPhase_OPERATION_PHASE_ADMITTED.Enum()

	withReads := laneRecord()
	withReads.OpenReads = map[string]*storev1.OpenRead{
		"ethernet 1/1/1": storev1.OpenRead_builder{
			Sequence: proto.Uint64(8),
			Read:     typedRead("ethernet 1/1/1"),
			Deadline: timestamppb.New(edgeIssuedAt.Add(30 * time.Second)),
		}.Build(),
	}
	withReads.ExpectedDescriptions = map[string]string{"ethernet 1/1/1": "uplink to core"}
	withReads.LastObservations = map[string]*accessv1.InterfaceObservation{"ethernet 1/1/1": interfaceObservation().Build()}
	withReads.Idempotency = []*storev1.IdempotencyEntry{
		storev1.IdempotencyEntry_builder{IdempotencyKey: proto.String(idempotencyKey), Sequence: proto.Uint64(7)}.Build(),
	}

	badReadKey := laneRecord()
	badReadKey.OpenReads = map[string]*storev1.OpenRead{
		"": storev1.OpenRead_builder{
			Sequence: proto.Uint64(8),
			Read:     typedRead("ethernet 1/1/1"),
			Deadline: timestamppb.New(edgeIssuedAt),
		}.Build(),
	}

	runValidationCases(t, []validationCase{
		{name: "free lane is valid", message: laneRecord().Build(), wantValid: true},
		{name: "open mutation with its facts is valid", message: openLaneRecord().Build(), wantValid: true},
		{name: "open mutation without a submission time is rejected", message: noTime.Build()},
		{name: "submission time without a mutation is rejected", message: timeWithoutMutation.Build()},
		{name: "confirmation without a mutation is rejected", message: confirmedWithoutMutation.Build()},
		{name: "reported phase without a mutation is rejected", message: phaseWithoutMutation.Build()},
		{name: "open reads and expectations are valid", message: withReads.Build(), wantValid: true},
		{name: "open read with an empty interface key is rejected", message: badReadKey.Build()},
		{name: "device is required", message: storev1.DeviceLaneRecord_builder{HighWatermark: 1}.Build()},
	})
}

func TestOpenReadRules(t *testing.T) {
	closedWithObservation := storev1.OpenRead_builder{
		Sequence:    proto.Uint64(8),
		Read:        typedRead("ethernet 1/1/1"),
		Deadline:    timestamppb.New(edgeIssuedAt),
		Observation: interfaceObservation().Build(),
	}.Build()

	runValidationCases(t, []validationCase{
		{name: "closed read keeps its observation", message: closedWithObservation, wantValid: true},
		{
			name: "open read without a deadline is rejected",
			message: storev1.OpenRead_builder{
				Sequence: proto.Uint64(8),
				Read:     typedRead("ethernet 1/1/1"),
			}.Build(),
		},
		{
			name: "open read without the read is rejected",
			message: storev1.OpenRead_builder{
				Sequence: proto.Uint64(8),
				Deadline: timestamppb.New(edgeIssuedAt),
			}.Build(),
		},
	})
}

func registryDevice(id, name string) *storev1.RegistryDevice {
	return storev1.RegistryDevice_builder{
		Config: inventoryv1.DeviceConfig_builder{
			Ref:            deviceRef(id),
			Name:           proto.String(name),
			ManagementMode: inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED.Enum(),
			AccessPolicy:   accessPolicyHandle("icx7150-lab", 3),
		}.Build(),
		Binding:             bindingRef(),
		Ip:                  addrv1.IpAddress_builder{V4: addrv1.Ipv4Address_builder{Octets: []byte{172, 16, 0, 6}}.Build()}.Build(),
		DelayedApplyHorizon: durationpb.New(30 * time.Second),
		ManagedInterfaces:   []string{"ethernet 1/1/1"},
	}.Build()
}

func registryPolicy(key string) *storev1.RegistryPolicy {
	return storev1.RegistryPolicy_builder{
		Handle:               accessPolicyHandle(key, 3),
		ReadCredential:       policyv1.CredentialHandle_builder{Key: proto.String(key + "-snmp"), Version: proto.Uint64(1)}.Build(),
		SubmissionCredential: policyv1.CredentialHandle_builder{Key: proto.String(key + "-ssh"), Version: proto.Uint64(1)}.Build(),
		HostTrust:            hostTrust(key+"-hostkey", 1),
		SshHostKeySha256:     proto.String(hostKeyPin),
	}.Build()
}

func TestDeviceRegistryRules(t *testing.T) {
	integration := storev1.RegistryIntegration_builder{
		Ref:  integrationRef(cloudIntegrationID),
		Edge: edgeRef(),
	}.Build()

	valid := storev1.DeviceRegistry_builder{
		Integration: integration,
		Devices:     []*storev1.RegistryDevice{registryDevice(deviceID, "icx7150")},
		Policies:    []*storev1.RegistryPolicy{registryPolicy("icx7150-lab")},
	}.Build()

	duplicateDevice := storev1.DeviceRegistry_builder{
		Integration: integration,
		Devices:     []*storev1.RegistryDevice{registryDevice(deviceID, "icx7150"), registryDevice(deviceID, "icx7150-again")},
	}.Build()

	duplicatePolicy := storev1.DeviceRegistry_builder{
		Integration: integration,
		Policies:    []*storev1.RegistryPolicy{registryPolicy("icx7150-lab"), registryPolicy("icx7150-lab")},
	}.Build()

	unmeasured := registryDevice(deviceID, "icx7150")
	unmeasured.ClearDelayedApplyHorizon()

	badInterface := registryDevice(deviceID, "icx7150")
	badInterface.SetManagedInterfaces([]string{"ethernet 1/1/1", "ethernet 1/1/1"})

	runValidationCases(t, []validationCase{
		{name: "registry with one device and one policy is valid", message: valid, wantValid: true},
		{name: "a device listed twice is rejected", message: duplicateDevice},
		{name: "a policy key listed twice is rejected", message: duplicatePolicy},
		{name: "registry without an integration is rejected", message: storev1.DeviceRegistry_builder{}.Build()},
		{name: "device without a measured horizon is still a valid record", message: unmeasured, wantValid: true},
		{name: "duplicate managed interfaces are rejected", message: badInterface},
		{
			name: "policy without a host key pin is rejected",
			message: storev1.RegistryPolicy_builder{
				Handle:               accessPolicyHandle("icx7150-lab", 3),
				ReadCredential:       policyv1.CredentialHandle_builder{Key: proto.String("k-snmp"), Version: proto.Uint64(1)}.Build(),
				SubmissionCredential: policyv1.CredentialHandle_builder{Key: proto.String("k-ssh"), Version: proto.Uint64(1)}.Build(),
				HostTrust:            hostTrust("k-hostkey", 1),
			}.Build(),
		},
	})
}

func TestStoredEdgeRules(t *testing.T) {
	record := edgev1.EdgeRecord_builder{
		Config: edgev1.EdgeConfig_builder{Ref: edgeRef()}.Build(),
		State: edgev1.EdgeState_builder{
			Ref:       edgeRef(),
			Lifecycle: edgev1.EdgeLifecycle_EDGE_LIFECYCLE_PENDING.Enum(),
			SetupKey:  edgeSetupKey(edgeIssuedAt, edgeIssuedAt.Add(time.Hour)),
		}.Build(),
	}.Build()

	runValidationCases(t, []validationCase{
		{name: "stored edge with a key hash is valid", message: storev1.StoredEdge_builder{Record: record, SetupKeyHash: make([]byte, 32)}.Build(), wantValid: true},
		{name: "stored edge without a hash is valid", message: storev1.StoredEdge_builder{Record: record}.Build(), wantValid: true},
		{name: "a hash that is not 32 bytes is rejected", message: storev1.StoredEdge_builder{Record: record, SetupKeyHash: []byte{1, 2, 3}}.Build()},
		{name: "stored edge without a record is rejected", message: storev1.StoredEdge_builder{}.Build()},
	})
}
