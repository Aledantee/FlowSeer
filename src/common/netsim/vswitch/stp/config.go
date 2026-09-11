// Package stp implements the Rapid Spanning Tree Protocol (IEEE 802.1D-2004)
// for the virtual switch, providing loop-free topology calculation, root election,
// and state transitions.
package stp

import (
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
	Priority     uint8
	PathCost     uint32
	AdminEdge    bool
	PointToPoint PointToPointMode
}

// Config defines the spanning tree configuration of a virtual switch.
type Config struct {
	Priority     uint16
	Address      netaddr.MAC
	HelloTime    time.Duration
	MaxAge       time.Duration
	ForwardDelay time.Duration
	Ports        map[string]Port
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

// Validate checks the configuration against the port table: the bridge address
// cannot be zero, bridge priority must be a multiple of 4096, every configured
// port must exist in the port table, and no configured port may be a LAG member.
func (c Config) Validate(ports port.Table) error {
	if c.Address == (netaddr.MAC{}) {
		return errs.New().Msg("spanning tree bridge address cannot be zero")
	}
	if c.Priority%4096 != 0 {
		return errs.New().
			Attr("priority", c.Priority).
			Msgf("bridge priority %d must be a multiple of 4096", c.Priority)
	}
	// The BPDU carries each timer in 1/256 s in 16 bits and 802.1D-2004
	// clause 17.14 bounds them; a value outside would encode as another.
	for _, t := range []struct {
		name    string
		value   time.Duration
		low, hi time.Duration
	}{
		{"hello_time", c.HelloTime, time.Second, 10 * time.Second},
		{"max_age", c.MaxAge, 6 * time.Second, 40 * time.Second},
		{"forward_delay", c.ForwardDelay, 4 * time.Second, 30 * time.Second},
	} {
		if t.value != 0 && (t.value < t.low || t.value > t.hi) {
			return errs.New().
				Attr("field", t.name).
				Attr("value", t.value).
				Msgf("%s %s is outside %s through %s", t.name, t.value, t.low, t.hi)
		}
	}
	// A port id keeps its index in one byte.
	if len(c.Ports) > 255 {
		return errs.New().
			Attr("ports", len(c.Ports)).
			Msg("a bridge holds at most 255 spanning tree ports")
	}

	for _, name := range sortedKeys(c.Ports) {
		p, ok := ports.Port(name)
		if !ok {
			return errs.New().
				Attr("port", name).
				Msgf("spanning tree port %q absent from port table", name)
		}
		if p.LagParent != "" {
			return errs.New().
				Attr("port", name).
				Attr("parent", p.LagParent).
				Msgf("spanning tree port %q cannot be a LAG member", name)
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
