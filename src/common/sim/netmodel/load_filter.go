package netmodel

import (
	"fmt"
	"net/netip"
	"slices"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
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
		valid := true
		reject := func(rule, field, reason string) {
			valid = false
			c.addSkippedAt(c.rootScope, "", "filter", fmt.Sprintf("rule set %q rule %q field %s: %s", name, rule, field, reason), analysis.Incomplete, IssueSkippedUnsupportedFacet)
		}
		defaultAction, err := translateAction(set.HasDefault(), set.GetDefault())
		if err != nil {
			reject("default", "default", err.Error())
		}
		ruleSet := filter.RuleSet{Stateful: set.GetStateful(), Default: defaultAction}
		for _, rule := range set.GetRules() {
			if rule == nil {
				continue
			}
			r := c.loadFilterRule(rule, func(field, reason string) { reject(rule.GetName(), field, reason) })
			ruleSet.Rules = append(ruleSet.Rules, r)
		}
		if !valid {
			unevaluable[name] = true
			delete(setByName, name)
		}
		if valid && !unevaluable[name] {
			setByName[name] = ruleSet
		}
	}

	return setByName, unevaluable
}

func (c *loadContext) loadFilterRule(rule *filterv1.FilterRule, reject func(string, string)) filter.Rule {
	action, err := translateAction(rule.HasAction(), rule.GetAction())
	if err != nil {
		reject("action", err.Error())
	}
	r := filter.Rule{Name: rule.GetName(), Action: action}
	m := rule.GetMatch()
	if m == nil {
		return r
	}
	if m.HasProtocol() {
		if m.GetProtocol() < 0 || m.GetProtocol() > 255 {
			reject("protocol", "protocol is outside 0..255")
		} else {
			r.Match.Protocol = new(uint8(m.GetProtocol()))
		}
	}
	for _, field := range []struct {
		name    string
		present bool
	}{
		{"ether_type", m.HasEtherType()}, {"src_mac", m.HasSrcMac()}, {"dst_mac", m.HasDstMac()}, {"pcps", len(m.GetPcps()) > 0}, {"dscps", len(m.GetDscps()) > 0},
	} {
		if field.present {
			reject(field.name, "unsupported predicate")
		}
	}
	for _, field := range []struct {
		name     string
		prefixes []*addrv1.IpPrefix
		target   *[]netip.Prefix
	}{
		{"src_prefixes", m.GetSrcPrefixes(), &r.Match.Src}, {"dst_prefixes", m.GetDstPrefixes(), &r.Match.Dst},
	} {
		for i, p := range field.prefixes {
			prefix, err := parseIPPrefix(p)
			switch {
			case err != nil:
				reject(fmt.Sprintf("%s[%d]", field.name, i), err.Error())
			case prefix.Addr().Is4In6():
				reject(fmt.Sprintf("%s[%d]", field.name, i), "IPv4-mapped prefix is unsupported")
			default:
				*field.target = append(*field.target, prefix)
			}
		}
	}
	for _, field := range []struct {
		name   string
		ports  []*packetv1.TransportPortMatch
		target *[]filter.PortRange
	}{
		{"src_ports", m.GetSrcPorts(), &r.Match.SrcPorts}, {"dst_ports", m.GetDstPorts(), &r.Match.DstPorts},
	} {
		for i, p := range field.ports {
			pr, err := parsePortMatch(p)
			if err != nil {
				reject(fmt.Sprintf("%s[%d]", field.name, i), err.Error())
			} else {
				*field.target = append(*field.target, pr)
			}
		}
	}
	if icmp := m.GetIcmp(); icmp != nil {
		var types, codes []uint32
		var protocol uint8
		if v4 := icmp.GetV4(); v4 != nil {
			types, codes, protocol = v4.GetTypes(), v4.GetCodes(), 1
		} else if v6 := icmp.GetV6(); v6 != nil {
			types, codes, protocol = v6.GetTypes(), v6.GetCodes(), 58
		} else {
			reject("icmp", "family is absent")
		}
		if len(types) != 1 {
			reject("icmp.types", "exactly one type is required")
		}
		if len(codes) > 1 {
			reject("icmp.codes", "at most one code is supported")
		}
		for _, field := range []struct {
			name   string
			values []uint32
		}{{"icmp.types", types}, {"icmp.codes", codes}} {
			for _, value := range field.values {
				if value > 255 {
					reject(field.name, "value exceeds 255")
				}
			}
		}
		if m.HasProtocol() && m.GetProtocol() != packetv1.IpProtocol(protocol) {
			reject("icmp", "protocol conflicts with ICMP family")
		}
		if len(types) == 1 && types[0] <= 255 && len(codes) <= 1 {
			r.Match.ICMP = &filter.ICMPMatch{Type: uint8(types[0])}
			if len(codes) == 1 {
				r.Match.ICMP.Code = new(uint8(codes[0]))
			}
			if !m.HasProtocol() {
				r.Match.Protocol = new(protocol)
			}
		}
	}
	if flags := m.GetTcpFlags(); flags != nil {
		r.Match.TCPFlags = loadFilterFlags(flags, reject)
	}
	return r
}

func loadFilterFlags(flags *packetv1.TcpFlagsMatch, reject func(string, string)) *filter.FlagMatch {
	if len(flags.GetRequiredSet())+len(flags.GetRequiredClear()) == 0 {
		reject("tcp_flags", "empty predicate")
	}
	var mask, value tcp.Flags
	seen := make(map[packetv1.TcpFlag]string)
	for _, field := range []struct {
		name  string
		flags []packetv1.TcpFlag
	}{{"required_set", flags.GetRequiredSet()}, {"required_clear", flags.GetRequiredClear()}} {
		for _, flag := range field.flags {
			name := "tcp_flags." + field.name
			if flag <= 0 || flag > 2048 || flag&(flag-1) != 0 {
				reject(name, "flag must be one-hot in 1..2048")
			} else if flag > 128 {
				reject(name, "reserved flag is unsupported")
			}
			if prior, ok := seen[flag]; ok {
				if prior == field.name {
					reject(name, "duplicate flag")
				} else {
					reject(name, "flag required both set and clear")
				}
			}
			seen[flag] = field.name
			mask |= tcp.Flags(flag)
			if field.name == "required_set" {
				value |= tcp.Flags(flag)
			}
		}
	}
	return &filter.FlagMatch{Mask: mask, Value: value}
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

func translateAction(present bool, a filterv1.FilterAction) (filter.Action, error) {
	if !present {
		return "", errs.Msg("action is absent")
	}
	switch a {
	case filterv1.FilterAction_FILTER_ACTION_ACCEPT:
		return filter.Accept, nil
	case filterv1.FilterAction_FILTER_ACTION_DROP:
		return filter.Drop, nil
	case filterv1.FilterAction_FILTER_ACTION_REJECT:
		return filter.Reject, nil
	default:
		return "", errs.Msgf("action %d is unspecified or unknown", a)
	}
}
