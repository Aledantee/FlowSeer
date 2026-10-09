package vswitch

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
)

// The positions of the layers in [Switch.layers].
const (
	tableSTP = iota
	tableLAG
	tableLoopProtect
	tableBridge
	tableMcast
	tableRouting
	tableFilter
	tableTraffic
	tableLen
)

type timed interface {
	Advance(time.Time) layer.Effects
	NextWake() (time.Time, bool)
}

type layerEntry struct {
	key       func(*Switch) string
	retention func(*Switch) *LayerRetention
	carry     func(*Switch, *Switch) (int, error)
	perRecord bool
	rebuild   bool
	advance   func(*Switch, time.Time) layer.Effects
	timer     timed
	apply     func(*Switch, time.Time, layer.Effects)
	age       bool
	clone     func(*Switch, *Switch)
	bind      func(*Switch)
}

func (s *Switch) layers(_ time.Time) [tableLen]layerEntry {
	var entries [tableLen]layerEntry
	if s.stp != nil {
		entries[tableSTP] = layerEntry{
			advance: func(s *Switch, now time.Time) layer.Effects { return s.stp.Advance(now) }, timer: s.stp,
			apply: func(s *Switch, _ time.Time, fx layer.Effects) { s.applySTPEffects(fx) },
			clone: func(s, cp *Switch) { cp.stp = s.stp.Clone() },
			bind:  func(cp *Switch) { cp.bridge.SetGate(cp.stp, protocolScope(cp.nodeID, stp.LayerName)) },
		}
	}
	if s.lag != nil {
		entries[tableLAG] = layerEntry{
			advance: func(s *Switch, now time.Time) layer.Effects { return s.lag.Advance(now) }, timer: s.lag,
			apply: func(s *Switch, now time.Time, fx layer.Effects) { s.applyLAGEffects(now, fx) },
			clone: func(s, cp *Switch) { cp.lag = s.lag.Clone() },
			bind:  func(cp *Switch) { cp.bridge.SetSelector(lagSelector{sw: cp}, protocolScope(cp.nodeID, lag.LayerName)) },
		}
	}
	if s.loopprotect != nil {
		entries[tableLoopProtect] = layerEntry{
			advance: func(s *Switch, now time.Time) layer.Effects { return s.loopprotect.Advance(now) }, timer: s.loopprotect,
			apply: func(s *Switch, _ time.Time, fx layer.Effects) { s.applyLoopProtectEffects(fx) },
			clone: func(s, cp *Switch) { cp.loopprotect = s.loopprotect.Clone() },
			bind:  func(cp *Switch) { cp.bridge.SetGate(cp.loopprotect, protocolScope(cp.nodeID, loopprotect.LayerName)) },
		}
	}
	if s.bridge != nil {
		entries[tableBridge] = layerEntry{advance: func(s *Switch, now time.Time) layer.Effects { return s.bridge.Advance(now) }, age: true, clone: func(s, cp *Switch) { cp.bridge = s.bridge.Clone() }}
	}
	if s.mcast != nil {
		entries[tableMcast] = layerEntry{
			advance: func(s *Switch, now time.Time) layer.Effects { return s.mcast.Advance(now) }, age: true,
			clone: func(s, cp *Switch) { cp.mcast = s.mcast.Clone() },
			bind: func(cp *Switch) {
				cp.bridge.SetGroupResolver(mcastResolver{sw: cp}, protocolScope(cp.nodeID, mcast.LayerName), mcast.LayerName, mcast.RuleGroupMembers)
			},
		}
	}
	if s.routing != nil {
		entries[tableRouting] = layerEntry{advance: func(s *Switch, now time.Time) layer.Effects { return s.routing.Advance(now) }, timer: s.routing, age: true, clone: func(s, cp *Switch) { cp.routing = s.routing.Clone() }, apply: func(s *Switch, now time.Time, fx layer.Effects) {
			s.emitRouting(now, fx.Emissions)
			s.applyRoutingExits(now, s.routing.DrainExits())
		}}
	}
	if s.filter != nil {
		entries[tableFilter] = layerEntry{clone: func(s, cp *Switch) { cp.filter = s.filter.Clone() }}
	}
	if s.traffic != nil {
		entries[tableTraffic] = layerEntry{clone: func(s, cp *Switch) { cp.traffic = s.traffic.Clone() }}
	}
	entries[tableSTP].key = func(s *Switch) string {
		if s.cfg.STP == nil {
			return ""
		}
		return stp.RetentionKey(*s.cfg.STP, s.env())
	}
	entries[tableSTP].retention = func(s *Switch) *LayerRetention { return &s.retention.STP }
	entries[tableLAG].key = func(s *Switch) string {
		if s.cfg.LAG == nil {
			return ""
		}
		return lag.RetentionKey(*s.cfg.LAG, s.env())
	}
	entries[tableLAG].retention = func(s *Switch) *LayerRetention { return &s.retention.LAG }
	entries[tableLoopProtect].key = func(s *Switch) string {
		if s.cfg.LoopProtect == nil {
			return ""
		}
		return loopprotect.RetentionKey(*s.cfg.LoopProtect, s.env())
	}
	entries[tableLoopProtect].retention = func(s *Switch) *LayerRetention { return &s.retention.LoopProtect }
	entries[tableBridge].key = func(s *Switch) string {
		if s.cfg.Bridge == nil {
			return ""
		}
		return bridge.RetentionKey(*s.cfg.Bridge, s.env())
	}
	entries[tableBridge].retention = func(s *Switch) *LayerRetention { return &s.retention.Bridge }
	entries[tableMcast].key = func(s *Switch) string {
		if s.cfg.Mcast == nil {
			return ""
		}
		return mcast.RetentionKey(*s.cfg.Mcast, s.env())
	}
	entries[tableMcast].retention = func(s *Switch) *LayerRetention { return &s.retention.Mcast }
	entries[tableRouting].key = func(s *Switch) string {
		if s.cfg.Routing == nil {
			return ""
		}
		return routing.RetentionKey(*s.cfg.Routing, s.env())
	}
	entries[tableRouting].retention = func(s *Switch) *LayerRetention { return &s.retention.Routing }
	entries[tableFilter].key = func(s *Switch) string {
		if s.cfg.Filter == nil {
			return ""
		}
		return filter.RetentionKey(*s.cfg.Filter, s.env())
	}
	entries[tableFilter].retention = func(s *Switch) *LayerRetention { return &s.retention.Filter }
	entries[tableTraffic].key = func(s *Switch) string {
		if s.cfg.Traffic == nil {
			return ""
		}
		return traffic.RetentionKey(*s.cfg.Traffic, s.env())
	}
	entries[tableTraffic].retention = func(s *Switch) *LayerRetention { return &s.retention.Traffic }
	entries[tableBridge].carry, entries[tableBridge].perRecord = carryBridge, true
	entries[tableMcast].carry, entries[tableMcast].perRecord = carryMcast, true
	entries[tableFilter].rebuild = true
	entries[tableTraffic].carry = func(cur, next *Switch) (int, error) {
		if cur.traffic != nil && next.traffic != nil {
			next.traffic.Retain(cur.traffic)
		}
		return 0, nil
	}
	return entries
}

func (s *Switch) advance(now time.Time) {
	for _, entry := range s.layers(now) {
		if entry.advance == nil {
			continue
		}
		fx := entry.advance(s, now)
		if entry.apply == nil {
			continue
		}
		entry.apply(s, now, fx)
	}
}
