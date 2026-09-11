package netmodel_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"buf.build/go/protovalidate"

	addrv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/addr/v1"
	interfacev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/interface/v1"
	ipv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/ip/v1"
	switchingv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/switching/v1"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
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

func protoEUI48(octets [6]byte) *addrv1.EuiAddress {
	return addrv1.EuiAddress_builder{
		Eui48: addrv1.Eui48Address_builder{Octets: octets[:]}.Build(),
	}.Build()
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

	cfg, _, report, err := netmodel.Load(now, ifaces, vlans, nil, nil, nil, nil, addrs, neighbors, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

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

	cfg, _, report, err := netmodel.Load(now, ifaces, nil, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !slices.Contains(report.Capabilities, port.LayerRouting) {
		t.Errorf("expected routing capability in %v", report.Capabilities)
	}
	if slices.Contains(report.Capabilities, port.LayerVlan) {
		t.Errorf("did not expect vlan capability in %v", report.Capabilities)
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

	cfg, _, report, err := netmodel.Load(now, ifaces, vlans, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	vrf := cfg.Routing.VRFs[routing.DefaultVRF]
	iface := vrf.Interfaces["vlan10"]
	if iface.MAC != (netaddr.MAC{}) {
		t.Errorf("vlan10 MAC = %s, want zero MAC", iface.MAC)
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

	cfg, _, report, err := netmodel.Load(now, ifaces, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

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

	cfg, _, report, err := netmodel.Load(now, ifaces, nil, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

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

	cfg, _, report, err := netmodel.Load(now, ifaces, vlans, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

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

	cfg, _, report, err := netmodel.Load(now, ifaces, vlans, nil, nil, nil, nil, addrs, neighbors, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

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
	cfg, _, report, err := netmodel.Load(now, ifaces, nil, nil, nil, nil, nil, addrs, neighbors, want)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

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

	cfg, _, _, err := netmodel.Load(now, ifaces, vlans, nil, nil, nil, nil, addrs, nil, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	p, ok := cfg.Ports.Port("vlan10")
	if !ok {
		t.Fatalf("port table missing vlan10")
	}
	if p.Kind != port.Other {
		t.Errorf("vlan10 port kind = %v, want port.Other", p.Kind)
	}

	sw := vswitch.New(cfg)

	broadcastFrame := ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("test broadcast payload"),
	}

	res := sw.Forward(now, "1/1/1", broadcastFrame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("forward outcome = %v, want Flooded", res.Outcome)
	}

	if len(res.Egress) != 1 {
		t.Fatalf("egress count = %d, want 1", len(res.Egress))
	}
	if res.Egress[0].Port != "1/1/2" {
		t.Errorf("egress port = %q, want 1/1/2", res.Egress[0].Port)
	}
	for _, eg := range res.Egress {
		if eg.Port == "vlan10" {
			t.Errorf("vlan10 should not receive flooded broadcast frame")
		}
	}
}
