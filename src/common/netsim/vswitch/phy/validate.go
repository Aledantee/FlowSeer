package phy

import (
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Validate checks the configuration against the port table: every named port
// must exist and must not be a LAG, since a LAG has no physical layer of its
// own; every PSE port's group must be a known group; no class may exceed 8;
// and a configured speed must be one the port supports.
func (c Config) Validate(ports port.Table) error {
	for _, name := range sortedKeys(c.Ethernet) {
		e := c.Ethernet[name]
		p, ok := ports.Port(name)
		if !ok {
			return errs.New().Attr("port", name).Msgf("ethernet entry names unknown port %q", name)
		}
		if p.Kind == port.Lag {
			return errs.New().Attr("port", name).Msgf("ethernet entry names LAG %q", name)
		}
		fixed := e.Setting != nil && !e.Setting.AutoNegotiation && e.Setting.SpeedBPS != 0
		if fixed && !slices.Contains(e.SupportedSpeedsBPS, e.Setting.SpeedBPS) {
			return errs.New().
				Attr("port", name).
				Attr("speed_bps", e.Setting.SpeedBPS).
				Msgf("port %q speed %d is outside its supported set", name, e.Setting.SpeedBPS)
		}
	}

	if c.PoE == nil {
		return nil
	}
	for _, name := range sortedKeys(c.PoE.Ports) {
		pp := c.PoE.Ports[name]
		p, ok := ports.Port(name)
		if !ok {
			return errs.New().Attr("port", name).Msgf("poe entry names unknown port %q", name)
		}
		if p.Kind == port.Lag {
			return errs.New().Attr("port", name).Msgf("poe entry names LAG %q", name)
		}
		if _, ok := c.PoE.Groups[pp.Group]; !ok {
			return errs.New().
				Attr("port", name).
				Attr("group", pp.Group).
				Msgf("port %q names unknown pse group %q", name, pp.Group)
		}
		if pp.PDClass != nil && *pp.PDClass > maxClass {
			return errs.New().
				Attr("port", name).
				Attr("class", *pp.PDClass).
				Msgf("port %q pd class %d exceeds %d", name, *pp.PDClass, maxClass)
		}
		if pp.MaxClass > maxClass {
			return errs.New().
				Attr("port", name).
				Attr("class", pp.MaxClass).
				Msgf("port %q maximum class %d exceeds %d", name, pp.MaxClass, maxClass)
		}
	}

	return nil
}
