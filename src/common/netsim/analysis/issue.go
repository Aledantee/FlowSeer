package analysis

import (
	"cmp"
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// IssueCode identifies a producer-owned reason that analysis trust was reduced.
// The analysis package accepts unknown codes without a central capability registry.
// The zero value is an unspecified code. IssueCode is safe for concurrent use.
type IssueCode string

// String returns the issue code unchanged.
func (c IssueCode) String() string {
	return string(c)
}

// Issue records one lossless cause affecting analysis trust. Message is for
// people and is retained, but Code is the producer-owned semantic identity.
// Issue values are safe for concurrent use when their Evidence slice is not mutated.
type Issue struct {
	Code     IssueCode
	Status   Status
	Scope    Scope
	Message  string
	Evidence []trace.EvidenceRef
}

// Canonical returns an independent issue with a conservative status and sorted,
// deduplicated evidence references.
func (i Issue) Canonical() Issue {
	i.Status = normalizeStatus(i.Status)
	i.Evidence = canonicalEvidenceRefs(i.Evidence)
	return i
}

// Summarize derives the conservative status of issues. Every issue remains in
// the caller's collection; only the summary collapses their statuses.
func Summarize(issues []Issue) Status {
	status := Complete
	for _, issue := range issues {
		candidate := normalizeStatus(issue.Status)
		if candidate > status {
			status = candidate
		}
	}
	return status
}

// CanonicalIssues returns independent canonical issues in stable scope, code,
// status, message, and evidence order. Equal issues are retained.
func CanonicalIssues(issues []Issue) []Issue {
	if len(issues) == 0 {
		return nil
	}

	result := make([]Issue, len(issues))
	for i, issue := range issues {
		result[i] = issue.Canonical()
	}
	slices.SortFunc(result, compareIssue)
	return result
}

func issuesFor(issues []Issue, scope Scope) []Issue {
	var result []Issue
	for _, issue := range issues {
		if scope.Overlaps(issue.Scope) {
			result = append(result, issue.Canonical())
		}
	}
	return result
}

func compareIssue(a, b Issue) int {
	if order := a.Scope.Compare(b.Scope); order != 0 {
		return order
	}
	if order := strings.Compare(string(a.Code), string(b.Code)); order != 0 {
		return order
	}
	if order := cmp.Compare(a.Status, b.Status); order != 0 {
		return order
	}
	if order := strings.Compare(a.Message, b.Message); order != 0 {
		return order
	}
	return slices.Compare(a.Evidence, b.Evidence)
}

func canonicalEvidenceRefs(refs []trace.EvidenceRef) []trace.EvidenceRef {
	if len(refs) == 0 {
		return nil
	}

	result := slices.Clone(refs)
	slices.Sort(result)
	return slices.Compact(result)
}
