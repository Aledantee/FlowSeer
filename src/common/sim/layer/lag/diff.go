package lag

import (
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// primaryFact wraps a primary port name as a trace.Fact.
type primaryFact string

// TypeID returns the fact type identifier for primaryFact.
func (f primaryFact) TypeID() string { return "lag.primary" }

// Canonical returns the primary port name.
func (f primaryFact) Canonical() string { return string(f) }

// durationFact wraps a time.Duration as a trace.Fact.
type durationFact time.Duration

// TypeID returns the fact type identifier for durationFact.
func (f durationFact) TypeID() string { return "lag.duration" }

// Canonical returns the duration string.
func (f durationFact) Canonical() string { return time.Duration(f).String() }

// rebalanceIntervalFact wraps an optional rebalance interval as a trace.Fact,
// distinguishing an unset (nil) interval from an explicit zero one.
type rebalanceIntervalFact struct {
	Set   bool
	Value time.Duration
}

// TypeID returns the fact type identifier for rebalanceIntervalFact.
func (f rebalanceIntervalFact) TypeID() string { return "lag.rebalance_interval" }

// Canonical returns "unset" or the duration string.
func (f rebalanceIntervalFact) Canonical() string {
	if !f.Set {
		return "unset"
	}

	return f.Value.String()
}

func snapshotRebalanceInterval(interval *time.Duration) rebalanceIntervalFact {
	if interval == nil {
		return rebalanceIntervalFact{}
	}

	return rebalanceIntervalFact{Set: true, Value: *interval}
}

// hashBasisFact wraps a hash basis as a trace.Fact.
type hashBasisFact uint32

// TypeID returns the fact type identifier for hashBasisFact.
func (f hashBasisFact) TypeID() string { return "lag.hash_basis" }

// Canonical returns the decimal string of the hash basis.
func (f hashBasisFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// minLinksFact wraps a min links count as a trace.Fact.
type minLinksFact int

// TypeID returns the fact type identifier for minLinksFact.
func (f minLinksFact) TypeID() string { return "lag.min_links" }

// Canonical returns the decimal string of min links.
func (f minLinksFact) Canonical() string { return strconv.Itoa(int(f)) }

// boolFact wraps a boolean value as a trace.Fact.
type boolFact bool

// TypeID returns the fact type identifier for boolFact.
func (f boolFact) TypeID() string { return "lag.bool" }

// Canonical returns "true" or "false".
func (f boolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// uint16Fact wraps a uint16 as a trace.Fact.
type uint16Fact uint16

// TypeID returns the fact type identifier for uint16Fact.
func (f uint16Fact) TypeID() string { return "lag.uint16" }

// Canonical returns the decimal string of the uint16 value.
func (f uint16Fact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// macFact wraps a netaddr.MAC as a trace.Fact.
type macFact netaddr.MAC

// TypeID returns the fact type identifier for macFact.
func (f macFact) TypeID() string { return "lag.mac" }

// Canonical returns the formatted MAC string.
func (f macFact) Canonical() string { return netaddr.MAC(f).String() }

// portPriorityFact wraps a member port priority as a trace.Fact.
type portPriorityFact uint16

// TypeID returns the fact type identifier for portPriorityFact.
func (f portPriorityFact) TypeID() string { return "lag.port_priority" }

// Canonical returns the decimal string of the port priority.
func (f portPriorityFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

type lagSnapshotFact string

func (f lagSnapshotFact) TypeID() string    { return "lag.lag" }
func (f lagSnapshotFact) Canonical() string { return string(f) }

func snapshotLAG(l LAG) lagSnapshotFact {
	var b strings.Builder
	b.WriteString("mode=")
	b.WriteString(strconv.Quote(string(l.Mode)))
	b.WriteString(";primary=")
	b.WriteString(strconv.Quote(l.Primary))
	b.WriteString(";up_delay_ns=")
	b.WriteString(strconv.FormatInt(int64(l.UpDelay), 10))
	b.WriteString(";down_delay_ns=")
	b.WriteString(strconv.FormatInt(int64(l.DownDelay), 10))
	b.WriteString(";hash_basis=")
	b.WriteString(strconv.FormatUint(uint64(l.HashBasis), 10))
	b.WriteString(";min_links=")
	b.WriteString(strconv.Itoa(l.MinLinks))
	b.WriteString(";rebalance_interval=")
	b.WriteString(snapshotRebalanceInterval(l.RebalanceInterval).Canonical())
	b.WriteString(";lacp={mode=")
	b.WriteString(strconv.Quote(string(l.LACP.Mode)))
	b.WriteString(";fast=")
	b.WriteString(strconv.FormatBool(l.LACP.Fast))
	b.WriteString(";system_priority=")
	b.WriteString(strconv.FormatUint(uint64(l.LACP.SystemPriority), 10))
	b.WriteString(";system_id=")
	b.WriteString(strconv.Quote(l.LACP.SystemID.String()))
	b.WriteString(";key=")
	b.WriteString(strconv.FormatUint(uint64(l.LACP.Key), 10))
	b.WriteString(";fallback=")
	b.WriteString(strconv.FormatBool(l.LACP.Fallback))
	b.WriteString("};members={")
	for i, name := range sortedKeys(l.Members) {
		if i > 0 {
			b.WriteByte(',')
		}
		member := l.Members[name]
		b.WriteString(strconv.Quote(name))
		b.WriteString(":{priority=")
		b.WriteString(strconv.FormatUint(uint64(member.Priority), 10))
		b.WriteString(";key=")
		b.WriteString(strconv.FormatUint(uint64(member.Key), 10))
		b.WriteByte('}')
	}
	b.WriteByte('}')

	return lagSnapshotFact(b.String())
}

// Diff computes the difference between two link aggregation configurations,
// reporting changes to LAG settings and per-member administrative parameters.
// Diff normalizes both sides with the zero [layer.Env], so defaults that derive
// from the port table or the switch MAC compare as written: a default
// LACPConfig.Key stays 0, a zero LACPConfig.SystemID stays zero, and no
// port-table member is added. A caller that needs those defaults compared
// normalizes both configurations with the real Env before calling Diff.
func Diff(a, b Config) []trace.Change {
	a = a.Normalize(layer.Env{})
	b = b.Normalize(layer.Env{})

	var changes []trace.Change
	lyr := LayerName

	for _, lagName := range sortedKeys(a.LAGs) {
		aLag := a.LAGs[lagName]
		bLag, exists := b.LAGs[lagName]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "",
				From:    snapshotLAG(aLag),
				To:      nil,
			})

			continue
		}

		if aLag.Mode != bLag.Mode {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "mode",
				From:    aLag.Mode,
				To:      bLag.Mode,
			})
		}

		if aLag.Primary != bLag.Primary {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "primary",
				From:    primaryFact(aLag.Primary),
				To:      primaryFact(bLag.Primary),
			})
		}

		if aLag.UpDelay != bLag.UpDelay {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "up_delay",
				From:    durationFact(aLag.UpDelay),
				To:      durationFact(bLag.UpDelay),
			})
		}

		if aLag.DownDelay != bLag.DownDelay {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "down_delay",
				From:    durationFact(aLag.DownDelay),
				To:      durationFact(bLag.DownDelay),
			})
		}

		if aLag.HashBasis != bLag.HashBasis {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "hash_basis",
				From:    hashBasisFact(aLag.HashBasis),
				To:      hashBasisFact(bLag.HashBasis),
			})
		}

		if aLag.MinLinks != bLag.MinLinks {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "min_links",
				From:    minLinksFact(aLag.MinLinks),
				To:      minLinksFact(bLag.MinLinks),
			})
		}

		aRebalance := snapshotRebalanceInterval(aLag.RebalanceInterval)
		bRebalance := snapshotRebalanceInterval(bLag.RebalanceInterval)
		if aRebalance != bRebalance {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "rebalance_interval",
				From:    aRebalance,
				To:      bRebalance,
			})
		}

		if aLag.LACP.Mode != bLag.LACP.Mode {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_mode",
				From:    aLag.LACP.Mode,
				To:      bLag.LACP.Mode,
			})
		}

		if aLag.LACP.Fast != bLag.LACP.Fast {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_fast",
				From:    boolFact(aLag.LACP.Fast),
				To:      boolFact(bLag.LACP.Fast),
			})
		}

		if aLag.LACP.SystemPriority != bLag.LACP.SystemPriority {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_system_priority",
				From:    uint16Fact(aLag.LACP.SystemPriority),
				To:      uint16Fact(bLag.LACP.SystemPriority),
			})
		}

		if aLag.LACP.SystemID != bLag.LACP.SystemID {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_system_id",
				From:    macFact(aLag.LACP.SystemID),
				To:      macFact(bLag.LACP.SystemID),
			})
		}

		if aLag.LACP.Key != bLag.LACP.Key {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_key",
				From:    uint16Fact(aLag.LACP.Key),
				To:      uint16Fact(bLag.LACP.Key),
			})
		}

		if aLag.LACP.Fallback != bLag.LACP.Fallback {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_fallback",
				From:    boolFact(aLag.LACP.Fallback),
				To:      boolFact(bLag.LACP.Fallback),
			})
		}

		for _, memName := range sortedKeys(aLag.Members) {
			am := aLag.Members[memName]
			bm, memExists := bLag.Members[memName]
			subject := trace.Subject{Kind: "lag_member", Key: memberSubjectKey(lagName, memName)}
			if !memExists {
				changes = append(changes, trace.Change{
					Layer:   lyr,
					Subject: subject,
					Field:   "",
					From:    am,
					To:      nil,
				})

				continue
			}

			if am.Priority != bm.Priority {
				changes = append(changes, trace.Change{
					Layer:   lyr,
					Subject: subject,
					Field:   "priority",
					From:    portPriorityFact(am.Priority),
					To:      portPriorityFact(bm.Priority),
				})
			}

			if am.Key != bm.Key {
				changes = append(changes, trace.Change{
					Layer:   lyr,
					Subject: subject,
					Field:   "key",
					From:    uint16Fact(am.Key),
					To:      uint16Fact(bm.Key),
				})
			}
		}

		for _, memName := range sortedKeys(bLag.Members) {
			if _, memExists := aLag.Members[memName]; !memExists {
				changes = append(changes, trace.Change{
					Layer:   lyr,
					Subject: trace.Subject{Kind: "lag_member", Key: memberSubjectKey(lagName, memName)},
					Field:   "",
					From:    nil,
					To:      bLag.Members[memName],
				})
			}
		}
	}

	for _, lagName := range sortedKeys(b.LAGs) {
		if _, exists := a.LAGs[lagName]; !exists {
			changes = append(changes, trace.Change{
				Layer:   lyr,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "",
				From:    nil,
				To:      snapshotLAG(b.LAGs[lagName]),
			})
		}
	}

	return changes
}

func memberSubjectKey(lagName, memberName string) string {
	return trace.CompositeKey(lagName, memberName)
}
