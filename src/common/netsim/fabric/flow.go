package fabric

import (
	"maps"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// FlowID identifies a caller's traffic stream within a fabric run. Zero names
// no flow: an injection that leaves it zero folds into no statistics.
type FlowID uint32

// FlowStats reports what became of the frames one flow offered. Delivered
// counts accepted deliveries per destination host; Copies counts the
// deliveries of mirror and reflector copies per mirror name or "reflection",
// which are never the stream being delivered. Drops counts dropped frames by
// reason; Lost counts cable losses; Unresolved, Rejected, and Held count
// frames whose journey ended in those outcomes. Latency summarizes
// Delivery.At minus the injection time over every delivery. Metadata is the
// merge of every folded journey's metadata.
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
// id. A run that folded nothing returns nil.
func (f *Fabric) Flows() map[FlowID]FlowStats {
	if len(f.flows) == 0 {
		return nil
	}
	out := make(map[FlowID]FlowStats, len(f.flows))
	for id, stats := range f.flows {
		out[id] = stats.Clone()
	}

	return out
}

// settle folds a journey whose last in-flight arrival has left into its flow
// and, when its retention is [RetainAggregate], frees it. A journey settles
// once; a later settle of the same frame is a no-op.
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
	if j.Injection.Retention == RetainAggregate {
		delete(f.journeys, fid)
		delete(f.entered, fid)
	}
}

// foldJourney folds one settled journey into its flow's statistics. A
// mirror or reflector copy counts its deliveries under Copies, keyed by its
// mirror name, rather than under Delivered.
func (f *Fabric) foldJourney(j *Journey) {
	stats := f.flowAccumulator(j.Injection.Flow)
	if j.Origin.Kind == OriginInjection {
		stats.Offered++
	}

	for _, delivery := range j.Deliveries {
		if j.Origin.Kind == OriginMirror {
			if stats.Copies == nil {
				stats.Copies = make(map[string]uint64)
			}
			stats.Copies[copyMirrorName(j)]++
		} else {
			if stats.Delivered == nil {
				stats.Delivered = make(map[string]uint64)
			}
			stats.Delivered[delivery.Host]++
		}
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
		case EntryHop:
			if e.Result != nil && e.Result.Outcome == trace.Held {
				stats.Held++
			}
		}
	}

	stats.Metadata = mergeMetadata(stats.Metadata, j.Metadata)
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
func copyMirrorName(j *Journey) string {
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
