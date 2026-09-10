package bridge

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// DefaultAgingTime is the standard IEEE 802.1D recommended forwarding database aging time (300 seconds).
const DefaultAgingTime = 300 * time.Second

// Admission specifies which Ethernet frame-tag forms a switchport admits at ingress.
type Admission string

const (
	// All admits untagged, priority-tagged, and tagged frames.
	All Admission = "All"

	// TaggedOnly admits only frames carrying a nonzero VLAN identifier.
	TaggedOnly Admission = "TaggedOnly"

	// UntaggedAndPriorityTaggedOnly admits only untagged frames and priority-tagged frames with VID zero.
	UntaggedAndPriorityTaggedOnly Admission = "UntaggedAndPriorityTaggedOnly"
)

// Switchport configures VLAN processing and ingress policies for a single bridge port.
type Switchport struct {
	PVID             *vlan.ID
	Tagged           []vlan.ID
	Untagged         []vlan.ID
	IngressFiltering bool
	Admission        Admission
}

// Clone returns an independent deep copy of the switchport configuration.
func (s Switchport) Clone() Switchport {
	cp := s
	if s.PVID != nil {
		pvid := *s.PVID
		cp.PVID = &pvid
	}
	if len(s.Tagged) > 0 {
		cp.Tagged = make([]vlan.ID, len(s.Tagged))
		copy(cp.Tagged, s.Tagged)
	}
	if len(s.Untagged) > 0 {
		cp.Untagged = make([]vlan.ID, len(s.Untagged))
		copy(cp.Untagged, s.Untagged)
	}

	return cp
}

// VLAN holds the bridge VLAN table and per-port switchport configurations.
type VLAN struct {
	Table       map[vlan.ID]string
	Switchports map[string]Switchport
}

// Clone returns an independent deep copy of the VLAN configuration.
func (v *VLAN) Clone() *VLAN {
	if v == nil {
		return nil
	}
	cp := &VLAN{
		Table:       make(map[vlan.ID]string, len(v.Table)),
		Switchports: make(map[string]Switchport, len(v.Switchports)),
	}
	for id, name := range v.Table {
		cp.Table[id] = name
	}
	for portName, sw := range v.Switchports {
		cp.Switchports[portName] = sw.Clone()
	}

	return cp
}

// Config defines the configuration for a [Bridge].
type Config struct {
	AgingTime time.Duration
	VLAN      *VLAN
}

// Clone returns an independent deep copy of the bridge configuration.
func (c Config) Clone() Config {
	return Config{
		AgingTime: c.AgingTime,
		VLAN:      c.VLAN.Clone(),
	}
}

// Validate verifies the invariants of the bridge configuration against the given port table.
// It rejects a switchport naming an absent port or a LAG member, a VLAN in both tagged and
// untagged sets, a VLAN identifier outside 1 through 4094, and any switchport when the VLAN
// table is absent or empty.
func (c Config) Validate(ports port.Table) error {
	if c.VLAN == nil {
		return nil
	}

	if len(c.VLAN.Switchports) > 0 && len(c.VLAN.Table) == 0 {
		var names []string
		for name := range c.VLAN.Switchports {
			names = append(names, name)
		}
		slices.Sort(names)

		return errs.New().
			Attr("port", names[0]).
			Msgf("switchport %q configured with no VLAN table", names[0])
	}

	for id := range c.VLAN.Table {
		if !id.Valid() {
			return errs.New().
				Attr("vlan", id).
				Msgf("VLAN ID %d outside valid range 1..4094", id)
		}
	}

	var portNames []string
	for name := range c.VLAN.Switchports {
		portNames = append(portNames, name)
	}
	slices.Sort(portNames)

	for _, name := range portNames {
		sw := c.VLAN.Switchports[name]

		p, ok := ports.Port(name)
		if !ok {
			return errs.New().
				Attr("port", name).
				Msgf("switchport %q not found in port table", name)
		}

		if p.LagParent != "" {
			return errs.New().
				Attr("port", name).
				Attr("member", name).
				Msgf("switchport %q is a member of LAG %q", name, p.LagParent)
		}

		if sw.PVID != nil && !sw.PVID.Valid() {
			return errs.New().
				Attr("port", name).
				Attr("vlan", *sw.PVID).
				Msgf("PVID %d on switchport %q outside valid range 1..4094", *sw.PVID, name)
		}

		for _, vid := range sw.Tagged {
			if !vid.Valid() {
				return errs.New().
					Attr("port", name).
					Attr("vlan", vid).
					Msgf("tagged VLAN ID %d on switchport %q outside valid range 1..4094", vid, name)
			}
		}

		for _, vid := range sw.Untagged {
			if !vid.Valid() {
				return errs.New().
					Attr("port", name).
					Attr("vlan", vid).
					Msgf("untagged VLAN ID %d on switchport %q outside valid range 1..4094", vid, name)
			}
		}

		for _, vid := range sw.Tagged {
			if slices.Contains(sw.Untagged, vid) {
				return errs.New().
					Attr("port", name).
					Attr("vlan", vid).
					Msgf("VLAN %d cannot be both tagged and untagged on switchport %q", vid, name)
			}
		}
	}

	return nil
}
