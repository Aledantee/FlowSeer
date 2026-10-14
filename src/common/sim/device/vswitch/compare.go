package vswitch

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
)

// Comparison holds the forwarding results from evaluating the same frame arrival
// on two switches, and reports the derived comparison disposition and the first
// differing behavioral observable.
type Comparison struct {
	Current     device.Output
	Expected    device.Output
	Disposition analysis.Disposition
	Difference  analysis.Difference
}

func compareResults(cur, exp device.Output) Comparison {
	diff, hasDiff := diffForwardResult(cur.Result, exp.Result)
	if !hasDiff {
		copiesA, copiesB := mirrorCopies(cur.Copies), mirrorCopies(exp.Copies)
		if !slices.Equal(copiesA, copiesB) {
			diff = analysis.Difference{Observable: "mirror", Current: strings.Join(copiesA, ","), Expected: strings.Join(copiesB, ",")}
			hasDiff = true
		}
	}
	var disp analysis.Disposition
	switch {
	case cur.Metadata.Status() != analysis.Complete || exp.Metadata.Status() != analysis.Complete:
		disp = analysis.Inconclusive
	case hasDiff:
		disp = analysis.Different
	default:
		disp = analysis.Equivalent
	}

	return Comparison{
		Current:     cur,
		Expected:    exp,
		Disposition: disp,
		Difference:  diff,
	}
}

// Compare evaluates a frame arrival on both switches using [Switch.Peek] without mutating
// their forwarding tables, returning both results and reporting their exact behavioral
// disposition ([analysis.Equivalent], [analysis.Different], or [analysis.Inconclusive]).
// Difference names the first differing observable even when incomplete input
// makes the disposition [analysis.Inconclusive]. Its declared order is:
// typed forwarding outcome and reason, classified FID, egress ports and per-port drops,
// rewritten frame fields (dst, src, ethertype, tags, payload), selected LAG member,
// egress PCP, and mirror copies. Semantic traces and metadata are diagnostic and never
// make a Different.
func Compare(a, b *Switch, now time.Time, port string, f ethernet.Frame) Comparison {
	return compareResults(a.Peek(now, port, f), b.Peek(now, port, f))
}

func diffForwardResult(cur, exp device.Result) (analysis.Difference, bool) {
	if cur.Outcome != exp.Outcome {
		return analysis.Difference{
			Observable: "outcome",
			Current:    string(cur.Outcome),
			Expected:   string(exp.Outcome),
		}, true
	}
	if cur.Reason != exp.Reason {
		return analysis.Difference{
			Observable: "reason",
			Current:    string(cur.Reason),
			Expected:   string(exp.Reason),
		}, true
	}
	if cur.FID != exp.FID {
		return analysis.Difference{
			Observable: "fid",
			Current:    strconv.Itoa(int(cur.FID)),
			Expected:   strconv.Itoa(int(exp.FID)),
		}, true
	}

	// The bridge guarantees at most one Egress entry per logical port (unicast resolves to
	// at most one destination port, and flooding/replication deduplicates candidate ports
	// via a seen set), so keying by eg.Port preserves all egress records without collapse.
	curByPort := make(map[string]device.Egress, len(cur.Egress))
	for _, eg := range cur.Egress {
		curByPort[eg.Port] = eg
	}
	expByPort := make(map[string]device.Egress, len(exp.Egress))
	for _, eg := range exp.Egress {
		expByPort[eg.Port] = eg
	}

	allPorts := make([]string, 0, len(curByPort)+len(expByPort))
	for p := range curByPort {
		allPorts = append(allPorts, p)
	}
	for p := range expByPort {
		if _, ok := curByPort[p]; !ok {
			allPorts = append(allPorts, p)
		}
	}
	slices.Sort(allPorts)

	for _, p := range allPorts {
		egA, okA := curByPort[p]
		egB, okB := expByPort[p]
		if !okA {
			return analysis.Difference{
				Observable: "egress.port",
				Current:    "<absent>",
				Expected:   p,
			}, true
		}
		if !okB {
			return analysis.Difference{
				Observable: "egress.port",
				Current:    p,
				Expected:   "<absent>",
			}, true
		}
		if egA.Dropped != egB.Dropped {
			return analysis.Difference{
				Observable: "egress.dropped",
				Current:    string(egA.Dropped),
				Expected:   string(egB.Dropped),
			}, true
		}
		if egA.Frame.Dst != egB.Frame.Dst {
			return analysis.Difference{
				Observable: "frame.dst",
				Current:    egA.Frame.Dst.String(),
				Expected:   egB.Frame.Dst.String(),
			}, true
		}
		if egA.Frame.Src != egB.Frame.Src {
			return analysis.Difference{
				Observable: "frame.src",
				Current:    egA.Frame.Src.String(),
				Expected:   egB.Frame.Src.String(),
			}, true
		}
		if egA.Frame.EtherType != egB.Frame.EtherType {
			return analysis.Difference{
				Observable: "frame.ethertype",
				Current:    egA.Frame.EtherType.String(),
				Expected:   egB.Frame.EtherType.String(),
			}, true
		}
		if !slices.Equal(egA.Frame.Tags, egB.Frame.Tags) {
			return analysis.Difference{
				Observable: "frame.tags",
				Current:    fmt.Sprint(egA.Frame.Tags),
				Expected:   fmt.Sprint(egB.Frame.Tags),
			}, true
		}
		if !bytes.Equal(egA.Frame.Payload, egB.Frame.Payload) {
			return analysis.Difference{
				Observable: "frame.payload",
				Current:    fmt.Sprintf("%x", egA.Frame.Payload),
				Expected:   fmt.Sprintf("%x", egB.Frame.Payload),
			}, true
		}
		if egA.Member != egB.Member {
			return analysis.Difference{
				Observable: "lag.member",
				Current:    egA.Member,
				Expected:   egB.Member,
			}, true
		}
		if egA.PCP != egB.PCP {
			return analysis.Difference{
				Observable: "pcp",
				Current:    strconv.Itoa(int(egA.PCP)),
				Expected:   strconv.Itoa(int(egB.PCP)),
			}, true
		}
	}

	return analysis.Difference{}, false
}

func mirrorCopies(copies []device.Copy) []string {
	ordered := slices.Clone(copies)
	slices.SortFunc(ordered, func(a, b device.Copy) int {
		if order := cmp.Compare(a.Mirror, b.Mirror); order != 0 {
			return order
		}
		if order := cmp.Compare(a.Port, b.Port); order != 0 {
			return order
		}
		return cmp.Compare(a.VLAN, b.VLAN)
	})
	out := make([]string, len(ordered))
	for i, copy := range ordered {
		out[i] = fmt.Sprintf("mirror=%q;port=%q;vlan=%d;member=%q;src=%s;dst=%s;ether_type=%d;tags=%v;payload=%x",
			copy.Mirror, copy.Port, copy.VLAN, copy.Member, copy.Frame.Src, copy.Frame.Dst, copy.Frame.EtherType, copy.Frame.Tags, copy.Frame.Payload)
	}
	return out
}
