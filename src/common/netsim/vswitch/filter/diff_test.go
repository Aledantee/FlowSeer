package filter_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/tcp"
	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/filter"
)

func TestDiffReportsRuleAndBindingChanges(t *testing.T) {
	protoTCP := uint8(6)
	protoUDP := uint8(17)

	cfgA := filter.Config{
		Sets: map[string]filter.RuleSet{
			"set1": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "rule-stay",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 80, End: 80}},
						},
					},
					{
						Name:   "rule-to-remove",
						Action: filter.Drop,
						Match: filter.Match{
							Protocol: &protoUDP,
							DstPorts: []filter.PortRange{{Start: 5353, End: 5353}},
						},
					},
					{
						Name:   "rule-to-change",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 8080, End: 8080}},
						},
					},
				},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "set1"},
			{Interface: "vlan20", Direction: filter.Out, Set: "set1"},
		},
	}

	cfgB := filter.Config{
		Sets: map[string]filter.RuleSet{
			"set1": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "rule-stay",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 80, End: 80}},
						},
					},
					{
						Name:   "rule-to-change",
						Action: filter.Reject,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 8080, End: 8080}},
						},
					},
					{
						Name:   "rule-to-add",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							DstPorts: []filter.PortRange{{Start: 443, End: 443}},
						},
					},
				},
			},
			"set2": {
				Stateful: false,
				Default:  filter.Accept,
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "set1"},
			{Interface: "vlan20", Direction: filter.Out, Set: "set2"}, // changed set
			{Interface: "vlan30", Direction: filter.In, Set: "set2"},  // added binding
		},
	}

	changes := filter.Diff(cfgA, cfgB)

	// Changes expected:
	// 1. Rule removed: rule-to-remove
	// 2. Rule changed: rule-to-change
	// 3. Rule added: rule-to-add
	// 4. Set added: set2
	// 5. Binding changed: vlan20/out from set1 to set2
	// 6. Binding added: vlan30/in set2
	var foundRemovedRule, foundChangedRule, foundAddedRule bool
	var foundChangedBinding, foundAddedBinding bool

	for _, c := range changes {
		if c.Subject.Kind == "rule" {
			switch c.Subject.Key {
			case "set1/rule-to-remove":
				if c.From != nil && c.To == nil {
					foundRemovedRule = true
				}
			case "set1/rule-to-change":
				if c.From != nil && c.To != nil {
					foundChangedRule = true
				}
			case "set1/rule-to-add":
				if c.From == nil && c.To != nil {
					foundAddedRule = true
				}
			}
		}
		if c.Subject.Kind == "binding" {
			switch c.Subject.Key {
			case "vlan20/out":
				if c.From != nil && c.To != nil && c.Field == "set" {
					foundChangedBinding = true
				}
			case "vlan30/in":
				if c.From == nil && c.To != nil {
					foundAddedBinding = true
				}
			}
		}
	}

	if !foundRemovedRule {
		t.Errorf("Diff missing rule removal change; got changes: %+v", changes)
	}
	if !foundChangedRule {
		t.Errorf("Diff missing rule modification change; got changes: %+v", changes)
	}
	if !foundAddedRule {
		t.Errorf("Diff missing rule addition change; got changes: %+v", changes)
	}
	if !foundChangedBinding {
		t.Errorf("Diff missing binding change; got changes: %+v", changes)
	}
	if !foundAddedBinding {
		t.Errorf("Diff missing binding addition; got changes: %+v", changes)
	}
}

func TestDiffCoversEveryConfigField(t *testing.T) {
	protoTCP := uint8(6)
	icmpCode := uint8(0)

	seed := filter.Config{
		Sets: map[string]filter.RuleSet{
			"set1": {
				Stateful: true,
				Default:  filter.Drop,
				Rules: []filter.Rule{
					{
						Name:   "rule1",
						Action: filter.Accept,
						Match: filter.Match{
							Protocol: &protoTCP,
							Src:      []netip.Prefix{netip.MustParsePrefix("10.0.1.0/24")},
							Dst:      []netip.Prefix{netip.MustParsePrefix("10.0.2.0/24")},
							SrcPorts: []filter.PortRange{{Start: 1000, End: 2000}},
							DstPorts: []filter.PortRange{{Start: 80, End: 80}},
							ICMP:     &filter.ICMPMatch{Type: 8, Code: &icmpCode},
							TCPFlags: &filter.FlagMatch{Mask: tcp.SYN | tcp.ACK, Value: tcp.SYN},
						},
					},
				},
			},
		},
		Bindings: []filter.Binding{
			{Interface: "vlan10", Direction: filter.In, Set: "set1"},
		},
	}

	netsimtest.AssertDiffCoversConfig(t, seed, filter.Config.Normalize, filter.Diff, nil)
}
