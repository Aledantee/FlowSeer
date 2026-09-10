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
)

// Link is the outcome of speed negotiation across a cable. When negotiation
// fails, SpeedBPS is zero and Reason records the failure.
type Link struct {
	SpeedBPS uint64
	Duplex   Duplex
	Reason   trace.Reason
}

// Negotiate computes the active link between two Ethernet endpoints across a
// cable with a maximum speed top, where a top of 0 is unlimited.
//
// An end is auto when its Setting is nil or Setting.AutoNegotiation is true.
// The failure for unsupported auto-negotiation applies only to an explicit
// Setting with AutoNegotiation true on an end with AutoNegotiationSupported
// false. An end with AutoNegotiationSupported false and a nil Setting remains
// an auto end over every speed when SupportedSpeedsBPS is empty, representing a
// host or a port lacking the Ethernet capability.
//
// An end is forced at Setting.SpeedBPS when Setting.AutoNegotiation is false; a
// forced speed of 0 fails.
//
// Two auto ends take the highest speed both support and the cable allows, at
// [Full] duplex. Two all-speeds auto ends take top when top is non-zero, or
// 1_000_000_000 bps when top is 0. A forced end against an auto end takes the
// forced speed when the auto end and the cable allow it, at the forced end's
// Setting.Duplex. Two forced ends must agree and fit the cable.
//
// When negotiation fails, Negotiate returns a [Link] with zero SpeedBPS and
// [ReasonSpeedMismatch].
func Negotiate(a, b Ethernet, top uint64) Link {
	if a.Setting != nil && a.Setting.AutoNegotiation && !a.AutoNegotiationSupported {
		return Link{Reason: ReasonSpeedMismatch}
	}
	if a.Setting != nil && !a.Setting.AutoNegotiation && a.Setting.SpeedBPS == 0 {
		return Link{Reason: ReasonSpeedMismatch}
	}
	if b.Setting != nil && b.Setting.AutoNegotiation && !b.AutoNegotiationSupported {
		return Link{Reason: ReasonSpeedMismatch}
	}
	if b.Setting != nil && !b.Setting.AutoNegotiation && b.Setting.SpeedBPS == 0 {
		return Link{Reason: ReasonSpeedMismatch}
	}

	aAuto := a.Setting == nil || a.Setting.AutoNegotiation
	bAuto := b.Setting == nil || b.Setting.AutoNegotiation

	if aAuto && bAuto {
		if len(a.SupportedSpeedsBPS) == 0 && len(b.SupportedSpeedsBPS) == 0 {
			speed := uint64(1_000_000_000)
			if top != 0 {
				speed = top
			}

			return Link{SpeedBPS: speed, Duplex: Full}
		}

		candidates := a.SupportedSpeedsBPS
		peer := b.SupportedSpeedsBPS
		if len(candidates) == 0 {
			candidates = b.SupportedSpeedsBPS
			peer = a.SupportedSpeedsBPS
		}

		var best uint64
		for _, s := range candidates {
			if len(peer) > 0 && !slices.Contains(peer, s) {
				continue
			}
			if top != 0 && s > top {
				continue
			}
			if s > best {
				best = s
			}
		}
		if best == 0 {
			return Link{Reason: ReasonSpeedMismatch}
		}

		return Link{SpeedBPS: best, Duplex: Full}
	}

	if !aAuto && !bAuto {
		if a.Setting.SpeedBPS != b.Setting.SpeedBPS {
			return Link{Reason: ReasonSpeedMismatch}
		}
		if top != 0 && a.Setting.SpeedBPS > top {
			return Link{Reason: ReasonSpeedMismatch}
		}
		if a.Setting.Duplex != "" && b.Setting.Duplex != "" && a.Setting.Duplex != b.Setting.Duplex {
			return Link{Reason: ReasonSpeedMismatch}
		}

		duplex := a.Setting.Duplex
		if duplex == "" {
			duplex = b.Setting.Duplex
		}

		return Link{SpeedBPS: a.Setting.SpeedBPS, Duplex: duplex}
	}

	forced := a
	auto := b
	if aAuto {
		forced = b
		auto = a
	}

	s := forced.Setting.SpeedBPS
	if top != 0 && s > top {
		return Link{Reason: ReasonSpeedMismatch}
	}
	if len(auto.SupportedSpeedsBPS) > 0 && !slices.Contains(auto.SupportedSpeedsBPS, s) {
		return Link{Reason: ReasonSpeedMismatch}
	}

	return Link{SpeedBPS: s, Duplex: forced.Setting.Duplex}
}
