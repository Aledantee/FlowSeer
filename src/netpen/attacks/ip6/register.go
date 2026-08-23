// register.go exports the DHCP/IPv6 behavior functions for the runner's
// [runner.Options.Behaviors] map. Each behavior is a thin function over
// the toolkit; durability metadata (class, legs, teardown) lives in the
// catalog (registered in catalog/registrations.go by U4) and is the
// single source the runner's gate consults.
//
// The catalog rows for these 9 behavior pairs are already registered
// in catalog/registrations.go and verified by the oracle test
// (TestOracleDurabilityClassification). This file adds only the run
// functions; it does not re-register metadata (no second registration
// mechanism — KTD8).

package ip6

import "go.aledante.io/FlowSeer/src/netpen/runner"

// Behaviors returns the DHCP/IPv6 behavior map for
// [runner.Options.Behaviors]. The keys are the attack names the
// catalog registered: dhcpstarve, roguedhcp, roguedhcp6, daddos,
// ndpspoof, raguard, roguera, raflood, mld.
func Behaviors() map[string]runner.Behavior {
	return map[string]runner.Behavior{
		"dhcpstarve": RunDHCPStarve,
		"roguedhcp":  RunRogueDHCP,
		"roguedhcp6": RunRogueDHCPv6,
		"daddos":     RunDADDOS,
		"ndpspoof":   RunNDPSpoof,
		"raguard":    RunRAGuard,
		"roguera":    RunRogueRA,
		"raflood":    RunRAFlood,
		"mld":        RunMLD,
	}
}
