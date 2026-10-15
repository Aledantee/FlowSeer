package fh

import (
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// Behaviors returns the first-hop and identity behaviors with their catalog
// entries: arpsweep, arpspoof, gratarp, hsrp, vrrp, icmpredirect, llmnr,
// ghost, glbp, and lldpspoof. Each call returns a fresh slice.
func Behaviors() []catalog.Behavior {
	none := check.Restore{Kind: check.RestoreNone}
	decay := check.Restore{Kind: check.RestoreDecay}
	active := check.Restore{Kind: check.RestoreActive}
	return []catalog.Behavior{
		{Entry: check.Entry{Name: "arpsweep", Protocols: []string{"arp"}, Legs: check.AttackOnly, Impact: check.ImpactNone, Restore: none, Affects: check.AffectsNone, Help: "sweep a subnet for live hosts via ARP"}, Run: RunARPSweep},
		{Entry: check.Entry{Name: "arpspoof", Protocols: []string{"arp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsClients, Help: "ARP cache poisoning"}, Run: RunARPSpoof},
		{Entry: check.Entry{Name: "gratarp", Protocols: []string{"arp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsClients, Help: "gratuitous ARP to announce a MAC"}, Run: RunGratARP},
		{Entry: check.Entry{Name: "hsrp", Protocols: []string{"hsrp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsClients, Help: "HSRP active-router hijack"}, Run: RunHSRP},
		{Entry: check.Entry{Name: "vrrp", Protocols: []string{"vrrp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsClients, Help: "spoof VRRP to claim the virtual router"}, Run: RunVRRP},
		{Entry: check.Entry{Name: "icmpredirect", Protocols: []string{"icmp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsClients, Help: "ICMP redirect to poison routes"}, Run: RunICMPRedirect},
		{Entry: check.Entry{Name: "llmnr", Protocols: []string{"llmnr"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: 30 * time.Second}, Affects: check.AffectsClients, Help: "spoof LLMNR name resolution"}, Run: RunLLMNR},
		{Entry: check.Entry{Name: "ghost", Protocols: []string{"arp"}, Legs: check.WatchRequired, Impact: check.ImpactNone, Restore: none, Affects: check.AffectsNone, Help: "ghost-VLAN traversal evidence via the watch leg"}, Run: RunGhost},
		{Entry: check.Entry{Name: "glbp", Protocols: []string{"glbp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsClients, Help: "GLBP virtual-router hijack"}, Run: RunGLBP},
		{Entry: check.Entry{Name: "lldpspoof", Protocols: []string{"lldp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsDevice, Help: "generic LLDP spoofing"}, Run: RunLLDPSpoof},
	}
}
