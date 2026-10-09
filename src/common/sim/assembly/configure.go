package assembly

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

// Configure changes a switch at the fabric clock to the provided configuration,
// re-evaluating operational link states, updating attached devices, and
// restarting protocol layers from the new switch state.
// It returns an error and leaves the fabric untouched if node is not a switch
// or if the configuration fails validation.
func Configure(f *fabric.Fabric, node string, cfg vswitch.Config) error {
	curSw := curSwitch(f, node)
	ok := curSw != nil
	if !ok {
		return errs.New().
			Attr("node", node).
			Msgf("node %q is not a switch", node)
	}

	spec := Spec(f)
	swSpec := curSw.Spec()
	swSpec.Seeds = curSw.StaticSeeds()
	swSpec.Config = cfg
	spec.Switches[node] = swSpec

	normSpec, err := normalizeConstructionSpec(f, spec)
	if err != nil {
		return err
	}

	normCfg := normSpec.Config()
	top := configuredTopology(normCfg)
	tempByEnd := make(map[fabric.Endpoint]linkEndRef)
	for _, c := range top.Cables {
		link := resolveLink(c, top)
		tempByEnd[c.A] = linkEndRef{end: &link.A}
		tempByEnd[c.B] = linkEndRef{end: &link.B}
	}
	uncabled := make(map[fabric.Endpoint]fabric.Uncabled)
	for _, entry := range top.Uncabled {
		uncabled[entry.Endpoint] = entry
	}
	rebuiltPorts, err := rebuildSwitchPorts(node, normSpec.Switches[node].Config.Ports, tempByEnd, uncabled)
	if err != nil {
		return err
	}

	targetSpec := normSpec.Switches[node]
	targetSpec.Config.Ports = rebuiltPorts

	derived, out, err := vswitch.Derive(curSw, targetSpec, f.Clock())
	if err != nil {
		return errs.Wrapf(err, "derive switch %q", node)
	}

	n := top.Nodes[node]
	n.Device, n.Output = derived, out
	return fabric.Replace(f, node, n)
}
