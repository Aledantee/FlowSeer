// Package netsimtest provides a versioned conformance test corpus and execution helpers
// for verifying network simulation analysis contracts.
package netsimtest

import (
	"cmp"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
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
//
// Journey and FabricMetadata are populated by a case built on a [fabric.Fabric]. Journey is the
// frame's recorded traversal, whose own Metadata is scoped to what that journey depended on.
// FabricMetadata is [fabric.Fabric.Metadata], scoped over the whole topology; it can carry issues
// a given journey never depended on, such as a sibling port's unresolved adjacency, so it is
// informational rather than part of the admitted exact-match contract.
type ExecutionResult struct {
	Outcome        trace.Outcome
	Reason         trace.Reason
	Steps          []trace.Step
	Changes        []trace.Change
	Metadata       analysis.Metadata
	Switch         *vswitch.Switch
	ModelResult    *netmodel.Result
	Comparison     *vswitch.Comparison
	Forward        *vswitch.ForwardResult
	Journey        *fabric.Journey
	FabricMetadata *analysis.Metadata
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

// IssueExpectation binds one issue's semantic identity, trust status, scope,
// and exact evidence references. Human-facing message wording is deliberately
// absent so corpus cases survive copy edits without weakening semantic checks.
type IssueExpectation struct {
	Code     analysis.IssueCode
	Status   analysis.Status
	Scope    analysis.Scope
	Evidence []trace.EvidenceRef
}

// NewIssueExpectation creates an exact canonical expectation from an analysis issue.
func NewIssueExpectation(issue analysis.Issue) IssueExpectation {
	return IssueExpectation{
		Code:     issue.Code,
		Status:   issue.Status,
		Scope:    issue.Scope,
		Evidence: canonicalEvidence(issue.Evidence),
	}
}

// Canonical returns an independent expectation with sorted, deduplicated evidence references.
func (e IssueExpectation) Canonical() IssueExpectation {
	e.Evidence = canonicalEvidence(e.Evidence)
	return e
}

// Matches reports whether issue has exactly the expected semantic trust metadata.
func (e IssueExpectation) Matches(issue analysis.Issue) bool {
	actual := NewIssueExpectation(issue)
	expected := e.Canonical()
	return actual.Code == expected.Code &&
		actual.Status == expected.Status &&
		actual.Scope.Compare(expected.Scope) == 0 &&
		slices.Equal(actual.Evidence, expected.Evidence)
}

// AssumptionExpectation binds one assumption's scope, statement, and exact evidence references.
type AssumptionExpectation struct {
	Scope     analysis.Scope
	Statement string
	Evidence  []trace.EvidenceRef
}

// NewAssumptionExpectation creates an exact canonical expectation from an analysis assumption.
func NewAssumptionExpectation(assumption analysis.Assumption) AssumptionExpectation {
	return AssumptionExpectation{
		Scope:     assumption.Scope,
		Statement: assumption.Statement,
		Evidence:  canonicalEvidence(assumption.Evidence),
	}
}

// Canonical returns an independent expectation with sorted, deduplicated evidence references.
func (e AssumptionExpectation) Canonical() AssumptionExpectation {
	e.Evidence = canonicalEvidence(e.Evidence)
	return e
}

// Matches reports whether assumption has exactly the expected semantic trust metadata.
func (e AssumptionExpectation) Matches(assumption analysis.Assumption) bool {
	actual := NewAssumptionExpectation(assumption)
	expected := e.Canonical()
	return actual.Scope.Compare(expected.Scope) == 0 &&
		actual.Statement == expected.Statement &&
		slices.Equal(actual.Evidence, expected.Evidence)
}

func canonicalEvidence(refs []trace.EvidenceRef) []trace.EvidenceRef {
	canonical := slices.Clone(refs)
	slices.Sort(canonical)
	return slices.Compact(canonical)
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

// MetadataExpectation identifies the exact trust metadata of a result axis.
type MetadataExpectation struct {
	Status      analysis.Status
	Scope       analysis.Scope
	Issues      []IssueExpectation
	Evidence    []analysis.EvidenceEntry
	Assumptions []AssumptionExpectation
}

// NewMetadataExpectation returns an independent structural expectation for metadata.
func NewMetadataExpectation(metadata analysis.Metadata) MetadataExpectation {
	issues := metadata.Issues()
	issueExpectations := make([]IssueExpectation, len(issues))
	for i, issue := range issues {
		issueExpectations[i] = NewIssueExpectation(issue)
	}
	assumptions := metadata.Assumptions()
	assumptionExpectations := make([]AssumptionExpectation, len(assumptions))
	for i, assumption := range assumptions {
		assumptionExpectations[i] = NewAssumptionExpectation(assumption)
	}

	return MetadataExpectation{
		Status:      metadata.Status(),
		Scope:       metadata.Scope(),
		Issues:      issueExpectations,
		Evidence:    slices.Clone(metadata.Evidence().Entries()),
		Assumptions: assumptionExpectations,
	}
}

// Canonical returns an independent expectation in deterministic metadata order.
func (e MetadataExpectation) Canonical() MetadataExpectation {
	e.Issues = cloneIssueExpectations(e.Issues)
	slices.SortFunc(e.Issues, compareIssueExpectations)
	e.Evidence = slices.Clone(e.Evidence)
	slices.SortFunc(e.Evidence, func(a, b analysis.EvidenceEntry) int {
		return strings.Compare(string(a.Ref), string(b.Ref))
	})
	e.Assumptions = cloneAssumptionExpectations(e.Assumptions)
	slices.SortFunc(e.Assumptions, compareAssumptionExpectations)
	return e
}

// Matches reports whether metadata has exactly the expected structural contents.
func (e MetadataExpectation) Matches(metadata analysis.Metadata) bool {
	return reflect.DeepEqual(NewMetadataExpectation(metadata).Canonical(), e.Canonical())
}

func compareIssueExpectations(a, b IssueExpectation) int {
	if order := a.Scope.Compare(b.Scope); order != 0 {
		return order
	}
	if order := strings.Compare(string(a.Code), string(b.Code)); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Status, b.Status); order != 0 {
		return order
	}
	return slices.Compare(a.Evidence, b.Evidence)
}

func compareAssumptionExpectations(a, b AssumptionExpectation) int {
	if order := a.Scope.Compare(b.Scope); order != 0 {
		return order
	}
	if order := strings.Compare(a.Statement, b.Statement); order != 0 {
		return order
	}
	return slices.Compare(a.Evidence, b.Evidence)
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

	// ExpectedMetadata defines the primary result's exact trust metadata.
	// It must be explicitly specified so an omitted zero value cannot silently
	// admit whole-analysis Complete metadata.
	ExpectedMetadata *MetadataExpectation

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
	if c.ExpectedMetadata != nil {
		expectation := c.ExpectedMetadata.Canonical()
		cp.ExpectedMetadata = &expectation
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
	if c.ExpectedComparison != nil {
		expectation := cloneComparisonExpectation(*c.ExpectedComparison)
		cp.ExpectedComparison = &expectation
	}
	if c.ExpectedModelMetadata != nil {
		expectation := c.ExpectedModelMetadata.Canonical()
		cp.ExpectedModelMetadata = &expectation
	}
	if c.ExpectedForwardMetadata != nil {
		expectation := c.ExpectedForwardMetadata.Canonical()
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

func cloneIssueExpectations(expectations []IssueExpectation) []IssueExpectation {
	clones := make([]IssueExpectation, len(expectations))
	for i, expectation := range expectations {
		clones[i] = expectation.Canonical()
	}
	return clones
}

func cloneAssumptionExpectations(expectations []AssumptionExpectation) []AssumptionExpectation {
	clones := make([]AssumptionExpectation, len(expectations))
	for i, expectation := range expectations {
		clones[i] = expectation.Canonical()
	}
	return clones
}

func cloneForwardExpectation(expectation ForwardExpectation) ForwardExpectation {
	expectation.Metadata = expectation.Metadata.Canonical()
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
	if c.ExpectedMetadata == nil {
		return fmt.Errorf("corpus case %q has unspecified expected metadata", c.ID)
	}
	if err := validateMetadataExpectation(c.ID, "result", *c.ExpectedMetadata); err != nil {
		return err
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
		if strings.TrimSpace(subject.Kind) == "" {
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
		if strings.TrimSpace(string(step.Layer)) == "" ||
			strings.TrimSpace(string(step.Op)) == "" ||
			strings.TrimSpace(string(step.RuleID)) == "" ||
			strings.TrimSpace(step.Subject.Kind) == "" ||
			strings.TrimSpace(step.Subject.Key) == "" {
			return fmt.Errorf("corpus case %q has incomplete expected step at index %d", c.ID, i)
		}
		for _, fact := range append(slices.Clone(step.Inputs), step.Outputs...) {
			if strings.TrimSpace(fact.TypeID) == "" {
				return fmt.Errorf("corpus case %q has empty fact type ID in expected step at index %d", c.ID, i)
			}
		}
	}
	for i, change := range c.ExpectedChanges {
		if strings.TrimSpace(string(change.Layer)) == "" ||
			strings.TrimSpace(change.Subject.Kind) == "" ||
			(strings.TrimSpace(change.Subject.Key) == "" && strings.TrimSpace(change.Field) == "") ||
			(change.From == nil && change.To == nil) {
			return fmt.Errorf("corpus case %q has incomplete expected change at index %d", c.ID, i)
		}
		for _, fact := range []*FactExpectation{change.From, change.To} {
			if fact != nil && strings.TrimSpace(fact.TypeID) == "" {
				return fmt.Errorf("corpus case %q has empty fact type ID in expected change at index %d", c.ID, i)
			}
		}
	}
	if c.ExpectedComparison != nil {
		if err := validateMetadataExpectation(c.ID, "comparison current", c.ExpectedComparison.Current.Metadata); err != nil {
			return err
		}
		if err := validateMetadataExpectation(c.ID, "comparison expected", c.ExpectedComparison.Expected.Metadata); err != nil {
			return err
		}
	}
	if c.ExpectedModelMetadata != nil {
		if err := validateMetadataExpectation(c.ID, "model", *c.ExpectedModelMetadata); err != nil {
			return err
		}
	}
	if c.ExpectedForwardMetadata != nil {
		if err := validateMetadataExpectation(c.ID, "forward", *c.ExpectedForwardMetadata); err != nil {
			return err
		}
	}
	if c.Execute == nil {
		return fmt.Errorf("corpus case %q has nil execute function", c.ID)
	}
	return nil
}

func validateMetadataExpectation(caseID, axis string, expectation MetadataExpectation) error {
	if expectation.Status > analysis.Unsupported {
		return fmt.Errorf("corpus case %q has invalid %s metadata status %d", caseID, axis, expectation.Status)
	}
	if expectation.Status != analysis.Complete && len(expectation.Issues) == 0 {
		return fmt.Errorf("corpus case %q has non-Complete %s metadata but no expected issues", caseID, axis)
	}
	for i, issue := range expectation.Issues {
		if strings.TrimSpace(string(issue.Code)) == "" {
			return fmt.Errorf("corpus case %q has empty %s metadata issue code at index %d", caseID, axis, i)
		}
		if issue.Status == analysis.Complete || issue.Status > analysis.Unsupported {
			return fmt.Errorf("corpus case %q has invalid %s metadata issue status at index %d", caseID, axis, i)
		}
		if len(issue.Evidence) == 0 {
			return fmt.Errorf("corpus case %q has %s metadata issue without evidence at index %d", caseID, axis, i)
		}
		if err := validateEvidenceRefs(caseID, axis+" metadata issue", i, issue.Evidence); err != nil {
			return err
		}
	}
	for i, assumption := range expectation.Assumptions {
		if strings.TrimSpace(assumption.Statement) == "" {
			return fmt.Errorf("corpus case %q has empty %s metadata assumption at index %d", caseID, axis, i)
		}
		if len(assumption.Evidence) == 0 {
			return fmt.Errorf("corpus case %q has %s metadata assumption without evidence at index %d", caseID, axis, i)
		}
		if err := validateEvidenceRefs(caseID, axis+" metadata assumption", i, assumption.Evidence); err != nil {
			return err
		}
	}

	entries := make(map[trace.EvidenceRef]struct{}, len(expectation.Evidence))
	for i, entry := range expectation.Evidence {
		if strings.TrimSpace(string(entry.Ref)) == "" {
			return fmt.Errorf("corpus case %q has empty %s metadata evidence reference at index %d", caseID, axis, i)
		}
		if _, duplicate := entries[entry.Ref]; duplicate {
			return fmt.Errorf("corpus case %q has duplicate %s metadata evidence reference %q", caseID, axis, entry.Ref)
		}
		catalog, ref := (analysis.EvidenceCatalog{}).Add(entry.Evidence)
		if _, ok := catalog.Lookup(entry.Ref); !ok || ref != entry.Ref {
			return fmt.Errorf("corpus case %q has %s metadata evidence whose reference does not match its contents at index %d", caseID, axis, i)
		}
		entries[entry.Ref] = struct{}{}
	}
	for _, ref := range expectedEvidenceRefs(expectation.Issues, expectation.Assumptions) {
		if _, ok := entries[ref]; !ok {
			return fmt.Errorf("corpus case %q has %s metadata reference %q without expected evidence contents", caseID, axis, ref)
		}
	}

	statusIssues := make([]analysis.Issue, len(expectation.Issues))
	for i, issue := range expectation.Issues {
		statusIssues[i] = analysis.Issue{Status: issue.Status, Scope: issue.Scope}
	}
	derivedStatus := analysis.NewMetadata(expectation.Scope, statusIssues, analysis.EvidenceCatalog{}, nil).Status()
	if expectation.Status != derivedStatus {
		return fmt.Errorf(
			"corpus case %q has %s metadata status %s, want %s derived from issues overlapping scope %s",
			caseID, axis, expectation.Status, derivedStatus, expectation.Scope,
		)
	}
	return nil
}

func validateEvidenceRefs(caseID, owner string, index int, refs []trace.EvidenceRef) error {
	for _, ref := range refs {
		if strings.TrimSpace(string(ref)) == "" {
			return fmt.Errorf("corpus case %q has empty evidence reference on expected %s at index %d", caseID, owner, index)
		}
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

	assertMetadataExpectation(t, c.ID, "result", res1.Metadata, *c.ExpectedMetadata)
	if res1.Outcome != c.ExpectedOutcome {
		t.Errorf("case %s outcome = %v, want %v", c.ID, res1.Outcome, c.ExpectedOutcome)
	}
	if res1.Reason != c.ExpectedReason {
		t.Errorf("case %s reason = %v, want %v", c.ID, res1.Reason, c.ExpectedReason)
	}
	assertExpectedSteps(t, c.ID, res1.Steps, c.ExpectedSteps)
	assertExpectedChanges(t, c.ID, res1.Changes, c.ExpectedChanges)

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

func expectedEvidenceRefs(issues []IssueExpectation, assumptions []AssumptionExpectation) []trace.EvidenceRef {
	var refs []trace.EvidenceRef
	for _, issue := range issues {
		refs = append(refs, issue.Evidence...)
	}
	for _, assumption := range assumptions {
		refs = append(refs, assumption.Evidence...)
	}

	return canonicalEvidence(refs)
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
	if !expected.Matches(actual) {
		t.Errorf(
			"case %s %s metadata does not match its exact structured expectation: got %+v, want %+v",
			caseID,
			axis,
			NewMetadataExpectation(actual),
			expected.Canonical(),
		)
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
	assertDeterministicJourney(t, caseID, first.Journey, second.Journey)
	assertDeterministicOptionalMetadata(t, caseID, "fabric", first.FabricMetadata, second.FabricMetadata)
}

func assertDeterministicOptionalMetadata(t testing.TB, caseID, axis string, first, second *analysis.Metadata) {
	t.Helper()
	if first == nil || second == nil {
		if first != nil || second != nil {
			t.Errorf("case %s non-deterministic %s metadata presence across runs", caseID, axis)
		}
		return
	}
	assertDeterministicMetadata(t, caseID, axis, *first, *second)
}

func assertDeterministicJourney(t testing.TB, caseID string, first, second *fabric.Journey) {
	t.Helper()
	if first == nil || second == nil {
		if first != nil || second != nil {
			t.Errorf("case %s non-deterministic journey presence across runs", caseID)
		}
		return
	}
	if first.FrameID != second.FrameID || first.Protocol != second.Protocol ||
		first.Mirror != second.Mirror || first.Parent != second.Parent {
		t.Errorf("case %s non-deterministic journey identity across runs", caseID)
	}
	if !reflect.DeepEqual(first.Deliveries, second.Deliveries) {
		t.Errorf("case %s non-deterministic journey deliveries across runs", caseID)
	}
	if len(first.Entries) != len(second.Entries) {
		t.Errorf("case %s non-deterministic journey entry count across runs: %d vs %d", caseID, len(first.Entries), len(second.Entries))
	} else {
		for i := range first.Entries {
			assertDeterministicJourneyEntry(t, caseID, i, first.Entries[i], second.Entries[i])
		}
	}
	assertDeterministicMetadata(t, caseID, "journey", first.Metadata, second.Metadata)
}

func assertDeterministicJourneyEntry(t testing.TB, caseID string, index int, first, second fabric.Entry) {
	t.Helper()
	if first.Kind != second.Kind || first.Device != second.Device || first.Port != second.Port || first.Reason != second.Reason {
		t.Errorf("case %s non-deterministic journey entry %d across runs", caseID, index)
	}
	if (first.Cable == nil) != (second.Cable == nil) || (first.Cable != nil && !first.Cable.Equal(*second.Cable)) {
		t.Errorf("case %s non-deterministic journey entry %d cable across runs", caseID, index)
	}
	if (first.Step == nil) != (second.Step == nil) || (first.Step != nil && !first.Step.Equal(*second.Step)) {
		t.Errorf("case %s non-deterministic journey entry %d host step across runs", caseID, index)
	}
	assertDeterministicForwardResult(t, caseID, fmt.Sprintf("journey entry %d", index), first.Result, second.Result)
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
	firstIssues := NewMetadataExpectation(first).Canonical().Issues
	secondIssues := NewMetadataExpectation(second).Canonical().Issues
	if !reflect.DeepEqual(firstIssues, secondIssues) {
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
		reflect.DeepEqual(first.ConsultedPorts(), second.ConsultedPorts()) &&
		reflect.DeepEqual(first.ConsultedScopes(), second.ConsultedScopes())
	if !domainEqual {
		t.Errorf("case %s non-deterministic %s domain result across runs", caseID, axis)
	}
	assertDeterministicMetadata(t, caseID, axis, first.Metadata, second.Metadata)
}
