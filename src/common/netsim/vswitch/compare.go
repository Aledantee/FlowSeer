package vswitch

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// Difference describes the first behavioral observable that differed between two
// evaluations, naming the observable and the values observed on both sides.
type Difference struct {
	Observable string
	Current    string
	Expected   string
}

// String returns a human-readable representation of the difference, or empty if none.
func (d Difference) String() string {
	if d.Observable == "" {
		return ""
	}
	return d.Observable + ": current=" + d.Current + ", expected=" + d.Expected
}

// Comparison holds the forwarding results from evaluating the same frame arrival
// on two switches, and reports the derived comparison disposition and the first
// differing behavioral observable. Same is true when observable forwarding behaviors match.
type Comparison struct {
	Current     ForwardResult
	Expected    ForwardResult
	Disposition analysis.Disposition
	Difference  Difference
	Same        bool
}

// CompareResults compares two [ForwardResult] values directly and reports their exact
// behavioral disposition ([analysis.Equivalent], [analysis.Different], or [analysis.Inconclusive]).
func CompareResults(cur, exp ForwardResult) Comparison {
	diff, hasDiff := diffForwardResult(cur, exp)
	var disp analysis.Disposition
	switch {
	case hasDiff:
		disp = analysis.Different
	case cur.Metadata.Status() != analysis.Complete || exp.Metadata.Status() != analysis.Complete:
		disp = analysis.Inconclusive
	default:
		disp = analysis.Equivalent
	}

	return Comparison{
		Current:     cur,
		Expected:    exp,
		Disposition: disp,
		Difference:  diff,
		Same:        !hasDiff,
	}
}

// Compare evaluates a frame arrival on both switches using [Switch.Peek] without mutating
// their forwarding tables, returning both results and reporting their exact behavioral
// disposition ([analysis.Equivalent], [analysis.Different], or [analysis.Inconclusive]).
// On [analysis.Different], Difference names the first differing observable in declared order:
// typed forwarding outcome and reason, classified FID, egress ports and per-port drops,
// rewritten frame fields (dst, src, ethertype, tags, payload), selected LAG member,
// egress PCP, and mirror copies. Semantic traces and metadata are diagnostic and never
// make a Different.
func Compare(a, b *Switch, now time.Time, port string, f ethernet.Frame) Comparison {
	return CompareResults(a.Peek(now, port, f), b.Peek(now, port, f))
}

func diffForwardResult(cur, exp ForwardResult) (Difference, bool) {
	if cur.Outcome != exp.Outcome {
		return Difference{
			Observable: "outcome",
			Current:    string(cur.Outcome),
			Expected:   string(exp.Outcome),
		}, true
	}
	if cur.Reason != exp.Reason {
		return Difference{
			Observable: "reason",
			Current:    string(cur.Reason),
			Expected:   string(exp.Reason),
		}, true
	}
	if cur.FID != exp.FID {
		return Difference{
			Observable: "fid",
			Current:    strconv.Itoa(int(cur.FID)),
			Expected:   strconv.Itoa(int(exp.FID)),
		}, true
	}

	curByPort := make(map[string]bridge.Egress, len(cur.Egress))
	for _, eg := range cur.Egress {
		curByPort[eg.Port] = eg
	}
	expByPort := make(map[string]bridge.Egress, len(exp.Egress))
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
			return Difference{
				Observable: "egress.port",
				Current:    "<absent>",
				Expected:   p,
			}, true
		}
		if !okB {
			return Difference{
				Observable: "egress.port",
				Current:    p,
				Expected:   "<absent>",
			}, true
		}
		if egA.Dropped != egB.Dropped {
			return Difference{
				Observable: "egress.dropped",
				Current:    string(egA.Dropped),
				Expected:   string(egB.Dropped),
			}, true
		}
		if egA.Frame.Dst != egB.Frame.Dst {
			return Difference{
				Observable: "frame.dst",
				Current:    egA.Frame.Dst.String(),
				Expected:   egB.Frame.Dst.String(),
			}, true
		}
		if egA.Frame.Src != egB.Frame.Src {
			return Difference{
				Observable: "frame.src",
				Current:    egA.Frame.Src.String(),
				Expected:   egB.Frame.Src.String(),
			}, true
		}
		if egA.Frame.EtherType != egB.Frame.EtherType {
			return Difference{
				Observable: "frame.ethertype",
				Current:    egA.Frame.EtherType.String(),
				Expected:   egB.Frame.EtherType.String(),
			}, true
		}
		if !slices.Equal(egA.Frame.Tags, egB.Frame.Tags) {
			return Difference{
				Observable: "frame.tags",
				Current:    fmt.Sprint(egA.Frame.Tags),
				Expected:   fmt.Sprint(egB.Frame.Tags),
			}, true
		}
		if !bytes.Equal(egA.Frame.Payload, egB.Frame.Payload) {
			return Difference{
				Observable: "frame.payload",
				Current:    fmt.Sprintf("%x", egA.Frame.Payload),
				Expected:   fmt.Sprintf("%x", egB.Frame.Payload),
			}, true
		}
		if egA.Member != egB.Member {
			return Difference{
				Observable: "lag.member",
				Current:    egA.Member,
				Expected:   egB.Member,
			}, true
		}
		if egA.PCP != egB.PCP {
			return Difference{
				Observable: "pcp",
				Current:    strconv.Itoa(int(egA.PCP)),
				Expected:   strconv.Itoa(int(egB.PCP)),
			}, true
		}
	}

	mirrorsA := mirrorCopies(cur)
	mirrorsB := mirrorCopies(exp)
	if !slices.Equal(mirrorsA, mirrorsB) {
		return Difference{
			Observable: "mirror",
			Current:    strings.Join(mirrorsA, ","),
			Expected:   strings.Join(mirrorsB, ","),
		}, true
	}

	return Difference{}, false
}

func mirrorCopies(res ForwardResult) []string {
	var out []string
	for _, step := range res.Steps {
		if step.RuleID == traffic.RuleMirrorCopy {
			desc := step.Subject.Key
			for _, fact := range step.Outputs {
				if fact.TypeID() == "traffic.mirror_decision" {
					desc = fact.Canonical()
					break
				}
			}
			out = append(out, desc)
		}
	}
	slices.Sort(out)
	return out
}
