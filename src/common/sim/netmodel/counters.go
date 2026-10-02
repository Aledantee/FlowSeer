package netmodel

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

// InterfaceCounters converts cumulative per-port fabric counters into a typed network model
// [interfacev1.InterfaceCounters] message with all twelve RFC 2863 counter fields populated.
//
// The fabric starts every link at the run's start and never resets a counter afterwards, so
// start is the counters' last discontinuity. A zero start leaves last_discontinuity unset: a
// fabric without a start time has no instant at which its counting began, and year 1 would
// be a false report.
func InterfaceCounters(c fabric.Counters, start time.Time) *interfacev1.InterfaceCounters {
	b := interfacev1.InterfaceCounters_builder{
		InBytes:             &c.InBytes,
		OutBytes:            &c.OutBytes,
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
	}
	if !start.IsZero() {
		b.LastDiscontinuity = timestamppb.New(start)
	}

	return b.Build()
}
