package cable

import (
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

const (
	// ReasonCut records that a link cannot operate because its physical cable is severed.
	ReasonCut trace.Reason = "cut"
	// ReasonPeerDown records that a link cannot operate because the peer switch port is administratively disabled.
	ReasonPeerDown trace.Reason = "peer-down"
	// ReasonAdminDown records that a link cannot operate because this end's own port is administratively
	// disabled; the other end reads peer-down, so a reader is pointed at the disabled device.
	ReasonAdminDown trace.Reason = "admin-down"
	// ReasonReachUnknown records that a link is Unknown because the medium's reach is unknown at the speed
	// negotiation would select or at a higher candidate speed.
	ReasonReachUnknown trace.Reason = "reach-unknown"
	// ReasonDeadDirection records that two-ended auto-negotiation cannot succeed because the cable is impaired in one direction.
	ReasonDeadDirection trace.Reason = "dead-direction"
	// ReasonCableLoss records that a transmitted frame was lost during cable propagation due to a configured fault.
	ReasonCableLoss trace.Reason = "cable-loss"
	// ReasonBadFrame records that an arriving frame was dropped because it was corrupted during transmission.
	ReasonBadFrame trace.Reason = "bad-frame"
	// ReasonReachExceeded records that a link cannot operate because the cable length exceeds the medium reach for every candidate speed.
	ReasonReachExceeded trace.Reason = "reach-exceeded"
	// ReasonAdminUnknown leaves both ends unknown when either administrative state is unreported.
	ReasonAdminUnknown trace.Reason = "unknown-operational-status"
)

const (
	// IssueObservedSpeedConflict marks a link end whose observed speed differs from the speed its link
	// resolved to by negotiation.
	IssueObservedSpeedConflict analysis.IssueCode = "observed-speed-conflict"
	// IssuePropagationUnknown marks an operational link whose medium is unspecified and which has no
	// Delay, so the propagation time of every frame crossing it is unknown and taken as zero.
	IssuePropagationUnknown analysis.IssueCode = "propagation-unknown"
)
