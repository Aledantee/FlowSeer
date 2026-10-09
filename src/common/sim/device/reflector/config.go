// Package reflector implements an mDNS reflection endpoint.
package reflector

import (
	"maps"
	"net/netip"
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
)

// Config is a third node kind beside a switch and a host: an endpoint with
// no address of its own reachable by unicast, whose ports all carry a cable,
// unlike a switch's, which may go uncabled. Ports holds each port's physical
// facts under a switch port's validation rules, keyed by port name; a port
// with no cable has nothing to reflect onto, so every key must appear on a
// cable. Attachments names the VLANs a port answers on and the addresses a
// copy originates from: an arriving mDNS query is accepted on the attachment
// its tag form and port match, and reflected as a fresh copy on every other
// attachment of the query's address family.
type Config struct {
	Address     netaddr.MAC
	Ports       map[string]phy.Ethernet
	Attachments map[string]Attachment
}

// Attachment names one VLAN a reflector answers on: Port is one of the
// reflector's declared ports, a nil VLAN means the port's untagged form, and
// Addresses are the source addresses a copy originates from on that VLAN.
type Attachment struct {
	Port      string
	VLAN      *vlan.ID
	Addresses []netip.Prefix
}

// Clone returns an independent deep copy of the reflector configuration.
func (r Config) Clone() Config {
	cp := r
	if r.Ports != nil {
		cp.Ports = make(map[string]phy.Ethernet, len(r.Ports))
		for k, v := range r.Ports {
			cp.Ports[k] = v.Clone()
		}
	}
	if r.Attachments != nil {
		cp.Attachments = make(map[string]Attachment, len(r.Attachments))
		for k, v := range r.Attachments {
			cp.Attachments[k] = v.Clone()
		}
	}

	return cp
}

// Equal reports whether two reflector configurations are identical.
func (r Config) Equal(other Config) bool {
	if r.Address != other.Address {
		return false
	}
	if len(r.Ports) != len(other.Ports) || len(r.Attachments) != len(other.Attachments) {
		return false
	}
	for k, e := range r.Ports {
		oe, ok := other.Ports[k]
		if !ok || e.Canonical() != oe.Canonical() {
			return false
		}
	}
	for k, a := range r.Attachments {
		oa, ok := other.Attachments[k]
		if !ok || !a.Equal(oa) {
			return false
		}
	}

	return true
}

// Clone returns an independent deep copy of the attachment.
func (a Attachment) Clone() Attachment {
	cp := a
	cp.Addresses = slices.Clone(a.Addresses)
	if a.VLAN != nil {
		v := *a.VLAN
		cp.VLAN = &v
	}

	return cp
}

// Equal reports whether two attachments are identical.
func (a Attachment) Equal(other Attachment) bool {
	if a.Port != other.Port {
		return false
	}
	if (a.VLAN == nil) != (other.VLAN == nil) {
		return false
	}
	if a.VLAN != nil && *a.VLAN != *other.VLAN {
		return false
	}

	return slices.Equal(a.Addresses, other.Addresses)
}

// Validate checks that every attachment names a declared port with a valid VLAN
// and at least one address, and that no two attachments share a port and tag form, which would
// leave a frame on that port unable to tell which attachment it belongs to. Attachments are
// walked in sorted name order, so the field path a rejection names is the same on every run.
func (r Config) Validate(field string) error {
	for _, name := range slices.Sorted(maps.Keys(r.Ports)) {
		if err := cablemodel.ValidateEthernet(field+".ports."+name, r.Ports[name]); err != nil {
			return errs.Wrapf(err, "reflector %q port %q", strings.TrimPrefix(field, "reflectors."), name)
		}
	}
	type portVLAN struct {
		port string
		vlan vlan.ID
	}
	seen := make(map[portVLAN]string, len(r.Attachments))

	names := make([]string, 0, len(r.Attachments))
	for name := range r.Attachments {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		a := r.Attachments[name]
		afield := field + ".attachments." + name

		if _, ok := r.Ports[a.Port]; !ok {
			return errs.New().
				Attr("field", afield+".port").
				Attr("port", a.Port).
				Msgf("attachment %q names port %q, which the reflector does not have", name, a.Port)
		}
		if a.VLAN != nil && !a.VLAN.Valid() {
			return errs.New().
				Attr("field", afield+".vlan").
				Attr("vlan", *a.VLAN).
				Msgf("attachment %q has invalid VLAN ID %d", name, *a.VLAN)
		}
		if len(a.Addresses) == 0 {
			return errs.New().
				Attr("field", afield+".addresses").
				Msgf("attachment %q has no address to originate a copy from", name)
		}

		for i, prefix := range a.Addresses {
			if !prefix.IsValid() {
				return errs.New().Attr("field", afield+".addresses").Attr("index", i).Msg("attachment address prefix is invalid")
			}
		}
		key := portVLAN{port: a.Port}
		if a.VLAN != nil {
			key.vlan = *a.VLAN
		}
		if other, dup := seen[key]; dup {
			return errs.New().
				Attr("field", afield+".vlan").
				Attr("port", a.Port).
				Attr("vlan", key.vlan).
				Attr("duplicates", field+".attachments."+other).
				Msgf("attachment %q and %q share port %q and VLAN %d", other, name, a.Port, key.vlan)
		}
		seen[key] = name
	}

	return nil
}
