package syslogsource_test

import (
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/agent/internal/syslogsource"
)

func TestRawPolicy_Drives220FailuresAndWindowRollOver(t *testing.T) {
	t.Parallel()

	var frozenNanos atomic.Int64
	initial := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	frozenNanos.Store(initial.UnixNano())
	clock := func() time.Time { return time.Unix(0, frozenNanos.Load()).UTC() }
	setNow := func(t time.Time) { frozenNanos.Store(t.UnixNano()) }

	policy := syslogsource.NewRawPolicy(20, 100, clock)
	const (
		device      = "dev-1"
		otherDevice = "dev-2"
	)

	var keptCount int
	var totalSuppressed uint64

	for i := 1; i <= 220; i++ {
		keep, suppressed := policy.Evaluate(device)
		switch {
		case i <= 20:
			if !keep {
				t.Errorf("failure %d: keep = false, want true", i)
			}
			if suppressed != 0 {
				t.Errorf("failure %d: suppressed = %d, want 0", i, suppressed)
			}
			keptCount++
		case i == 120:
			if !keep {
				t.Errorf("failure %d (100th sample): keep = false, want true", i)
			}
			if suppressed != 99 {
				t.Errorf("failure %d: suppressed = %d, want 99", i, suppressed)
			}
			keptCount++
			totalSuppressed += suppressed
		case i == 220:
			if !keep {
				t.Errorf("failure %d (200th sample): keep = false, want true", i)
			}
			if suppressed != 99 {
				t.Errorf("failure %d: suppressed = %d, want 99", i, suppressed)
			}
			keptCount++
			totalSuppressed += suppressed
		default:
			if keep {
				t.Errorf("failure %d: keep = true, want false (suppressed)", i)
			}
			if suppressed != 0 {
				t.Errorf("failure %d: suppressed = %d, want 0 on suppressed failure", i, suppressed)
			}
		}
	}

	if keptCount != 22 {
		t.Errorf("keptCount = %d, want 22 (20 initial + 2 sampled)", keptCount)
	}
	if totalSuppressed != 198 {
		t.Errorf("totalSuppressed = %d, want 198 (99 + 99)", totalSuppressed)
	}

	keepOther, suppOther := policy.Evaluate(otherDevice)
	if !keepOther {
		t.Errorf("other device: keep = false, want true")
	}
	if suppOther != 0 {
		t.Errorf("other device: suppressed = %d, want 0", suppOther)
	}

	for j := 1; j <= 3; j++ {
		keep, supp := policy.Evaluate(device)
		if keep {
			t.Errorf("pre-rollover failure %d: keep = true, want false", j)
		}
		if supp != 0 {
			t.Errorf("pre-rollover failure %d: suppressed = %d, want 0", j, supp)
		}
	}

	setNow(initial.Add(time.Minute))

	keep, suppressed := policy.Evaluate(device)
	if !keep {
		t.Error("failure 1 in new minute: keep = false, want true")
	}
	if suppressed != 3 {
		t.Errorf("failure 1 in new minute: suppressed = %d, want 3", suppressed)
	}
}
