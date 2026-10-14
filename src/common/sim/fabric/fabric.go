package fabric

import (
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/flow"
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
	arrivalQueue
	attachments    []attachedSource
	nextFrameID    FrameID
	nextSeq        uint64
	journeys       map[FrameID]*journey
	entered        map[FrameID]map[Endpoint]bool
	cableCrossings map[Endpoint]uint
	busyUntil      map[Endpoint]time.Time
	egress         map[Endpoint]*egressQueue
	counters       map[Endpoint]*Counters

	// unstatedBacked retains each endpoint's first queue-threshold evidence ref.
	unstatedBacked map[Endpoint]trace.EvidenceRef

	// metadataCache holds the value [Fabric.Metadata] last built. Its inputs
	// are fixed after construction except where [Fabric.SetFault] rewrites a
	// link and its trust, [Replace] rebuilds a switch's ports and
	// links, or a queue with no stated buffer first backs up past one
	// maximum-size frame; each nils the cache. The value is immutable, so a
	// fork may share it.
	metadataCache *analysis.Metadata

	// inflight counts the arrivals of each frame that are still in the queue
	// or an egress queue, including device holds. A frame settles, folding
	// and possibly freeing its journey, when its count reaches zero at the end of the call that
	// touched it.
	inflight map[FrameID]int
	// held maps device-scoped tokens to frames still in flight.
	held map[holdKey]FrameID
	// flows accumulates per-flow statistics as journeys settle.
	flows map[flow.ID]*flow.Stats
	// protocolIssues holds the distinct issues settled protocol journeys
	// carried. A protocol journey is freed at settle and has no flow, so this
	// set is the only place its issues outlive it.
	protocolIssues []analysis.Issue
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
	f.applyDeviceOutput(now, node, out, nil)
}

type holdKey struct {
	node  string
	token device.HoldToken
}
