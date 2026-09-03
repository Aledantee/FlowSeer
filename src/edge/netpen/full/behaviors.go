package full

import (
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/fh"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/ip6"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/l2"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// l2Behaviors returns the L2 behavior map.
func l2Behaviors() map[string]runner.Behavior { return l2.Behaviors() }

// fhBehaviors returns the first-hop/identity behavior map.
func fhBehaviors() map[string]runner.Behavior { return fh.Behaviors() }

// ip6Behaviors returns the DHCP/IPv6 behavior map.
func ip6Behaviors() map[string]runner.Behavior { return ip6.Behaviors() }

// routingBehaviors returns the routing-injection/WPAD behavior map.
func routingBehaviors() map[string]runner.Behavior { return routing.Behaviors() }
