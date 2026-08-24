package full

// behaviors.go is the import bridge to the four behavior packages. It
// exists as a separate file so full.go's test-only path (nil Behaviors)
// does not import the attack packages unconditionally in tests — tests
// that substitute stubs do not pull the heavy gopacket dependency graph.
// The production path (mergedBehaviors) calls these helpers.

import (
	"go.aledante.io/FlowSeer/src/netpen/attacks/fh"
	"go.aledante.io/FlowSeer/src/netpen/attacks/ip6"
	"go.aledante.io/FlowSeer/src/netpen/attacks/l2"
	"go.aledante.io/FlowSeer/src/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/netpen/runner"
)

// l2Behaviors returns the L2 behavior map.
func l2Behaviors() map[string]runner.Behavior { return l2.Behaviors() }

// fhBehaviors returns the first-hop/identity behavior map.
func fhBehaviors() map[string]runner.Behavior { return fh.Behaviors() }

// ip6Behaviors returns the DHCP/IPv6 behavior map.
func ip6Behaviors() map[string]runner.Behavior { return ip6.Behaviors() }

// routingBehaviors returns the routing-injection/WPAD behavior map.
func routingBehaviors() map[string]runner.Behavior { return routing.Behaviors() }
