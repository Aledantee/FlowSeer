package netmodel

import (
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

// InterfaceCounters converts cumulative per-port fabric counters into a typed network model
// [interfacev1.InterfaceCounters] message with all twelve RFC 2863 counter fields populated.
func InterfaceCounters(c fabric.Counters) *interfacev1.InterfaceCounters {
	return interfacev1.InterfaceCounters_builder{
		InOctets:            &c.InOctets,
		OutOctets:           &c.OutOctets,
		InUnicastPackets:    &c.InUnicast,
		OutUnicastPackets:   &c.OutUnicast,
		InMulticastPackets:  &c.InMulticast,
		OutMulticastPackets: &c.OutMulticast,
		InBroadcastPackets:  &c.InBroadcast,
		OutBroadcastPackets: &c.OutBroadcast,
		InErrors:            &c.InErrors,
		OutErrors:           &c.OutErrors,
		InDiscards:          &c.InDiscards,
		OutDiscards:         &c.OutDiscards,
	}.Build()
}
