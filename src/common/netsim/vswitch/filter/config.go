// Package filter implements interface-bound packet filtering and stateful
// inspection for the virtual switch.
package filter

import (
	"cmp"
	"net/netip"
	"slices"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/tcp"
)

// Direction indicates whether a filter binding evaluates traffic entering
// (In) or leaving (Out) an interface.
type Direction string

const (
	// In identifies an ingress filter binding.
	In Direction = "in"
	// Out identifies an egress filter binding.
	Out Direction = "out"
)

// Action is the filtering verdict applied to a matching frame.
type Action string

const (
	// Accept permits the frame to continue along its forwarding path.
	Accept Action = "accept"
	// Drop silently discards the frame.
	Drop Action = "drop"
	// Reject discards the frame with an explicit rejection reason.
	Reject Action = "reject"
)

// PortRange specifies an inclusive range of transport-layer ports [Start, End].
type PortRange struct {
	Start uint16
	End   uint16
}

// Contains reports whether port falls within the inclusive range.
func (r PortRange) Contains(port uint16) bool {
	return port >= r.Start && port <= r.End
}

// ICMPMatch constrains an ICMP message by type and optional code.
type ICMPMatch struct {
	Type uint8
	Code *uint8
}

// Matches reports whether t and code satisfy the ICMP constraint.
func (m *ICMPMatch) Matches(t uint8, code uint8) bool {
	if m == nil {
		return true
	}
	if m.Type != t {
		return false
	}
	if m.Code != nil && *m.Code != code {
		return false
	}
	return true
}

// FlagMatch checks TCP control bits against an expected mask and value.
type FlagMatch struct {
	Mask  tcp.Flags
	Value tcp.Flags
}

// Matches reports whether flags satisfies the mask and value constraint.
func (m *FlagMatch) Matches(flags tcp.Flags) bool {
	if m == nil {
		return true
	}
	return (flags & m.Mask) == m.Value
}

// Match defines the packet criteria for a filter rule. Omitted or empty fields
// match any value.
type Match struct {
	Protocol *uint8
	Src      []netip.Prefix
	Dst      []netip.Prefix
	SrcPorts []PortRange
	DstPorts []PortRange
	ICMP     *ICMPMatch
	TCPFlags *FlagMatch
}

// Rule pairs a match specification with an action.
type Rule struct {
	Name   string
	Match  Match
	Action Action
}

// RuleSet is an ordered sequence of rules with a default action.
type RuleSet struct {
	Stateful bool
	Default  Action
	Rules    []Rule
}

// Binding attaches a named rule set to an interface in a given direction.
type Binding struct {
	Interface string
	Direction Direction
	Set       string
}

// Config specifies the virtual switch's filter sets and interface bindings.
type Config struct {
	Sets     map[string]RuleSet
	Bindings []Binding
}

// Normalize returns a deep copy of c with canonical slice order and non-nil
// maps. Rule order inside a set is preserved because first-match evaluation
// is order-dependent.
func (c Config) Normalize() Config {
	out := Config{
		Sets:     make(map[string]RuleSet, len(c.Sets)),
		Bindings: make([]Binding, len(c.Bindings)),
	}

	for setName, set := range c.Sets {
		normSet := RuleSet{
			Stateful: set.Stateful,
			Default:  set.Default,
			Rules:    make([]Rule, len(set.Rules)),
		}
		for i, r := range set.Rules {
			normRule := Rule{
				Name:   r.Name,
				Action: r.Action,
				Match: Match{
					Protocol: r.Match.Protocol,
					Src:      slices.Clone(r.Match.Src),
					Dst:      slices.Clone(r.Match.Dst),
					SrcPorts: slices.Clone(r.Match.SrcPorts),
					DstPorts: slices.Clone(r.Match.DstPorts),
				},
			}
			if r.Match.ICMP != nil {
				var codeCopy *uint8
				if r.Match.ICMP.Code != nil {
					val := *r.Match.ICMP.Code
					codeCopy = &val
				}
				normRule.Match.ICMP = &ICMPMatch{
					Type: r.Match.ICMP.Type,
					Code: codeCopy,
				}
			}
			if r.Match.TCPFlags != nil {
				normRule.Match.TCPFlags = &FlagMatch{
					Mask:  r.Match.TCPFlags.Mask,
					Value: r.Match.TCPFlags.Value,
				}
			}
			normSet.Rules[i] = normRule
		}
		out.Sets[setName] = normSet
	}

	copy(out.Bindings, c.Bindings)
	slices.SortFunc(out.Bindings, func(a, b Binding) int {
		if c := cmp.Compare(a.Interface, b.Interface); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Direction, b.Direction); c != 0 {
			return c
		}
		return cmp.Compare(a.Set, b.Set)
	})

	return out
}

// Clone returns an independent deep copy of c.
func (c Config) Clone() Config {
	return c.Normalize()
}

// Validate checks internal consistency of the configuration.
func (c Config) Validate() error {
	for name, set := range c.Sets {
		if name == "" {
			return errs.New().Attr("field", "sets").Msg("rule set name cannot be empty")
		}
		switch set.Default {
		case Accept, Drop, Reject:
		default:
			return errs.New().
				Attr("set", name).
				Attr("default", string(set.Default)).
				Msgf("rule set %q has invalid default action %q", name, set.Default)
		}

		for i, r := range set.Rules {
			switch r.Action {
			case Accept, Drop, Reject:
			default:
				return errs.New().
					Attr("set", name).
					Attr("rule_index", i).
					Attr("action", string(r.Action)).
					Msgf("rule %d in set %q has invalid action %q", i, name, r.Action)
			}

			for _, p := range r.Match.Src {
				if !p.IsValid() {
					return errs.New().
						Attr("set", name).
						Attr("rule_index", i).
						Msgf("rule %d in set %q has invalid source prefix", i, name)
				}
			}
			for _, p := range r.Match.Dst {
				if !p.IsValid() {
					return errs.New().
						Attr("set", name).
						Attr("rule_index", i).
						Msgf("rule %d in set %q has invalid destination prefix", i, name)
				}
			}
			for _, pr := range r.Match.SrcPorts {
				if pr.Start > pr.End {
					return errs.New().
						Attr("set", name).
						Attr("rule_index", i).
						Msgf("rule %d in set %q has invalid source port range [%d, %d]", i, name, pr.Start, pr.End)
				}
			}
			for _, pr := range r.Match.DstPorts {
				if pr.Start > pr.End {
					return errs.New().
						Attr("set", name).
						Attr("rule_index", i).
						Msgf("rule %d in set %q has invalid destination port range [%d, %d]", i, name, pr.Start, pr.End)
				}
			}
			if r.Match.TCPFlags != nil && r.Match.TCPFlags.Mask&r.Match.TCPFlags.Value != r.Match.TCPFlags.Value {
				return errs.New().
					Attr("set", name).
					Attr("rule_index", i).
					Msgf("rule %d in set %q has TCP flags value outside mask", i, name)
			}
		}
	}

	type bindingKey struct {
		iface string
		dir   Direction
	}
	seenBindings := make(map[bindingKey]string, len(c.Bindings))

	for i, b := range c.Bindings {
		if b.Interface == "" {
			return errs.New().
				Attr("binding_index", i).
				Msgf("binding %d has empty interface", i)
		}
		if b.Direction != In && b.Direction != Out {
			return errs.New().
				Attr("binding_index", i).
				Attr("direction", string(b.Direction)).
				Msgf("binding %d has invalid direction %q", i, b.Direction)
		}
		if _, ok := c.Sets[b.Set]; !ok {
			return errs.New().
				Attr("binding_index", i).
				Attr("set", b.Set).
				Msgf("binding %d references unknown rule set %q", i, b.Set)
		}
		k := bindingKey{iface: b.Interface, dir: b.Direction}
		if prev, ok := seenBindings[k]; ok {
			return errs.New().
				Attr("interface", b.Interface).
				Attr("direction", string(b.Direction)).
				Attr("set", b.Set).
				Attr("previous_set", prev).
				Msgf("duplicate binding for %s %s (sets %q and %q)", b.Interface, b.Direction, prev, b.Set)
		}
		seenBindings[k] = b.Set
	}

	return nil
}

// Equal reports whether c and other are semantically identical.
func (c Config) Equal(other Config) bool {
	normA := c.Normalize()
	normB := other.Normalize()

	if len(normA.Sets) != len(normB.Sets) || len(normA.Bindings) != len(normB.Bindings) {
		return false
	}

	for k, setA := range normA.Sets {
		setB, ok := normB.Sets[k]
		if !ok || setA.Stateful != setB.Stateful || setA.Default != setB.Default || len(setA.Rules) != len(setB.Rules) {
			return false
		}
		for i := range setA.Rules {
			if !setA.Rules[i].Equal(setB.Rules[i]) {
				return false
			}
		}
	}

	for i := range normA.Bindings {
		if normA.Bindings[i] != normB.Bindings[i] {
			return false
		}
	}

	return true
}

// Equal reports whether r and other are identical.
func (r Rule) Equal(other Rule) bool {
	if r.Name != other.Name || r.Action != other.Action {
		return false
	}
	return r.Match.Equal(other.Match)
}

// Equal reports whether m and other match identical packet criteria.
func (m Match) Equal(other Match) bool {
	if (m.Protocol == nil) != (other.Protocol == nil) {
		return false
	}
	if m.Protocol != nil && *m.Protocol != *other.Protocol {
		return false
	}
	if !slices.Equal(m.Src, other.Src) || !slices.Equal(m.Dst, other.Dst) {
		return false
	}
	if !slices.Equal(m.SrcPorts, other.SrcPorts) || !slices.Equal(m.DstPorts, other.DstPorts) {
		return false
	}
	if (m.ICMP == nil) != (other.ICMP == nil) {
		return false
	}
	if m.ICMP != nil {
		if m.ICMP.Type != other.ICMP.Type {
			return false
		}
		if (m.ICMP.Code == nil) != (other.ICMP.Code == nil) {
			return false
		}
		if m.ICMP.Code != nil && *m.ICMP.Code != *other.ICMP.Code {
			return false
		}
	}
	if (m.TCPFlags == nil) != (other.TCPFlags == nil) {
		return false
	}
	if m.TCPFlags != nil && *m.TCPFlags != *other.TCPFlags {
		return false
	}
	return true
}
