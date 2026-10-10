package netmodel_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"buf.build/go/protovalidate"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

func predicateSet(name string, matches ...*filterv1.FilterMatch) *filterv1.FilterRuleSet {
	rules := make([]*filterv1.FilterRule, 0, len(matches))
	for i, match := range matches {
		rules = append(rules, filterv1.FilterRule_builder{Name: new(fmt.Sprintf("rule-%d", i)), Action: filterv1.FilterAction_FILTER_ACTION_DROP.Enum(), Match: match}.Build())
	}
	return filterv1.FilterRuleSet_builder{Name: new(name), Default: filterv1.FilterAction_FILTER_ACTION_ACCEPT.Enum(), Rules: rules}.Build()
}

func loadPredicates(t *testing.T, direction filter.Direction, sets ...*filterv1.FilterRuleSet) netmodel.Result {
	t.Helper()
	iface := routedPhysicalInterface("in")
	facet := filterv1.FilterFacet_builder{}.Build()
	if direction == filter.In {
		facet.SetInSet("bad")
		facet.SetOutSet("good")
	} else {
		facet.SetOutSet("bad")
		facet.SetInSet("good")
	}
	iface.SetFilter(facet)
	if err := protovalidate.Validate(iface); err != nil {
		t.Fatal(err)
	}
	good := predicateSet("good", filterv1.FilterMatch_builder{Protocol: packetv1.IpProtocol_IP_PROTOCOL_TCP.Enum()}.Build())
	if err := protovalidate.Validate(good); err != nil {
		t.Fatal(err)
	}
	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot"}, netmodel.Input{Interfaces: []*interfacev1.Interface{iface}, FilterSets: append(sets, good)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := vswitch.NewWithSpec(res.Spec); err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	return res
}

func assertPredicateOmitted(t *testing.T, res netmodel.Result, rule, field, reason string) {
	t.Helper()
	cfg := res.Spec.Config.Filter
	if cfg == nil {
		t.Fatal("filter missing, want retained good sibling")
	}
	if _, ok := cfg.Sets["bad"]; ok {
		t.Error("bad set retained")
	}
	good, ok := cfg.Sets["good"]
	if !ok || len(good.Rules) != 1 || good.Rules[0].Match.Protocol == nil || *good.Rules[0].Match.Protocol != 6 {
		t.Errorf("good sibling = %+v, want TCP rule", good)
	}
	if len(cfg.Sets) != 1 || len(cfg.Bindings) != 1 || cfg.Bindings[0].Set != "good" {
		t.Errorf("filter = %+v, want only good set and binding", cfg)
	}
	if res.Readiness() == analysis.Complete {
		t.Error("readiness Complete, want degraded")
	}
	found := false
	for _, skipped := range res.Report.Skipped {
		if strings.Contains(skipped.Why, `"bad"`) && strings.Contains(skipped.Why, rule) && strings.Contains(skipped.Why, field) && strings.Contains(skipped.Why, reason) && len(skipped.Evidence) > 0 {
			for _, ref := range skipped.Evidence {
				if _, ok := res.Metadata.Evidence().Lookup(ref); !ok {
					t.Errorf("evidence %q absent", ref)
				}
			}
			for _, issue := range res.Metadata.Issues() {
				if issue.Code == netmodel.IssueSkippedUnsupportedFacet && strings.Contains(issue.Message, `"bad"`) && strings.Contains(issue.Message, rule) && strings.Contains(issue.Message, field) && strings.Contains(issue.Message, reason) && len(issue.Evidence) > 0 {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("skipped = %+v, want evidenced %s/%s/%s diagnostic", res.Report.Skipped, rule, field, reason)
	}
}

func TestFilterMatchFieldMatrix(t *testing.T) {
	cases := []struct {
		field, reason string
		apply         func(*filterv1.FilterMatch)
	}{
		{"protocol", "255", func(m *filterv1.FilterMatch) { m.SetProtocol(packetv1.IpProtocol(256)) }},
		{"src_prefixes", "length is absent", func(m *filterv1.FilterMatch) { m.SetSrcPrefixes([]*addrv1.IpPrefix{missingLengthPrefix()}) }},
		{"dst_prefixes", "length is absent", func(m *filterv1.FilterMatch) { m.SetDstPrefixes([]*addrv1.IpPrefix{missingLengthPrefix()}) }},
		{"src_ports", "start is absent", func(m *filterv1.FilterMatch) {
			m.SetSrcPorts([]*packetv1.TransportPortMatch{portRange(nil, new(uint32(80)))})
		}},
		{"dst_ports", "start is absent", func(m *filterv1.FilterMatch) {
			m.SetDstPorts([]*packetv1.TransportPortMatch{portRange(nil, new(uint32(80)))})
		}},
		{"icmp", "type", func(m *filterv1.FilterMatch) { m.SetIcmp(icmpPredicate(false, nil, []uint32{0})) }},
		{"tcp_flags", "empty", func(m *filterv1.FilterMatch) { m.SetTcpFlags(packetv1.TcpFlagsMatch_builder{}.Build()) }},
		{"ether_type", "unsupported", func(m *filterv1.FilterMatch) { m.SetEtherType(packetv1.EtherType(0x0800)) }},
		{"src_mac", "unsupported", func(m *filterv1.FilterMatch) {
			m.SetSrcMac(filterv1.MacMatch_builder{Address: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build()}.Build())
		}},
		{"dst_mac", "unsupported", func(m *filterv1.FilterMatch) {
			m.SetDstMac(filterv1.MacMatch_builder{Address: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4, 5}}.Build()}.Build())
		}},
		{"pcps", "unsupported", func(m *filterv1.FilterMatch) { m.SetPcps([]uint32{1}) }},
		{"dscps", "unsupported", func(m *filterv1.FilterMatch) { m.SetDscps([]packetv1.IpDscp{0}) }},
	}
	reached := 0
	for _, tc := range cases {
		for _, sibling := range []bool{false, true} {
			for _, dir := range []filter.Direction{filter.In, filter.Out} {
				t.Run(fmt.Sprintf("%s/sibling-%v/%s", tc.field, sibling, dir), func(t *testing.T) {
					m := filterv1.FilterMatch_builder{}.Build()
					if sibling && tc.field != "protocol" {
						m.SetProtocol(packetv1.IpProtocol_IP_PROTOCOL_TCP)
					}
					if sibling && tc.field == "protocol" {
						m.SetDstPorts([]*packetv1.TransportPortMatch{packetv1.TransportPortMatch_builder{Exact: new(uint32(443))}.Build()})
					}
					tc.apply(m)
					if tc.reason == "unsupported" {
						if err := protovalidate.Validate(m); err != nil {
							t.Fatal(err)
						}
					}
					assertPredicateOmitted(t, loadPredicates(t, dir, predicateSet("bad", m)), "rule-0", tc.field, tc.reason)
					reached++
				})
			}
		}
	}
	if reached != 48 {
		t.Errorf("reached %d cases, want 48", reached)
	}
}

func missingLengthPrefix() *addrv1.IpPrefix {
	return addrv1.IpPrefix_builder{V4: addrv1.Ipv4Prefix_builder{Address: addrv1.Ipv4Address_builder{Octets: []byte{0, 0, 0, 0}}.Build()}.Build()}.Build()
}

func portRange(start, end *uint32) *packetv1.TransportPortMatch {
	return packetv1.TransportPortMatch_builder{Range: packetv1.TransportPortRange_builder{Start: start, End: end}.Build()}.Build()
}

func icmpPredicate(v6 bool, types, codes []uint32) *packetv1.IcmpMatch {
	if v6 {
		return packetv1.IcmpMatch_builder{V6: packetv1.Icmpv6Match_builder{Types: types, Codes: codes}.Build()}.Build()
	}
	return packetv1.IcmpMatch_builder{V4: packetv1.Icmpv4Match_builder{Types: types, Codes: codes}.Build()}.Build()
}

func TestFilterMalformedPrefixPortMatrix(t *testing.T) {
	cases := []struct {
		name, reason string
		prefix       *addrv1.IpPrefix
		port         *packetv1.TransportPortMatch
	}{
		{name: "length-absent", reason: "length is absent", prefix: missingLengthPrefix()},
		{name: "bad-bytes", reason: "octets length", prefix: addrv1.IpPrefix_builder{V4: addrv1.Ipv4Prefix_builder{Address: addrv1.Ipv4Address_builder{Octets: []byte{0, 0, 0}}.Build(), Length: new(uint32(0))}.Build()}.Build()},
		{name: "host-bits", reason: "host bits", prefix: protoIPv4Prefix([4]byte{10, 0, 0, 1}, 24)},
		{name: "length-large", reason: "exceeds 32", prefix: protoIPv4Prefix([4]byte{}, 33)},
		{name: "start-absent", reason: "start is absent", port: portRange(nil, new(uint32(80)))},
		{name: "end-absent", reason: "end is absent", port: portRange(new(uint32(0)), nil)},
		{name: "inverted", reason: "exceeds end", port: portRange(new(uint32(100)), new(uint32(99)))},
		{name: "oversized", reason: "exceeds 65535", port: portRange(new(uint32(0)), new(uint32(65536)))},
	}
	reached := 0
	for _, tc := range cases {
		for _, src := range []bool{false, true} {
			for _, dir := range []filter.Direction{filter.In, filter.Out} {
				t.Run(fmt.Sprintf("%s/src-%v/%s", tc.name, src, dir), func(t *testing.T) {
					m := filterv1.FilterMatch_builder{}.Build()
					field := "dst_"
					if src {
						field = "src_"
					}
					if tc.prefix != nil {
						field += "prefixes"
						if src {
							m.SetSrcPrefixes([]*addrv1.IpPrefix{tc.prefix})
						} else {
							m.SetDstPrefixes([]*addrv1.IpPrefix{tc.prefix})
						}
					} else {
						field += "ports"
						if src {
							m.SetSrcPorts([]*packetv1.TransportPortMatch{tc.port})
						} else {
							m.SetDstPorts([]*packetv1.TransportPortMatch{tc.port})
						}
					}
					assertPredicateOmitted(t, loadPredicates(t, dir, predicateSet("bad", m)), "rule-0", field, tc.reason)
					reached++
				})
			}
		}
	}
	if reached != 32 {
		t.Errorf("reached %d cases, want 32", reached)
	}
}

func TestFilterActionsOmitSet(t *testing.T) {
	for _, position := range []string{"default", "action"} {
		for _, value := range []int{-1, 0, 99} {
			t.Run(fmt.Sprintf("%s/%d", position, value), func(t *testing.T) {
				set := predicateSet("bad", filterv1.FilterMatch_builder{}.Build())
				if position == "default" {
					if value < 0 {
						set.ClearDefault()
					} else {
						set.SetDefault(filterv1.FilterAction(value))
					}
				} else {
					if value < 0 {
						set.GetRules()[0].ClearAction()
					} else {
						set.GetRules()[0].SetAction(filterv1.FilterAction(value))
					}
				}
				rule := "rule-0"
				if position == "default" {
					rule = "default"
				}
				assertPredicateOmitted(t, loadPredicates(t, filter.In, set), rule, position, "action")
			})
		}
	}
}

func TestFilterICMPShapesOmitSet(t *testing.T) {
	cases := []struct {
		name, reason string
		types, codes []uint32
	}{
		{"code-only", "type", nil, []uint32{0}}, {"multi-type", "type", []uint32{8, 0}, nil}, {"multi-code", "code", []uint32{8}, []uint32{0, 1}}, {"empty", "type", nil, nil}, {"large-type", "255", []uint32{256}, nil}, {"large-code", "255", []uint32{8}, []uint32{256}},
	}
	for _, v6 := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("v6-%v/%s", v6, tc.name), func(t *testing.T) {
				assertPredicateOmitted(t, loadPredicates(t, filter.In, predicateSet("bad", filterv1.FilterMatch_builder{Icmp: icmpPredicate(v6, tc.types, tc.codes)}.Build())), "rule-0", "icmp", tc.reason)
			})
		}
	}
	for _, protocol := range []packetv1.IpProtocol{-1, 256} {
		t.Run(fmt.Sprintf("protocol-%d", protocol), func(t *testing.T) {
			assertPredicateOmitted(t, loadPredicates(t, filter.In, predicateSet("bad", filterv1.FilterMatch_builder{Protocol: new(protocol)}.Build())), "rule-0", "protocol", "255")
		})
	}
	t.Run("missing-family", func(t *testing.T) {
		assertPredicateOmitted(t, loadPredicates(t, filter.In, predicateSet("bad", filterv1.FilterMatch_builder{Icmp: packetv1.IcmpMatch_builder{}.Build()}.Build())), "rule-0", "icmp", "family")
	})
	for _, v6 := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict-v6-%v", v6), func(t *testing.T) {
			protocol := packetv1.IpProtocol(58)
			if v6 {
				protocol = 1
			}
			assertPredicateOmitted(t, loadPredicates(t, filter.In, predicateSet("bad", filterv1.FilterMatch_builder{Protocol: new(protocol), Icmp: icmpPredicate(v6, []uint32{8}, nil)}.Build())), "rule-0", "icmp", "protocol")
		})
	}
}

func TestFilterMalformedTCPFlagsOmitSet(t *testing.T) {
	cases := []struct {
		name, reason string
		set, clear   []packetv1.TcpFlag
	}{
		{"empty", "empty", nil, nil}, {"duplicate-set", "duplicate", []packetv1.TcpFlag{2, 2}, nil}, {"duplicate-clear", "duplicate", nil, []packetv1.TcpFlag{2, 2}}, {"zero", "one-hot", []packetv1.TcpFlag{0}, nil}, {"combined", "one-hot", []packetv1.TcpFlag{3}, nil}, {"oversized", "one-hot", []packetv1.TcpFlag{4096}, nil}, {"overlap", "both", []packetv1.TcpFlag{2}, []packetv1.TcpFlag{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPredicateOmitted(t, loadPredicates(t, filter.In, predicateSet("bad", filterv1.FilterMatch_builder{TcpFlags: packetv1.TcpFlagsMatch_builder{RequiredSet: tc.set, RequiredClear: tc.clear}.Build()}.Build())), "rule-0", "tcp_flags", tc.reason)
		})
	}
}

func TestFilterReservedTCPFlagsMatrix(t *testing.T) {
	reached := 0
	for _, mask := range []packetv1.TcpFlag{256, 512, 1024, 2048} {
		for _, clear := range []bool{false, true} {
			for _, dir := range []filter.Direction{filter.In, filter.Out} {
				t.Run(fmt.Sprintf("%d/clear-%v/%s", mask, clear, dir), func(t *testing.T) {
					flags := packetv1.TcpFlagsMatch_builder{}.Build()
					if clear {
						flags.SetRequiredClear([]packetv1.TcpFlag{mask})
					} else {
						flags.SetRequiredSet([]packetv1.TcpFlag{mask})
					}
					set := predicateSet("bad", filterv1.FilterMatch_builder{TcpFlags: flags}.Build())
					if err := protovalidate.Validate(set); err != nil {
						t.Fatal(err)
					}
					assertPredicateOmitted(t, loadPredicates(t, dir, set), "rule-0", "tcp_flags", "unsupported")
					reached++
				})
			}
		}
	}
	if reached != 16 {
		t.Errorf("reached %d cases, want 16", reached)
	}
}

func TestFilterSameNameRowsOmittedMatrix(t *testing.T) {
	reached := 0
	for _, invalidFirst := range []bool{false, true} {
		for _, dir := range []filter.Direction{filter.In, filter.Out} {
			t.Run(fmt.Sprintf("invalid-first-%v/%s", invalidFirst, dir), func(t *testing.T) {
				valid := predicateSet("bad", filterv1.FilterMatch_builder{}.Build())
				invalid := predicateSet("bad", filterv1.FilterMatch_builder{SrcPrefixes: []*addrv1.IpPrefix{missingLengthPrefix()}}.Build())
				sets := []*filterv1.FilterRuleSet{valid, invalid}
				if invalidFirst {
					sets = []*filterv1.FilterRuleSet{invalid, valid}
				}
				assertPredicateOmitted(t, loadPredicates(t, dir, sets...), "rule-0", "src_prefixes", "length is absent")
				reached++
			})
		}
	}
	if reached != 4 {
		t.Errorf("reached %d cases, want 4", reached)
	}
}

func TestFilterAllOffendersReported(t *testing.T) {
	reached := 0
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprintf("separate-%v", separate), func(t *testing.T) {
			first := filterv1.FilterMatch_builder{SrcPrefixes: []*addrv1.IpPrefix{missingLengthPrefix()}}.Build()
			second := filterv1.FilterMatch_builder{DstPorts: []*packetv1.TransportPortMatch{portRange(new(uint32(100)), new(uint32(99)))}}.Build()
			matches := []*filterv1.FilterMatch{first, second}
			secondRule := "rule-1"
			if !separate {
				first.SetDstPorts(second.GetDstPorts())
				matches = matches[:1]
				secondRule = "rule-0"
			}
			res := loadPredicates(t, filter.In, predicateSet("bad", matches...))
			assertPredicateOmitted(t, res, "rule-0", "src_prefixes", "length is absent")
			assertPredicateOmitted(t, res, secondRule, "dst_ports", "exceeds end")
			reached++
		})
	}
	if reached != 2 {
		t.Errorf("reached %d cases, want 2", reached)
	}
}

func TestFilterSupportedPredicatesRetained(t *testing.T) {
	cases := []struct {
		name  string
		match *filterv1.FilterMatch
		check func(filter.Match) bool
	}{
		{"protocol", filterv1.FilterMatch_builder{Protocol: packetv1.IpProtocol_IP_PROTOCOL_TCP.Enum()}.Build(), func(m filter.Match) bool { return m.Protocol != nil && *m.Protocol == 6 }},
		{"src_prefixes", filterv1.FilterMatch_builder{SrcPrefixes: []*addrv1.IpPrefix{protoIPv4Prefix([4]byte{10}, 24)}}.Build(), func(m filter.Match) bool { return len(m.Src) == 1 && m.Src[0] == netip.MustParsePrefix("10.0.0.0/24") }},
		{"dst_prefixes", filterv1.FilterMatch_builder{DstPrefixes: []*addrv1.IpPrefix{protoIPv6Prefix([16]byte{}, 0)}}.Build(), func(m filter.Match) bool { return len(m.Dst) == 1 && m.Dst[0] == netip.MustParsePrefix("::/0") }},
		{"src_ports", filterv1.FilterMatch_builder{SrcPorts: []*packetv1.TransportPortMatch{portRange(new(uint32(0)), new(uint32(80)))}}.Build(), func(m filter.Match) bool {
			return len(m.SrcPorts) == 1 && m.SrcPorts[0] == (filter.PortRange{Start: 0, End: 80})
		}},
		{"dst_ports", filterv1.FilterMatch_builder{DstPorts: []*packetv1.TransportPortMatch{packetv1.TransportPortMatch_builder{Exact: new(uint32(443))}.Build()}}.Build(), func(m filter.Match) bool {
			return len(m.DstPorts) == 1 && m.DstPorts[0] == (filter.PortRange{Start: 443, End: 443})
		}},
		{"icmp4", filterv1.FilterMatch_builder{Icmp: icmpPredicate(false, []uint32{8}, []uint32{0})}.Build(), func(m filter.Match) bool {
			return m.Protocol != nil && *m.Protocol == 1 && m.ICMP != nil && m.ICMP.Type == 8 && m.ICMP.Code != nil && *m.ICMP.Code == 0
		}},
		{"icmp6", filterv1.FilterMatch_builder{Icmp: icmpPredicate(true, []uint32{128}, nil)}.Build(), func(m filter.Match) bool {
			return m.Protocol != nil && *m.Protocol == 58 && m.ICMP != nil && m.ICMP.Type == 128 && m.ICMP.Code == nil
		}},
		{"tcp_flags", filterv1.FilterMatch_builder{TcpFlags: packetv1.TcpFlagsMatch_builder{RequiredSet: []packetv1.TcpFlag{2}, RequiredClear: []packetv1.TcpFlag{16}}.Build()}.Build(), func(m filter.Match) bool { return m.TCPFlags != nil && m.TCPFlags.Mask == 18 && m.TCPFlags.Value == 2 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := predicateSet("bad", tc.match)
			if err := protovalidate.Validate(set); err != nil {
				t.Fatal(err)
			}
			res := loadPredicates(t, filter.In, set)
			cfg := res.Spec.Config.Filter
			if cfg == nil || len(cfg.Sets) != 2 || len(cfg.Bindings) != 2 || !tc.check(cfg.Sets["bad"].Rules[0].Match) {
				t.Errorf("filter = %+v, want retained full %s predicate and bindings", cfg, tc.name)
			}
		})
	}
}

func TestFilterICMPFamilyReceive(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			for _, code := range []bool{false, true} {
				t.Run(fmt.Sprintf("v6-%v/explicit-%v/code-%v", v6, explicit, code), func(t *testing.T) {
					protocol := packetv1.IpProtocol(1)
					if v6 {
						protocol = 58
					}
					var codes []uint32
					if code {
						codes = []uint32{0}
					}
					m := filterv1.FilterMatch_builder{Icmp: icmpPredicate(v6, []uint32{8}, codes)}.Build()
					if explicit {
						m.SetProtocol(protocol)
					}
					set := predicateSet("bad", m)
					if err := protovalidate.Validate(set); err != nil {
						t.Fatal(err)
					}
					res := loadPredicates(t, filter.In, set)
					for _, packetV6 := range []bool{false, true} {
						sw, err := vswitch.NewWithSpec(res.Spec)
						if err != nil {
							t.Fatal(err)
						}
						hdr := ip.Header{Src: netip.MustParseAddr("192.0.2.1"), Dst: netip.MustParseAddr("192.0.2.2"), HopLimit: 64, Protocol: 1, V4: &ip.V4{}}
						etherType := ethernet.EtherTypeIPv4
						if packetV6 {
							hdr = ip.Header{Src: netip.MustParseAddr("2001:db8::1"), Dst: netip.MustParseAddr("2001:db8::2"), HopLimit: 64, Protocol: 58, V6: &ip.V6{}}
							etherType = ethernet.EtherTypeIPv6
						}
						payload, err := hdr.Encode([]byte{8, 0, 0, 0})
						if err != nil {
							t.Fatal(err)
						}
						frame := ethernet.Frame{Src: netaddr.MAC{0, 1, 2, 3, 4, 5}, Dst: res.Spec.Config.Routing.VRFs["default"].Interfaces["in"].MAC, EtherType: etherType, Payload: payload}
						out := sw.Receive(testTime, "in", frame)
						if packetV6 == v6 {
							if out.Reason != filter.ReasonFilterDrop {
								t.Errorf("same-family Receive = %+v, want filter drop", out)
							}
						} else {
							if out.Reason == filter.ReasonFilterDrop {
								t.Errorf("opposite-family Receive = %+v, want default acceptance", out)
							}
							found := false
							for _, step := range out.Steps {
								if step.Layer == filter.LayerName {
									found = true
								}
							}
							if !found {
								t.Errorf("opposite-family Receive steps = %+v, want consulted filter", out.Steps)
							}
						}
					}
				})
			}
		}
	}
}

func TestFilterAdditionalMalformedScalars(t *testing.T) {
	cases := []struct {
		name, field, reason string
		match               *filterv1.FilterMatch
	}{
		{"v6-length-absent", "src_prefixes", "length is absent", filterv1.FilterMatch_builder{SrcPrefixes: []*addrv1.IpPrefix{addrv1.IpPrefix_builder{V6: addrv1.Ipv6Prefix_builder{Address: addrv1.Ipv6Address_builder{Octets: make([]byte, 16)}.Build()}.Build()}.Build()}}.Build()},
		{"v6-bad-bytes", "src_prefixes", "octets length", filterv1.FilterMatch_builder{SrcPrefixes: []*addrv1.IpPrefix{addrv1.IpPrefix_builder{V6: addrv1.Ipv6Prefix_builder{Address: addrv1.Ipv6Address_builder{Octets: make([]byte, 15)}.Build(), Length: new(uint32(0))}.Build()}.Build()}}.Build()},
		{"v6-host-bits", "src_prefixes", "host bits", filterv1.FilterMatch_builder{SrcPrefixes: []*addrv1.IpPrefix{protoIPv6Prefix([16]byte{0x20, 1, 0xdb, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}, 64)}}.Build()},
		{"v6-length-large", "src_prefixes", "exceeds 128", filterv1.FilterMatch_builder{SrcPrefixes: []*addrv1.IpPrefix{protoIPv6Prefix([16]byte{}, 129)}}.Build()},
		{"prefix-family-absent", "src_prefixes", "family is absent", filterv1.FilterMatch_builder{SrcPrefixes: []*addrv1.IpPrefix{addrv1.IpPrefix_builder{}.Build()}}.Build()},
		{"range-start-large", "src_ports", "exceeds 65535", filterv1.FilterMatch_builder{SrcPorts: []*packetv1.TransportPortMatch{portRange(new(uint32(65536)), new(uint32(65536)))}}.Build()},
		{"exact-large", "src_ports", "exceeds 65535", filterv1.FilterMatch_builder{SrcPorts: []*packetv1.TransportPortMatch{packetv1.TransportPortMatch_builder{Exact: new(uint32(65536))}.Build()}}.Build()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPredicateOmitted(t, loadPredicates(t, filter.In, predicateSet("bad", tc.match)), "rule-0", tc.field, tc.reason)
		})
	}
}
