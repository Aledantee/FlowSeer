package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
)

type timed interface {
	Advance(time.Time) layer.Effects
	NextWake() (time.Time, bool)
}

type layerEntry struct {
	advance func(*Switch, time.Time) layer.Effects
	timer   timed
	apply   func(*Switch, time.Time, layer.Effects)
	age     bool
	clone   func(*Switch, *Switch)
	bind    func(*Switch)
}

func (s *Switch) layers(_ time.Time) [8]layerEntry {
	var entries [8]layerEntry
	if s.stp != nil {
		entries[0] = layerEntry{
			advance: func(s *Switch, now time.Time) layer.Effects { return s.stp.Advance(now) }, timer: s.stp,
			apply: func(s *Switch, _ time.Time, fx layer.Effects) { s.applySTPEffects(fx) },
			clone: func(s, cp *Switch) { cp.stp = s.stp.Clone() },
			bind:  func(cp *Switch) { cp.bridge.SetGate(cp.stp, protocolScope(cp.nodeID, stp.LayerName)) },
		}
	}
	if s.lag != nil {
		entries[1] = layerEntry{
			advance: func(s *Switch, now time.Time) layer.Effects { return s.lag.Advance(now) }, timer: s.lag,
			apply: func(s *Switch, now time.Time, fx layer.Effects) { s.applyLAGEffects(now, fx) },
			clone: func(s, cp *Switch) { cp.lag = s.lag.Clone() },
			bind:  func(cp *Switch) { cp.bridge.SetSelector(lagSelector{sw: cp}, protocolScope(cp.nodeID, lag.LayerName)) },
		}
	}
	if s.loopprotect != nil {
		entries[2] = layerEntry{
			advance: func(s *Switch, now time.Time) layer.Effects { return s.loopprotect.Advance(now) }, timer: s.loopprotect,
			apply: func(s *Switch, _ time.Time, fx layer.Effects) { s.applyLoopProtectEffects(fx) },
			clone: func(s, cp *Switch) { cp.loopprotect = s.loopprotect.Clone() },
			bind:  func(cp *Switch) { cp.bridge.SetGate(cp.loopprotect, protocolScope(cp.nodeID, loopprotect.LayerName)) },
		}
	}
	if s.bridge != nil {
		entries[3] = layerEntry{advance: func(s *Switch, now time.Time) layer.Effects { return s.bridge.Advance(now) }, age: true, clone: func(s, cp *Switch) { cp.bridge = s.bridge.Clone() }}
	}
	if s.mcast != nil {
		entries[4] = layerEntry{
			advance: func(s *Switch, now time.Time) layer.Effects { return s.mcast.Advance(now) }, age: true,
			clone: func(s, cp *Switch) { cp.mcast = s.mcast.Clone() },
			bind: func(cp *Switch) {
				cp.bridge.SetGroupResolver(mcastResolver{sw: cp}, protocolScope(cp.nodeID, mcast.LayerName), mcast.LayerName, mcast.RuleGroupMembers)
			},
		}
	}
	if s.routing != nil {
		entries[5] = layerEntry{advance: func(s *Switch, now time.Time) layer.Effects { return s.routing.Advance(now) }, timer: s.routing, age: true, clone: func(s, cp *Switch) { cp.routing = s.routing.Clone() }, apply: func(s *Switch, now time.Time, fx layer.Effects) {
			s.emitRouting(now, fx.Emissions)
			s.applyRoutingExits(now, s.routing.DrainExits())
		}}
	}
	if s.filter != nil {
		entries[6] = layerEntry{clone: func(s, cp *Switch) { cp.filter = s.filter.Clone() }}
	}
	if s.traffic != nil {
		entries[7] = layerEntry{clone: func(s, cp *Switch) { cp.traffic = s.traffic.Clone() }}
	}
	return entries
}

func (s *Switch) advance(now time.Time) {
	for _, entry := range s.layers(now) {
		if entry.advance == nil {
			continue
		}
		fx := entry.advance(s, now)
		if entry.apply != nil {
			entry.apply(s, now, fx)
		}
	}
}
