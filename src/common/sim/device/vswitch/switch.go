package vswitch

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/mcast"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// forwardResult combines the Ethernet bridge forwarding outcome with analysis
// trust metadata recording status, issues, evidence, and assumptions.
type forwardResult struct {
	bridge.Result
	Metadata analysis.Metadata
}

// PowerResult combines the Power over Ethernet allocation outcome with analysis
// trust metadata.
type PowerResult struct {
	phy.Allocation
	Metadata analysis.Metadata
}

// Switch simulates a network device composed of a port table and optional
// physical, bridge, filter, link aggregation, spanning tree, loop protection,
// multicast, routing, and traffic layers.
// [Switch.Receive], [Switch.ReceiveMirrored], and [Switch.Advance] return their
// output directly. [Switch.Peek] returns the output without changing runtime state.
//
// A Switch is not safe for concurrent use.
type Switch struct {
	cfg                Config
	ports              port.Table
	bridge             *bridge.Layer
	speeds             map[string]phy.Resolved
	power              phy.Allocation
	stp                *stp.Layer
	loopprotect        *loopprotect.Layer
	lag                *lag.Layer
	mcast              *mcast.Layer
	routing            *routing.Layer
	filter             *filter.Layer
	traffic            *traffic.Layer
	trafficSwitchports map[string]traffic.Switchport
	portP2P            map[string]device.PointToPoint
	portSpeed          map[string]uint64
	protocolIssues     map[string]analysis.Issue
	seeds              []bridge.Seed
	nodeID             string
	metadata           analysis.Metadata
	missingSTP         bool

	call *call

	retention Retention
}

// New constructs a [Switch] from the provided configuration, cloning the configuration,
// assigning missing MAC addresses, validating subsystem configurations, and initializing
// each present subsystem. When the base MAC is zero, New selects the first unused local MAC
// address; two standalone switches may pick the same address. A fabric assigns unique
// addresses across nodes. It returns an error if configuration validation fails.
func New(cfg Config) (*Switch, error) {
	return NewWithSpec(ConstructionSpec{Config: cfg})
}

// NewWithSpec constructs a [Switch] from an immutable deep clone of spec,
// restoring preloaded forwarding database seeds and construction trust metadata.
func NewWithSpec(spec ConstructionSpec) (*Switch, error) {
	norm, err := spec.Normalize()
	if err != nil {
		return nil, err
	}
	return newSwitch(norm.Config, norm.Seeds, norm.NodeID, norm.Metadata)
}

func (s *Switch) env() layer.Env {
	if s == nil {
		return layer.Env{}
	}
	return layer.Env{
		NodeID: s.nodeID,
		Ports:  s.ports,
		MAC:    s.cfg.MAC,
		Speeds: resolvedSpeeds(s),
	}
}

func newSwitch(norm Config, seeds []bridge.Seed, nodeID string, metadata analysis.Metadata) (*Switch, error) {
	sw := &Switch{
		cfg:      norm,
		ports:    norm.Ports.Clone(),
		seeds:    slices.Clone(seeds),
		nodeID:   nodeID,
		metadata: metadata.Clone(),
	}

	if norm.Phy != nil {
		sw.speeds = norm.Phy.Resolve()
		sw.power = norm.Phy.Allocate()
	}

	env := sw.env()

	if norm.Bridge != nil {
		b, err := bridge.New(*norm.Bridge, env)
		if err != nil {
			return nil, err
		}
		b.SetFDBScope(protocolScope(nodeID, bridge.LayerName))
		stpScope := protocolScope(nodeID, stp.LayerName)
		if norm.STP == nil && metadataHasScopedContent(metadata, stpScope) {
			b.SetGate(nil, stpScope)
			sw.missingSTP = true
		}
		sw.bridge = b
	}
	if norm.Mcast != nil {
		m, err := mcast.New(*norm.Mcast, env)
		if err != nil {
			return nil, err
		}
		sw.mcast = m
		if sw.bridge != nil {
			sw.bridge.SetGroupResolver(mcastResolver{sw: sw}, protocolScope(nodeID, mcast.LayerName), mcast.LayerName, mcast.RuleGroupMembers)
		}
	}

	var hasLag bool
	for _, p := range norm.Ports.Ports() {
		if p.Kind == port.LAG {
			hasLag = true
			break
		}
	}
	if hasLag {
		lagCfg := lag.Config{}
		if norm.LAG != nil {
			lagCfg = *norm.LAG
		}
		l, err := lag.New(lagCfg, env)
		if err != nil {
			return nil, err
		}
		sw.lag = l
		if sw.bridge != nil {
			sw.bridge.SetSelector(lagSelector{sw: sw}, protocolScope(nodeID, lag.LayerName))
		}
		for _, p := range norm.Ports.Ports() {
			if p.LagParent != "" && p.Forwards() {
				lCfg := lagCfg.LAGs[p.LagParent]
				if lCfg.LACP.Mode == lag.Off && lCfg.UpDelay == 0 {
					sw.lag.LinkChange(time.Time{}, p.Name, true)
				}
			}
		}
	}

	if norm.STP != nil {
		st, err := stp.New(*norm.STP, env)
		if err != nil {
			return nil, err
		}
		sw.stp = st
		if sw.bridge != nil {
			sw.bridge.SetGate(sw.stp, protocolScope(nodeID, stp.LayerName))
		}
	}

	if norm.LoopProtect != nil {
		lp, err := loopprotect.New(*norm.LoopProtect, env)
		if err != nil {
			return nil, err
		}
		sw.loopprotect = lp
		if sw.bridge != nil {
			sw.bridge.SetGate(sw.loopprotect, protocolScope(nodeID, loopprotect.LayerName))
		}
	}

	if norm.Routing != nil {
		rt, err := routing.New(*norm.Routing, env)
		if err != nil {
			return nil, err
		}
		sw.routing = rt
	}
	if norm.Filter != nil {
		flt, err := filter.New(*norm.Filter, env)
		if err != nil {
			return nil, err
		}
		sw.filter = flt
	}
	if norm.Traffic != nil {
		traf, err := traffic.New(*norm.Traffic, env)
		if err != nil {
			return nil, err
		}
		sw.traffic = traf
		sw.trafficSwitchports = buildTrafficSwitchports(norm)
	}

	if len(seeds) > 0 && sw.bridge != nil {
		if err := sw.bridge.InstallSeeds(seeds); err != nil {
			return nil, err
		}
	}

	sw.recomputeProtocolLinkIssues()

	return sw, nil
}
