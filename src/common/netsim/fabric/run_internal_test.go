package fabric

import (
	"math"
	"testing"
	"time"
)

func TestRateIntervalHandlesArithmeticBoundaries(t *testing.T) {
	t.Parallel()

	if got := rateInterval(704, math.MaxUint64); got != time.Nanosecond {
		t.Errorf("rateInterval at maximum rate = %v, want 1ns", got)
	}
	if got := rateInterval(math.MaxUint64, 1); got != time.Duration(math.MaxInt64) {
		t.Errorf("overflowing rateInterval = %v, want maximum duration", got)
	}
}
