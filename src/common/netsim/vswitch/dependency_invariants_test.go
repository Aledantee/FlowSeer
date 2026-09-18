package vswitch_test

import (
	"net/netip"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func TestFDBDependencyInvariantAcrossLearningAndLookup(t *testing.T) {
	sourceMAC := netaddr.MAC{2, 0, 0, 0, 1, 1}
	destinationMAC := netaddr.MAC{2, 0, 0, 0, 2, 2}
	unrelatedSource := netaddr.MAC{2, 0, 0, 0, 3, 3}
	unrelatedDestination := netaddr.MAC{2, 0, 0, 0, 4, 4}
	group := netip.MustParseAddr("239.1.1.1")

	for _, test := range []struct {
		name     string
		frame    func(*testing.T) ethernet.Frame
		wantCode analysis.IssueCode
	}{
		{
			name: "ordinary source learning",
			frame: func(*testing.T) ethernet.Frame {
				return ethernet.Frame{Src: sourceMAC, Dst: unrelatedDestination}
			},
			wantCode: "test.fdb.source",
		},
		{
			name: "ordinary destination lookup",
			frame: func(*testing.T) ethernet.Frame {
				return ethernet.Frame{Src: unrelatedSource, Dst: destinationMAC}
			},
			wantCode: "test.fdb.destination",
		},
		{
			name: "ordinary unrelated keys",
			frame: func(*testing.T) ethernet.Frame {
				return ethernet.Frame{Src: unrelatedSource, Dst: unrelatedDestination}
			},
		},
		{
			name: "multicast control source learning",
			frame: func(t *testing.T) ethernet.Frame {
				return makeIGMPControlFrame(
					t,
					netip.MustParseAddr("192.0.2.1"),
					group,
					sourceMAC,
					mcastGroupMAC,
					1,
					igmp.Message{Type: igmp.ReportV2, Group: group},
				)
			},
			wantCode: "test.fdb.source",
		},
		{
			name: "multicast control unrelated source",
			frame: func(t *testing.T) ethernet.Frame {
				return makeIGMPControlFrame(
					t,
					netip.MustParseAddr("192.0.2.2"),
					group,
					unrelatedSource,
					mcastGroupMAC,
					1,
					igmp.Message{Type: igmp.ReportV2, Group: group},
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := mcastSwitchConfig(t, nil)
			metadata := analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
				{
					Code: "test.fdb.source", Status: analysis.Unstable,
					Scope: bridge.FDBLookupScope("sw1", 10, sourceMAC),
				},
				{
					Code: "test.fdb.destination", Status: analysis.Unstable,
					Scope: bridge.FDBLookupScope("sw1", 10, destinationMAC),
				},
			}, analysis.EvidenceCatalog{}, nil)
			sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
				Config: cfg, NodeID: "sw1", Metadata: metadata,
			})
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			result := sw.Forward(fixedTime, "1/1/1", test.frame(t))
			if test.wantCode == "" {
				if result.Metadata.Status() != analysis.Complete || len(result.Metadata.Issues()) != 0 {
					t.Fatalf("unrelated forwarding metadata = %s %+v, want Complete", result.Metadata.Status(), result.Metadata.Issues())
				}
				return
			}
			if result.Metadata.Status() != analysis.Unstable {
				t.Fatalf("metadata status = %s, want Unstable; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
			}
			if issues := result.Metadata.Issues(); len(issues) != 1 || issues[0].Code != test.wantCode {
				t.Fatalf("issues = %+v, want only %s", issues, test.wantCode)
			}
		})
	}
}

func TestBoundedFDBLearningConsultsTableState(t *testing.T) {
	candidateMAC := netaddr.MAC{2, 0, 0, 0, 5, 5}
	newSourceMAC := netaddr.MAC{2, 0, 0, 0, 6, 6}
	destinationMAC := netaddr.MAC{2, 0, 0, 0, 7, 7}

	for _, test := range []struct {
		name         string
		maxEntries   int
		wantUnstable bool
	}{
		{name: "at capacity", maxEntries: 1, wantUnstable: true},
		{name: "below capacity", maxEntries: 2, wantUnstable: true},
		{name: "unbounded", maxEntries: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := mcastSwitchConfig(t, nil)
			cfg.Mcast = nil
			cfg.Bridge.MaxEntries = test.maxEntries
			metadata := analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
				Code:   "test.fdb.candidate",
				Status: analysis.Unstable,
				Scope:  bridge.FDBLookupScope("sw1", 10, candidateMAC),
			}}, analysis.EvidenceCatalog{}, nil)
			sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
				Config: cfg,
				Seeds: []bridge.Seed{{
					FID: 10, MAC: candidateMAC, Port: "1/1/2", LearnedAt: fixedTime.Add(-1),
				}},
				NodeID:   "sw1",
				Metadata: metadata,
			})
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			result := sw.Forward(fixedTime, "1/1/1", ethernet.Frame{
				Src: newSourceMAC, Dst: destinationMAC,
			})
			if test.wantUnstable {
				if result.Metadata.Status() != analysis.Unstable {
					t.Fatalf("metadata status = %s, want Unstable; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
				}
				if issues := result.Metadata.Issues(); len(issues) != 1 || issues[0].Code != "test.fdb.candidate" {
					t.Fatalf("issues = %+v, want bounded-table candidate issue", issues)
				}
				return
			}
			if result.Metadata.Status() != analysis.Complete || len(result.Metadata.Issues()) != 0 {
				t.Fatalf("unbounded metadata = %s %+v, want Complete", result.Metadata.Status(), result.Metadata.Issues())
			}
		})
	}
}

func TestExactAggregatorDependenciesAcrossSwitchPaths(t *testing.T) {
	issueScope := exactAggregatorScope()

	for _, path := range []struct {
		name string
		run  func(*testing.T, string, analysis.Metadata) vswitch.ForwardResult
	}{
		{
			name: "hub",
			run: func(t *testing.T, aggregate string, metadata analysis.Metadata) vswitch.ForwardResult {
				sw := newAggregatorDependencySwitch(t, aggregate, metadata, nil, nil, 0)
				return sw.Forward(fixedTime, "member", ethernet.Frame{Src: macH1, Dst: macH2})
			},
		},
		{
			name: "mirror",
			run: func(t *testing.T, aggregate string, metadata analysis.Metadata) vswitch.ForwardResult {
				const outputVLAN vlan.ID = 99
				trafficConfig := &traffic.Config{Mirrors: []traffic.Mirror{{
					Name: "span", SelectSrcPorts: []string{"in"}, OutputVLAN: new(outputVLAN),
				}}}
				seeds := []bridge.Seed{{FID: 10, MAC: macH2, Port: "out", Lifetime: bridge.Static}}
				sw := newAggregatorDependencySwitch(t, aggregate, metadata, trafficConfig, seeds, outputVLAN)
				return sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
			},
		},
		{
			name: "LACP",
			run: func(t *testing.T, aggregate string, metadata analysis.Metadata) vswitch.ForwardResult {
				sw := newAggregatorDependencySwitch(t, aggregate, metadata, nil, nil, 0)
				return sw.Forward(fixedTime, "member", ethernet.Frame{
					EtherType: ethernet.EtherTypeSlowProtocols,
					Payload:   []byte{1},
				})
			},
		},
	} {
		t.Run(path.name, func(t *testing.T) {
			metadata := analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
				Code: "test.lag1", Status: analysis.Incomplete, Scope: issueScope,
			}}, analysis.EvidenceCatalog{}, nil)

			relevant := path.run(t, "lag1", metadata)
			if relevant.Metadata.Status() != analysis.Incomplete {
				t.Fatalf("relevant status = %s, want Incomplete; issues: %+v", relevant.Metadata.Status(), relevant.Metadata.Issues())
			}
			if issues := relevant.Metadata.Issues(); len(issues) != 1 || issues[0].Code != "test.lag1" {
				t.Fatalf("relevant issues = %+v, want exact aggregator issue", issues)
			}
			if !slices.ContainsFunc(relevant.ConsultedScopes(), func(scope analysis.Scope) bool {
				return scope.Compare(issueScope) == 0
			}) {
				t.Errorf("relevant consulted scopes = %v, want %s", relevant.ConsultedScopes(), issueScope)
			}

			unrelated := path.run(t, "lag2", metadata)
			if unrelated.Metadata.Status() != analysis.Complete || len(unrelated.Metadata.Issues()) != 0 {
				t.Fatalf("unrelated metadata = %s %+v, want Complete", unrelated.Metadata.Status(), unrelated.Metadata.Issues())
			}
		})
	}
}

func TestAggregatorDependencyExcludedBeforePhysicalShortCircuit(t *testing.T) {
	aggregatorScope := exactAggregatorScope()
	metadata := analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{
		{Code: "test.aggregator", Status: analysis.Incomplete, Scope: aggregatorScope},
		{Code: "test.parent-port", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "lag1")},
	}, analysis.EvidenceCatalog{}, nil)

	for _, withBridge := range []bool{false, true} {
		name := "hub"
		if withBridge {
			name = "bridge"
		}
		t.Run(name, func(t *testing.T) {
			ports := mustTable(t, port.NewBuilder().
				Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Down}).
				Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
				Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
			cfg := vswitch.Config{
				Ports: ports,
				LAG:   &lag.Config{LAGs: map[string]lag.LAG{"lag1": {}}},
			}
			var seeds []bridge.Seed
			if withBridge {
				cfg.Bridge = &bridge.Config{}
				seeds = []bridge.Seed{{FID: 0, MAC: macH2, Port: "out", Lifetime: bridge.Static}}
			}
			sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
				Config: cfg, Seeds: seeds, NodeID: "sw1", Metadata: metadata,
			})
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}

			result := sw.Forward(fixedTime, "member", ethernet.Frame{Src: macH1, Dst: macH2})
			if result.Metadata.Status() != analysis.Complete || len(result.Metadata.Issues()) != 0 {
				t.Fatalf("metadata = %s %+v, want Complete physical-port short circuit", result.Metadata.Status(), result.Metadata.Issues())
			}
			if slices.ContainsFunc(result.ConsultedScopes(), func(scope analysis.Scope) bool {
				return scope.Compare(aggregatorScope) == 0
			}) {
				t.Fatalf("consulted scopes = %v, must exclude untouched aggregator", result.ConsultedScopes())
			}
		})
	}
}

func TestAggregatorDependencyExcludedBeforeAggregateEgressSelection(t *testing.T) {
	aggregatorScope := exactAggregatorScope()
	metadata := analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
		Code: "test.aggregator", Status: analysis.Incomplete, Scope: aggregatorScope,
	}}, analysis.EvidenceCatalog{}, nil)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Down}))
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{
			Ports: ports,
			LAG:   &lag.Config{LAGs: map[string]lag.LAG{"lag1": {}}},
		},
		NodeID: "sw1", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	result := sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2})
	if result.Metadata.Status() != analysis.Complete || len(result.Metadata.Issues()) != 0 {
		t.Fatalf("metadata = %s %+v, want Complete aggregate-port short circuit", result.Metadata.Status(), result.Metadata.Issues())
	}
	if slices.ContainsFunc(result.ConsultedScopes(), func(scope analysis.Scope) bool {
		return scope.Compare(aggregatorScope) == 0
	}) {
		t.Fatalf("consulted scopes = %v, must exclude untouched aggregator", result.ConsultedScopes())
	}
}

func TestLACPConsultsAggregatorAfterPhysicalAdmission(t *testing.T) {
	aggregatorScope := exactAggregatorScope()
	metadata := analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
		Code: "test.aggregator", Status: analysis.Incomplete, Scope: aggregatorScope,
	}}, analysis.EvidenceCatalog{}, nil)
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Down}))
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: vswitch.Config{
			Ports: ports,
			LAG:   &lag.Config{LAGs: map[string]lag.LAG{"lag1": {}}},
		},
		NodeID: "sw1", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	result := sw.Forward(fixedTime, "member", ethernet.Frame{
		EtherType: ethernet.EtherTypeSlowProtocols,
		Payload:   []byte{1},
	})
	if result.Metadata.Status() != analysis.Incomplete {
		t.Fatalf("metadata = %s %+v, want exact aggregator issue", result.Metadata.Status(), result.Metadata.Issues())
	}
	if issues := result.Metadata.Issues(); len(issues) != 1 || issues[0].Code != "test.aggregator" {
		t.Fatalf("issues = %+v, want only aggregator issue", issues)
	}
}

func exactAggregatorScope() analysis.Scope {
	return analysis.FieldScope(
		analysis.ProtocolScope("sw1", string(port.LayerLag), "0"),
		"aggregators", "lag1",
	)
}

func newAggregatorDependencySwitch(
	t *testing.T,
	aggregate string,
	metadata analysis.Metadata,
	trafficConfig *traffic.Config,
	seeds []bridge.Seed,
	outputVLAN vlan.ID,
) *vswitch.Switch {
	t.Helper()
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "member", Kind: port.Physical, LagParent: aggregate, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: aggregate, Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	cfg := vswitch.Config{
		Ports:   ports,
		LAG:     &lag.Config{LAGs: map[string]lag.LAG{aggregate: {}}},
		Traffic: trafficConfig,
	}
	if trafficConfig != nil {
		const inputVLAN vlan.ID = 10
		cfg.Bridge = &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{inputVLAN: "input", outputVLAN: "mirror"},
			Switchports: map[string]bridge.Switchport{
				"in":      {PVID: new(inputVLAN), Untagged: []vlan.ID{inputVLAN}},
				"out":     {PVID: new(inputVLAN), Untagged: []vlan.ID{inputVLAN}},
				aggregate: {Untagged: []vlan.ID{outputVLAN}},
			},
		}}
	}
	sw, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: cfg, Seeds: seeds, NodeID: "sw1", Metadata: metadata,
	})
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	return sw
}
