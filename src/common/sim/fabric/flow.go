package fabric

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/flow"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// flowStatsSince returns what the fabric folded into the statistics after pre was read.
// Offered, Lost, Unresolved, Rejected, the per-key counts, and the latency
// count and sum only grow, so each subtracts. Held is a gauge and the latency
// extremes do not subtract: both stay as they are, and the caller treats them
// as unseparable when pre holds any.
func flowStatsSince(s, pre flow.Stats) flow.Stats {
	cp := s
	cp.Offered -= pre.Offered
	cp.Lost -= pre.Lost
	cp.Unresolved -= pre.Unresolved
	cp.Rejected -= pre.Rejected
	cp.Delivered = countsSince(s.Delivered, pre.Delivered)
	cp.Drops = countsSince(s.Drops, pre.Drops)
	cp.Copies = countsSince(s.Copies, pre.Copies)
	cp.Latency.Sum -= pre.Latency.Sum
	cp.Latency.Count -= pre.Latency.Count

	return cp
}

// countsSince returns the keys whose count grew past pre, with the growth.
func countsSince[K comparable](counts, pre map[K]uint64) map[K]uint64 {
	var out map[K]uint64
	for key, n := range counts {
		if n -= pre[key]; n > 0 {
			if out == nil {
				out = make(map[K]uint64)
			}
			out[key] = n
		}
	}

	return out
}

// Flows returns an independent copy of every flow's statistics, keyed by flow
// id. A run with no folded statistics or held flow returns nil.
func (f *Fabric) Flows() map[flow.ID]flow.Stats {
	if len(f.flows) == 0 && len(f.held) == 0 {
		return nil
	}
	out := make(map[flow.ID]flow.Stats, len(f.flows))
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
// A protocol journey keeps its issues in the fabric's protocol set first, since
// it has no flow to hold them.
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
	if j.Protocol {
		f.foldProtocolIssues(j)
	}
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
		addLatency(&stats.Latency, delivery.At.Sub(j.Injection.At))
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

// foldProtocolIssues adds the journey's issues that the fabric does not hold
// yet to the protocol set, so the set grows with the distinct issues and not
// with the number of protocol frames.
func (f *Fabric) foldProtocolIssues(j *journey) {
	for _, iss := range j.Metadata.Issues() {
		if !slices.ContainsFunc(f.protocolIssues, func(kept analysis.Issue) bool { return analysis.SameIssue(kept, iss) }) {
			f.protocolIssues = append(f.protocolIssues, iss)
		}
	}
}

func (f *Fabric) flowAccumulator(id flow.ID) *flow.Stats {
	if f.flows == nil {
		f.flows = make(map[flow.ID]*flow.Stats)
	}
	stats := f.flows[id]
	if stats == nil {
		stats = &flow.Stats{}
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

func addLatency(l *flow.LatencyStats, d time.Duration) {
	if l.Count == 0 || d < l.Min {
		l.Min = d
	}
	if d > l.Max {
		l.Max = d
	}
	l.Sum += d
	l.Count++
}
