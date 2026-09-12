// Package netsimtest provides a versioned conformance test corpus and execution helpers
// for verifying network simulation analysis contracts.
package netsimtest

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
)

// SchemaVersion identifies the contract and schema version of the test corpus.
const SchemaVersion = "v1"

// UseCaseClass classifies the simulation goal of a corpus case.
type UseCaseClass string

const (
	// UseCasePlanning evaluates the behavioral impact of prospective configuration changes.
	UseCasePlanning UseCaseClass = "planning"

	// UseCaseTopologyShadowing evaluates device and network state under incomplete or noisy telemetry.
	UseCaseTopologyShadowing UseCaseClass = "topology-shadowing"

	// UseCaseTroubleshooting traces frame progression to isolate root causes and decisive rules.
	UseCaseTroubleshooting UseCaseClass = "troubleshooting"
)

// ExecutionResult captures the artifacts and outcomes produced by executing a corpus case.
type ExecutionResult struct {
	Status      analysis.Status
	Outcome     trace.Outcome
	Reason      trace.Reason
	Steps       []trace.Step
	Changes     []trace.Change
	Metadata    analysis.Metadata
	Switch      *vswitch.Switch
	ModelResult *netmodel.Result
	Comparison  *vswitch.Comparison
	Forward     *vswitch.ForwardResult
}

// Clone returns an independent copy of the execution result.
func (r ExecutionResult) Clone() ExecutionResult {
	cp := r
	if len(r.Steps) > 0 {
		cp.Steps = slices.Clone(r.Steps)
	}
	if len(r.Changes) > 0 {
		cp.Changes = slices.Clone(r.Changes)
	}
	return cp
}

// Case defines an admitted conformance case in the versioned test corpus.
// It pairs scenario metadata and execution logic with explicit trust and semantic invariants.
type Case struct {
	// ID uniquely identifies the case within the corpus (for example, "planning/port-vlan-change").
	ID string

	// UseCase identifies the simulation class.
	UseCase UseCaseClass

	// Question is the concrete operational or engineering question being evaluated.
	Question string

	// FalseAnswer describes the specific incorrect conclusion this case prevents.
	FalseAnswer string

	// CurrentResult summarizes the expected result produced by the current implementation.
	CurrentResult string

	// ExpectedStatus is the expected overall analysis readiness status.
	ExpectedStatus analysis.Status

	// ExpectedOutcome is the expected domain forwarding outcome.
	ExpectedOutcome trace.Outcome

	// ExpectedRules names decisive trace rule identifiers that must be present.
	ExpectedRules []trace.RuleID

	// ExpectedSubjects names decisive subjects that must be present in trace steps or changes.
	ExpectedSubjects []trace.Subject

	// ExpectedFacts names typed semantic facts that must be present in trace steps or changes.
	ExpectedFacts []trace.Fact

	// ExpectedIssues names issue codes that must be present when ExpectedStatus is non-Complete.
	ExpectedIssues []analysis.IssueCode

	// ExpectedIssueScopes names scopes that must be affected when ExpectedStatus is non-Complete.
	ExpectedIssueScopes []analysis.Scope

	// ExpectedEvidenceRefs names evidence references that must be present when ExpectedStatus is non-Complete.
	ExpectedEvidenceRefs []trace.EvidenceRef

	// ExpectedAssumptions names statements or keys of assumptions that must be present.
	ExpectedAssumptions []string

	// Invariants describes the deterministic invariants guaranteed by the case.
	Invariants []string

	// Execute runs the case against the library and returns the execution result.
	Execute func() (ExecutionResult, error)
}

// Clone returns an independent deep copy of the case to preserve immutability.
func (c Case) Clone() Case {
	cp := c
	if len(c.ExpectedRules) > 0 {
		cp.ExpectedRules = slices.Clone(c.ExpectedRules)
	}
	if len(c.ExpectedSubjects) > 0 {
		cp.ExpectedSubjects = slices.Clone(c.ExpectedSubjects)
	}
	if len(c.ExpectedFacts) > 0 {
		cp.ExpectedFacts = slices.Clone(c.ExpectedFacts)
	}
	if len(c.ExpectedIssues) > 0 {
		cp.ExpectedIssues = slices.Clone(c.ExpectedIssues)
	}
	if len(c.ExpectedIssueScopes) > 0 {
		cp.ExpectedIssueScopes = slices.Clone(c.ExpectedIssueScopes)
	}
	if len(c.ExpectedEvidenceRefs) > 0 {
		cp.ExpectedEvidenceRefs = slices.Clone(c.ExpectedEvidenceRefs)
	}
	if len(c.ExpectedAssumptions) > 0 {
		cp.ExpectedAssumptions = slices.Clone(c.ExpectedAssumptions)
	}
	if len(c.Invariants) > 0 {
		cp.Invariants = slices.Clone(c.Invariants)
	}
	return cp
}

// ValidateCase validates that an admitted case satisfies all required corpus fields and invariants.
func ValidateCase(c Case) error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("corpus case ID cannot be empty")
	}
	switch c.UseCase {
	case UseCasePlanning, UseCaseTopologyShadowing, UseCaseTroubleshooting:
	default:
		return fmt.Errorf("corpus case %q has unspecified or invalid use case class %q", c.ID, c.UseCase)
	}
	if strings.TrimSpace(c.Question) == "" {
		return fmt.Errorf("corpus case %q has empty question", c.ID)
	}
	if strings.TrimSpace(c.FalseAnswer) == "" {
		return fmt.Errorf("corpus case %q has empty false answer", c.ID)
	}
	if strings.TrimSpace(c.CurrentResult) == "" {
		return fmt.Errorf("corpus case %q has empty current result description", c.ID)
	}
	if c.ExpectedOutcome == "" {
		return fmt.Errorf("corpus case %q has empty expected outcome", c.ID)
	}
	if c.ExpectedStatus > analysis.Unsupported {
		return fmt.Errorf("corpus case %q has invalid expected status %d", c.ID, c.ExpectedStatus)
	}
	if len(c.ExpectedRules) == 0 && len(c.ExpectedFacts) == 0 {
		return fmt.Errorf("corpus case %q must define at least one expected trace rule or semantic fact", c.ID)
	}
	if len(c.Invariants) == 0 {
		return fmt.Errorf("corpus case %q must declare at least one reproducibility invariant", c.ID)
	}
	if c.ExpectedStatus != analysis.Complete {
		if len(c.ExpectedIssues) == 0 {
			return fmt.Errorf("corpus case %q has non-Complete expected status but no expected issue codes", c.ID)
		}
		if len(c.ExpectedIssueScopes) == 0 {
			return fmt.Errorf("corpus case %q has non-Complete expected status but no expected issue scopes", c.ID)
		}
	}
	if c.Execute == nil {
		return fmt.Errorf("corpus case %q has nil execute function", c.ID)
	}
	return nil
}

// Registry maintains an admitted collection of versioned corpus cases.
type Registry struct {
	cases map[string]Case
}

// NewRegistry creates a new empty corpus registry.
func NewRegistry() *Registry {
	return &Registry{
		cases: make(map[string]Case),
	}
}

// Register admits a case into the registry after validating admission criteria.
// It returns an error if the case fails admission or has a duplicate ID.
func (r *Registry) Register(c Case) error {
	if err := ValidateCase(c); err != nil {
		return err
	}
	if _, exists := r.cases[c.ID]; exists {
		return fmt.Errorf("duplicate corpus case ID: %q", c.ID)
	}
	r.cases[c.ID] = c.Clone()
	return nil
}

// MustRegister admits a case into the registry and panics if validation fails.
func (r *Registry) MustRegister(c Case) {
	if err := r.Register(c); err != nil {
		panic(err)
	}
}

// Get retrieves an independent copy of the case with the given ID.
func (r *Registry) Get(id string) (Case, bool) {
	c, ok := r.cases[id]
	if !ok {
		return Case{}, false
	}
	return c.Clone(), true
}

// All returns all admitted cases sorted deterministically by ID.
func (r *Registry) All() []Case {
	ids := make([]string, 0, len(r.cases))
	for id := range r.cases {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	out := make([]Case, len(ids))
	for i, id := range ids {
		out[i] = r.cases[id].Clone()
	}
	return out
}

// ByUseCase returns all admitted cases for a given use-case class, sorted by ID.
func (r *Registry) ByUseCase(u UseCaseClass) []Case {
	var out []Case
	for _, c := range r.All() {
		if c.UseCase == u {
			out = append(out, c)
		}
	}
	return out
}

// AssertCase executes a corpus case and asserts that its actual results satisfy
// all declared invariants, status, domain outcomes, decisive rules, facts, issues, and scopes.
func AssertCase(t testing.TB, c Case) ExecutionResult {
	t.Helper()

	if err := ValidateCase(c); err != nil {
		t.Fatalf("case %s failed admission validation: %v", c.ID, err)
	}

	res1, err := c.Execute()
	if err != nil {
		t.Fatalf("case %s execution failed: %v", c.ID, err)
	}

	if res1.Status != c.ExpectedStatus {
		t.Errorf("case %s status = %v, want %v", c.ID, res1.Status, c.ExpectedStatus)
	}
	if res1.Outcome != c.ExpectedOutcome {
		t.Errorf("case %s outcome = %v, want %v", c.ID, res1.Outcome, c.ExpectedOutcome)
	}

	for _, wantRule := range c.ExpectedRules {
		found := false
		for _, step := range res1.Steps {
			if step.RuleID == wantRule {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("case %s missing expected rule %q in steps", c.ID, wantRule)
		}
	}

	for _, wantSubject := range c.ExpectedSubjects {
		found := false
		for _, step := range res1.Steps {
			if step.Subject == wantSubject {
				found = true
				break
			}
		}
		if !found {
			for _, chg := range res1.Changes {
				if chg.Subject == wantSubject {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("case %s missing expected subject %s in steps or changes", c.ID, wantSubject)
		}
	}

	for _, wantFact := range c.ExpectedFacts {
		found := false
		for _, step := range res1.Steps {
			for _, in := range step.Inputs {
				if trace.EqualFact(in, wantFact) {
					found = true
					break
				}
			}
			for _, out := range step.Outputs {
				if trace.EqualFact(out, wantFact) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			for _, chg := range res1.Changes {
				if trace.EqualFact(chg.From, wantFact) || trace.EqualFact(chg.To, wantFact) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("case %s missing expected fact %s (%s)", c.ID, wantFact.TypeID(), wantFact.Canonical())
		}
	}

	if c.ExpectedStatus != analysis.Complete {
		issues := res1.Metadata.Issues()
		for _, wantCode := range c.ExpectedIssues {
			found := false
			for _, issue := range issues {
				if issue.Code == wantCode {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("case %s missing expected issue code %q", c.ID, wantCode)
			}
		}

		for _, wantScope := range c.ExpectedIssueScopes {
			found := false
			for _, issue := range issues {
				if issue.Scope.Overlaps(wantScope) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("case %s missing issue overlapping scope %s", c.ID, wantScope)
			}
		}
	}

	res2, err := c.Execute()
	if err != nil {
		t.Fatalf("case %s repeated execution failed: %v", c.ID, err)
	}
	if res1.Status != res2.Status {
		t.Errorf("case %s non-deterministic status across runs: %v vs %v", c.ID, res1.Status, res2.Status)
	}
	if res1.Outcome != res2.Outcome {
		t.Errorf("case %s non-deterministic outcome across runs: %v vs %v", c.ID, res1.Outcome, res2.Outcome)
	}
	if len(res1.Steps) != len(res2.Steps) {
		t.Errorf("case %s non-deterministic step count across runs: %d vs %d", c.ID, len(res1.Steps), len(res2.Steps))
	} else {
		for i := range res1.Steps {
			if !res1.Steps[i].Equal(res2.Steps[i]) {
				t.Errorf("case %s non-deterministic step at index %d across runs", c.ID, i)
				break
			}
		}
	}
	if len(res1.Changes) != len(res2.Changes) {
		t.Errorf("case %s non-deterministic change count across runs: %d vs %d", c.ID, len(res1.Changes), len(res2.Changes))
	} else {
		for i := range res1.Changes {
			if !res1.Changes[i].Equal(res2.Changes[i]) {
				t.Errorf("case %s non-deterministic change at index %d across runs", c.ID, i)
				break
			}
		}
	}

	return res1
}
