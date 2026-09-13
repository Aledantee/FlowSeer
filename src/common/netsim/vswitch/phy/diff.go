package phy

import (
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// SpeedFact represents a port speed in bits per second.
type SpeedFact uint64

// TypeID returns the stable identifier for SpeedFact.
func (f SpeedFact) TypeID() string { return "phy.speed_bps" }

// Canonical returns the decimal string representation of the speed.
func (f SpeedFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// String returns the decimal string representation of the speed.
func (f SpeedFact) String() string { return strconv.FormatUint(uint64(f), 10) }

// BoolFact represents a boolean physical layer setting.
type BoolFact bool

// TypeID returns the stable identifier for BoolFact.
func (f BoolFact) TypeID() string { return "phy.bool" }

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

// PowerFact represents power in milliwatts.
type PowerFact uint32

// TypeID returns the stable identifier for PowerFact.
func (f PowerFact) TypeID() string { return "phy.power_mw" }

// Canonical returns the decimal string representation of power in milliwatts.
func (f PowerFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// String returns the decimal string representation of power in milliwatts.
func (f PowerFact) String() string { return strconv.FormatUint(uint64(f), 10) }

// ClassFact represents an IEEE PD or PSE class.
type ClassFact uint8

// TypeID returns the stable identifier for ClassFact.
func (f ClassFact) TypeID() string { return "phy.class" }

// Canonical returns the decimal string representation of the class.
func (f ClassFact) Canonical() string { return strconv.Itoa(int(f)) }

// String returns the decimal string representation of the class.
func (f ClassFact) String() string { return strconv.Itoa(int(f)) }

type speedsFact string

func (f speedsFact) TypeID() string    { return "phy.supported_speeds" }
func (f speedsFact) Canonical() string { return string(f) }

// SpeedsFact returns an immutable, sorted snapshot of supported Ethernet speeds.
func SpeedsFact(speeds []uint64) trace.Fact {
	cp := slices.Clone(speeds)
	slices.Sort(cp)
	var strs []string
	for _, s := range cp {
		strs = append(strs, strconv.FormatUint(s, 10))
	}

	return speedsFact(strings.Join(strs, ","))
}

type ethernetSnapshotFact string

func (f ethernetSnapshotFact) TypeID() string    { return "phy.ethernet" }
func (f ethernetSnapshotFact) Canonical() string { return string(f) }

func snapshotEthernet(ethernet Ethernet) trace.Fact {
	return ethernetSnapshotFact(ethernet.Canonical())
}

type psePortSnapshotFact string

func (f psePortSnapshotFact) TypeID() string    { return "phy.pse_port" }
func (f psePortSnapshotFact) Canonical() string { return string(f) }

func snapshotPSEPort(psePort PsePort) trace.Fact {
	return psePortSnapshotFact(psePort.Canonical())
}

// StringFact represents a string setting in the physical layer.
type StringFact string

// TypeID returns the stable identifier for StringFact.
func (f StringFact) TypeID() string { return "phy.string" }

// Canonical returns the string value.
func (f StringFact) Canonical() string { return string(f) }

// String returns the string value.
func (f StringFact) String() string { return string(f) }

// Diff computes the difference between two physical-layer configurations,
// covering all behavior-bearing fields for Ethernet and PoE.
func Diff(a, b Config) []trace.Change {
	na := a.Normalize()
	nb := b.Normalize()
	changes := diffEthernet(na.Ethernet, nb.Ethernet)

	return append(changes, diffPoE(na.PoE, nb.PoE)...)
}

func diffEthernet(a, b map[string]Ethernet) []trace.Change {
	var changes []trace.Change

	for _, name := range sortedKeys(a) {
		ae := a[name]
		be, exists := b[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				From:    snapshotEthernet(ae),
			})

			continue
		}

		if from, to := settingSpeed(ae), settingSpeed(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "speed_bps",
				From:    SpeedFact(from),
				To:      SpeedFact(to),
			})
		}
		if from, to := settingDuplex(ae), settingDuplex(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "duplex",
				From:    from,
				To:      to,
			})
		}
		if from, to := settingAutoNegotiation(ae), settingAutoNegotiation(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "auto_negotiation_enabled",
				From:    BoolFact(from),
				To:      BoolFact(to),
			})
		}
		if !slices.Equal(ae.SupportedSpeedsBPS, be.SupportedSpeedsBPS) {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "supported_speeds_bps",
				From:    SpeedsFact(ae.SupportedSpeedsBPS),
				To:      SpeedsFact(be.SupportedSpeedsBPS),
			})
		}
		if ae.AutoNegotiationSupported != be.AutoNegotiationSupported {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "auto_negotiation_supported",
				From:    ae.AutoNegotiationSupported,
				To:      be.AutoNegotiationSupported,
			})
		}
		if from, to := observedSpeed(ae), observedSpeed(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "observed_speed_bps",
				From:    SpeedFact(from),
				To:      SpeedFact(to),
			})
		}
		if from, to := observedDuplex(ae), observedDuplex(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "observed_duplex",
				From:    from,
				To:      to,
			})
		}
		if from, to := ae.Resolve().Source, be.Resolve().Source; from != to {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "resolve_source",
				From:    StringFact(from),
				To:      StringFact(to),
			})
		}
	}

	for _, name := range sortedKeys(b) {
		if _, exists := a[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerEthernet,
				Subject: trace.Subject{Kind: "port", Key: name},
				To:      snapshotEthernet(b[name]),
			})
		}
	}

	return changes
}

func diffPoE(a, b *PoE) []trace.Change {
	var aGroups, bGroups map[string]Group
	var aPorts, bPorts map[string]PsePort
	if a != nil {
		aGroups, aPorts = a.Groups, a.Ports
	}
	if b != nil {
		bGroups, bPorts = b.Groups, b.Ports
	}

	var changes []trace.Change

	for _, name := range sortedKeys(aGroups) {
		ag := aGroups[name]
		bg, exists := bGroups[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "pse_group", Key: name},
				From:    ag,
			})

			continue
		}
		if ag.PowerMilliwatts != bg.PowerMilliwatts {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "pse_group", Key: name},
				Field:   "power_milliwatts",
				From:    PowerFact(ag.PowerMilliwatts),
				To:      PowerFact(bg.PowerMilliwatts),
			})
		}
	}
	for _, name := range sortedKeys(bGroups) {
		if _, exists := aGroups[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "pse_group", Key: name},
				To:      bGroups[name],
			})
		}
	}

	for _, name := range sortedKeys(aPorts) {
		ap := aPorts[name]
		bp, exists := bPorts[name]
		if !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				From:    snapshotPSEPort(ap),
			})

			continue
		}

		if ap.Group != bp.Group {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "group",
				From:    StringFact(ap.Group),
				To:      StringFact(bp.Group),
			})
		}
		if ap.MaxClass != bp.MaxClass {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "max_class",
				From:    ClassFact(ap.MaxClass),
				To:      ClassFact(bp.MaxClass),
			})
		}
		if ap.Enabled != bp.Enabled {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "enabled",
				From:    BoolFact(ap.Enabled),
				To:      BoolFact(bp.Enabled),
			})
		}
		if !equalUint32Ptr(ap.Limit, bp.Limit) {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "power_limit_milliwatts",
				From:    limitFact(ap.Limit),
				To:      limitFact(bp.Limit),
			})
		}
		if ap.Priority != bp.Priority {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "priority",
				From:    ap.Priority,
				To:      bp.Priority,
			})
		}
		if ap.PD != bp.PD {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "pd",
				From:    ap.PD,
				To:      bp.PD,
			})
		}
		if !equalUint8Ptr(ap.PDClass, bp.PDClass) {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "pd_class",
				From:    pdClassFact(ap.PDClass),
				To:      pdClassFact(bp.PDClass),
			})
		}
	}
	for _, name := range sortedKeys(bPorts) {
		if _, exists := aPorts[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   port.LayerPoe,
				Subject: trace.Subject{Kind: "port", Key: name},
				To:      snapshotPSEPort(bPorts[name]),
			})
		}
	}

	return changes
}

func settingSpeed(e Ethernet) uint64 {
	if e.Setting == nil {
		return 0
	}

	return e.Setting.SpeedBPS
}

func settingDuplex(e Ethernet) Duplex {
	if e.Setting == nil {
		return ""
	}

	return e.Setting.Duplex
}

func settingAutoNegotiation(e Ethernet) bool {
	return e.Setting != nil && e.Setting.AutoNegotiation
}

func observedSpeed(e Ethernet) uint64 {
	if e.Observed == nil {
		return 0
	}

	return e.Observed.SpeedBPS
}

func observedDuplex(e Ethernet) Duplex {
	if e.Observed == nil {
		return ""
	}

	return e.Observed.Duplex
}

func limitFact(limit *uint32) trace.Fact {
	if limit == nil {
		return nil
	}

	return PowerFact(*limit)
}

func pdClassFact(pdClass *uint8) trace.Fact {
	if pdClass == nil {
		return nil
	}

	return ClassFact(*pdClass)
}

func equalUint32Ptr(a, b *uint32) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	return *a == *b
}

func equalUint8Ptr(a, b *uint8) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	return *a == *b
}
