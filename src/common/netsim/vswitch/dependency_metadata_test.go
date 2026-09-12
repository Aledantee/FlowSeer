package vswitch_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
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
			mustSwitchLearn(t, sw, []bridge.Seed{{MAC: macH2, Port: "out", Static: true}})

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
