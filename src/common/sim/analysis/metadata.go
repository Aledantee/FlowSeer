package analysis

import (
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/sim/trace"
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

// Clone returns an independent deep copy of m. It rebuilds the evidence
// catalog and returns independent slices for issues and assumptions so
// modifications to the caller's slices cannot affect m.
func (m Metadata) Clone() Metadata {
	catalog := EvidenceCatalog{}
	for _, entry := range m.evidence.Entries() {
		catalog, _ = catalog.Add(entry.Evidence)
	}

	return NewMetadata(
		m.scope,
		m.Issues(),
		catalog,
		m.Assumptions(),
	)
}

// Equal reports whether m and other describe equivalent metadata. It checks
// that evaluated scopes and derived statuses match, that all evidence entries
// and assumptions are equal, and that issues match without regard to order or
// human-readable Message.
func (m Metadata) Equal(other Metadata) bool {
	return m.scope.Compare(other.scope) == 0 &&
		m.status == other.status &&
		issueListsEqual(m.Issues(), other.Issues()) &&
		slices.Equal(m.evidence.Entries(), other.evidence.Entries()) &&
		slices.EqualFunc(m.Assumptions(), other.Assumptions(), assumptionEqual)
}

// Merge folds source's issues and assumptions into m, citing each added item's
// evidence in m's catalog and dropping duplicates already held. It compares
// issues and assumptions using their canonical forms, including issue Message.
// It returns m unchanged when nothing was added.
func (m Metadata) Merge(source Metadata) Metadata {
	issues := m.Issues()
	assumptions := m.Assumptions()
	catalog := m.Evidence()
	changed := false

	for _, issue := range source.Issues() {
		if slices.ContainsFunc(issues, func(kept Issue) bool { return sameIssue(kept, issue) }) {
			continue
		}
		issues = append(issues, issue)
		catalog = citeEvidence(catalog, issue.Evidence, source.Evidence())
		changed = true
	}
	for _, assumption := range source.Assumptions() {
		if slices.ContainsFunc(assumptions, func(kept Assumption) bool { return sameAssumption(kept, assumption) }) {
			continue
		}
		assumptions = append(assumptions, assumption)
		catalog = citeEvidence(catalog, assumption.Evidence, source.Evidence())
		changed = true
	}

	if !changed {
		return m
	}

	return NewMetadata(m.scope, issues, catalog, assumptions)
}

// SameIssues reports whether a and b have identical lengths and corresponding
// issues with matching Code, Status, and Scope in order. It ignores Message and
// Evidence.
func SameIssues(a, b []Issue) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Code != b[i].Code || a[i].Status != b[i].Status || a[i].Scope.Compare(b[i].Scope) != 0 {
			return false
		}
	}
	return true
}

func issueEqual(a, b Issue) bool {
	return a.Code == b.Code &&
		a.Status == b.Status &&
		a.Scope.Compare(b.Scope) == 0 &&
		slices.Equal(a.Evidence, b.Evidence)
}

func issueListsEqual(a, b []Issue) bool {
	if len(a) != len(b) {
		return false
	}

	matched := make([]bool, len(b))
	for _, issue := range a {
		found := -1
		for i, candidate := range b {
			if !matched[i] && issueEqual(issue, candidate) {
				found = i
				break
			}
		}
		if found < 0 {
			return false
		}
		matched[found] = true
	}

	return true
}

func assumptionEqual(a, b Assumption) bool {
	return a.Scope.Compare(b.Scope) == 0 &&
		a.Statement == b.Statement &&
		slices.Equal(a.Evidence, b.Evidence)
}

func sameIssue(a, b Issue) bool {
	a, b = a.Canonical(), b.Canonical()

	return a.Code == b.Code && a.Status == b.Status && a.Scope.Compare(b.Scope) == 0 && a.Message == b.Message &&
		slices.Equal(a.Evidence, b.Evidence)
}

func sameAssumption(a, b Assumption) bool {
	a, b = a.Canonical(), b.Canonical()

	return a.Scope.Compare(b.Scope) == 0 && a.Statement == b.Statement && slices.Equal(a.Evidence, b.Evidence)
}

func citeEvidence(catalog EvidenceCatalog, refs []trace.EvidenceRef, source EvidenceCatalog) EvidenceCatalog {
	for _, ref := range refs {
		if evidence, ok := source.Lookup(ref); ok {
			catalog, _ = catalog.Add(evidence)
		}
	}

	return catalog
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
