package analysis_test

import (
	"slices"
	"sync"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

func TestSummarizeStatusPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		issues []analysis.Issue
		want   analysis.Status
	}{
		{name: "no issues", want: analysis.Complete},
		{name: "complete", issues: issues(analysis.Complete), want: analysis.Complete},
		{name: "incomplete", issues: issues(analysis.Complete, analysis.Incomplete), want: analysis.Incomplete},
		{name: "exhausted", issues: issues(analysis.Incomplete, analysis.Exhausted), want: analysis.Exhausted},
		{name: "unstable", issues: issues(analysis.Exhausted, analysis.Unstable), want: analysis.Unstable},
		{name: "unsupported", issues: issues(analysis.Unstable, analysis.Unsupported), want: analysis.Unsupported},
		{name: "order independent", issues: issues(analysis.Unsupported, analysis.Complete, analysis.Exhausted), want: analysis.Unsupported},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := analysis.Summarize(tt.issues); got != tt.want {
				t.Errorf("Summarize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetadataRetainsEveryIssueAndDerivesStatus(t *testing.T) {
	t.Parallel()

	scope := analysis.NodeScope("switch-a")
	metadata := analysis.NewMetadata(scope, []analysis.Issue{
		{Code: "bridge/fdb-missing", Status: analysis.Incomplete, Scope: scope, Message: "forwarding entry was not observed"},
		{Code: "routing/unsupported-family", Status: analysis.Unsupported, Scope: scope, Message: "address family is not modeled"},
	}, analysis.EvidenceCatalog{}, nil)

	if got := metadata.Status(); got != analysis.Unsupported {
		t.Errorf("Status() = %v, want %v", got, analysis.Unsupported)
	}
	gotIssues := metadata.Issues()
	if len(gotIssues) != 2 {
		t.Fatalf("len(Issues()) = %d, want 2", len(gotIssues))
	}
	if gotIssues[0].Code != analysis.IssueCode("bridge/fdb-missing") || gotIssues[1].Code != analysis.IssueCode("routing/unsupported-family") {
		t.Errorf("issue codes = %q, %q; want both producer-owned causes", gotIssues[0].Code, gotIssues[1].Code)
	}
}

func TestMetadataFiltersIssuesByOverlappingScope(t *testing.T) {
	t.Parallel()

	nodeA := analysis.NodeScope("switch-a")
	portA1 := analysis.PortScope("switch-a", "1/1")
	portA2 := analysis.PortScope("switch-a", "1/2")
	nodeB := analysis.NodeScope("switch-b")
	metadata := analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{
		{Code: "port/link-state", Status: analysis.Exhausted, Scope: portA1},
		{Code: "node/config-partial", Status: analysis.Incomplete, Scope: nodeA},
		{Code: "port/media", Status: analysis.Unsupported, Scope: portA2},
		{Code: "node/unreachable", Status: analysis.Unstable, Scope: nodeB},
	}, analysis.EvidenceCatalog{}, nil)

	got := metadata.IssuesFor(portA1)
	wantCodes := []analysis.IssueCode{"node/config-partial", "port/link-state"}
	if len(got) != len(wantCodes) {
		t.Fatalf("len(IssuesFor(portA1)) = %d, want %d", len(got), len(wantCodes))
	}
	for i, want := range wantCodes {
		if got[i].Code != want {
			t.Errorf("IssuesFor(portA1)[%d].Code = %q, want %q", i, got[i].Code, want)
		}
	}
	if status := metadata.StatusFor(portA1); status != analysis.Exhausted {
		t.Errorf("StatusFor(portA1) = %v, want %v", status, analysis.Exhausted)
	}
	if status := metadata.StatusFor(nodeA); status != analysis.Unsupported {
		t.Errorf("StatusFor(nodeA) = %v, want %v", status, analysis.Unsupported)
	}
	if status := metadata.StatusFor(analysis.PortScope("switch-a", "1/3")); status != analysis.Incomplete {
		t.Errorf("StatusFor(unaffected sibling) = %v, want %v", status, analysis.Incomplete)
	}
}

func TestScopeOrderingAndRendering(t *testing.T) {
	t.Parallel()

	scopes := []analysis.Scope{
		analysis.FieldScope(analysis.PortScope("switch-a", "1/1"), "vlans", "10", "pvid"),
		analysis.JourneyScope("frame-7"),
		analysis.ProtocolScope("switch-a", "stp", "0"),
		analysis.PortScope("switch-a", "1/1"),
		analysis.LinkScope("uplink/a-b"),
		analysis.NodeScope("switch-a"),
		analysis.WholeScope(),
	}
	slices.SortFunc(scopes, func(a, b analysis.Scope) int { return a.Compare(b) })

	want := []string{
		"analysis",
		`node["switch-a"]`,
		`node["switch-a"]/port["1/1"]`,
		`node["switch-a"]/port["1/1"]/field["vlans","10","pvid"]`,
		`node["switch-a"]/protocol["stp","0"]`,
		`link["uplink/a-b"]`,
		`journey["frame-7"]`,
	}
	for i, scope := range scopes {
		if got := scope.String(); got != want[i] {
			t.Errorf("scope[%d].String() = %q, want %q", i, got, want[i])
		}
	}

	if !analysis.NodeScope("switch-a").Contains(analysis.PortScope("switch-a", "1/1")) {
		t.Error("node scope does not contain its port scope")
	}
	if analysis.PortScope("switch-a", "1/1").Overlaps(analysis.PortScope("switch-a", "1/2")) {
		t.Error("sibling port scopes overlap")
	}
}

func TestEvidenceCatalogDeduplicatesAndLooksUp(t *testing.T) {
	t.Parallel()

	evidence := analysis.Evidence{
		Kind:    "observation",
		Origin:  "snapshot/current",
		Context: "ifOperStatus for switch-a port 1/1",
	}
	var empty analysis.EvidenceCatalog
	catalog, ref1 := empty.Add(evidence)
	catalog, ref2 := catalog.Add(evidence)

	if ref1 != ref2 {
		t.Errorf("duplicate refs = %q and %q, want equal", ref1, ref2)
	}
	if got := empty.Entries(); len(got) != 0 {
		t.Errorf("zero catalog changed after Add: len = %d, want 0", len(got))
	}
	entries := catalog.Entries()
	if len(entries) != 1 {
		t.Fatalf("len(Entries()) = %d, want 1", len(entries))
	}
	if entries[0].Ref != ref1 || entries[0].Evidence != evidence {
		t.Errorf("entry = %+v, want ref %q and evidence %+v", entries[0], ref1, evidence)
	}
	if got, ok := catalog.Lookup(ref1); !ok || got != evidence {
		t.Errorf("Lookup(%q) = %+v, %t; want %+v, true", ref1, got, ok, evidence)
	}
	if _, ok := catalog.Lookup(trace.EvidenceRef("missing")); ok {
		t.Error("Lookup(missing) found evidence")
	}

	other := analysis.Evidence{Kind: "configuration", Origin: "candidate", Context: "port 1/1 enabled"}
	catalog, _ = catalog.Add(other)
	entries = catalog.Entries()
	if !slices.IsSortedFunc(entries, func(a, b analysis.EvidenceEntry) int {
		return stringCompare(string(a.Ref), string(b.Ref))
	}) {
		t.Errorf("Entries() = %+v, want stable reference order", entries)
	}
}

func TestMetadataReturnsImmutableCopies(t *testing.T) {
	t.Parallel()

	issueEvidence := []trace.EvidenceRef{"ev-b", "ev-a", "ev-a"}
	assumptionEvidence := []trace.EvidenceRef{"ev-b", "ev-a"}
	path := []string{"interfaces", "1/1", "speed"}
	issue := analysis.Issue{
		Code:     "phy/speed-assumed",
		Status:   analysis.Incomplete,
		Scope:    analysis.FieldScope(analysis.NodeScope("switch-a"), path...),
		Evidence: issueEvidence,
	}
	assumption := analysis.Assumption{
		Scope:     analysis.PortScope("switch-a", "1/1"),
		Statement: "the configured maximum is the operational speed",
		Evidence:  assumptionEvidence,
	}
	catalog, ref := (analysis.EvidenceCatalog{}).Add(analysis.Evidence{Kind: "configuration", Origin: "current", Context: "speed"})
	metadata := analysis.NewMetadata(analysis.NodeScope("switch-a"), []analysis.Issue{issue}, catalog, []analysis.Assumption{assumption})

	issueEvidence[0] = "changed"
	assumptionEvidence[0] = "changed"
	path[0] = "changed"
	gotIssues := metadata.Issues()
	gotIssues[0].Code = "changed"
	gotIssues[0].Evidence[0] = "changed"
	gotAssumptions := metadata.Assumptions()
	gotAssumptions[0].Statement = "changed"
	gotAssumptions[0].Evidence[0] = "changed"
	returnedCatalog, _ := metadata.Evidence().Add(analysis.Evidence{Kind: "other"})

	gotIssues = metadata.Issues()
	if gotIssues[0].Code != analysis.IssueCode("phy/speed-assumed") {
		t.Errorf("stored issue code = %q, want phy/speed-assumed", gotIssues[0].Code)
	}
	wantIssueEvidence := []trace.EvidenceRef{"ev-a", "ev-b"}
	if !slices.Equal(gotIssues[0].Evidence, wantIssueEvidence) {
		t.Errorf("stored issue evidence = %v, want %v", gotIssues[0].Evidence, wantIssueEvidence)
	}
	if got := gotIssues[0].Scope.String(); got != `node["switch-a"]/field["interfaces","1/1","speed"]` {
		t.Errorf("stored issue scope = %q, want original field path", got)
	}
	gotAssumptions = metadata.Assumptions()
	if gotAssumptions[0].Statement != assumption.Statement || !slices.Equal(gotAssumptions[0].Evidence, []trace.EvidenceRef{"ev-a", "ev-b"}) {
		t.Errorf("stored assumption = %+v, want original canonical value", gotAssumptions[0])
	}
	if got, ok := metadata.Evidence().Lookup(ref); !ok || got.Context != "speed" {
		t.Errorf("stored evidence = %+v, %t; want original", got, ok)
	}
	if len(returnedCatalog.Entries()) != 2 || len(metadata.Evidence().Entries()) != 1 {
		t.Error("adding to returned evidence catalog changed metadata")
	}
}

func TestMetadataOrdersAssumptions(t *testing.T) {
	t.Parallel()

	metadata := analysis.NewMetadata(analysis.WholeScope(), nil, analysis.EvidenceCatalog{}, []analysis.Assumption{
		{Scope: analysis.PortScope("switch-b", "1/1"), Statement: "later"},
		{Scope: analysis.NodeScope("switch-a"), Statement: "zebra"},
		{Scope: analysis.NodeScope("switch-a"), Statement: "alpha"},
	})

	got := metadata.Assumptions()
	want := []string{"alpha", "zebra", "later"}
	for i := range want {
		if got[i].Statement != want[i] {
			t.Errorf("Assumptions()[%d].Statement = %q, want %q", i, got[i].Statement, want[i])
		}
	}
}

func TestZeroValuesAreExplicitAndUsable(t *testing.T) {
	t.Parallel()

	var validity analysis.InputValidity
	if validity != analysis.InputValid || validity.String() != "valid" {
		t.Errorf("zero InputValidity = %v, want valid", validity)
	}
	var status analysis.Status
	if status != analysis.Complete || status.String() != "complete" {
		t.Errorf("zero Status = %v, want complete", status)
	}
	var scope analysis.Scope
	if scope != analysis.WholeScope() || scope.String() != "analysis" {
		t.Errorf("zero Scope = %q, want analysis", scope.String())
	}
	var metadata analysis.Metadata
	if metadata.Status() != analysis.Complete || metadata.Scope() != analysis.WholeScope() || len(metadata.Issues()) != 0 || len(metadata.Assumptions()) != 0 {
		t.Errorf("zero Metadata = status %v, scope %q, %d issues, %d assumptions", metadata.Status(), metadata.Scope(), len(metadata.Issues()), len(metadata.Assumptions()))
	}
	var catalog analysis.EvidenceCatalog
	updated, ref := catalog.Add(analysis.Evidence{})
	if ref == "" || len(updated.Entries()) != 1 || len(catalog.Entries()) != 0 {
		t.Errorf("zero EvidenceCatalog Add = ref %q, updated %d, original %d", ref, len(updated.Entries()), len(catalog.Entries()))
	}
}

func TestMetadataSupportsConcurrentReaders(t *testing.T) {
	t.Parallel()

	catalog, ref := (analysis.EvidenceCatalog{}).Add(analysis.Evidence{Kind: "observation", Origin: "current", Context: "port state"})
	metadata := analysis.NewMetadata(analysis.NodeScope("switch-a"), []analysis.Issue{{
		Code:     "port/state-missing",
		Status:   analysis.Incomplete,
		Scope:    analysis.PortScope("switch-a", "1/1"),
		Evidence: []trace.EvidenceRef{ref},
	}}, catalog, []analysis.Assumption{{
		Scope:     analysis.PortScope("switch-a", "1/1"),
		Statement: "missing state is treated as unknown",
		Evidence:  []trace.EvidenceRef{ref},
	}})

	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for range 100 {
				_ = metadata.Status()
				_ = metadata.StatusFor(analysis.PortScope("switch-a", "1/1"))
				_ = metadata.Issues()
				_ = metadata.Assumptions()
				_, _ = metadata.Evidence().Lookup(ref)
			}
		}()
	}
	wg.Wait()
}

func issues(statuses ...analysis.Status) []analysis.Issue {
	result := make([]analysis.Issue, len(statuses))
	for i, status := range statuses {
		result[i] = analysis.Issue{Code: analysis.IssueCode(status.String()), Status: status}
	}
	return result
}

func stringCompare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
