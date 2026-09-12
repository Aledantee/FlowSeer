// Package netsimtest provides a versioned conformance test corpus and execution helpers
// for verifying network simulation analysis contracts.
package netsimtest

import (
	"errors"
	"fmt"
	"reflect"
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
// Metadata is the sole readiness authority; callers access the derived readiness status via Status().
type ExecutionResult struct {
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

// Status returns the analysis readiness status derived from Metadata.
func (r ExecutionResult) Status() analysis.Status {
	return r.Metadata.Status()
}

// FactExpectation represents an immutable, canonical expectation for a trace.Fact
// without retaining slices or mutable references that could alias across copies.
type FactExpectation struct {
	TypeID    string
	Canonical string
}

// NewFactExpectation creates a canonical fact expectation from a trace.Fact.
func NewFactExpectation(f trace.Fact) FactExpectation {
	if f == nil {
		return FactExpectation{}
	}
	return FactExpectation{
		TypeID:    f.TypeID(),
		Canonical: f.Canonical(),
	}
}

// Matches reports whether f satisfies this fact expectation.
func (e FactExpectation) Matches(f trace.Fact) bool {
	if f == nil {
		return false
	}
	return f.TypeID() == e.TypeID && f.Canonical() == e.Canonical
}

// StepExpectation is an immutable canonical snapshot of one expected trace step.
// The case's step expectations are matched in order and cover the complete trace.
type StepExpectation struct {
	Layer    trace.Layer
	Op       trace.Op
	RuleID   trace.RuleID
	Subject  trace.Subject
	Inputs   []FactExpectation
	Outputs  []FactExpectation
	Evidence []trace.EvidenceRef
}

// NewStepExpectation copies a trace step into its canonical expectation form.
func NewStepExpectation(step trace.Step) StepExpectation {
	canonical := step.Canonical()
	return StepExpectation{
		Layer:    canonical.Layer,
		Op:       canonical.Op,
		RuleID:   canonical.RuleID,
		Subject:  canonical.Subject,
		Inputs:   newFactExpectations(canonical.Inputs),
		Outputs:  newFactExpectations(canonical.Outputs),
		Evidence: slices.Clone(canonical.Evidence),
	}
}

// Canonical returns an independent expectation with facts and evidence in stable order.
func (e StepExpectation) Canonical() StepExpectation {
	e.Inputs = canonicalFactExpectations(e.Inputs)
	e.Outputs = canonicalFactExpectations(e.Outputs)
	e.Evidence = slices.Clone(e.Evidence)
	slices.Sort(e.Evidence)
	e.Evidence = slices.Compact(e.Evidence)
	return e
}

// Matches reports whether step has exactly the expected canonical semantics.
func (e StepExpectation) Matches(step trace.Step) bool {
	actual := NewStepExpectation(step)
	expected := e.Canonical()
	return actual.Layer == expected.Layer &&
		actual.Op == expected.Op &&
		actual.RuleID == expected.RuleID &&
		actual.Subject == expected.Subject &&
		slices.Equal(actual.Inputs, expected.Inputs) &&
		slices.Equal(actual.Outputs, expected.Outputs) &&
		slices.Equal(actual.Evidence, expected.Evidence)
}

// ChangeExpectation is an immutable canonical snapshot of one expected configuration change.
type ChangeExpectation struct {
	Layer    trace.Layer
	Subject  trace.Subject
	Field    string
	From     *FactExpectation
	To       *FactExpectation
	Evidence []trace.EvidenceRef
}

// NewChangeExpectation copies a trace change into its canonical expectation form.
func NewChangeExpectation(change trace.Change) ChangeExpectation {
	canonical := change.Canonical()
	return ChangeExpectation{
		Layer:    canonical.Layer,
		Subject:  canonical.Subject,
		Field:    canonical.Field,
		From:     newOptionalFactExpectation(canonical.From),
		To:       newOptionalFactExpectation(canonical.To),
		Evidence: slices.Clone(canonical.Evidence),
	}
}

// Canonical returns an independent expectation with evidence in stable order.
func (e ChangeExpectation) Canonical() ChangeExpectation {
	e.From = cloneFactExpectation(e.From)
	e.To = cloneFactExpectation(e.To)
	e.Evidence = slices.Clone(e.Evidence)
	slices.Sort(e.Evidence)
	e.Evidence = slices.Compact(e.Evidence)
	return e
}

// Matches reports whether change has exactly the expected canonical semantics.
func (e ChangeExpectation) Matches(change trace.Change) bool {
	actual := NewChangeExpectation(change)
	expected := e.Canonical()
	return actual.Layer == expected.Layer &&
		actual.Subject == expected.Subject &&
		actual.Field == expected.Field &&
		equalOptionalFactExpectation(actual.From, expected.From) &&
		equalOptionalFactExpectation(actual.To, expected.To) &&
		slices.Equal(actual.Evidence, expected.Evidence)
}

// MetadataExpectation identifies the exact readiness summary and evaluated scope of a result axis.
type MetadataExpectation struct {
	Status analysis.Status
	Scope  analysis.Scope
}

// ForwardExpectation identifies the exact trace and trust summary for one forwarding result axis.
type ForwardExpectation struct {
	Outcome  trace.Outcome
	Reason   trace.Reason
	Metadata MetadataExpectation
	Steps    []StepExpectation
}

// ComparisonExpectation identifies both forwarding axes and the comparison disposition.
type ComparisonExpectation struct {
	Current  ForwardExpectation
	Expected ForwardExpectation
	Same     bool
}

func newFactExpectations(facts []trace.Fact) []FactExpectation {
	if len(facts) == 0 {
		return nil
	}
	expectations := make([]FactExpectation, len(facts))
	for i, fact := range facts {
		expectations[i] = NewFactExpectation(fact)
	}
	return expectations
}

func canonicalFactExpectations(expectations []FactExpectation) []FactExpectation {
	if len(expectations) == 0 {
		return nil
	}
	canonical := slices.Clone(expectations)
	slices.SortFunc(canonical, func(a, b FactExpectation) int {
		if order := strings.Compare(a.TypeID, b.TypeID); order != 0 {
			return order
		}
		return strings.Compare(a.Canonical, b.Canonical)
	})
	return canonical
}

func newOptionalFactExpectation(fact trace.Fact) *FactExpectation {
	if fact == nil {
		return nil
	}
	expectation := NewFactExpectation(fact)
	return &expectation
}

func cloneFactExpectation(expectation *FactExpectation) *FactExpectation {
	if expectation == nil {
		return nil
	}
	clone := *expectation
	return &clone
}

func equalOptionalFactExpectation(a, b *FactExpectation) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// StatusPtr returns a pointer to an analysis.Status for explicit, copy-safe status expectations.
func StatusPtr(s analysis.Status) *analysis.Status {
	return &s
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
	// It must be explicitly specified (non-nil) so omitted values are rejected
	// rather than defaulting to analysis.Complete.
	ExpectedStatus *analysis.Status

	// ExpectedOutcome is the expected domain forwarding outcome.
	ExpectedOutcome trace.Outcome

	// ExpectedReason is the exact domain reason; an empty value is meaningful for normal forwarding.
	ExpectedReason trace.Reason

	// ExpectedRules names decisive rule identifiers bound by ExpectedSteps.
	ExpectedRules []trace.RuleID

	// ExpectedSubjects names decisive subjects bound by ExpectedSteps or ExpectedChanges.
	ExpectedSubjects []trace.Subject

	// ExpectedFacts names canonical facts bound by ExpectedSteps or ExpectedChanges.
	ExpectedFacts []FactExpectation

	// ExpectedSteps defines the complete ordered semantic trace.
	ExpectedSteps []StepExpectation

	// ExpectedChanges defines the complete ordered configuration diff.
	ExpectedChanges []ChangeExpectation

	// ExpectedIssues names issue codes that must be present when ExpectedStatus is non-Complete.
	ExpectedIssues []analysis.IssueCode

	// ExpectedIssueScopes pairs by index with ExpectedIssues and requires exact scope equality.
	ExpectedIssueScopes []analysis.Scope

	// ExpectedEvidenceRefs names the complete evidence catalog when ExpectedStatus is non-Complete.
	ExpectedEvidenceRefs []trace.EvidenceRef

	// ExpectedAssumptions names the complete set of assumption statements.
	ExpectedAssumptions []string

	// ExpectedComparison defines both forwarding axes when the case returns a comparison.
	ExpectedComparison *ComparisonExpectation

	// ExpectedModelMetadata defines the model-loading trust axis when present.
	ExpectedModelMetadata *MetadataExpectation

	// ExpectedForwardMetadata defines the forwarding trust axis when present.
	ExpectedForwardMetadata *MetadataExpectation

	// Execute runs the case against the library and returns the execution result.
	Execute func() (ExecutionResult, error)
}

// Clone returns an independent deep copy of the case to preserve immutability.
func (c Case) Clone() Case {
	cp := c
	if c.ExpectedStatus != nil {
		s := *c.ExpectedStatus
		cp.ExpectedStatus = &s
	}
	if len(c.ExpectedRules) > 0 {
		cp.ExpectedRules = slices.Clone(c.ExpectedRules)
	}
	if len(c.ExpectedSubjects) > 0 {
		cp.ExpectedSubjects = slices.Clone(c.ExpectedSubjects)
	}
	if len(c.ExpectedFacts) > 0 {
		cp.ExpectedFacts = slices.Clone(c.ExpectedFacts)
	}
	if len(c.ExpectedSteps) > 0 {
		cp.ExpectedSteps = cloneStepExpectations(c.ExpectedSteps)
	}
	if len(c.ExpectedChanges) > 0 {
		cp.ExpectedChanges = cloneChangeExpectations(c.ExpectedChanges)
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
	if c.ExpectedComparison != nil {
		expectation := cloneComparisonExpectation(*c.ExpectedComparison)
		cp.ExpectedComparison = &expectation
	}
	if c.ExpectedModelMetadata != nil {
		expectation := *c.ExpectedModelMetadata
		cp.ExpectedModelMetadata = &expectation
	}
	if c.ExpectedForwardMetadata != nil {
		expectation := *c.ExpectedForwardMetadata
		cp.ExpectedForwardMetadata = &expectation
	}
	return cp
}

func cloneStepExpectations(expectations []StepExpectation) []StepExpectation {
	clones := make([]StepExpectation, len(expectations))
	for i, expectation := range expectations {
		clones[i] = expectation.Canonical()
	}
	return clones
}

func cloneChangeExpectations(expectations []ChangeExpectation) []ChangeExpectation {
	clones := make([]ChangeExpectation, len(expectations))
	for i, expectation := range expectations {
		clones[i] = expectation.Canonical()
	}
	return clones
}

func cloneForwardExpectation(expectation ForwardExpectation) ForwardExpectation {
	expectation.Steps = cloneStepExpectations(expectation.Steps)
	return expectation
}

func cloneComparisonExpectation(expectation ComparisonExpectation) ComparisonExpectation {
	expectation.Current = cloneForwardExpectation(expectation.Current)
	expectation.Expected = cloneForwardExpectation(expectation.Expected)
	return expectation
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
	if c.ExpectedStatus == nil {
		return fmt.Errorf("corpus case %q has unspecified expected status (must be explicitly set)", c.ID)
	}
	if *c.ExpectedStatus > analysis.Unsupported {
		return fmt.Errorf("corpus case %q has invalid expected status %d", c.ID, *c.ExpectedStatus)
	}
	if len(c.ExpectedRules) == 0 {
		return fmt.Errorf("corpus case %q must define at least one expected trace rule", c.ID)
	}
	if len(c.ExpectedSubjects) == 0 {
		return fmt.Errorf("corpus case %q must define at least one expected trace subject", c.ID)
	}
	if len(c.ExpectedFacts) == 0 {
		return fmt.Errorf("corpus case %q must define at least one expected semantic fact", c.ID)
	}
	if len(c.ExpectedSteps) == 0 {
		return fmt.Errorf("corpus case %q must define its ordered expected trace steps", c.ID)
	}
	for _, rule := range c.ExpectedRules {
		if strings.TrimSpace(string(rule)) == "" {
			return fmt.Errorf("corpus case %q has empty expected rule ID", c.ID)
		}
		if !stepsContainRule(c.ExpectedSteps, rule) {
			return fmt.Errorf("corpus case %q has expected rule %q without a matching step expectation", c.ID, rule)
		}
	}
	for _, subject := range c.ExpectedSubjects {
		if strings.TrimSpace(subject.String()) == "" {
			return fmt.Errorf("corpus case %q has empty expected subject", c.ID)
		}
		if !expectationsContainSubject(c.ExpectedSteps, c.ExpectedChanges, subject) {
			return fmt.Errorf("corpus case %q has expected subject %s without a matching step or change expectation", c.ID, subject)
		}
	}
	for _, fact := range c.ExpectedFacts {
		if strings.TrimSpace(fact.TypeID) == "" {
			return fmt.Errorf("corpus case %q has empty expected fact type ID", c.ID)
		}
		if !expectationsContainFact(c.ExpectedSteps, c.ExpectedChanges, fact) {
			return fmt.Errorf("corpus case %q has expected fact %s (%s) without a matching step or change expectation", c.ID, fact.TypeID, fact.Canonical)
		}
	}
	for i, step := range c.ExpectedSteps {
		if step.Op == "" || step.RuleID == "" || strings.TrimSpace(step.Subject.String()) == "" {
			return fmt.Errorf("corpus case %q has incomplete expected step at index %d", c.ID, i)
		}
		for _, fact := range append(slices.Clone(step.Inputs), step.Outputs...) {
			if strings.TrimSpace(fact.TypeID) == "" {
				return fmt.Errorf("corpus case %q has empty fact type ID in expected step at index %d", c.ID, i)
			}
		}
	}
	for i, change := range c.ExpectedChanges {
		if strings.TrimSpace(change.Subject.String()) == "" || strings.TrimSpace(change.Field) == "" {
			return fmt.Errorf("corpus case %q has incomplete expected change at index %d", c.ID, i)
		}
		for _, fact := range []*FactExpectation{change.From, change.To} {
			if fact != nil && strings.TrimSpace(fact.TypeID) == "" {
				return fmt.Errorf("corpus case %q has empty fact type ID in expected change at index %d", c.ID, i)
			}
		}
	}
	if *c.ExpectedStatus != analysis.Complete {
		if len(c.ExpectedIssues) == 0 {
			return fmt.Errorf("corpus case %q has non-Complete expected status but no expected issue codes", c.ID)
		}
		if len(c.ExpectedIssueScopes) == 0 {
			return fmt.Errorf("corpus case %q has non-Complete expected status but no expected issue scopes", c.ID)
		}
		if len(c.ExpectedIssues) != len(c.ExpectedIssueScopes) {
			return fmt.Errorf("corpus case %q must pair each expected issue code with one exact scope", c.ID)
		}
		if len(c.ExpectedEvidenceRefs) == 0 {
			return fmt.Errorf("corpus case %q has non-Complete expected status but no expected evidence references", c.ID)
		}
	}
	for _, code := range c.ExpectedIssues {
		if strings.TrimSpace(string(code)) == "" {
			return fmt.Errorf("corpus case %q has empty expected issue code", c.ID)
		}
	}
	for _, a := range c.ExpectedAssumptions {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("corpus case %q has empty expected assumption statement", c.ID)
		}
	}
	if c.Execute == nil {
		return fmt.Errorf("corpus case %q has nil execute function", c.ID)
	}
	return nil
}

func stepsContainRule(steps []StepExpectation, rule trace.RuleID) bool {
	return slices.ContainsFunc(steps, func(step StepExpectation) bool {
		return step.RuleID == rule
	})
}

func expectationsContainSubject(steps []StepExpectation, changes []ChangeExpectation, subject trace.Subject) bool {
	return slices.ContainsFunc(steps, func(step StepExpectation) bool {
		return step.Subject == subject
	}) || slices.ContainsFunc(changes, func(change ChangeExpectation) bool {
		return change.Subject == subject
	})
}

func expectationsContainFact(steps []StepExpectation, changes []ChangeExpectation, fact FactExpectation) bool {
	for _, step := range steps {
		if slices.Contains(step.Inputs, fact) || slices.Contains(step.Outputs, fact) {
			return true
		}
	}
	return slices.ContainsFunc(changes, func(change ChangeExpectation) bool {
		return equalOptionalFactExpectation(change.From, &fact) || equalOptionalFactExpectation(change.To, &fact)
	})
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

// AssertCase executes a corpus case and asserts its declared semantic and trust contracts.
func AssertCase(t testing.TB, c Case) ExecutionResult {
	t.Helper()

	if err := ValidateCase(c); err != nil {
		t.Fatalf("case %s failed admission validation: %v", c.ID, err)
	}

	res1, err := c.Execute()
	if err != nil {
		t.Fatalf("case %s execution failed: %v", c.ID, err)
	}

	if res1.Status() != *c.ExpectedStatus {
		t.Errorf("case %s status = %v, want %v", c.ID, res1.Status(), *c.ExpectedStatus)
	}
	if res1.Outcome != c.ExpectedOutcome {
		t.Errorf("case %s outcome = %v, want %v", c.ID, res1.Outcome, c.ExpectedOutcome)
	}
	if res1.Reason != c.ExpectedReason {
		t.Errorf("case %s reason = %v, want %v", c.ID, res1.Reason, c.ExpectedReason)
	}
	assertExpectedSteps(t, c.ID, res1.Steps, c.ExpectedSteps)
	assertExpectedChanges(t, c.ID, res1.Changes, c.ExpectedChanges)

	if *c.ExpectedStatus != analysis.Complete {
		issues := res1.Metadata.Issues()
		if len(issues) != len(c.ExpectedIssues) {
			t.Errorf("case %s issue count = %d, want %d", c.ID, len(issues), len(c.ExpectedIssues))
		}
		matched := make([]bool, len(issues))
		for i, wantCode := range c.ExpectedIssues {
			wantScope := c.ExpectedIssueScopes[i]
			found := false
			for j, issue := range issues {
				if !matched[j] && issue.Code == wantCode && issue.Scope.Compare(wantScope) == 0 {
					matched[j] = true
					found = true
					break
				}
			}
			if !found {
				t.Errorf("case %s missing expected issue %q at exact scope %s", c.ID, wantCode, wantScope)
			}
		}
	}

	catalog := res1.Metadata.Evidence()
	if entries := catalog.Entries(); len(entries) != len(c.ExpectedEvidenceRefs) {
		t.Errorf("case %s evidence entry count = %d, want %d", c.ID, len(entries), len(c.ExpectedEvidenceRefs))
	}
	issues := res1.Metadata.Issues()
	actualAssumptions := res1.Metadata.Assumptions()
	for _, wantRef := range c.ExpectedEvidenceRefs {
		if _, ok := catalog.Lookup(wantRef); !ok {
			t.Errorf("case %s missing expected evidence reference %q in metadata catalog", c.ID, wantRef)
		}
		attached := false
		for _, issue := range issues {
			if slices.Contains(issue.Evidence, wantRef) {
				attached = true
				break
			}
		}
		if !attached {
			for _, assumption := range actualAssumptions {
				if slices.Contains(assumption.Evidence, wantRef) {
					attached = true
					break
				}
			}
		}
		if !attached {
			t.Errorf("case %s expected evidence reference %q is not attached to any issue or assumption", c.ID, wantRef)
		}
	}

	if len(actualAssumptions) != len(c.ExpectedAssumptions) {
		t.Errorf("case %s assumption count = %d, want %d", c.ID, len(actualAssumptions), len(c.ExpectedAssumptions))
	}
	for _, wantAssumption := range c.ExpectedAssumptions {
		found := false
		for _, actual := range actualAssumptions {
			if actual.Statement == wantAssumption {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("case %s missing expected assumption statement %q", c.ID, wantAssumption)
		}
	}

	assertOptionalComparisonExpectation(t, c.ID, res1.Comparison, c.ExpectedComparison)
	assertOptionalMetadataExpectation(t, c.ID, "model", metadataFromModelResult(res1.ModelResult), c.ExpectedModelMetadata)
	assertOptionalMetadataExpectation(t, c.ID, "forward", metadataFromForwardResult(res1.Forward), c.ExpectedForwardMetadata)

	res2, err := c.Execute()
	if err != nil {
		t.Fatalf("case %s repeated execution failed: %v", c.ID, err)
	}
	assertDeterministicExecution(t, c.ID, res1, res2)

	return res1
}

func assertExpectedSteps(t testing.TB, caseID string, actual []trace.Step, expected []StepExpectation) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Errorf("case %s step count = %d, want %d", caseID, len(actual), len(expected))
		return
	}
	for i := range expected {
		if !expected[i].Matches(actual[i]) {
			t.Errorf("case %s step at index %d = %s, want exact structured expectation", caseID, i, trace.RenderStep(actual[i]))
		}
	}
}

func assertExpectedChanges(t testing.TB, caseID string, actual []trace.Change, expected []ChangeExpectation) {
	t.Helper()
	if len(actual) != len(expected) {
		t.Errorf("case %s change count = %d, want %d", caseID, len(actual), len(expected))
		return
	}
	for i := range expected {
		if !expected[i].Matches(actual[i]) {
			t.Errorf("case %s change at index %d = %s, want exact structured expectation", caseID, i, trace.RenderChange(actual[i]))
		}
	}
}

func assertOptionalComparisonExpectation(t testing.TB, caseID string, actual *vswitch.Comparison, expected *ComparisonExpectation) {
	t.Helper()
	if actual == nil || expected == nil {
		if actual != nil || expected != nil {
			t.Errorf("case %s comparison presence does not match its structured expectation", caseID)
		}
		return
	}
	if actual.Same != expected.Same {
		t.Errorf("case %s comparison disposition = %t, want %t", caseID, actual.Same, expected.Same)
	}
	assertForwardExpectation(t, caseID, "comparison current", actual.Current, expected.Current)
	assertForwardExpectation(t, caseID, "comparison expected", actual.Expected, expected.Expected)
}

func assertForwardExpectation(t testing.TB, caseID, axis string, actual vswitch.ForwardResult, expected ForwardExpectation) {
	t.Helper()
	if actual.Outcome != expected.Outcome {
		t.Errorf("case %s %s outcome = %s, want %s", caseID, axis, actual.Outcome, expected.Outcome)
	}
	if actual.Reason != expected.Reason {
		t.Errorf("case %s %s reason = %s, want %s", caseID, axis, actual.Reason, expected.Reason)
	}
	assertMetadataExpectation(t, caseID, axis, actual.Metadata, expected.Metadata)
	assertExpectedSteps(t, caseID+" "+axis, actual.Steps, expected.Steps)
}

func metadataFromModelResult(result *netmodel.Result) *analysis.Metadata {
	if result == nil {
		return nil
	}
	return &result.Metadata
}

func metadataFromForwardResult(result *vswitch.ForwardResult) *analysis.Metadata {
	if result == nil {
		return nil
	}
	return &result.Metadata
}

func assertOptionalMetadataExpectation(t testing.TB, caseID, axis string, actual *analysis.Metadata, expected *MetadataExpectation) {
	t.Helper()
	if actual == nil || expected == nil {
		if actual != nil || expected != nil {
			t.Errorf("case %s %s metadata presence does not match its structured expectation", caseID, axis)
		}
		return
	}
	assertMetadataExpectation(t, caseID, axis, *actual, *expected)
}

func assertMetadataExpectation(t testing.TB, caseID, axis string, actual analysis.Metadata, expected MetadataExpectation) {
	t.Helper()
	if actual.Status() != expected.Status {
		t.Errorf("case %s %s status = %s, want %s", caseID, axis, actual.Status(), expected.Status)
	}
	if actual.Scope().Compare(expected.Scope) != 0 {
		t.Errorf("case %s %s scope = %s, want %s", caseID, axis, actual.Scope(), expected.Scope)
	}
}

func assertDeterministicExecution(t testing.TB, caseID string, first, second ExecutionResult) {
	t.Helper()
	if first.Outcome != second.Outcome {
		t.Errorf("case %s non-deterministic outcome across runs: %v vs %v", caseID, first.Outcome, second.Outcome)
	}
	if first.Reason != second.Reason {
		t.Errorf("case %s non-deterministic reason across runs: %v vs %v", caseID, first.Reason, second.Reason)
	}
	assertDeterministicSteps(t, caseID, "result", first.Steps, second.Steps)
	assertDeterministicChanges(t, caseID, first.Changes, second.Changes)
	assertDeterministicMetadata(t, caseID, "result", first.Metadata, second.Metadata)
	assertDeterministicComparison(t, caseID, first.Comparison, second.Comparison)
	assertDeterministicModelResult(t, caseID, first.ModelResult, second.ModelResult)
	assertDeterministicForwardResult(t, caseID, "forward", first.Forward, second.Forward)
}

func assertDeterministicSteps(t testing.TB, caseID, axis string, first, second []trace.Step) {
	t.Helper()
	if len(first) != len(second) {
		t.Errorf("case %s non-deterministic %s step count across runs: %d vs %d", caseID, axis, len(first), len(second))
		return
	}
	for i := range first {
		if !first[i].Equal(second[i]) {
			t.Errorf("case %s non-deterministic %s step at index %d across runs", caseID, axis, i)
		}
	}
}

func assertDeterministicChanges(t testing.TB, caseID string, first, second []trace.Change) {
	t.Helper()
	if len(first) != len(second) {
		t.Errorf("case %s non-deterministic change count across runs: %d vs %d", caseID, len(first), len(second))
		return
	}
	for i := range first {
		if !first[i].Equal(second[i]) {
			t.Errorf("case %s non-deterministic change at index %d across runs", caseID, i)
		}
	}
}

func assertDeterministicMetadata(t testing.TB, caseID, axis string, first, second analysis.Metadata) {
	t.Helper()
	if first.Scope().Compare(second.Scope()) != 0 {
		t.Errorf("case %s non-deterministic %s metadata scope across runs: %s vs %s", caseID, axis, first.Scope(), second.Scope())
	}
	if first.Status() != second.Status() {
		t.Errorf("case %s non-deterministic %s metadata status across runs: %s vs %s", caseID, axis, first.Status(), second.Status())
	}
	if !reflect.DeepEqual(first.Issues(), second.Issues()) {
		t.Errorf("case %s non-deterministic %s metadata issues across runs", caseID, axis)
	}
	if !slices.Equal(first.Evidence().Entries(), second.Evidence().Entries()) {
		t.Errorf("case %s non-deterministic %s metadata evidence across runs", caseID, axis)
	}
	if !reflect.DeepEqual(first.Assumptions(), second.Assumptions()) {
		t.Errorf("case %s non-deterministic %s metadata assumptions across runs", caseID, axis)
	}
}

func assertDeterministicComparison(t testing.TB, caseID string, first, second *vswitch.Comparison) {
	t.Helper()
	if first == nil || second == nil {
		if first != nil || second != nil {
			t.Errorf("case %s non-deterministic comparison presence across runs", caseID)
		}
		return
	}
	if first.Same != second.Same {
		t.Errorf("case %s non-deterministic comparison disposition across runs: %t vs %t", caseID, first.Same, second.Same)
	}
	assertDeterministicForwardResult(t, caseID, "comparison current", &first.Current, &second.Current)
	assertDeterministicForwardResult(t, caseID, "comparison expected", &first.Expected, &second.Expected)
}

func assertDeterministicModelResult(t testing.TB, caseID string, first, second *netmodel.Result) {
	t.Helper()
	if first == nil || second == nil {
		if first != nil || second != nil {
			t.Errorf("case %s non-deterministic model result presence across runs", caseID)
		}
		return
	}
	if !first.Spec.Equal(second.Spec) {
		t.Errorf("case %s non-deterministic model construction specification across runs", caseID)
	}
	if !reflect.DeepEqual(first.Report, second.Report) {
		t.Errorf("case %s non-deterministic model report across runs", caseID)
	}
	assertDeterministicMetadata(t, caseID, "model", first.Metadata, second.Metadata)
}

func assertDeterministicForwardResult(t testing.TB, caseID, axis string, first, second *vswitch.ForwardResult) {
	t.Helper()
	if first == nil || second == nil {
		if first != nil || second != nil {
			t.Errorf("case %s non-deterministic %s result presence across runs", caseID, axis)
		}
		return
	}
	domainEqual := trace.Equal(first.Trace, second.Trace) &&
		first.Ingress == second.Ingress &&
		first.FID == second.FID &&
		reflect.DeepEqual(first.Egress, second.Egress) &&
		reflect.DeepEqual(first.ConsultedPorts(), second.ConsultedPorts())
	if !domainEqual {
		t.Errorf("case %s non-deterministic %s domain result across runs", caseID, axis)
	}
	assertDeterministicMetadata(t, caseID, axis, first.Metadata, second.Metadata)
}
