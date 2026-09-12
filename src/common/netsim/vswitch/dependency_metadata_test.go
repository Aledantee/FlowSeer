package vswitch_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

func TestHubCompletenessUsesOnlyConsultedPortState(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "unrelated", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports})

	res := sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
	if got := res.Metadata.Status(); got != analysis.Complete {
		t.Fatalf("hub status = %s, want %s; issues: %+v", got, analysis.Complete, res.Metadata.Issues())
	}

	res = sw.Forward(fixedTime, "unrelated", ethernet.Frame{Src: macH1, Dst: macH2})
	assertSingleUnknownPortIssue(t, res, "unrelated")
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
			sw.Learn([]bridge.Seed{{MAC: macH2, Port: "out", Static: true}})

			res := sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
			if got := res.Metadata.Status(); got != test.wantStatus {
				t.Fatalf("bridge status = %s, want %s; issues: %+v", got, test.wantStatus, res.Metadata.Issues())
			}
			if test.wantStatus == analysis.Incomplete {
				assertSingleUnknownPortIssue(t, res, "out")
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
				assertSingleUnknownPortIssue(t, res, "out")
			} else if issues := res.Metadata.Issues(); len(issues) != 0 {
				t.Errorf("known-down routed egress issues = %+v, want none", issues)
			}
		})
	}
}

func assertSingleUnknownPortIssue(t *testing.T, res vswitch.ForwardResult, name string) {
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
	if want := analysis.PortScope("", name); issues[0].Scope.Compare(want) != 0 {
		t.Errorf("issue scope = %s, want %s", issues[0].Scope, want)
	}
}
