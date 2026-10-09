package cable

import (
	"fmt"
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// End holds one endpoint's Ethernet facts and administrative state.
// Label and Scope identify it in assumptions and observed-speed issues.
type End struct {
	Ethernet phy.Ethernet
	Admin    port.LinkState
	Label    string
	Scope    analysis.Scope
}

// EndState carries the facts resolution used and the result at one end.
// Speed.DuplexA is this end's duplex. Speed is nonzero only on an Up end.
type EndState struct {
	Oper     port.LinkState
	Reason   trace.Reason
	Speed    phy.Link
	Ethernet phy.Ethernet
}

// Resolution returns both ends and the physical facts used, including assumptions.
type Resolution struct {
	Cable      Cable
	A          EndState
	B          EndState
	Issues     []analysis.Issue
	Assumption *analysis.Assumption
}

// Resolve derives a cable's link in the order fault, administrative state,
// reach, negotiation, and the observed rule, after filling unreported facts from
// the configuration's physical assumption.
func (c Cable) Resolve(a, b End, assumed *PhyAssumption, key string, evidence []trace.EvidenceRef) Resolution {
	ethA, adminA := a.Ethernet, a.Admin
	ethB, adminB := b.Ethernet, b.Admin
	scope := analysis.LinkScope(key)
	var trust Resolution
	if assumed != nil {
		var filled []string
		if c.Medium == MediumUnspecified && assumed.Medium != MediumUnspecified {
			c.Medium = assumed.Medium
			filled = append(filled, "medium")
		}
		ethA = assumed.fill(ethA, a.Label, &filled)
		ethB = assumed.fill(ethB, b.Label, &filled)
		if len(filled) > 0 {
			trust.Assumption = &analysis.Assumption{
				Scope:     scope,
				Statement: "assumed physical facts: " + strings.Join(filled, ", "),
			}
		}
	}

	link := Resolution{Cable: c, A: EndState{Ethernet: ethA}, B: EndState{Ethernet: ethB}}
	unknown := func(reason trace.Reason, status analysis.Status, speed phy.Link) Resolution {
		link.setBoth(port.Unknown, reason, speed)
		trust.Issues = append(trust.Issues, analysis.Issue{
			Code:     analysis.IssueCode(reason),
			Status:   status,
			Scope:    scope,
			Message:  fmt.Sprintf("link %q is unknown: %s", key, reason),
			Evidence: evidence,
		})

		link.Issues, link.Assumption = trust.Issues, trust.Assumption
		return link
	}
	down := func(reason trace.Reason, speed phy.Link) Resolution {
		link.setBoth(port.Down, reason, speed)

		link.Issues, link.Assumption = trust.Issues, trust.Assumption
		return link
	}

	if c.Fault.Kind == FaultCut {
		return down(ReasonCut, phy.Link{})
	}

	// Two ends that both disable auto-negotiation still pass frames the dead
	// direction does not carry; an end whose mode is unreported might be either.
	if c.Fault.Kind == FaultDeadAToB || c.Fault.Kind == FaultDeadBToA {
		switch {
		case isAuto(ethA) || isAuto(ethB):
			return down(ReasonDeadDirection, phy.Link{})
		case !isForced(ethA) || !isForced(ethB):
			return unknown(phy.ReasonCapabilityUnknown, analysis.Incomplete, phy.Link{})
		}
	}

	if adminA == port.Down || adminB == port.Down {
		link.setBoth(port.Down, ReasonPeerDown, phy.Link{})
		if adminA == port.Down {
			link.A.Reason = ReasonAdminDown
		}
		if adminB == port.Down {
			link.B.Reason = ReasonAdminDown
		}

		link.Issues, link.Assumption = trust.Issues, trust.Assumption
		return link
	}

	if adminA == port.Unknown || adminB == port.Unknown {
		return unknown(ReasonAdminUnknown, analysis.Incomplete, phy.Link{})
	}

	for _, eth := range []phy.Ethernet{ethA, ethB} {
		if isForced(eth) && eth.Setting.SpeedBPS > 0 && c.reach(eth.Setting.SpeedBPS) == ReachExceeded {
			return down(ReasonReachExceeded, phy.Link{})
		}
	}

	candidates := negotiableSpeeds(ethA, ethB, c.TopSpeedBPS)
	remaining := slices.DeleteFunc(slices.Clone(candidates), func(speed uint64) bool {
		return c.reach(speed) == ReachExceeded
	})
	if len(candidates) > 0 && len(remaining) == 0 {
		return down(ReasonReachExceeded, phy.Link{})
	}
	top := c.TopSpeedBPS
	if len(remaining) < len(candidates) {
		top = slices.Max(remaining)
	}

	negotiated := phy.Negotiate(ethA, ethB, top)
	if negotiated.State == phy.LinkResolved && negotiated.Source != phy.SourceObserved &&
		slices.ContainsFunc(remaining, func(speed uint64) bool {
			return speed >= negotiated.SpeedBPS && c.reach(speed) == ReachUnknown
		}) {
		negotiated = observedLink(ethA, ethB, top)
		if negotiated.State != phy.LinkResolved && negotiated.State != phy.LinkFailed {
			return unknown(ReasonReachUnknown, analysis.Incomplete, phy.Link{State: phy.LinkUnknown, Reason: ReasonReachUnknown})
		}
	}

	switch negotiated.State {
	case phy.LinkResolved:
	case phy.LinkFailed:
		return down(negotiated.Reason, negotiated)
	case phy.LinkUnsupported:
		return unknown(negotiated.Reason, analysis.Unsupported, negotiated)
	default:
		return unknown(negotiated.Reason, analysis.Incomplete, negotiated)
	}

	link.setBoth(port.Up, "", negotiated)
	if c.Medium == MediumUnspecified && c.Delay == nil && c.LengthMeters > 0 {
		trust.Issues = append(trust.Issues, analysis.Issue{
			Code:     IssuePropagationUnknown,
			Status:   analysis.Incomplete,
			Scope:    scope,
			Message:  fmt.Sprintf("link %q has an unspecified medium and no delay", key),
			Evidence: evidence,
		})
	}
	if negotiated.Source == phy.SourceObserved {
		link.Issues, link.Assumption = trust.Issues, trust.Assumption
		return link
	}
	for i, end := range []EndState{link.A, link.B} {
		endpoint := []End{a, b}[i]
		observed := end.Ethernet.Observed
		if observed == nil || observed.SpeedBPS == 0 || observed.SpeedBPS == negotiated.SpeedBPS {
			continue
		}
		trust.Issues = append(trust.Issues, analysis.Issue{
			Code:   IssueObservedSpeedConflict,
			Status: analysis.Incomplete,
			Scope:  endpoint.Scope,
			Message: fmt.Sprintf("%s observed %d b/s but its link negotiated %d b/s",
				endpoint.Label, observed.SpeedBPS, negotiated.SpeedBPS),
			Evidence: evidence,
		})
	}

	link.Issues, link.Assumption = trust.Issues, trust.Assumption
	return link
}

// setBoth gives both ends a state and reason, with speed as end A sees it.
func (l *Resolution) setBoth(oper port.LinkState, reason trace.Reason, speed phy.Link) {
	l.A.Oper, l.A.Reason, l.A.Speed = oper, reason, speed
	l.B.Oper, l.B.Reason, l.B.Speed = oper, reason, speed
	l.B.Speed.DuplexA, l.B.Speed.DuplexB = speed.DuplexB, speed.DuplexA
}

// negotiableSpeeds returns the speeds negotiation could select at or below top
// (0 is unlimited): the forced speeds when an end forces one, otherwise the
// speeds both ends report.
func negotiableSpeeds(a, b phy.Ethernet, top uint64) []uint64 {
	var speeds []uint64
	for _, eth := range []phy.Ethernet{a, b} {
		if isForced(eth) && eth.Setting.SpeedBPS > 0 {
			speeds = append(speeds, eth.Setting.SpeedBPS)
		}
	}
	if len(speeds) == 0 {
		for _, speed := range a.SupportedSpeedsBPS {
			if slices.Contains(b.SupportedSpeedsBPS, speed) {
				speeds = append(speeds, speed)
			}
		}
	}

	return slices.DeleteFunc(speeds, func(speed uint64) bool {
		return top != 0 && speed > top
	})
}

// observedLink applies phy's observed rule on its own. phy owns the rule and
// reaches it only through ends whose mode is unreported, so the settings are
// dropped here.
func observedLink(a, b phy.Ethernet, top uint64) phy.Link {
	a.Setting, b.Setting = nil, nil

	return phy.Negotiate(a, b, top)
}

func (c Cable) reach(speedBPS uint64) ReachState {
	state, _ := c.Medium.Reach(c.LengthMeters, speedBPS)

	return state
}

func isForced(e phy.Ethernet) bool {
	return e.Setting != nil && !e.Setting.AutoNegotiation
}

func isAuto(e phy.Ethernet) bool {
	return e.Setting != nil && e.Setting.AutoNegotiation
}
