package routing

import (
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// Behaviors returns the routing-injection and rogue-WPAD behaviors with
// their catalog entries: ospf, eigrp, and wpad. Each call returns a fresh
// slice.
func Behaviors() []catalog.Behavior {
	active := check.Restore{Kind: check.RestoreActive}
	return []catalog.Behavior{
		{Entry: check.Entry{Name: "ospf", Protocols: []string{"ospf"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsFabric, Help: "OSPF LSA injection"}, Run: RunOSPF},
		{Entry: check.Entry{Name: "eigrp", Protocols: []string{"eigrp"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: active, Affects: check.AffectsFabric, Help: "EIGRP route injection"}, Run: RunEIGRP},
		{Entry: check.Entry{Name: "wpad", Protocols: []string{"wpad", "llmnr"}, Legs: check.AttackOnly, Impact: check.ImpactTemporary, Restore: check.Restore{Kind: check.RestoreDecay, Bound: wpadPacTTL * time.Second}, Affects: check.AffectsClients, Help: "rogue WPAD proxy"}, Run: RunWPAD},
	}
}
