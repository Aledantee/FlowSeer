// Package fabric composes virtual switches, hosts, and cables into a Layer 2 network fabric.
package fabric

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Metadata returns the fabric's trust metadata over the whole analysis as the
// links stand now, so a later [Fabric.SetFault] changes what it returns. It holds:
//   - an issue on the link's [analysis.LinkScope] for every link whose facts leave
//     it Unknown, coded with the link's reason and Unsupported when the negotiation
//     case is unmodeled, and a propagation-unknown issue for an operational link
//     over an unspecified medium without a Delay;
//   - an adjacency-unresolved issue on the port scope of every unresolved switch
//     port, and oper-status-conflict or observed-speed-conflict issues on the port
//     scope where an observation disagrees with what executes;
//   - a queue-buffer-unstated issue on the endpoint scope of every endpoint whose
//     egress queue states no buffer and has backed up past one maximum-size frame;
//   - one assumption per link [Config.PhyAssumption] filled.
//
// A link's scope key is its cable's [Diff] subject key. Issues cite the evidence
// references of the cable, Uncabled entry, or runtime queue crossing they rest
// on; Metadata merges the runtime catalog with the construction specification's
// evidence catalog so all cited references resolve.
//
// The result is cached: its inputs are fixed after construction except where
// [Fabric.SetFault] rewrites a link, [Fabric.Configure] rebuilds a switch, or a
// queue with no stated buffer first backs up past one maximum-size frame. Each
// clears the cache.
func (f *Fabric) Metadata() analysis.Metadata {
	if f.metadataCache != nil {
		return *f.metadataCache
	}

	var issues []analysis.Issue
	var assumptions []analysis.Assumption
	for _, trust := range f.linkTrust {
		issues = append(issues, trust.issues...)
		if trust.assumption != nil {
			assumptions = append(assumptions, *trust.assumption)
		}
	}

	for _, name := range f.switchNames() {
		for _, p := range f.topology.nodes[name].ports.Ports() {
			if p.Kind == port.LAG {
				continue
			}
			ep := Endpoint{Node: name, Port: p.Name}
			derived, evidence := f.derivedEnd(ep)
			if derived.Reason == ReasonAdjacencyUnresolved {
				issues = append(issues, analysis.Issue{
					Code:    analysis.IssueCode(ReasonAdjacencyUnresolved),
					Status:  analysis.Incomplete,
					Scope:   analysis.PortScope(name, p.Name),
					Message: fmt.Sprintf("no cable names port %q on switch %q and no uncabled entry lists it", p.Name, name),
				})
			}
			if observedOperStatus(p.OperStatus) && observedOperStatus(derived.Oper) && p.OperStatus != derived.Oper {
				issues = append(issues, analysis.Issue{
					Code:     IssueOperStatusConflict,
					Status:   analysis.Incomplete,
					Scope:    analysis.PortScope(name, p.Name),
					Message:  fmt.Sprintf("port %q on switch %q is configured %s but its cable derives %s", p.Name, name, p.OperStatus, derived.Oper),
					Evidence: evidence,
				})
			}
		}
	}

	for _, ep := range slices.SortedFunc(maps.Keys(f.unstatedBacked), compareEndpoint) {
		issues = append(issues, queueBufferUnstatedIssue(ep, f.unstatedBacked[ep]))
	}

	catalog := f.evidence
	for _, entry := range f.runtimeEvidence.Entries() {
		catalog, _ = catalog.Add(entry.Evidence)
	}
	md := analysis.NewMetadata(analysis.WholeScope(), issues, catalog, assumptions)
	f.metadataCache = &md

	return md
}

// queueBufferUnstatedIssue is the issue an endpoint raises once an unstated-buffer
// egress queue first backs up past one maximum-size frame.
func queueBufferUnstatedIssue(ep Endpoint, ref trace.EvidenceRef) analysis.Issue {
	return analysis.Issue{
		Code:     IssueQueueBufferUnstated,
		Status:   analysis.Incomplete,
		Scope:    endpointScope(ep),
		Message:  fmt.Sprintf("egress queue on node %q port %q states no buffer and has backed up past one maximum-size frame", ep.Node, ep.Port),
		Evidence: []trace.EvidenceRef{ref},
	}
}

// mergeRaised folds issues raised about a journey into base. It bypasses the
// dependency scope [Fabric.record] applies to an entry, so an issue reaches a
// journey even when no later hop would carry it.
func (f *Fabric) mergeRaised(base analysis.Metadata, issues []analysis.Issue) analysis.Metadata {
	if len(issues) == 0 {
		return base
	}

	return base.Merge(analysis.NewMetadata(analysis.WholeScope(), issues, f.evidence, nil))
}

// markQueueBufferUnstated records the first threshold crossing per physical
// endpoint before enqueue, retaining its evidence for later metadata reads.
func (f *Fabric) markQueueBufferUnstated(now time.Time, ep Endpoint, egressPort string, pcp vlan.PCP, fid FrameID, step trace.Step, journey *Journey) {
	if _, marked := f.unstatedBacked[ep]; marked {
		return
	}
	if f.unstatedBacked == nil {
		f.unstatedBacked = make(map[Endpoint]trace.EvidenceRef)
	}
	target := egressPort
	if egressPort == "" {
		target = ep.Node
		step.Subject = trace.Subject{Kind: "host", Key: trace.CompositeKey(ep.Node, strconv.Itoa(int(pcp)))}
	}
	fact := step.Inputs[0]
	context := "rule=" + strconv.Quote(string(step.RuleID)) +
		";physical_node=" + strconv.Quote(ep.Node) +
		";physical_port=" + strconv.Quote(ep.Port) +
		";egress_port=" + strconv.Quote(target) +
		";pcp=" + strconv.FormatUint(uint64(pcp), 10) +
		";frame_id=" + strconv.FormatUint(uint64(fid), 10) +
		";at=" + strconv.Quote(now.UTC().Format(time.RFC3339Nano)) +
		";" + fact.Canonical()
	var ref trace.EvidenceRef
	f.runtimeEvidence, ref = f.runtimeEvidence.Add(analysis.Evidence{
		Kind: "fabric.runtime", Origin: "egress-queue", Context: context,
	})
	f.unstatedBacked[ep] = ref
	f.metadataCache = nil
	step.Evidence = []trace.EvidenceRef{ref}
	f.record(journey, Entry{
		At: now, Kind: EntryQueueThreshold, Device: ep.Node, Port: ep.Port, PCP: pcp,
		Step: &step,
	})
}

// observedOperStatus reports whether a state says something definite: an unset
// or Unknown status observes nothing, so it cannot conflict.
func observedOperStatus(state port.LinkState) bool {
	return state == port.Up || state == port.Down
}

func (f *Fabric) derivedEnd(ep Endpoint) (LinkEnd, []trace.EvidenceRef) {
	if ref, ok := f.byEnd[ep]; ok {
		return *ref.end, ref.link.Cable.Evidence
	}

	return unlinkedEnd(ep, f.uncabled), f.uncabled[ep].Evidence
}
