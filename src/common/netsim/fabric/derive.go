package fabric

import (
	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
)

// Derive constructs a new [Fabric] from the target configuration, seeding each switch
// with dynamic forwarding database entries from its namesake in cur that the new configuration
// still admits. A caller deriving mid-run passes cur.Snapshot().Clock as Start in cfg so
// cloned timers continue from the simulation clock; an unset Start takes cur's clock.
// Switches in cfg without a namesake in cur are built fresh. The layers start only after
// the cloned ones are in place, so no proposal from a switch that is thrown away reaches
// the queue, and a cloned layer hears only the links that differ from cur's.
func Derive(cur *Fabric, cfg Config) (*Fabric, error) {
	if cur != nil && cfg.Start.IsZero() {
		cfg.Start = cur.clock
	}
	next, err := build(cfg)
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

		derived, err := vswitch.Derive(curSw, sw.Config())
		if err != nil {
			return nil, errs.Wrapf(err, "derive switch %q", name)
		}
		next.switches[name] = derived
	}
	next.startLayers(next.switchNames())

	return next, nil
}
