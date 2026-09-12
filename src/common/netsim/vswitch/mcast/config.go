package mcast

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
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

// Normalize returns an independent copy of the configuration with standard defaults applied.
// Unspecified FloodUnregistered defaults to true, zero intervals default to [DefaultMembershipInterval],
// and router ports are sorted and deduplicated.
func (c Config) Normalize() Config {
	if c.VLANs == nil {
		return Config{}
	}

	norm := Config{VLANs: make(map[vlan.ID]VLANSnooping, len(c.VLANs))}
	for _, vid := range sortedVLANIDs(c.VLANs) {
		cfg := c.VLANs[vid]
		flood := true
		if cfg.FloodUnregistered != nil {
			flood = *cfg.FloodUnregistered
		}
		cfg.FloodUnregistered = &flood

		if cfg.MembershipInterval == 0 {
			cfg.MembershipInterval = DefaultMembershipInterval
		}
		if cfg.RouterPortInterval == 0 {
			cfg.RouterPortInterval = DefaultMembershipInterval
		}

		if len(cfg.RouterPorts) > 0 {
			rports := slices.Clone(cfg.RouterPorts)
			slices.Sort(rports)
			cfg.RouterPorts = slices.Compact(rports)
		} else {
			cfg.RouterPorts = nil
		}

		norm.VLANs[vid] = cfg
	}

	return norm
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

// Canonical returns a deterministic representation of the VLAN snooping configuration.
func (v VLANSnooping) Canonical() string {
	flood := true
	if v.FloodUnregistered != nil {
		flood = *v.FloodUnregistered
	}
	ports := slices.Clone(v.RouterPorts)
	slices.Sort(ports)
	var encodedPorts strings.Builder
	for i, portName := range ports {
		if i > 0 {
			encodedPorts.WriteByte(',')
		}
		encodedPorts.WriteString(strconv.Quote(portName))
	}

	return fmt.Sprintf("flood=%t,fast_leave=%t,router_ports=[%s],mem_int=%s,rtr_int=%s",
		flood, v.FastLeave, encodedPorts.String(), v.membershipInterval(), v.routerPortInterval())
}

// Validate rejects invalid VLANs, physical LAG members used as router ports,
// missing router ports, and negative aging intervals.
func (c Config) Validate(ports port.Table) error {
	for _, vid := range sortedVLANIDs(c.VLANs) {
		cfg := c.VLANs[vid]
		if !vid.Valid() {
			return errs.New().
				Attr("field", fmt.Sprintf("vlans.%d", vid)).
				Attr("vlan", vid).
				Msgf("VLAN %d is outside the assignable range", vid)
		}
		if cfg.MembershipInterval < 0 {
			return errs.New().
				Attr("field", fmt.Sprintf("vlans.%d.membership_interval", vid)).
				Attr("vlan", vid).
				Attr("membership_interval", cfg.MembershipInterval).
				Msg("membership interval cannot be negative")
		}
		if cfg.RouterPortInterval < 0 {
			return errs.New().
				Attr("field", fmt.Sprintf("vlans.%d.router_port_interval", vid)).
				Attr("vlan", vid).
				Attr("router_port_interval", cfg.RouterPortInterval).
				Msg("router port interval cannot be negative")
		}

		for _, name := range cfg.RouterPorts {
			if name == "" {
				return errs.New().
					Attr("field", fmt.Sprintf("vlans.%d.router_ports", vid)).
					Attr("vlan", vid).
					Msg("router port name cannot be empty")
			}
			p, ok := ports.Port(name)
			if !ok {
				return errs.New().
					Attr("field", fmt.Sprintf("vlans.%d.router_ports", vid)).
					Attr("vlan", vid).
					Attr("port", name).
					Msgf("router port %q absent from port table", name)
			}
			if p.LagParent != "" {
				return errs.New().
					Attr("field", fmt.Sprintf("vlans.%d.router_ports", vid)).
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

func (v VLANSnooping) membershipInterval() time.Duration {
	if v.MembershipInterval == 0 {
		return DefaultMembershipInterval
	}

	return v.MembershipInterval
}

func (v VLANSnooping) routerPortInterval() time.Duration {
	if v.RouterPortInterval == 0 {
		return DefaultMembershipInterval
	}

	return v.RouterPortInterval
}

func sortedVLANIDs[V any](values map[vlan.ID]V) []vlan.ID {
	ids := make([]vlan.ID, 0, len(values))
	for vid := range values {
		ids = append(ids, vid)
	}
	slices.Sort(ids)

	return ids
}
