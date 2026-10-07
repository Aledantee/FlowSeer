// Package stp implements the Rapid Spanning Tree Protocol and Multiple
// Spanning Tree Protocol using IEEE 802.1Q-2003 clause 13 and the
// P802.1aq/D1.5 draft for the virtual switch. It elects roots
// and drives state transitions. A nil MST configuration leaves the
// bridge on plain RSTP with the CIST as its only tree.
package stp

import (
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
)

const (
	// DefaultBridgePriority is the default bridge priority (32768).
	DefaultBridgePriority uint16 = 32768

	// DefaultHelloTime is the standard hello timer interval of 2 seconds.
	DefaultHelloTime time.Duration = 2 * time.Second

	// DefaultMaxAge is the standard maximum information age of 20 seconds.
	DefaultMaxAge time.Duration = 20 * time.Second

	// DefaultForwardDelay is the standard forward delay timer interval of 15 seconds.
	DefaultForwardDelay time.Duration = 15 * time.Second

	// DefaultPortPriority is the standard administrative port priority (128).
	DefaultPortPriority uint8 = 128

	// MaxPathCost is the largest administrative or operational path cost the layer accepts.
	MaxPathCost uint32 = 200_000_000

	// DefaultTxHoldCount is the default transmit counter limit of 6.
	// Draft P802.1aq/D1.5 Table 13-5 gives the default. Figure 13-15
	// decreases the counter by one per second.
	DefaultTxHoldCount uint8 = 6

	// migrateTime is the protocol migration delay of 3 seconds
	// (draft P802.1aq/D1.5 clause 13.26.6 and Table 13-5).
	migrateTime time.Duration = 3 * time.Second

	// defaultMaxHops is the IEEE 802.1Q recommended default MST region maximum hop count (20).
	defaultMaxHops uint8 = 20
)

// PointToPointMode controls whether a port operates as a point-to-point link.
type PointToPointMode string

const (
	// PointToPointAuto derives point-to-point status from link duplex and media negotiation.
	PointToPointAuto PointToPointMode = "Auto"

	// PointToPointForceTrue forces the port to operate as point-to-point regardless of duplex.
	PointToPointForceTrue PointToPointMode = "ForceTrue"

	// PointToPointForceFalse forces the port to operate as shared media regardless of duplex.
	PointToPointForceFalse PointToPointMode = "ForceFalse"
)

// Port defines administrative spanning tree settings for one network port.
type Port struct {
	Priority        uint8
	PriorityPresent bool
	PathCost        uint32
	AdminEdge       bool
	AutoEdge        bool
	PointToPoint    PointToPointMode

	// BPDUGuard disables the port for spanning tree when a BPDU arrives on it.
	BPDUGuard bool

	// RestrictedRole keeps the port from ever being selected as the root port,
	// which IEEE 802.1Q calls restricted role and vendors call root guard.
	RestrictedRole bool

	// RestrictedTCN keeps a topology change received on the port from
	// propagating to the other ports.
	RestrictedTCN bool

	// LoopGuard holds a port whose received information expired in a
	// discarding role rather than letting it become designated.
	LoopGuard bool
}

// TypeID returns the fact type identifier for Port.
func (p Port) TypeID() string { return "stp.port" }

// Canonical returns the canonical string representation of the Port fact.
func (p Port) Canonical() string {
	return fmt.Sprintf(
		"priority=%d,path_cost=%d,admin_edge=%t,auto_edge=%t,point_to_point=%q,"+
			"bpdu_guard=%t,restricted_role=%t,restricted_tcn=%t,loop_guard=%t",
		effectivePortPriority(p.Priority, p.PriorityPresent),
		p.PathCost,
		p.AdminEdge,
		p.AutoEdge,
		effectivePointToPoint(p.PointToPoint),
		p.BPDUGuard,
		p.RestrictedRole,
		p.RestrictedTCN,
		p.LoopGuard)
}

// TypeID returns the fact type identifier for PointToPointMode.
func (m PointToPointMode) TypeID() string { return "stp.point_to_point" }

// Canonical returns the string representation of the mode.
func (m PointToPointMode) Canonical() string { return string(effectivePointToPoint(m)) }

// Config defines the spanning tree configuration of a virtual switch. A
// non-nil MST selects the Multiple Spanning Tree Protocol region it names. A
// non-nil PVST selects Per-VLAN Rapid Spanning Tree instead. A Config with
// both is invalid. Neither set leaves the bridge running plain Rapid
// Spanning Tree.
type Config struct {
	Priority        uint16
	PriorityPresent bool
	Address         netaddr.MAC
	HelloTime       time.Duration
	MaxAge          time.Duration
	ForwardDelay    time.Duration
	TxHoldCount     uint8
	Ports           map[string]Port
	MST             *MST
	PVST            *PVST
}

// Clone returns a deep copy of the spanning tree configuration.
func (c Config) Clone() Config {
	cloned := c
	if c.Ports != nil {
		cloned.Ports = make(map[string]Port, len(c.Ports))
		for k, v := range c.Ports {
			cloned.Ports[k] = v
		}
	}
	if c.MST != nil {
		mst := c.MST.Clone()
		cloned.MST = &mst
	}
	if c.PVST != nil {
		pvst := c.PVST.Clone()
		cloned.PVST = &pvst
	}
	return cloned
}

func effectivePriority(p uint16, present bool) uint16 {
	if p == 0 && !present {
		return DefaultBridgePriority
	}
	return p
}

func effectiveHelloTime(d time.Duration) time.Duration {
	if d == 0 {
		return DefaultHelloTime
	}
	return d
}

func effectiveMaxAge(d time.Duration) time.Duration {
	if d == 0 {
		return DefaultMaxAge
	}
	return d
}

func effectiveForwardDelay(d time.Duration) time.Duration {
	if d == 0 {
		return DefaultForwardDelay
	}
	return d
}

func effectiveTxHoldCount(c uint8) uint8 {
	if c == 0 {
		return DefaultTxHoldCount
	}
	return c
}

func effectivePortPriority(p uint8, present bool) uint8 {
	if p == 0 && !present {
		return DefaultPortPriority
	}
	return p
}

func effectivePointToPoint(m PointToPointMode) PointToPointMode {
	if m == "" {
		return PointToPointAuto
	}
	return m
}

// Normalize returns a normalized copy of the spanning tree configuration,
// filling unspecified fields with standard defaults.
func (c Config) Normalize(_ layer.Env) Config {
	cloned := c.Clone()
	cloned.Priority = effectivePriority(cloned.Priority, cloned.PriorityPresent)
	cloned.PriorityPresent = true
	cloned.HelloTime = effectiveHelloTime(cloned.HelloTime)
	cloned.MaxAge = effectiveMaxAge(cloned.MaxAge)
	cloned.ForwardDelay = effectiveForwardDelay(cloned.ForwardDelay)
	cloned.TxHoldCount = effectiveTxHoldCount(cloned.TxHoldCount)
	for name, p := range cloned.Ports {
		p.Priority = effectivePortPriority(p.Priority, p.PriorityPresent)
		p.PriorityPresent = true
		p.PointToPoint = effectivePointToPoint(p.PointToPoint)
		cloned.Ports[name] = p
	}
	if cloned.MST != nil {
		normalized := cloned.MST.Normalize()
		for id, inst := range normalized.Instances {
			for name, instPort := range inst.Ports {
				if !instPort.PriorityPresent {
					if bp, ok := cloned.Ports[name]; ok {
						instPort.Priority = bp.Priority
					} else {
						instPort.Priority = DefaultPortPriority
					}
					instPort.PriorityPresent = true
					inst.Ports[name] = instPort
				}
			}
			normalized.Instances[id] = inst
		}
		cloned.MST = &normalized
	}
	if cloned.PVST != nil {
		normalized := cloned.PVST.Normalize(cloned.Priority)
		for vid, tree := range normalized.Trees {
			for name, treePort := range tree.Ports {
				if !treePort.PriorityPresent {
					if bp, ok := cloned.Ports[name]; ok {
						treePort.Priority = bp.Priority
					} else {
						treePort.Priority = DefaultPortPriority
					}
					treePort.PriorityPresent = true
					tree.Ports[name] = treePort
				}
			}
			normalized.Trees[vid] = tree
		}
		cloned.PVST = &normalized
	}
	return cloned
}

// defaultPathCost returns the layer's path cost for the given link speed in
// bits per second. IEEE 802.1D-2004 was not read, so attribution of these
// costs to its recommended values is unverified. A speed between two rows
// takes the cost of the row at or below it. A zero or unknown speed returns 20,000.
func defaultPathCost(speedBPS uint64) uint32 {
	switch {
	case speedBPS >= 100_000_000_000:
		return 200
	case speedBPS >= 10_000_000_000:
		return 2_000
	case speedBPS >= 1_000_000_000:
		return 20_000
	case speedBPS >= 100_000_000:
		return 200_000
	case speedBPS >= 10_000_000:
		return 2_000_000
	default:
		return 20_000
	}
}

// ValidateTimers checks the layer's individual and relational timer bounds
// after applying defaults to omitted values.
func (c Config) ValidateTimers() error {
	helloTime := effectiveHelloTime(c.HelloTime)
	maxAge := effectiveMaxAge(c.MaxAge)
	forwardDelay := effectiveForwardDelay(c.ForwardDelay)

	// These are the layer's configured timer bounds. IEEE 802.1D-2004
	// clause 17.14 was not read, so that attribution is unverified.
	for _, t := range []struct {
		name    string
		value   time.Duration
		low, hi time.Duration
	}{
		{"hello_time", helloTime, time.Second, 10 * time.Second},
		{"max_age", maxAge, 6 * time.Second, 40 * time.Second},
		{"forward_delay", forwardDelay, 4 * time.Second, 30 * time.Second},
	} {
		if t.value < t.low || t.value > t.hi {
			return errs.New().
				Attr("field", t.name).
				Attr("value", t.value).
				Msgf("%s %s is outside %s through %s", t.name, t.value, t.low, t.hi)
		}
	}
	minimumMaxAge := 2 * (helloTime + time.Second)
	if maxAge < minimumMaxAge {
		return errs.New().
			Attr("field", "max_age").
			Attr("hello_time", helloTime).
			Attr("max_age", maxAge).
			Msgf("max_age %s must satisfy max_age >= 2 * (hello_time + 1s) (%s)", maxAge, minimumMaxAge)
	}
	maximumMaxAge := 2 * (forwardDelay - time.Second)
	if maxAge > maximumMaxAge {
		return errs.New().
			Attr("field", "max_age").
			Attr("max_age", maxAge).
			Attr("forward_delay", forwardDelay).
			Msgf("max_age %s must satisfy max_age <= 2 * (forward_delay - 1s) (%s)", maxAge, maximumMaxAge)
	}

	return nil
}

// Validate checks the configuration against the port table: bridge priority must
// be a multiple of 4096 and effective timers must satisfy ValidateTimers.
// Every configured port must exist in the port table and may not be a LAG member.
func (c Config) Validate(env layer.Env) error {
	if c.MST != nil && c.PVST != nil {
		return errs.New().
			Attr("field", "pvst").
			Msg("MST and PVST cannot both be configured")
	}
	if c.Priority%4096 != 0 {
		return errs.New().
			Attr("field", "priority").
			Attr("priority", c.Priority).
			Msgf("bridge priority %d must be a multiple of 4096", c.Priority)
	}
	if c.Address.IsGroup() {
		return errs.New().
			Attr("field", "address").
			Attr("address", c.Address).
			Msgf("spanning tree address %s cannot be a group MAC", c.Address)
	}
	if err := c.ValidateTimers(); err != nil {
		return err
	}
	if c.TxHoldCount > 10 {
		return errs.New().
			Attr("field", "tx_hold_count").
			Attr("tx_hold_count", c.TxHoldCount).
			Msgf("tx hold count %d exceeds maximum 10", c.TxHoldCount)
	}
	// A port id keeps its index in one byte.
	if len(c.Ports) > 255 {
		return errs.New().
			Attr("field", "ports").
			Attr("ports", len(c.Ports)).
			Msg("a bridge holds at most 255 spanning tree ports")
	}

	for _, name := range sortedKeys(c.Ports) {
		p, ok := env.Ports.Port(name)
		if !ok {
			return errs.New().
				Attr("field", "ports."+name).
				Attr("port", name).
				Msgf("spanning tree port %q absent from port table", name)
		}
		if p.LagParent != "" {
			return errs.New().
				Attr("field", "ports."+name).
				Attr("port", name).
				Attr("parent", p.LagParent).
				Msgf("spanning tree port %q cannot be a LAG member", name)
		}
		cfgPort := c.Ports[name]
		if cfgPort.PathCost > MaxPathCost {
			return errs.New().
				Attr("field", "ports."+name+".path_cost").
				Attr("port", name).
				Attr("path_cost", cfgPort.PathCost).
				Msgf("spanning tree path cost %d on port %q exceeds maximum %d", cfgPort.PathCost, name, MaxPathCost)
		}
		switch cfgPort.PointToPoint {
		case "", PointToPointAuto, PointToPointForceTrue, PointToPointForceFalse:
		default:
			return errs.New().
				Attr("field", "ports."+name+".point_to_point").
				Attr("port", name).
				Attr("mode", cfgPort.PointToPoint).
				Msgf("unknown point to point mode %q on port %q", cfgPort.PointToPoint, name)
		}
		// Loop guard watches a port that holds received information. Restricted
		// role denies it the one role it guards against losing, and an edge port
		// is defined never to hold that information, so neither pairing has a
		// correct answer to simulate.
		if cfgPort.LoopGuard && cfgPort.RestrictedRole {
			return errs.New().
				Attr("field", "ports."+name+".loop_guard").
				Attr("port", name).
				Msgf("loop guard on port %q conflicts with restricted role", name)
		}
		if cfgPort.LoopGuard && cfgPort.AdminEdge {
			return errs.New().
				Attr("field", "ports."+name+".loop_guard").
				Attr("port", name).
				Msgf("loop guard on port %q conflicts with admin edge", name)
		}
	}

	if c.MST != nil {
		if err := c.MST.Validate(env.Ports, c.Ports); err != nil {
			return err
		}
	}
	if c.PVST != nil {
		if err := c.PVST.Validate(env.Ports, c.Ports); err != nil {
			return err
		}
	}

	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return keys
}
