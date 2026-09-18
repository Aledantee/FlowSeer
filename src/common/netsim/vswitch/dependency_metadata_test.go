package vswitch_test

import (
	"net/netip"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func TestHubCompletenessUsesEveryEligibleEgressState(t *testing.T) {
	for _, test := range []struct {
		name       string
		state      port.LinkState
		wantStatus analysis.Status
		wantEgress int
	}{
		{name: "unknown can change egress", state: port.Unknown, wantStatus: analysis.Incomplete},
		{name: "known down is definite", state: port.Down, wantStatus: analysis.Complete},
		{name: "known up forwards", state: port.Up, wantStatus: analysis.Complete, wantEgress: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ports := mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: test.state}))
			sw := mustSwitch(t, vswitch.Config{Ports: ports})

			res := sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
			if got := res.Metadata.Status(); got != test.wantStatus {
				t.Fatalf("hub status = %s, want %s; issues: %+v", got, test.wantStatus, res.Metadata.Issues())
			}
			if got := len(res.Egress); got != test.wantEgress {
				t.Errorf("hub egress count = %d, want %d", got, test.wantEgress)
			}
			if test.state == port.Unknown {
				assertSingleUnknownPortIssue(t, res)
			}
		})
	}
}

func TestBridgeFloodCompletenessUsesEligibleButNotIrrelevantUnknownPorts(t *testing.T) {
	for _, test := range []struct {
		name       string
		state      port.LinkState
		wantStatus analysis.Status
		wantEgress int
	}{
		{name: "unknown can change egress", state: port.Unknown, wantStatus: analysis.Incomplete},
		{name: "known down is definite", state: port.Down, wantStatus: analysis.Complete},
		{name: "known up forwards", state: port.Up, wantStatus: analysis.Complete, wantEgress: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ports := mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: test.state}))
			sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})

			res := sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
			if got := res.Metadata.Status(); got != test.wantStatus {
				t.Fatalf("bridge status = %s, want %s; issues: %+v", got, test.wantStatus, res.Metadata.Issues())
			}
			if got := len(res.Egress); got != test.wantEgress {
				t.Errorf("bridge egress count = %d, want %d", got, test.wantEgress)
			}
			if test.state == port.Unknown {
				assertSingleUnknownPortIssue(t, res)
			}
		})
	}

	t.Run("unknown VLAN non-member is irrelevant", func(t *testing.T) {
		vid := vlan.ID(10)
		ports := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "unrelated", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown}))
		sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{vid: "ten"},
			Switchports: map[string]bridge.Switchport{
				"in":  {PVID: &vid, Untagged: []vlan.ID{vid}},
				"out": {PVID: &vid, Untagged: []vlan.ID{vid}},
			},
		}}})

		res := sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
		if got := res.Metadata.Status(); got != analysis.Complete {
			t.Fatalf("bridge status = %s, want %s; issues: %+v", got, analysis.Complete, res.Metadata.Issues())
		}
		if got := len(res.Egress); got != 1 {
			t.Errorf("bridge egress count = %d, want 1", got)
		}
	})
}

func TestKnownBridgeEgressUsesItsOwnPortState(t *testing.T) {
	for _, test := range []struct {
		name       string
		admin      port.LinkState
		oper       port.LinkState
		wantStatus analysis.Status
	}{
		{name: "unknown is incomplete", admin: port.Up, oper: port.Unknown, wantStatus: analysis.Incomplete},
		{name: "known down is complete", admin: port.Down, oper: port.Down, wantStatus: analysis.Complete},
	} {
		t.Run(test.name, func(t *testing.T) {
			ports := mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: test.admin, OperStatus: test.oper}).
				Add(port.Port{Name: "unrelated", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown}))
			sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
			mustSwitchLearn(t, sw, []bridge.Seed{{MAC: macH2, Port: "out", Lifetime: bridge.Static}})

			res := sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
			if got := res.Metadata.Status(); got != test.wantStatus {
				t.Fatalf("bridge status = %s, want %s; issues: %+v", got, test.wantStatus, res.Metadata.Issues())
			}
			if test.wantStatus == analysis.Incomplete {
				assertSingleUnknownPortIssue(t, res)
			} else if issues := res.Metadata.Issues(); len(issues) != 0 {
				t.Errorf("known-down bridge egress issues = %+v, want none", issues)
			}
		})
	}
}

func TestRoutedEgressCompletenessUsesConsultedPortState(t *testing.T) {
	for _, test := range []struct {
		name       string
		admin      port.LinkState
		oper       port.LinkState
		wantStatus analysis.Status
	}{
		{name: "unknown is incomplete", admin: port.Up, oper: port.Unknown, wantStatus: analysis.Incomplete},
		{name: "known down is complete", admin: port.Down, oper: port.Down, wantStatus: analysis.Complete},
	} {
		t.Run(test.name, func(t *testing.T) {
			ports := mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: test.admin, OperStatus: test.oper}))
			cfg := vswitch.Config{
				Ports: ports,
				Routing: &routing.Config{VRFs: map[string]routing.VRF{
					routing.DefaultVRF: {
						Interfaces: map[string]routing.Interface{
							"inside":  {Port: "in", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"outside": {Port: "out", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
						Neighbors: []routing.Neighbor{{Interface: "outside", Addr: ipH2, MAC: macH2}},
					},
				}},
			}
			sw := mustSwitch(t, cfg)
			frame := ethernet.Frame{
				Src:       macH1,
				Dst:       macRouter,
				EtherType: ethernet.EtherTypeIPv4,
				Payload:   makeIPv4Packet(t, ipH1, ipH2, 64, []byte("payload")),
			}

			res := sw.Forward(fixedTime, "in", frame)
			if got := res.Metadata.Status(); got != test.wantStatus {
				t.Fatalf("routed status = %s, want %s; issues: %+v", got, test.wantStatus, res.Metadata.Issues())
			}
			if test.wantStatus == analysis.Incomplete {
				assertSingleUnknownPortIssue(t, res)
			} else if issues := res.Metadata.Issues(); len(issues) != 0 {
				t.Errorf("known-down routed egress issues = %+v, want none", issues)
			}
		})
	}
}

func assertSingleUnknownPortIssue(t *testing.T, res vswitch.ForwardResult) {
	t.Helper()
	if !traceHasFactType(res.Steps, "port.forwarding") {
		t.Errorf("unknown-port trace has no port forwarding fact: %+v", res.Steps)
	}
	issues := res.Metadata.Issues()
	if got := res.Metadata.Status(); got != analysis.Incomplete {
		t.Fatalf("status = %s, want %s; issues: %+v", got, analysis.Incomplete, issues)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want one unknown-port issue", issues)
	}
	if want := analysis.PortScope("", "out"); issues[0].Scope.Compare(want) != 0 {
		t.Errorf("issue scope = %s, want %s", issues[0].Scope, want)
	}
}

func TestComposeForwardResultUsesSwitchOwnedDependencyMetadata(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "unrelated", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	catalog := analysis.EvidenceCatalog{}
	issues := make([]analysis.Issue, 0, 5)
	for _, scoped := range []struct {
		name  string
		scope analysis.Scope
	}{
		{name: "node", scope: analysis.NodeScope("sw1")},
		{name: "member", scope: analysis.PortScope("sw1", "member")},
		{name: "lag", scope: analysis.PortScope("sw1", "lag1")},
		{name: "missing", scope: analysis.PortScope("sw1", "missing")},
		{name: "unrelated", scope: analysis.PortScope("sw1", "unrelated")},
	} {
		var ref trace.EvidenceRef
		catalog, ref = catalog.Add(analysis.Evidence{
			Kind:    "snapshot",
			Origin:  scoped.name,
			Context: "conflicting loaded state",
		})
		issues = append(issues, analysis.Issue{
			Code:     analysis.IssueCode("test." + scoped.name),
			Status:   analysis.Unsupported,
			Scope:    scoped.scope,
			Message:  scoped.name + " is unsupported",
			Evidence: []trace.EvidenceRef{ref},
		})
	}
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{Ports: ports},
		NodeID: "sw1",
		Metadata: analysis.NewMetadata(
			analysis.NodeScope("sw1"),
			issues,
			catalog,
			nil,
		),
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	member := sw.ComposeForwardResult(bridge.Result{Ingress: "member"}, "member")
	if member.Metadata.Status() != analysis.Unsupported {
		t.Fatalf("member status = %s, want Unsupported", member.Metadata.Status())
	}
	wantMemberCodes := map[analysis.IssueCode]bool{
		"test.node":   false,
		"test.member": false,
		"test.lag":    false,
	}
	for _, issue := range member.Metadata.Issues() {
		if _, ok := wantMemberCodes[issue.Code]; !ok {
			t.Errorf("member result contains unrelated issue %q", issue.Code)
			continue
		}
		wantMemberCodes[issue.Code] = true
		if len(issue.Evidence) != 1 {
			t.Errorf("issue %q evidence = %+v, want one reference", issue.Code, issue.Evidence)
		}
	}
	for code, found := range wantMemberCodes {
		if !found {
			t.Errorf("member result missing issue %q", code)
		}
	}

	missing := sw.ComposeForwardResult(bridge.Result{Ingress: "missing"}, "missing")
	if missing.Metadata.Status() != analysis.Unsupported {
		t.Fatalf("missing status = %s, want Unsupported", missing.Metadata.Status())
	}
	foundMissingEvidence := false
	for _, issue := range missing.Metadata.Issues() {
		if issue.Code == "test.missing" && len(issue.Evidence) == 1 {
			foundMissingEvidence = true
		}
		if issue.Code == "test.unrelated" || issue.Code == "test.member" || issue.Code == "test.lag" {
			t.Errorf("missing result contains unrelated issue %q", issue.Code)
		}
	}
	if !foundMissingEvidence {
		t.Errorf("missing ingress result lacks its loaded issue evidence: %+v", missing.Metadata.Issues())
	}
}

func TestForwardingMetadataIncludesOnlyConsultedProtocolScope(t *testing.T) {
	deviceMAC := netaddr.MAC{2, 0, 0, 0, 0, 1}
	vid := vlan.ID(10)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "routed", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "bridged", AdminStatus: port.Up, OperStatus: port.Up}))
	stpScope := analysis.ProtocolScope("sw1", string(port.LayerStp), "0")
	catalog, ref := analysis.EvidenceCatalog{}.Add(analysis.Evidence{
		Kind:    "snapshot",
		Origin:  "inventory",
		Context: "the spanning tree protocol state is incomplete",
	})
	catalog, assumptionRef := catalog.Add(analysis.Evidence{
		Kind:    "default",
		Origin:  "inventory",
		Context: "the spanning tree fallback was applied",
	})
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{
			MAC:   deviceMAC,
			Ports: ports,
			Bridge: &bridge.Config{VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{vid: "bridged"},
				Switchports: map[string]bridge.Switchport{
					"bridged": {PVID: &vid, Untagged: []vlan.ID{vid}},
				},
			}},
			STP: &stp.Config{
				Address: deviceMAC,
				Ports: map[string]stp.Port{
					"bridged": {},
				},
			},
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
					"routed": {
						Port:     "routed",
						MAC:      deviceMAC,
						Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
					},
				}},
			}},
		},
		NodeID: "sw1",
		Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
			Code:     "test.stp.protocol",
			Status:   analysis.Unsupported,
			Scope:    stpScope,
			Message:  "the spanning tree protocol state is incomplete",
			Evidence: []trace.EvidenceRef{ref},
		}}, catalog, []analysis.Assumption{{
			Scope:     stpScope,
			Statement: "the spanning tree fallback was applied",
			Evidence:  []trace.EvidenceRef{assumptionRef},
		}}),
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	bridgeResult := sw.Peek(fixedTime, "bridged", ethernet.Frame{Src: macH1, Dst: macH2})
	if bridgeResult.Metadata.Status() != analysis.Unsupported {
		t.Fatalf("bridge status = %s, want Unsupported; issues: %+v", bridgeResult.Metadata.Status(), bridgeResult.Metadata.Issues())
	}
	if issues := bridgeResult.Metadata.Issues(); len(issues) != 1 || issues[0].Scope.Compare(stpScope) != 0 {
		t.Errorf("bridge issues = %+v, want the STP protocol issue", issues)
	}
	if _, ok := bridgeResult.Metadata.Evidence().Lookup(ref); !ok {
		t.Errorf("bridge evidence = %+v, want %q", bridgeResult.Metadata.Evidence().Entries(), ref)
	}
	if assumptions := bridgeResult.Metadata.Assumptions(); len(assumptions) != 1 || assumptions[0].Scope.Compare(stpScope) != 0 {
		t.Errorf("bridge assumptions = %+v, want the STP-scoped assumption", assumptions)
	}
	if _, ok := bridgeResult.Metadata.Evidence().Lookup(assumptionRef); !ok {
		t.Errorf("bridge evidence = %+v, want assumption reference %q", bridgeResult.Metadata.Evidence().Entries(), assumptionRef)
	}
	bridgeSTPField := analysis.FieldScope(stpScope, "ports", "bridged")
	if !slices.ContainsFunc(bridgeResult.ConsultedScopes(), func(scope analysis.Scope) bool {
		return scope.Compare(bridgeSTPField) == 0
	}) {
		t.Errorf("bridge consulted scopes = %v, want %s", bridgeResult.ConsultedScopes(), bridgeSTPField)
	}

	hdr := ip.Header{
		Src:      netip.MustParseAddr("192.0.2.2"),
		Dst:      netip.MustParseAddr("198.51.100.2"),
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	payload, err := hdr.Encode([]byte("routing bypasses spanning tree"))
	if err != nil {
		t.Fatalf("encode packet: %v", err)
	}
	routedResult := sw.Peek(fixedTime, "routed", ethernet.Frame{
		Src:       macH1,
		Dst:       deviceMAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	})
	if routedResult.Reason != routing.ReasonNoRoute {
		t.Fatalf("routed reason = %s, want no-route", routedResult.Reason)
	}
	if routedResult.Metadata.Status() != analysis.Complete || len(routedResult.Metadata.Issues()) != 0 {
		t.Errorf("routed metadata = %s %+v, want Complete without the unconsulted STP issue", routedResult.Metadata.Status(), routedResult.Metadata.Issues())
	}
	if assumptions := routedResult.Metadata.Assumptions(); len(assumptions) != 0 {
		t.Errorf("routed assumptions = %+v, want no unconsulted STP assumption", assumptions)
	}
	if _, ok := routedResult.Metadata.Evidence().Lookup(assumptionRef); ok {
		t.Errorf("routed evidence retained unconsulted STP assumption reference %q", assumptionRef)
	}
	routingScope := routing.RouteLookupScope("sw1", routing.DefaultVRF, netip.MustParseAddr("198.51.100.2"))
	if !slices.ContainsFunc(routedResult.ConsultedScopes(), func(scope analysis.Scope) bool {
		return scope.Compare(routingScope) == 0
	}) {
		t.Errorf("routed consulted scopes = %v, want %s", routedResult.ConsultedScopes(), routingScope)
	}
}

func TestNegativeRoutingCapabilityLookupsRetainScopedUncertainty(t *testing.T) {
	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	bridgeConfig := &bridge.Config{VLAN: &bridge.VLAN{
		Table: map[vlan.ID]string{vid10: "ten", vid20: "twenty"},
		Switchports: map[string]bridge.Switchport{
			"in":  {PVID: &vid10, Untagged: []vlan.ID{vid10}},
			"out": {PVID: &vid10, Untagged: []vlan.ID{vid10}},
		},
	}}
	routingOnVLAN := func(vid vlan.ID, name string) *routing.Config {
		return &routing.Config{VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
				name: {VLAN: vid, MAC: macRouter},
			}},
		}}
	}

	for _, test := range []struct {
		name    string
		routing *routing.Config
		scope   analysis.Scope
	}{
		{
			name:    "ByPort miss",
			routing: routingOnVLAN(vid10, "vlan10"),
			scope:   routing.PortLookupScope("sw1", routing.DefaultVRF, "in"),
		},
		{
			name:    "ByVLAN miss",
			routing: routingOnVLAN(vid20, "vlan20"),
			scope:   routing.VLANLookupScope("sw1", routing.DefaultVRF, vid10),
		},
		{
			name:    "Owns false",
			routing: routingOnVLAN(vid10, "vlan10"),
			scope:   routing.OwnershipScope("sw1", routing.DefaultVRF, "vlan10"),
		},
		{
			name:  "absent routing layer",
			scope: routing.VLANLookupScope("sw1", routing.DefaultVRF, vid10),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog, relevantRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{
				Kind: "snapshot", Origin: "inventory", Context: "relevant routing uncertainty",
			})
			catalog, siblingRef := catalog.Add(analysis.Evidence{
				Kind: "snapshot", Origin: "inventory", Context: "unrelated routing uncertainty",
			})
			siblingScope := routing.VLANLookupScope("sw1", routing.DefaultVRF, 99)
			sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
				Config: vswitch.Config{Ports: ports, Bridge: bridgeConfig, Routing: test.routing},
				NodeID: "sw1",
				Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
					{
						Code: "test.routing.relevant", Status: analysis.Incomplete, Scope: test.scope,
						Message: "the consulted routing lookup is incomplete", Evidence: []trace.EvidenceRef{relevantRef},
					},
					{
						Code: "test.routing.sibling", Status: analysis.Unsupported, Scope: siblingScope,
						Message: "an unrelated routing lookup is unsupported", Evidence: []trace.EvidenceRef{siblingRef},
					},
				}, catalog, nil),
			})
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			result := sw.Peek(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
			if result.Metadata.Status() != analysis.Incomplete {
				t.Fatalf("status = %s, want Incomplete; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
			}
			if issues := result.Metadata.Issues(); len(issues) != 1 || issues[0].Code != "test.routing.relevant" {
				t.Fatalf("issues = %+v, want only the relevant routing uncertainty", issues)
			}
			if _, ok := result.Metadata.Evidence().Lookup(relevantRef); !ok {
				t.Errorf("forward metadata lacks relevant evidence %q", relevantRef)
			}
			if _, ok := result.Metadata.Evidence().Lookup(siblingRef); ok {
				t.Errorf("forward metadata retained unrelated evidence %q", siblingRef)
			}
			if !slices.ContainsFunc(result.ConsultedScopes(), func(scope analysis.Scope) bool {
				return scope.Compare(test.scope) == 0
			}) {
				t.Errorf("consulted scopes = %v, want %s", result.ConsultedScopes(), test.scope)
			}
		})
	}
}

// TestNegativeSubInterfaceTagMissRetainsScopedUncertainty proves that a routed port carrying
// a sub-interface, given a frame tagged at a VID none of its sub-interfaces claim, consults
// the port-and-VID scope [routing.PortVLANLookupScope] rather than the bare port scope
// [TestNegativeRoutingCapabilityLookupsRetainScopedUncertainty]'s "ByPort miss" case consults
// for a port with no routed interface at all: the two misses answer different questions and
// must retain different uncertainty.
func TestNegativeSubInterfaceTagMissRetainsScopedUncertainty(t *testing.T) {
	vid10 := vlan.ID(10)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	scope := routing.PortVLANLookupScope("sw1", routing.DefaultVRF, "in", 20)

	catalog, relevantRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{
		Kind: "snapshot", Origin: "inventory", Context: "relevant routing uncertainty",
	})
	catalog, siblingRef := catalog.Add(analysis.Evidence{
		Kind: "snapshot", Origin: "inventory", Context: "unrelated routing uncertainty",
	})
	siblingScope := routing.VLANLookupScope("sw1", routing.DefaultVRF, 99)

	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {Interfaces: map[string]routing.Interface{
					"in.10": {Port: "in", VLAN: vid10, MAC: macRouter},
				}},
			}},
		},
		NodeID: "sw1",
		Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
			{
				Code: "test.routing.relevant", Status: analysis.Incomplete, Scope: scope,
				Message: "the consulted routing lookup is incomplete", Evidence: []trace.EvidenceRef{relevantRef},
			},
			{
				Code: "test.routing.sibling", Status: analysis.Unsupported, Scope: siblingScope,
				Message: "an unrelated routing lookup is unsupported", Evidence: []trace.EvidenceRef{siblingRef},
			},
		}, catalog, nil),
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	result := sw.Peek(fixedTime, "in", ethernet.Frame{
		Src: macH1,
		Dst: macH2,
		Tags: []vlan.Tag{
			{TPID: uint16(ethernet.EtherTypeDot1Q), VID: 20},
		},
	})
	if result.Metadata.Status() != analysis.Incomplete {
		t.Fatalf("status = %s, want Incomplete; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
	}
	if issues := result.Metadata.Issues(); len(issues) != 1 || issues[0].Code != "test.routing.relevant" {
		t.Fatalf("issues = %+v, want only the relevant routing uncertainty", issues)
	}
	if !slices.ContainsFunc(result.ConsultedScopes(), func(s analysis.Scope) bool {
		return s.Compare(scope) == 0
	}) {
		t.Errorf("consulted scopes = %v, want %s", result.ConsultedScopes(), scope)
	}
}

func TestComposeForwardResultTreatsMissingDependencyAsUnknown(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "present", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports})

	res := sw.ComposeForwardResult(bridge.Result{}, "missing")
	if got := res.Metadata.Status(); got != analysis.Incomplete {
		t.Fatalf("status = %s, want %s; issues: %+v", got, analysis.Incomplete, res.Metadata.Issues())
	}
	issues := res.Metadata.Issues()
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want one missing-dependency issue", issues)
	}
	wantScope := analysis.PortScope("", "missing")
	if issues[0].Code != "unknown-operational-status" || issues[0].Scope.Compare(wantScope) != 0 {
		t.Errorf("issue = %+v, want unknown-operational-status at %s", issues[0], wantScope)
	}
	consulted := res.ConsultedPorts()
	if len(consulted) != 1 || consulted[0].AdminStatus != port.Unknown || consulted[0].OperStatus != port.Unknown {
		t.Errorf("consulted ports = %+v, want missing placeholder with explicit Unknown states", consulted)
	}
}

func TestMissingIngressRetainsForwardingDependencyMetadata(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "present", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	catalog, loadedRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{
		Kind:    "snapshot",
		Origin:  "inventory",
		Context: "missing ingress state was unavailable",
	})
	metadata := analysis.NewMetadata(
		analysis.PortScope("sw1", "present"),
		[]analysis.Issue{{
			Code:     "test.missing-ingress",
			Status:   analysis.Incomplete,
			Scope:    analysis.PortScope("sw1", "missing"),
			Message:  "missing ingress state was unavailable",
			Evidence: []trace.EvidenceRef{loadedRef},
		}},
		catalog,
		nil,
	)

	bpduFrame := ethernet.Frame{
		Src: netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		Dst: netaddr.MAC{0x01, 0x80, 0xc2, 0, 0, 0},
	}
	dataFrame := ethernet.Frame{Src: macH1, Dst: macH2}
	tests := []struct {
		name  string
		cfg   vswitch.Config
		frame ethernet.Frame
	}{
		{name: "hub", cfg: vswitch.Config{Ports: ports}, frame: dataFrame},
		{name: "bridge", cfg: vswitch.Config{Ports: ports, Bridge: &bridge.Config{}}, frame: dataFrame},
		{name: "BPDU", cfg: vswitch.Config{Ports: ports, Bridge: &bridge.Config{}, STP: &stp.Config{}}, frame: bpduFrame},
		{
			name: "routing",
			cfg: vswitch.Config{
				Ports: ports,
				Routing: &routing.Config{VRFs: map[string]routing.VRF{
					routing.DefaultVRF: {
						Interfaces: map[string]routing.Interface{
							"present": {
								Port:     "present",
								Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")},
							},
						},
					},
				}},
			},
			frame: dataFrame,
		},
	}

	for _, test := range tests {
		for _, operation := range []struct {
			name string
			run  func(*vswitch.Switch) vswitch.ForwardResult
		}{
			{name: "Forward", run: func(sw *vswitch.Switch) vswitch.ForwardResult {
				return sw.Forward(fixedTime, "missing", test.frame)
			}},
			{name: "Peek", run: func(sw *vswitch.Switch) vswitch.ForwardResult {
				return sw.Peek(fixedTime, "missing", test.frame)
			}},
		} {
			t.Run(test.name+"/"+operation.name, func(t *testing.T) {
				sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
					Config: test.cfg, NodeID: "sw1", Metadata: metadata,
				})
				if err != nil {
					t.Fatalf("NewWithSpec: %v", err)
				}

				res := operation.run(sw)
				assertMissingIngressDependency(t, res, loadedRef)
			})
		}
	}
}

func assertMissingIngressDependency(t *testing.T, res vswitch.ForwardResult, loadedRef trace.EvidenceRef) {
	t.Helper()

	consulted := res.ConsultedPorts()
	want := (port.Port{Name: "missing"}).Normalize()
	if len(consulted) != 1 || consulted[0] != want {
		t.Fatalf("consulted ports = %+v, want normalized placeholder %+v", consulted, want)
	}

	wantCodes := map[analysis.IssueCode]bool{
		"forwarding-dependency-outside-loaded-scope": false,
		"test.missing-ingress":                       false,
		"unknown-operational-status":                 false,
	}
	for _, issue := range res.Metadata.Issues() {
		if _, ok := wantCodes[issue.Code]; !ok {
			continue
		}
		wantCodes[issue.Code] = true
		if len(issue.Evidence) == 0 {
			t.Errorf("issue %q has no evidence", issue.Code)
		}
		for _, ref := range issue.Evidence {
			if ref == "" {
				t.Errorf("issue %q has an empty evidence reference", issue.Code)
				continue
			}
			evidence, ok := res.Metadata.Evidence().Lookup(ref)
			if !ok {
				t.Errorf("issue %q evidence %q is absent from catalog", issue.Code, ref)
				continue
			}
			if issue.Code != "test.missing-ingress" &&
				(evidence.Kind != "vswitch.runtime" || evidence.Origin != "forward" || evidence.Context == issue.Message) {
				t.Errorf("issue %q evidence = %+v, want stable runtime evidence", issue.Code, evidence)
			}
		}
	}
	for code, found := range wantCodes {
		if !found {
			t.Errorf("issues = %+v, want %q", res.Metadata.Issues(), code)
		}
	}
	if _, ok := res.Metadata.Evidence().Lookup(loadedRef); !ok {
		t.Errorf("evidence = %+v, want loaded reference %q", res.Metadata.Evidence().Entries(), loadedRef)
	}
}

func TestForwardingMetadataDoesNotOverstateLoadedCoverage(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "p2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	tests := []struct {
		name             string
		loadedScope      analysis.Scope
		dependencies     []string
		wantStatus       analysis.Status
		wantCoveragePort string
	}{
		{name: "child scope excludes consulted sibling", loadedScope: analysis.PortScope("sw1", "p1"), dependencies: []string{"p2"}, wantStatus: analysis.Incomplete, wantCoveragePort: "p2"},
		{name: "child scope covers consulted port", loadedScope: analysis.PortScope("sw1", "p1"), dependencies: []string{"p1"}, wantStatus: analysis.Complete},
		{name: "child scope with no dependencies", loadedScope: analysis.PortScope("sw1", "p1"), wantStatus: analysis.Complete},
		{name: "node scope covers sibling", loadedScope: analysis.NodeScope("sw1"), dependencies: []string{"p2"}, wantStatus: analysis.Complete},
		{name: "whole scope covers sibling", loadedScope: analysis.WholeScope(), dependencies: []string{"p2"}, wantStatus: analysis.Complete},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
				Config:   vswitch.Config{Ports: ports},
				NodeID:   "sw1",
				Metadata: analysis.NewMetadata(test.loadedScope, nil, analysis.EvidenceCatalog{}, nil),
			})
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			res := sw.ComposeForwardResult(bridge.Result{}, test.dependencies...)
			if got := res.Metadata.Status(); got != test.wantStatus {
				t.Fatalf("status = %s, want %s; issues: %+v", got, test.wantStatus, res.Metadata.Issues())
			}
			if got := res.Metadata.Scope(); got.Compare(analysis.NodeScope("sw1")) != 0 {
				t.Errorf("scope = %s, want %s", got, analysis.NodeScope("sw1"))
			}
			issues := res.Metadata.Issues()
			if test.wantCoveragePort == "" {
				if len(issues) != 0 {
					t.Errorf("issues = %+v, want none", issues)
				}
				return
			}
			if len(issues) != 1 {
				t.Fatalf("issues = %+v, want one coverage issue", issues)
			}
			wantScope := analysis.PortScope("sw1", test.wantCoveragePort)
			if issues[0].Code != "forwarding-dependency-outside-loaded-scope" || issues[0].Scope.Compare(wantScope) != 0 {
				t.Errorf("coverage issue = %+v, want scoped issue at %s", issues[0], wantScope)
			}
		})
	}
}

func TestNamedSwitchKeepsWholeConstructionMetadataWithoutPortDependencies(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	catalog, ref := analysis.EvidenceCatalog{}.Add(analysis.Evidence{
		Kind:    "snapshot",
		Origin:  "inventory",
		Context: "switch-wide state was unavailable",
	})
	metadata := analysis.NewMetadata(
		analysis.NodeScope("sw1"),
		[]analysis.Issue{{
			Code:     "test.whole",
			Status:   analysis.Unsupported,
			Scope:    analysis.WholeScope(),
			Message:  "the source could not model switch-wide state",
			Evidence: []trace.EvidenceRef{ref},
		}},
		catalog,
		[]analysis.Assumption{{
			Scope:     analysis.WholeScope(),
			Statement: "the unavailable state affects every switch result",
			Evidence:  []trace.EvidenceRef{ref},
		}},
	)
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{Ports: ports}, NodeID: "sw1", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	res := sw.ComposeForwardResult(bridge.Result{})
	if got := res.Metadata.Status(); got != analysis.Unsupported {
		t.Errorf("status = %s, want Unsupported", got)
	}
	if issues := res.Metadata.Issues(); len(issues) != 1 || issues[0].Code != "test.whole" {
		t.Errorf("issues = %+v, want whole-scope construction issue", issues)
	}
	if assumptions := res.Metadata.Assumptions(); len(assumptions) != 1 || assumptions[0].Statement == "" {
		t.Errorf("assumptions = %+v, want whole-scope construction assumption", assumptions)
	}
	if _, ok := res.Metadata.Evidence().Lookup(ref); !ok {
		t.Errorf("evidence = %+v, want %q", res.Metadata.Evidence().Entries(), ref)
	}
}

func TestMirrorOutputDropUsesIngressDependencyMetadata(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "mirror", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown}))
	catalog, ref := analysis.EvidenceCatalog{}.Add(analysis.Evidence{
		Kind:    "snapshot",
		Origin:  "switch",
		Context: "mirror output state is incomplete",
	})
	metadata := analysis.NewMetadata(
		analysis.NodeScope("sw1"),
		[]analysis.Issue{{
			Code:     "test.mirror-output",
			Status:   analysis.Unsupported,
			Scope:    analysis.PortScope("sw1", "mirror"),
			Message:  "mirror output cannot be modeled",
			Evidence: []trace.EvidenceRef{ref},
		}},
		catalog,
		nil,
	)
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{
			Ports: ports,
			Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
				Name:       "span",
				OutputPort: "mirror",
			}}},
		},
		NodeID:   "sw1",
		Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	res := sw.Forward(fixedTime, "mirror", ethernet.Frame{Src: macH1, Dst: macH2})
	if res.Reason != traffic.ReasonMirrorOutput {
		t.Fatalf("reason = %s, want %s", res.Reason, traffic.ReasonMirrorOutput)
	}
	wantCodes := map[analysis.IssueCode]bool{
		"unknown-operational-status": false,
		"test.mirror-output":         false,
	}
	for _, issue := range res.Metadata.Issues() {
		if _, ok := wantCodes[issue.Code]; ok {
			wantCodes[issue.Code] = true
		}
	}
	for code, found := range wantCodes {
		if !found {
			t.Errorf("issues = %+v, want %q", res.Metadata.Issues(), code)
		}
	}
	if _, ok := res.Metadata.Evidence().Lookup(ref); !ok {
		t.Errorf("evidence = %+v, want %q", res.Metadata.Evidence().Entries(), ref)
	}
}
