package phy

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

// Negotiation failure reasons recorded by [Negotiate].
const (
	// ReasonSpeedMismatch records that two ends could not agree on an
	// operational speed, or that a forced speed exceeds what the cable or
	// peer allows.
	ReasonSpeedMismatch trace.Reason = "speed-mismatch"

	// ReasonCapabilityUnknown records that physical capabilities or speeds are unreported.
	ReasonCapabilityUnknown trace.Reason = "capability-unknown"

	// ReasonDuplexMismatch records that both ends stated conflicting duplex modes.
	ReasonDuplexMismatch trace.Reason = "duplex-mismatch"

	// ReasonForcedAgainstAutoUnmodeled records that forced operation above 100 Mb/s
	// against an auto-negotiating peer is unmodeled.
	ReasonForcedAgainstAutoUnmodeled trace.Reason = "forced-against-auto-unmodeled"
)

// LinkState represents the physical negotiation outcome state.
type LinkState string

const (
	// LinkUnknown indicates negotiation outcome is uncertain due to unreported facts. It is the zero value.
	LinkUnknown LinkState = ""

	// LinkResolved indicates negotiation completed successfully.
	LinkResolved LinkState = "Resolved"

	// LinkFailed indicates negotiation failed due to speed or capability incompatibility.
	LinkFailed LinkState = "Failed"

	// LinkUnsupported indicates the combination of modes is not modeled.
	LinkUnsupported LinkState = "Unsupported"
)

// TypeID returns the stable identifier for LinkState facts.
func (s LinkState) TypeID() string {
	return "phy.link_state"
}

// Canonical returns the string representation of the LinkState.
func (s LinkState) Canonical() string {
	if s == "" {
		return "Unknown"
	}

	return string(s)
}

// String returns the string representation of the LinkState.
func (s LinkState) String() string {
	return s.Canonical()
}

// Link is the outcome of speed negotiation across a cable.
type Link struct {
	State    LinkState
	SpeedBPS uint64
	DuplexA  Duplex
	DuplexB  Duplex
	Source   Source
	Reason   trace.Reason
}

// Negotiate computes the active link between two Ethernet endpoints across a
// cable with a maximum speed top, where a top of 0 is unlimited.
//
// Negotiation outcome follows the physical negotiation truth table:
// - A nil Setting or a forced Setting with speed 0 is unreported, yielding
//   [LinkUnknown] with [ReasonCapabilityUnknown].
// - Two auto-negotiating ends with known supported speeds select the highest
//   common supported speed up to top at [Full] duplex on both ends. If no
//   common speed exists, negotiation fails with [LinkFailed] and [ReasonSpeedMismatch].
// - Two auto-negotiating ends where either end lacks reported supported speeds
//   yield [LinkUnknown] with [ReasonCapabilityUnknown].
// - Two forced ends agree on speed if both speeds are identical and at or below top.
//   If both ends state different duplexes, [ReasonDuplexMismatch] is set. If speeds
//   differ or exceed top, negotiation fails with [LinkFailed] and [ReasonSpeedMismatch].
// - A forced end at or below 100 Mb/s against an auto-negotiating end with known
//   speeds resolves at the forced speed if supported by the auto end and within top.
//   The forced end keeps its configured duplex, while the auto end resolves to [Half]
//   via parallel detection. If the forced end configured [Full] duplex,
//   [ReasonDuplexMismatch] is set.
// - A forced end at or below 100 Mb/s against an auto-negotiating end with unreported
//   speeds yields [LinkUnknown] with [ReasonCapabilityUnknown].
// - A forced end above 100 Mb/s against an auto-negotiating end yields [LinkUnsupported]
//   with [ReasonForcedAgainstAutoUnmodeled].
// - When the truth table yields [LinkUnknown], the observed rule resolves the link
//   to [LinkResolved] with [SourceObserved] if both ends report identical non-zero
//   observed speeds.
func Negotiate(a, b Ethernet, top uint64) Link {
	if a.Setting != nil && a.Setting.AutoNegotiation && a.AutoNegotiationSupported == CapabilityUnsupported {
		return Link{State: LinkFailed, Reason: ReasonSpeedMismatch}
	}
	if b.Setting != nil && b.Setting.AutoNegotiation && b.AutoNegotiationSupported == CapabilityUnsupported {
		return Link{State: LinkFailed, Reason: ReasonSpeedMismatch}
	}

	isUnreported := func(e Ethernet) bool {
		return e.Setting == nil || (!e.Setting.AutoNegotiation && e.Setting.SpeedBPS == 0)
	}

	if isUnreported(a) || isUnreported(b) {
		if link, ok := checkObserved(a, b); ok {
			return link
		}

		return Link{State: LinkUnknown, Reason: ReasonCapabilityUnknown}
	}

	aAuto := a.Setting.AutoNegotiation
	bAuto := b.Setting.AutoNegotiation

	if aAuto && bAuto {
		if len(a.SupportedSpeedsBPS) == 0 || len(b.SupportedSpeedsBPS) == 0 {
			if link, ok := checkObserved(a, b); ok {
				return link
			}

			return Link{State: LinkUnknown, Reason: ReasonCapabilityUnknown}
		}

		var best uint64
		for _, s := range a.SupportedSpeedsBPS {
			if slices.Contains(b.SupportedSpeedsBPS, s) {
				if top == 0 || s <= top {
					if s > best {
						best = s
					}
				}
			}
		}
		if best == 0 {
			return Link{State: LinkFailed, Reason: ReasonSpeedMismatch}
		}

		return Link{State: LinkResolved, SpeedBPS: best, DuplexA: Full, DuplexB: Full, Source: SourceNegotiated}
	}

	if !aAuto && !bAuto {
		sa := a.Setting.SpeedBPS
		sb := b.Setting.SpeedBPS
		if sa != sb {
			return Link{State: LinkFailed, Reason: ReasonSpeedMismatch}
		}
		if top != 0 && sa > top {
			return Link{State: LinkFailed, Reason: ReasonSpeedMismatch}
		}

		stated := func(d Duplex) bool { return d != "" && d != Unknown }
		var reason trace.Reason
		if stated(a.Setting.Duplex) && stated(b.Setting.Duplex) && a.Setting.Duplex != b.Setting.Duplex {
			reason = ReasonDuplexMismatch
		}

		return Link{
			State:    LinkResolved,
			SpeedBPS: sa,
			DuplexA:  a.Setting.Duplex,
			DuplexB:  b.Setting.Duplex,
			Source:   SourceNegotiated,
			Reason:   reason,
		}
	}

	aIsForced := !aAuto
	forced := a
	auto := b
	if !aIsForced {
		forced = b
		auto = a
	}

	s := forced.Setting.SpeedBPS
	if s > 100_000_000 {
		return Link{State: LinkUnsupported, Reason: ReasonForcedAgainstAutoUnmodeled}
	}

	if len(auto.SupportedSpeedsBPS) == 0 {
		if link, ok := checkObserved(a, b); ok {
			return link
		}

		return Link{State: LinkUnknown, Reason: ReasonCapabilityUnknown}
	}

	if !slices.Contains(auto.SupportedSpeedsBPS, s) || (top != 0 && s > top) {
		return Link{State: LinkFailed, Reason: ReasonSpeedMismatch}
	}

	var duplexA, duplexB Duplex
	var reason trace.Reason
	if aIsForced {
		duplexA = forced.Setting.Duplex
		duplexB = Half
		if forced.Setting.Duplex == Full {
			reason = ReasonDuplexMismatch
		}
	} else {
		duplexA = Half
		duplexB = forced.Setting.Duplex
		if forced.Setting.Duplex == Full {
			reason = ReasonDuplexMismatch
		}
	}

	return Link{
		State:    LinkResolved,
		SpeedBPS: s,
		DuplexA:  duplexA,
		DuplexB:  duplexB,
		Source:   SourceNegotiated,
		Reason:   reason,
	}
}

func checkObserved(a, b Ethernet) (Link, bool) {
	if a.Observed != nil && b.Observed != nil &&
		a.Observed.SpeedBPS > 0 && a.Observed.SpeedBPS == b.Observed.SpeedBPS {
		return Link{
			State:    LinkResolved,
			SpeedBPS: a.Observed.SpeedBPS,
			DuplexA:  a.Observed.Duplex,
			DuplexB:  b.Observed.Duplex,
			Source:   SourceObserved,
		}, true
	}

	return Link{}, false
}
