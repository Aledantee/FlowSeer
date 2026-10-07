package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func (s *Switch) aggregatorScope(name string) analysis.Scope {
	return analysis.FieldScope(protocolScope(s.nodeID, lag.LayerName), "aggregators", name)
}

// SelectMember commits an enabled member choice for the named LAG to carry f
// at vid, recording the choice as the layer's new bucket assignment or
// last-active member: a fabric transmission or another real transmission the
// switch makes outside the bridge pipeline. It returns false if the LAG is
// unknown, has no layer, or has no enabled member.
func (s *Switch) SelectMember(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID) (string, bool) {
	sel := s.selectOrPeekMember(now, lagName, f, vid, true)

	return sel.Member, sel.OK
}

// selectOrPeekMember chooses a member of the named LAG to carry f at vid,
// committing the choice when commit is true, and records the selection for
// the lag-rebalance-unmodeled issue when it reports that condition.
func (s *Switch) selectOrPeekMember(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID, commit bool) lag.Selection {
	if s.lag == nil {
		return lag.Selection{}
	}

	var sel lag.Selection
	if commit {
		sel = s.lag.Select(now, lagName, f, vid)
	} else {
		sel = s.lag.Peek(now, lagName, f, vid)
	}
	if sel.OK {
		s.recordLAGSelection(lagName, sel)
	}

	return sel
}

// selectMemberWithFact behaves as selectOrPeekMember, additionally returning
// the semantic fact describing the selection for a caller building its own
// trace step, such as hub flooding or routed LAG egress.
func (s *Switch) selectMemberWithFact(now time.Time, lagName string, f ethernet.Frame, vid vlan.ID, commit bool) (lag.Selection, trace.Fact) {
	sel := s.selectOrPeekMember(now, lagName, f, vid, commit)
	if s.lag == nil {
		return sel, nil
	}

	return sel, s.lag.SelectionFact(lagName, f, vid, sel)
}

// lagSelector adapts a switch's LAG layer to [bridge.Selector] and
// [bridge.semanticSelector], recording each selection so forward metadata can
// raise the rebalance-unmodeled issue for the LAGs a journey actually used.
type lagSelector struct {
	sw *Switch
}

func (a lagSelector) Select(now time.Time, lagName string, commit bool, f ethernet.Frame, vid vlan.ID) bridge.Selection {
	sel := a.sw.selectOrPeekMember(now, lagName, f, vid, commit)

	return bridge.Selection{
		Member:             sel.Member,
		OK:                 sel.OK,
		Bucket:             sel.Bucket,
		Prior:              sel.Prior,
		Cause:              string(sel.Cause),
		RebalanceUnmodeled: sel.RebalanceUnmodeled,
	}
}

func (a lagSelector) SelectionFact(lagName string, f ethernet.Frame, vid vlan.ID, sel bridge.Selection) trace.Fact {
	return a.sw.lag.SelectionFact(lagName, f, vid, lag.Selection{
		Member:             sel.Member,
		OK:                 sel.OK,
		Bucket:             sel.Bucket,
		Prior:              sel.Prior,
		Cause:              lag.SelectionCause(sel.Cause),
		RebalanceUnmodeled: sel.RebalanceUnmodeled,
	})
}
