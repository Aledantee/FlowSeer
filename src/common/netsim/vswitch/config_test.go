package vswitch_test

import (
	"net/netip"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/filter"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

// TestCloneMSTRegionIsIndependent proves that cloning a Config carrying an
// MST region yields a clone whose region, instances, VLAN lists, and
// per-instance port maps do not alias the original's. Config.Clone once
// hand-rolled the STP copy and stopped before MST, so two configs Clone
// reported as independent shared one *stp.MST underneath.
func TestCloneMSTRegionIsIndependent(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "a", Kind: port.Physical}).
		Add(port.Port{Name: "b", Kind: port.Physical}))

	original := vswitch.Config{
		Ports: ports,
		STP: &stp.Config{
			Ports: map[string]stp.Port{"a": {}},
			MST: &stp.MST{
				Name: "region-1",
				Instances: map[stp.MSTID]stp.Instance{
					1: {
						VLANs: []vlan.ID{10, 20},
						Ports: map[string]stp.InstancePort{"a": {}},
					},
				},
			},
		},
	}

	clone := original.Clone()

	clone.STP.MST.Instances[1].VLANs[0] = 999
	clone.STP.MST.Instances[1].Ports["b"] = stp.InstancePort{Priority: 64, PriorityPresent: true}

	originalInstance := original.STP.MST.Instances[1]
	if originalInstance.VLANs[0] != 10 {
		t.Errorf("original instance VLANs[0] = %d after mutating the clone, want 10 (unchanged)", originalInstance.VLANs[0])
	}
	if _, ok := originalInstance.Ports["b"]; ok {
		t.Errorf("original instance Ports has key %q after the clone added it, want it absent", "b")
	}
}

// TestValidateRefusesLoopProtectWithoutBridge proves that loop protection,
// like spanning tree, requires a bridge to gate: with no bridge relay there
// is nothing for the layer's Block or NoLearn action to deny.
func TestValidateRefusesLoopProtectWithoutBridge(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "a", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"a": {Action: loopprotect.Block},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() = nil, want error when loop protection is configured without bridge")
	}
}

// TestValidateRefusesLoopProtectVLANNotAdmitted proves that a probe VLAN a
// loop-protection port names must be one its switchport actually carries: a
// VID the switchport admits neither tagged nor untagged nor as its PVID
// would never see the port's own probe return, silently disabling detection.
func TestValidateRefusesLoopProtectVLANNotAdmitted(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "a", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"a": {Untagged: []vlan.ID{10}},
				},
			},
		},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"a": {Action: loopprotect.Block, VLANs: []vlan.ID{20}},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() = nil, want error when a loop protection port names a VLAN its switchport does not admit")
	}
}

// TestValidateRefusesLoopProtectTrunkWithNoVLANsAndNoPVID proves that a
// protected trunk port with an empty VLANs list needs a PVID or a tunnel to
// probe untagged: Bridge.OriginateFrame on a VLAN-aware bridge rejects VID 0
// unless the port carries it, so a port with neither would never emit a
// probe and Validate's per-VLAN admission loop, which only walks a
// non-empty VLANs list, would otherwise accept it silently.
func TestValidateRefusesLoopProtectTrunkWithNoVLANsAndNoPVID(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "a", 20: "b"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {Tagged: []vlan.ID{10, 20}},
				},
			},
		},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() = nil, want error for a protected trunk port with no VLANs and no PVID")
	}
}

// TestValidateAcceptsLoopProtectTrunkWithPVID proves the symmetric accept:
// the same protected trunk port with no VLANs is valid once it carries a
// PVID, because that PVID is what lets it originate a VID-0 probe.
func TestValidateAcceptsLoopProtectTrunkWithPVID(t *testing.T) {
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "a", 20: "b"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &pvid, Untagged: []vlan.ID{10}, Tagged: []vlan.ID{20}},
				},
			},
		},
		LoopProtect: &loopprotect.Config{
			Ports: map[string]loopprotect.Port{
				"1/1/1": {Action: loopprotect.Block},
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for a protected trunk port with a PVID", err)
	}
}

// TestNewAcceptsBridgelessRouterWithSubInterfaces proves a firewall cabled to a trunk with
// no bridge at all can load: its port carries only routed sub-interfaces, so R1's constraint
// against a bridgeless router leaving a port unrouted is satisfied by the sub-interfaces alone.
func TestNewAcceptsBridgelessRouterWithSubInterfaces(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: tbl,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"eth1.10": {
							Port:     "eth1",
							VLAN:     10,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
						},
					},
				},
			},
		},
	}

	if _, err := vswitch.New(cfg); err != nil {
		t.Fatalf("New() = %v, want nil for a bridgeless router with a sub-interface", err)
	}
}

// TestNewRejectsSubInterfaceParentPortAsBridgeSwitchport and
// TestNewRejectsSubInterfaceParentPortAsSpanningTreePort prove that a sub-interface's parent
// port stays outside the bridge and the spanning tree exactly as a plain routed port's does
// (docs/architecture/2026-09-16-local-network-analysis-direction.md): all three bans in
// [Config.Validate] name the same `.port` field, keyed off Port alone, so a sub-interface's
// Port is banned identically.
func TestNewRejectsSubInterfaceParentPortAsBridgeSwitchport(t *testing.T) {
	pvid := vlan.ID(10)
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"eth1": {PVID: &pvid, Untagged: []vlan.ID{10}},
				},
			},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"eth1.10": {
							Port:     "eth1",
							VLAN:     10,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
						},
					},
				},
			},
		},
	}

	_, err := vswitch.New(cfg)
	if err == nil {
		t.Fatal("New() = nil, want error for a sub-interface parent port that is also a bridge switchport")
	}
	if !strings.Contains(err.Error(), "cannot be configured as a bridge switchport") {
		t.Errorf("New() error = %q, want the switchport ban's message", err)
	}
}

func TestNewRejectsSubInterfaceParentPortAsSpanningTreePort(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "eth1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: tbl,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{},
		},
		STP: &stp.Config{
			Ports: map[string]stp.Port{"eth1": {}},
		},
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"eth1.10": {
							Port:     "eth1",
							VLAN:     10,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
						},
					},
				},
			},
		},
	}

	_, err := vswitch.New(cfg)
	if err == nil {
		t.Fatal("New() = nil, want error for a sub-interface parent port that is also a spanning tree port")
	}
	if !strings.Contains(err.Error(), "cannot be configured as a spanning tree port") {
		t.Errorf("New() error = %q, want the spanning tree ban's message", err)
	}
}

func TestValidateRefusesFilterWithoutRouting(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Filter: &filter.Config{
			Sets: map[string]filter.RuleSet{
				"s1": {Default: filter.Accept},
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error for filter without routing")
	}
	attrs := errs.Attributes(err)
	if attrs["field"] != "filter" {
		t.Errorf("field attribute = %v, want filter", attrs["field"])
	}
}

func TestValidateRefusesFilterBindingToUnknownInterface(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfg := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.1.1/24")}},
					},
				},
			},
		},
		Filter: &filter.Config{
			Sets: map[string]filter.RuleSet{
				"s1": {Default: filter.Accept},
			},
			Bindings: []filter.Binding{
				{Interface: "unknown-iface", Direction: filter.In, Set: "s1"},
			},
		},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() = nil, want error for filter binding to unknown interface")
	}
	attrs := errs.Attributes(err)
	if attrs["field"] != "filter.bindings.unknown-iface.in" {
		t.Errorf("field attribute = %v, want filter.bindings.unknown-iface.in", attrs["field"])
	}
}

func TestDiffReportsFilterCapabilityChange(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	cfgA := vswitch.Config{
		Ports: ports,
		Routing: &routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"1/1/1": {Port: "1/1/1", Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.1.1/24")}},
					},
				},
			},
		},
	}
	cfgB := cfgA.Clone()
	cfgB.Filter = &filter.Config{
		Sets: map[string]filter.RuleSet{
			"s1": {Default: filter.Accept},
		},
		Bindings: []filter.Binding{
			{Interface: "1/1/1", Direction: filter.In, Set: "s1"},
		},
	}

	changes := vswitch.Diff(cfgA, cfgB)
	found := false
	for _, ch := range changes {
		if ch.Layer == port.LayerFilter && ch.Subject.Kind == "capability" && ch.Subject.Key == "filter" {
			found = true
			if ch.From != nil {
				t.Errorf("From = %v, want nil", ch.From)
			}
			if ch.To == nil {
				t.Errorf("To = nil, want LayerFact")
			}
		}
	}
	if !found {
		t.Errorf("Diff did not report filter capability addition; got %v", changes)
	}
}
