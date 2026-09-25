package goname_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/protocol/internal/goname"
)

func TestExported(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty string", input: "", want: "X"},
		{name: "separators only", input: "--", want: "X"},
		{name: "camel case with trailing initialism", input: "lldpRemChassisId", want: "LLDPRemChassisID"},
		{name: "digits attached to preceding word", input: "dot1qFdbId", want: "Dot1qFdbID"},
		{name: "run of capitals ending one letter early", input: "ifHCInOctets", want: "IfHCInOctets"},
		{name: "hyphenated initialisms", input: "vlan-id", want: "VLANID"},
		{name: "hyphenated ipv6", input: "ipv6-address", want: "IPv6Address"},
		{name: "hyphenated mac", input: "mac-address", want: "MACAddress"},
		{name: "hyphenated non-initialism", input: "if-gsn", want: "IfGsn"},
		{name: "leading digit prefix", input: "802dot3", want: "X802dot3"},
		{name: "single initialism", input: "ip", want: "IP"},
		{name: "initialism with camel case", input: "ipAdEntAddr", want: "IPAdEntAddr"},
		{name: "hyphenated word containing initialism", input: "idle-timeout", want: "IdleTimeout"},
		{name: "word containing initialism idle", input: "Idle", want: "Idle"},
		{name: "word containing initialism vlans", input: "Vlans", want: "Vlans"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goname.Exported(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnexported(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "leading initialism lldp", input: "LLDPPortConfigTable", want: "lldpPortConfigTable"},
		{name: "leading regular word", input: "IfTable", want: "ifTable"},
		{name: "standalone initialism", input: "ID", want: "id"},
		{name: "leading mixed-case initialism", input: "IPv6Address", want: "ipv6Address"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goname.Unexported(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
