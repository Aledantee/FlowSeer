package netmodel_test

import (
	"slices"
	"testing"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	phyv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/phy/v1"
	lacpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/lacp/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/lag"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
)

func TestLoadAnonymousSourceUsesAnonymousNodeScope(t *testing.T) {
	name := "p1"
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	input := netmodel.Input{Interfaces: []*interfacev1.Interface{interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
	}.Build()}}
	validateInput(t, input)

	loaded := load(t, netmodel.SourceContext{}, input)
	wantScope := analysis.NodeScope("")
	if got := loaded.Metadata.Scope(); got.Compare(wantScope) != 0 {
		t.Fatalf("metadata scope = %s, want %s", got, wantScope)
	}
	if len(loaded.Report.Defaults) == 0 {
		t.Fatal("anonymous load produced no defaults")
	}
	for _, item := range loaded.Report.Defaults {
		if !wantScope.Contains(item.Scope) {
			t.Errorf("default scope = %s, want scope beneath %s", item.Scope, wantScope)
		}
	}
	issues := loaded.Metadata.Issues()
	if len(issues) == 0 {
		t.Fatal("anonymous load produced no issues")
	}
	for _, issue := range issues {
		if !wantScope.Contains(issue.Scope) {
			t.Errorf("issue scope = %s, want scope beneath %s", issue.Scope, wantScope)
		}
	}

	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec(Load().Spec): %v", err)
	}
	if got := sw.Spec(); !got.Equal(loaded.Spec) {
		t.Errorf("round-trip spec differs:\n got: %+v\nwant: %+v", got, loaded.Spec)
	}
}

func TestLoadMalformedNetworkValuesAreScopedPartialRows(t *testing.T) {
	vid := uint32(10)
	routedName := "vlan10"
	portName := "1/1/1"
	active := switchingv1.FdbEntryStatus_FDB_ENTRY_STATUS_ACTIVE
	static := switchingv1.FdbEntryKind_FDB_ENTRY_KIND_STATIC
	malformedIP := addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: []byte{10, 0, 0}}.Build(),
	}.Build()
	malformedPrefix := addrv1.IpPrefix_builder{
		V4: addrv1.Ipv4Prefix_builder{
			Address: addrv1.Ipv4Address_builder{Octets: []byte{10, 0, 0}}.Build(),
			Length:  ptr(uint32(24)),
		}.Build(),
	}.Build()

	input := netmodel.Input{
		Interfaces: []*interfacev1.Interface{
			routedVLANInterface(routedName, vid, []byte{0, 1, 2, 3, 4}),
			plainPhysicalInterface(portName),
		},
		FDB: []*switchingv1.FdbEntry{
			switchingv1.FdbEntry_builder{
				NetworkInstance: ptr("default"),
				VlanId:          &vid, InterfaceName: &portName,
				Mac: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(), Kind: &static, Status: &active,
			}.Build(),
		},
		Addresses: []*ipv1.InterfaceAddress{
			ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: malformedIP, Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24)}.Build(),
			ipv1.InterfaceAddress_builder{InterfaceName: &routedName, Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: malformedPrefix}.Build(),
		},
		Neighbors: []*ipv1.NeighborEntry{
			ipv1.NeighborEntry_builder{InterfaceName: &routedName, Ip: malformedIP, Mac: protoEUI48([6]byte{0, 1, 2, 3, 4, 6})}.Build(),
			ipv1.NeighborEntry_builder{
				InterfaceName: &routedName, Ip: protoIPv4Addr([4]byte{10, 0, 0, 2}),
				Mac: addrv1.MacAddress_builder{Eui48: addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build()}.Build(),
			}.Build(),
		},
	}

	result := load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "malformed"}, input)
	if result.Readiness() == analysis.Complete {
		t.Fatal("readiness = Complete, want non-Complete")
	}
	if len(result.Spec.Seeds) != 0 {
		t.Errorf("seeds = %+v, want malformed FDB row omitted", result.Spec.Seeds)
	}
	vrf := result.Spec.Config.Routing.VRFs[routing.DefaultVRF]
	if prefixes := vrf.Interfaces[routedName].Prefixes; len(prefixes) != 0 {
		t.Errorf("prefixes = %v, want malformed rows omitted", prefixes)
	}
	if len(vrf.Neighbors) != 0 {
		t.Errorf("neighbors = %+v, want malformed rows omitted", vrf.Neighbors)
	}

	wants := []struct {
		what string
		port string
	}{
		{what: "interface_mac", port: routedName},
		{what: "fdb_entry", port: portName},
		{what: "ip_address", port: routedName},
		{what: "ip_neighbor", port: routedName},
	}
	for _, want := range wants {
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == want.what && skip.Port == want.port
		}) {
			t.Errorf("skipped = %+v, want scoped %s skip for %s", result.Report.Skipped, want.what, want.port)
		}
	}
	if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
		t.Errorf("NewWithSpec: %v", err)
	}
}

func TestLoadMalformedProtocolMACsArePartial(t *testing.T) {
	src := netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "protocol-mac"}

	t.Run("spanning tree bridge", func(t *testing.T) {
		protocol := stpv1.ProtocolVersion_PROTOCOL_VERSION_RSTP
		priority := uint32(32768)
		bridgeState := stpv1.BridgeState_builder{
			NetworkInstance: ptr(netmodel.DefaultNetworkInstance),
			ProtocolVersion: &protocol,
			BridgeId: stpv1.BridgeId_builder{
				Priority: &priority,
				Address:  addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(),
			}.Build(),
		}.Build()
		result := load(t, src, (netmodel.Input{
			Interfaces:  []*interfacev1.Interface{plainPhysicalInterface("1/1/1")},
			BridgeState: bridgeState,
		}))

		if result.Spec.Config.STP != nil {
			t.Errorf("STP config = %+v, want malformed bridge state omitted", result.Spec.Config.STP)
		}
		if result.Readiness() == analysis.Complete {
			t.Fatal("readiness = Complete, want non-Complete")
		}
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == "stp_bridge"
		}) {
			t.Errorf("skipped = %+v, want stp_bridge", result.Report.Skipped)
		}
		if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
			t.Errorf("NewWithSpec: %v", err)
		}
	})

	t.Run("link aggregation system id", func(t *testing.T) {
		lagName := "lag1"
		active := lacpv1.LacpMode_LACP_MODE_ACTIVE
		result := load(t, src, (netmodel.Input{
			Interfaces: lagInterfaces(),
			LACPAggregators: []*lacpv1.AggregatorState{
				lacpv1.AggregatorState_builder{
					InterfaceName: &lagName,
					Mode:          &active,
					SystemId:      addrv1.Eui48Address_builder{Octets: []byte{0, 1, 2, 3, 4}}.Build(),
				}.Build(),
			},
		}))

		if result.Spec.Config.LAG.LAGs[lagName].LACP.Mode != lag.Off {
			t.Errorf("LACP mode = %v, want default Off", result.Spec.Config.LAG.LAGs[lagName].LACP.Mode)
		}
		if result.Readiness() == analysis.Complete {
			t.Fatal("readiness = Complete, want non-Complete")
		}
		if !slices.ContainsFunc(result.Report.Skipped, func(skip netmodel.Skipped) bool {
			return skip.What == "lacp_aggregator" && skip.Port == lagName
		}) {
			t.Errorf("skipped = %+v, want scoped lacp_aggregator", result.Report.Skipped)
		}
		if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
			t.Errorf("NewWithSpec: %v", err)
		}
	})
}

func TestLoadUnknownEnumValuesAreScopedUnsupported(t *testing.T) {
	assertUnsupported := func(t *testing.T, result netmodel.Result, code analysis.IssueCode, scope analysis.Scope) {
		t.Helper()
		if result.Readiness() != analysis.Unsupported {
			t.Fatalf("readiness = %s, want %s; issues: %+v", result.Readiness(), analysis.Unsupported, result.Metadata.Issues())
		}
		issues := result.Metadata.Issues()
		issueIndex := slices.IndexFunc(issues, func(issue analysis.Issue) bool {
			return issue.Code == code && issue.Scope.Compare(scope) == 0
		})
		if issueIndex < 0 {
			t.Fatalf("issues = %+v, want %s at %s", issues, code, scope)
		}
		issue := issues[issueIndex]
		if len(issue.Evidence) == 0 {
			t.Fatalf("issue %s has no evidence", code)
		}
		if _, ok := result.Metadata.Evidence().Lookup(issue.Evidence[0]); !ok {
			t.Errorf("issue %s evidence is absent from catalog", code)
		}
	}

	t.Run("frame admission", func(t *testing.T) {
		name := "1/1/1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := switchingv1.FrameAdmission(99)
		result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, (netmodel.Input{Interfaces: []*interfacev1.Interface{
			interfacev1.Interface_builder{
				Name: &name, AdminStatus: &admin, OperStatus: &oper,
				Physical: interfacev1.PhysicalInterface_builder{
					Switchport: switchingv1.SwitchportFacet_builder{FrameAdmission: &unknown}.Build(),
				}.Build(),
			}.Build(),
		}}))
		assertUnsupported(t, result, netmodel.IssueInvalidFrameAdmission, analysis.PortScope("sw1", name))
		if _, ok := result.Spec.Config.Bridge.VLAN.Switchports[name]; ok {
			t.Error("unknown frame admission was coerced into an executable switchport")
		}
	})

	t.Run("switchport mode", func(t *testing.T) {
		name := "1/1/1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := switchingv1.SwitchportMode(99)
		result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, (netmodel.Input{Interfaces: []*interfacev1.Interface{
			interfacev1.Interface_builder{
				Name: &name, AdminStatus: &admin, OperStatus: &oper,
				Physical: interfacev1.PhysicalInterface_builder{
					Switchport: switchingv1.SwitchportFacet_builder{Mode: &unknown}.Build(),
				}.Build(),
			}.Build(),
		}}))
		assertUnsupported(t, result, netmodel.IssueInvalidSwitchportMode, analysis.PortScope("sw1", name))
		if _, ok := result.Spec.Config.Bridge.VLAN.Switchports[name]; ok {
			t.Error("unknown switchport mode was coerced into an executable switchport")
		}
	})

	t.Run("ethernet duplex", func(t *testing.T) {
		name := "1/1/1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := phyv1.EthernetDuplex(99)
		result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, (netmodel.Input{Interfaces: []*interfacev1.Interface{
			interfacev1.Interface_builder{
				Name: &name, AdminStatus: &admin, OperStatus: &oper,
				Physical: interfacev1.PhysicalInterface_builder{
					Ethernet: phyv1.EthernetFacet_builder{ActiveDuplex: &unknown}.Build(),
				}.Build(),
			}.Build(),
		}}))
		assertUnsupported(t, result, netmodel.IssueInvalidEthernetDuplex, analysis.PortScope("sw1", name))
		if observed := result.Spec.Config.Phy.Ethernet[name].Observed; observed != nil {
			t.Errorf("unknown duplex produced observed link state %+v", observed)
		}
	})

	t.Run("PoE priority", func(t *testing.T) {
		name := "1/1/1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := phyv1.PoePriority(99)
		group := uint32(1)
		power := uint64(100_000_000_000)
		result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, (netmodel.Input{
			Interfaces: []*interfacev1.Interface{
				interfacev1.Interface_builder{
					Name: &name, AdminStatus: &admin, OperStatus: &oper,
					Physical: interfacev1.PhysicalInterface_builder{
						Ethernet: phyv1.EthernetFacet_builder{
							Copper: phyv1.CopperFacet_builder{
								PoeSettings: phyv1.PoeSettings_builder{Priority: &unknown}.Build(),
								PoeDetail:   phyv1.PoePortDetail_builder{PseGroup: &group}.Build(),
							}.Build(),
						}.Build(),
					}.Build(),
				}.Build(),
			},
			Budgets: []*phyv1.PseBudget{
				phyv1.PseBudget_builder{PseGroup: &group, PowerNanowatts: &power}.Build(),
			},
		}))
		assertUnsupported(t, result, netmodel.IssueInvalidPoEPriority, analysis.PortScope("sw1", name))
		if _, ok := result.Spec.Config.Phy.PoE.Ports[name]; ok {
			t.Error("unknown PoE priority was coerced into an executable PSE port")
		}
	})

	t.Run("stp point to point", func(t *testing.T) {
		name := "1/1/1"
		unknown := stpv1.PointToPointMode(99)
		result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, (netmodel.Input{
			Interfaces:  []*interfacev1.Interface{plainPhysicalInterface(name)},
			BridgeState: validBridgeState(),
			STPPorts: []*stpv1.PortState{
				stpv1.PortState_builder{InterfaceName: &name, PointToPoint: &unknown}.Build(),
			},
		}))
		assertUnsupported(t, result, netmodel.IssueInvalidPointToPointMode, analysis.FieldScope(
			analysis.ProtocolScope("sw1", string(stp.LayerName), "0"), "ports", name,
		))
		if _, ok := result.Spec.Config.STP.Ports[name]; ok {
			t.Error("unknown point-to-point mode was coerced into an executable STP row")
		}
	})

	t.Run("lacp mode", func(t *testing.T) {
		name := "lag1"
		unknown := lacpv1.LacpMode(99)
		result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, (netmodel.Input{
			Interfaces: lagInterfaces(),
			LACPAggregators: []*lacpv1.AggregatorState{
				lacpv1.AggregatorState_builder{InterfaceName: &name, Mode: &unknown}.Build(),
			},
		}))
		assertUnsupported(t, result, netmodel.IssueInvalidLACPMode, analysis.PortScope("sw1", name))
	})

	t.Run("bond mode", func(t *testing.T) {
		name := "lag1"
		admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
		oper := interfacev1.OperStatus_OPER_STATUS_UP
		unknown := switchingv1.BondMode(99)
		ifaces := lagInterfaces()
		ifaces[0] = interfacev1.Interface_builder{
			Name: &name, AdminStatus: &admin, OperStatus: &oper,
			Lag: interfacev1.LagInterface_builder{
				Aggregation: switchingv1.AggregationFacet_builder{BondMode: &unknown}.Build(),
			}.Build(),
		}.Build()
		result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, (netmodel.Input{Interfaces: ifaces}))
		assertUnsupported(t, result, netmodel.IssueInvalidBondMode, analysis.PortScope("sw1", name))
	})
}
