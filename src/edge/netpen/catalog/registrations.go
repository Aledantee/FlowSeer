package catalog

// registrations.go owns the full 35-behavior metadata registration table. Every
// CLI command name and the 8 superset attacks are registered here with
// the correctness commitment: each (behavior, mode) pair's class, legs,
// and teardown match the durability oracle row-for-row.
//
// This file is the production registration path for [Register]. Attack packages
// contain the run functions and expose runner behavior maps; they do not call
// [Register].

func init() {
	nonDestructive := []Behavior{
		{Name: "scan", Protocols: []string{"arp", "ipv6-nd"}, Preconditions: nil, Legs: WatchOptional, Class: NonDestructive, Help: "passive/active segment discovery"},
		{Name: "arpsweep", Protocols: []string{"arp"}, Legs: AttackOnly, Class: NonDestructive, Help: "sweep a subnet for live hosts via ARP"},
		{Name: "vlanenum", Protocols: []string{"vlan", "stp"}, Legs: AttackOnly, Class: NonDestructive, Help: "enumerate VLANs from a trunk port"},
		{Name: "ghost", Protocols: []string{"arp"}, Preconditions: []string{"watch-leg"}, Legs: WatchRequired, Class: NonDestructive, Help: "ghost-VLAN traversal evidence via the watch leg"},
		{Name: "raguard", Protocols: []string{"ipv6-nd", "ra"}, Legs: WatchOptional, Class: NonDestructive, Help: "observe forged RA handling under RA-Guard"},
	}
	for _, b := range nonDestructive {
		Register(b)
	}

	transientDecay := []Behavior{
		{Name: "stproot", Protocols: []string{"stp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "engineered max_age~6s", Help: "claim STP root with superior BPDUs"},
		{Name: "camflood", Protocols: []string{"eth"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "CAM table ages out", Help: "flood the CAM table to force fail-open switching"},
		{Name: "doubletag", Protocols: []string{"vlan"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "one-shot injected frames; nothing persists", Help: "VLAN double-tagging hop to another VLAN"},
		{Name: "dhcpstarve", Protocols: []string{"dhcp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "lease pool recovers", Help: "exhaust a DHCP server's lease pool"},
		{Name: "gratarp", Protocols: []string{"arp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "neighbor cache ages", Help: "gratuitous ARP to announce a MAC"},
		{Name: "llmnr", Protocols: []string{"llmnr"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "spoofed answers expire", Help: "spoof LLMNR name resolution"},
		{Name: "daddos", Protocols: []string{"ipv6-nd"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "DAD window passes", Help: "duplicate-address denial via DAD"},
		{Name: "ndpspoof", Protocols: []string{"ipv6-nd"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "NUD / real master resumes", Help: "spoof IPv6 neighbor discovery"},
		{Name: "vrrp", Protocols: []string{"vrrp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "NUD / real master resumes", Help: "spoof VRRP to claim the virtual router"},
		{Name: "roguedhcp6", Protocols: []string{"dhcpv6"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "~300s lifetime announced", Help: "rogue DHCPv6 server"},
		{Name: "roguera", Protocols: []string{"ipv6-nd", "ra"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "~1800s lifetime announced", Help: "rogue router advertisement"},
		{Name: "roguedhcp", Protocols: []string{"dhcp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "client leases ~1800s", Help: "rogue DHCPv4 server"},
		{Name: "icmpredirect", Protocols: []string{"icmp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "victim route cache decays", Help: "ICMP redirect to poison routes"},
		{Name: "mvrp", Protocols: []string{"mvrp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "MRP timers, minutes", Help: "MVRP VLAN registration flood"},
		{
			Name: "vtp", Protocols: []string{"vtp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "revision-bump side effect recorded", Help: "VTP domain manipulation (SAFE mode default)",
			Modes: []Mode{
				{Flag: "wipe", Class: PermanentDestructive, Teardown: "requires per-run opt-in", Help: "VTP VLAN-database wipe"},
				{Flag: "set", Class: PermanentDestructive, Teardown: "requires per-run opt-in", Help: "VTP VLAN-database overwrite"},
			},
		},
		{Name: "ospf", Protocols: []string{"ospf"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "goodbye/flush teardown", Help: "OSPF LSA injection"},
		{Name: "wpad", Protocols: []string{"wpad", "llmnr"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "client proxy config residue, bound = TTL", Help: "rogue WPAD proxy"},
		{Name: "mld", Protocols: []string{"mld"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "bounded bursts / holdtimes", Help: "MLD abuse flood"},
		{Name: "raflood", Protocols: []string{"ipv6-nd", "ra"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "bounded bursts / holdtimes", Help: "RA-flood variant"},
		{Name: "lldpspoof", Protocols: []string{"lldp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "bounded bursts / holdtimes", Help: "generic LLDP spoofing"},
	}
	for _, b := range transientDecay {
		Register(b)
	}

	Register(Behavior{
		Name: "portsteal", Protocols: []string{"arp"}, Legs: AttackOnly, Class: TransientDecay, Teardown: "CAM aging", Help: "port-steal ARP spoofing",
		Modes: []Mode{
			{Flag: "relay", Class: TemporaryRestored, Teardown: "ip_forward restore armed", Help: "port-steal with relay enabled"},
		},
	})

	temporaryRestored := []Behavior{
		{Name: "arpspoof", Protocols: []string{"arp"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "neighbor unicast repairs + ip_forward restore", Help: "ARP cache poisoning"},
		{
			Name: "vlanhop", Protocols: []string{"vlan"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "active restore armed", Help: "VLAN hopping attack",
			Modes: []Mode{
				{Flag: "persist", Class: PermanentDestructive, Teardown: "host-side persistence, flag is the opt-in", Help: "persistent VLAN hop"},
			},
		},
		{
			Name: "voicevlan", Protocols: []string{"vlan", "lldp"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "active restore armed", Help: "voice-VLAN hijack",
			Modes: []Mode{
				{Flag: "persist", Class: PermanentDestructive, Teardown: "host-side persistence, flag is the opt-in", Help: "persistent voice-VLAN hijack"},
			},
		},
		{Name: "hsrp", Protocols: []string{"hsrp"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "resign teardown", Help: "HSRP active-router hijack"},
		{
			Name: "dtp", Protocols: []string{"dtp"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "access-port restore armed by default", Help: "DTP trunk negotiation",
			Modes: []Mode{
				{Flag: "keep-trunk", Class: PermanentDestructive, Teardown: "opt-in to keep the negotiated trunk", Help: "keep the negotiated trunk (no restore)"},
			},
		},
		{Name: "eigrp", Protocols: []string{"eigrp"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "goodbye/flush teardown", Help: "EIGRP route injection"},
		{Name: "etherchannel", Protocols: []string{"lacp", "pagp"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "port-channel release", Help: "EtherChannel (LACP/PAgP) attack"},
		{Name: "glbp", Protocols: []string{"glbp"}, Legs: AttackOnly, Class: TemporaryRestored, Teardown: "resign teardown", Help: "GLBP virtual-router hijack"},
	}
	for _, b := range temporaryRestored {
		Register(b)
	}

	// full is orchestration scripting the gate and carries no (attack,
	// mode) entry of its own. It has a CLI dispatch handler but no
	// catalog row; the CLI reconciliation test allows it as the sole
	// CLI-only orchestration name.
}
