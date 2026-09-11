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

// effectiveAgingTime is the aging time a bridge runs with: the configured one, or
// the default when the configuration left it unset. New and Diff share it so two
// configurations that build the same bridge diff empty.
func effectiveAgingTime(configured time.Duration) time.Duration {
	if configured <= 0 {
		return DefaultAgingTime
	}

	return configured
}

// DefaultServiceTPID is the standard IEEE 802.1ad Service Tag protocol identifier (0x88A8).
const DefaultServiceTPID uint16 = 0x88A8

// Tunnel configures 802.1Q tunnel (QinQ) behavior for a switchport.
type Tunnel struct {
	VID          vlan.ID
	CustomerVIDs []vlan.ID
	TPID         uint16
}

// EffectiveTPID returns the configured service TPID or [DefaultServiceTPID] when unset.
func (t *Tunnel) EffectiveTPID() uint16 {
	if t == nil || t.TPID == 0 {
		return DefaultServiceTPID
	}

	return t.TPID
}

// PriorityTagPolicy specifies how untagged egress frames are priority-tagged.
type PriorityTagPolicy string

const (
	// PriorityTagsNever omits the 802.1Q header on untagged egress.
	PriorityTagsNever PriorityTagPolicy = "Never"

	// PriorityTagsIfNonzero emits an 802.1Q header with VID 0 on untagged egress only when the priority is non-zero.
	PriorityTagsIfNonzero PriorityTagPolicy = "IfNonzero"

	// PriorityTagsAlways emits an 802.1Q header with VID 0 on untagged egress even when the priority is zero.
	PriorityTagsAlways PriorityTagPolicy = "Always"
)

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
	Tunnel           *Tunnel
	PriorityTags     PriorityTagPolicy
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
	if s.Tunnel != nil {
		tun := *s.Tunnel
		if s.Tunnel.CustomerVIDs != nil {
			tun.CustomerVIDs = make([]vlan.ID, len(s.Tunnel.CustomerVIDs))
			copy(tun.CustomerVIDs, s.Tunnel.CustomerVIDs)
		}
		cp.Tunnel = &tun
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
	AgingTime      time.Duration
	MaxEntries     int
	FloodVLANs     []vlan.ID
	ProtectedPorts []string
	ForwardBPDU    bool
	VLAN           *VLAN
}

// Clone returns an independent deep copy of the bridge configuration.
func (c Config) Clone() Config {
	cp := Config{
		AgingTime:   c.AgingTime,
		MaxEntries:  c.MaxEntries,
		ForwardBPDU: c.ForwardBPDU,
		VLAN:        c.VLAN.Clone(),
	}
	if len(c.FloodVLANs) > 0 {
		cp.FloodVLANs = make([]vlan.ID, len(c.FloodVLANs))
		copy(cp.FloodVLANs, c.FloodVLANs)
	}
	if len(c.ProtectedPorts) > 0 {
		cp.ProtectedPorts = make([]string, len(c.ProtectedPorts))
		copy(cp.ProtectedPorts, c.ProtectedPorts)
	}

	return cp
}

// Validate verifies the invariants of the bridge configuration against the given port table.
// It rejects a negative maximum entries bound, a flood VLAN identifier outside 1 through 4094,
// a protected port absent from the port table or naming a LAG member, a switchport naming an
// absent port or a LAG member, an unknown priority-tag policy, a tunnel switchport with PVID,
// tagged, or untagged VLANs configured, a tunnel or customer VLAN identifier outside 1 through 4094,
// a VLAN in both tagged and untagged sets, a VLAN identifier outside 1 through 4094, and any
// switchport when the VLAN table is absent or empty.
func (c Config) Validate(ports port.Table) error {
	if c.MaxEntries < 0 {
		return errs.New().
			Attr("max_entries", c.MaxEntries).
			Msg("max_entries cannot be negative")
	}

	for _, vid := range c.FloodVLANs {
		if !vid.Valid() {
			return errs.New().
				Attr("vlan", vid).
				Msgf("flood VLAN ID %d outside valid range 1..4094", vid)
		}
	}

	for _, name := range c.ProtectedPorts {
		p, ok := ports.Port(name)
		if !ok {
			return errs.New().
				Attr("port", name).
				Msgf("protected port %q not found in port table", name)
		}

		if p.LagParent != "" {
			return errs.New().
				Attr("port", name).
				Attr("member", name).
				Msgf("protected port %q is a member of LAG %q", name, p.LagParent)
		}
	}

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

		switch sw.PriorityTags {
		case "", PriorityTagsNever, PriorityTagsIfNonzero, PriorityTagsAlways:
		default:
			return errs.New().
				Attr("port", name).
				Attr("priority_tags", sw.PriorityTags).
				Msgf("unknown priority-tag policy %q on switchport %q", sw.PriorityTags, name)
		}

		if sw.Tunnel != nil {
			if sw.PVID != nil {
				return errs.New().
					Attr("port", name).
					Msgf("tunnel switchport %q cannot have PVID configured", name)
			}

			if len(sw.Tagged) > 0 {
				return errs.New().
					Attr("port", name).
					Msgf("tunnel switchport %q cannot have tagged VLANs configured", name)
			}

			if len(sw.Untagged) > 0 {
				return errs.New().
					Attr("port", name).
					Msgf("tunnel switchport %q cannot have untagged VLANs configured", name)
			}

			if !sw.Tunnel.VID.Valid() {
				return errs.New().
					Attr("port", name).
					Attr("vlan", sw.Tunnel.VID).
					Msgf("tunnel VLAN ID %d on switchport %q outside valid range 1..4094", sw.Tunnel.VID, name)
			}

			for _, vid := range sw.Tunnel.CustomerVIDs {
				if !vid.Valid() {
					return errs.New().
						Attr("port", name).
						Attr("vlan", vid).
						Msgf("customer VLAN ID %d on switchport %q outside valid range 1..4094", vid, name)
				}
			}
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
