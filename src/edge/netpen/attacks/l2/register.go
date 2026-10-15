package l2

import (
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// Behaviors returns the L2 behaviors with their catalog entries: dtp,
// doubletag, vlanenum, vlanhop, voicevlan, stproot, camflood, vtp, mvrp,
// portsteal, and etherchannel. A mode-gated variant shares its base
// behavior's Run, which reads the mode from the resolved entry. Each call
// returns a fresh slice.
func Behaviors() []catalog.Behavior {
	none := check.Restore{Kind: check.RestoreNone}
	decay := check.Restore{Kind: check.RestoreDecay}
	active := check.Restore{Kind: check.RestoreActive}
	return []catalog.Behavior{
		{
			Entry: check.Entry{Name: "dtp", Protocols: []string{"dtp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsDevice, Help: "DTP trunk negotiation"},
			Run:   RunDTP,
			Modes: []catalog.Mode{{Flag: "keep-trunk", Help: "keep the negotiated trunk (no restore)", Impact: check.ImpactPermanent, Restore: none, Affects: check.AffectsDevice}},
		},
		{Entry: check.Entry{Name: "doubletag", Protocols: []string{"vlan"}, Legs: check.WatchOptional, Impact: check.ImpactNone, Restore: none, Affects: check.AffectsNone, Help: "VLAN double-tagging hop to another VLAN"}, Run: RunDoubleTag},
		{Entry: check.Entry{Name: "vlanenum", Protocols: []string{"vlan", "stp"}, Legs: check.AttackOnly, Impact: check.ImpactNone, Restore: none, Affects: check.AffectsNone, Help: "enumerate VLANs from a trunk port"}, Run: RunVlanEnum},
		{Entry: check.Entry{Name: "vlanhop", Protocols: []string{"vlan"}, Legs: check.WatchOptional, Impact: check.ImpactNone, Restore: none, Affects: check.AffectsNone, Help: "VLAN hopping attack"}, Run: RunVlanHop},
		{
			Entry: check.Entry{Name: "voicevlan", Protocols: []string{"vlan", "lldp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsClients, Help: "voice-VLAN hijack"},
			Run:   RunVoiceVLAN,
			Modes: []catalog.Mode{{Flag: "persist", Help: "leave the neighbor entry to expire", Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: lldpTTL * time.Second}, Affects: check.AffectsClients}},
		},
		{Entry: check.Entry{Name: "stproot", Protocols: []string{"stp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: 6 * time.Second}, Affects: check.AffectsFabric, Help: "claim STP root with superior BPDUs"}, Run: RunSTPRoot},
		{Entry: check.Entry{Name: "camflood", Protocols: []string{"eth"}, Legs: check.WatchOptional, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsDevice, Help: "flood the CAM table to force fail-open switching"}, Run: RunCAMFlood},
		{
			Entry: check.Entry{Name: "vtp", Protocols: []string{"vtp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsFabric, Help: "VTP domain manipulation (SAFE mode default)"},
			Run:   RunVTP,
			Modes: []catalog.Mode{
				{Flag: "wipe", Help: "VTP VLAN-database wipe", Impact: check.ImpactPermanent, Restore: none, Affects: check.AffectsFabric},
				{Flag: "set", Help: "VTP VLAN-database overwrite", Impact: check.ImpactPermanent, Restore: none, Affects: check.AffectsFabric},
			},
		},
		{Entry: check.Entry{Name: "mvrp", Protocols: []string{"mvrp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsFabric, Help: "MVRP VLAN registration flood"}, Run: RunMVRP},
		{
			Entry: check.Entry{Name: "portsteal", Protocols: []string{"arp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: decay, Affects: check.AffectsClients, Help: "port-steal ARP spoofing"},
			Run:   RunPortSteal,
			Modes: []catalog.Mode{{Flag: "relay", Help: "port-steal with relay enabled", Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsClients}},
		},
		{Entry: check.Entry{Name: "etherchannel", Protocols: []string{"lacp", "pagp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsDevice, Help: "EtherChannel (LACP/PAgP) attack"}, Run: RunEtherChannel},
	}
}
