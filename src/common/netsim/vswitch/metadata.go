package vswitch

import (
	"slices"

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
		slices.EqualFunc(a.Issues(), b.Issues(), issueEqual) &&
		slices.Equal(a.Evidence().Entries(), b.Evidence().Entries()) &&
		slices.EqualFunc(a.Assumptions(), b.Assumptions(), assumptionEqual)
}

func issueEqual(a, b analysis.Issue) bool {
	return a.Code == b.Code &&
		a.Status == b.Status &&
		a.Scope.Compare(b.Scope) == 0 &&
		a.Message == b.Message &&
		slices.Equal(a.Evidence, b.Evidence)
}

func assumptionEqual(a, b analysis.Assumption) bool {
	return a.Scope.Compare(b.Scope) == 0 &&
		a.Statement == b.Statement &&
		slices.Equal(a.Evidence, b.Evidence)
}

func forwardingMetadata(
	nodeID string,
	loaded analysis.Metadata,
	consulted []port.Port,
	runtimeIssues []analysis.Issue,
) analysis.Metadata {
	portScopes := make([]analysis.Scope, len(consulted))
	for i, consultedPort := range consulted {
		portScopes[i] = analysis.PortScope(nodeID, consultedPort.Name)
	}

	issues := slices.Clone(runtimeIssues)
	issueEvidence := make(map[trace.EvidenceRef]struct{})
	for _, issue := range loaded.Issues() {
		if !forwardingScopeRelevant(nodeID, issue.Scope, portScopes) {
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
		if !forwardingScopeRelevant(nodeID, assumption.Scope, portScopes) &&
			!referencesAny(assumption.Evidence, issueEvidence) {
			continue
		}
		assumptions = append(assumptions, assumption)
		for _, ref := range assumption.Evidence {
			evidenceRefs[ref] = struct{}{}
		}
	}

	catalog := analysis.EvidenceCatalog{}
	for _, entry := range loaded.Evidence().Entries() {
		if _, ok := evidenceRefs[entry.Ref]; !ok {
			continue
		}
		catalog, _ = catalog.Add(entry.Evidence)
	}

	return analysis.NewMetadata(analysis.NodeScope(nodeID), issues, catalog, assumptions)
}

func forwardingScopeRelevant(nodeID string, scope analysis.Scope, ports []analysis.Scope) bool {
	if scope.Compare(analysis.NodeScope(nodeID)) == 0 {
		return true
	}
	if nodeID == "" && scope.Compare(analysis.WholeScope()) == 0 {
		return true
	}
	return slices.ContainsFunc(ports, scope.Overlaps)
}

func referencesAny(refs []trace.EvidenceRef, selected map[trace.EvidenceRef]struct{}) bool {
	return slices.ContainsFunc(refs, func(ref trace.EvidenceRef) bool {
		_, ok := selected[ref]
		return ok
	})
}
