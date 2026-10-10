package netmodel

import (
	"fmt"
	"slices"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Issue codes used by netmodel.
const (
	IssueMissingFilterSet       analysis.IssueCode = "netmodel.filter.missing_set"
	IssueUnboundFilterInterface analysis.IssueCode = "netmodel.filter.unbound_interface"
)

func (c *loadContext) reportUnwantedFilter(ifaces []*interfacev1.Interface, filterSets []*filterv1.FilterRuleSet) {
	if !c.isWanted(filter.LayerName) {
		for _, iface := range ifaces {
			if iface.GetFilter() != nil {
				scope := routing.OwnershipScope(c.src.DeviceID, routing.DefaultVRF, iface.GetName())
				c.addSkippedAt(scope, iface.GetName(), "filter", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
			}
		}
		for _, set := range filterSets {
			if set == nil {
				continue
			}
			c.addSkippedAt(c.rootScope, "", "filter_rule_set", "layer not wanted", analysis.Incomplete, IssueSkippedLayerNotWanted)
		}
	}
}

func (c *loadContext) loadFilter(ifaces []*interfacev1.Interface, filterSets []*filterv1.FilterRuleSet, ownership loadOwnership, routingAvailable bool) *filter.Config {
	if !c.isWanted(filter.LayerName) {
		return nil
	}
	setByName, unevaluable := c.loadFilterSets(filterSets)
	bindings := c.loadFilterBindings(ifaces, ownership, setByName, unevaluable)
	if routingAvailable && (len(setByName) > 0 || len(bindings) > 0) {
		return &filter.Config{
			Sets:     setByName,
			Bindings: bindings,
		}
	}
	c.report.Capabilities = slices.DeleteFunc(c.report.Capabilities, func(l trace.Layer) bool {
		return l == filter.LayerName
	})
	delete(c.report.CapabilitySources, filter.LayerName)

	return nil
}

func (c *loadContext) loadFilterSets(filterSets []*filterv1.FilterRuleSet) (map[string]filter.RuleSet, map[string]bool) {
	setByName := make(map[string]filter.RuleSet, len(filterSets))
	// A set the IP-layer filter cannot evaluate is left out whole:
	// translating a rule without its unsupported terms would widen
	// it, and dropping the rule would let a later rule decide.
	unevaluable := make(map[string]bool)
	for _, set := range filterSets {
		if set == nil {
			continue
		}
		name := set.GetName()
		if name == "" {
			continue
		}
		if slices.ContainsFunc(set.GetRules(), hasUnevaluableMatch) {
			unevaluable[name] = true
			c.addSkippedAt(c.rootScope, "", "filter", fmt.Sprintf("rule set %q carries unsupported match terms or IPv4-mapped prefixes", name), analysis.Incomplete, IssueSkippedUnsupportedFacet)
			continue
		}
		ruleSet := filter.RuleSet{
			Stateful: set.GetStateful(),
			Default:  translateAction(set.GetDefault()),
			Rules:    make([]filter.Rule, 0, len(set.GetRules())),
		}
		for _, rule := range set.GetRules() {
			if rule == nil {
				continue
			}
			ruleSet.Rules = append(ruleSet.Rules, c.loadFilterRule(rule))
		}
		setByName[name] = ruleSet
	}

	return setByName, unevaluable
}

func (c *loadContext) loadFilterRule(rule *filterv1.FilterRule) filter.Rule {
	r := filter.Rule{
		Name:   rule.GetName(),
		Action: translateAction(rule.GetAction()),
	}
	if m := rule.GetMatch(); m != nil {
		if m.HasProtocol() {
			protoNum := uint8(m.GetProtocol())
			r.Match.Protocol = &protoNum
		}
		for _, p := range m.GetSrcPrefixes() {
			if prefix, ok := parseIPPrefix(p); ok {
				r.Match.Src = append(r.Match.Src, prefix)
			}
		}
		for _, p := range m.GetDstPrefixes() {
			if prefix, ok := parseIPPrefix(p); ok {
				r.Match.Dst = append(r.Match.Dst, prefix)
			}
		}
		for _, portMatch := range m.GetSrcPorts() {
			if pr, ok := parsePortMatch(portMatch); ok {
				r.Match.SrcPorts = append(r.Match.SrcPorts, pr)
			}
		}
		for _, portMatch := range m.GetDstPorts() {
			if pr, ok := parsePortMatch(portMatch); ok {
				r.Match.DstPorts = append(r.Match.DstPorts, pr)
			}
		}
		if icmp := m.GetIcmp(); icmp != nil {
			if v4 := icmp.GetV4(); v4 != nil {
				if len(v4.GetTypes()) > 1 || len(v4.GetCodes()) > 1 {
					c.addSkippedAt(c.rootScope, "", "filter_icmp_match", "ICMPv4 lists carry more than one type or code; only the first entry of each is translated", analysis.Incomplete, IssueSkippedUnsupportedFacet)
				}
				if len(v4.GetTypes()) > 0 || len(v4.GetCodes()) > 0 {
					im := &filter.ICMPMatch{}
					if len(v4.GetTypes()) > 0 {
						im.Type = uint8(v4.GetTypes()[0])
					}
					if len(v4.GetCodes()) > 0 {
						c := uint8(v4.GetCodes()[0])
						im.Code = &c
					}
					r.Match.ICMP = im
				}
			} else if v6 := icmp.GetV6(); v6 != nil {
				if len(v6.GetTypes()) > 1 || len(v6.GetCodes()) > 1 {
					c.addSkippedAt(c.rootScope, "", "filter_icmp_match", "ICMPv6 lists carry more than one type or code; only the first entry of each is translated", analysis.Incomplete, IssueSkippedUnsupportedFacet)
				}
				if len(v6.GetTypes()) > 0 || len(v6.GetCodes()) > 0 {
					im := &filter.ICMPMatch{}
					if len(v6.GetTypes()) > 0 {
						im.Type = uint8(v6.GetTypes()[0])
					}
					if len(v6.GetCodes()) > 0 {
						c := uint8(v6.GetCodes()[0])
						im.Code = &c
					}
					r.Match.ICMP = im
				}
			}
		}
		if tf := m.GetTcpFlags(); tf != nil {
			if len(tf.GetRequiredSet()) > 0 || len(tf.GetRequiredClear()) > 0 {
				var mask, value tcp.Flags
				for _, f := range tf.GetRequiredSet() {
					mask |= tcp.Flags(f)
					value |= tcp.Flags(f)
				}
				for _, f := range tf.GetRequiredClear() {
					mask |= tcp.Flags(f)
				}
				r.Match.TCPFlags = &filter.FlagMatch{Mask: mask, Value: value}
			}
		}
	}

	return r
}

func (c *loadContext) loadFilterBindings(ifaces []*interfacev1.Interface, ownership loadOwnership, setByName map[string]filter.RuleSet, unevaluable map[string]bool) []filter.Binding {
	var bindings []filter.Binding
	for _, iface := range ifaces {
		facet := iface.GetFilter()
		if facet == nil {
			continue
		}
		inSet := facet.GetInSet()
		outSet := facet.GetOutSet()
		if inSet == "" && outSet == "" {
			continue
		}

		scope := routing.OwnershipScope(c.src.DeviceID, routing.DefaultVRF, iface.GetName())
		hasIP := ownership.ipPresent[iface.GetName()]
		if !hasIP {
			c.addSkippedAt(scope, iface.GetName(), "filter", "interface carries no ip facet", analysis.Incomplete, IssueUnboundFilterInterface)
		}

		if inSet != "" {
			if unevaluable[inSet] {
				c.addSkippedAt(scope, iface.GetName(), "filter", fmt.Sprintf("rule set %q carries match terms the simulator does not evaluate", inSet), analysis.Incomplete, IssueSkippedUnsupportedFacet)
			} else if _, ok := setByName[inSet]; !ok {
				c.addSkippedAt(scope, iface.GetName(), "filter", fmt.Sprintf("rule set %q is missing", inSet), analysis.Incomplete, IssueMissingFilterSet)
			} else if hasIP {
				bindings = append(bindings, filter.Binding{
					Interface: iface.GetName(),
					Direction: filter.In,
					Set:       inSet,
				})
			}
		}

		if outSet != "" {
			if unevaluable[outSet] {
				c.addSkippedAt(scope, iface.GetName(), "filter", fmt.Sprintf("rule set %q carries match terms the simulator does not evaluate", outSet), analysis.Incomplete, IssueSkippedUnsupportedFacet)
			} else if _, ok := setByName[outSet]; !ok {
				c.addSkippedAt(scope, iface.GetName(), "filter", fmt.Sprintf("rule set %q is missing", outSet), analysis.Incomplete, IssueMissingFilterSet)
			} else if hasIP {
				bindings = append(bindings, filter.Binding{
					Interface: iface.GetName(),
					Direction: filter.Out,
					Set:       outSet,
				})
			}
		}
	}

	return bindings
}

// hasUnevaluableMatch reports whether rule matches on a term filter.Match has
// no counterpart for, or on an IPv4-mapped prefix the filter refuses.
func hasUnevaluableMatch(rule *filterv1.FilterRule) bool {
	m := rule.GetMatch()
	if m.HasEtherType() || m.HasSrcMac() || m.HasDstMac() || len(m.GetPcps()) > 0 || len(m.GetDscps()) > 0 {
		return true
	}
	for _, prefixes := range [][]*addrv1.IpPrefix{m.GetSrcPrefixes(), m.GetDstPrefixes()} {
		for _, prefix := range prefixes {
			if p, ok := parseIPPrefix(prefix); ok && p.Addr().Is4In6() {
				return true
			}
		}
	}
	return false
}

func translateAction(a filterv1.FilterAction) filter.Action {
	switch a {
	case filterv1.FilterAction_FILTER_ACTION_ACCEPT:
		return filter.Accept
	case filterv1.FilterAction_FILTER_ACTION_DROP:
		return filter.Drop
	case filterv1.FilterAction_FILTER_ACTION_REJECT:
		return filter.Reject
	default:
		return filter.Drop
	}
}
