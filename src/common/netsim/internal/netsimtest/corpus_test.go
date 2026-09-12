package netsimtest_test

import (
	"fmt"
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
			name: "unspecified expected metadata",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedMetadata = nil
			},
			wantErr: "has unspecified expected metadata",
		},
		{
			name: "invalid expected status",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedMetadata.Status = 99
			},
			wantErr: "has invalid result metadata status",
		},
		{
			name: "missing expected rules",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedRules = nil
			},
			wantErr: "must define at least one expected trace rule",
		},
		{
			name: "missing expected subjects",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSubjects = nil
			},
			wantErr: "must define at least one expected trace subject",
		},
		{
			name: "missing expected facts",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedFacts = nil
			},
			wantErr: "must define at least one expected semantic fact",
		},
		{
			name: "missing ordered expected steps",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSteps = nil
			},
			wantErr: "must define its ordered expected trace steps",
		},
		{
			name: "step without layer",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSteps[0].Layer = ""
			},
			wantErr: "incomplete expected step",
		},
		{
			name: "step without operation",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSteps[0].Op = ""
			},
			wantErr: "incomplete expected step",
		},
		{
			name: "step without rule",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSteps[0].RuleID = ""
				c.ExpectedRules = c.ExpectedRules[1:]
			},
			wantErr: "incomplete expected step",
		},
		{
			name: "step without subject kind",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSteps[0].Subject.Kind = ""
			},
			wantErr: "incomplete expected step",
		},
		{
			name: "step without subject key",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSteps[0].Subject.Key = ""
			},
			wantErr: "incomplete expected step",
		},
		{
			name: "change without layer",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedChanges[0].Layer = ""
			},
			wantErr: "incomplete expected change",
		},
		{
			name: "whole-subject change without key",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedChanges[0].Subject.Key = ""
				c.ExpectedChanges[0].Field = ""
			},
			wantErr: "incomplete expected change",
		},
		{
			name: "non-complete status without issues",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedMetadata.Status = analysis.Incomplete
				c.ExpectedMetadata.Issues = nil
			},
			wantErr: "has non-Complete result metadata but no expected issues",
		},
		{
			name: "non-complete side metadata without issues",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedForwardMetadata = &netsimtest.MetadataExpectation{
					Status: analysis.Incomplete,
					Scope:  analysis.WholeScope(),
				}
			},
			wantErr: "has non-Complete forward metadata but no expected issues",
		},
		{
			name: "primary issue without evidence",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedMetadata.Status = analysis.Incomplete
				c.ExpectedMetadata.Issues = []netsimtest.IssueExpectation{{
					Code: "test.issue", Status: analysis.Incomplete, Scope: c.ExpectedMetadata.Scope,
				}}
			},
			wantErr: "result metadata issue without evidence",
		},
		{
			name: "issue with invalid status",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedMetadata.Status = analysis.Incomplete
				c.ExpectedMetadata.Issues = []netsimtest.IssueExpectation{{
					Code: "test.issue", Status: analysis.Complete, Scope: analysis.WholeScope(), Evidence: []trace.EvidenceRef{"ev-1"},
				}}
			},
			wantErr: "invalid result metadata issue status",
		},
		{
			name: "empty issue code",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedMetadata.Status = analysis.Incomplete
				c.ExpectedMetadata.Issues = []netsimtest.IssueExpectation{{
					Status: analysis.Incomplete, Scope: analysis.WholeScope(), Evidence: []trace.EvidenceRef{"ev-1"},
				}}
			},
			wantErr: "empty result metadata issue code",
		},
		{
			name: "empty expected assumption statement",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedMetadata.Assumptions = []netsimtest.AssumptionExpectation{{
					Scope: analysis.WholeScope(), Statement: "   ", Evidence: []trace.EvidenceRef{"ev-1"},
				}}
			},
			wantErr: "has empty result metadata assumption",
		},
		{
			name: "empty expected rule ID",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedRules = []trace.RuleID{""}
			},
			wantErr: "has empty expected rule ID",
		},
		{
			name: "empty expected subject",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSubjects = []trace.Subject{{}}
			},
			wantErr: "has empty expected subject",
		},
		{
			name: "empty expected fact type ID",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedFacts = []netsimtest.FactExpectation{{TypeID: "  "}}
			},
			wantErr: "has empty expected fact type ID",
		},
		{
			name: "rule not bound to an expected step",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedRules = []trace.RuleID{"missing-rule"}
			},
			wantErr: "without a matching step expectation",
		},
		{
			name: "subject not bound to structured expectation",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedSubjects = []trace.Subject{{Kind: "port", Key: "missing"}}
			},
			wantErr: "without a matching step or change expectation",
		},
		{
			name: "fact not bound to structured expectation",
			mutate: func(c *netsimtest.Case) {
				c.ExpectedFacts = []netsimtest.FactExpectation{{TypeID: "missing.fact", Canonical: "missing"}}
			},
			wantErr: "without a matching step or change expectation",
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

func TestAdmissionRejectsMetadataStatusContradictions(t *testing.T) {
	localScope := analysis.PortScope("status-node", "local")
	otherScope := analysis.PortScope("status-node", "other")

	tests := []struct {
		name        string
		expectation netsimtest.MetadataExpectation
		wantErr     string
	}{
		{
			name: "complete with overlapping incomplete issue",
			expectation: netsimtest.MetadataExpectation{
				Status: analysis.Complete,
				Scope:  localScope,
				Issues: []netsimtest.IssueExpectation{{
					Code: "test.incomplete", Status: analysis.Incomplete, Scope: localScope,
				}},
			},
			wantErr: "metadata status complete, want incomplete derived",
		},
		{
			name: "lower precedence than overlapping issues",
			expectation: netsimtest.MetadataExpectation{
				Status: analysis.Incomplete,
				Scope:  analysis.NodeScope("status-node"),
				Issues: []netsimtest.IssueExpectation{
					{Code: "test.incomplete", Status: analysis.Incomplete, Scope: localScope},
					{Code: "test.unsupported", Status: analysis.Unsupported, Scope: otherScope},
				},
			},
			wantErr: "metadata status incomplete, want unsupported derived",
		},
		{
			name: "non-complete status justified only by disjoint issue",
			expectation: netsimtest.MetadataExpectation{
				Status: analysis.Incomplete,
				Scope:  localScope,
				Issues: []netsimtest.IssueExpectation{{
					Code: "test.incomplete", Status: analysis.Incomplete, Scope: otherScope,
				}},
			},
			wantErr: "metadata status incomplete, want complete derived",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := netsimtest.CasePlanningPortVLANChange()
			c.ExpectedForwardMetadata = &tc.expectation

			err := netsimtest.ValidateCase(c)
			if err == nil {
				t.Fatal("ValidateCase() error = nil, want metadata status contradiction")
			}
			if !containsSubstring(err.Error(), tc.wantErr) {
				t.Errorf("ValidateCase() error = %q, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestAdmissionRejectsPrimaryMetadataStatusPrecedenceContradiction(t *testing.T) {
	c := netsimtest.CasePlanningPortVLANChange()
	c.ExpectedMetadata.Status = analysis.Incomplete
	c.ExpectedMetadata.Scope = analysis.NodeScope("")
	c.ExpectedMetadata.Issues = []netsimtest.IssueExpectation{
		{Code: "test.incomplete", Status: analysis.Incomplete, Scope: analysis.PortScope("", "p1")},
		{Code: "test.unsupported", Status: analysis.Unsupported, Scope: analysis.PortScope("", "p2")},
	}

	err := netsimtest.ValidateCase(c)
	if err == nil {
		t.Fatal("ValidateCase() error = nil, want primary metadata status contradiction")
	}
	if !containsSubstring(err.Error(), "status incomplete, want unsupported derived") {
		t.Errorf("ValidateCase() error = %q, want status precedence contradiction", err)
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

	// Mutate fields in retrieved copy.
	retrieved.ExpectedRules[0] = "mutated-rule"
	retrieved.ExpectedSubjects[0] = trace.Subject{Kind: "mutated", Key: "mutated"}
	retrieved.ExpectedFacts[0] = netsimtest.FactExpectation{TypeID: "mutated", Canonical: "mutated"}
	retrieved.ExpectedSteps[0].Outputs[0] = netsimtest.FactExpectation{TypeID: "mutated", Canonical: "mutated"}
	retrieved.ExpectedChanges[0].From.TypeID = "mutated"
	retrieved.ExpectedComparison.Current.Steps[0].Inputs[0].TypeID = "mutated"
	retrieved.ExpectedMetadata.Scope = analysis.WholeScope()

	// Fresh retrieval must remain unchanged.
	fresh, ok := r.Get(c.ID)
	if !ok {
		t.Fatalf("case %s not found on second retrieval", c.ID)
	}
	if fresh.ExpectedRules[0] == "mutated-rule" {
		t.Error("registry internal case was mutated through retrieved ExpectedRules slice")
	}
	if fresh.ExpectedSubjects[0].Kind == "mutated" {
		t.Error("registry internal case was mutated through retrieved ExpectedSubjects slice")
	}
	if fresh.ExpectedFacts[0].TypeID == "mutated" {
		t.Error("registry internal case was mutated through retrieved ExpectedFacts slice")
	}
	if fresh.ExpectedSteps[0].Outputs[0].TypeID == "mutated" {
		t.Error("registry internal ExpectedSteps fact was mutated")
	}
	if fresh.ExpectedChanges[0].From.TypeID == "mutated" {
		t.Error("registry internal ExpectedChanges fact was mutated")
	}
	if fresh.ExpectedComparison.Current.Steps[0].Inputs[0].TypeID == "mutated" {
		t.Error("registry internal ExpectedComparison steps were mutated")
	}
	if fresh.ExpectedMetadata.Scope.Compare(analysis.NodeScope("")) != 0 {
		t.Error("registry internal expected metadata was mutated through pointer alias")
	}

	// Also verify non-Complete shadowing case isolation for issues, scopes, evidence, assumptions.
	cShadow := netsimtest.CaseShadowingPartialUnknownPort()
	r.MustRegister(cShadow)

	retrievedShadow, ok := r.Get(cShadow.ID)
	if !ok {
		t.Fatalf("case %s not found in registry", cShadow.ID)
	}
	retrievedShadow.ExpectedMetadata.Scope = analysis.WholeScope()
	retrievedShadow.ExpectedMetadata.Issues[0].Code = "mutated-issue"
	retrievedShadow.ExpectedMetadata.Issues[0].Evidence[0] = "mutated-issue-evidence"
	retrievedShadow.ExpectedMetadata.Evidence[0].Evidence.Context = "mutated-evidence"
	retrievedShadow.ExpectedMetadata.Assumptions[0].Scope = analysis.WholeScope()
	retrievedShadow.ExpectedMetadata.Assumptions[0].Statement = "mutated-assumption"
	retrievedShadow.ExpectedMetadata.Assumptions[0].Evidence[0] = "mutated-assumption-evidence"
	retrievedShadow.ExpectedModelMetadata.Scope = analysis.WholeScope()
	retrievedShadow.ExpectedModelMetadata.Issues[0].Message = "mutated-model-issue"
	retrievedShadow.ExpectedModelMetadata.Issues[0].Evidence[0] = "mutated-model-issue-evidence"
	retrievedShadow.ExpectedModelMetadata.Evidence[0].Evidence.Context = "mutated-model-evidence"
	retrievedShadow.ExpectedModelMetadata.Assumptions[0].Statement = "mutated-model-assumption"
	retrievedShadow.ExpectedModelMetadata.Assumptions[0].Evidence[0] = "mutated-model-assumption-evidence"
	retrievedShadow.ExpectedForwardMetadata.Scope = analysis.WholeScope()
	retrievedShadow.ExpectedForwardMetadata.Issues[0].Message = "mutated-forward-issue"
	retrievedShadow.ExpectedForwardMetadata.Evidence[0].Evidence.Context = "mutated-forward-evidence"
	retrievedShadow.ExpectedForwardMetadata.Assumptions[0].Statement = "mutated-forward-assumption"

	freshShadow, ok := r.Get(cShadow.ID)
	if !ok {
		t.Fatalf("case %s not found on second retrieval", cShadow.ID)
	}
	if freshShadow.ExpectedMetadata.Scope.Compare(analysis.WholeScope()) == 0 {
		t.Error("registry internal ExpectedMetadata scope was mutated")
	}
	if freshShadow.ExpectedMetadata.Issues[0].Code == "mutated-issue" ||
		freshShadow.ExpectedMetadata.Issues[0].Evidence[0] == "mutated-issue-evidence" ||
		freshShadow.ExpectedMetadata.Evidence[0].Evidence.Context == "mutated-evidence" ||
		freshShadow.ExpectedMetadata.Assumptions[0].Scope.Compare(analysis.WholeScope()) == 0 ||
		freshShadow.ExpectedMetadata.Assumptions[0].Statement == "mutated-assumption" ||
		freshShadow.ExpectedMetadata.Assumptions[0].Evidence[0] == "mutated-assumption-evidence" {
		t.Error("registry internal ExpectedMetadata contents were mutated")
	}
	if freshShadow.ExpectedModelMetadata.Scope == analysis.WholeScope() {
		t.Error("registry internal ExpectedModelMetadata was mutated")
	}
	if freshShadow.ExpectedModelMetadata.Issues[0].Message == "mutated-model-issue" ||
		freshShadow.ExpectedModelMetadata.Issues[0].Evidence[0] == "mutated-model-issue-evidence" ||
		freshShadow.ExpectedModelMetadata.Evidence[0].Evidence.Context == "mutated-model-evidence" ||
		freshShadow.ExpectedModelMetadata.Assumptions[0].Statement == "mutated-model-assumption" ||
		freshShadow.ExpectedModelMetadata.Assumptions[0].Evidence[0] == "mutated-model-assumption-evidence" {
		t.Error("registry internal ExpectedModelMetadata contents were mutated")
	}
	if freshShadow.ExpectedForwardMetadata.Scope == analysis.WholeScope() {
		t.Error("registry internal ExpectedForwardMetadata was mutated")
	}
	if freshShadow.ExpectedForwardMetadata.Issues[0].Message == "mutated-forward-issue" ||
		freshShadow.ExpectedForwardMetadata.Evidence[0].Evidence.Context == "mutated-forward-evidence" ||
		freshShadow.ExpectedForwardMetadata.Assumptions[0].Statement == "mutated-forward-assumption" {
		t.Error("registry internal ExpectedForwardMetadata contents were mutated")
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
		if issue.Code == netmodel.IssueMissingOperStatus && issue.Scope.Compare(unknownScope) == 0 {
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

func TestExecutionResultStatusDerivesFromMetadata(t *testing.T) {
	meta := analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{{
		Code:   "test.issue",
		Status: analysis.Incomplete,
	}}, analysis.EvidenceCatalog{}, nil)
	res := netsimtest.ExecutionResult{Metadata: meta}
	if res.Status() != analysis.Incomplete {
		t.Errorf("res.Status() = %v, want Incomplete (derived from Metadata)", res.Status())
	}
}

func TestAdmissionRequiresPrimaryMetadataEvidenceContents(t *testing.T) {
	c := netsimtest.CaseShadowingPartialUnknownPort()
	c.ExpectedMetadata.Evidence = nil
	err := netsimtest.ValidateCase(c)
	if err == nil {
		t.Fatal("expected error for metadata reference without evidence contents, got nil")
	}
	if !containsSubstring(err.Error(), "evidence") {
		t.Errorf("error %q does not mention evidence", err.Error())
	}
}

type recordingTB struct {
	testing.TB
	errors []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func TestAssertCaseVerifiesExactPrimaryEvidenceAndAssumptions(t *testing.T) {
	c := netsimtest.CaseShadowingPartialUnknownPort()
	origExecute := c.Execute
	c.Execute = func() (netsimtest.ExecutionResult, error) {
		res, err := origExecute()
		if err != nil {
			return res, err
		}
		issues := res.Metadata.Issues()
		assumptions := res.Metadata.Assumptions()
		issues[0].Evidence, assumptions[0].Evidence = assumptions[0].Evidence, issues[0].Evidence
		assumptions[0].Statement = "changed assumption"
		res.Metadata = analysis.NewMetadata(res.Metadata.Scope(), issues, res.Metadata.Evidence(), assumptions)
		return res, nil
	}

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)
	if !recordedErrorContains(rec, "result metadata does not match its exact structured expectation") {
		t.Errorf("AssertCase errors = %v, want exact primary metadata failure", rec.errors)
	}
}

func TestShadowingCasePopulatesEvidenceAndAssumptions(t *testing.T) {
	c := netsimtest.CaseShadowingPartialUnknownPort()
	for axis, metadata := range map[string]*netsimtest.MetadataExpectation{
		"result":  c.ExpectedMetadata,
		"model":   c.ExpectedModelMetadata,
		"forward": c.ExpectedForwardMetadata,
	} {
		if metadata == nil || len(metadata.Issues) == 0 || len(metadata.Evidence) == 0 || len(metadata.Assumptions) == 0 {
			t.Errorf("%s metadata must declare exact issues, evidence, and assumptions", axis)
		}
	}
}

func TestAssertCaseRequiresExactSideMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*netsimtest.MetadataExpectation)
	}{
		{
			name: "issue",
			mutate: func(metadata *netsimtest.MetadataExpectation) {
				metadata.Issues[0].Message = "changed side issue"
			},
		},
		{
			name: "assumption",
			mutate: func(metadata *netsimtest.MetadataExpectation) {
				metadata.Assumptions[0].Statement = "changed side assumption"
			},
		},
		{
			name: "evidence",
			mutate: func(metadata *netsimtest.MetadataExpectation) {
				var catalog analysis.EvidenceCatalog
				catalog, _ = catalog.Add(analysis.Evidence{
					Kind:    "test.side-metadata",
					Origin:  "corpus-test",
					Context: "extra expected evidence",
				})
				metadata.Evidence = append(metadata.Evidence, catalog.Entries()...)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c := netsimtest.CaseShadowingPartialUnknownPort()
			test.mutate(c.ExpectedModelMetadata)

			rec := &recordingTB{}
			netsimtest.AssertCase(rec, c)
			if !recordedErrorContains(rec, "model metadata does not match its exact structured expectation") {
				t.Errorf("AssertCase errors = %v, want exact model metadata failure", rec.errors)
			}
		})
	}
}

func TestAssertCaseRejectsUndeclaredCatalogEvidence(t *testing.T) {
	c := netsimtest.CasePlanningPortVLANChange()
	cat := analysis.EvidenceCatalog{}
	cat, _ = cat.Add(analysis.Evidence{
		Kind:    "test.kind",
		Origin:  "test-origin",
		Context: "unattached evidence",
	})
	origExecute := c.Execute
	c.Execute = func() (netsimtest.ExecutionResult, error) {
		res, err := origExecute()
		if err != nil {
			return res, err
		}
		res.Metadata = analysis.NewMetadata(res.Metadata.Scope(), res.Metadata.Issues(), cat, res.Metadata.Assumptions())
		return res, nil
	}

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)

	if !recordedErrorContains(rec, "result metadata does not match its exact structured expectation") {
		t.Errorf("AssertCase errors = %v, want exact primary metadata evidence failure", rec.errors)
	}
}

func TestAssertCaseRequiresExactAssumptionMatch(t *testing.T) {
	c := netsimtest.CaseShadowingPartialUnknownPort()
	c.ExpectedMetadata.Assumptions[0].Statement = "aging_time: 300s"

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)

	if !recordedErrorContains(rec, "result metadata does not match its exact structured expectation") {
		t.Errorf("AssertCase errors = %v, want exact primary assumption failure", rec.errors)
	}
}

func TestAssertCaseBindsTrustEvidenceToItsIssueAndAssumption(t *testing.T) {
	c := netsimtest.CaseShadowingPartialUnknownPort()
	origExecute := c.Execute
	c.Execute = func() (netsimtest.ExecutionResult, error) {
		res, err := origExecute()
		if err != nil {
			return res, err
		}
		issues := res.Metadata.Issues()
		assumptions := res.Metadata.Assumptions()
		issues[0].Evidence, assumptions[0].Evidence = assumptions[0].Evidence, issues[0].Evidence
		assumptions[0].Scope = analysis.WholeScope()
		res.Metadata = analysis.NewMetadata(res.Metadata.Scope(), issues, res.Metadata.Evidence(), assumptions)

		return res, nil
	}

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)
	if !recordedErrorContains(rec, "result metadata does not match its exact structured expectation") {
		t.Errorf("AssertCase errors = %v, want exact primary metadata binding failure", rec.errors)
	}
}

func TestAssertCaseRejectsReorderedDecisiveSteps(t *testing.T) {
	c := netsimtest.CaseTroubleshootingUnicastForwarding()
	origExecute := c.Execute
	c.Execute = func() (netsimtest.ExecutionResult, error) {
		res, err := origExecute()
		if err != nil {
			return res, err
		}
		res.Steps[0], res.Steps[1] = res.Steps[1], res.Steps[0]
		return res, nil
	}

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)
	if !recordedErrorContains(rec, "step at index") {
		t.Errorf("AssertCase errors = %v, want reordered-step failure", rec.errors)
	}
}

func TestAssertCaseRejectsFactOnWrongStep(t *testing.T) {
	c := netsimtest.CaseTroubleshootingUnicastForwarding()
	baseline, err := c.Execute()
	if err != nil {
		t.Fatalf("execute baseline: %v", err)
	}
	if len(baseline.Steps) < 2 || len(baseline.Steps[0].Outputs) == 0 {
		t.Fatal("baseline does not expose a movable output fact")
	}
	c.ExpectedFacts = []netsimtest.FactExpectation{
		netsimtest.NewFactExpectation(baseline.Steps[0].Outputs[0]),
	}

	origExecute := c.Execute
	c.Execute = func() (netsimtest.ExecutionResult, error) {
		res, err := origExecute()
		if err != nil {
			return res, err
		}
		fact := res.Steps[0].Outputs[0]
		res.Steps[0].Outputs = res.Steps[0].Outputs[1:]
		res.Steps[1].Outputs = append(res.Steps[1].Outputs, fact)
		return res, nil
	}

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)
	if !recordedErrorContains(rec, "step at index") {
		t.Errorf("AssertCase errors = %v, want wrong-step fact failure", rec.errors)
	}
}

func TestAssertCaseRejectsBroaderIssueScope(t *testing.T) {
	c := netsimtest.CaseShadowingPartialUnknownPort()
	origExecute := c.Execute
	c.Execute = func() (netsimtest.ExecutionResult, error) {
		res, err := origExecute()
		if err != nil {
			return res, err
		}
		issues := res.Metadata.Issues()
		issues[0].Scope = analysis.NodeScope("shadow-sw1")
		res.Metadata = analysis.NewMetadata(
			res.Metadata.Scope(),
			issues,
			res.Metadata.Evidence(),
			res.Metadata.Assumptions(),
		)
		return res, nil
	}

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)
	if !recordedErrorContains(rec, "result metadata does not match its exact structured expectation") {
		t.Errorf("AssertCase errors = %v, want exact primary issue-scope failure", rec.errors)
	}
}

func TestAssertCaseRejectsWrongPrimaryMetadataScope(t *testing.T) {
	c := netsimtest.CaseTroubleshootingUnicastForwarding()
	origExecute := c.Execute
	c.Execute = func() (netsimtest.ExecutionResult, error) {
		res, err := origExecute()
		if err != nil {
			return res, err
		}
		res.Metadata = analysis.NewMetadata(analysis.WholeScope(), res.Metadata.Issues(), res.Metadata.Evidence(), res.Metadata.Assumptions())
		return res, nil
	}

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)
	if !recordedErrorContains(rec, "result metadata does not match its exact structured expectation") {
		t.Errorf("AssertCase errors = %v, want primary metadata scope failure", rec.errors)
	}
}

func TestAssertCaseRejectsWrongPrimaryMetadataStatus(t *testing.T) {
	c := netsimtest.CaseTroubleshootingUnicastForwarding()
	origExecute := c.Execute
	c.Execute = func() (netsimtest.ExecutionResult, error) {
		res, err := origExecute()
		if err != nil {
			return res, err
		}
		res.Metadata = analysis.NewMetadata(res.Metadata.Scope(), []analysis.Issue{{
			Code:   "test.wrong-status",
			Status: analysis.Unsupported,
			Scope:  res.Metadata.Scope(),
		}}, analysis.EvidenceCatalog{}, nil)
		return res, nil
	}

	rec := &recordingTB{}
	netsimtest.AssertCase(rec, c)
	if !recordedErrorContains(rec, "result metadata does not match its exact structured expectation") {
		t.Errorf("AssertCase errors = %v, want primary metadata status failure", rec.errors)
	}
}

func TestAssertCaseComparesCompleteRepeatedResult(t *testing.T) {
	tests := []struct {
		name      string
		caseValue func() netsimtest.Case
		mutate    func(*netsimtest.ExecutionResult)
		wantError string
	}{
		{
			name:      "outcome",
			caseValue: netsimtest.CaseTroubleshootingUnicastForwarding,
			mutate: func(res *netsimtest.ExecutionResult) {
				res.Outcome = trace.Dropped
			},
			wantError: "non-deterministic outcome",
		},
		{
			name:      "reason",
			caseValue: netsimtest.CaseTroubleshootingUnicastForwarding,
			mutate: func(res *netsimtest.ExecutionResult) {
				res.Reason = "changed-reason"
			},
			wantError: "non-deterministic reason",
		},
		{
			name:      "steps with equal count",
			caseValue: netsimtest.CaseTroubleshootingUnicastForwarding,
			mutate: func(res *netsimtest.ExecutionResult) {
				res.Steps[0], res.Steps[1] = res.Steps[1], res.Steps[0]
			},
			wantError: "non-deterministic result step",
		},
		{
			name:      "changes with equal count",
			caseValue: netsimtest.CasePlanningPortVLANChange,
			mutate: func(res *netsimtest.ExecutionResult) {
				res.Changes[0].Field = "changed-field"
			},
			wantError: "non-deterministic change",
		},
		{
			name:      "metadata status",
			caseValue: netsimtest.CaseShadowingPartialUnknownPort,
			mutate: func(res *netsimtest.ExecutionResult) {
				issues := res.Metadata.Issues()
				issues[0].Status = analysis.Unsupported
				res.Metadata = analysis.NewMetadata(res.Metadata.Scope(), issues, res.Metadata.Evidence(), res.Metadata.Assumptions())
			},
			wantError: "non-deterministic result metadata status",
		},
		{
			name:      "metadata scope",
			caseValue: netsimtest.CaseShadowingPartialUnknownPort,
			mutate: func(res *netsimtest.ExecutionResult) {
				res.Metadata = analysis.NewMetadata(analysis.WholeScope(), res.Metadata.Issues(), res.Metadata.Evidence(), res.Metadata.Assumptions())
			},
			wantError: "non-deterministic result metadata scope",
		},
		{
			name:      "issues with equal count",
			caseValue: netsimtest.CaseShadowingPartialUnknownPort,
			mutate: func(res *netsimtest.ExecutionResult) {
				issues := res.Metadata.Issues()
				issues[0].Message = "changed issue message"
				res.Metadata = analysis.NewMetadata(res.Metadata.Scope(), issues, res.Metadata.Evidence(), res.Metadata.Assumptions())
			},
			wantError: "non-deterministic result metadata issues",
		},
		{
			name:      "assumptions with equal count",
			caseValue: netsimtest.CaseShadowingPartialUnknownPort,
			mutate: func(res *netsimtest.ExecutionResult) {
				assumptions := res.Metadata.Assumptions()
				assumptions[0].Statement = "changed assumption"
				res.Metadata = analysis.NewMetadata(res.Metadata.Scope(), res.Metadata.Issues(), res.Metadata.Evidence(), assumptions)
			},
			wantError: "non-deterministic result metadata assumptions",
		},
		{
			name:      "evidence contents with equal count",
			caseValue: netsimtest.CaseShadowingPartialUnknownPort,
			mutate: func(res *netsimtest.ExecutionResult) {
				var catalog analysis.EvidenceCatalog
				for i, entry := range res.Metadata.Evidence().Entries() {
					evidence := entry.Evidence
					if i == 0 {
						evidence.Context = "changed evidence context"
					}
					catalog, _ = catalog.Add(evidence)
				}
				res.Metadata = analysis.NewMetadata(res.Metadata.Scope(), res.Metadata.Issues(), catalog, res.Metadata.Assumptions())
			},
			wantError: "non-deterministic result metadata evidence",
		},
		{
			name:      "comparison current axis",
			caseValue: netsimtest.CasePlanningPortVLANChange,
			mutate: func(res *netsimtest.ExecutionResult) {
				res.Comparison.Current.Reason = "changed-current-reason"
			},
			wantError: "non-deterministic comparison current domain result",
		},
		{
			name:      "comparison expected axis",
			caseValue: netsimtest.CasePlanningPortVLANChange,
			mutate: func(res *netsimtest.ExecutionResult) {
				res.Comparison.Expected.Reason = "changed-expected-reason"
			},
			wantError: "non-deterministic comparison expected domain result",
		},
		{
			name:      "model metadata axis",
			caseValue: netsimtest.CaseShadowingPartialUnknownPort,
			mutate: func(res *netsimtest.ExecutionResult) {
				metadata := res.ModelResult.Metadata
				res.ModelResult.Metadata = analysis.NewMetadata(analysis.WholeScope(), metadata.Issues(), metadata.Evidence(), metadata.Assumptions())
			},
			wantError: "non-deterministic model metadata scope",
		},
		{
			name:      "forward metadata axis",
			caseValue: netsimtest.CaseShadowingPartialUnknownPort,
			mutate: func(res *netsimtest.ExecutionResult) {
				metadata := res.Forward.Metadata
				res.Forward.Metadata = analysis.NewMetadata(analysis.WholeScope(), metadata.Issues(), metadata.Evidence(), metadata.Assumptions())
			},
			wantError: "non-deterministic forward metadata scope",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.caseValue()
			origExecute := c.Execute
			executions := 0
			c.Execute = func() (netsimtest.ExecutionResult, error) {
				res, err := origExecute()
				if err != nil {
					return res, err
				}
				executions++
				if executions == 2 {
					tc.mutate(&res)
				}
				return res, nil
			}

			rec := &recordingTB{}
			netsimtest.AssertCase(rec, c)
			if !recordedErrorContains(rec, tc.wantError) {
				t.Errorf("AssertCase errors = %v, want one containing %q", rec.errors, tc.wantError)
			}
		})
	}
}

func TestCanonicalExpectationsDoNotRetainMutableFacts(t *testing.T) {
	ids := []vlan.ID{10}
	fact := bridge.VLANsFact(ids)
	expectation := netsimtest.NewFactExpectation(fact)
	stepExpectation := netsimtest.NewStepExpectation(trace.Step{Inputs: []trace.Fact{fact}})

	ids[0] = 20
	if expectation.Canonical != "10" {
		t.Errorf("fact expectation canonical value = %q, want %q", expectation.Canonical, "10")
	}
	if stepExpectation.Inputs[0].Canonical != "10" {
		t.Errorf("step expectation canonical input = %q, want %q", stepExpectation.Inputs[0].Canonical, "10")
	}
}

func recordedErrorContains(rec *recordingTB, substring string) bool {
	for _, recorded := range rec.errors {
		if containsSubstring(recorded, substring) {
			return true
		}
	}
	return false
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
