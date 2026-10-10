package fabric

import (
	"maps"
	"slices"

	"go.aledante.io/FlowSeer/src/common/sim/flow"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Fork returns an independent execution copy of the fabric at its current simulation point.
// Immutable evidence and frame buffers are shared. Simulation state is copied independently.
func (f *Fabric) Fork() *Fabric {
	links := make([]Link, len(f.links))
	for i, l := range f.links {
		links[i] = cloneLink(l)
	}

	byEnd := make(map[Endpoint]linkEndRef, len(links)*2)
	for i := range links {
		byEnd[links[i].A.Endpoint] = linkEndRef{link: &links[i], end: &links[i].A, peer: &links[i].B}
		byEnd[links[i].B.Endpoint] = linkEndRef{link: &links[i], end: &links[i].B, peer: &links[i].A}
	}

	uncabled := make(map[Endpoint]Uncabled, len(f.uncabled))
	for ep, u := range f.uncabled {
		uncabled[ep] = u.Clone()
	}

	topology := f.topology.clone()
	for name, node := range topology.nodes {
		node.device = node.device.Fork()
		topology.nodes[name] = node
	}

	var queue []Arrival
	if len(f.queue) > 0 {
		queue = slices.Clone(f.queue)
	}
	attachments := make([]attachedSource, len(f.attachments))
	for i, source := range f.attachments {
		attachments[i] = source
		attachments[i].attachment.Source = source.attachment.Source.Clone()
		attachments[i].frame = source.frame
	}

	wakes := maps.Clone(f.wakes)

	var dequeueItems map[Endpoint]int
	if len(f.dequeueItems) > 0 {
		dequeueItems = maps.Clone(f.dequeueItems)
	}

	var wakeItems map[string]int
	if len(f.wakeItems) > 0 {
		wakeItems = maps.Clone(f.wakeItems)
	}

	journeys := make(map[FrameID]*journey, len(f.journeys))
	for id, j := range f.journeys {
		journeys[id] = j.shallowClone()
	}

	entered := make(map[FrameID]map[Endpoint]bool, len(f.entered))
	for id, eps := range f.entered {
		entered[id] = maps.Clone(eps)
	}

	cableCrossings := maps.Clone(f.cableCrossings)

	busyUntil := maps.Clone(f.busyUntil)

	egress := make(map[Endpoint]*egressQueue, len(f.egress))
	for ep, q := range f.egress {
		egress[ep] = cloneEgressQueue(q, journeys)
	}

	var unstatedBacked map[Endpoint]trace.EvidenceRef
	if len(f.unstatedBacked) > 0 {
		unstatedBacked = maps.Clone(f.unstatedBacked)
	}

	inflight := maps.Clone(f.inflight)

	var held map[holdKey]FrameID
	if len(f.held) > 0 {
		held = maps.Clone(f.held)
	}

	var flows map[flow.FlowID]*flow.FlowStats
	if f.flows != nil {
		flows = make(map[flow.FlowID]*flow.FlowStats, len(f.flows))
		for id, stats := range f.flows {
			cp := stats.Clone()
			flows[id] = &cp
		}
	}

	var counters map[Endpoint]*Counters
	if f.counters != nil {
		counters = make(map[Endpoint]*Counters, len(f.counters))
		for ep, c := range f.counters {
			cloned := c.Clone()
			counters[ep] = &cloned
		}
	}

	return &Fabric{
		topology:        topology,
		evidence:        f.evidence,
		runtimeEvidence: f.runtimeEvidence,
		links:           links,
		linkTrust:       cloneLinkTrustSlice(f.linkTrust),
		uncabled:        uncabled,
		byEnd:           byEnd,
		clock:           f.clock,
		stepped:         f.stepped,
		arrivalQueue:    arrivalQueue{queue: queue, wakes: wakes, dequeueItems: dequeueItems, wakeItems: wakeItems},
		attachments:     attachments,
		nextFrameID:     f.nextFrameID,
		nextSeq:         f.nextSeq,
		journeys:        journeys,
		entered:         entered,
		cableCrossings:  cableCrossings,
		busyUntil:       busyUntil,
		egress:          egress,
		counters:        counters,
		unstatedBacked:  unstatedBacked,
		metadataCache:   f.metadataCache,
		inflight:        inflight,
		held:            held,
		flows:           flows,
		protocolIssues:  slices.Clone(f.protocolIssues),
		touched:         nil,
		err:             f.err,
	}
}

func cloneLink(l Link) Link {
	cp := l
	cp.Cable = l.Cable.Clone()
	cp.A = l.A.clone()
	cp.B = l.B.clone()
	return cp
}

func cloneLinkTrustSlice(trust []linkTrust) []linkTrust {
	if len(trust) == 0 {
		return nil
	}
	cp := make([]linkTrust, len(trust))
	for i, t := range trust {
		cp[i] = linkTrust{
			issues: slices.Clone(t.issues),
		}
		if t.assumption != nil {
			a := *t.assumption
			cp[i].assumption = &a
		}
	}
	return cp
}

func cloneEgressQueue(q *egressQueue, journeys map[FrameID]*journey) *egressQueue {
	cp := *q
	for i := 0; i < 8; i++ {
		if len(q.pending[i]) > 0 {
			pending := make([]queued, len(q.pending[i]))
			for j, item := range q.pending[i] {
				pending[j] = item
				if journeys[item.fid] != nil {
					pending[j].journey = journeys[item.fid]
				} else if item.journey != nil {
					pending[j].journey = item.journey.shallowClone()
				}
			}
			cp.pending[i] = pending
		}
	}
	return &cp
}
