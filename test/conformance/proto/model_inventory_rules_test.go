package conformance

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/inventory/v1"
	policyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/policy/v1"
)

func TestInventoryCapabilityRules(t *testing.T) {
	tests := []validationCase{
		{
			name:      "capabilities may be empty",
			message:   inventoryv1.CapabilitySet_builder{}.Build(),
			wantValid: true,
		},
		{
			name: "capabilities are unique",
			message: inventoryv1.CapabilitySet_builder{
				Capabilities: []inventoryv1.Capability{
					inventoryv1.Capability_CAPABILITY_INTERFACE,
					inventoryv1.Capability_CAPABILITY_INTERFACE,
				},
			}.Build(),
			wantValid: false,
		},
		{
			name: "capabilities reject unspecified",
			message: inventoryv1.CapabilitySet_builder{
				Capabilities: []inventoryv1.Capability{
					inventoryv1.Capability_CAPABILITY_UNSPECIFIED,
				},
			}.Build(),
			wantValid: false,
		},
		{
			name: "unknown nonzero capabilities remain valid",
			message: inventoryv1.CapabilitySet_builder{
				Capabilities: []inventoryv1.Capability{
					inventoryv1.Capability(99),
				},
			}.Build(),
			wantValid: true,
		},
		{
			name: "distinct capabilities are valid",
			message: inventoryv1.CapabilitySet_builder{
				Capabilities: []inventoryv1.Capability{
					inventoryv1.Capability_CAPABILITY_SYSTEM,
					inventoryv1.Capability_CAPABILITY_INTERFACE,
					inventoryv1.Capability_CAPABILITY_SWITCHING,
					inventoryv1.Capability_CAPABILITY_ROUTING,
					inventoryv1.Capability_CAPABILITY_FIREWALL,
					inventoryv1.Capability_CAPABILITY_WIRELESS,
				},
			}.Build(),
			wantValid: true,
		},
	}

	runValidationCases(t, tests)
}

func deviceConfig(mode inventoryv1.DeviceManagementMode, policy *policyv1.AccessPolicyHandle) *inventoryv1.DeviceConfig {
	b := inventoryv1.DeviceConfig_builder{
		Ref:          deviceRef(deviceID),
		AccessPolicy: policy,
	}
	if mode != inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_UNSPECIFIED {
		b.ManagementMode = mode.Enum()
	}

	return b.Build()
}

func TestDeviceConfigRules(t *testing.T) {
	tests := []validationCase{
		{
			name: "authoritative device with a policy is valid",
			message: deviceConfig(
				inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE,
				accessPolicyHandle("icx7150-lab", 3),
			),
			wantValid: true,
		},
		{
			name: "operator-managed device with a policy is valid",
			message: deviceConfig(
				inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_OPERATOR_MANAGED,
				accessPolicyHandle("icx7150-lab", 1),
			),
			wantValid: true,
		},
		{
			name: "management mode is required",
			message: deviceConfig(
				inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_UNSPECIFIED,
				accessPolicyHandle("icx7150-lab", 1),
			),
			wantValid: false,
		},
		{
			name: "access policy is required",
			message: deviceConfig(
				inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE,
				nil,
			),
			wantValid: false,
		},
		{
			name: "invalid policy handle fails the config",
			message: deviceConfig(
				inventoryv1.DeviceManagementMode_DEVICE_MANAGEMENT_MODE_AUTHORITATIVE,
				accessPolicyHandle("../root", 1),
			),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}

func provenance() inventoryv1.Provenance_builder {
	return inventoryv1.Provenance_builder{
		Binding:             bindingRef(),
		ObservedAt:          timestamppb.New(edgeIssuedAt),
		Management:          inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_SSH.Enum(),
		Edge:                edgeRef(),
		FirmwareFingerprint: proto.String("ICX7150-24P SPS10010g"),
	}
}

func TestProvenanceRules(t *testing.T) {
	complete := provenance()

	logArm := provenance()
	logArm.Management = nil
	logArm.Log = inventoryv1.LogProtocol_LOG_PROTOCOL_SYSLOG.Enum()

	noEdge := provenance()
	noEdge.Edge = nil

	neitherArm := provenance()
	neitherArm.Management = nil
	neitherArm.Log = nil

	unspecifiedManagement := provenance()
	unspecifiedManagement.Management = inventoryv1.ManagementProtocol_MANAGEMENT_PROTOCOL_UNSPECIFIED.Enum()

	undefinedManagement := provenance()
	badMgmt := inventoryv1.ManagementProtocol(99)
	undefinedManagement.Management = &badMgmt

	unspecifiedLog := provenance()
	unspecifiedLog.Management = nil
	unspecifiedLog.Log = inventoryv1.LogProtocol_LOG_PROTOCOL_UNSPECIFIED.Enum()

	undefinedLog := provenance()
	undefinedLog.Management = nil
	badLog := inventoryv1.LogProtocol(99)
	undefinedLog.Log = &badLog

	noFingerprint := provenance()
	noFingerprint.FirmwareFingerprint = nil

	emptyFingerprint := provenance()
	emptyFingerprint.FirmwareFingerprint = proto.String("")

	noBinding := provenance()
	noBinding.Binding = nil

	tests := []validationCase{
		{name: "complete provenance with management protocol is valid", message: complete.Build(), wantValid: true},
		{name: "provenance with log protocol syslog is valid", message: logArm.Build(), wantValid: true},
		{name: "cloud-mediated provenance without an edge is valid", message: noEdge.Build(), wantValid: true},
		{name: "neither protocol arm fails oneof", message: neitherArm.Build()},
		{name: "unspecified management protocol is rejected", message: unspecifiedManagement.Build()},
		{name: "undefined management protocol is rejected", message: undefinedManagement.Build()},
		{name: "unspecified log protocol is rejected", message: unspecifiedLog.Build()},
		{name: "undefined log protocol is rejected", message: undefinedLog.Build()},
		{name: "provenance without an identity probe is valid", message: noFingerprint.Build(), wantValid: true},
		{name: "empty firmware fingerprint is rejected", message: emptyFingerprint.Build()},
		{name: "binding is required", message: noBinding.Build()},
	}

	runValidationCases(t, tests)
}
