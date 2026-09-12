package analysis

import (
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Assumption records an explicit premise used by an analysis. Its scope names
// the affected result, and Evidence links the premise to its support.
// Assumption values are safe for concurrent use when their Evidence slice is not mutated.
type Assumption struct {
	Scope     Scope
	Statement string
	Evidence  []trace.EvidenceRef
}

// Canonical returns an independent assumption with sorted, deduplicated evidence references.
func (a Assumption) Canonical() Assumption {
	a.Evidence = canonicalEvidenceRefs(a.Evidence)
	return a
}

// Metadata carries the trust contract for a capability-owned result. It keeps
// every canonical issue while deriving the summary for its evaluated scope.
// The zero value describes a complete whole-analysis result. Metadata is
// immutable and safe for concurrent use.
type Metadata struct {
	scope       Scope
	status      Status
	issues      []Issue
	evidence    EvidenceCatalog
	assumptions []Assumption
}

// NewMetadata constructs immutable result metadata. It derives Status from all
// issues overlapping evaluatedScope, so callers cannot supply a contradictory summary.
func NewMetadata(evaluatedScope Scope, issues []Issue, evidence EvidenceCatalog, assumptions []Assumption) Metadata {
	canonicalIssues := CanonicalIssues(issues)
	canonicalAssumptions := canonicalAssumptionList(assumptions)

	return Metadata{
		scope:       evaluatedScope,
		status:      Summarize(issuesFor(canonicalIssues, evaluatedScope)),
		issues:      canonicalIssues,
		evidence:    evidence,
		assumptions: canonicalAssumptions,
	}
}

// Scope returns the scope evaluated by the result.
func (m Metadata) Scope() Scope {
	return m.scope
}

// Status returns the status derived for the evaluated scope.
func (m Metadata) Status() Status {
	return m.status
}

// StatusFor derives the status of issues affecting scope.
func (m Metadata) StatusFor(scope Scope) Status {
	return Summarize(issuesFor(m.issues, scope))
}

// Issues returns an independent copy of every canonical issue.
func (m Metadata) Issues() []Issue {
	return CanonicalIssues(m.issues)
}

// IssuesFor returns independent canonical issues whose scopes overlap scope.
func (m Metadata) IssuesFor(scope Scope) []Issue {
	return issuesFor(m.issues, scope)
}

// Evidence returns the immutable evidence catalog. Adding evidence to the
// returned value produces a new catalog and cannot change the metadata.
func (m Metadata) Evidence() EvidenceCatalog {
	return m.evidence
}

// Assumptions returns independent canonical assumptions.
func (m Metadata) Assumptions() []Assumption {
	if len(m.assumptions) == 0 {
		return nil
	}

	result := make([]Assumption, len(m.assumptions))
	for i, assumption := range m.assumptions {
		result[i] = assumption.Canonical()
	}
	return result
}

func canonicalAssumptionList(assumptions []Assumption) []Assumption {
	if len(assumptions) == 0 {
		return nil
	}

	result := make([]Assumption, len(assumptions))
	for i, assumption := range assumptions {
		result[i] = assumption.Canonical()
	}
	slices.SortFunc(result, compareAssumption)
	return result
}

func compareAssumption(a, b Assumption) int {
	if order := a.Scope.Compare(b.Scope); order != 0 {
		return order
	}
	if order := strings.Compare(a.Statement, b.Statement); order != 0 {
		return order
	}
	return slices.Compare(a.Evidence, b.Evidence)
}
