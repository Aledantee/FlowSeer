package vswitch

import (
	"net/netip"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestPendingResultUsesMetadataWithoutTraceSubjects(t *testing.T) {
	t.Parallel()
	l, err := routing.New(routing.Config{}, layer.Env{})
	if err != nil {
		t.Fatal(err)
	}
	c := &call{}
	s := &Switch{nodeID: "sw1", routing: l, call: c}
	addr := netip.MustParseAddr("10.0.99.2")
	res := s.assembleRouteResult(c, "in", 10, 0, false, nil, "vlan10", filter.Result{}, routing.Result{
		Reason: routing.ReasonNeighborPending, Interface: "out", VRF: "tenant", NextHop: addr,
	})
	if res.Outcome != trace.Held {
		t.Fatalf("outcome = %v, want Held", res.Outcome)
	}
	issues := s.neighborUnresolvedIssues(c)
	wantScope := routing.NeighborLookupScope("sw1", "tenant", "out", addr)
	if len(issues) != 1 || issues[0].issue.Scope.Compare(wantScope) != 0 || issues[0].issue.Status != analysis.Incomplete {
		t.Fatalf("issues = %+v, want unresolved tenant/out/10.0.99.2 at Incomplete", issues)
	}
}

func TestSwitchSpeedsInternal(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	pCfg := &phy.Config{
		Ethernet: map[string]phy.Ethernet{
			"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
		},
	}

	sw, err := New(Config{Ports: ports, Phy: pCfg, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if sw.speeds == nil || len(sw.speeds) != 1 {
		t.Errorf("got resolved speeds %v, want exactly 1", sw.speeds)
	}
}

func TestLinkChangeReturnsInvalidOperStatusFault(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, first := sw.LinkChange(now, "1/1/1", port.LinkState("bogus"), device.PointToPointTrue, 1_000_000_000)
	if first == nil {
		t.Fatal("LinkChange returned nil after an invalid oper status, want a fault")
	}
	if got := errs.Attributes(first)["name"]; got != "1/1/1" {
		t.Errorf("fault names port %v, want 1/1/1", got)
	}

	_, second := sw.LinkChange(now.Add(time.Second), "1/1/2", port.LinkState("worse"), device.PointToPointTrue, 1_000_000_000)
	if second == nil || errs.Attributes(second)["name"] != "1/1/2" {
		t.Errorf("second fault = %v, want error naming 1/1/2", second)
	}
}

func TestLinkChangeValidTransitionReturnsNoFault(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	sw, err := New(Config{Ports: ports, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := sw.LinkChange(now, "1/1/1", port.Down, device.PointToPointTrue, 1_000_000_000); err != nil {
		t.Errorf("LinkChange returned %v after a valid transition, want nil", err)
	}
}

func TestReleaseHeldFrameOntoDownPortRecordsPortStatusDown(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	macRouter := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	sw, err := New(Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				"default": {
					Interfaces: map[string]routing.Interface{
						"rp1": {Port: "1/1/1", MAC: macRouter, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.1.1/24")}},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	hf := routing.HeldFrame{
		Interface: "rp1",
		Port:      "1/1/1",
		Cause:     routing.HeldReleased,
		Frame:     ethernet.Frame{Payload: make([]byte, 100)},
	}

	c, end := sw.beginCall(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true)
	sw.releaseHeldFrame(c.now, hf)
	end()
	drops := c.output(forwardResult{}).Drops
	if len(drops) != 1 {
		t.Fatalf("output drops returned %d drops, want 1", len(drops))
	}
	if drops[0].Reason != port.ReasonPortDown {
		t.Errorf("drop reason = %v, want %v", drops[0].Reason, port.ReasonPortDown)
	}
	if drops[0].Port != "1/1/1" {
		t.Errorf("drop port = %q, want 1/1/1", drops[0].Port)
	}
	if got, want := drops[0].Step.RuleID, trace.RuleID("port.status.port-down"); got != want {
		t.Errorf("drop step RuleID = %q, want literal %q", got, want)
	}
}

func TestSwitchWithoutTrafficLeavesTrafficSwitchportsNil(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	pvid := vlan.ID(10)
	sw, err := New(Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if sw.trafficSwitchports != nil {
		t.Errorf("trafficSwitchports = %v, want nil when Traffic is not configured", sw.trafficSwitchports)
	}
}

func TestSwitchHoldsNoPendingOutput(t *testing.T) {
	typ := reflect.TypeFor[Switch]()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Type == reflect.TypeFor[[]emission]() || field.Type == reflect.TypeFor[[]mirrorCopy]() || field.Type == reflect.TypeFor[[]neighborDrop]() {
			t.Errorf("Switch field %s retains output of type %s", field.Name, field.Type)
		}
	}
}

// captureOutput keeps the outputs a fixture needs to inspect across several calls.
func captureOutput(callOutputs map[*Switch]device.Output, sw *Switch, out device.Output) device.Output {
	prior := callOutputs[sw]
	prior.Emissions = append(prior.Emissions, out.Emissions...)
	prior.Drops = append(prior.Drops, out.Drops...)
	prior.Copies = out.Copies
	callOutputs[sw] = prior
	return out
}

func takeEmissions(callOutputs map[*Switch]device.Output, sw *Switch) []device.Emission {
	out := callOutputs[sw]
	emissions := out.Emissions
	out.Emissions = nil
	callOutputs[sw] = out
	return emissions
}

func takeDrops(callOutputs map[*Switch]device.Output, sw *Switch) []device.Drop {
	out := callOutputs[sw]
	drops := out.Drops
	out.Drops = nil
	callOutputs[sw] = out
	return drops
}

func TestForwardingMetadataUsesSwitchOwnedDependencyMetadata(t *testing.T) {
	ports := mustInternalTable(t, port.NewBuilder().
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up}).
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
	sw, err := NewWithSpec(ConstructionSpec{
		Config: Config{Ports: ports},
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

	c1, end1 := sw.beginCall(time.Time{}, false)
	result1 := bridge.Result{Ingress: "member"}
	for _, dependency := range []string{"member"} {
		sw.forwardingDependencies(dependency).consult(&result1)
	}
	member := sw.wrapResult(c1, result1)
	end1()
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

	c2, end2 := sw.beginCall(time.Time{}, false)
	result2 := bridge.Result{Ingress: "missing"}
	for _, dependency := range []string{"missing"} {
		sw.forwardingDependencies(dependency).consult(&result2)
	}
	missing := sw.wrapResult(c2, result2)
	end2()
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

func TestForwardingMetadataTreatsMissingDependencyAsUnknown(t *testing.T) {
	ports := mustInternalTable(t, port.NewBuilder().
		Add(port.Port{Name: "present", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustInternalSwitch(t, Config{Ports: ports})

	c1, end1 := sw.beginCall(time.Time{}, false)
	result1 := bridge.Result{}
	for _, dependency := range []string{"missing"} {
		sw.forwardingDependencies(dependency).consult(&result1)
	}
	res := sw.wrapResult(c1, result1)
	end1()
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

func TestForwardingMetadataDoesNotOverstateLoadedCoverage(t *testing.T) {
	ports := mustInternalTable(t, port.NewBuilder().
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
			sw, err := NewWithSpec(ConstructionSpec{
				Config:   Config{Ports: ports},
				NodeID:   "sw1",
				Metadata: analysis.NewMetadata(test.loadedScope, nil, analysis.EvidenceCatalog{}, nil),
			})
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			c1, end1 := sw.beginCall(time.Time{}, false)
			result1 := bridge.Result{}
			for _, dependency := range test.dependencies {
				sw.forwardingDependencies(dependency).consult(&result1)
			}
			res := sw.wrapResult(c1, result1)
			end1()
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
	ports := mustInternalTable(t, port.NewBuilder().
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
	sw, err := NewWithSpec(ConstructionSpec{
		Config: Config{Ports: ports}, NodeID: "sw1", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	c1, end1 := sw.beginCall(time.Time{}, false)
	result1 := bridge.Result{}
	for _, dependency := range []string{} {
		sw.forwardingDependencies(dependency).consult(&result1)
	}
	res := sw.wrapResult(c1, result1)
	end1()
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

func mustInternalTable(t *testing.T, builder *port.Builder) port.Table {
	t.Helper()
	table, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return table
}

func mustInternalSwitch(t *testing.T, cfg Config) *Switch {
	t.Helper()
	sw, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return sw
}
