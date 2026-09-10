package phy

import (
	"slices"
)

// Duplex is the duplex mode of an Ethernet link.
type Duplex string

const (
	// Full is simultaneous transmission in both directions.
	Full Duplex = "Full"

	// Half is transmission in one direction at a time.
	Half Duplex = "Half"

	// Unknown is an unreported duplex mode.
	Unknown Duplex = "Unknown"
)

// Setting is the requested link configuration of one port. With
// AutoNegotiation on, the port negotiates over its supported speeds; with it
// off, SpeedBPS and Duplex are the requested fixed link.
type Setting struct {
	SpeedBPS        uint64
	Duplex          Duplex
	AutoNegotiation bool
}

// Observed is the active speed and duplex a source reported for a link.
type Observed struct {
	SpeedBPS uint64
	Duplex   Duplex
}

// Ethernet describes one port's physical link: the speeds it supports,
// whether it can auto-negotiate, the requested setting, and the observed
// active speed and duplex when the source reported them.
type Ethernet struct {
	SupportedSpeedsBPS       []uint64
	AutoNegotiationSupported bool
	Setting                  *Setting
	Observed                 *Observed
}

// Source identifies which fact resolved a port's active speed.
type Source string

const (
	// SourceObserved resolves from the reported active speed.
	SourceObserved Source = "observed"

	// SourceSetting resolves from the configured speed with auto-negotiation off.
	SourceSetting Source = "setting"

	// SourceNegotiated resolves to the highest supported speed at full duplex
	// with auto-negotiation on.
	SourceNegotiated Source = "negotiated"

	// SourceUnresolved reports that neither an observation nor a usable
	// setting exists; a link-down port has neither, and that is not an error.
	SourceUnresolved Source = "unresolved"
)

// Resolved is a port's active speed and duplex with the fact that resolved
// them. An unresolved port has zero SpeedBPS.
type Resolved struct {
	SpeedBPS uint64
	Duplex   Duplex
	Source   Source
}

// Resolve computes the port's active speed and duplex. An observed active
// speed overrides any setting, since the source's report is the link as it
// runs, not as it was asked to run.
func (e Ethernet) Resolve() Resolved {
	if e.Observed != nil {
		return Resolved{SpeedBPS: e.Observed.SpeedBPS, Duplex: e.Observed.Duplex, Source: SourceObserved}
	}
	if e.Setting == nil {
		return Resolved{Source: SourceUnresolved}
	}
	if e.Setting.AutoNegotiation {
		if len(e.SupportedSpeedsBPS) > 0 {
			return Resolved{SpeedBPS: slices.Max(e.SupportedSpeedsBPS), Duplex: Full, Source: SourceNegotiated}
		}

		return Resolved{Source: SourceUnresolved}
	}
	if e.Setting.SpeedBPS == 0 {
		return Resolved{Source: SourceUnresolved}
	}

	return Resolved{SpeedBPS: e.Setting.SpeedBPS, Duplex: e.Setting.Duplex, Source: SourceSetting}
}

// Resolve computes every port's active speed and duplex. It returns nil when
// the Ethernet capability is absent.
func (c Config) Resolve() map[string]Resolved {
	if c.Ethernet == nil {
		return nil
	}
	resolved := make(map[string]Resolved, len(c.Ethernet))
	for name, e := range c.Ethernet {
		resolved[name] = e.Resolve()
	}

	return resolved
}
