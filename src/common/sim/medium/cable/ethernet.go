package cable

import (
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// PhyAssumption is the physical profile a fabric assumes for facts no source reported. On each link it
// fills an end's empty SupportedSpeedsBPS, unknown AutoNegotiationSupported, and nil Setting from Ethernet,
// and an unspecified cable Medium from Medium; a stated fact is never replaced, and an unspecified Medium or
// an unreported Ethernet field fills nothing. Ethernet follows a switch port's validation rules and must not
// carry Observed, since an observation cannot be assumed. The filled values show in the resolved ends, and
// the resolution carries one assumption per link it filled, naming the facts. PhyAssumption values
// are safe for concurrent reads but not for concurrent mutation.
type PhyAssumption struct {
	Medium   Medium
	Ethernet phy.Ethernet
}

// Clone returns an independent deep copy of the assumption, or nil for nil.
func (a *PhyAssumption) Clone() *PhyAssumption {
	if a == nil {
		return nil
	}

	return &PhyAssumption{Medium: a.Medium, Ethernet: a.Ethernet.Clone()}
}

// Equal reports whether two assumptions are both nil or hold the same medium and Ethernet facts.
func (a *PhyAssumption) Equal(other *PhyAssumption) bool {
	if a == nil || other == nil {
		return a == other
	}

	return a.Medium == other.Medium && a.Ethernet.Canonical() == other.Ethernet.Canonical()
}

// fill returns eth with the facts its source did not report taken from the
// assumption, appending the name of each filled fact under label.
func (a *PhyAssumption) fill(eth phy.Ethernet, label string, filled *[]string) phy.Ethernet {
	eth = eth.Clone()
	if len(eth.SupportedSpeedsBPS) == 0 && len(a.Ethernet.SupportedSpeedsBPS) > 0 {
		eth.SupportedSpeedsBPS = slices.Clone(a.Ethernet.SupportedSpeedsBPS)
		*filled = append(*filled, label+" supported_speeds_bps")
	}
	if eth.AutoNegotiationSupported == phy.CapabilityUnknown && a.Ethernet.AutoNegotiationSupported != phy.CapabilityUnknown {
		eth.AutoNegotiationSupported = a.Ethernet.AutoNegotiationSupported
		*filled = append(*filled, label+" auto_negotiation_supported")
	}
	if eth.Setting == nil && a.Ethernet.Setting != nil {
		eth.Setting = a.Ethernet.Setting.Clone()
		*filled = append(*filled, label+" setting")
	}

	return eth
}

// Validate rejects an assumed observation and applies the same Ethernet rules as a port.
func (a *PhyAssumption) Validate() error {
	if a == nil {
		return nil
	}
	if err := ValidateMedium("phy_assumption.medium", a.Medium); err != nil {
		return err
	}
	if a.Ethernet.Observed != nil {
		return errs.New().
			Attr("field", "phy_assumption.ethernet.observed").
			Msg("a physical assumption cannot carry an observation")
	}

	return ValidateEthernet("phy_assumption.ethernet", a.Ethernet)
}

// ValidateMedium rejects a medium outside the modeled set, attributing the error to field.
func ValidateMedium(field string, m Medium) error {
	switch m {
	case MediumUnspecified, TwistedPair, MultimodeFiber, SinglemodeFiber, Twinax:
		return nil
	default:
		return errs.New().Attr("field", field).Attr("medium", m).Msg("unknown cable medium")
	}
}

// ValidateEthernet checks facts that belong to no switch under the rules phy
// applies to a switch port's, reporting phy's field path beneath field.
func ValidateEthernet(field string, e phy.Ethernet) error {
	const name = "end"
	// A table of one physical port always builds.
	tbl, _ := port.NewBuilder().
		Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	err := phy.Config{Ethernet: map[string]phy.Ethernet{name: e}}.Validate(layer.Env{Ports: tbl})
	if err == nil {
		return nil
	}
	inner, _ := errs.Attributes(err)["field"].(string)

	return errs.From(err).
		Attr("field", field+strings.TrimPrefix(inner, "ethernet."+name)).
		Msg("invalid ethernet facts")
}

// NormalizeEthernet applies the normalization phy applies to a switch port's facts.
func NormalizeEthernet(e phy.Ethernet) phy.Ethernet {
	return phy.Config{Ethernet: map[string]phy.Ethernet{"": e}}.Normalize(layer.Env{}).Ethernet[""]
}
