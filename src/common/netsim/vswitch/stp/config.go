// Package stp implements the Rapid Spanning Tree Protocol (IEEE 802.1D-2004)
// for the virtual switch, providing loop-free topology calculation, root election,
// and state transitions.
package stp

import (
	"fmt"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

const (
	// DefaultBridgePriority is the IEEE 802.1D recommended default bridge priority (32768).
	DefaultBridgePriority uint16 = 32768

	// DefaultHelloTime is the standard hello timer interval of 2 seconds.
	DefaultHelloTime time.Duration = 2 * time.Second

	// DefaultMaxAge is the standard maximum information age of 20 seconds.
	DefaultMaxAge time.Duration = 20 * time.Second

	// DefaultForwardDelay is the standard forward delay timer interval of 15 seconds.
	DefaultForwardDelay time.Duration = 15 * time.Second

	// DefaultPortPriority is the standard administrative port priority (128).
	DefaultPortPriority uint8 = 128

	// MaxPathCost is the largest administrative or operational path cost IEEE 802.1D permits.
	MaxPathCost uint32 = 200_000_000

	// DefaultTxHoldCount is the standard transmit hold count limit of 6 BPDUs per second.
	DefaultTxHoldCount uint8 = 6

	// MigrateTime is the protocol migration delay of 3 seconds (IEEE 802.1D-2004 Table 17-1).
	MigrateTime time.Duration = 3 * time.Second
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
}

// TypeID returns the fact type identifier for Port.
func (p Port) TypeID() string { return "stp.port" }

// Canonical returns the canonical string representation of the Port fact.
func (p Port) Canonical() string {
	return fmt.Sprintf("priority=%d,path_cost=%d,admin_edge=%t,auto_edge=%t,point_to_point=%q",
		effectivePortPriority(p.Priority, p.PriorityPresent),
		p.PathCost,
		p.AdminEdge,
		p.AutoEdge,
		effectivePointToPoint(p.PointToPoint))
}

// TypeID returns the fact type identifier for PointToPointMode.
func (m PointToPointMode) TypeID() string { return "stp.point_to_point" }

// Canonical returns the string representation of the mode.
func (m PointToPointMode) Canonical() string { return string(effectivePointToPoint(m)) }

// Config defines the spanning tree configuration of a virtual switch.
type Config struct {
	Priority        uint16
	PriorityPresent bool
	Address         netaddr.MAC
	HelloTime       time.Duration
	MaxAge          time.Duration
	ForwardDelay    time.Duration
	TxHoldCount     uint8
	Ports           map[string]Port
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
func (c Config) Normalize() Config {
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
	return cloned
}

// DefaultPathCost returns the IEEE 802.1D-2004 recommended path cost for the
// given link speed in bits per second. A speed between two rows takes the cost
// of the row at or below it. A zero or unknown speed returns 20,000.
func DefaultPathCost(speedBPS uint64) uint32 {
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

// ValidateTimers checks the individual and relational IEEE bounds after applying
// standard defaults to omitted timer values.
func (c Config) ValidateTimers() error {
	helloTime := effectiveHelloTime(c.HelloTime)
	maxAge := effectiveMaxAge(c.MaxAge)
	forwardDelay := effectiveForwardDelay(c.ForwardDelay)

	// The BPDU carries each timer in 1/256 s in 16 bits and 802.1D-2004
	// clause 17.14 bounds them; a value outside would encode as another.
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
// be a multiple of 4096, effective timers must satisfy IEEE bounds, every configured
// port must exist in the port table, and no configured port may be a LAG member.
func (c Config) Validate(ports port.Table) error {
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
		p, ok := ports.Port(name)
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
