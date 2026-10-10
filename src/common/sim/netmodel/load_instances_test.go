package netmodel_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	filterv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/filter/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	stpv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/protocol/stp/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/netmodel"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestLoadNamedDomainsBeforeClaims(t *testing.T) {
	reached := 0
	for _, sibling := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("default=%t/reverse=%t", sibling, reverse), func(t *testing.T) {
				in := netmodel.Input{
					Interfaces: []*interfacev1.Interface{switchedPhysicalInterface("eth1", 10), switchedPhysicalInterface("eth2", 10)},
					FilterSets: []*filterv1.FilterRuleSet{filterv1.FilterRuleSet_builder{Name: new("acl"), Default: filterv1.FilterAction_FILTER_ACTION_ACCEPT.Enum()}.Build()},
				}
				domains := []string{"blue", "red"}
				if sibling {
					domains = append(domains, netmodel.DefaultNetworkInstance)
				}
				for _, domain := range domains {
					name := domain + "10"
					iface := routedVLANInterface(name, 10, []byte{0, 1, 2, 3, 4, 6})
					iface.GetIp().SetNetworkInstance(domain)
					iface.SetFilter(filterv1.FilterFacet_builder{InSet: new("acl"), OutSet: new("acl")}.Build())
					in.Interfaces = append(in.Interfaces, iface)
					in.VLANs = append(in.VLANs, switchingv1.Vlan_builder{NetworkInstance: new(domain), Id: new(uint32(10)), Name: new(domain)}.Build())
					in.Addresses = append(in.Addresses, ipv1.InterfaceAddress_builder{InterfaceName: new(name), Address: protoIPv4Addr([4]byte{10, 0, 0, 1}), Prefix: protoIPv4Prefix([4]byte{10, 0, 0, 0}, 24)}.Build())
					in.Neighbors = append(in.Neighbors, ipv1.NeighborEntry_builder{InterfaceName: new(name), Ip: protoIPv4Addr([4]byte{10, 0, 0, 2}), Mac: protoEUI48([6]byte{0, 1, 2, 3, 4, 7})}.Build())
					row := activeFDBRow("eth2", 10)
					row.SetNetworkInstance(domain)
					if domain == netmodel.DefaultNetworkInstance {
						row.SetInterfaceName("eth1")
					}
					in.FDB = append(in.FDB, row)
				}
				if reverse {
					in.Interfaces = reversed(in.Interfaces)
					in.VLANs = reversed(in.VLANs)
					in.FDB = reversed(in.FDB)
					in.Addresses = reversed(in.Addresses)
					in.Neighbors = reversed(in.Neighbors)
				}
				result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, in)
				cfg := result.Spec.Config
				if sibling {
					if cfg.Routing == nil || cfg.Filter == nil {
						t.Fatalf("default sibling lost: routing=%+v filter=%+v", cfg.Routing, cfg.Filter)
					}
					vrf := cfg.Routing.VRFs[routing.DefaultVRF]
					if len(vrf.Interfaces) != 1 || len(vrf.Interfaces["default10"].Prefixes) != 1 || len(vrf.Neighbors) != 1 || vrf.Neighbors[0].Interface != "default10" {
						t.Errorf("default ownership and dependents = %+v, want default10 only", vrf)
					}
					if len(cfg.Filter.Bindings) != 2 || cfg.Filter.Bindings[0].Interface != "default10" || cfg.Filter.Bindings[1].Interface != "default10" || cfg.Filter.Bindings[0].Direction == cfg.Filter.Bindings[1].Direction {
						t.Errorf("bindings = %+v, want default10 In and Out", cfg.Filter.Bindings)
					}
					if cfg.Bridge.VLAN.Table[10] != "default" || len(result.Spec.Seeds) != 1 || result.Spec.Seeds[0].Port != "eth1" {
						t.Errorf("VLANs=%+v seeds=%+v, want default name and eth1 seed", cfg.Bridge.VLAN.Table, result.Spec.Seeds)
					}
				} else if cfg.Routing != nil || cfg.Filter != nil || cfg.Bridge.VLAN.Table[10] != "" || len(result.Spec.Seeds) != 0 {
					t.Errorf("named contributions survived: config=%+v seeds=%+v", cfg, result.Spec.Seeds)
				}
				if len(result.Report.Conflicts) != 0 {
					t.Errorf("refused domains contested default claims: %+v", result.Report.Conflicts)
				}
				for _, domain := range []string{"blue", "red"} {
					for _, what := range []string{"ip", "ip_address", "ip_neighbor", "filter_in", "filter_out", "vlan", "fdb_entry"} {
						assertInstanceIssue(t, result, domain, what)
					}
				}
				defaultScope := routing.OwnershipScope("sw1", routing.DefaultVRF, "default10")
				if issues := result.Metadata.IssuesFor(defaultScope); len(issues) != 0 {
					t.Errorf("default sibling issues = %+v, want independent scope", issues)
				}
				if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
					t.Fatalf("construct: %v", err)
				}
				reached++
			})
		}
	}
	if reached != 4 {
		t.Errorf("reached %d domain cases, want 4", reached)
	}
}

func assertInstanceIssue(t *testing.T, result netmodel.Result, domain, what string) {
	t.Helper()
	scope := analysis.FieldScope(analysis.NodeScope("sw1"), "network_instance", domain, what)
	issues := result.Metadata.IssuesFor(scope)
	status, code := analysis.Unsupported, netmodel.IssueUnsupportedNetworkInstance
	if domain == "" {
		status, code = analysis.Incomplete, netmodel.IssueInvalidNetworkInstance
	}
	if len(issues) != 1 || issues[0].Status != status || issues[0].Code != code || !strings.Contains(issues[0].Message, "network_instance") || len(issues[0].Evidence) == 0 {
		t.Errorf("%s/%s issues = %+v, want one evidenced instance refusal", domain, what, issues)
	}
}

func TestLoadInstancePresence(t *testing.T) {
	reached := 0
	for _, domain := range []*string{nil, new(netmodel.DefaultNetworkInstance), new(""), new("blue")} {
		for _, what := range []string{"ip", "vlan", "fdb_entry", "stp_bridge", "mst_instance", "mst_vlan_map"} {
			label := "absent"
			if domain != nil {
				label = *domain
			}
			t.Run(what+"/"+label, func(t *testing.T) {
				in := netmodel.Input{Interfaces: []*interfacev1.Interface{switchedPhysicalInterface("eth1", 10)}}
				switch what {
				case "ip":
					iface := routedVLANInterface("vlan10", 10, []byte{0, 1, 2, 3, 4, 6})
					iface.SetIp(ipv1.IpFacet_builder{NetworkInstance: domain}.Build())
					in.Interfaces = append(in.Interfaces, iface)
				case "vlan":
					in.VLANs = []*switchingv1.Vlan{switchingv1.Vlan_builder{NetworkInstance: domain, Id: new(uint32(10)), Name: new("ten")}.Build()}
				case "fdb_entry":
					row := activeFDBRow("eth1", 10)
					row.ClearNetworkInstance()
					if domain != nil {
						row.SetNetworkInstance(*domain)
					}
					in.FDB = []*switchingv1.FdbEntry{row}
				case "stp_bridge":
					in.BridgeState = validBridgeState()
					in.BridgeState.ClearNetworkInstance()
					if domain != nil {
						in.BridgeState.SetNetworkInstance(*domain)
					}
					in.STPPorts = []*stpv1.PortState{stpv1.PortState_builder{InterfaceName: new("eth1")}.Build()}
				case "mst_instance":
					in.MSTInstances = []*stpv1.MstInstance{stpv1.MstInstance_builder{NetworkInstance: domain, MstId: new(uint32(1))}.Build()}
				case "mst_vlan_map":
					in.MSTVLANMaps = []*stpv1.MstVlanMap{stpv1.MstVlanMap_builder{NetworkInstance: domain, MstId: new(uint32(1)), VlanIds: []uint32{10}}.Build()}
				}
				result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, in)
				accepted := domain == nil || *domain == netmodel.DefaultNetworkInstance
				if !accepted {
					assertInstanceIssue(t, result, *domain, what)
				}
				assumptions := 0
				for _, a := range result.Metadata.Assumptions() {
					if strings.Contains(a.Statement, "network_instance") {
						assumptions++
						if len(a.Evidence) == 0 {
							t.Error("instance assumption lacks evidence")
						}
					}
				}
				wantAssumptions := 0
				if domain == nil {
					wantAssumptions = 1
				}
				if assumptions != wantAssumptions {
					t.Errorf("instance assumptions = %d, want %d", assumptions, wantAssumptions)
				}
				cfg := result.Spec.Config
				switch what {
				case "ip":
					if (cfg.Routing != nil) != accepted {
						t.Errorf("routing present = %t, want %t", cfg.Routing != nil, accepted)
					}
				case "vlan":
					if (cfg.Bridge.VLAN.Table[10] == "ten") != accepted {
						t.Errorf("VLAN name = %q, accepted=%t", cfg.Bridge.VLAN.Table[10], accepted)
					}
				case "fdb_entry":
					if (len(result.Spec.Seeds) == 1) != accepted {
						t.Errorf("seeds=%+v, accepted=%t", result.Spec.Seeds, accepted)
					}
				case "stp_bridge":
					if (cfg.STP != nil) != accepted || slices.Contains(result.Report.Capabilities, stp.LayerName) != accepted {
						t.Errorf("STP=%+v capabilities=%+v, accepted=%t", cfg.STP, result.Report.Capabilities, accepted)
					}
				}
				if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
					t.Fatalf("construct: %v", err)
				}
				reached++
			})
		}
	}
	if reached != 24 {
		t.Errorf("reached %d presence cases, want 24", reached)
	}
}

func TestLoadRefusedIPPreservesRawExclusions(t *testing.T) {
	physical := switchedPhysicalInterface("eth1", 10)
	physical.SetIp(ipv1.IpFacet_builder{NetworkInstance: new("blue")}.Build())
	parent := switchedPhysicalInterface("eth2", 10)
	sub := subInterface("eth2.10", "eth2", switchingv1.VlanTag_builder{Tpid: packetv1.EtherType_ETHER_TYPE_DOT1Q.Enum(), VlanId: new(uint32(10))}.Build())
	sub.GetIp().SetNetworkInstance("red")
	result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{
		Interfaces: []*interfacev1.Interface{physical, parent, sub}, BridgeState: validBridgeState(),
		STPPorts: []*stpv1.PortState{stpv1.PortState_builder{InterfaceName: new("eth1")}.Build(), stpv1.PortState_builder{InterfaceName: new("eth2")}.Build()},
		Want:     []trace.Layer{bridge.LayerNameVLAN, routing.LayerName, stp.LayerName},
	})
	if _, ok := result.Spec.Config.Bridge.VLAN.Switchports["eth1"]; ok {
		t.Error("raw IP physical interface regained switchport")
	}
	if _, ok := result.Spec.Config.STP.Ports["eth1"]; ok {
		t.Error("raw IP physical interface regained STP port")
	}
	if _, ok := result.Spec.Config.Bridge.VLAN.Switchports["eth2"]; !ok {
		t.Error("refused named sub-interface claimed parent switchport")
	}
	if _, ok := result.Spec.Config.STP.Ports["eth2"]; !ok {
		t.Error("refused named sub-interface claimed parent STP port")
	}
	if result.Spec.Config.Routing != nil {
		t.Error("refused named owners created routing")
	}
	if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
		t.Fatalf("construct: %v", err)
	}
}

func TestLoadNamedIPDoesNotInferCapabilities(t *testing.T) {
	iface := routedVLANInterface("blue10", 10, []byte{0, 1, 2, 3, 4, 6})
	iface.GetIp().SetNetworkInstance("blue")
	iface.SetFilter(filterv1.FilterFacet_builder{InSet: new("acl"), OutSet: new("acl")}.Build())
	result := load(t, netmodel.SourceContext{DeviceID: "sw1"}, netmodel.Input{Interfaces: []*interfacev1.Interface{plainPhysicalInterface("eth1"), iface}})
	if !slices.Equal(result.Report.Capabilities, []trace.Layer{bridge.LayerName}) || result.Spec.Config.Bridge.VLAN != nil {
		t.Errorf("capabilities = %+v, VLAN=%+v, want relay only", result.Report.Capabilities, result.Spec.Config.Bridge.VLAN)
	}
	for _, layer := range []trace.Layer{routing.LayerName, filter.LayerName, bridge.LayerNameVLAN} {
		if source := result.Report.CapabilitySources[layer]; source != "" {
			t.Errorf("%s source = %q, want none", layer, source)
		}
	}
	assertInstanceIssue(t, result, "blue", "ip")
	assertInstanceIssue(t, result, "blue", "filter_in")
	assertInstanceIssue(t, result, "blue", "filter_out")
	if _, err := vswitch.NewWithSpec(result.Spec); err != nil {
		t.Fatalf("construct: %v", err)
	}
}
