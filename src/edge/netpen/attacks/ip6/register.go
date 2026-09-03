package ip6

import "go.aledante.io/FlowSeer/src/edge/netpen/runner"

// Behaviors returns the DHCP/IPv6 behavior map for
// [runner.Options.Behaviors]. The keys are the attack names the
// catalog registered: dhcpstarve, roguedhcp, roguedhcp6, daddos,
// ndpspoof, raguard, roguera, raflood, mld.
// Each call returns a new map that callers may modify independently.
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
