// Package fabric runs built devices over cables and compares their execution.
package fabric

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"

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
		arrivalQueue: arrivalQueue{wakes: make(map[string]time.Time), dequeueItems: make(map[Endpoint]int), wakeItems: make(map[string]int)},
		nextFrameID:  1, nextSeq: 1, journeys: make(map[FrameID]*Journey), entered: make(map[FrameID]map[Endpoint]bool),
		cableCrossings: make(map[Endpoint]uint), inflight: make(map[FrameID]int), held: make(map[holdKey]FrameID),
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

// Node supplies a built device and the configured facts used by the medium.
// Device remains owned by the caller until New or Replace accepts the node.
// Snapshot includes the device in state projections. Repeats marks a relay-free repeater.
// Output is scheduled after the device receives its initial links.
type Node struct {
	Device   device.Device
	Ports    port.Table
	Physical map[string]cablemodel.End
	Repeats  bool
	Snapshot bool
	Output   device.Output
}

// Config supplies built nodes and their topology. DefaultQueue supplies the
// queue policy for an endpoint that has no node. Devices are not cloned by Config.
type Config struct {
	Nodes         map[string]Node
	Cables        []Cable
	Uncabled      []Uncabled
	PhyAssumption *cablemodel.PhyAssumption
	Evidence      analysis.EvidenceCatalog
	Start         time.Time
	DefaultQueue  device.Device
}

func topologyFromConfig(c Config) topology {
	t := topology{nodes: make(map[string]builtNode, len(c.Nodes)), cables: c.Cables, uncabled: c.Uncabled, assumption: c.PhyAssumption, evidence: c.Evidence, start: c.Start, defaultQueue: c.DefaultQueue}
	for name, n := range c.Nodes {
		t.nodes[name] = builtNode{device: n.Device, ports: n.Ports, physical: n.Physical, repeats: n.Repeats, snapshot: n.Snapshot, output: n.Output}
	}
	return t.clone()
}

// Config copies the configured topology and its facts, retaining each live
// device. Mutating a returned fact cannot alter the fabric. Not safe for concurrent use.
func (f *Fabric) Config() Config {
	t := f.topologyCopy()
	c := Config{Nodes: make(map[string]Node, len(t.nodes)), Cables: t.cables, Uncabled: t.uncabled, PhyAssumption: t.assumption, Evidence: t.evidence, Start: t.start, DefaultQueue: t.defaultQueue}
	for name, n := range t.nodes {
		c.Nodes[name] = Node{Device: n.device, Ports: n.ports, Physical: n.physical, Repeats: n.repeats, Snapshot: n.snapshot, Output: n.output}
	}
	return c
}

func validateTopology(t topology) error {
	for _, name := range slices.Sorted(maps.Keys(t.nodes)) {
		if name == "" || t.nodes[name].device == nil {
			return errs.Msgf("node %q needs a name and a device", name)
		}
	}
	used := make(map[Endpoint]bool)
	for _, c := range t.cables {
		for _, ep := range []Endpoint{c.A, c.B} {
			n, ok := t.nodes[ep.Node]
			if !ok {
				return errs.Msgf("unknown endpoint node %q", ep.Node)
			}
			if _, ok := n.physical[ep.Port]; !ok {
				return errs.Msgf("unknown endpoint port %q on node %q", ep.Port, ep.Node)
			}
			if used[ep] {
				return errs.Msgf("two cables on endpoint %q:%q", ep.Node, ep.Port)
			}
			used[ep] = true
		}
	}
	return nil
}

// New takes ownership of built devices, resolves their links, and starts the run.
// It returns no fabric on an invalid endpoint, duplicate cable attachment, or link error.
func New(c Config) (*Fabric, error) {
	t := topologyFromConfig(c)
	if err := validateTopology(t); err != nil {
		return nil, err
	}
	return newTopology(t)
}

// Replace installs a built node at the current clock and reports changed links.
// A rejected link report restores the previous node. Not safe for concurrent use.
func Replace(f *Fabric, name string, node Node) error {
	if _, ok := f.topology.nodes[name]; !ok {
		return errs.Msgf("node %q not found", name)
	}
	c := f.Config()
	c.Nodes[name] = node
	t := topologyFromConfig(c)
	if err := validateTopology(t); err != nil {
		return err
	}
	links := f.Links()
	trust := cloneLinkTrustSlice(f.linkTrust)
	var changed []int
	for i, c := range t.cables {
		if c.A.Node != name && c.B.Node != name {
			continue
		}
		links[i], trust[i] = resolveLink(c, t)
		if linkStateChanged(f.links[i], f.topology, links[i], t) {
			changed = append(changed, i)
		}
	}
	return f.replace(name, t.nodes[name], links, trust, changed)
}

// Operate calls fn with the live node and clock, then schedules its output and
// next wake. Errors are returned without applying output. Not safe for concurrent use.
func Operate(f *Fabric, name string, fn func(device.Device, time.Time) (device.Output, error)) error {
	if f.nodeDevice(name) == nil {
		return errs.Msgf("node %q not found", name)
	}
	return f.operate(name, fn)
}

// Device returns the live device by name, or nil when absent. Not safe for concurrent use.
func (f *Fabric) Device(name string) device.Device { return f.nodeDevice(name) }

func linkStateChanged(old Link, oldCfg topology, next Link, nextCfg topology) bool {
	if old.A.Oper != next.A.Oper || old.A.Reason != next.A.Reason || old.A.Speed != next.A.Speed {
		return true
	}
	if old.B.Oper != next.B.Oper || old.B.Reason != next.B.Reason || old.B.Speed != next.B.Speed {
		return true
	}
	if old.A.Ethernet.Canonical() != next.A.Ethernet.Canonical() {
		return true
	}
	if old.B.Ethernet.Canonical() != next.B.Ethernet.Canonical() {
		return true
	}
	oldReportA := linkReport(oldCfg, old.A, old.B.Endpoint)
	newReportA := linkReport(nextCfg, next.A, next.B.Endpoint)
	if oldReportA != newReportA {
		return true
	}
	oldReportB := linkReport(oldCfg, old.B, old.A.Endpoint)
	newReportB := linkReport(nextCfg, next.B, next.A.Endpoint)
	return oldReportB != newReportB
}
