package fabric

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

type builtNode struct {
	device   device.Device
	ports    port.Table
	physical map[string]cablemodel.End
	repeats  bool
	snapshot bool
	output   device.Output
}

type topology struct {
	defaultQueue device.Device
	nodes        map[string]builtNode
	cables       []Cable
	uncabled     []Uncabled
	assumption   *cablemodel.PhyAssumption
	evidence     analysis.EvidenceCatalog
	start        time.Time
}

func (t topology) clone() topology {
	cp := t
	cp.nodes = maps.Clone(t.nodes)
	for name, n := range cp.nodes {
		n.ports = n.ports.Clone()
		n.physical = maps.Clone(n.physical)
		for p, end := range n.physical {
			end.Ethernet = end.Ethernet.Clone()
			n.physical[p] = end
		}
		cp.nodes[name] = n
	}
	cp.cables = make([]Cable, len(t.cables))
	for i, c := range t.cables {
		cp.cables[i] = c.Clone()
	}
	cp.uncabled = cloneUncabled(t.uncabled)
	cp.assumption = t.assumption.Clone()
	return cp
}

func (t topology) end(ep Endpoint) cablemodel.End {
	return t.nodes[ep.Node].physical[ep.Port]
}

func (f *Fabric) nodeDevice(name string) device.Device { return f.topology.nodes[name].device }

func (f *Fabric) topologyCopy() topology {
	t := f.topology.clone()
	t.evidence = f.evidence
	return t
}

func newTopology(t topology) (*Fabric, error) {
	f := &Fabric{
		topology: t, clock: t.start, evidence: t.evidence,
		links: make([]Link, len(t.cables)), linkTrust: make([]linkTrust, len(t.cables)),
		byEnd: make(map[Endpoint]linkEndRef), uncabled: make(map[Endpoint]Uncabled),
		wakes: make(map[string]time.Time), dequeueItems: make(map[Endpoint]int), wakeItems: make(map[string]int),
		nextFrameID: 1, nextSeq: 1, journeys: make(map[FrameID]*Journey), entered: make(map[FrameID]map[Endpoint]bool),
		cableCrossings: make(map[Endpoint]uint), inflight: make(map[FrameID]int), heldAggregates: make(map[string][]FrameID),
	}
	for i, c := range t.cables {
		f.links[i], f.linkTrust[i] = resolveLink(c, t)
		l := &f.links[i]
		f.byEnd[c.A] = linkEndRef{link: l, end: &l.A, peer: &l.B}
		f.byEnd[c.B] = linkEndRef{link: l, end: &l.B, peer: &l.A}
	}
	for _, entry := range t.uncabled {
		f.uncabled[entry.Endpoint] = entry
	}
	for _, name := range slices.Sorted(maps.Keys(t.nodes)) {
		if err := f.startLayers([]string{name}); err != nil {
			return nil, err
		}
		f.applyOutput(f.clock, name, t.nodes[name].output)
		n := f.topology.nodes[name]
		n.output = device.Output{}
		f.topology.nodes[name] = n
	}
	return f, nil
}

func (f *Fabric) replace(name string, node builtNode, links []Link, trust []linkTrust, changed []int) error {
	f.initRunState()
	defer f.settleTouched()
	f.links = links
	f.linkTrust = trust
	for i := range f.links {
		l := &f.links[i]
		f.byEnd[l.A.Endpoint] = linkEndRef{link: l, end: &l.A, peer: &l.B}
		f.byEnd[l.B.Endpoint] = linkEndRef{link: l, end: &l.B, peer: &l.A}
	}
	previous := f.topology.nodes[name]
	f.topology.nodes[name] = node
	if err := f.startLayers([]string{name}); err != nil {
		f.topology.nodes[name] = previous
		return err
	}
	for _, idx := range changed {
		if err := f.relinkAt(idx); err != nil {
			f.topology.nodes[name] = previous
			return err
		}
	}
	f.applyOutput(f.clock, name, node.output)
	node.output = device.Output{}
	f.topology.nodes[name] = node
	f.removeWake(name)
	f.scheduleWake(name)
	f.metadataCache = nil
	return nil
}

func (f *Fabric) operate(name string, fn func(device.Device, time.Time) (device.Output, error)) error {
	f.initRunState()
	defer f.settleTouched()
	out, err := fn(f.nodeDevice(name), f.clock)
	if err != nil {
		return err
	}
	f.applyOutput(f.clock, name, out)
	f.scheduleWake(name)
	return nil
}
