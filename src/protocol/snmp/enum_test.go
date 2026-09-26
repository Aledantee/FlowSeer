package snmp

import (
	"testing"
)

func TestEnumString(t *testing.T) {
	values := []int32{-10, -5, 0, 1, 5, 20}
	names := []string{"negTen", "negFive", "zero", "one", "five", "twenty"}

	tests := []struct {
		name     string
		v        int32
		typeName string
		want     string
	}{
		{"declared value", 1, "Status", "one"},
		{"sparse value", 5, "Speed", "five"},
		{"negative declared value", -5, "Temp", "negFive"},
		{"zero value", 0, "Counter", "zero"},
		{"unknown positive", 99, "FakeStatusValue", "FakeStatusValue(99)"},
		{"unknown negative", -7, "FakeStatusValue", "FakeStatusValue(-7)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EnumString(tc.v, tc.typeName, values, names)
			if got != tc.want {
				t.Errorf("EnumString(%d, %q) = %q, want %q", tc.v, tc.typeName, got, tc.want)
			}
		})
	}
}

func TestEnumString_ZeroAllocations(t *testing.T) {
	values := []int32{1, 2, 3}
	names := []string{"up", "down", "testing"}

	allocs := testing.AllocsPerRun(1000, func() {
		_ = EnumString(1, "Status", values, names)
	})
	if allocs != 0 {
		t.Errorf("EnumString for declared value allocated %v times, want 0", allocs)
	}
}
