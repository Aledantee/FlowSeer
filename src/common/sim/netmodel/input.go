package netmodel

import (
	"slices"

	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Input supplies the model records and requested capabilities for [Load].
// Empty Want infers capabilities from the records. Missing records retain
// the omission and default reporting described by Load.
type Input struct {
	Interfaces      []*interfacev1.Interface
	VLANs           []*switchingv1.Vlan
	FDB             []*switchingv1.FdbEntry
	Budgets         []*phyv1.PseBudget
	BridgeState     *stpv1.BridgeState
	STPPorts        []*stpv1.PortState
	LACPAggregators []*lacpv1.AggregatorState
	LACPPorts       []*lacpv1.PortState
	Addresses       []*ipv1.InterfaceAddress
	Neighbors       []*ipv1.NeighborEntry
	FilterSets      []*filterv1.FilterRuleSet
	Want            []trace.Layer

	// MST rows may be absent.
	MSTInstances []*stpv1.MstInstance
	MSTPorts     []*stpv1.MstPort
	MSTVLANMaps  []*stpv1.MstVlanMap
}

// DefaultNetworkInstance names the default domain used by model inputs.
const DefaultNetworkInstance = "default"

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
