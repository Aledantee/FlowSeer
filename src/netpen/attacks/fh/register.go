// register.go exports the first-hop/identity behavior functions for the
// runner's [runner.Options.Behaviors] map. Each behavior is a thin function
// over the toolkit; durability metadata (class, legs, teardown) lives in
// the catalog (registered in catalog/registrations.go by U4) and is the
// single source the runner's gate consults.
//
// The catalog rows for these 10 behavior pairs are already registered
// in catalog/registrations.go and verified by the oracle test
// (TestOracleDurabilityClassification). This file adds only the run
// functions; it does not re-register metadata (no second registration
// mechanism — KTD8).

package fh

import "go.aledante.io/FlowSeer/src/netpen/runner"

// Behaviors returns the first-hop/identity behavior map for
// [runner.Options.Behaviors]. The keys are the attack names the catalog
// registered: arpsweep, arpspoof, gratarp, hsrp, vrrp, icmpredirect,
// llmnr, ghost, glbp, lldpspoof.
func Behaviors() map[string]runner.Behavior {
	return map[string]runner.Behavior{
		"arpsweep":     RunARPSweep,
		"arpspoof":     RunARPSpoof,
		"gratarp":      RunGratARP,
		"hsrp":         RunHSRP,
		"vrrp":         RunVRRP,
		"icmpredirect": RunICMPRedirect,
		"llmnr":        RunLLMNR,
		"ghost":        RunGhost,
		"glbp":         RunGLBP,
		"lldpspoof":    RunLLDPSpoof,
	}
}
