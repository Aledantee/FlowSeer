package phy

import (
	"slices"
	"strconv"
	"strings"

	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// speedFact represents a port speed in bits per second.
type speedFact uint64

// TypeID returns the stable identifier for speedFact.
func (f speedFact) TypeID() string { return "phy.speed_bps" }

// Canonical returns the decimal string representation of the speed.
func (f speedFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// String returns the decimal string representation of the speed.
func (f speedFact) String() string { return strconv.FormatUint(uint64(f), 10) }

// boolFact represents a boolean physical layer setting.
type boolFact bool

// TypeID returns the stable identifier for boolFact.
func (f boolFact) TypeID() string { return "phy.bool" }

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

// powerFact represents power in nanowatts.
type powerFact uint64

// TypeID returns the stable identifier for powerFact.
func (f powerFact) TypeID() string { return "phy.power_nw" }

// Canonical returns the decimal string representation of power in nanowatts.
func (f powerFact) Canonical() string { return strconv.FormatUint(uint64(f), 10) }

// String returns the decimal string representation of power in nanowatts.
func (f powerFact) String() string { return strconv.FormatUint(uint64(f), 10) }

// classFact represents an IEEE PD or PSE class.
type classFact uint8

// TypeID returns the stable identifier for classFact.
func (f classFact) TypeID() string { return "phy.class" }

// Canonical returns the decimal string representation of the class.
func (f classFact) Canonical() string { return strconv.Itoa(int(f)) }

// String returns the decimal string representation of the class.
func (f classFact) String() string { return strconv.Itoa(int(f)) }

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

// stringFact represents a string setting in the physical layer.
type stringFact string

// TypeID returns the stable identifier for stringFact.
func (f stringFact) TypeID() string { return "phy.string" }

// Canonical returns the string value.
func (f stringFact) Canonical() string { return string(f) }

// String returns the string value.
func (f stringFact) String() string { return string(f) }

// Diff computes the difference between two physical-layer configurations,
// covering all behavior-bearing fields for Ethernet and PoE.
func Diff(a, b Config) []trace.Change {
	na := a.Normalize(layer.Env{})
	nb := b.Normalize(layer.Env{})
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
				Layer:   LayerName,
				Subject: trace.Subject{Kind: "port", Key: name},
				From:    snapshotEthernet(ae),
			})

			continue
		}

		if from, to := settingSpeed(ae), settingSpeed(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "speed_bps",
				From:    speedFact(from),
				To:      speedFact(to),
			})
		}
		if from, to := settingDuplex(ae), settingDuplex(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "duplex",
				From:    from,
				To:      to,
			})
		}
		if from, to := settingAutoNegotiation(ae), settingAutoNegotiation(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "auto_negotiation_enabled",
				From:    boolFact(from),
				To:      boolFact(to),
			})
		}
		if !slices.Equal(ae.SupportedSpeedsBPS, be.SupportedSpeedsBPS) {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "supported_speeds_bps",
				From:    SpeedsFact(ae.SupportedSpeedsBPS),
				To:      SpeedsFact(be.SupportedSpeedsBPS),
			})
		}
		if ae.AutoNegotiationSupported != be.AutoNegotiationSupported {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "auto_negotiation_supported",
				From:    ae.AutoNegotiationSupported,
				To:      be.AutoNegotiationSupported,
			})
		}
		if from, to := observedSpeed(ae), observedSpeed(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "observed_speed_bps",
				From:    speedFact(from),
				To:      speedFact(to),
			})
		}
		if from, to := observedDuplex(ae), observedDuplex(be); from != to {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "observed_duplex",
				From:    from,
				To:      to,
			})
		}
	}

	for _, name := range sortedKeys(b) {
		if _, exists := a[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   LayerName,
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
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "pse_group", Key: name},
				From:    ag,
			})

			continue
		}
		if ag.PowerNanowatts != bg.PowerNanowatts {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "pse_group", Key: name},
				Field:   "power_nanowatts",
				From:    powerFact(ag.PowerNanowatts),
				To:      powerFact(bg.PowerNanowatts),
			})
		}
	}
	for _, name := range sortedKeys(bGroups) {
		if _, exists := aGroups[name]; !exists {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
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
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "port", Key: name},
				From:    snapshotPSEPort(ap),
			})

			continue
		}

		if ap.Group != bp.Group {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "group",
				From:    stringFact(ap.Group),
				To:      stringFact(bp.Group),
			})
		}
		if ap.MaxClass != bp.MaxClass {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "max_class",
				From:    classFact(ap.MaxClass),
				To:      classFact(bp.MaxClass),
			})
		}
		if ap.Enabled != bp.Enabled {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "enabled",
				From:    boolFact(ap.Enabled),
				To:      boolFact(bp.Enabled),
			})
		}
		if !equalUint64Ptr(ap.Limit, bp.Limit) {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "power_limit_nanowatts",
				From:    limitFact(ap.Limit),
				To:      limitFact(bp.Limit),
			})
		}
		if ap.Priority != bp.Priority {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "priority",
				From:    ap.Priority,
				To:      bp.Priority,
			})
		}
		if ap.PD != bp.PD {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
				Subject: trace.Subject{Kind: "port", Key: name},
				Field:   "pd",
				From:    ap.PD,
				To:      bp.PD,
			})
		}
		if !equalUint8Ptr(ap.PDClass, bp.PDClass) {
			changes = append(changes, trace.Change{
				Layer:   LayerNamePoE,
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
				Layer:   LayerNamePoE,
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

func limitFact(limit *uint64) trace.Fact {
	if limit == nil {
		return nil
	}

	return powerFact(*limit)
}

func pdClassFact(pdClass *uint8) trace.Fact {
	if pdClass == nil {
		return nil
	}

	return classFact(*pdClass)
}

func equalUint64Ptr(a, b *uint64) bool {
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
