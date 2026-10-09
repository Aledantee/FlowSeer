package fabric

import (
	"maps"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// FlowID identifies a caller's traffic stream within a fabric run. Zero names
// no flow: an injection that leaves it zero folds into no statistics.
type FlowID uint32

// FlowStats reports what became of the frames one flow offered. Delivered
// counts accepted deliveries per destination host. Copies counts the
// deliveries of mirror and reflector copies per mirror name or "reflection",
// which are never the stream being delivered: a copy contributes only this
// count and its metadata, so its latency and its drops, losses, unresolved,
// rejection outcomes stay out of the stream's counters. Drops counts
// drop events by reason, so a flooded frame refused on two egress ports counts
// twice; Lost counts cable losses; Unresolved and Rejected count frames
// whose journey ended in those outcomes. Held is the number of the flow
// frames currently in device hold queues when Flows is read, including copies.
// Latency summarizes Delivery.At minus the injection time over every
// non-copy delivery. Metadata is the merge of every folded journey's metadata.
type FlowStats struct {
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
func (s FlowStats) Clone() FlowStats {
	cp := s
	cp.Delivered = maps.Clone(s.Delivered)
	cp.Drops = maps.Clone(s.Drops)
	cp.Copies = maps.Clone(s.Copies)

	return cp
}

// Flows returns an independent copy of every flow's statistics, keyed by flow
// id. A run with no folded statistics or held flow returns nil.
func (f *Fabric) Flows() map[FlowID]FlowStats {
	if len(f.flows) == 0 && len(f.held) == 0 {
		return nil
	}
	out := make(map[FlowID]FlowStats, len(f.flows))
	for id, stats := range f.flows {
		out[id] = stats.Clone()
	}

	for _, fid := range f.held {
		j := f.journeys[fid]
		if j == nil || j.Injection.Flow == 0 {
			continue
		}
		stats := out[j.Injection.Flow]
		stats.Held++
		out[j.Injection.Flow] = stats
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// settle folds a journey once its last arrival, queued transmission, or hold
// leaves. Aggregate and unretained protocol journeys release their traversal storage.
func (f *Fabric) settle(fid FrameID) {
	j := f.journeys[fid]
	if j == nil || j.settled || f.inflight[fid] > 0 {
		return
	}
	j.settled = true
	if j.Injection.Flow != 0 {
		f.foldJourney(j)
	}
	delete(f.inflight, fid)
	if j.Injection.Retention == RetainAggregate || j.Protocol {
		delete(f.journeys, fid)
		delete(f.entered, fid)
	}
}

// foldJourney folds one settled journey into its flow's statistics. A
// mirror or reflector copy counts its deliveries under Copies, keyed by its
// mirror name, rather than under Delivered, and contributes nothing else to
// the stream's counters: its latency is measured from the mirror hop's own
// injection, and its drops, losses, unresolved, rejection, and held outcomes
// are the copy's, not the stream's. It still merges its metadata.
func (f *Fabric) foldJourney(j *journey) {
	stats := f.flowAccumulator(j.Injection.Flow)
	if j.Origin.Kind == OriginInjection {
		stats.Offered++
	}

	if j.Origin.Kind == OriginMirror || j.Origin.Kind == OriginReflection {
		for range j.Deliveries {
			if stats.Copies == nil {
				stats.Copies = make(map[string]uint64)
			}
			stats.Copies[copyMirrorName(j)]++
		}
		stats.Metadata = stats.Metadata.Merge(j.Metadata)

		return
	}

	for _, delivery := range j.Deliveries {
		if stats.Delivered == nil {
			stats.Delivered = make(map[string]uint64)
		}
		stats.Delivered[delivery.Host]++
		stats.Latency.add(delivery.At.Sub(j.Injection.At))
	}

	for _, e := range j.Entries {
		switch e.Kind {
		case EntryDrop:
			if stats.Drops == nil {
				stats.Drops = make(map[trace.Reason]uint64)
			}
			stats.Drops[e.Reason]++
		case EntryLoss:
			stats.Lost++
		case EntryUnresolved:
			stats.Unresolved++
		case EntryRejection:
			stats.Rejected++
		}
	}

	stats.Metadata = stats.Metadata.Merge(j.Metadata)
}

func (f *Fabric) flowAccumulator(id FlowID) *FlowStats {
	if f.flows == nil {
		f.flows = make(map[FlowID]*FlowStats)
	}
	stats := f.flows[id]
	if stats == nil {
		stats = &FlowStats{}
		f.flows[id] = stats
	}

	return stats
}

// copyMirrorName names the copy bucket a mirror or reflector journey folds
// into: its mirror's name, or "reflection" for a reflector copy, which
// carries no mirror name.
func copyMirrorName(j *journey) string {
	if j.Origin.Mirror != "" {
		return j.Origin.Mirror
	}

	return "reflection"
}

func (l *LatencyStats) add(d time.Duration) {
	if l.Count == 0 || d < l.Min {
		l.Min = d
	}
	if d > l.Max {
		l.Max = d
	}
	l.Sum += d
	l.Count++
}
