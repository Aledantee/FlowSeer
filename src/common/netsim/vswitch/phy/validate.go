package phy

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Validate checks the configuration against the port table: every named port
// must exist and must not be a LAG, since a LAG has no physical layer of its
// own; every PSE port's group must be a known group; no class may exceed 8;
// enums must be in their declared domains; and a configured speed must be one
// the port supports.
func (c Config) Validate(ports port.Table) error {
	for _, name := range sortedKeys(c.Ethernet) {
		e := c.Ethernet[name]
		p, ok := ports.Port(name)
		if !ok {
			return errs.New().
				Attr("field", "ethernet."+name).
				Attr("port", name).
				Msgf("ethernet entry names unknown port %q", name)
		}
		if p.Kind == port.Lag {
			return errs.New().
				Attr("field", "ethernet."+name).
				Attr("port", name).
				Msgf("ethernet entry names LAG %q", name)
		}

		switch e.AutoNegotiationSupported {
		case CapabilityUnknown, CapabilitySupported, CapabilityUnsupported:
		default:
			return errs.New().
				Attr("field", "ethernet."+name+".auto_negotiation_supported").
				Attr("port", name).
				Attr("capability", e.AutoNegotiationSupported).
				Msgf("port %q has unknown capability %q", name, e.AutoNegotiationSupported)
		}

		if e.Setting != nil {
			switch e.Setting.Duplex {
			case Full, Half, Unknown, "":
			default:
				return errs.New().
					Attr("field", "ethernet."+name+".duplex").
					Attr("port", name).
					Attr("duplex", e.Setting.Duplex).
					Msgf("port %q has unknown setting duplex %q", name, e.Setting.Duplex)
			}

			if e.Setting.AutoNegotiation && e.AutoNegotiationSupported == CapabilityUnsupported {
				return errs.New().
					Attr("field", "ethernet."+name+".auto_negotiation").
					Attr("port", name).
					Msgf("port %q has auto-negotiation enabled but auto-negotiation is not supported", name)
			}
		}

		if e.Observed != nil {
			switch e.Observed.Duplex {
			case Full, Half, Unknown, "":
			default:
				return errs.New().
					Attr("field", "ethernet."+name+".observed.duplex").
					Attr("port", name).
					Attr("duplex", e.Observed.Duplex).
					Msgf("port %q has unknown observed duplex %q", name, e.Observed.Duplex)
			}
		}

		fixed := e.Setting != nil && !e.Setting.AutoNegotiation && e.Setting.SpeedBPS != 0
		if fixed && !slices.Contains(e.SupportedSpeedsBPS, e.Setting.SpeedBPS) {
			return errs.New().
				Attr("field", "ethernet."+name+".speed_bps").
				Attr("port", name).
				Attr("speed_bps", e.Setting.SpeedBPS).
				Msgf("port %q speed %d is outside its supported set", name, e.Setting.SpeedBPS)
		}
	}

	if c.PoE == nil {
		return nil
	}

	for _, gname := range sortedKeys(c.PoE.Groups) {
		if gname == "" {
			return errs.New().
				Attr("field", "poe.groups").
				Msg("pse group name cannot be empty")
		}
	}

	for _, name := range sortedKeys(c.PoE.Ports) {
		pp := c.PoE.Ports[name]
		p, ok := ports.Port(name)
		if !ok {
			return errs.New().
				Attr("field", "poe.ports."+name).
				Attr("port", name).
				Msgf("poe entry names unknown port %q", name)
		}
		if p.Kind == port.Lag {
			return errs.New().
				Attr("field", "poe.ports."+name).
				Attr("port", name).
				Msgf("poe entry names LAG %q", name)
		}
		if _, ok := c.PoE.Groups[pp.Group]; !ok {
			return errs.New().
				Attr("field", "poe.ports."+name+".group").
				Attr("port", name).
				Attr("group", pp.Group).
				Msgf("port %q names unknown pse group %q", name, pp.Group)
		}
		switch pp.Priority {
		case PriorityCritical, PriorityHigh, PriorityLow, "":
		default:
			return errs.New().
				Attr("field", "poe.ports."+name+".priority").
				Attr("port", name).
				Attr("priority", pp.Priority).
				Msgf("port %q has unknown priority %q", name, pp.Priority)
		}
		switch pp.PD {
		case PDUnknown, PDAbsent, PDAttached:
		default:
			return errs.New().
				Attr("field", "poe.ports."+name+".pd").
				Attr("port", name).
				Attr("pd", pp.PD).
				Msgf("port %q has unknown pd state %q", name, pp.PD)
		}
		if pp.PDClass != nil && pp.PD != PDAttached {
			return errs.New().
				Attr("field", "poe.ports."+name+".pd_class").
				Attr("port", name).
				Attr("class", *pp.PDClass).
				Msgf("port %q has pd class %d without attached powered device", name, *pp.PDClass)
		}
		if pp.PDClass != nil && *pp.PDClass > maxClass {
			return errs.New().
				Attr("field", "poe.ports."+name+".pd_class").
				Attr("port", name).
				Attr("class", *pp.PDClass).
				Msgf("port %q pd class %d exceeds %d", name, *pp.PDClass, maxClass)
		}
		if pp.MaxClass > maxClass {
			return errs.New().
				Attr("field", "poe.ports."+name+".max_class").
				Attr("port", name).
				Attr("class", pp.MaxClass).
				Msgf("port %q maximum class %d exceeds %d", name, pp.MaxClass, maxClass)
		}
	}

	return nil
}
