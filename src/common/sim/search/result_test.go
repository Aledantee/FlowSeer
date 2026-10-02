package search

import (
	"testing"
)

func TestCoverageProperties(t *testing.T) {
	t.Parallel()

	covZero := Coverage{Tested: 0, Total: 0}
	if covZero.Complete() {
		t.Errorf("covZero.Complete() = true, want false")
	}
	if r := covZero.Ratio(); r != 1.0 {
		t.Errorf("covZero.Ratio() = %f, want 1.0", r)
	}
	if s := covZero.String(); s != "0/0" {
		t.Errorf("covZero.String() = %q, want 0/0", s)
	}

	covPartial := Coverage{Tested: 8, Total: 12}
	if covPartial.Complete() {
		t.Errorf("covPartial.Complete() = true, want false")
	}
	if r := covPartial.Ratio(); r != 8.0/12.0 {
		t.Errorf("covPartial.Ratio() = %f, want %f", r, 8.0/12.0)
	}
	if s := covPartial.String(); s != "8/12" {
		t.Errorf("covPartial.String() = %q, want 8/12", s)
	}

	covFull := Coverage{Tested: 12, Total: 12}
	if !covFull.Complete() {
		t.Errorf("covFull.Complete() = false, want true")
	}
	if r := covFull.Ratio(); r != 1.0 {
		t.Errorf("covFull.Ratio() = %f, want 1.0", r)
	}
}

func TestResultDiscardedDifferences(t *testing.T) {
	t.Parallel()

	res := Result{
		Differences:      make([]RetainedDifference, 2),
		TotalDifferences: 5,
	}

	if disc := res.DiscardedDifferences(); disc != 3 {
		t.Errorf("res.DiscardedDifferences() = %d, want 3", disc)
	}

	resNoDiscard := Result{
		Differences:      make([]RetainedDifference, 2),
		TotalDifferences: 2,
	}
	if disc := resNoDiscard.DiscardedDifferences(); disc != 0 {
		t.Errorf("resNoDiscard.DiscardedDifferences() = %d, want 0", disc)
	}
}
