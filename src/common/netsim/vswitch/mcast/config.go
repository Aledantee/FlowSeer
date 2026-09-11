package mcast

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// DefaultMembershipInterval is the membership and router-port lifetime used when an interval is unset.
const DefaultMembershipInterval = 260 * time.Second

// Config selects the VLANs whose multicast membership is snooped.
type Config struct {
	VLANs map[vlan.ID]VLANSnooping
}

// VLANSnooping controls membership and router-port learning for one VLAN.
// Zero intervals use [DefaultMembershipInterval], and a nil FloodUnregistered enables flooding.
type VLANSnooping struct {
	FloodUnregistered  *bool
	FastLeave          bool
	RouterPorts        []string
	MembershipInterval time.Duration
	RouterPortInterval time.Duration
}

// Validate rejects invalid VLANs, physical LAG members used as router ports,
// missing router ports, and negative aging intervals.
func (c Config) Validate(ports port.Table) error {
	for _, vid := range sortedVLANIDs(c.VLANs) {
		cfg := c.VLANs[vid]
		if !vid.Valid() {
			return errs.New().
				Attr("vlan", vid).
				Msgf("VLAN %d is outside the assignable range", vid)
		}
		if cfg.MembershipInterval < 0 {
			return errs.New().
				Attr("vlan", vid).
				Attr("membership_interval", cfg.MembershipInterval).
				Msg("membership interval cannot be negative")
		}
		if cfg.RouterPortInterval < 0 {
			return errs.New().
				Attr("vlan", vid).
				Attr("router_port_interval", cfg.RouterPortInterval).
				Msg("router port interval cannot be negative")
		}

		for _, name := range cfg.RouterPorts {
			p, ok := ports.Port(name)
			if !ok {
				return errs.New().
					Attr("vlan", vid).
					Attr("port", name).
					Msgf("router port %q absent from port table", name)
			}
			if p.LagParent != "" {
				return errs.New().
					Attr("vlan", vid).
					Attr("port", name).
					Attr("lag", p.LagParent).
					Msgf("router port %q is a physical LAG member", name)
			}
		}
	}

	return nil
}

// Clone returns an independent deep copy of c.
func (c Config) Clone() Config {
	if c.VLANs == nil {
		return Config{}
	}

	cp := Config{VLANs: make(map[vlan.ID]VLANSnooping, len(c.VLANs))}
	for vid, cfg := range c.VLANs {
		cfg.RouterPorts = slices.Clone(cfg.RouterPorts)
		if cfg.FloodUnregistered != nil {
			flood := *cfg.FloodUnregistered
			cfg.FloodUnregistered = &flood
		}
		cp.VLANs[vid] = cfg
	}

	return cp
}

// Floods reports whether unregistered multicast groups flood on vid.
// Unsnooped VLANs and snooped VLANs with no explicit option both flood.
func (c Config) Floods(vid vlan.ID) bool {
	cfg, ok := c.VLANs[vid]
	if !ok || cfg.FloodUnregistered == nil {
		return true
	}

	return *cfg.FloodUnregistered
}

func (c VLANSnooping) membershipInterval() time.Duration {
	if c.MembershipInterval == 0 {
		return DefaultMembershipInterval
	}

	return c.MembershipInterval
}

func (c VLANSnooping) routerPortInterval() time.Duration {
	if c.RouterPortInterval == 0 {
		return DefaultMembershipInterval
	}

	return c.RouterPortInterval
}

func sortedVLANIDs[V any](values map[vlan.ID]V) []vlan.ID {
	ids := make([]vlan.ID, 0, len(values))
	for vid := range values {
		ids = append(ids, vid)
	}
	slices.Sort(ids)

	return ids
}
