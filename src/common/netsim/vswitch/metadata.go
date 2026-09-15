package vswitch

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func cloneMetadata(metadata analysis.Metadata) analysis.Metadata {
	catalog := analysis.EvidenceCatalog{}
	for _, entry := range metadata.Evidence().Entries() {
		catalog, _ = catalog.Add(entry.Evidence)
	}

	return analysis.NewMetadata(
		metadata.Scope(),
		metadata.Issues(),
		catalog,
		metadata.Assumptions(),
	)
}

func metadataEqual(a, b analysis.Metadata) bool {
	return a.Scope().Compare(b.Scope()) == 0 &&
		a.Status() == b.Status() &&
		issueListsEqual(a.Issues(), b.Issues()) &&
		slices.Equal(a.Evidence().Entries(), b.Evidence().Entries()) &&
		slices.EqualFunc(a.Assumptions(), b.Assumptions(), assumptionEqual)
}

func issueEqual(a, b analysis.Issue) bool {
	return a.Code == b.Code &&
		a.Status == b.Status &&
		a.Scope.Compare(b.Scope) == 0 &&
		slices.Equal(a.Evidence, b.Evidence)
}

func issueListsEqual(a, b []analysis.Issue) bool {
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

func assumptionEqual(a, b analysis.Assumption) bool {
	return a.Scope.Compare(b.Scope) == 0 &&
		a.Statement == b.Statement &&
		slices.Equal(a.Evidence, b.Evidence)
}

func forwardingMetadata(
	nodeID string,
	loaded analysis.Metadata,
	consulted []analysis.Scope,
	runtimeIssues []runtimeIssue,
) analysis.Metadata {
	issues := make([]analysis.Issue, len(runtimeIssues))
	issueEvidence := make(map[trace.EvidenceRef]struct{})
	catalog := analysis.EvidenceCatalog{}
	for i, runtime := range runtimeIssues {
		issue := runtime.issue
		var ref trace.EvidenceRef
		catalog, ref = catalog.Add(runtimeIssueEvidence(runtime))
		issue.Evidence = append(issue.Evidence, ref)
		issues[i] = issue
		issueEvidence[ref] = struct{}{}
	}
	for _, issue := range loaded.Issues() {
		if !forwardingScopeRelevant(nodeID, issue.Scope, consulted) {
			continue
		}
		issues = append(issues, issue)
		for _, ref := range issue.Evidence {
			issueEvidence[ref] = struct{}{}
		}
	}

	var assumptions []analysis.Assumption
	evidenceRefs := issueEvidence
	for _, assumption := range loaded.Assumptions() {
		if !forwardingScopeRelevant(nodeID, assumption.Scope, consulted) &&
			!referencesAny(assumption.Evidence, issueEvidence) {
			continue
		}
		assumptions = append(assumptions, assumption)
		for _, ref := range assumption.Evidence {
			evidenceRefs[ref] = struct{}{}
		}
	}

	for _, entry := range loaded.Evidence().Entries() {
		if _, ok := evidenceRefs[entry.Ref]; !ok {
			continue
		}
		catalog, _ = catalog.Add(entry.Evidence)
	}

	return analysis.NewMetadata(analysis.NodeScope(nodeID), issues, catalog, assumptions)
}

type runtimeIssue struct {
	issue analysis.Issue
	facts []trace.Fact
}

type membershipFact string

func (f membershipFact) TypeID() string    { return "vswitch.mcast_membership" }
func (f membershipFact) Canonical() string { return string(f) }

// newMembershipFact returns an immutable snapshot of a multicast membership
// lookup naming the frame's IP source: ports is already that source's
// admitted egress set, so the fact records which source produced it rather
// than only the group and the resulting port list.
func newMembershipFact(vid vlan.ID, group, source netip.Addr, ports []string, registered, decided bool) trace.Fact {
	sorted := slices.Clone(ports)
	slices.Sort(sorted)

	var b strings.Builder
	fmt.Fprintf(&b, "fid=%d;group=%q;source=%q;registered=%t;decided=%t;ports=[",
		vid, group.String(), source.String(), registered, decided)
	for i, name := range sorted {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q", name)
	}
	b.WriteByte(']')

	return membershipFact(b.String())
}

type runtimeFact struct {
	typeID    string
	canonical string
}

func (f runtimeFact) TypeID() string    { return f.typeID }
func (f runtimeFact) Canonical() string { return f.canonical }

func runtimeIssueEvidence(runtime runtimeIssue) analysis.Evidence {
	canonical := runtime.issue.Canonical()
	facts := slices.Clone(runtime.facts)
	trace.SortFacts(facts)
	var context strings.Builder
	fmt.Fprintf(
		&context,
		"code=%q,status=%q,scope=%q",
		canonical.Code.String(),
		canonical.Status.String(),
		canonical.Scope.String(),
	)
	for _, fact := range facts {
		fmt.Fprintf(&context, ",fact[%q]=%q", fact.TypeID(), fact.Canonical())
	}
	return analysis.Evidence{
		Kind:    "vswitch.runtime",
		Origin:  "forward",
		Context: context.String(),
	}
}

func forwardingScopeRelevant(nodeID string, scope analysis.Scope, consulted []analysis.Scope) bool {
	if scope.Compare(analysis.WholeScope()) == 0 {
		return true
	}
	if scope.Compare(analysis.NodeScope(nodeID)) == 0 {
		return true
	}
	return slices.ContainsFunc(consulted, scope.Overlaps)
}

func protocolScope(nodeID string, layer port.Layer) analysis.Scope {
	return analysis.ProtocolScope(nodeID, string(layer), "0")
}

func metadataHasScopedContent(metadata analysis.Metadata, parent analysis.Scope) bool {
	if slices.ContainsFunc(metadata.Issues(), func(issue analysis.Issue) bool {
		return parent.Contains(issue.Scope)
	}) {
		return true
	}
	return slices.ContainsFunc(metadata.Assumptions(), func(assumption analysis.Assumption) bool {
		return parent.Contains(assumption.Scope)
	})
}

func canonicalScopes(scopes []analysis.Scope) []analysis.Scope {
	result := slices.Clone(scopes)
	slices.SortFunc(result, func(a, b analysis.Scope) int { return a.Compare(b) })
	return slices.CompactFunc(result, func(a, b analysis.Scope) bool { return a.Compare(b) == 0 })
}

func validateConstructionMetadata(nodeID string, metadata analysis.Metadata) error {
	if canonicalZeroMetadata(metadata) {
		return nil
	}

	node := analysis.NodeScope(nodeID)
	if !node.Contains(metadata.Scope()) {
		return incompatibleMetadataScopeError("metadata.scope", nodeID, metadata.Scope())
	}
	for i, issue := range metadata.Issues() {
		if !constructionScopeCompatible(node, issue.Scope) {
			return incompatibleMetadataScopeError("metadata.issues."+strconv.Itoa(i)+".scope", nodeID, issue.Scope)
		}
		for j, ref := range issue.Evidence {
			if _, ok := metadata.Evidence().Lookup(ref); !ok {
				return missingMetadataEvidenceError(
					"metadata.issues."+strconv.Itoa(i)+".evidence."+strconv.Itoa(j),
					ref,
				)
			}
		}
	}
	for i, assumption := range metadata.Assumptions() {
		if !constructionScopeCompatible(node, assumption.Scope) {
			return incompatibleMetadataScopeError("metadata.assumptions."+strconv.Itoa(i)+".scope", nodeID, assumption.Scope)
		}
		for j, ref := range assumption.Evidence {
			if _, ok := metadata.Evidence().Lookup(ref); !ok {
				return missingMetadataEvidenceError(
					"metadata.assumptions."+strconv.Itoa(i)+".evidence."+strconv.Itoa(j),
					ref,
				)
			}
		}
	}

	return nil
}

func canonicalZeroMetadata(metadata analysis.Metadata) bool {
	return metadata.Scope().Compare(analysis.WholeScope()) == 0 &&
		metadata.Status() == analysis.Complete &&
		len(metadata.Issues()) == 0 &&
		len(metadata.Evidence().Entries()) == 0 &&
		len(metadata.Assumptions()) == 0
}

func constructionScopeCompatible(node, scope analysis.Scope) bool {
	return scope.Compare(analysis.WholeScope()) == 0 || node.Contains(scope)
}

func incompatibleMetadataScopeError(field, nodeID string, scope analysis.Scope) error {
	return errs.New().
		Attr("field", field).
		Attr("node_id", nodeID).
		Attr("scope", scope.String()).
		Msgf("construction metadata scope %s is incompatible with node %q", scope, nodeID)
}

func missingMetadataEvidenceError(field string, ref trace.EvidenceRef) error {
	return errs.New().
		Attr("field", field).
		Attr("evidence_ref", ref).
		Msgf("construction metadata evidence reference %q is absent from the evidence catalog", ref)
}

func referencesAny(refs []trace.EvidenceRef, selected map[trace.EvidenceRef]struct{}) bool {
	return slices.ContainsFunc(refs, func(ref trace.EvidenceRef) bool {
		_, ok := selected[ref]
		return ok
	})
}
