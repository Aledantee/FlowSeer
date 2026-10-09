// Package fabric composes virtual switches, hosts, and cables into a Layer 2 network fabric.
package fabric

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Fabric is a set of switches, hosts, and reflectors joined by cables, with every switch port's operational
// state decided by its cable, by an Uncabled entry, or left Unknown when neither names it.
//
// A Fabric is not safe for concurrent use.
type Fabric struct {
	topology        topology
	evidence        analysis.EvidenceCatalog
	runtimeEvidence analysis.EvidenceCatalog
	links           []Link
	linkTrust       []linkTrust
	uncabled        map[Endpoint]Uncabled
	byEnd           map[Endpoint]linkEndRef
	clock           time.Time
	stepped         bool
	queue           []Arrival
	attachments     []attachedSource
	pendingHosts    []pendingHostInjection
	wakes           map[string]time.Time
	dequeueItems    map[Endpoint]int
	wakeItems       map[string]int
	nextFrameID     FrameID
	nextSeq         uint64
	journeys        map[FrameID]*Journey
	entered         map[FrameID]map[Endpoint]bool
	cableCrossings  map[Endpoint]uint
	busyUntil       map[Endpoint]time.Time
	egress          map[Endpoint]*egressQueue
	counters        map[Endpoint]*Counters

	// unstatedBacked retains each endpoint's first queue-threshold evidence ref.
	unstatedBacked map[Endpoint]trace.EvidenceRef

	// metadataCache holds the value [Fabric.Metadata] last built. Its inputs
	// are fixed after construction except where [Fabric.SetFault] rewrites a
	// link and its trust, [Fabric.Configure] rebuilds a switch's ports and
	// links, or a queue with no stated buffer first backs up past one
	// maximum-size frame; each nils the cache. The value is immutable, so a
	// fork may share it.
	metadataCache *analysis.Metadata

	// inflight counts the arrivals of each frame that are still in the queue
	// or an egress queue. A frame settles, folding and possibly freeing its
	// journey, when its count reaches zero at the end of the call that
	// touched it.
	inflight map[FrameID]int
	// heldAggregates records what a freed aggregate journey left behind: a
	// switch held the frame for neighbor resolution, so the frame's FrameID
	// stays under the device of the frame's last entry, ascending, as a
	// placeholder a release can claim. The journey itself is freed, and a
	// placeholder carries no payload. A hold resolution abandons stays a
	// candidate, as a retained held journey does, so a later release on the
	// same device may name it and claim its placeholder.
	heldAggregates map[string][]FrameID
	// flows accumulates per-flow statistics as journeys settle.
	flows map[FlowID]*FlowStats
	// touched collects the frame IDs count changes touched during the current
	// Inject or Step, drained as it settles them.
	touched []FrameID

	// err is the first scheduling-invariant breach [Fabric.scheduleDequeue]
	// recorded. It is sticky: once set, [Fabric.Step] refuses to advance and
	// [Fabric.Err] reports it. A caller must read Err to see a fault, because
	// a short step count alone does not distinguish a fault from an empty
	// queue.
	err error
}

type linkEndRef struct {
	link *Link
	end  *LinkEnd
	peer *LinkEnd
}

// Fork returns an independent execution copy of the fabric at its current simulation point.
// Construction inputs and immutable frames are shared; all simulation state is deep-copied.
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

	var uncabled map[Endpoint]Uncabled
	if f.uncabled != nil {
		uncabled = make(map[Endpoint]Uncabled, len(f.uncabled))
		for ep, u := range f.uncabled {
			uncabled[ep] = u.Clone()
		}
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
		attachments[i].frame = cloneFrame(source.frame)
	}
	pendingHosts := slices.Clone(f.pendingHosts)
	for i := range pendingHosts {
		pendingHosts[i].frame = cloneFrame(pendingHosts[i].frame)
	}

	var wakes map[string]time.Time
	if f.wakes != nil {
		wakes = maps.Clone(f.wakes)
	}

	var dequeueItems map[Endpoint]int
	if len(f.dequeueItems) > 0 {
		dequeueItems = maps.Clone(f.dequeueItems)
	}

	var wakeItems map[string]int
	if len(f.wakeItems) > 0 {
		wakeItems = maps.Clone(f.wakeItems)
	}

	var journeys map[FrameID]*Journey
	if f.journeys != nil {
		journeys = make(map[FrameID]*Journey, len(f.journeys))
		for id, j := range f.journeys {
			journeys[id] = j.shallowClone()
		}
	}

	var entered map[FrameID]map[Endpoint]bool
	if f.entered != nil {
		entered = make(map[FrameID]map[Endpoint]bool, len(f.entered))
		for id, eps := range f.entered {
			entered[id] = maps.Clone(eps)
		}
	}

	var cableCrossings map[Endpoint]uint
	if f.cableCrossings != nil {
		cableCrossings = maps.Clone(f.cableCrossings)
	}

	var busyUntil map[Endpoint]time.Time
	if f.busyUntil != nil {
		busyUntil = maps.Clone(f.busyUntil)
	}

	var egress map[Endpoint]*egressQueue
	if f.egress != nil {
		egress = make(map[Endpoint]*egressQueue, len(f.egress))
		for ep, q := range f.egress {
			egress[ep] = cloneEgressQueue(q, journeys)
		}
	}

	var unstatedBacked map[Endpoint]trace.EvidenceRef
	if len(f.unstatedBacked) > 0 {
		unstatedBacked = maps.Clone(f.unstatedBacked)
	}

	var inflight map[FrameID]int
	if f.inflight != nil {
		inflight = maps.Clone(f.inflight)
	}

	var heldAggregates map[string][]FrameID
	if len(f.heldAggregates) > 0 {
		heldAggregates = make(map[string][]FrameID, len(f.heldAggregates))
		for device, ids := range f.heldAggregates {
			heldAggregates[device] = slices.Clone(ids)
		}
	}

	var flows map[FlowID]*FlowStats
	if f.flows != nil {
		flows = make(map[FlowID]*FlowStats, len(f.flows))
		for id, stats := range f.flows {
			cp := stats.Clone()
			flows[id] = &cp
		}
	}

	var counters map[Endpoint]*Counters
	if f.counters != nil {
		counters = make(map[Endpoint]*Counters, len(f.counters))
		for ep, c := range f.counters {
			if c != nil {
				cloned := c.Clone()
				counters[ep] = &cloned
			}
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
		queue:           queue,
		attachments:     attachments,
		pendingHosts:    pendingHosts,
		wakes:           wakes,
		dequeueItems:    dequeueItems,
		wakeItems:       wakeItems,
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
		heldAggregates:  heldAggregates,
		flows:           flows,
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

func cloneEgressQueue(q *egressQueue, journeys map[FrameID]*Journey) *egressQueue {
	if q == nil {
		return nil
	}
	cp := *q
	for i := 0; i < 8; i++ {
		if len(q.pending[i]) > 0 {
			pending := make([]queued, len(q.pending[i]))
			for j, item := range q.pending[i] {
				pending[j] = item
				if journeys != nil && journeys[item.fid] != nil {
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

func (f *Fabric) switchNames() []string {
	names := make([]string, 0, len(f.topology.nodes))
	for name, node := range f.topology.nodes {
		if node.snapshot {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// startLayers reports every port, with LAG members before their parents, and
// schedules each switch's next timer at the fabric's clock.
func (f *Fabric) startLayers(names []string) error {
	for _, name := range names {
		sw := f.nodeDevice(name)
		ports := sw.Ports().Ports()
		slices.SortFunc(ports, func(a, b port.Port) int {
			if (a.LagParent != "") != (b.LagParent != "") {
				if a.LagParent != "" {
					return -1
				}
				return 1
			}
			return cmp.Compare(a.Name, b.Name)
		})
		for _, p := range ports {
			if _, unnamed := f.topology.nodes[name].physical[""]; unnamed {
				p.Name = ""
			}
			link := device.Link{State: p.OperStatus, Duplex: device.DuplexHalf}
			if ref, ok := f.byEnd[Endpoint{Node: name, Port: p.Name}]; ok {
				link = linkReport(f.topology, *ref.end, ref.peer.Endpoint)
			}
			out, err := sw.LinkChange(f.clock, p.Name, link)
			if err != nil {
				return err
			}
			f.applyOutput(f.clock, name, out)
		}
		f.scheduleWake(name)
	}
	return nil
}

func (f *Fabric) linkEnd(node, portName string) (linkEndRef, bool) {
	ref, ok := f.byEnd[Endpoint{Node: node, Port: portName}]

	return ref, ok
}

// Links returns an independent deep copy of all resolved links in cable order. Each
// link carries the facts resolution used: a medium and Ethernet facts that
// [Config.PhyAssumption] filled appear here and nowhere in [Fabric.Config].
func (f *Fabric) Links() []Link {
	if len(f.links) == 0 {
		return nil
	}
	cp := make([]Link, len(f.links))
	for i, l := range f.links {
		cp[i] = Link{
			Cable: l.Cable.Clone(),
			A:     l.A.clone(),
			B:     l.B.clone(),
		}
	}

	return cp
}

// unlinked returns one link end per non-LAG port of the named switch that has
// no cable, in port name order: Down with reason no-cable for a port
// [Config.Uncabled] lists, Unknown with reason adjacency-unresolved for any
// other. A port without a cable has no Link, so this is where that reason is
// carried. It returns nil for a node that is not a switch or a switch whose
// ports are all cabled.
func (f *Fabric) unlinked(node string) []LinkEnd {
	n, ok := f.topology.nodes[node]
	if !ok {
		return nil
	}
	var unlinked []LinkEnd
	for _, p := range n.ports.Ports() {
		if p.Kind == port.LAG {
			continue
		}
		ep := Endpoint{Node: node, Port: p.Name}
		if _, ok := f.byEnd[ep]; !ok {
			unlinked = append(unlinked, unlinkedEnd(ep, f.uncabled))
		}
	}
	slices.SortFunc(unlinked, func(a, b LinkEnd) int { return cmp.Compare(a.Port, b.Port) })

	return unlinked
}

func unlinkedEnd(ep Endpoint, uncabled map[Endpoint]Uncabled) LinkEnd {
	if _, ok := uncabled[ep]; ok {
		return LinkEnd{Endpoint: ep, Oper: port.Down, Reason: ReasonNoCable}
	}

	return LinkEnd{Endpoint: ep, Oper: port.Unknown, Reason: ReasonAdjacencyUnresolved}
}

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

// linkTrust holds what a link's resolution could not decide from stated facts.
type linkTrust struct {
	issues     []analysis.Issue
	assumption *analysis.Assumption
}

func resolveLink(c Cable, cfg topology) (Link, linkTrust) {
	a := cfg.end(c.A)
	b := cfg.end(c.B)
	resolved := c.Resolve(a, b, cfg.assumption, cableEndpointsFor(c).Canonical(), c.Evidence)
	c.Cable = resolved.Cable
	return Link{Cable: c, A: LinkEnd{Endpoint: c.A, EndState: resolved.A}, B: LinkEnd{Endpoint: c.B, EndState: resolved.B}}, linkTrust{issues: resolved.Issues, assumption: resolved.Assumption}
}

func endpointLabel(ep Endpoint) string {
	if ep.Port == "" {
		return strconv.Quote(ep.Node)
	}

	return strconv.Quote(ep.Node) + ":" + strconv.Quote(ep.Port)
}

// cableScope is the link scope of a cable, keyed by its Diff subject key.
func cableScope(c Cable) analysis.Scope {
	return analysis.LinkScope(cableEndpointsFor(c).Canonical())
}

// endpointScope is a switch end's port scope, or a host's node scope, since a
// host has one unnamed port.
func endpointScope(ep Endpoint) analysis.Scope {
	if ep.Port == "" {
		return analysis.NodeScope(ep.Node)
	}

	return analysis.PortScope(ep.Node, ep.Port)
}

// SetFault modifies the declared fault on the cable connecting endpoints a and b at the
// current fabric clock, re-evaluating operational link states and notifying attached switches.
// It returns an error if no cable connects the specified endpoints or if the fault configuration is invalid.
func (f *Fabric) SetFault(a, b Endpoint, fault cablemodel.Fault) error {
	defer f.settleTouched()
	if err := cablemodel.ValidateFault(fault); err != nil {
		return err
	}
	normalized := normalizedFault(fault)

	idx := slices.IndexFunc(f.links, func(l Link) bool {
		c := l.Cable
		return (c.A == a && c.B == b) || (c.A == b && c.B == a)
	})
	if idx < 0 {
		return errs.New().
			Attr("endpointA", a).
			Attr("endpointB", b).
			Msgf("no cable found connecting endpoints %v and %v", a, b)
	}

	// Links and configured cables share an index, and byEnd points into the
	// link, so the link is overwritten in place.
	f.topology.cables[idx].Fault = normalized
	return f.relinkAt(idx)
}

func (f *Fabric) relinkAt(idx int) error {
	f.links[idx], f.linkTrust[idx] = resolveLink(f.topology.cables[idx], f.topology)
	f.metadataCache = nil
	linkA, linkB := f.links[idx].A, f.links[idx].B

	type endInfo struct {
		end  LinkEnd
		peer LinkEnd
	}
	ends := []endInfo{
		{end: linkA, peer: linkB},
		{end: linkB, peer: linkA},
	}

	for _, info := range ends {
		sw := f.nodeDevice(info.end.Node)
		if sw == nil {
			continue
		}

		out, err := sw.LinkChange(f.clock, info.end.Port, linkReport(f.topology, info.end, info.peer.Endpoint))
		if err != nil {
			return errs.Wrapf(err, "update operational status for %s", info.end.Port)
		}
		f.applyOutput(f.clock, info.end.Node, out)
		f.scheduleWake(info.end.Node)
	}

	return nil
}

func linkReport(cfg topology, end LinkEnd, peer Endpoint) device.Link {
	return device.Link{
		State: end.Oper, SpeedBPS: end.Speed.SpeedBPS,
		Duplex: device.Duplex(end.Speed.DuplexA), PeerRelays: !cfg.nodes[peer.Node].repeats,
	}
}

func (f *Fabric) applyOutput(now time.Time, node string, out device.Output) {
	for _, em := range out.Emissions {
		f.injectEmission(now, node, em)
	}
	for _, drop := range out.Drops {
		f.recordNeighborFailure(now, node, drop)
	}
}
