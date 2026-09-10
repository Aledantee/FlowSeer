package stp

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

func effectivePriority(p uint16) uint16 {
	if p == 0 {
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

func effectivePortPriority(p uint8) uint8 {
	if p == 0 {
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

// Diff computes the difference between two spanning tree configurations,
// reporting changes to bridge priority, hello time, max age, forward delay,
// and per-port priority, admin path cost, admin edge, and point-to-point mode.
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change

	const layer trace.Layer = "stp"

	aPrio, bPrio := effectivePriority(a.Priority), effectivePriority(b.Priority)
	if aPrio != bPrio {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "priority",
			From:    aPrio,
			To:      bPrio,
		})
	}

	aHello, bHello := effectiveHelloTime(a.HelloTime), effectiveHelloTime(b.HelloTime)
	if aHello != bHello {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "hello_time",
			From:    aHello,
			To:      bHello,
		})
	}

	aMax, bMax := effectiveMaxAge(a.MaxAge), effectiveMaxAge(b.MaxAge)
	if aMax != bMax {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "max_age",
			From:    aMax,
			To:      bMax,
		})
	}

	aFwd, bFwd := effectiveForwardDelay(a.ForwardDelay), effectiveForwardDelay(b.ForwardDelay)
	if aFwd != bFwd {
		changes = append(changes, trace.Change{
			Layer:   layer,
			Subject: trace.Subject{Kind: "bridge", Key: ""},
			Field:   "forward_delay",
			From:    aFwd,
			To:      bFwd,
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

		if from, to := effectivePortPriority(ap.Priority), effectivePortPriority(bp.Priority); from != to {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "priority",
				From:    from,
				To:      to,
			})
		}

		if ap.PathCost != bp.PathCost {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_path_cost",
				From:    ap.PathCost,
				To:      bp.PathCost,
			})
		}

		if ap.AdminEdge != bp.AdminEdge {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_edge",
				From:    ap.AdminEdge,
				To:      bp.AdminEdge,
			})
		}

		if from, to := effectivePointToPoint(ap.PointToPoint), effectivePointToPoint(bp.PointToPoint); from != to {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "admin_point_to_point",
				From:    from,
				To:      to,
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
