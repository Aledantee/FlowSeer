package fabric

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
)

// Derive constructs a new [Fabric] from the target specification, seeding each switch
// with dynamic forwarding database entries from its namesake in cur that the target
// still admits. A caller deriving mid-run passes cur.Snapshot().Clock as Start in target so
// cloned timers continue from the simulation clock; an unset Start takes cur's clock.
// Switches in target without a namesake in cur are built fresh. The layers start only after
// the cloned ones are in place, so no proposal from a switch that is thrown away reaches
// the queue, and a cloned layer hears only the links that differ from cur's.
func Derive(cur *Fabric, target ConstructionSpec) (*Fabric, error) {
	if cur != nil && target.Start.IsZero() {
		target.Start = cur.clock
	}
	next, err := build(cur, target)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		next.startLayers(next.switchNames())

		return next, nil
	}

	for name, sw := range next.switches {
		curSw := cur.Switch(name)
		if curSw == nil {
			continue
		}

		derived, err := vswitch.Derive(curSw, sw.Spec())
		if err != nil {
			return nil, errs.Wrapf(err, "derive switch %q", name)
		}
		next.switches[name] = derived
		derivedCfg := derived.Config()
		derivedCfg.Ports = next.cfg.Switches[name].Ports
		next.cfg.Switches[name] = derivedCfg
	}
	next.startLayers(next.switchNames())

	return next, nil
}
