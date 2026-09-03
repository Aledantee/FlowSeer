//go:build mibgen_baseline_complete

package main

import "testing"

// TestBaselineComplete is the merge gate, the second of the baseline's
// two tiers.
//
// The always-on gate in baseline_test.go permits a pending group so that
// a branch part-way through triaging an upstream re-sync still builds.
// This one does not: every group in the committed baseline must be
// recorded with a reason or waived as an allowlisted accepted risk
// before the branch merges, or the record says a condition exists
// without saying why anybody was willing to live with it.
//
// Run it with:
//
//	go test -tags=mibgen_baseline_complete -run TestBaselineComplete ./src/protocol/snmp/cmd/mibgen/
//
// It is deliberately absent from the default suite, which is why the
// untagged run and this one are both required.
func TestBaselineComplete(t *testing.T) {
	_, _, bl := committedBaseline(t)

	if pending := bl.PendingGroups(); len(pending) > 0 {
		t.Errorf("%d baseline group(s) still pending — each needs a recorded reason or an allowlisted accepted risk before merge:\n  %v",
			len(pending), pending)
	}
}
