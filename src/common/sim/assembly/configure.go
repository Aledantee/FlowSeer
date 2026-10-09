package fabric

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
)

// Configure changes switch node at f.clock to the provided configuration,
// re-evaluating operational link states, updating attached devices, and
// restarting protocol layers from the new switch state.
// It returns an error and leaves the fabric untouched if node is not a switch
// or if the configuration fails validation.
func (f *Fabric) Configure(node string, cfg vswitch.Config) error {
	curSw := f.Switch(node)
	ok := curSw != nil
	if !ok {
		return errs.New().
			Attr("node", node).
			Msgf("node %q is not a switch", node)
	}

	spec := f.Spec()
	swSpec := curSw.Spec()
	swSpec.Seeds = curSw.StaticSeeds()
	swSpec.Config = cfg
	spec.Switches[node] = swSpec

	normSpec, err := normalizeConstructionSpec(f, spec)
	if err != nil {
		return err
	}

	normCfg := normSpec.Config()
	type resolvedLink struct {
		link  Link
		trust linkTrust
	}
	newLinks := make(map[int]resolvedLink)
	var changedLinks []int

	for i, c := range f.topology.cables {
		if c.A.Node != node && c.B.Node != node {
			continue
		}
		nl, nt := resolveLink(c, configuredTopology(normCfg))
		newLinks[i] = resolvedLink{link: nl, trust: nt}
		if linkStateChanged(f.links[i], f.topology, nl, configuredTopology(normCfg)) {
			changedLinks = append(changedLinks, i)
		}
	}

	tempByEnd := make(map[Endpoint]linkEndRef, len(f.byEnd))
	for ep, ref := range f.byEnd {
		tempByEnd[ep] = ref
	}
	for _, rl := range newLinks {
		tempByEnd[rl.link.A.Endpoint] = linkEndRef{link: &rl.link, end: &rl.link.A, peer: &rl.link.B}
		tempByEnd[rl.link.B.Endpoint] = linkEndRef{link: &rl.link, end: &rl.link.B, peer: &rl.link.A}
	}

	rebuiltPorts, err := rebuildSwitchPorts(node, normSpec.Switches[node].Config.Ports, tempByEnd, f.uncabled)
	if err != nil {
		return err
	}

	targetSpec := normSpec.Switches[node]
	targetSpec.Config.Ports = rebuiltPorts

	derived, out, err := vswitch.Derive(curSw, targetSpec, f.clock)
	if err != nil {
		return errs.Wrapf(err, "derive switch %q", node)
	}

	nextLinks := f.Links()
	nextTrust := cloneLinkTrustSlice(f.linkTrust)
	for i, rl := range newLinks {
		nextLinks[i] = rl.link
		nextTrust[i] = rl.trust
	}
	n := configuredTopology(normCfg).nodes[node]
	n.device, n.output = derived, out
	return f.replace(node, n, nextLinks, nextTrust, changedLinks)
}

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
