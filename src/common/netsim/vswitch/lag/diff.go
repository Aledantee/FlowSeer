package lag

import (
	"strconv"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// PrimaryFact wraps a primary port name as a trace.Fact.
type PrimaryFact string

// TypeID returns the fact type identifier for PrimaryFact.
func (f PrimaryFact) TypeID() string { return "lag.primary" }

// Canonical returns the primary port name.
func (f PrimaryFact) Canonical() string { return string(f) }

// DurationFact wraps a time.Duration as a trace.Fact.
type DurationFact time.Duration

// TypeID returns the fact type identifier for DurationFact.
func (f DurationFact) TypeID() string { return "lag.duration" }

// Canonical returns the duration string.
func (f DurationFact) Canonical() string { return time.Duration(f).String() }

// HashBasisFact wraps a hash basis as a trace.Fact.
type HashBasisFact uint32

// TypeID returns the fact type identifier for HashBasisFact.
func (f HashBasisFact) TypeID() string { return "lag.hash_basis" }

// Canonical returns the decimal string of the hash basis.
func (f HashBasisFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// MinLinksFact wraps a min links count as a trace.Fact.
type MinLinksFact int

// TypeID returns the fact type identifier for MinLinksFact.
func (f MinLinksFact) TypeID() string { return "lag.min_links" }

// Canonical returns the decimal string of min links.
func (f MinLinksFact) Canonical() string { return strconv.Itoa(int(f)) }

// BoolFact wraps a boolean value as a trace.Fact.
type BoolFact bool

// TypeID returns the fact type identifier for BoolFact.
func (f BoolFact) TypeID() string { return "lag.bool" }

// Canonical returns "true" or "false".
func (f BoolFact) Canonical() string { return strconv.FormatBool(bool(f)) }

// Uint16Fact wraps a uint16 as a trace.Fact.
type Uint16Fact uint16

// TypeID returns the fact type identifier for Uint16Fact.
func (f Uint16Fact) TypeID() string { return "lag.uint16" }

// Canonical returns the decimal string of the uint16 value.
func (f Uint16Fact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// MACFact wraps a netaddr.MAC as a trace.Fact.
type MACFact netaddr.MAC

// TypeID returns the fact type identifier for MACFact.
func (f MACFact) TypeID() string { return "lag.mac" }

// Canonical returns the formatted MAC string.
func (f MACFact) Canonical() string { return netaddr.MAC(f).String() }

// PortPriorityFact wraps a member port priority as a trace.Fact.
type PortPriorityFact uint16

// TypeID returns the fact type identifier for PortPriorityFact.
func (f PortPriorityFact) TypeID() string { return "lag.port_priority" }

// Canonical returns the decimal string of the port priority.
func (f PortPriorityFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

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
func Diff(a, b Config) []trace.Change {
	var changes []trace.Change
	layer := port.LayerLag

	for _, lagName := range sortedKeys(a.LAGs) {
		aLag := a.LAGs[lagName]
		bLag, exists := b.LAGs[lagName]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "",
				From:    snapshotLAG(aLag),
				To:      nil,
			})

			continue
		}

		if aLag.Mode != bLag.Mode {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "mode",
				From:    aLag.Mode,
				To:      bLag.Mode,
			})
		}

		if aLag.Primary != bLag.Primary {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "primary",
				From:    PrimaryFact(aLag.Primary),
				To:      PrimaryFact(bLag.Primary),
			})
		}

		if aLag.UpDelay != bLag.UpDelay {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "up_delay",
				From:    DurationFact(aLag.UpDelay),
				To:      DurationFact(bLag.UpDelay),
			})
		}

		if aLag.DownDelay != bLag.DownDelay {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "down_delay",
				From:    DurationFact(aLag.DownDelay),
				To:      DurationFact(bLag.DownDelay),
			})
		}

		if aLag.HashBasis != bLag.HashBasis {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "hash_basis",
				From:    HashBasisFact(aLag.HashBasis),
				To:      HashBasisFact(bLag.HashBasis),
			})
		}

		if aLag.MinLinks != bLag.MinLinks {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "min_links",
				From:    MinLinksFact(aLag.MinLinks),
				To:      MinLinksFact(bLag.MinLinks),
			})
		}

		if aLag.LACP.Mode != bLag.LACP.Mode {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_mode",
				From:    aLag.LACP.Mode,
				To:      bLag.LACP.Mode,
			})
		}

		if aLag.LACP.Fast != bLag.LACP.Fast {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_fast",
				From:    BoolFact(aLag.LACP.Fast),
				To:      BoolFact(bLag.LACP.Fast),
			})
		}

		if aLag.LACP.SystemPriority != bLag.LACP.SystemPriority {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_system_priority",
				From:    Uint16Fact(aLag.LACP.SystemPriority),
				To:      Uint16Fact(bLag.LACP.SystemPriority),
			})
		}

		if aLag.LACP.SystemID != bLag.LACP.SystemID {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_system_id",
				From:    MACFact(aLag.LACP.SystemID),
				To:      MACFact(bLag.LACP.SystemID),
			})
		}

		if aLag.LACP.Key != bLag.LACP.Key {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_key",
				From:    Uint16Fact(aLag.LACP.Key),
				To:      Uint16Fact(bLag.LACP.Key),
			})
		}

		if aLag.LACP.Fallback != bLag.LACP.Fallback {
			changes = append(changes, trace.Change{
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "lacp_fallback",
				From:    BoolFact(aLag.LACP.Fallback),
				To:      BoolFact(bLag.LACP.Fallback),
			})
		}

		for _, memName := range sortedKeys(aLag.Members) {
			am := aLag.Members[memName]
			bm, memExists := bLag.Members[memName]
			if !memExists {
				changes = append(changes, trace.Change{
					Layer:   layer,
					Subject: trace.Subject{Kind: "port", Key: memName},
					Field:   "",
					From:    am,
					To:      nil,
				})

				continue
			}

			if am.Priority != bm.Priority {
				changes = append(changes, trace.Change{
					Layer:   layer,
					Subject: trace.Subject{Kind: "port", Key: memName},
					Field:   "priority",
					From:    PortPriorityFact(am.Priority),
					To:      PortPriorityFact(bm.Priority),
				})
			}

			if am.Key != bm.Key {
				changes = append(changes, trace.Change{
					Layer:   layer,
					Subject: trace.Subject{Kind: "port", Key: memName},
					Field:   "key",
					From:    Uint16Fact(am.Key),
					To:      Uint16Fact(bm.Key),
				})
			}
		}

		for _, memName := range sortedKeys(bLag.Members) {
			if _, memExists := aLag.Members[memName]; !memExists {
				changes = append(changes, trace.Change{
					Layer:   layer,
					Subject: trace.Subject{Kind: "port", Key: memName},
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
				Layer:   layer,
				Subject: trace.Subject{Kind: "lag", Key: lagName},
				Field:   "",
				From:    nil,
				To:      snapshotLAG(b.LAGs[lagName]),
			})
		}
	}

	return changes
}
