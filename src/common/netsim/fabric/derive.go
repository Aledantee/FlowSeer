package fabric

import "go.aledante.io/FlowSeer/src/common/netsim/vswitch"

// Derive constructs a new [Fabric] from the target configuration, seeding each switch
// with dynamic forwarding database entries from its namesake in cur that the new configuration
// still admits. Switches in cfg without a namesake in cur are built fresh.
func Derive(cur *Fabric, cfg Config) (*Fabric, error) {
	next, err := New(cfg)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return next, nil
	}

	for name, sw := range next.switches {
		curSw := cur.Switch(name)
		if curSw == nil {
			continue
		}

		derived, err := vswitch.Derive(curSw, sw.Config())
		if err != nil {
			return nil, err
		}
		next.switches[name] = derived
	}

	return next, nil
}
