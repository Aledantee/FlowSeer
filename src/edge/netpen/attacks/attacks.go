// Package attacks assembles the shipped netpen catalog from the four
// behavior families and the scan orchestration entry. The catalog package
// cannot import the families, which import it, so the assembly lives here
// and a host hands [Default] to the runner.
package attacks

import (
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/fh"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/ip6"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/l2"
	"go.aledante.io/FlowSeer/src/edge/netpen/attacks/routing"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

// Default returns the shipped catalog assembled from [Behaviors]. The
// families' entries are literals this package controls, so the assembly
// cannot fail.
func Default() catalog.Catalog {
	return catalog.MustNew(Behaviors()...)
}

// Behaviors returns every shipped behavior: scan, the one orchestration
// entry the command dispatches to its own handler, then the l2, fh, ip6,
// and routing families. A test replaces a Run before building its own
// catalog. Each call returns a fresh slice.
func Behaviors() []catalog.Behavior {
	behaviors := []catalog.Behavior{{
		Entry: check.Entry{
			Name: "scan", Protocols: []string{"arp", "ipv6-nd"}, Legs: check.WatchOptional,
			Impact: check.ImpactNone, Restore: check.Restore{Kind: check.RestoreNone}, Affects: check.AffectsNone,
			Help: "passive/active segment discovery", Orchestration: true,
		},
	}}
	behaviors = append(behaviors, l2.Behaviors()...)
	behaviors = append(behaviors, fh.Behaviors()...)
	behaviors = append(behaviors, ip6.Behaviors()...)
	behaviors = append(behaviors, routing.Behaviors()...)
	return behaviors
}
