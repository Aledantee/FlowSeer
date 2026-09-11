package fabric_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/mld"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

var mcastFabricMACs = map[string]netaddr.MAC{
	"h1": {0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
	"h2": {0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
	"h3": {0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
	"h4": {0x00, 0x11, 0x22, 0x33, 0x44, 0x04},
}

func newMcastFabric(t *testing.T, floodUnregistered *bool, fastLeave bool) *fabric.Fabric {
	t.Helper()

	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 4, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	pvid := vlan.ID(10)
	portNames := []string{"1/1/1", "1/1/2", "1/1/3", "1/1/4"}
	switchports := make(map[string]bridge.Switchport, 4)
	for _, name := range portNames {
		switchports[name] = bridge.Switchport{PVID: &pvid, Untagged: []vlan.ID{10}}
	}

	cables := make([]fabric.Cable, 0, 4)
	for i, host := range []string{"h1", "h2", "h3", "h4"} {
		cables = append(cables, fabric.Cable{
			A: fabric.Endpoint{Node: host},
			B: fabric.Endpoint{Node: "sw1", Port: portNames[i]},
		})
	}
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports,
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table:       map[vlan.ID]string{10: "ten"},
					Switchports: switchports,
				}},
				Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
					10: {FloodUnregistered: floodUnregistered, FastLeave: fastLeave},
				}},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: mcastFabricMACs["h1"]},
			"h2": {Address: mcastFabricMACs["h2"]},
			"h3": {Address: mcastFabricMACs["h3"]},
			"h4": {Address: mcastFabricMACs["h4"]},
		},
		Cables: cables,
	})
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	return fab
}

func fabricIGMPFrame(t *testing.T, host string, src, dst netip.Addr, dstMAC netaddr.MAC, message igmp.Message) ethernet.Frame {
	t.Helper()

	payload, err := igmp.Encode(message)
	if err != nil {
		t.Fatalf("encode IGMP: %v", err)
	}
	packet, err := (ip.Header{Src: src, Dst: dst, HopLimit: 1, Protocol: 2, V4: &ip.V4{}}).Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv4: %v", err)
	}

	return ethernet.Frame{Src: mcastFabricMACs[host], Dst: dstMAC, EtherType: ethernet.EtherTypeIPv4, Payload: packet}
}

func fabricMLDFrame(t *testing.T, host string, src, dst netip.Addr, message mld.Message) ethernet.Frame {
	t.Helper()

	hdr := ip.Header{Src: src, Dst: dst, HopLimit: 1, Protocol: 0, V6: &ip.V6{}}
	payload, err := mld.Encode(hdr, message)
	if err != nil {
		t.Fatalf("encode MLD: %v", err)
	}
	payload = append([]byte{58, 0, 5, 2, 0, 0, 1, 0}, payload...)
	packet, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("encode IPv6: %v", err)
	}

	dstBytes := dst.As16()
	dstMAC := netaddr.MAC{0x33, 0x33, dstBytes[12], dstBytes[13], dstBytes[14], dstBytes[15]}
	return ethernet.Frame{Src: mcastFabricMACs[host], Dst: dstMAC, EtherType: ethernet.EtherTypeIPv6, Payload: packet}
}

func fabricGroupData(t *testing.T, dst netip.Addr) ethernet.Frame {
	t.Helper()

	src := netip.MustParseAddr("10.0.0.3")
	dstBytes := dst.As4()
	dstMAC := netaddr.MAC{0x01, 0x00, 0x5e, dstBytes[1] & 0x7f, dstBytes[2], dstBytes[3]}
	packet, err := (ip.Header{Src: src, Dst: dst, HopLimit: 32, Protocol: 17, V4: &ip.V4{}}).Encode([]byte("group data"))
	if err != nil {
		t.Fatalf("encode group data: %v", err)
	}

	return ethernet.Frame{Src: mcastFabricMACs["h3"], Dst: dstMAC, EtherType: ethernet.EtherTypeIPv4, Payload: packet}
}

func fabricIPv6GroupData(t *testing.T, host string, dst netip.Addr) ethernet.Frame {
	t.Helper()

	packet, err := (ip.Header{
		Src: netip.MustParseAddr("2001:db8::3"), Dst: dst, HopLimit: 32, Protocol: 17, V6: &ip.V6{},
	}).Encode([]byte("group data"))
	if err != nil {
		t.Fatalf("encode IPv6 group data: %v", err)
	}
	dstBytes := dst.As16()
	dstMAC := netaddr.MAC{0x33, 0x33, dstBytes[12], dstBytes[13], dstBytes[14], dstBytes[15]}

	return ethernet.Frame{Src: mcastFabricMACs[host], Dst: dstMAC, EtherType: ethernet.EtherTypeIPv6, Payload: packet}
}

func injectMcastFrame(t *testing.T, fab *fabric.Fabric, at time.Time, host string, frame ethernet.Frame) fabric.Journey {
	t.Helper()

	id, err := fab.Inject(fabric.Injection{At: at, Origin: fabric.Endpoint{Node: host}, Frame: frame})
	if err != nil {
		t.Fatalf("Inject from %s: %v", host, err)
	}
	fab.Run(100)
	for _, journey := range fab.Report() {
		if journey.FrameID == id {
			return journey
		}
	}
	t.Fatalf("journey %d not found", id)
	return fabric.Journey{}
}

func deliveryHosts(journey fabric.Journey) []string {
	hosts := make([]string, len(journey.Deliveries))
	for i, delivery := range journey.Deliveries {
		hosts[i] = delivery.Host
	}
	slices.Sort(hosts)

	return hosts
}

func TestFabricMulticastGroupForwardingAndSnapshot(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fab := newMcastFabric(t, nil, false)
	group := netip.MustParseAddr("239.1.1.1")
	query := fabricIGMPFrame(t, "h4", netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		netaddr.MAC{0x01, 0x00, 0x5e, 0, 0, 1}, igmp.Message{Type: igmp.Query, Version: igmp.V2})
	if got := deliveryHosts(injectMcastFrame(t, fab, t0, "h4", query)); !slices.Equal(got, []string{"h1", "h2", "h3"}) {
		t.Errorf("query deliveries = %v, want [h1 h2 h3]", got)
	}
	report := fabricIGMPFrame(t, "h1", netip.MustParseAddr("10.0.0.1"), group,
		netaddr.MAC{0x01, 0x00, 0x5e, 1, 1, 1}, igmp.Message{Type: igmp.ReportV2, Group: group})
	if got := deliveryHosts(injectMcastFrame(t, fab, t0.Add(time.Second), "h1", report)); !slices.Equal(got, []string{"h4"}) {
		t.Errorf("report deliveries = %v, want [h4]", got)
	}

	journey := injectMcastFrame(t, fab, t0.Add(2*time.Second), "h3", fabricGroupData(t, group))
	if got := deliveryHosts(journey); !slices.Equal(got, []string{"h1", "h4"}) {
		t.Errorf("group deliveries = %v, want [h1 h4] with h2 absent", got)
	}
	var replicated bool
	for _, entry := range journey.Entries {
		if entry.Result != nil && slices.ContainsFunc(entry.Result.Steps, func(step trace.Step) bool {
			return step.Op == trace.OpReplicate && step.Detail == "group members"
		}) {
			replicated = true
		}
	}
	if !replicated {
		t.Errorf("journey entries = %+v, want group members replication", journey.Entries)
	}

	device := fab.Snapshot().Devices["sw1"]
	if groups := device.Groups[10]; len(groups) != 1 || groups[0].Group != group || groups[0].Port != "1/1/1" {
		t.Errorf("snapshot Groups[10] = %+v, want h1 membership", groups)
	}
	if routers := device.RouterPorts[10]; len(routers) != 1 || routers[0].Port != "1/1/4" {
		t.Errorf("snapshot RouterPorts[10] = %+v, want 1/1/4", routers)
	}
}

func TestFabricUnregisteredGroups(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	group := netip.MustParseAddr("239.2.2.2")

	fab := newMcastFabric(t, nil, false)
	journey := injectMcastFrame(t, fab, t0, "h3", fabricGroupData(t, group))
	if got := deliveryHosts(journey); !slices.Equal(got, []string{"h1", "h2", "h4"}) {
		t.Errorf("default unregistered deliveries = %v, want flood", got)
	}

	flood := false
	fab = newMcastFabric(t, &flood, false)
	query := fabricIGMPFrame(t, "h4", netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		netaddr.MAC{0x01, 0x00, 0x5e, 0, 0, 1}, igmp.Message{Type: igmp.Query, Version: igmp.V2})
	injectMcastFrame(t, fab, t0, "h4", query)
	journey = injectMcastFrame(t, fab, t0.Add(time.Second), "h3", fabricGroupData(t, group))
	if got := deliveryHosts(journey); !slices.Equal(got, []string{"h4"}) {
		t.Errorf("router-only unregistered deliveries = %v, want [h4]", got)
	}
	for i, tt := range []struct {
		name  string
		frame ethernet.Frame
	}{
		{name: "IPv4 link-local group", frame: fabricGroupData(t, netip.MustParseAddr("224.0.0.5"))},
		{name: "IPv6 all nodes", frame: fabricIPv6GroupData(t, "h3", netip.MustParseAddr("ff02::1"))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			journey := injectMcastFrame(t, fab, t0.Add(time.Duration(i+2)*time.Second), "h3", tt.frame)
			if got := deliveryHosts(journey); !slices.Equal(got, []string{"h1", "h2", "h4"}) {
				t.Errorf("deliveries = %v, want ordinary flood", got)
			}
		})
	}

	fab = newMcastFabric(t, &flood, false)
	journey = injectMcastFrame(t, fab, t0, "h3", fabricGroupData(t, group))
	if len(journey.Deliveries) != 0 || journey.Entries[len(journey.Entries)-1].Reason != mcast.ReasonUnregistered {
		t.Errorf("unregistered without router = deliveries %+v, last entry %+v", journey.Deliveries, journey.Entries[len(journey.Entries)-1])
	}
}

func TestFabricMulticastAgingAndFastLeave(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	group := netip.MustParseAddr("239.1.1.1")
	query := fabricIGMPFrame(t, "h4", netip.MustParseAddr("10.0.0.254"), netip.MustParseAddr("224.0.0.1"),
		netaddr.MAC{0x01, 0x00, 0x5e, 0, 0, 1}, igmp.Message{Type: igmp.Query, Version: igmp.V2})
	report := fabricIGMPFrame(t, "h1", netip.MustParseAddr("10.0.0.1"), group,
		netaddr.MAC{0x01, 0x00, 0x5e, 1, 1, 1}, igmp.Message{Type: igmp.ReportV2, Group: group})
	leave := fabricIGMPFrame(t, "h1", netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("224.0.0.2"),
		netaddr.MAC{0x01, 0x00, 0x5e, 0, 0, 2}, igmp.Message{Type: igmp.Leave, Group: group})

	fab := newMcastFabric(t, nil, false)
	injectMcastFrame(t, fab, t0, "h4", query)
	injectMcastFrame(t, fab, t0.Add(time.Second), "h1", report)
	injectMcastFrame(t, fab, t0.Add(2*time.Second), "h1", leave)
	injectMcastFrame(t, fab, t0.Add(3*time.Second), "h4", query)
	journey := injectMcastFrame(t, fab, t0.Add(260*time.Second), "h3", fabricGroupData(t, group))
	if got := deliveryHosts(journey); !slices.Equal(got, []string{"h1", "h4"}) {
		t.Errorf("deliveries before expiry = %v, want [h1 h4]", got)
	}
	journey = injectMcastFrame(t, fab, t0.Add(262*time.Second), "h3", fabricGroupData(t, group))
	if got := deliveryHosts(journey); !slices.Equal(got, []string{"h1", "h2", "h4"}) {
		t.Errorf("deliveries after expiry = %v, want flood", got)
	}

	fab = newMcastFabric(t, nil, true)
	injectMcastFrame(t, fab, t0, "h4", query)
	injectMcastFrame(t, fab, t0.Add(time.Second), "h1", report)
	injectMcastFrame(t, fab, t0.Add(2*time.Second), "h1", leave)
	journey = injectMcastFrame(t, fab, t0.Add(3*time.Second), "h3", fabricGroupData(t, group))
	if got := deliveryHosts(journey); !slices.Equal(got, []string{"h1", "h2", "h4"}) {
		t.Errorf("deliveries after fast leave = %v, want flood", got)
	}
}

func TestFabricMLDGroupForwarding(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fab := newMcastFabric(t, nil, false)
	group := netip.MustParseAddr("ff05::1")
	query := fabricMLDFrame(t, "h4", netip.MustParseAddr("fe80::4"), netip.MustParseAddr("ff02::1"),
		mld.Message{Type: mld.Query, Version: mld.V1})
	injectMcastFrame(t, fab, t0, "h4", query)
	report := fabricMLDFrame(t, "h1", netip.MustParseAddr("fe80::1"), group,
		mld.Message{Type: mld.ReportV1, Group: group})
	injectMcastFrame(t, fab, t0.Add(time.Second), "h1", report)
	journey := injectMcastFrame(t, fab, t0.Add(2*time.Second), "h3", fabricIPv6GroupData(t, "h3", group))
	if got := deliveryHosts(journey); !slices.Equal(got, []string{"h1", "h4"}) {
		t.Errorf("MLD group deliveries = %v, want [h1 h4] with h2 absent", got)
	}
	if groups := fab.Snapshot().Devices["sw1"].Groups[10]; len(groups) != 1 || groups[0].Group != group {
		t.Errorf("snapshot Groups[10] = %+v, want ff05::1", groups)
	}
}
