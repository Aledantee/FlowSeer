package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

type timed interface {
	Advance(time.Time) layer.Effects
	NextWake() (time.Time, bool)
}

type layerEntry struct {
	advance func(time.Time) layer.Effects
	timer   timed
	apply   func(layer.Effects)
	age     bool
}

func (s *Switch) layers(now time.Time) []layerEntry {
	var entries []layerEntry
	if s.stp != nil {
		entries = append(entries, layerEntry{advance: s.stp.Advance, timer: s.stp, apply: s.applySTPEffects})
	}
	if s.lag != nil {
		entries = append(entries, layerEntry{advance: s.lag.Advance, timer: s.lag, apply: func(fx layer.Effects) { s.applyLAGEffects(now, fx) }})
	}
	if s.loopprotect != nil {
		entries = append(entries, layerEntry{advance: s.loopprotect.Advance, timer: s.loopprotect, apply: s.applyLoopProtectEffects})
	}
	if s.bridge != nil {
		entries = append(entries, layerEntry{advance: s.bridge.Advance, age: true})
	}
	if s.mcast != nil {
		entries = append(entries, layerEntry{advance: s.mcast.Advance, age: true})
	}
	if s.routing != nil {
		entries = append(entries, layerEntry{advance: s.routing.Advance, timer: s.routing, age: true, apply: func(fx layer.Effects) {
			s.emitRouting(now, fx.Emissions)
			s.applyRoutingExits(now, s.routing.DrainExits())
		}})
	}
	return entries
}

func (s *Switch) advance(now time.Time) {
	for _, entry := range s.layers(now) {
		fx := entry.advance(now)
		if entry.apply != nil {
			entry.apply(fx)
		}
	}
}
