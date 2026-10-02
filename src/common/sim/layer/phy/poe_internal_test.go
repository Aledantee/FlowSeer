package phy

import "testing"

func TestClassPowerNanowatts(t *testing.T) {
	t.Parallel()

	for class, want := range classPowerLevels {
		got, ok := classPowerNanowatts(uint8(class))
		if !ok || got != want {
			t.Errorf("classPowerNanowatts(%d) = %d, %v; want %d, true", class, got, ok, want)
		}
	}

	if got, ok := classPowerNanowatts(9); ok || got != 0 {
		t.Errorf("classPowerNanowatts(9) = %d, %v; want 0, false", got, ok)
	}
}
