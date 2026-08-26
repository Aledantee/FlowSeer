package protoconformance

import (
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
)

func TestAddressPrimitiveRules(t *testing.T) {
	eui48 := func(octets ...byte) *addrv1.EuiAddress {
		return addrv1.EuiAddress_builder{
			Eui48: addrv1.Eui48Address_builder{Octets: octets}.Build(),
		}.Build()
	}
	eui64 := func(octets ...byte) *addrv1.EuiAddress {
		return addrv1.EuiAddress_builder{
			Eui64: addrv1.Eui64Address_builder{Octets: octets}.Build(),
		}.Build()
	}

	tests := []validationCase{
		{
			name:      "EUI address requires an arm",
			message:   addrv1.EuiAddress_builder{}.Build(),
			wantValid: false,
		},
		{
			name:      "EUI-48 with 6 octets",
			message:   eui48(0x00, 0x11, 0x22, 0x33, 0x44, 0x55),
			wantValid: true,
		},
		{
			name:      "EUI-48 rejects 5 octets",
			message:   eui48(0x00, 0x11, 0x22, 0x33, 0x44),
			wantValid: false,
		},
		{
			name:      "EUI-48 rejects 7 octets",
			message:   eui48(0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66),
			wantValid: false,
		},
		{
			name:      "EUI-64 with 8 octets",
			message:   eui64(0x02, 0x00, 0x00, 0xff, 0xfe, 0x00, 0x00, 0x01),
			wantValid: true,
		},
		{
			name:      "EUI-64 rejects 6 octets",
			message:   eui64(0x02, 0x00, 0x00, 0xff, 0xfe, 0x00),
			wantValid: false,
		},
	}

	runValidationCases(t, tests)
}
