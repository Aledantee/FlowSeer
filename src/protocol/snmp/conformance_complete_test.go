//go:build snmp_conformance_complete

package snmp

import "testing"

// TestConformanceCorpusComplete is the merge gate. Unlike the always-on
// TestConformanceCorpusIntegrity (which permits pending rows so the dev branch
// stays green during the hardening), this build-tagged test fails if ANY row
// is still pending — every catalogued quirk must resolve to covered or an
// allowlisted accepted-risk before the branch merges.
//
// Run via `go test -tags=snmp_conformance_complete ./common/snmp/` (wired into
// the merge/CI gate). It deliberately does NOT run in the default suite.
func TestConformanceCorpusComplete(t *testing.T) {
	var pending []string
	for _, r := range conformanceCorpus {
		if r.Status == statusPending {
			pending = append(pending, r.ID)
		}
	}
	if len(pending) > 0 {
		t.Errorf("%d corpus row(s) still pending — every row must be covered or allowlisted accepted-risk before merge:\n  %v",
			len(pending), pending)
	}
}
