package vlan_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

func TestIDValid(t *testing.T) {
	cases := []struct {
		name string
		id   vlan.ID
		want bool
	}{
		{name: "zero priority tagged", id: 0, want: false},
		{name: "min valid id", id: 1, want: true},
		{name: "interior valid id", id: 100, want: true},
		{name: "max valid id", id: 4094, want: true},
		{name: "reserved 4095", id: 4095, want: false},
		{name: "above 12 bit 4096", id: 4096, want: false},
		{name: "uint16 max", id: 65535, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.id.Valid(); got != tc.want {
				t.Errorf("ID(%d).Valid() = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestPCPValid(t *testing.T) {
	cases := []struct {
		name string
		pcp  vlan.PCP
		want bool
	}{
		{name: "min valid pcp", pcp: 0, want: true},
		{name: "interior valid pcp", pcp: 5, want: true},
		{name: "max valid pcp", pcp: 7, want: true},
		{name: "first invalid pcp", pcp: 8, want: false},
		{name: "high invalid pcp", pcp: 255, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pcp.Valid(); got != tc.want {
				t.Errorf("PCP(%d).Valid() = %v, want %v", tc.pcp, got, tc.want)
			}
		})
	}
}

func TestTagVIDMayBeZero(t *testing.T) {
	tag := vlan.Tag{
		TPID: 0x8100,
		PCP:  3,
		DEI:  false,
		VID:  0,
	}

	if tag.VID != 0 {
		t.Errorf("tag.VID = %d, want 0", tag.VID)
	}
	if tag.VID.Valid() {
		t.Errorf("tag.VID.Valid() = true, want false for priority-tagged VID 0")
	}
}
