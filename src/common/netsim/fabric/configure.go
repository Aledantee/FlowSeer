package fabric

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
)

// Configure changes switch node at f.clock to the provided configuration,
// re-evaluating operational link states, updating attached devices, and
// restarting protocol layers from the new switch state.
// It returns an error and leaves the fabric untouched if node is not a switch
// or if the configuration fails validation.
func (f *Fabric) Configure(node string, cfg vswitch.Config) error {
	curSw, ok := f.switches[node]
	if !ok {
		return errs.New().
			Attr("node", node).
			Msgf("node %q is not a switch", node)
	}

	spec := f.Spec()
	swSpec := curSw.Spec()
	var staticSeeds []bridge.Seed
	for _, s := range swSpec.Seeds {
		if s.Lifetime == bridge.Static {
			staticSeeds = append(staticSeeds, s)
		}
	}
	swSpec.Config = cfg
	swSpec.Seeds = staticSeeds
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

	for i, c := range f.cfg.Cables {
		if c.A.Node != node && c.B.Node != node {
			continue
		}
		nl, nt := resolveLink(c, normCfg)
		newLinks[i] = resolvedLink{link: nl, trust: nt}
		if linkStateChanged(f.links[i], f.cfg, nl, normCfg) {
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

	derived, err := vswitch.Derive(curSw, targetSpec)
	if err != nil {
		return errs.Wrapf(err, "derive switch %q", node)
	}

	f.initRunState()
	defer f.settleTouched()

	for i, rl := range newLinks {
		f.links[i] = rl.link
		f.linkTrust[i] = rl.trust
	}

	f.switches[node] = derived
	derivedCfg := derived.Config()
	derivedCfg.Ports = normSpec.Switches[node].Config.Ports
	f.cfg.Switches[node] = derivedCfg

	notifyLAGMembers(derived, node, f.cfg, f.clock, f.byEnd)

	for _, idx := range changedLinks {
		if err := f.relinkAt(idx); err != nil {
			return err
		}
	}

	f.startLayers([]string{node})

	for _, em := range derived.Drain() {
		f.injectEmission(f.clock, node, em)
	}
	for _, drop := range derived.DrainNeighborFailures() {
		f.recordNeighborFailure(f.clock, node, drop)
	}

	f.removeWake(node)
	f.scheduleWake(node)

	f.metadataCache = nil

	return nil
}

func linkStateChanged(old Link, oldCfg Config, next Link, nextCfg Config) bool {
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
	oldP2PA := portPointToPoint(oldCfg, old.A.Node, old.A.Port, old.A, old.B.Endpoint)
	newP2PA := portPointToPoint(nextCfg, next.A.Node, next.A.Port, next.A, next.B.Endpoint)
	if oldP2PA != newP2PA {
		return true
	}
	oldP2PB := portPointToPoint(oldCfg, old.B.Node, old.B.Port, old.B, old.A.Endpoint)
	newP2PB := portPointToPoint(nextCfg, next.B.Node, next.B.Port, next.B, next.A.Endpoint)
	return oldP2PB != newP2PB
}
