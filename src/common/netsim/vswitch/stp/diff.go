package stp

import (
	"strconv"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// MACFact wraps a netaddr.MAC as a trace.Fact.
type MACFact netaddr.MAC

// TypeID returns the fact type identifier for MACFact.
func (f MACFact) TypeID() string { return "stp.mac" }

// Canonical returns the formatted MAC string.
func (f MACFact) Canonical() string { return netaddr.MAC(f).String() }

// PriorityFact wraps a bridge priority as a trace.Fact.
type PriorityFact uint16

// TypeID returns the fact type identifier for PriorityFact.
func (f PriorityFact) TypeID() string { return "stp.priority" }

// Canonical returns the decimal string of the priority.
func (f PriorityFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// DurationFact wraps a time.Duration as a trace.Fact.
type DurationFact time.Duration

// TypeID returns the fact type identifier for DurationFact.
func (f DurationFact) TypeID() string { return "stp.duration" }

// Canonical returns the formatted duration string.
func (f DurationFact) Canonical() string { return time.Duration(f).String() }

// TxHoldCountFact wraps a tx hold count as a trace.Fact.
type TxHoldCountFact uint8

// TypeID returns the fact type identifier for TxHoldCountFact.
func (f TxHoldCountFact) TypeID() string { return "stp.tx_hold_count" }

// Canonical returns the decimal string of the tx hold count.
func (f TxHoldCountFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// PortPriorityFact wraps a port priority as a trace.Fact.
type PortPriorityFact uint8

// TypeID returns the fact type identifier for PortPriorityFact.
func (f PortPriorityFact) TypeID() string { return "stp.port_priority" }

// Canonical returns the decimal string of the port priority.
func (f PortPriorityFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// PathCostFact wraps an admin path cost as a trace.Fact.
type PathCostFact uint32

// TypeID returns the fact type identifier for PathCostFact.
func (f PathCostFact) TypeID() string { return "stp.path_cost" }

// Canonical returns the decimal string of the path cost.
func (f PathCostFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// BoolFact wraps a boolean value as a trace.Fact.
type BoolFact bool

// TypeID returns the fact type identifier for BoolFact.
func (f BoolFact) TypeID() string { return "stp.bool" }

// Canonical returns "true" or "false".
func (f BoolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// Diff computes the difference between two spanning tree configurations,
// reporting changes to bridge priority, hello time, max age, forward delay,
// tx hold count, and per-port priority, admin path cost, admin edge,
// point-to-point mode, auto edge, and the four guards.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize()
	b = b.Normalize()

	var changes []trace.Change

	layer := port.LayerStp

	if a.Address != b.Address {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "address",
			From:    MACFact(a.Address),
			To:      MACFact(b.Address),
		})
	}

	if a.Priority != b.Priority {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "priority",
			From:    PriorityFact(a.Priority),
			To:      PriorityFact(b.Priority),
		})
	}

	if a.HelloTime != b.HelloTime {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "hello_time",
			From:    DurationFact(a.HelloTime),
			To:      DurationFact(b.HelloTime),
		})
	}

	if a.MaxAge != b.MaxAge {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "max_age",
			From:    DurationFact(a.MaxAge),
			To:      DurationFact(b.MaxAge),
		})
	}

	if a.ForwardDelay != b.ForwardDelay {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "forward_delay",
			From:    DurationFact(a.ForwardDelay),
			To:      DurationFact(b.ForwardDelay),
		})
	}

	if a.TxHoldCount != b.TxHoldCount {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "tx_hold_count",
			From:    TxHoldCountFact(a.TxHoldCount),
			To:      TxHoldCountFact(b.TxHoldCount),
		})
	}

	for _, name := range sortedKeys(a.Ports) {
		ap := a.Ports[name]
		bp, exists := b.Ports[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "",
				From:    ap,
				To:      nil,
			})

			continue
		}

		if ap.Priority != bp.Priority {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "priority",
				From:    PortPriorityFact(ap.Priority),
				To:      PortPriorityFact(bp.Priority),
			})
		}

		if ap.PathCost != bp.PathCost {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_path_cost",
				From:    PathCostFact(ap.PathCost),
				To:      PathCostFact(bp.PathCost),
			})
		}

		if ap.AdminEdge != bp.AdminEdge {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_edge",
				From:    BoolFact(ap.AdminEdge),
				To:      BoolFact(bp.AdminEdge),
			})
		}

		if ap.PointToPoint != bp.PointToPoint {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_point_to_point",
				From:    ap.PointToPoint,
				To:      bp.PointToPoint,
			})
		}

		if ap.AutoEdge != bp.AutoEdge {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "auto_edge",
				From:    BoolFact(ap.AutoEdge),
				To:      BoolFact(bp.AutoEdge),
			})
		}

		for _, guard := range []struct {
			field string
			from  bool
			to    bool
		}{
			{"bpdu_guard", ap.BPDUGuard, bp.BPDUGuard},
			{"restricted_role", ap.RestrictedRole, bp.RestrictedRole},
			{"restricted_tcn", ap.RestrictedTCN, bp.RestrictedTCN},
			{"loop_guard", ap.LoopGuard, bp.LoopGuard},
		} {
			if guard.from == guard.to {
				continue
			}
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   guard.field,
				From:    BoolFact(guard.from),
				To:      BoolFact(guard.to),
			})
		}
	}

	for _, name := range sortedKeys(b.Ports) {
		if _, exists := a.Ports[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "",
				From:    nil,
				To:      b.Ports[name],
			})
		}
	}

	return changes
}
