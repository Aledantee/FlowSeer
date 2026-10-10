package netmodel_test

import (
	"net/netip"
	"slices"
	"testing"

	"buf.build/go/protovalidate"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

func TestLoad_FilterFacetWithIPFacet(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vid10 := uint32(10)
	vlan10Name := "vlan10"
	inSetName := "in-set"
	outSetName := "out-set"

	rule1Name := "allow-http"
	tcpProto := packetv1.IpProtocol_IP_PROTOCOL_TCP
	p80 := uint32(80)
	acceptAction := filterv1.FilterAction_FILTER_ACTION_ACCEPT
	dropAction := filterv1.FilterAction_FILTER_ACTION_DROP

	rule1 := filterv1.FilterRule_builder{
		Name:   &rule1Name,
		Action: &acceptAction,
		Match: filterv1.FilterMatch_builder{
			Protocol: &tcpProto,
			DstPrefixes: []*addrv1.IpPrefix{
				protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24),
			},
			DstPorts: []*packetv1.TransportPortMatch{
				packetv1.TransportPortMatch_builder{
					Exact: &p80,
				}.Build(),
			},
		}.Build(),
	}.Build()

	isStateful := true
	inSet := filterv1.FilterRuleSet_builder{
		Name:     &inSetName,
		Stateful: &isStateful,
		Default:  &dropAction,
		Rules:    []*filterv1.FilterRule{rule1},
	}.Build()

	notStateful := false
	outSet := filterv1.FilterRuleSet_builder{
		Name:     &outSetName,
		Stateful: &notStateful,
		Default:  &acceptAction,
	}.Build()

	filterSets := []*filterv1.FilterRuleSet{inSet, outSet}
	for _, s := range filterSets {
		if err := protovalidate.Validate(s); err != nil {
			t.Fatalf("filter set validation failed: %v", err)
		}
	}

	vlan10 := interfacev1.Interface_builder{
		Name:        &vlan10Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Vlan: interfacev1.VlanInterface_builder{
			VlanId: &vid10,
		}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
		Filter: filterv1.FilterFacet_builder{
			InSet:  &inSetName,
			OutSet: &outSetName,
		}.Build(),
	}.Build()

	if err := protovalidate.Validate(vlan10); err != nil {
		t.Fatalf("interface validation failed: %v", err)
	}

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{vlan10}, nil, nil, nil, nil, nil, nil, nil, nil, nil, filterSets, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}

	cfg := res.Spec.Config
	if cfg.Filter == nil {
		t.Fatal("expected cfg.Filter to be non-nil")
	}
	if len(cfg.Filter.Sets) != 2 {
		t.Fatalf("sets count = %d, want 2", len(cfg.Filter.Sets))
	}
	sIn, ok := cfg.Filter.Sets["in-set"]
	if !ok {
		t.Fatal("missing in-set")
	}
	if !sIn.Stateful {
		t.Errorf("in-set stateful = %v, want true", sIn.Stateful)
	}
	if sIn.Default != filter.Drop {
		t.Errorf("in-set default = %v, want Drop", sIn.Default)
	}
	if len(sIn.Rules) != 1 {
		t.Fatalf("in-set rules count = %d, want 1", len(sIn.Rules))
	}
	r := sIn.Rules[0]
	if r.Name != "allow-http" || r.Action != filter.Accept {
		t.Errorf("rule = %+v, want allow-http / Accept", r)
	}
	if r.Match.Protocol == nil || *r.Match.Protocol != 6 {
		t.Errorf("protocol = %v, want 6", r.Match.Protocol)
	}
	wantPrefix := netip.MustParsePrefix("10.0.0.0/24")
	if len(r.Match.Dst) != 1 || r.Match.Dst[0] != wantPrefix {
		t.Errorf("dst prefix = %v, want %v", r.Match.Dst, wantPrefix)
	}
	if len(r.Match.DstPorts) != 1 || r.Match.DstPorts[0].Start != 80 || r.Match.DstPorts[0].End != 80 {
		t.Errorf("dst ports = %+v, want 80..80", r.Match.DstPorts)
	}

	sOut, ok := cfg.Filter.Sets["out-set"]
	if !ok {
		t.Fatal("missing out-set")
	}
	if sOut.Stateful {
		t.Errorf("out-set stateful = %v, want false", sOut.Stateful)
	}
	if sOut.Default != filter.Accept {
		t.Errorf("out-set default = %v, want Accept", sOut.Default)
	}

	var foundIn, foundOut bool
	for _, b := range cfg.Filter.Bindings {
		if b.Interface == "vlan10" && b.Direction == filter.In && b.Set == "in-set" {
			foundIn = true
		}
		if b.Interface == "vlan10" && b.Direction == filter.Out && b.Set == "out-set" {
			foundOut = true
		}
	}
	if !foundIn || !foundOut {
		t.Errorf("bindings = %+v, want vlan10 in->in-set and out->out-set", cfg.Filter.Bindings)
	}

	if !slices.Contains(res.Report.Capabilities, filter.LayerName) {
		t.Errorf("capabilities = %v, want to contain filter.LayerName", res.Report.Capabilities)
	}
}

func TestLoad_FilterFacetWithoutIPFacet(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"
	inSetName := "in-set"
	acceptAction := filterv1.FilterAction_FILTER_ACTION_ACCEPT

	inSet := filterv1.FilterRuleSet_builder{
		Name:    &inSetName,
		Default: &acceptAction,
	}.Build()

	p1 := interfacev1.Interface_builder{
		Name:        &p1Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		Filter: filterv1.FilterFacet_builder{
			InSet: &inSetName,
		}.Build(),
	}.Build()

	if err := protovalidate.Validate(p1); err != nil {
		t.Fatalf("interface validation failed: %v", err)
	}

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{p1}, nil, nil, nil, nil, nil, nil, nil, nil, nil, []*filterv1.FilterRuleSet{inSet}, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}

	if res.Readiness() == analysis.Complete {
		t.Errorf("readiness = %v, want degraded", res.Readiness())
	}

	wantScope := routing.OwnershipScope("sw1", routing.DefaultVRF, "1/1/1")
	found := false
	for _, issue := range res.Metadata.Issues() {
		if issue.Code == netmodel.IssueUnboundFilterInterface && issue.Scope.Compare(wantScope) == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("issues = %+v, want issue %s with scope %s", res.Metadata.Issues(), netmodel.IssueUnboundFilterInterface, wantScope)
	}
}

func TestLoad_FilterFacetMissingSet(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vid10 := uint32(10)
	vlan10Name := "vlan10"
	missingSetName := "nonexistent-set"

	vlan10 := interfacev1.Interface_builder{
		Name:        &vlan10Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Vlan: interfacev1.VlanInterface_builder{
			VlanId: &vid10,
		}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
		Filter: filterv1.FilterFacet_builder{
			InSet: &missingSetName,
		}.Build(),
	}.Build()

	if err := protovalidate.Validate(vlan10); err != nil {
		t.Fatalf("interface validation failed: %v", err)
	}

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{vlan10}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}

	if res.Readiness() == analysis.Complete {
		t.Errorf("readiness = %v, want degraded", res.Readiness())
	}

	wantScope := routing.OwnershipScope("sw1", routing.DefaultVRF, "vlan10")
	found := false
	for _, issue := range res.Metadata.Issues() {
		if issue.Code == netmodel.IssueMissingFilterSet && issue.Scope.Compare(wantScope) == 0 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("issues = %+v, want issue %s with scope %s", res.Metadata.Issues(), netmodel.IssueMissingFilterSet, wantScope)
	}
}

// A rule set matching a destination MAC, which the IP-layer filter cannot
// evaluate, is left out of the filter configuration rather than translated
// wider: its ingress binding reports an unsupported facet, not a missing set,
// while the evaluable egress set still binds.
func TestLoad_FilterSetWithL2MatchIsSkipped(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vid10 := uint32(10)
	vlan10Name := "vlan10"
	l2SetName := "l2-set"
	ipSetName := "ip-set"
	ruleName := "drop-bpdu"
	acceptAction := filterv1.FilterAction_FILTER_ACTION_ACCEPT
	dropAction := filterv1.FilterAction_FILTER_ACTION_DROP

	l2Set := filterv1.FilterRuleSet_builder{
		Name:    &l2SetName,
		Default: &acceptAction,
		Rules: []*filterv1.FilterRule{filterv1.FilterRule_builder{
			Name:   &ruleName,
			Action: &dropAction,
			Match: filterv1.FilterMatch_builder{
				DstMac: filterv1.MacMatch_builder{
					Address: addrv1.Eui48Address_builder{Octets: []byte{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00}}.Build(),
				}.Build(),
			}.Build(),
		}.Build()},
	}.Build()
	ipSet := filterv1.FilterRuleSet_builder{
		Name:    &ipSetName,
		Default: &acceptAction,
	}.Build()

	filterSets := []*filterv1.FilterRuleSet{l2Set, ipSet}
	for _, s := range filterSets {
		if err := protovalidate.Validate(s); err != nil {
			t.Fatalf("filter set validation failed: %v", err)
		}
	}

	vlan10 := interfacev1.Interface_builder{
		Name:        &vlan10Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Vlan: interfacev1.VlanInterface_builder{
			VlanId: &vid10,
		}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
		Filter: filterv1.FilterFacet_builder{
			InSet:  &l2SetName,
			OutSet: &ipSetName,
		}.Build(),
	}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{vlan10}, nil, nil, nil, nil, nil, nil, nil, nil, nil, filterSets, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}

	cfg := res.Spec.Config
	if cfg.Filter == nil {
		t.Fatal("expected cfg.Filter to be non-nil for the evaluable egress set")
	}
	if _, ok := cfg.Filter.Sets[l2SetName]; ok {
		t.Errorf("sets = %v, want %q left out", cfg.Filter.Sets, l2SetName)
	}
	var boundOut bool
	for _, b := range cfg.Filter.Bindings {
		if b.Set == l2SetName {
			t.Errorf("binding %+v names the skipped set", b)
		}
		if b.Interface == vlan10Name && b.Direction == filter.Out && b.Set == ipSetName {
			boundOut = true
		}
	}
	if !boundOut {
		t.Errorf("bindings = %+v, want vlan10 out->%s", cfg.Filter.Bindings, ipSetName)
	}

	wantScope := routing.OwnershipScope("sw1", routing.DefaultVRF, vlan10Name)
	var skippedAtInterface bool
	for _, issue := range res.Metadata.Issues() {
		if issue.Code == netmodel.IssueMissingFilterSet {
			t.Errorf("unexpected issue %+v: the set exists but is skipped", issue)
		}
		if issue.Code == netmodel.IssueSkippedUnsupportedFacet && issue.Scope.Compare(wantScope) == 0 {
			skippedAtInterface = true
		}
	}
	if !skippedAtInterface {
		t.Errorf("issues = %+v, want issue %s with scope %s", res.Metadata.Issues(), netmodel.IssueSkippedUnsupportedFacet, wantScope)
	}
}

func TestLoad_FilterICMPMatchNarrowingReportsIssue(t *testing.T) {
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vid10 := uint32(10)
	vlan10Name := "vlan10"
	setName := "icmp-set"
	ruleName := "icmp-echo"
	protoICMP := packetv1.IpProtocol_IP_PROTOCOL_ICMP
	acceptAction := filterv1.FilterAction_FILTER_ACTION_ACCEPT

	rule := filterv1.FilterRule_builder{
		Name:   &ruleName,
		Action: &acceptAction,
		Match: filterv1.FilterMatch_builder{
			Protocol: &protoICMP,
			Icmp: packetv1.IcmpMatch_builder{
				V4: packetv1.Icmpv4Match_builder{
					Types: []uint32{8, 0},
				}.Build(),
			}.Build(),
		}.Build(),
	}.Build()

	set := filterv1.FilterRuleSet_builder{
		Name:    &setName,
		Default: &acceptAction,
		Rules:   []*filterv1.FilterRule{rule},
	}.Build()

	if err := protovalidate.Validate(set); err != nil {
		t.Fatalf("filter set validation failed: %v", err)
	}

	vlan10 := interfacev1.Interface_builder{
		Name:        &vlan10Name,
		AdminStatus: &adminUp,
		OperStatus:  &operUp,
		Vlan: interfacev1.VlanInterface_builder{
			VlanId: &vid10,
		}.Build(),
		Ip: ipv1.IpFacet_builder{
			NetworkInstance: ptr("default"),
			Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
	}.Build()

	res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{vlan10}, nil, nil, nil, nil, nil, nil, nil, nil, nil, []*filterv1.FilterRuleSet{set}, nil)
	if err != nil {
		t.Fatalf("netmodel.Load failed: %v", err)
	}

	if res.Readiness() == analysis.Complete {
		t.Errorf("readiness = %v, want degraded after narrowing an ICMP list", res.Readiness())
	}

	found := false
	for _, issue := range res.Metadata.Issues() {
		if issue.Code == netmodel.IssueSkippedUnsupportedFacet {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("issues = %+v, want issue %s for the narrowed ICMP match", res.Metadata.Issues(), netmodel.IssueSkippedUnsupportedFacet)
	}
}

func TestLoad_FilterSetWithMappedPrefixIsSkipped(t *testing.T) {
	for _, field := range []string{"source", "destination"} {
		t.Run(field, func(t *testing.T) {
			adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
			operUp := interfacev1.OperStatus_OPER_STATUS_UP
			vid10 := uint32(10)
			vlan10Name := "vlan10"
			mappedSetName := "mapped-set"
			ipSetName := "ip-set"
			ruleName := "drop-mapped"
			acceptAction := filterv1.FilterAction_FILTER_ACTION_ACCEPT
			dropAction := filterv1.FilterAction_FILTER_ACTION_DROP

			prefixes := []*addrv1.IpPrefix{protoIPv6Prefix([16]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0xff, 0xff, 10}, 104)}
			match := filterv1.FilterMatch_builder{}.Build()
			if field == "source" {
				match.SetSrcPrefixes(prefixes)
			} else {
				match.SetDstPrefixes(prefixes)
			}
			mappedSet := filterv1.FilterRuleSet_builder{
				Name:    new(mappedSetName),
				Default: new(acceptAction),
				Rules: []*filterv1.FilterRule{filterv1.FilterRule_builder{
					Name:   new(ruleName),
					Action: new(dropAction),
					Match:  match,
				}.Build()},
			}.Build()
			ipSet := filterv1.FilterRuleSet_builder{
				Name:    new(ipSetName),
				Default: new(acceptAction),
			}.Build()

			filterSets := []*filterv1.FilterRuleSet{mappedSet, ipSet}
			for _, s := range filterSets {
				if err := protovalidate.Validate(s); err != nil {
					t.Fatalf("filter set validation failed: %v", err)
				}
			}

			vlan10 := interfacev1.Interface_builder{
				Name:        new(vlan10Name),
				AdminStatus: new(adminUp),
				OperStatus:  new(operUp),
				Vlan: interfacev1.VlanInterface_builder{
					VlanId: new(vid10),
				}.Build(),
				Ip: ipv1.IpFacet_builder{
					NetworkInstance: new("default"),
					Ipv4:            ipv1.Ipv4Facet_builder{}.Build(),
				}.Build(),
				Filter: filterv1.FilterFacet_builder{
					InSet:  new(mappedSetName),
					OutSet: new(ipSetName),
				}.Build(),
			}.Build()

			res, err := netmodel.Load(testTime, netmodel.SourceContext{DeviceID: "sw1"}, []*interfacev1.Interface{vlan10}, nil, nil, nil, nil, nil, nil, nil, nil, nil, filterSets, nil)
			if err != nil {
				t.Fatalf("netmodel.Load failed: %v", err)
			}

			cfg := res.Spec.Config
			if cfg.Filter == nil {
				t.Fatal("expected cfg.Filter to be non-nil for the evaluable egress set")
			}
			if _, ok := cfg.Filter.Sets[mappedSetName]; ok {
				t.Errorf("sets = %v, want %q left out", cfg.Filter.Sets, mappedSetName)
			}
			var boundOut bool
			for _, b := range cfg.Filter.Bindings {
				if b.Set == mappedSetName {
					t.Errorf("binding %+v names the skipped set", b)
				}
				if b.Interface == vlan10Name && b.Direction == filter.Out && b.Set == ipSetName {
					boundOut = true
				}
			}
			if !boundOut {
				t.Errorf("bindings = %+v, want vlan10 out->%s", cfg.Filter.Bindings, ipSetName)
			}

			wantScope := routing.OwnershipScope("sw1", routing.DefaultVRF, vlan10Name)
			var skippedAtInterface bool
			for _, issue := range res.Metadata.Issues() {
				if issue.Code == netmodel.IssueMissingFilterSet {
					t.Errorf("unexpected issue %+v: the set exists but is skipped", issue)
				}
				if issue.Code == netmodel.IssueSkippedUnsupportedFacet && issue.Scope.Compare(wantScope) == 0 {
					skippedAtInterface = true
				}
			}
			if !skippedAtInterface {
				t.Errorf("issues = %+v, want issue %s with scope %s", res.Metadata.Issues(), netmodel.IssueSkippedUnsupportedFacet, wantScope)
			}
		})
	}
}
