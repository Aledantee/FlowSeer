package vswitch

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
)

// Comparison holds the forwarding results from evaluating the same frame arrival
// on two switches, and reports whether their observable forwarding behaviors match.
type Comparison struct {
	Current  ForwardResult
	Expected ForwardResult
	Same     bool
}

// Compare evaluates a frame arrival on both switches using [Switch.Peek] without mutating
// their forwarding tables, returning both results and reporting whether they produced
// the same outcome, reason, classified filtering database ID, and set of egress
// port, drop reason, and tag stack triples.
func Compare(a, b *Switch, now time.Time, port string, f ethernet.Frame) Comparison {
	cur := a.Peek(now, port, f)
	exp := b.Peek(now, port, f)

	same := cur.Outcome == exp.Outcome &&
		cur.Reason == exp.Reason &&
		cur.FID == exp.FID &&
		sameEgressTriples(cur.Egress, exp.Egress)

	return Comparison{
		Current:  cur,
		Expected: exp,
		Same:     same,
	}
}

func sameEgressTriples(a, b []bridge.Egress) bool {
	if len(a) != len(b) {
		return false
	}
	matched := make([]bool, len(b))
	for _, egA := range a {
		found := false
		for j, egB := range b {
			if !matched[j] &&
				egA.Port == egB.Port &&
				egA.Dropped == egB.Dropped &&
				slices.Equal(egA.Frame.Tags, egB.Frame.Tags) {
				matched[j] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
