// register.go exports the routing-injection and rogue-WPAD behavior functions
// for the runner's [runner.Options.Behaviors] map. Each behavior is a thin
// function over the toolkit; durability metadata (class, legs, teardown)
// lives in the catalog (registered in catalog/registrations.go) and
// is the single source the runner's gate consults.
//
// The catalog rows for these 3 behaviors are already registered in
// catalog/registrations.go and verified by the oracle test
// (TestOracleDurabilityClassification). This file adds only the run
// functions; it does not re-register metadata — the catalog is the
// single registration mechanism.

package routing

import "go.aledante.io/FlowSeer/src/edge/netpen/runner"

// Behaviors returns the routing-injection and rogue-WPAD behavior map for
// [runner.Options.Behaviors]. The keys are the attack names the catalog
// registered: ospf, eigrp, wpad.
func Behaviors() map[string]runner.Behavior {
	return map[string]runner.Behavior{
		"ospf":  RunOSPF,
		"eigrp": RunEIGRP,
		"wpad":  RunWPAD,
	}
}
