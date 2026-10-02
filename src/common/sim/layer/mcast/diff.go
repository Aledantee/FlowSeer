package mcast

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// boolFact represents a boolean multicast snooping setting.
type boolFact bool

// TypeID returns the stable identifier for boolFact.
func (f boolFact) TypeID() string { return "mcast.bool" }

// Canonical returns "true" or "false".
func (f boolFact) Canonical() string {
	if f {
		return "true"
	}

	return "false"
}

// String returns "true" or "false".
func (f boolFact) String() string {
	if f {
		return "true"
	}

	return "false"
}

// durationFact represents a duration interval in multicast snooping.
type durationFact time.Duration

// TypeID returns the stable identifier for durationFact.
func (f durationFact) TypeID() string { return "mcast.duration" }

// Canonical returns the string representation of the duration.
func (f durationFact) Canonical() string { return time.Duration(f).String() }

// String returns the string representation of the duration.
func (f durationFact) String() string { return time.Duration(f).String() }

// intFact represents an integer multicast snooping setting, such as the robustness variable.
type intFact int

// TypeID returns the stable identifier for intFact.
func (f intFact) TypeID() string { return "mcast.int" }

// Canonical returns the decimal string representation of the value.
func (f intFact) Canonical() string { return strconv.Itoa(int(f)) }

// String returns the decimal string representation of the value.
func (f intFact) String() string { return strconv.Itoa(int(f)) }

type routerPortsFact string

func (f routerPortsFact) TypeID() string    { return "mcast.router_ports" }
func (f routerPortsFact) Canonical() string { return string(f) }

// RouterPortsFact returns an immutable, injective snapshot of sorted router-port names.
func RouterPortsFact(ports []string) trace.Fact {
	cp := slices.Clone(ports)
	slices.Sort(cp)
	var out strings.Builder
	for i, portName := range cp {
		if i > 0 {
			out.WriteByte(',')
		}
		out.WriteString(strconv.Quote(portName))
	}

	return routerPortsFact(out.String())
}

type vlanSnoopingSnapshotFact string

func (f vlanSnoopingSnapshotFact) TypeID() string    { return "mcast.vlan_snooping" }
func (f vlanSnoopingSnapshotFact) Canonical() string { return string(f) }

func snapshotVLANSnooping(snooping VLANSnooping) trace.Fact {
	return vlanSnoopingSnapshotFact(snooping.Canonical())
}

// Diff returns deterministic per-VLAN changes between a and b.
// Defaulted intervals and router-port order do not create changes.
func Diff(a, b Config) []trace.Change {
	na := a.Normalize(layer.Env{})
	nb := b.Normalize(layer.Env{})
	var changes []trace.Change

	for _, vid := range sortedVLANIDs(na.VLANs) {
		aCfg := na.VLANs[vid]
		bCfg, ok := nb.VLANs[vid]
		if !ok {
			changes = append(changes, vlanChange(vid, "", snapshotVLANSnooping(aCfg), nil))

			continue
		}

		if from, to := a.Floods(vid), b.Floods(vid); from != to {
			changes = append(changes, vlanChange(vid, "flood_unregistered", boolFact(from), boolFact(to)))
		}
		if aCfg.FastLeave != bCfg.FastLeave {
			changes = append(changes, vlanChange(vid, "fast_leave", boolFact(aCfg.FastLeave), boolFact(bCfg.FastLeave)))
		}

		if !slices.Equal(aCfg.RouterPorts, bCfg.RouterPorts) {
			changes = append(changes, vlanChange(vid, "router_ports", RouterPortsFact(aCfg.RouterPorts), RouterPortsFact(bCfg.RouterPorts)))
		}
		if aCfg.MembershipInterval != bCfg.MembershipInterval {
			changes = append(changes, vlanChange(vid, "membership_interval", durationFact(aCfg.MembershipInterval), durationFact(bCfg.MembershipInterval)))
		}
		if aCfg.RouterPortInterval != bCfg.RouterPortInterval {
			changes = append(changes, vlanChange(vid, "router_port_interval", durationFact(aCfg.RouterPortInterval), durationFact(bCfg.RouterPortInterval)))
		}
		if aCfg.LastMemberQueryInterval != bCfg.LastMemberQueryInterval {
			changes = append(changes, vlanChange(vid, "last_member_query_interval",
				durationFact(aCfg.LastMemberQueryInterval), durationFact(bCfg.LastMemberQueryInterval)))
		}
		if aCfg.LastMemberQueryCount != bCfg.LastMemberQueryCount {
			changes = append(changes, vlanChange(vid, "last_member_query_count",
				intFact(aCfg.LastMemberQueryCount), intFact(bCfg.LastMemberQueryCount)))
		}
	}

	for _, vid := range sortedVLANIDs(nb.VLANs) {
		if _, ok := na.VLANs[vid]; !ok {
			changes = append(changes, vlanChange(vid, "", nil, snapshotVLANSnooping(nb.VLANs[vid])))
		}
	}

	return changes
}

func vlanChange(vid vlan.ID, field string, from, to trace.Fact) trace.Change {
	return trace.Change{
		Layer:   LayerName,
		Subject: trace.Subject{Kind: "vlan", Key: strconv.Itoa(int(vid))},
		Field:   field,
		From:    from,
		To:      to,
	}
}
