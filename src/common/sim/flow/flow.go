// Package flow defines traffic stream identities and statistics independent of execution.
package flow

import (
	"maps"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// ID identifies a caller's traffic stream within a fabric run. Zero names
// no flow: an injection that leaves it zero folds into no statistics.
type ID uint32

// Stats reports what became of the frames one flow offered. Delivered
// counts accepted deliveries per destination host. Copies counts the
// deliveries of mirror and reflector copies per mirror name or "reflection",
// which are never the stream being delivered: a copy contributes only this
// count and its metadata, so its latency and its drops, losses, unresolved,
// rejection outcomes stay out of the stream's counters. Drops counts
// drop events by reason, so a flooded frame refused on two egress ports counts
// twice; Lost counts cable losses; Unresolved and Rejected count frames
// whose journey ended in those outcomes. Held is the number of the flow's
// frames currently in device hold queues when Flows is read, including copies.
// Latency summarizes Delivery.At minus the injection time over every
// non-copy delivery. Metadata is the merge of every folded journey's metadata.
type Stats struct {
	Offered    uint64
	Delivered  map[string]uint64
	Drops      map[trace.Reason]uint64
	Lost       uint64
	Unresolved uint64
	Rejected   uint64
	Held       uint64
	Copies     map[string]uint64
	Latency    LatencyStats
	Metadata   analysis.Metadata
}

// LatencyStats summarizes the per-delivery latency of a flow: its minimum,
// maximum, and sum over Count deliveries. Min and Max are zero before the
// first delivery.
type LatencyStats struct {
	Min   time.Duration
	Max   time.Duration
	Sum   time.Duration
	Count uint64
}

// Clone returns an independent copy of the statistics. The metadata value is
// immutable and shared.
func (s Stats) Clone() Stats {
	cp := s
	cp.Delivered = maps.Clone(s.Delivered)
	cp.Drops = maps.Clone(s.Drops)
	cp.Copies = maps.Clone(s.Copies)

	return cp
}
