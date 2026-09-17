package interfaces

import (
	"time"

	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/access/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodeRoute identifies a route-selection failure: neither the primary
// route nor its fallback produced a complete observation.
var ErrCodeRoute = errs.NewCode("interfaces/route")

// Route names which protocol answered a [SelectRoute] call.
type Route int

const (
	// RouteUnspecified means no route answered.
	RouteUnspecified Route = iota
	// RouteSNMP means the primary, SNMP-sourced observation was complete.
	RouteSNMP
	// RouteSSH means the fallback route answered because the primary was
	// not complete.
	RouteSSH
)

// String returns the route's name for logging and diagnostics.
func (r Route) String() string {
	switch r {
	case RouteSNMP:
		return "snmp"
	case RouteSSH:
		return "ssh"
	default:
		return "unspecified"
	}
}

// Freshness bounds how old an observation may be before it can no longer
// stand in for a read a verification requires to be fresh. It is an
// edge-local route-selection input, not a wire value: the verification rule
// requires "a fresh session", and Freshness is how a caller states what
// "fresh" means for its own read.
type Freshness struct {
	// ObservedAt is when the observation was read.
	ObservedAt time.Time
	// MaxAge is how long the observation may stand in for a fresh read.
	MaxAge time.Duration
}

// Stale reports whether the observation is too old, as of now, to stand in
// for a fresh read.
func (f Freshness) Stale(now time.Time) bool {
	return now.Sub(f.ObservedAt) > f.MaxAge
}

// DelayedEffect bounds how long a route's read may still show a device's
// pre-mutation state after the command that changed it returns — the
// declared delayed-apply horizon. It is
// edge-local, not a wire value: nothing in the schema's package list
// carries it, because the horizon is an input to routing and recovery, not
// a fact the device or the read reports.
type DelayedEffect struct {
	// Horizon is the longest a mutation may take to become visible to a
	// fresh read before an unmatched read counts as failed rather than
	// merely not yet applied.
	Horizon time.Duration
}

// WithinHorizon reports whether now still falls inside the horizon that
// began at since — the window in which an unmatched verification read is
// "not yet," not "failed."
func (d DelayedEffect) WithinHorizon(since, now time.Time) bool {
	return now.Sub(since) <= d.Horizon
}

// SelectRoute implements the route-fallback rule: SNMP is the
// cold-start prior and the tie-breaker, and a complete SNMP observation
// needs no second read. When primary is nil or not
// [accessv1.Completeness_COMPLETENESS_COMPLETE], fallback is called
// exactly once; its result is returned regardless of its own completeness,
// so the caller sees whatever the fallback route could produce. An error
// is returned only when there is no way to answer at all: primary is
// incomplete and fallback is nil, or fallback itself errors.
func SelectRoute(
	primary *accessv1.InterfaceObservation,
	fallback func() (*accessv1.InterfaceObservation, error),
) (*accessv1.InterfaceObservation, Route, error) {
	if primary.GetCompleteness() == accessv1.Completeness_COMPLETENESS_COMPLETE {
		return primary, RouteSNMP, nil
	}

	if fallback == nil {
		return nil, RouteUnspecified, errs.New().Code(ErrCodeRoute).
			Msg("primary observation is incomplete and no fallback route was given")
	}

	obs, err := fallback()
	if err != nil {
		return nil, RouteUnspecified, errs.Wrap(err, "fallback route")
	}

	return obs, RouteSSH, nil
}

// ConflictingReads compares two complete observations of the same
// interface field by field and reports the first field, if any, where
// they disagree. Either observation being anything but
// [accessv1.Completeness_COMPLETENESS_COMPLETE] is not a conflict: a
// partial observation is evidence for routing only, never something to
// compare (the direction record's device/access/v1/README.md states this
// directly), so this function reports no conflict rather than a caller
// having to filter partial inputs itself. Two observations naming
// different interfaces are never compared either: they have nothing to
// conflict about.
func ConflictingReads(a, b *accessv1.InterfaceObservation) (conflict bool, field string) {
	if a.GetInterfaceName() != b.GetInterfaceName() {
		return false, ""
	}

	if a.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE ||
		b.GetCompleteness() != accessv1.Completeness_COMPLETENESS_COMPLETE {
		return false, ""
	}

	switch {
	case a.GetDescription() != b.GetDescription():
		return true, "description"
	case a.GetAdminStatus() != b.GetAdminStatus():
		return true, "admin_status"
	case a.GetOperStatus() != b.GetOperStatus():
		return true, "oper_status"
	default:
		return false, ""
	}
}

// DescriptionApplied reports whether observation's description matches
// intent's — the comparison [VerifyDescriptionChange] makes after a
// mutation, per the verification rule.
func DescriptionApplied(observation *accessv1.InterfaceObservation, intent *accessv1.InterfaceDescriptionChange) bool {
	return observation.GetDescription() == intent.GetDescription()
}
