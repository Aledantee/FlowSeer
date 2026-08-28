package protoconformance

import (
	"testing"

	inventoryv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/inventory/v1"
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
