package fabric

import (
	"maps"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// FlowID identifies a caller's traffic stream within a fabric run. Zero names
// no flow: an injection that leaves it zero folds into no statistics.
type FlowID uint32

// FlowStats reports what became of the frames one flow offered. Delivered
// counts accepted deliveries per destination host. Copies counts the
// deliveries of mirror and reflector copies per mirror name or "reflection",
// which are never the stream being delivered: a copy contributes only this
// count and its metadata, so its latency and its drops, losses, unresolved,
// rejection, or held outcome stay out of the stream's counters. Drops counts
// drop events by reason, so a flooded frame refused on two egress ports counts
// twice; Lost counts cable losses; Unresolved, Rejected, and Held count frames
// whose journey ended in those outcomes. Latency summarizes Delivery.At minus
// the injection time over every non-copy delivery. Metadata is the merge of
// every folded journey's metadata.
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
// and, when its retention is [RetainAggregate], frees it. A journey a switch
// holds for neighbor resolution settles at the hold, counting under
// [FlowStats.Held] once; because the frame then travels with no journey, the
// freed journey leaves a placeholder keyed by the holding device so
// [Fabric.injectEmission] can still name it as the holder of the released
// frame. A journey settles once; a later settle of the same frame is a no-op.
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
		if isJourneyHeld(j) {
			f.recordHeldAggregate(fid, j)
		}
		delete(f.journeys, fid)
		delete(f.entered, fid)
	}
}

// recordHeldAggregate keeps the FrameID of a freed aggregate journey a switch
// held for neighbor resolution, under the device of the frame's last entry and
// ascending, so a release can still name the frame it was held from. The
// journey is gone; only the identity stays.
func (f *Fabric) recordHeldAggregate(fid FrameID, j *Journey) {
	device := j.Entries[len(j.Entries)-1].Device
	if f.heldAggregates == nil {
		f.heldAggregates = make(map[string][]FrameID)
	}
	ids := f.heldAggregates[device]
	at, _ := slices.BinarySearch(ids, fid)
	ids = slices.Insert(ids, at, fid)
	f.heldAggregates[device] = ids
}

// claimHeldAggregate removes the placeholder a release just named, so a later
// release cannot name the same held frame twice.
func (f *Fabric) claimHeldAggregate(device string, fid FrameID) {
	ids := f.heldAggregates[device]
	for i, id := range ids {
		if id == fid {
			f.heldAggregates[device] = slices.Delete(ids, i, i+1)
			break
		}
	}
}

// foldJourney folds one settled journey into its flow's statistics. A
// mirror or reflector copy counts its deliveries under Copies, keyed by its
// mirror name, rather than under Delivered, and contributes nothing else to
// the stream's counters: its latency is measured from the mirror hop's own
// injection, and its drops, losses, unresolved, rejection, and held outcomes
// are the copy's, not the stream's. It still merges its metadata.
func (f *Fabric) foldJourney(j *Journey) {
	stats := f.flowAccumulator(j.Injection.Flow)
	if j.Origin.Kind == OriginInjection {
		stats.Offered++
	}

	if j.Origin.Kind == OriginMirror {
		for range j.Deliveries {
			if stats.Copies == nil {
				stats.Copies = make(map[string]uint64)
			}
			stats.Copies[copyMirrorName(j)]++
		}
		stats.Metadata = mergeMetadata(stats.Metadata, j.Metadata)

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
