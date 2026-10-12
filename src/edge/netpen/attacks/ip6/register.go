package ip6

import (
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// Behaviors returns the DHCP and IPv6 behaviors with their catalog
// entries: dhcpstarve, roguedhcp, roguedhcp6, daddos, ndpspoof, raguard,
// roguera, raflood, and mld. Each call returns a fresh slice.
func Behaviors() []catalog.Behavior {
	none := check.Restore{Kind: check.RestoreNone}
	decay := check.Restore{Kind: check.RestoreDecay}
	return []catalog.Behavior{
		{Entry: check.Entry{Name: "dhcpstarve", Protocols: []string{"dhcp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsDevice, Help: "exhaust a DHCP server's lease pool"}, Run: RunDHCPStarve},
		{Entry: check.Entry{Name: "roguedhcp", Protocols: []string{"dhcp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: 1800 * time.Second}, Affects: check.AffectsClients, Help: "rogue DHCPv4 server"}, Run: RunRogueDHCP},
		{Entry: check.Entry{Name: "roguedhcp6", Protocols: []string{"dhcpv6"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: 300 * time.Second}, Affects: check.AffectsClients, Help: "rogue DHCPv6 server"}, Run: RunRogueDHCPv6},
		{Entry: check.Entry{Name: "daddos", Protocols: []string{"ipv6-nd"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsClients, Help: "duplicate-address denial via DAD"}, Run: RunDADDOS},
		{Entry: check.Entry{Name: "ndpspoof", Protocols: []string{"ipv6-nd"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsClients, Help: "spoof IPv6 neighbor discovery"}, Run: RunNDPSpoof},
		{Entry: check.Entry{Name: "raguard", Protocols: []string{"ipv6-nd", "ra"}, Legs: check.WatchOptional, Impact: check.ImpactNone, Restore: none, Affects: check.AffectsNone, Help: "observe forged RA handling under RA-Guard"}, Run: RunRAGuard},
		{Entry: check.Entry{Name: "roguera", Protocols: []string{"ipv6-nd", "ra"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: 1800 * time.Second}, Affects: check.AffectsClients, Help: "rogue router advertisement"}, Run: RunRogueRA},
		{Entry: check.Entry{Name: "raflood", Protocols: []string{"ipv6-nd", "ra"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsClients, Help: "RA-flood variant"}, Run: RunRAFlood},
		{Entry: check.Entry{Name: "mld", Protocols: []string{"mld"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsDevice, Help: "MLD abuse flood"}, Run: RunMLD},
	}
}
