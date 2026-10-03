package phy

import "testing"

func TestClassPowerNanowatts(t *testing.T) {
	t.Parallel()

	powers := []uint64{
		15_400_000_000, 4_000_000_000, 7_000_000_000, 15_400_000_000, 30_000_000_000,
		45_000_000_000, 60_000_000_000, 75_000_000_000, 90_000_000_000,
	}
	for class, want := range powers {
		got, ok := classPowerNanowatts(uint8(class))
		if !ok || got != want {
			t.Errorf("classPowerNanowatts(%d) = %d, %v; want %d, true", class, got, ok, want)
		}
	}

	if got, ok := classPowerNanowatts(9); ok || got != 0 {
		t.Errorf("classPowerNanowatts(9) = %d, %v; want 0, false", got, ok)
	}
}
