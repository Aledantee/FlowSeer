package mcast

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
)

const layer trace.Layer = "mcast"

// BoolFact represents a boolean multicast snooping setting.
type BoolFact bool

// TypeID returns the stable identifier for BoolFact.
func (f BoolFact) TypeID() string { return "mcast.bool" }

// Canonical returns "true" or "false".
func (f BoolFact) Canonical() string {
	if f {
		return "true"
	}

	return "false"
}

// String returns "true" or "false".
func (f BoolFact) String() string {
	if f {
		return "true"
	}

	return "false"
}

// DurationFact represents a duration interval in multicast snooping.
type DurationFact time.Duration

// TypeID returns the stable identifier for DurationFact.
func (f DurationFact) TypeID() string { return "mcast.duration" }

// Canonical returns the string representation of the duration.
func (f DurationFact) Canonical() string { return time.Duration(f).String() }

// String returns the string representation of the duration.
func (f DurationFact) String() string { return time.Duration(f).String() }

// RouterPortsFact represents configured router ports.
type RouterPortsFact []string

// TypeID returns the stable identifier for RouterPortsFact.
func (f RouterPortsFact) TypeID() string { return "mcast.router_ports" }

// Canonical returns the comma-separated sorted port names.
func (f RouterPortsFact) Canonical() string {
	cp := slices.Clone(f)
	slices.Sort(cp)

	return strings.Join(cp, ",")
}

// String returns the canonical representation.
func (f RouterPortsFact) String() string { return f.Canonical() }

// Diff returns deterministic per-VLAN changes between a and b.
// Defaulted intervals and router-port order do not create changes.
func Diff(a, b Config) []trace.Change {
	na := a.Normalize()
	nb := b.Normalize()
	var changes []trace.Change

	for _, vid := range sortedVLANIDs(na.VLANs) {
		aCfg := na.VLANs[vid]
		bCfg, ok := nb.VLANs[vid]
		if !ok {
			changes = append(changes, vlanChange(vid, "", aCfg, nil))

			continue
		}

		if from, to := a.Floods(vid), b.Floods(vid); from != to {
			changes = append(changes, vlanChange(vid, "flood_unregistered", BoolFact(from), BoolFact(to)))
		}
		if aCfg.FastLeave != bCfg.FastLeave {
			changes = append(changes, vlanChange(vid, "fast_leave", BoolFact(aCfg.FastLeave), BoolFact(bCfg.FastLeave)))
		}

		if !slices.Equal(aCfg.RouterPorts, bCfg.RouterPorts) {
			changes = append(changes, vlanChange(vid, "router_ports", RouterPortsFact(aCfg.RouterPorts), RouterPortsFact(bCfg.RouterPorts)))
		}
		if aCfg.MembershipInterval != bCfg.MembershipInterval {
			changes = append(changes, vlanChange(vid, "membership_interval", DurationFact(aCfg.MembershipInterval), DurationFact(bCfg.MembershipInterval)))
		}
		if aCfg.RouterPortInterval != bCfg.RouterPortInterval {
			changes = append(changes, vlanChange(vid, "router_port_interval", DurationFact(aCfg.RouterPortInterval), DurationFact(bCfg.RouterPortInterval)))
		}
	}

	for _, vid := range sortedVLANIDs(nb.VLANs) {
		if _, ok := na.VLANs[vid]; !ok {
			changes = append(changes, vlanChange(vid, "", nil, nb.VLANs[vid]))
		}
	}

	return changes
}

func vlanChange(vid vlan.ID, field string, from, to trace.Fact) trace.Change {
	return trace.Change{
		Layer:   layer,
		Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(vid))},
		Field:   field,
		From:    from,
		To:      to,
	}
}
