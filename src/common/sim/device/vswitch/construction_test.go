package vswitch_test

import (
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestNewRejectsZeroStaticNeighborMACAtRoutingField(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "routed", AdminStatus: port.Up, OperStatus: port.Up}))
	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"routed": {Port: "routed", Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}},
				},
				Neighbors: []routing.Neighbor{{Interface: "routed", Addr: netip.MustParseAddr("192.0.2.2")}},
			},
		}},
	}

	_, err := vswitch.New(cfg)
	if err == nil {
		t.Fatal("vswitch.New accepted a zero static neighbor MAC")
	}
	if got, want := errs.Attributes(err)["field"], "vrfs.default.neighbors.routed/192.0.2.2.mac"; got != want {
		t.Errorf("field = %v, want %q", got, want)
	}
}

func TestCapabilitiesFollowConfiguration(t *testing.T) {
	portsOnly := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("ports only", func(t *testing.T) {
		cfg := vswitch.Config{Ports: portsOnly}
		if caps := cfg.Capabilities(); len(caps) != 0 {
			t.Errorf("got capabilities %v, want empty set", caps)
		}
		if err := cfg.Validate(); err != nil {
			t.Errorf("got validation error %v, want nil", err)
		}
		sw := mustSwitch(t, cfg)
		if sw.Entries() != nil {
			t.Errorf("got entries %v on hub, want nil", sw.Entries())
		}
	})

	t.Run("relay only", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  portsOnly,
			Bridge: &bridge.Config{},
		}
		want := []trace.Layer{bridge.LayerName}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})

	t.Run("all capabilities", func(t *testing.T) {
		tbl := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
			Add(port.Port{Name: "lag1", Kind: port.LAG}))

		cfg := vswitch.Config{
			Ports:   tbl,
			Traffic: &traffic.Config{},
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			Phy: &phy.Config{
				Ethernet: map[string]phy.Ethernet{
					"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
				},
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{
						"g1": {PowerNanowatts: 60_000_000_000},
					},
					Ports: map[string]phy.PsePort{
						"1/1/1": {Group: "g1", Enabled: true, MaxClass: 4},
					},
				},
			},
		}

		want := []trace.Layer{
			phy.LayerName,
			lag.LayerName,
			phy.LayerNamePoE,
			bridge.LayerName,
			traffic.LayerName,
			bridge.LayerNameVLAN,
		}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})
}

func TestPortInsertionOrderDoesNotChangeConfigurationOrForwardingSemantics(t *testing.T) {
	newConfig := func(names ...string) vswitch.Config {
		builder := port.NewBuilder()
		for _, name := range names {
			builder.Add(port.Port{Name: name, AdminStatus: port.Up, OperStatus: port.Up})
		}
		return vswitch.Config{Ports: mustTable(t, builder)}
	}

	a := newConfig("ingress", "output-z", "output-a")
	b := newConfig("output-a", "ingress", "output-z")
	if !a.Equal(b) {
		t.Fatal("Config.Equal distinguished port insertion order")
	}
	if a.Canonical() != b.Canonical() {
		t.Errorf("canonical configurations differ by insertion order:\n a: %s\n b: %s", a.Canonical(), b.Canonical())
	}
	if changes := vswitch.Diff(a, b); len(changes) != 0 {
		t.Errorf("Diff reported insertion-only changes: %+v", changes)
	}

	frame := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{4}}
	aResult := mustSwitch(t, a).Receive(fixedTime, "ingress", frame).Result
	bResult := mustSwitch(t, b).Receive(fixedTime, "ingress", frame).Result
	if !reflect.DeepEqual(aResult, bResult) {
		t.Errorf("forwarding differs by insertion order:\n a: %+v\n b: %+v", aResult, bResult)
	}
}

func TestConstructionSpecCanonicalizesSeedTimesAndComparesInstants(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	fixed := time.Date(2026, 9, 10, 12, 0, 0, 123, time.UTC)
	// Add fixes the wall instant while preserving the clock's monotonic reading.
	clock := time.Now()
	withMonotonic := clock.Add(fixed.Sub(clock))
	if !strings.Contains(withMonotonic.String(), "m=") {
		t.Fatal("fixture lost its monotonic reading")
	}
	wall := time.Unix(withMonotonic.Unix(), int64(withMonotonic.Nanosecond())).UTC()
	otherLocation := wall.In(time.FixedZone("same-instant", 3*60*60))
	seed := bridge.Seed{MAC: netaddr.MAC{2}, Port: "in"}

	a := vswitch.ConstructionSpec{Config: vswitch.Config{Ports: ports, Bridge: &bridge.Config{}}, Seeds: []bridge.Seed{seed}}
	b := a.Clone()
	a.Seeds[0].LearnedAt = withMonotonic
	b.Seeds[0].LearnedAt = otherLocation
	if !a.Equal(b) {
		t.Fatal("Equal distinguished the same instant by location or monotonic reading")
	}

	normalizedA, err := a.Normalize()
	if err != nil {
		t.Fatalf("Normalize(a): %v", err)
	}
	normalizedB, err := b.Normalize()
	if err != nil {
		t.Fatalf("Normalize(b): %v", err)
	}
	if normalizedA.Seeds[0].LearnedAt != wall || normalizedB.Seeds[0].LearnedAt != wall {
		t.Errorf("normalized times = %v and %v, want canonical UTC %v", normalizedA.Seeds[0].LearnedAt, normalizedB.Seeds[0].LearnedAt, wall)
	}
}

func TestConstructionSpecIssueMessagesDoNotChangeIdentity(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	issue := analysis.Issue{
		Code:    "test.unobserved",
		Status:  analysis.Incomplete,
		Scope:   analysis.PortScope("sw1", "in"),
		Message: "first wording",
	}
	a := vswitch.ConstructionSpec{
		Config:   vswitch.Config{Ports: ports},
		NodeID:   "sw1",
		Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{issue}, analysis.EvidenceCatalog{}, nil),
	}
	b := a.Clone()
	issue.Message = "revised wording"
	b.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{issue}, analysis.EvidenceCatalog{}, nil)

	if !a.Equal(b) {
		t.Fatal("ConstructionSpec.Equal distinguished human-only issue messages")
	}
	sw, err := vswitch.NewWithSpec(a)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	if got := sw.Spec().Metadata.Issues()[0].Message; got != "first wording" {
		t.Errorf("copied issue message = %q, want retained human wording", got)
	}
}

func TestConstructionSpecIssueMessageOrderingDoesNotChangeIdentity(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	catalog, firstRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "snapshot", Origin: "first"})
	catalog, secondRef := catalog.Add(analysis.Evidence{Kind: "snapshot", Origin: "second"})
	issue := func(message string, ref trace.EvidenceRef) analysis.Issue {
		return analysis.Issue{
			Code: "test.unobserved", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "in"),
			Message: message, Evidence: []trace.EvidenceRef{ref},
		}
	}
	spec := func(issues []analysis.Issue) vswitch.ConstructionSpec {
		return vswitch.ConstructionSpec{
			Config:   vswitch.Config{Ports: ports},
			NodeID:   "sw1",
			Metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), issues, catalog, nil),
		}
	}

	a := spec([]analysis.Issue{issue("a", firstRef), issue("z", secondRef)})
	b := spec([]analysis.Issue{issue("z", firstRef), issue("a", secondRef)})
	if !a.Equal(b) {
		t.Fatal("human message sort order changed construction identity")
	}
}

func TestConstructionSpecValidatesMetadataScopesAgainstNode(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	config := vswitch.Config{Ports: ports}
	nodeScope := analysis.NodeScope("sw1")

	valid := []struct {
		name     string
		metadata analysis.Metadata
	}{
		{name: "zero value", metadata: analysis.Metadata{}},
		{name: "canonical zero", metadata: analysis.NewMetadata(analysis.WholeScope(), nil, analysis.EvidenceCatalog{}, nil)},
		{name: "node", metadata: analysis.NewMetadata(nodeScope, nil, analysis.EvidenceCatalog{}, nil)},
		{name: "child", metadata: analysis.NewMetadata(analysis.PortScope("sw1", "in"), nil, analysis.EvidenceCatalog{}, nil)},
		{name: "routing lookup child", metadata: analysis.NewMetadata(routing.NeighborLookupScope("sw1", routing.DefaultVRF, "vlan10", netip.MustParseAddr("192.0.2.2")), nil, analysis.EvidenceCatalog{}, nil)},
		{name: "FDB lookup child", metadata: analysis.NewMetadata(bridge.FDBLookupScope("sw1", 10, macH1), nil, analysis.EvidenceCatalog{}, nil)},
		{name: "whole issue", metadata: analysis.NewMetadata(nodeScope, []analysis.Issue{{
			Code: "test.global", Status: analysis.Incomplete, Scope: analysis.WholeScope(),
		}}, analysis.EvidenceCatalog{}, nil)},
		{name: "whole assumption", metadata: analysis.NewMetadata(nodeScope, nil, analysis.EvidenceCatalog{}, []analysis.Assumption{{
			Scope: analysis.WholeScope(), Statement: "global premise",
		}})},
	}
	for _, test := range valid {
		t.Run("valid "+test.name, func(t *testing.T) {
			if _, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{Config: config, NodeID: "sw1", Metadata: test.metadata}); err != nil {
				t.Fatalf("NewWithSpec rejected compatible metadata: %v", err)
			}
		})
	}

	invalid := []struct {
		name      string
		metadata  analysis.Metadata
		wantField string
	}{
		{
			name: "whole evaluated scope with content",
			metadata: analysis.NewMetadata(analysis.WholeScope(), []analysis.Issue{{
				Code: "test.local", Status: analysis.Incomplete, Scope: nodeScope,
			}}, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.scope",
		},
		{
			name:      "foreign evaluated node",
			metadata:  analysis.NewMetadata(analysis.NodeScope("sw2"), nil, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.scope",
		},
		{
			name: "foreign issue node",
			metadata: analysis.NewMetadata(nodeScope, []analysis.Issue{{
				Code: "test.foreign", Status: analysis.Incomplete, Scope: analysis.PortScope("sw2", "in"),
			}}, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.issues.0.scope",
		},
		{
			name: "foreign assumption node",
			metadata: analysis.NewMetadata(nodeScope, nil, analysis.EvidenceCatalog{}, []analysis.Assumption{{
				Scope: analysis.PortScope("sw2", "in"), Statement: "foreign premise",
			}}),
			wantField: "metadata.assumptions.0.scope",
		},
	}
	for _, test := range invalid {
		t.Run("invalid "+test.name, func(t *testing.T) {
			_, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{Config: config, NodeID: "sw1", Metadata: test.metadata})
			if err == nil {
				t.Fatal("NewWithSpec accepted incompatible metadata")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}

	anonymousNode := analysis.NodeScope("")
	anonymousInvalid := []struct {
		name      string
		metadata  analysis.Metadata
		wantField string
	}{
		{
			name:      "foreign evaluated node",
			metadata:  analysis.NewMetadata(analysis.NodeScope("sw2"), nil, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.scope",
		},
		{
			name: "foreign issue node",
			metadata: analysis.NewMetadata(anonymousNode, []analysis.Issue{{
				Code: "test.foreign", Status: analysis.Incomplete, Scope: analysis.PortScope("sw2", "in"),
			}}, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.issues.0.scope",
		},
		{
			name: "foreign assumption node",
			metadata: analysis.NewMetadata(anonymousNode, nil, analysis.EvidenceCatalog{}, []analysis.Assumption{{
				Scope: analysis.PortScope("sw2", "in"), Statement: "foreign premise",
			}}),
			wantField: "metadata.assumptions.0.scope",
		},
	}
	for _, test := range anonymousInvalid {
		t.Run("anonymous invalid "+test.name, func(t *testing.T) {
			_, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{Config: config, Metadata: test.metadata})
			if err == nil {
				t.Fatal("NewWithSpec accepted incompatible anonymous metadata")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}

	if _, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
		Config: config,
		Metadata: analysis.NewMetadata(anonymousNode, []analysis.Issue{{
			Code: "test.anonymous", Status: analysis.Incomplete, Scope: analysis.PortScope("", "in"),
		}}, analysis.EvidenceCatalog{}, nil),
	}); err != nil {
		t.Fatalf("NewWithSpec rejected anonymous-node metadata: %v", err)
	}
}

func TestConstructionSpecRejectsDanglingMetadataEvidence(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	const missing trace.EvidenceRef = "evidence:missing"

	for _, test := range []struct {
		name      string
		metadata  analysis.Metadata
		wantField string
	}{
		{
			name: "issue",
			metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), []analysis.Issue{{
				Code: "test.missing", Status: analysis.Incomplete, Scope: analysis.PortScope("sw1", "in"),
				Evidence: []trace.EvidenceRef{missing},
			}}, analysis.EvidenceCatalog{}, nil),
			wantField: "metadata.issues.0.evidence.0",
		},
		{
			name: "assumption",
			metadata: analysis.NewMetadata(analysis.NodeScope("sw1"), nil, analysis.EvidenceCatalog{}, []analysis.Assumption{{
				Scope: analysis.PortScope("sw1", "in"), Statement: "missing support",
				Evidence: []trace.EvidenceRef{missing},
			}}),
			wantField: "metadata.assumptions.0.evidence.0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := vswitch.NewWithSpec(vswitch.ConstructionSpec{
				Config: vswitch.Config{Ports: ports}, NodeID: "sw1", Metadata: test.metadata,
			})
			if err == nil {
				t.Fatal("NewWithSpec accepted a dangling evidence reference")
			}
			if got := errs.Attributes(err)["field"]; got != test.wantField {
				t.Errorf("field = %v, want %q", got, test.wantField)
			}
		})
	}
}

func TestCapabilitiesSTP(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports:  tbl,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	want := []trace.Layer{bridge.LayerName, stp.LayerName}
	if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
		t.Errorf("got capabilities %v, want %v", caps, want)
	}
}

func TestValidateRefusesSTPWithoutBridge(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		STP: &stp.Config{
			Priority: 32768,
			Address:  netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0x01},
			Ports: map[string]stp.Port{
				"1/1/1": {},
				"1/1/2": {},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() = nil, want error when STP is configured without bridge")
	}
}

func TestCapabilitiesWithRouting(t *testing.T) {
	portsOnly := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("relay routing vlan", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: portsOnly,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		want := []trace.Layer{bridge.LayerName, routing.LayerName, bridge.LayerNameVLAN}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})

	t.Run("routing alone for router", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: portsOnly,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"1/1/2": {Port: "1/1/2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		want := []trace.Layer{routing.LayerName}
		if caps := cfg.Capabilities(); !slices.Equal(caps, want) {
			t.Errorf("got capabilities %v, want %v", caps, want)
		}
	})
}

func TestCrossLayerValidation(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("routed VLAN interface requires bridge VLAN", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for missing bridge VLAN")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "vlan10" || attrs["vlan"] != vlan.ID(10) {
			t.Errorf("got attrs %v, want vrf=default, interface=vlan10, vlan=10", attrs)
		}
	})

	t.Run("routed port refused on a relay without VLAN", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/2": {Port: "1/1/2", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.50.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for a routed port on a relay that cannot filter it")
		}
		if attrs := errs.Attributes(err); attrs["port"] != "1/1/2" {
			t.Errorf("got attrs %v, want port=1/1/2", attrs)
		}
	})

	t.Run("routed VLAN interface requires VLAN in table", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{20: "vlan20"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{20}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for VLAN missing from table")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "vlan10" || attrs["vlan"] != vlan.ID(10) {
			t.Errorf("got attrs %v, want vrf=default, interface=vlan10, vlan=10", attrs)
		}
	})

	t.Run("routed port cannot appear in switchports", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for routed port in switchports")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "1/1/1" || attrs["port"] != "1/1/1" {
			t.Errorf("got attrs %v, want vrf=default, interface=1/1/1, port=1/1/1", attrs)
		}
	})

	t.Run("routed port cannot appear in STP ports", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/2": {Untagged: []vlan.ID{10}},
					},
				},
			},
			STP: &stp.Config{
				Priority: 32768,
				Ports:    map[string]stp.Port{"1/1/1": {Priority: 128}},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for routed port in STP ports")
		}
		attrs := errs.Attributes(err)
		if attrs["vrf"] != "default" || attrs["interface"] != "1/1/1" || attrs["port"] != "1/1/1" {
			t.Errorf("got attrs %v, want vrf=default, interface=1/1/1, port=1/1/1", attrs)
		}
	})

	t.Run("router without bridge must route every non-LAG port", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate succeeded, want error for unrouted port on router")
		}
		attrs := errs.Attributes(err)
		if attrs["port"] != "1/1/2" {
			t.Errorf("got attrs %v, want port=1/1/2", attrs)
		}
	})

	t.Run("router without bridge routes all non-LAG ports successfully", func(t *testing.T) {
		lagPorts := mustTable(t, port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1"}).
			Add(port.Port{Name: "lag1", Kind: port.LAG}))

		cfg := vswitch.Config{
			Ports: lagPorts,
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"lag1":  {Port: "lag1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate failed: %v", err)
		}
	})
}

func TestBaseMACAssignment(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}))

	t.Run("all zero MACs assigned base address", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
					},
				},
			},
			STP: &stp.Config{
				Priority: 32768,
				Ports:    map[string]stp.Port{"1/1/1": {Priority: 128}},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
						},
					},
				},
			},
		}
		sw := mustSwitch(t, cfg)
		wantMAC := netaddr.Local(1)
		if sw.Config().MAC != wantMAC {
			t.Errorf("sw.Config().MAC = %v, want %v", sw.Config().MAC, wantMAC)
		}
		if iface := sw.Config().Routing.VRFs["default"].Interfaces["vlan10"]; iface.MAC != wantMAC {
			t.Errorf("routed interface MAC = %v, want %v", iface.MAC, wantMAC)
		}
		if sw.Config().STP.Address != wantMAC {
			t.Errorf("STP config address = %v, want %v", sw.Config().STP.Address, wantMAC)
		}
		if sw.BridgeID().Address != wantMAC {
			t.Errorf("sw.BridgeID().Address = %v, want %v", sw.BridgeID().Address, wantMAC)
		}
	})

	t.Run("explicit interface MAC preserved beside assigned base", func(t *testing.T) {
		explicitMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
		cfg := vswitch.Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {Untagged: []vlan.ID{10}},
						"1/1/2": {Untagged: []vlan.ID{20}},
					},
				},
			},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					"default": {
						Interfaces: map[string]routing.Interface{
							"vlan10": {VLAN: 10, MAC: explicitMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
							"vlan20": {VLAN: 20, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}},
						},
					},
				},
			},
		}
		sw := mustSwitch(t, cfg)
		wantBase := netaddr.Local(1)
		if sw.Config().MAC != wantBase {
			t.Errorf("sw.Config().MAC = %v, want %v", sw.Config().MAC, wantBase)
		}
		if iface10 := sw.Config().Routing.VRFs["default"].Interfaces["vlan10"]; iface10.MAC != explicitMAC {
			t.Errorf("vlan10 MAC = %v, want %v", iface10.MAC, explicitMAC)
		}
		if iface20 := sw.Config().Routing.VRFs["default"].Interfaces["vlan20"]; iface20.MAC != wantBase {
			t.Errorf("vlan20 MAC = %v, want %v", iface20.MAC, wantBase)
		}
	})

	t.Run("derived switch keeps base MAC", func(t *testing.T) {
		cfg := vswitch.Config{
			MAC:   netaddr.Local(42),
			Ports: ports,
		}
		cur := mustSwitch(t, cfg)
		nextCfg := vswitch.Config{
			Ports: ports,
		}
		target := cur.Spec()
		target.Config = nextCfg
		target.Config.MAC = cur.Config().MAC
		nextSw, _, err := vswitch.Derive(cur, target)
		if err != nil {
			t.Fatalf("Derive failed: %v", err)
		}
		if nextSw.Config().MAC != netaddr.Local(42) {
			t.Errorf("nextSw.Config().MAC = %v, want %v", nextSw.Config().MAC, netaddr.Local(42))
		}
	})
}
