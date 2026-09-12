package netsimtest_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
)

func TestSchemaVersion(t *testing.T) {
	if netsimtest.SchemaVersion != "v1" {
		t.Errorf("SchemaVersion = %q, want %q", netsimtest.SchemaVersion, "v1")
	}
}

func TestAdmissionValidation(t *testing.T) {
	validCase := netsimtest.CasePlanningPortVLANChange()

	tests := []struct {
		name    string
		mutate  func(c *netsimtest.Case)
		wantErr string
	}{
		{
			name: "empty ID",
			mutate: func(c *netsimtest.Case) {
				c.ID = ""
			},
			wantErr: "corpus case ID cannot be empty",
		},
		{
			name: "unspecified use case",
			mutate: func(c *netsimtest.Case) {
				c.UseCase = ""
			},
			wantErr: "unspecified or invalid use case class",
		},
		{
			name: "invalid use case",
			mutate: func(c *netsimtest.Case) {
				c.UseCase = "benchmark"
			},
			wantErr: "unspecified or invalid use case class",
		},
		{
			name: "empty question",
			mutate: func(c *netsimtest.Case) {
				c.Question = "   "
			},
			wantErr: "has empty question",
		},
		{
			name: "empty false answer",
			mutate: func(c *netsimtest.Case) {
				c.FalseAnswer = ""
			},
			wantErr: "has empty false answer",
		},
		{
			name: "empty current result",
			mutate: func(c *netsimtest.Case) {
				c.CurrentResult = ""
			},
			wantErr: "has empty current result description",
		},
		{
			name: "empty expected outcome",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedOutcome = ""
			},
			wantErr: "has empty expected outcome",
		},
		{
			name: "invalid expected status",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedStatus = 99
			},
			wantErr: "has invalid expected status",
		},
		{
			name: "missing trace invariant",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedRules = nil
				c.ExpectedFacts = nil
			},
			wantErr: "must define at least one expected trace rule or semantic fact",
		},
		{
			name: "empty invariants",
			mutate: func(c *netsimtest.Case) {
				c.Invariants = nil
			},
			wantErr: "must declare at least one reproducibility invariant",
		},
		{
			name: "non-complete status without issue codes",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedStatus = analysis.Incomplete
				c.ExpectedIssues = nil
				c.ExpectedIssueScopes = []analysis.Scope{analysis.WholeScope()}
			},
			wantErr: "has non-Complete expected status but no expected issue codes",
		},
		{
			name: "non-complete status without issue scopes",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedStatus = analysis.Incomplete
				c.ExpectedIssues = []analysis.IssueCode{"test.issue"}
				c.ExpectedIssueScopes = nil
			},
			wantErr: "has non-Complete expected status but no expected issue scopes",
		},
		{
			name: "nil execute function",
			mutate: func(c *netsimtest.Case) {
				c.Execute = nil
			},
			wantErr: "has nil execute function",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := validCase.Clone()
			tc.mutate(&c)
			err := netsimtest.ValidateCase(c)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if tc.wantErr != "" && !containsSubstring(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestRegistryDuplicateRejection(t *testing.T) {
	r := netsimtest.NewRegistry()
	c := netsimtest.CasePlanningPortVLANChange()

	if err := r.Register(c); err != nil {
		t.Fatalf("first registration failed: %v", err)
	}

	err := r.Register(c)
	if err == nil {
		t.Fatal("expected duplicate registration error, got nil")
	}
	if !containsSubstring(err.Error(), "duplicate corpus case ID") {
		t.Errorf("error %q does not mention duplicate ID", err.Error())
	}
}

func TestRegistryCopyIsolation(t *testing.T) {
	r := netsimtest.NewRegistry()
	c := netsimtest.CasePlanningPortVLANChange()
	r.MustRegister(c)

	retrieved, ok := r.Get(c.ID)
	if !ok {
		t.Fatalf("case %s not found in registry", c.ID)
	}

	// Mutate slice in retrieved copy.
	retrieved.ExpectedRules[0] = "mutated-rule"
	retrieved.Invariants = append(retrieved.Invariants, "mutated-invariant")

	// Fresh retrieval must remain unchanged.
	fresh, ok := r.Get(c.ID)
	if !ok {
		t.Fatalf("case %s not found on second retrieval", c.ID)
	}
	if fresh.ExpectedRules[0] == "mutated-rule" {
		t.Error("registry internal case was mutated through retrieved slice")
	}
	if len(fresh.Invariants) == len(retrieved.Invariants) {
		t.Error("registry internal invariants slice was mutated")
	}
}

func TestRegistryDeterministicOrdering(t *testing.T) {
	r := netsimtest.DefaultRegistry()
	allCases := r.All()

	if len(allCases) != 3 {
		t.Fatalf("DefaultRegistry contains %d cases, want 3", len(allCases))
	}

	for i := 1; i < len(allCases); i++ {
		if allCases[i-1].ID >= allCases[i].ID {
			t.Errorf("cases not strictly sorted by ID: %q >= %q", allCases[i-1].ID, allCases[i].ID)
		}
	}

	planningCases := r.ByUseCase(netsimtest.UseCasePlanning)
	if len(planningCases) != 1 || planningCases[0].ID != "planning/port-vlan-change" {
		t.Errorf("ByUseCase(Planning) returned %v", planningCases)
	}

	shadowingCases := r.ByUseCase(netsimtest.UseCaseTopologyShadowing)
	if len(shadowingCases) != 1 || shadowingCases[0].ID != "topology-shadowing/partial-model-unknown-port" {
		t.Errorf("ByUseCase(TopologyShadowing) returned %v", shadowingCases)
	}

	troubleshootingCases := r.ByUseCase(netsimtest.UseCaseTroubleshooting)
	if len(troubleshootingCases) != 1 || troubleshootingCases[0].ID != "troubleshooting/unicast-fdb-forwarding" {
		t.Errorf("ByUseCase(Troubleshooting) returned %v", troubleshootingCases)
	}
}

func TestExecutePlanningCase(t *testing.T) {
	c := netsimtest.CasePlanningPortVLANChange()
	res := netsimtest.AssertCase(t, c)

	if res.Comparison == nil {
		t.Fatal("res.Comparison is nil")
	}
	if res.Comparison.Same {
		t.Error("res.Comparison.Same = true, want false")
	}
	if res.Comparison.Current.Outcome != trace.Forwarded {
		t.Errorf("Current outcome = %v, want %v", res.Comparison.Current.Outcome, trace.Forwarded)
	}
	if res.Comparison.Expected.Outcome != trace.Dropped {
		t.Errorf("Expected outcome = %v, want %v", res.Comparison.Expected.Outcome, trace.Dropped)
	}

	// Verify typed diff facts.
	var foundPVID, foundUntagged bool
	for _, chg := range res.Changes {
		if chg.Field == "pvid" {
			if trace.EqualFact(chg.From, bridge.PVIDFact(10)) && trace.EqualFact(chg.To, bridge.PVIDFact(20)) {
				foundPVID = true
			}
		}
		if chg.Field == "untagged_vlan_ids" {
			if trace.EqualFact(chg.From, bridge.VLANsFact([]vlan.ID{10})) && trace.EqualFact(chg.To, bridge.VLANsFact([]vlan.ID{20})) {
				foundUntagged = true
			}
		}
	}
	if !foundPVID {
		t.Error("diff did not produce typed bridge.PVIDFact change from 10 to 20")
	}
	if !foundUntagged {
		t.Error("diff did not produce typed bridge.VLANsFact change from [10] to [20]")
	}
}

func TestExecuteShadowingCase(t *testing.T) {
	c := netsimtest.CaseShadowingPartialUnknownPort()
	res := netsimtest.AssertCase(t, c)

	if res.ModelResult == nil {
		t.Fatal("res.ModelResult is nil")
	}
	if res.ModelResult.Readiness() != analysis.Incomplete {
		t.Errorf("Readiness = %v, want %v", res.ModelResult.Readiness(), analysis.Incomplete)
	}

	// Localized scope: unknown port is incomplete, but sibling known-up port is complete.
	unknownScope := analysis.PortScope("shadow-sw1", "1/1/2")
	knownScope := analysis.PortScope("shadow-sw1", "1/1/1")

	if res.ModelResult.Metadata.StatusFor(unknownScope) != analysis.Incomplete {
		t.Errorf("StatusFor(%s) = %v, want %v", unknownScope, res.ModelResult.Metadata.StatusFor(unknownScope), analysis.Incomplete)
	}
	if res.ModelResult.Metadata.StatusFor(knownScope) != analysis.Complete {
		t.Errorf("StatusFor(%s) = %v, want %v", knownScope, res.ModelResult.Metadata.StatusFor(knownScope), analysis.Complete)
	}

	// Issue code inspection.
	issues := res.ModelResult.Metadata.Issues()
	var foundMissingOper bool
	for _, issue := range issues {
		if issue.Code == netmodel.IssueMissingOperStatus && issue.Scope.Overlaps(unknownScope) {
			foundMissingOper = true
		}
	}
	if !foundMissingOper {
		t.Errorf("missing expected IssueMissingOperStatus on %s", unknownScope)
	}
}

func TestExecuteTroubleshootingCase(t *testing.T) {
	c := netsimtest.CaseTroubleshootingUnicastForwarding()
	res := netsimtest.AssertCase(t, c)

	if res.Forward == nil {
		t.Fatal("res.Forward is nil")
	}
	if res.Forward.Outcome != trace.Forwarded {
		t.Errorf("Outcome = %v, want %v", res.Forward.Outcome, trace.Forwarded)
	}
	if len(res.Forward.Egress) != 1 {
		t.Fatalf("len(Egress) = %d, want 1", len(res.Forward.Egress))
	}
	if res.Forward.Egress[0].Port != "1/1/2" {
		t.Errorf("Egress port = %q, want 1/1/2", res.Forward.Egress[0].Port)
	}
	if len(res.Forward.Egress[0].Frame.Tags) != 1 || res.Forward.Egress[0].Frame.Tags[0].VID != 10 {
		t.Errorf("Egress frame tags = %v, want 1 tag with VID 10", res.Forward.Egress[0].Frame.Tags)
	}

	// Decisive lookup rule must be unicast-hit.
	var decisiveFound bool
	for _, step := range res.Steps {
		if step.Op == trace.OpLookup && step.RuleID == "unicast-hit" {
			decisiveFound = true
			if step.Subject.Kind != "mac" || step.Subject.Key != "00:11:22:33:44:02" {
				t.Errorf("unicast-hit subject = %s, want mac:00:11:22:33:44:02", step.Subject)
			}
		}
	}
	if !decisiveFound {
		t.Error("trace steps missing decisive unicast-hit lookup step")
	}
}

func containsSubstring(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && len(sub) > 0 && stringContains(s, sub)))
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
