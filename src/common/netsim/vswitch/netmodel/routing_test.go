package netmodel_test

import (
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	packetv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/packet/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

func protoIPv4Addr(octets [4]byte) *addrv1.IpAddress {
	return addrv1.IpAddress_builder{
		V4: addrv1.Ipv4Address_builder{Octets: octets[:]}.Build(),
	}.Build()
}

func protoIPv4Prefix(masked [4]byte, length uint32) *addrv1.IpPrefix {
	return addrv1.IpPrefix_builder{
		V4: addrv1.Ipv4Prefix_builder{
			Address: addrv1.Ipv4Address_builder{Octets: masked[:]}.Build(),
			Length:  &length,
		}.Build(),
	}.Build()
}

func protoIPv6Addr(octets [16]byte) *addrv1.IpAddress {
	return addrv1.IpAddress_builder{
		V6: addrv1.Ipv6Address_builder{Octets: octets[:]}.Build(),
	}.Build()
}

func protoIPv6Prefix(masked [16]byte, length uint32) *addrv1.IpPrefix {
	return addrv1.IpPrefix_builder{
		V6: addrv1.Ipv6Prefix_builder{
			Address: addrv1.Ipv6Address_builder{Octets: masked[:]}.Build(),
			Length:  &length,
		}.Build(),
	}.Build()
}

func protoEUI48(octets [6]byte) *addrv1.EuiAddress {
	return addrv1.EuiAddress_builder{
		Eui48: addrv1.Eui48Address_builder{Octets: octets[:]}.Build(),
	}.Build()
}

func validateFixtures(t *testing.T, ifaces []*interfacev1.Interface, vlans []*switchingv1.Vlan, addrs []*ipv1.InterfaceAddress, neighbors []*ipv1.NeighborEntry) {
	t.Helper()
	for _, m := range ifaces {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("interface %s validation failed: %v", m.GetName(), err)
		}
	}
	for _, m := range vlans {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("vlan %d validation failed: %v", m.GetId(), err)
		}
	}
	for _, m := range addrs {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("address %s validation failed: %v", m.GetInterfaceName(), err)
		}
	}
	for _, m := range neighbors {
		if err := protovalidate.Validate(m); err != nil {
			t.Fatalf("neighbor %s validation failed: %v", m.GetInterfaceName(), err)
		}
	}
}

func TestLoad_VlanInterfacesRouting(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vlanMAC := protoEUI48([6]byte{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01})

	vid10 := uint32(10)
	vid20 := uint32(20)
	vlan10Name := "vlan10"
	vlan20Name := "vlan20"
	p1Name := "1/1/1"
	p2Name := "1/1/2"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &vlan10Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Mac:         vlanMAC,
			Vlan: interfacev1.VlanInterface_builder{
				VlanId: &vid10,
			}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &vlan20Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Mac:         vlanMAC,
			Vlan: interfacev1.VlanInterface_builder{
				VlanId: &vid20,
			}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:            &vid10,
					UntaggedVlanIds: []uint32{10},
				}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p2Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:            &vid20,
					UntaggedVlanIds: []uint32{20},
				}.Build(),
			}.Build(),
		}.Build(),
	}

	for _, iface := range ifaces {
		if err := protovalidate.Validate(iface); err != nil {
			t.Fatalf("interface %s validation failed: %v", iface.GetName(), err)
		}
	}

	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid10, Name: &vlan10Name}.Build(),
		switchingv1.Vlan_builder{Id: &vid20, Name: &vlan20Name}.Build(),
	}

	addrs := []*ipv1.InterfaceAddress{
		ipv1.InterfaceAddress_builder{
			InterfaceName: &vlan10Name,
			Address:       protoIPv4Addr([4]byte{10, 0, 10, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 10, 0}, 24),
		}.Build(),
		ipv1.InterfaceAddress_builder{
			InterfaceName: &vlan20Name,
			Address:       protoIPv4Addr([4]byte{10, 0, 20, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 20, 0}, 24),
		}.Build(),
	}
	for _, a := range addrs {
		if err := protovalidate.Validate(a); err != nil {
			t.Fatalf("address %s validation failed: %v", a.GetInterfaceName(), err)
		}
	}

	neighborMAC := protoEUI48([6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x77})
	neighbors := []*ipv1.NeighborEntry{
		ipv1.NeighborEntry_builder{
			InterfaceName: &vlan20Name,
			Ip:            protoIPv4Addr([4]byte{10, 0, 20, 7}),
			Mac:           neighborMAC,
		}.Build(),
	}
	for _, n := range neighbors {
		if err := protovalidate.Validate(n); err != nil {
			t.Fatalf("neighbor %s validation failed: %v", n.GetInterfaceName(), err)
		}
	}

	validateFixtures(t, ifaces, vlans, addrs, neighbors)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, vlans, nil, nil, nil, nil, nil, nil, addrs, neighbors, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	if !slices.Contains(report.Capabilities, port.LayerRouting) {
		t.Errorf("expected routing capability in %v", report.Capabilities)
	}
	if report.CapabilitySources[port.LayerRouting] != "inferred:ip" {
		t.Errorf("routing capability source = %q, want inferred:ip", report.CapabilitySources[port.LayerRouting])
	}

	hasVRFDefault := false
	hasMACDefault := false
	for _, d := range report.Defaults {
		if d.Port == "" && d.Field == "vrf" && d.Value == routing.DefaultVRF {
			hasVRFDefault = true
		}
		if d.Port == "" && d.Field == "mac" && d.Value == "assigned" {
			hasMACDefault = true
		}
	}
	if !hasVRFDefault {
		t.Errorf("expected vrf default in report: %+v", report.Defaults)
	}
	if !hasMACDefault {
		t.Errorf("expected assigned base MAC default in report: %+v", report.Defaults)
	}

	if cfg.Routing == nil {
		t.Fatal("expected Routing configuration to be non-nil")
	}
	vrf, ok := cfg.Routing.VRFs[routing.DefaultVRF]
	if !ok {
		t.Fatalf("missing default VRF in %v", cfg.Routing.VRFs)
	}

	if10, ok := vrf.Interfaces["vlan10"]
	if !ok {
		t.Fatalf("missing interface vlan10 in VRF default")
	}
	if if10.VLAN != 10 || if10.Port != "" {
		t.Errorf("vlan10 shape = VLAN:%d Port:%q, want VLAN:10 Port:\"\"", if10.VLAN, if10.Port)
	}
	wantMAC := netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01}
	if if10.MAC != wantMAC {
		t.Errorf("vlan10 MAC = %s, want %s", if10.MAC, wantMAC)
	}
	if len(if10.Prefixes) != 1 || if10.Prefixes[0].String() != "10.0.10.1/24" {
		t.Errorf("vlan10 prefixes = %v, want [10.0.10.1/24]", if10.Prefixes)
	}

	if20, ok := vrf.Interfaces["vlan20"]
	if !ok {
		t.Fatalf("missing interface vlan20 in VRF default")
	}
	if if20.VLAN != 20 || if20.Port != "" {
		t.Errorf("vlan20 shape = VLAN:%d Port:%q, want VLAN:20 Port:\"\"", if20.VLAN, if20.Port)
	}
	if if20.MAC != wantMAC {
		t.Errorf("vlan20 MAC = %s, want %s", if20.MAC, wantMAC)
	}
	if len(if20.Prefixes) != 1 || if20.Prefixes[0].String() != "10.0.20.1/24" {
		t.Errorf("vlan20 prefixes = %v, want [10.0.20.1/24]", if20.Prefixes)
	}

	if len(vrf.Neighbors) != 1 {
		t.Fatalf("neighbors count = %d, want 1", len(vrf.Neighbors))
	}
	n0 := vrf.Neighbors[0]
	if n0.Interface != "vlan20" || n0.Addr != netip.MustParseAddr("10.0.20.7") || n0.MAC != (netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}) {
		t.Errorf("neighbor[0] = %+v, want vlan20 10.0.20.7 00:11:22:33:44:77", n0)
	}

	if len(vrf.Routes) != 0 {
		t.Errorf("expected Routes to be empty, got: %+v", vrf.Routes)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}

func TestLoad_PhysicalRoutedPort(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	portName := "1/1/1"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &portName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
	}

	addrs := []*ipv1.InterfaceAddress{
		ipv1.InterfaceAddress_builder{
			InterfaceName: &portName,
			Address:       protoIPv4Addr([4]byte{10, 0, 50, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 50, 0}, 30),
		}.Build(),
	}

	validateFixtures(t, ifaces, nil, addrs, nil)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	if !slices.Contains(report.Capabilities, port.LayerRouting) {
		t.Errorf("expected routing capability in %v", report.Capabilities)
	}
	if !slices.Contains(report.Capabilities, port.LayerVlan) || report.CapabilitySources[port.LayerVlan] != "implied:routing" {
		t.Errorf("expected vlan implied by routing in %v (%v)", report.Capabilities, report.CapabilitySources)
	}
	if report.CapabilitySources[port.LayerRouting] != "inferred:ip" {
		t.Errorf("routing capability source = %q, want inferred:ip", report.CapabilitySources[port.LayerRouting])
	}

	if cfg.Routing == nil {
		t.Fatal("expected Routing configuration to be non-nil")
	}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	iface, ok := vrf.Interfaces["1/1/1"]
	if !ok {
		t.Fatalf("missing interface 1/1/1 in VRF default")
	}
	if iface.Port != "1/1/1" || iface.VLAN != 0 {
		t.Errorf("interface shape = Port:%q VLAN:%d, want Port:\"1/1/1\" VLAN:0", iface.Port, iface.VLAN)
	}
	if len(iface.Prefixes) != 1 || iface.Prefixes[0].String() != "10.0.50.1/30" {
		t.Errorf("prefixes = %v, want [10.0.50.1/30]", iface.Prefixes)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}

func TestLoad_VlanInterfaceDefaultMAC(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vlanName := "vlan10"
	vid10 := uint32(10)

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &vlanName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Vlan: interfacev1.VlanInterface_builder{
				VlanId: &vid10,
			}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
	}

	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid10, Name: &vlanName}.Build(),
	}

	addrs := []*ipv1.InterfaceAddress{
		ipv1.InterfaceAddress_builder{
			InterfaceName: &vlanName,
			Address:       protoIPv4Addr([4]byte{10, 0, 10, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 10, 0}, 24),
		}.Build(),
	}

	validateFixtures(t, ifaces, vlans, addrs, nil)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, vlans, nil, nil, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	iface := vrf.Interfaces["vlan10"]
	if iface.MAC != cfg.MAC || iface.MAC == (netaddr.MAC{}) {
		t.Errorf("vlan10 MAC = %s, want normalized switch MAC %s", iface.MAC, cfg.MAC)
	}

	hasIfaceMACDefault := false
	hasDeviceMACDefault := false
	for _, d := range report.Defaults {
		if d.Port == "vlan10" && d.Field == "mac" && d.Value == "device base address" {
			hasIfaceMACDefault = true
		}
		if d.Port == "" && d.Field == "mac" && d.Value == "assigned" {
			hasDeviceMACDefault = true
		}
	}
	if !hasIfaceMACDefault {
		t.Errorf("expected interface MAC default in report: %+v", report.Defaults)
	}
	if !hasDeviceMACDefault {
		t.Errorf("expected device MAC default in report: %+v", report.Defaults)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}

func TestLoad_LoopbackUnsupported(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	loName := "lo0"

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &loName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Loopback:    interfacev1.LoopbackInterface_builder{}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
	}

	validateFixtures(t, ifaces, nil, nil, nil)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "lo0" && s.What == "ip" && s.Why == "unsupported interface kind" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected unsupported interface kind skip for lo0, got: %+v", report.Skipped)
	}

	if cfg.Routing != nil {
		t.Errorf("expected Routing configuration to be nil when all facets skipped, got %+v", cfg.Routing)
	}
	if slices.Contains(report.Capabilities, port.LayerRouting) {
		t.Errorf("routing capability should be dropped from %v", report.Capabilities)
	}
	if _, ok := report.CapabilitySources[port.LayerRouting]; ok {
		t.Errorf("routing capability source should be removed from %v", report.CapabilitySources)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}

func TestLoad_RoutedPortSwitchportSkipped(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	portName := "1/1/1"
	vid10 := uint32(10)

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &portName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:            &vid10,
					UntaggedVlanIds: []uint32{10},
				}.Build(),
			}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
	}

	addrs := []*ipv1.InterfaceAddress{
		ipv1.InterfaceAddress_builder{
			InterfaceName: &portName,
			Address:       protoIPv4Addr([4]byte{10, 0, 50, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 50, 0}, 24),
		}.Build(),
	}

	validateFixtures(t, ifaces, nil, addrs, nil)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/1" && s.What == "switchport" && s.Why == "interface is routed" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected switchport skip with reason 'interface is routed', got: %+v", report.Skipped)
	}

	if cfg.Routing == nil {
		t.Fatal("expected Routing configuration to be non-nil")
	}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	iface, ok := vrf.Interfaces["1/1/1"]
	if !ok {
		t.Fatalf("missing interface 1/1/1 in VRF default")
	}
	if iface.Port != "1/1/1" {
		t.Errorf("iface.Port = %q, want 1/1/1", iface.Port)
	}

	if cfg.Bridge != nil && cfg.Bridge.VLAN != nil {
		if _, ok := cfg.Bridge.VLAN.Switchports["1/1/1"]; ok {
			t.Errorf("routed port 1/1/1 must not be loaded into Bridge.VLAN.Switchports")
		}
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}

func TestLoad_AddressWithoutIPFacetSkipped(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"
	vlanName := "vlan10"
	vid10 := uint32(10)

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &vlanName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Vlan: interfacev1.VlanInterface_builder{
				VlanId: &vid10,
			}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
	}

	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid10, Name: &vlanName}.Build(),
	}

	addrs := []*ipv1.InterfaceAddress{
		ipv1.InterfaceAddress_builder{
			InterfaceName: &p1Name,
			Address:       protoIPv4Addr([4]byte{10, 0, 50, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 50, 0}, 24),
		}.Build(),
		ipv1.InterfaceAddress_builder{
			InterfaceName: &vlanName,
			Address:       protoIPv4Addr([4]byte{10, 0, 10, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 10, 0}, 24),
		}.Build(),
	}

	validateFixtures(t, ifaces, vlans, addrs, nil)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, vlans, nil, nil, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.Port == "1/1/1" && s.What == "ip_address" && s.Why == "interface carries no ip facet" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected ip_address skip for 1/1/1, got: %+v", report.Skipped)
	}

	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	if _, ok := vrf.Interfaces["1/1/1"]; ok {
		t.Errorf("1/1/1 without IP facet should not be in VRF interfaces")
	}
	v10 := vrf.Interfaces["vlan10"]
	if len(v10.Prefixes) != 1 {
		t.Errorf("vlan10 prefixes count = %d, want 1", len(v10.Prefixes))
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}

func TestLoad_NeighborWithoutMACSkipped(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vlanName := "vlan10"
	vid10 := uint32(10)
	vlanMAC := protoEUI48([6]byte{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01})

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &vlanName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Mac:         vlanMAC,
			Vlan: interfacev1.VlanInterface_builder{
				VlanId: &vid10,
			}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
	}

	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid10, Name: &vlanName}.Build(),
	}

	addrs := []*ipv1.InterfaceAddress{
		ipv1.InterfaceAddress_builder{
			InterfaceName: &vlanName,
			Address:       protoIPv4Addr([4]byte{10, 0, 10, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 10, 0}, 24),
		}.Build(),
	}

	neighbors := []*ipv1.NeighborEntry{
		ipv1.NeighborEntry_builder{
			InterfaceName: &vlanName,
			Ip:            protoIPv4Addr([4]byte{10, 0, 10, 5}),
			// Deliberately omit MAC
		}.Build(),
	}

	validateFixtures(t, ifaces, vlans, addrs, neighbors)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, vlans, nil, nil, nil, nil, nil, nil, addrs, neighbors, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	foundSkipped := false
	for _, s := range report.Skipped {
		if s.What == "ip_neighbor" && s.Why == "neighbor has no mac" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected neighbor without mac skip, got: %+v", report.Skipped)
	}

	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	if len(vrf.Neighbors) != 0 {
		t.Errorf("expected 0 neighbors in VRF, got %d", len(vrf.Neighbors))
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}

func TestNeighborMissRetainsLoadedInvalidNeighborEvidence(t *testing.T) {
	inName := "in"
	outName := "out"
	input := loadInput{
		ifaces: []*interfacev1.Interface{routedPhysicalInterface(inName), routedPhysicalInterface(outName)},
		addrs: []*ipv1.InterfaceAddress{
			ipv1.InterfaceAddress_builder{
				InterfaceName: &inName,
				Address:       protoIPv4Addr([4]byte{192, 0, 2, 1}),
				Prefix:        protoIPv4Prefix([4]byte{192, 0, 2, 0}, 24),
			}.Build(),
			ipv1.InterfaceAddress_builder{
				InterfaceName: &outName,
				Address:       protoIPv4Addr([4]byte{198, 51, 100, 1}),
				Prefix:        protoIPv4Prefix([4]byte{198, 51, 100, 0}, 24),
			}.Build(),
		},
		neighbors: []*ipv1.NeighborEntry{
			ipv1.NeighborEntry_builder{
				InterfaceName: &outName,
				Ip:            protoIPv4Addr([4]byte{198, 51, 100, 7}),
			}.Build(),
		},
	}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "missing-neighbor-mac"})
	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	hdr := ip.Header{
		Src:      netip.MustParseAddr("192.0.2.7"),
		Dst:      netip.MustParseAddr("198.51.100.7"),
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	payload, err := hdr.Encode([]byte("neighbor evidence"))
	if err != nil {
		t.Fatalf("encode packet: %v", err)
	}
	iface := loaded.Spec.Config.Routing.VRFs[routing.DefaultVRF].Interfaces[inName]
	result := sw.Forward(trustTestTime, inName, ethernet.Frame{
		Src:       netaddr.MAC{2, 0, 0, 0, 0, 2},
		Dst:       iface.MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	})

	if result.Reason != routing.ReasonNeighborMiss {
		t.Fatalf("reason = %s, want neighbor-miss", result.Reason)
	}
	if result.Metadata.Status() != analysis.Incomplete {
		t.Fatalf("status = %s, want Incomplete; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
	}
	if !slices.ContainsFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueMissingNeighborMAC &&
			issue.Scope.Compare(routing.NeighborLookupScope(
				"sw1", routing.DefaultVRF, outName, netip.MustParseAddr("198.51.100.7"),
			)) == 0 &&
			len(issue.Evidence) > 0
	}) {
		t.Errorf("issues = %+v, want evidenced invalid neighbor issue on out", result.Metadata.Issues())
	}
}

func TestNoRouteRetainsConflictingOmittedPrefixEvidence(t *testing.T) {
	inName := "in"
	outName := "out"
	input := loadInput{
		ifaces: []*interfacev1.Interface{routedPhysicalInterface(inName), routedPhysicalInterface(outName)},
		addrs: []*ipv1.InterfaceAddress{
			ipv1.InterfaceAddress_builder{
				InterfaceName: &inName,
				Address:       protoIPv4Addr([4]byte{192, 0, 2, 1}),
				Prefix:        protoIPv4Prefix([4]byte{192, 0, 2, 0}, 24),
			}.Build(),
			ipv1.InterfaceAddress_builder{
				InterfaceName: &outName,
				Address:       protoIPv4Addr([4]byte{198, 51, 100, 129}),
				Prefix:        protoIPv4Prefix([4]byte{198, 51, 100, 0}, 24),
			}.Build(),
			ipv1.InterfaceAddress_builder{
				InterfaceName: &outName,
				Address:       protoIPv4Addr([4]byte{198, 51, 100, 129}),
				Prefix:        protoIPv4Prefix([4]byte{198, 51, 100, 128}, 25),
			}.Build(),
		},
	}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1", Origin: "snapshot", Context: "conflicting-prefixes"})
	sw, err := vswitch.NewWithSpec(loaded.Spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	hdr := ip.Header{
		Src:      netip.MustParseAddr("192.0.2.7"),
		Dst:      netip.MustParseAddr("198.51.100.130"),
		HopLimit: 64,
		Protocol: 17,
		V4:       &ip.V4{},
	}
	payload, err := hdr.Encode([]byte("conflicting prefix evidence"))
	if err != nil {
		t.Fatalf("encode packet: %v", err)
	}
	iface := loaded.Spec.Config.Routing.VRFs[routing.DefaultVRF].Interfaces[inName]
	result := sw.Forward(trustTestTime, inName, ethernet.Frame{
		Src:       netaddr.MAC{2, 0, 0, 0, 0, 2},
		Dst:       iface.MAC,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	})

	if result.Reason != routing.ReasonNoRoute {
		t.Fatalf("reason = %s, want no-route", result.Reason)
	}
	if result.Metadata.Status() != analysis.Unstable {
		t.Fatalf("status = %s, want Unstable; issues: %+v", result.Metadata.Status(), result.Metadata.Issues())
	}
	routeLookupScope := routing.RouteLookupScope("sw1", routing.DefaultVRF, netip.MustParseAddr("198.51.100.130"))
	if !slices.ContainsFunc(result.ConsultedScopes(), func(scope analysis.Scope) bool {
		return scope.Compare(routeLookupScope) == 0
	}) {
		t.Errorf("consulted scopes = %v, want %s", result.ConsultedScopes(), routeLookupScope)
	}
	routingScope := routing.VRFScope("sw1", routing.DefaultVRF)
	issueIndex := slices.IndexFunc(result.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueConflictAddress && issue.Scope.Compare(routingScope) == 0
	})
	if issueIndex < 0 {
		t.Fatalf("issues = %+v, want address conflict at %s", result.Metadata.Issues(), routingScope)
	}
	issue := result.Metadata.Issues()[issueIndex]
	if len(issue.Evidence) != 1 {
		t.Fatalf("issue evidence = %+v, want one original reference", issue.Evidence)
	}
	evidence, ok := result.Metadata.Evidence().Lookup(issue.Evidence[0])
	if !ok {
		t.Fatalf("issue evidence %q is absent from forwarding catalog", issue.Evidence[0])
	}
	if evidence.Origin != "snapshot" || !strings.Contains(evidence.Context, "conflict in ip_address for out/198.51.100.129") {
		t.Errorf("evidence = %+v, want original address conflict provenance", evidence)
	}
}

func TestLoad_UnwantedRoutingSkipsIP(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	vlanName := "vlan10"
	portName := "1/1/1"
	vid10 := uint32(10)

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &vlanName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Vlan: interfacev1.VlanInterface_builder{
				VlanId: &vid10,
			}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &portName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical:    interfacev1.PhysicalInterface_builder{}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
	}

	addrs := []*ipv1.InterfaceAddress{
		ipv1.InterfaceAddress_builder{
			InterfaceName: &vlanName,
			Address:       protoIPv4Addr([4]byte{10, 0, 10, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 10, 0}, 24),
		}.Build(),
		ipv1.InterfaceAddress_builder{
			InterfaceName: &portName,
			Address:       protoIPv4Addr([4]byte{10, 0, 50, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 50, 0}, 24),
		}.Build(),
	}

	neighbors := []*ipv1.NeighborEntry{
		ipv1.NeighborEntry_builder{
			InterfaceName: &vlanName,
			Ip:            protoIPv4Addr([4]byte{10, 0, 10, 5}),
			Mac:           protoEUI48([6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}),
		}.Build(),
	}

	want := []port.Layer{port.LayerRelay}
	validateFixtures(t, ifaces, nil, addrs, neighbors)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, nil, nil, nil, nil, nil, nil, nil, addrs, neighbors, want)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config
	report := res.Report

	if cfg.Routing != nil {
		t.Errorf("expected Routing configuration to be nil when routing not in want, got %+v", cfg.Routing)
	}
	if slices.Contains(report.Capabilities, port.LayerRouting) {
		t.Errorf("routing capability should not be in %v", report.Capabilities)
	}

	hasSkipIfaceVlan := false
	hasSkipIfacePort := false
	hasSkipAddrVlan := false
	hasSkipAddrPort := false
	hasSkipNeighbor := false

	for _, s := range report.Skipped {
		if s.Port == "vlan10" && s.What == "ip" && s.Why == "layer not wanted" {
			hasSkipIfaceVlan = true
		}
		if s.Port == "1/1/1" && s.What == "ip" && s.Why == "layer not wanted" {
			hasSkipIfacePort = true
		}
		if s.Port == "vlan10" && s.What == "ip_address" && s.Why == "layer not wanted" {
			hasSkipAddrVlan = true
		}
		if s.Port == "1/1/1" && s.What == "ip_address" && s.Why == "layer not wanted" {
			hasSkipAddrPort = true
		}
		if s.Port == "vlan10" && s.What == "ip_neighbor" && s.Why == "layer not wanted" {
			hasSkipNeighbor = true
		}
	}

	if !hasSkipIfaceVlan || !hasSkipIfacePort || !hasSkipAddrVlan || !hasSkipAddrPort || !hasSkipNeighbor {
		t.Errorf("missing expected skips for unwanted routing, got: %+v", report.Skipped)
	}
}

func TestLoad_VlanInterfaceOtherKindAbsentFromFlood(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	adminUp := interfacev1.AdminStatus_ADMIN_STATUS_UP
	operUp := interfacev1.OperStatus_OPER_STATUS_UP
	p1Name := "1/1/1"
	p2Name := "1/1/2"
	vlanName := "vlan10"
	vid10 := uint32(10)
	vlanMAC := protoEUI48([6]byte{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01})

	ifaces := []*interfacev1.Interface{
		interfacev1.Interface_builder{
			Name:        &p1Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:            &vid10,
					UntaggedVlanIds: []uint32{10},
				}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &p2Name,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Physical: interfacev1.PhysicalInterface_builder{
				Switchport: switchingv1.SwitchportFacet_builder{
					Pvid:            &vid10,
					UntaggedVlanIds: []uint32{10},
				}.Build(),
			}.Build(),
		}.Build(),
		interfacev1.Interface_builder{
			Name:        &vlanName,
			AdminStatus: &adminUp,
			OperStatus:  &operUp,
			Mac:         vlanMAC,
			Vlan: interfacev1.VlanInterface_builder{
				VlanId: &vid10,
			}.Build(),
			Ip: ipv1.IpFacet_builder{
				Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
			}.Build(),
		}.Build(),
	}

	vlans := []*switchingv1.Vlan{
		switchingv1.Vlan_builder{Id: &vid10, Name: &vlanName}.Build(),
	}

	addrs := []*ipv1.InterfaceAddress{
		ipv1.InterfaceAddress_builder{
			InterfaceName: &vlanName,
			Address:       protoIPv4Addr([4]byte{10, 0, 10, 1}),
			Prefix:        protoIPv4Prefix([4]byte{10, 0, 10, 0}, 24),
		}.Build(),
	}

	validateFixtures(t, ifaces, vlans, addrs, nil)
	res, err := netmodel.Load(now, netmodel.SourceContext{DeviceID: "sw1"}, ifaces, vlans, nil, nil, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cfg := res.Spec.Config

	p, ok := cfg.Ports.Port("vlan10")
	if !ok {
		t.Fatalf("port table missing vlan10")
	}
	if p.Kind != port.Other {
		t.Errorf("vlan10 port kind = %v, want port.Other", p.Kind)
	}

	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	broadcastFrame := ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test broadcast payload"),
	}

	fwdRes := sw.Forward(now, "1/1/1", broadcastFrame)
	if fwdRes.Outcome != trace.Flooded {
		t.Fatalf("forward outcome = %v, want Flooded", fwdRes.Outcome)
	}

	if len(fwdRes.Egress) != 1 {
		t.Fatalf("egress count = %d, want 1", len(fwdRes.Egress))
	}
	if fwdRes.Egress[0].Port != "1/1/2" {
		t.Errorf("egress port = %q, want 1/1/2", fwdRes.Egress[0].Port)
	}
	for _, eg := range fwdRes.Egress {
		if eg.Port == "vlan10" {
			t.Errorf("vlan10 should not receive flooded broadcast frame")
		}
	}
}

func TestLoadRoutingAcceptsZeroLengthPrefixes(t *testing.T) {
	for _, test := range []struct {
		name    string
		iface   *interfacev1.Interface
		address *addrv1.IpAddress
		prefix  *addrv1.IpPrefix
		want    string
	}{
		{
			name:    "IPv4",
			iface:   routedVLANInterface("vlan10", 10, []byte{0, 1, 2, 3, 4, 5}),
			address: protoIPv4Addr([4]byte{203, 0, 113, 7}),
			prefix:  protoIPv4Prefix([4]byte{}, 0),
			want:    "203.0.113.7/0",
		},
		{
			name: "IPv6",
			iface: func() *interfacev1.Interface {
				name := "vlan10"
				vid := uint32(10)
				admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
				oper := interfacev1.OperStatus_OPER_STATUS_UP
				return interfacev1.Interface_builder{
					Name: &name, AdminStatus: &admin, OperStatus: &oper,
					Vlan: interfacev1.VlanInterface_builder{VlanId: &vid}.Build(),
					Ip:   ipv1.IpFacet_builder{Ipv6: ipv1.Ipv6Facet_builder{}.Build()}.Build(),
				}.Build()
			}(),
			address: protoIPv6Addr([16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x07}),
			prefix:  protoIPv6Prefix([16]byte{}, 0),
			want:    "2001:db8::7/0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := test.iface.GetName()
			result := (loadInput{
				ifaces: []*interfacev1.Interface{test.iface},
				addrs: []*ipv1.InterfaceAddress{
					ipv1.InterfaceAddress_builder{InterfaceName: &name, Address: test.address, Prefix: test.prefix}.Build(),
				},
			}).load(t, netmodel.SourceContext{DeviceID: "sw1"})

			prefixes := result.Spec.Config.Routing.VRFs[routing.DefaultVRF].Interfaces[name].Prefixes
			if len(prefixes) != 1 || prefixes[0].String() != test.want {
				t.Errorf("prefixes = %v, want [%s]", prefixes, test.want)
			}
		})
	}
}

func vlanTag(tpid packetv1.EtherType, vid uint32) *switchingv1.VlanTag {
	pcp := uint32(0)
	dei := false
	return switchingv1.VlanTag_builder{
		Tpid:   &tpid,
		VlanId: &vid,
		Pcp:    &pcp,
		Dei:    &dei,
	}.Build()
}

// subInterface builds a Subinterface-kind interface over parent, carrying
// an IP facet so it is eligible to route. A nil tags slice leaves the
// encapsulation absent, matching a source that reported no encapsulation.
func subInterface(name, parent string, tags ...*switchingv1.VlanTag) *interfacev1.Interface {
	admin := interfacev1.AdminStatus_ADMIN_STATUS_UP
	oper := interfacev1.OperStatus_OPER_STATUS_UP

	var stack *switchingv1.VlanTagStack
	if tags != nil {
		stack = switchingv1.VlanTagStack_builder{Tags: tags}.Build()
	}

	return interfacev1.Interface_builder{
		Name:        &name,
		AdminStatus: &admin,
		OperStatus:  &oper,
		Sub: interfacev1.Subinterface_builder{
			Parent:        &parent,
			Encapsulation: stack,
		}.Build(),
		Ip: ipv1.IpFacet_builder{
			Ipv4: ipv1.Ipv4Facet_builder{}.Build(),
		}.Build(),
	}.Build()
}

// TestLoad_SubInterfacesRouteOverPhysicalParent pins that two sub-interfaces
// carved out of one physical parent by their outer C-TAG each load as a
// routed interface carrying the parent's port name and the tag's VID, the
// parent alone reaches the port table, and the loaded spec builds through
// vswitch.NewWithSpec.
func TestLoad_SubInterfacesRouteOverPhysicalParent(t *testing.T) {
	parentName := "eth1"
	sub10Name := "eth1.10"
	sub20Name := "eth1.20"

	parent := plainPhysicalInterface(parentName)
	sub10 := subInterface(sub10Name, parentName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10))
	sub20 := subInterface(sub20Name, parentName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 20))

	input := loadInput{ifaces: []*interfacev1.Interface{parent, sub10, sub20}}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

	cfg := loaded.Spec.Config
	if cfg.Routing == nil {
		t.Fatal("expected Routing configuration to be non-nil")
	}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]

	if10, ok := vrf.Interfaces[sub10Name]
	if !ok || if10.Port != parentName || if10.VLAN != 10 {
		t.Errorf("eth1.10 = %+v, want Port:%q VLAN:10", if10, parentName)
	}
	if20, ok := vrf.Interfaces[sub20Name]
	if !ok || if20.Port != parentName || if20.VLAN != 20 {
		t.Errorf("eth1.20 = %+v, want Port:%q VLAN:20", if20, parentName)
	}

	if _, ok := cfg.Ports.Port(parentName); !ok {
		t.Errorf("port table missing parent %s", parentName)
	}
	if _, ok := cfg.Ports.Port(sub10Name); ok {
		t.Errorf("port table must not hold sub-interface %s", sub10Name)
	}
	if _, ok := cfg.Ports.Port(sub20Name); ok {
		t.Errorf("port table must not hold sub-interface %s", sub20Name)
	}

	report := loaded.Report
	if !slices.Contains(report.Capabilities, port.LayerRouting) || report.CapabilitySources[port.LayerRouting] != "inferred:ip" {
		t.Errorf("routing capability = %v (%v), want inferred:ip", report.Capabilities, report.CapabilitySources)
	}
	if !slices.Contains(report.Capabilities, port.LayerVlan) || report.CapabilitySources[port.LayerVlan] != "implied:routing" {
		t.Errorf("vlan capability = %v (%v), want implied:routing", report.Capabilities, report.CapabilitySources)
	}
	if !slices.Contains(report.Capabilities, port.LayerRelay) || report.CapabilitySources[port.LayerRelay] != "always" {
		t.Errorf("relay capability = %v (%v), want always", report.Capabilities, report.CapabilitySources)
	}

	if _, err := vswitch.NewWithSpec(loaded.Spec); err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
}

// TestLoad_SubInterfaceUnsupportedEncapsulation pins that an unsupported
// sub-interface encapsulation is rejected rather than routed: a two-tag
// stack, an S-Tag, a priority-tagged VID, a reserved VID, and an absent
// encapsulation each raise netmodel.routing.unsupported_encapsulation
// scoped to the parent port, and the sub-interface still counts toward the
// routed-interface capability flag even though its own encapsulation is
// rejected, because the capability walk runs before the routing walk and
// cannot see the outcome of this check.
func TestLoad_SubInterfaceUnsupportedEncapsulation(t *testing.T) {
	parentName := "eth1"
	subName := "eth1.10"

	for _, test := range []struct {
		name string
		tags []*switchingv1.VlanTag
		// skipProtovalidate is set for a tag protovalidate itself refuses
		// (a reserved VID), which netmodel must still handle defensively
		// since Load does not assume its caller ran protovalidate.
		skipProtovalidate bool
	}{
		{
			name: "TwoTagStack",
			tags: []*switchingv1.VlanTag{
				vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10),
				vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 20),
			},
		},
		{
			name: "STag",
			tags: []*switchingv1.VlanTag{
				vlanTag(packetv1.EtherType_ETHER_TYPE_PROVIDER_BRIDGING, 10),
			},
		},
		{
			name: "PriorityTaggedVID",
			tags: []*switchingv1.VlanTag{
				vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 0),
			},
		},
		{
			name: "ReservedVID",
			tags: []*switchingv1.VlanTag{
				vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 4095),
			},
			skipProtovalidate: true,
		},
		{
			name: "AbsentEncapsulation",
			tags: nil,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := plainPhysicalInterface(parentName)
			sub := subInterface(subName, parentName, test.tags...)

			input := loadInput{ifaces: []*interfacev1.Interface{parent, sub}}
			if !test.skipProtovalidate {
				input.validate(t)
			}
			loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

			wantScope := routing.PortLookupScope("sw1", routing.DefaultVRF, parentName)
			if !slices.ContainsFunc(loaded.Metadata.Issues(), func(issue analysis.Issue) bool {
				return issue.Code == netmodel.IssueUnsupportedEncapsulation && issue.Scope.Compare(wantScope) == 0
			}) {
				t.Errorf("issues = %+v, want unsupported_encapsulation at %s", loaded.Metadata.Issues(), wantScope)
			}

			if cfg := loaded.Spec.Config; cfg.Routing != nil {
				if _, ok := cfg.Routing.VRFs[routing.DefaultVRF].Interfaces[subName]; ok {
					t.Errorf("rejected sub-interface %s must not appear in VRF interfaces", subName)
				}
			}

			if !slices.Contains(loaded.Report.Capabilities, port.LayerVlan) ||
				loaded.Report.CapabilitySources[port.LayerVlan] != "implied:routing" {
				t.Errorf("vlan capability = %v (%v), want implied:routing", loaded.Report.Capabilities, loaded.Report.CapabilitySources)
			}
		})
	}
}

// TestLoad_SubInterfaceInvalidParentKind pins that a sub-interface whose
// parent is absent from the load, names an interface that is not a physical
// or LAG port, or names a physical interface that is itself a LAG member,
// keeps raising netmodel.routing.unsupported_interface_kind rather than the
// encapsulation code or the LAG-member-as-routed-port refusal that routing
// validation would otherwise raise.
func TestLoad_SubInterfaceInvalidParentKind(t *testing.T) {
	subName := "eth1.10"

	for _, test := range []struct {
		name   string
		parent string
		ifaces []*interfacev1.Interface
	}{
		{
			name:   "ParentIsVLANInterface",
			parent: "vlan10",
			ifaces: []*interfacev1.Interface{
				routedVLANInterface("vlan10", 10, []byte{0, 1, 2, 3, 4, 5}),
			},
		},
		{
			name:   "ParentAbsentFromLoad",
			parent: "eth9",
		},
		{
			name:   "ParentIsLAGMember",
			parent: "1/1/1",
			ifaces: lagInterfaces(),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sub := subInterface(subName, test.parent, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10))
			ifaces := append(append([]*interfacev1.Interface{}, test.ifaces...), sub)

			input := loadInput{ifaces: ifaces}
			input.validate(t)
			loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

			wantScope := routing.PortLookupScope("sw1", routing.DefaultVRF, test.parent)
			if !slices.ContainsFunc(loaded.Metadata.Issues(), func(issue analysis.Issue) bool {
				return issue.Code == netmodel.IssueUnsupportedInterfaceKind && issue.Scope.Compare(wantScope) == 0
			}) {
				t.Errorf("issues = %+v, want unsupported_interface_kind at %s", loaded.Metadata.Issues(), wantScope)
			}
		})
	}
}

// TestLoad_SubInterfaceRoutesOverLAGParent pins that a sub-interface accepts
// a link-aggregation parent, the other half of the parent-kind check that
// TestLoad_SubInterfacesRouteOverPhysicalParent already pins for a physical
// parent.
func TestLoad_SubInterfaceRoutesOverLAGParent(t *testing.T) {
	lagName := "lag1"
	subName := "lag1.10"

	sub := subInterface(subName, lagName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10))
	ifaces := append(lagInterfaces(), sub)

	input := loadInput{ifaces: ifaces}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

	cfg := loaded.Spec.Config
	if cfg.Routing == nil {
		t.Fatal("expected Routing configuration to be non-nil")
	}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	iface, ok := vrf.Interfaces[subName]
	if !ok || iface.Port != lagName || iface.VLAN != 10 {
		t.Errorf("%s = %+v, want Port:%q VLAN:10", subName, iface, lagName)
	}

	if _, err := vswitch.NewWithSpec(loaded.Spec); err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
}

// TestLoad_DuplicateSubInterfaceNameRefused pins that a duplicate interface
// name is refused even when one of the colliding rows is a sub-interface,
// which the port builder never sees and so cannot catch on the builder's
// own duplicate-name guard.
func TestLoad_DuplicateSubInterfaceNameRefused(t *testing.T) {
	parentName := "eth1"
	subName := "eth1.10"

	parent := plainPhysicalInterface(parentName)
	subA := subInterface(subName, parentName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10))
	subB := subInterface(subName, parentName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 20))

	ifaces := []*interfacev1.Interface{parent, subA, subB}
	validateFixtures(t, ifaces, nil, nil, nil)

	_, err := netmodel.Load(
		trustTestTime, netmodel.SourceContext{DeviceID: "sw1"},
		ifaces, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	if err == nil {
		t.Fatal("Load succeeded, want an error for the duplicate interface name eth1.10")
	}
}

// TestLoad_SubInterfaceParentSwitchportSkippedAsRouted pins that a physical
// parent with a switchport facet and no address facet of its own is skipped
// out of the bridge switchport table when a sub-interface over it carries
// an address facet, the same way a directly routed physical port is
// skipped. Without this, the parent lands in the bridge switchport table
// while the routing walk also claims it as a routed port, and
// vswitch.Config.Validate refuses a routed port configured as a bridge
// switchport.
func TestLoad_SubInterfaceParentSwitchportSkippedAsRouted(t *testing.T) {
	parentName := "eth1"
	subName := "eth1.10"

	parent := switchedPhysicalInterface(parentName, 10)
	sub := subInterface(subName, parentName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10))

	input := loadInput{ifaces: []*interfacev1.Interface{parent, sub}}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

	foundSkipped := false
	for _, s := range loaded.Report.Skipped {
		if s.Port == parentName && s.What == "switchport" && s.Why == "interface is routed" {
			foundSkipped = true
			break
		}
	}
	if !foundSkipped {
		t.Errorf("expected switchport skip with reason 'interface is routed' for %s, got: %+v", parentName, loaded.Report.Skipped)
	}

	cfg := loaded.Spec.Config
	if cfg.Bridge != nil && cfg.Bridge.VLAN != nil {
		if _, ok := cfg.Bridge.VLAN.Switchports[parentName]; ok {
			t.Errorf("parent port %s must not be loaded into Bridge.VLAN.Switchports", parentName)
		}
	}
	if cfg.Routing == nil {
		t.Fatal("expected Routing configuration to be non-nil")
	}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	iface, ok := vrf.Interfaces[subName]
	if !ok || iface.Port != parentName || iface.VLAN != 10 {
		t.Errorf("%s = %+v, want Port:%q VLAN:10", subName, iface, parentName)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}

// TestLoad_DuplicateSubInterfaceClaimSkipped pins that two sub-interfaces
// naming the same parent and outer VID load one and skip the other as a
// conflict rather than both reaching the VRF, where routing validation
// would refuse the duplicate (port, VLAN) claim and fail the whole load.
func TestLoad_DuplicateSubInterfaceClaimSkipped(t *testing.T) {
	parentName := "eth1"
	subAName := "eth1-vid10-a"
	subBName := "eth1-vid10-b"

	parent := plainPhysicalInterface(parentName)
	subA := subInterface(subAName, parentName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10))
	subB := subInterface(subBName, parentName, vlanTag(packetv1.EtherType_ETHER_TYPE_DOT1Q, 10))

	input := loadInput{ifaces: []*interfacev1.Interface{parent, subA, subB}}
	input.validate(t)
	loaded := input.load(t, netmodel.SourceContext{DeviceID: "sw1"})

	wantScope := routing.PortLookupScope("sw1", routing.DefaultVRF, parentName)
	if !slices.ContainsFunc(loaded.Metadata.Issues(), func(issue analysis.Issue) bool {
		return issue.Code == netmodel.IssueConflictRoutedClaim && issue.Scope.Compare(wantScope) == 0
	}) {
		t.Errorf("issues = %+v, want claim conflict at %s", loaded.Metadata.Issues(), wantScope)
	}

	cfg := loaded.Spec.Config
	if cfg.Routing == nil {
		t.Fatal("expected Routing configuration to be non-nil")
	}
	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	if _, ok := vrf.Interfaces[subAName]; !ok {
		t.Errorf("first claimant %s must load", subAName)
	}
	if _, ok := vrf.Interfaces[subBName]; ok {
		t.Errorf("second claimant %s must not load", subBName)
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("cfg.Validate failed: %v", err)
	}
}
