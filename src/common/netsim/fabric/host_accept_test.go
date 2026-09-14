package fabric_test

import (
	"net/netip"
	"slices"
	"strconv"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

var (
	acceptSender   = netaddr.MAC{0x02, 0, 0, 0, 0, 0x01}
	acceptReceiver = netaddr.MAC{0x02, 0, 0, 0, 0, 0x02}
)

// arrivalAtReceiver joins h1 and receiver h2 through a hub, injects frame at
// the hub port facing h1, and returns the journey once the frame has reached h2
// unchanged.
func arrivalAtReceiver(t *testing.T, receiver fabric.Host, frame ethernet.Frame) fabric.Journey {
	t.Helper()
	receiver.Address = acceptReceiver
	fab, err := fabric.New(statedPhysical(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, port.NewBuilder().Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))},
		},
		Hosts: map[string]fabric.Host{"h1": {Address: acceptSender}, "h2": receiver},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	frame.Src = acceptSender
	fid, err := fab.Inject(fabric.Injection{At: fixedTime, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Frame: frame})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(10)

	return fab.Report()[fid-1]
}

func ipv4Payload(t *testing.T, dst string) []byte {
	t.Helper()
	return ipPayload(t, ip.Header{Src: netip.MustParseAddr("192.0.2.99"), Dst: netip.MustParseAddr(dst), HopLimit: 64, Protocol: 17, V4: &ip.V4{}})
}

func ipv6Payload(t *testing.T, dst string) []byte {
	t.Helper()
	return ipPayload(t, ip.Header{Src: netip.MustParseAddr("2001:db8::99"), Dst: netip.MustParseAddr(dst), HopLimit: 64, Protocol: 17, V6: &ip.V6{}})
}

func ipPayload(t *testing.T, header ip.Header) []byte {
	t.Helper()
	raw, err := header.Encode([]byte("payload"))
	if err != nil {
		t.Fatalf("encode IP header: %v", err)
	}

	return raw
}

func ipv4Stack() *fabric.HostIP {
	return &fabric.HostIP{Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}}
}

func ipv6Stack() *fabric.HostIP {
	return &fabric.HostIP{Addresses: []netip.Prefix{netip.MustParsePrefix("2001:db8::a/64")}}
}

func TestHostAcceptanceFollowsItsCheckOrder(t *testing.T) {
	vid10 := vlan.ID(10)
	allHostsMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01}
	groupMAC := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x05}
	allNodesMAC := netaddr.MAC{0x33, 0x33, 0x00, 0x00, 0x00, 0x01}
	solicitedMAC := netaddr.MAC{0x33, 0x33, 0xff, 0x00, 0x00, 0x0a}
	broadcast := netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	other := netaddr.MAC{0x02, 0, 0, 0, 0, 0x09}

	const (
		readVLAN = "vlan"
		readMAC  = "mac"
		readIP   = "ip"
	)
	tests := []struct {
		name       string
		host       fabric.Host
		frame      ethernet.Frame
		wantKind   fabric.EntryKind
		wantRule   trace.RuleID
		wantReason trace.Reason
		wantRead   []string
	}{
		{
			name:     "an untagged frame reaches a host without a VLAN",
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.own", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a VID 0 priority-tagged frame reaches a host without a VLAN",
			frame:    ethernet.Frame{Dst: acceptReceiver, Tags: []vlan.Tag{{TPID: 0x8100, PCP: 5}}, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.own", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a host without a VLAN refuses a C-TAG with a VID",
			frame:    ethernet.Frame{Dst: acceptReceiver, Tags: []vlan.Tag{{TPID: 0x8100, VID: 10}}, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.vlan.form", wantReason: fabric.ReasonHostVLANNotAccepted, wantRead: []string{readVLAN},
		},
		{
			name:     "a host with a VLAN accepts a C-TAG with its VID",
			host:     fabric.Host{VLAN: &vid10},
			frame:    ethernet.Frame{Dst: acceptReceiver, Tags: []vlan.Tag{{TPID: 0x8100, VID: 10}}, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.own", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a host with a VLAN refuses an untagged frame",
			host:     fabric.Host{VLAN: &vid10},
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.vlan.form", wantReason: fabric.ReasonHostVLANNotAccepted, wantRead: []string{readVLAN},
		},
		{
			name:     "a host with a VLAN refuses another VID",
			host:     fabric.Host{VLAN: &vid10},
			frame:    ethernet.Frame{Dst: acceptReceiver, Tags: []vlan.Tag{{TPID: 0x8100, VID: 20}}, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.vlan.form", wantReason: fabric.ReasonHostVLANNotAccepted, wantRead: []string{readVLAN},
		},
		{
			name:     "a host with a VLAN refuses an S-TAG with its VID",
			host:     fabric.Host{VLAN: &vid10},
			frame:    ethernet.Frame{Dst: acceptReceiver, Tags: []vlan.Tag{{TPID: 0x88a8, VID: 10}}, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.vlan.form", wantReason: fabric.ReasonHostVLANNotAccepted, wantRead: []string{readVLAN},
		},
		{
			name:     "a unicast frame to another MAC is refused",
			frame:    ethernet.Frame{Dst: other, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.mac.unicast_not_addressed", wantReason: fabric.ReasonHostUnicastNotAddressed, wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a promiscuous host accepts a unicast frame to another MAC",
			host:     fabric.Host{Accept: fabric.HostAccept{Promiscuous: true}},
			frame:    ethernet.Frame{Dst: other, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.promiscuous", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a promiscuous host skips the IP check",
			host:     fabric.Host{IP: ipv4Stack(), Accept: fabric.HostAccept{Promiscuous: true}},
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "198.51.100.1")},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.promiscuous", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "broadcast is accepted",
			frame:    ethernet.Frame{Dst: broadcast, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.broadcast", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a group MAC nothing accepts is refused",
			frame:    ethernet.Frame{Dst: groupMAC, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.mac.multicast_not_accepted", wantReason: fabric.ReasonHostMulticastNotAccepted, wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "all-multicast accepts any group MAC",
			host:     fabric.Host{Accept: fabric.HostAccept{AllMulticast: true}},
			frame:    ethernet.Frame{Dst: groupMAC, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.all_multicast", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a listed multicast MAC is accepted",
			host:     fabric.Host{Accept: fabric.HostAccept{Multicast: []netaddr.MAC{groupMAC}}},
			frame:    ethernet.Frame{Dst: groupMAC, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.listed_multicast", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "an IPv4 stack accepts the MAC of 224.0.0.1",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: allHostsMAC, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.ipv4_all_hosts", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a host without an IPv4 stack refuses the MAC of 224.0.0.1",
			host:     fabric.Host{IP: ipv6Stack()},
			frame:    ethernet.Frame{Dst: allHostsMAC, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.mac.multicast_not_accepted", wantReason: fabric.ReasonHostMulticastNotAccepted, wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "an IPv6 stack accepts the MAC of ff02::1",
			host:     fabric.Host{IP: ipv6Stack()},
			frame:    ethernet.Frame{Dst: allNodesMAC, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.ipv6_all_nodes", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a host without an IPv6 stack refuses the MAC of ff02::1",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: allNodesMAC, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.mac.multicast_not_accepted", wantReason: fabric.ReasonHostMulticastNotAccepted, wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "an IPv6 stack accepts the solicited-node MAC of an own address",
			host:     fabric.Host{IP: ipv6Stack()},
			frame:    ethernet.Frame{Dst: solicitedMAC, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.solicited_node", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "an IPv6 stack refuses the solicited-node MAC of another address",
			host:     fabric.Host{IP: ipv6Stack()},
			frame:    ethernet.Frame{Dst: netaddr.MAC{0x33, 0x33, 0xff, 0x00, 0x00, 0x0b}, EtherType: ethernet.EtherTypeARP},
			wantKind: fabric.EntryRejection, wantRule: "host.mac.multicast_not_accepted", wantReason: fabric.ReasonHostMulticastNotAccepted, wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "a non-IP EtherType to a host with an IP stack is accepted at the MAC check",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeARP, Payload: []byte("not an IP header")},
			wantKind: fabric.EntryDelivery, wantRule: "host.mac.own", wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "an IPv4 packet to an own address is accepted",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "192.0.2.10")},
			wantKind: fabric.EntryDelivery, wantRule: "host.ip.own", wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "an IPv6 packet to an own address is accepted",
			host:     fabric.Host{IP: ipv6Stack()},
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeIPv6, Payload: ipv6Payload(t, "2001:db8::a")},
			wantKind: fabric.EntryDelivery, wantRule: "host.ip.own", wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "an IP packet to another address is refused",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "192.0.2.11")},
			wantKind: fabric.EntryRejection, wantRule: "host.ip.not_addressed", wantReason: fabric.ReasonHostIPNotAddressed, wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "the limited broadcast is accepted",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: broadcast, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "255.255.255.255")},
			wantKind: fabric.EntryDelivery, wantRule: "host.ip.limited_broadcast", wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "the directed broadcast of an own prefix is accepted",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: broadcast, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "192.0.2.255")},
			wantKind: fabric.EntryDelivery, wantRule: "host.ip.directed_broadcast", wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "the directed broadcast of another prefix is refused",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: broadcast, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "198.51.100.255")},
			wantKind: fabric.EntryRejection, wantRule: "host.ip.not_addressed", wantReason: fabric.ReasonHostIPNotAddressed, wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name: "a 31-bit prefix has no directed broadcast",
			host: fabric.Host{IP: &fabric.HostIP{Addresses: []netip.Prefix{netip.MustParsePrefix("192.0.2.10/31")}}},
			frame: ethernet.Frame{
				Dst: broadcast, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "192.0.2.11"),
			},
			wantKind: fabric.EntryRejection, wantRule: "host.ip.not_addressed", wantReason: fabric.ReasonHostIPNotAddressed, wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "an IPv4 packet to 224.0.0.1 is accepted as a group",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: allHostsMAC, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "224.0.0.1")},
			wantKind: fabric.EntryDelivery, wantRule: "host.ip.group", wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "an IPv6 packet to the solicited-node group of an own address is accepted",
			host:     fabric.Host{IP: ipv6Stack()},
			frame:    ethernet.Frame{Dst: solicitedMAC, EtherType: ethernet.EtherTypeIPv6, Payload: ipv6Payload(t, "ff02::1:ff00:a")},
			wantKind: fabric.EntryDelivery, wantRule: "host.ip.group", wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "a group whose MAC the MAC check would refuse is refused inside an accepted frame",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: allHostsMAC, EtherType: ethernet.EtherTypeIPv4, Payload: ipv4Payload(t, "224.0.0.5")},
			wantKind: fabric.EntryRejection, wantRule: "host.ip.not_addressed", wantReason: fabric.ReasonHostIPNotAddressed, wantRead: []string{readVLAN, readMAC, readIP},
		},
		{
			name:     "an undecodable IP header leaves acceptance unknown",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeIPv4, Payload: []byte{0x45, 0x00, 0x00}},
			wantKind: fabric.EntryUnresolved, wantRule: "host.ip.undecodable", wantReason: fabric.ReasonHostIPHeaderUndecodable, wantRead: []string{readVLAN, readMAC},
		},
		{
			name:     "an IP header of the other version than its EtherType is undecodable",
			host:     fabric.Host{IP: ipv4Stack()},
			frame:    ethernet.Frame{Dst: acceptReceiver, EtherType: ethernet.EtherTypeIPv4, Payload: ipv6Payload(t, "2001:db8::a")},
			wantKind: fabric.EntryUnresolved, wantRule: "host.ip.undecodable", wantReason: fabric.ReasonHostIPHeaderUndecodable, wantRead: []string{readVLAN, readMAC},
		},
	}

	factTypes := map[string]string{readVLAN: "fabric.vlan_tags", readMAC: "fabric.mac", readIP: "routing.addr"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			journey := arrivalAtReceiver(t, tc.host, tc.frame)

			n := len(journey.Entries)
			if n < 2 || journey.Entries[n-2].Kind != fabric.EntryArrival {
				t.Fatalf("entries = %+v, want an arrival before the decision", journey.Entries)
			}
			arrival, decision := journey.Entries[n-2], journey.Entries[n-1]
			if arrival.Device != "h2" || arrival.Cable == nil {
				t.Errorf("arrival = %+v, want h2 with its cable", arrival)
			}
			if decision.Kind != tc.wantKind || decision.Reason != tc.wantReason || decision.Device != "h2" {
				t.Errorf("decision = %s %q at %q, want %s %q at h2", decision.Kind, decision.Reason, decision.Device, tc.wantKind, tc.wantReason)
			}
			if decision.Cable == nil || decision.Cable.B != (fabric.Endpoint{Node: "h2"}) && decision.Cable.A != (fabric.Endpoint{Node: "h2"}) {
				t.Errorf("decision cable = %+v, want the h2 cable", decision.Cable)
			}
			step := decision.Step
			if step == nil {
				t.Fatalf("decision %+v carries no step", decision)
			}
			if step.Layer != fabric.HostLayer || step.Op != trace.OpFilter || step.RuleID != tc.wantRule || step.Subject != (trace.Subject{Kind: "host", Key: "h2"}) {
				t.Errorf("step = %s %s %s %+v, want host filter %s on host h2", step.Layer, step.Op, step.RuleID, step.Subject, tc.wantRule)
			}
			var gotTypes, wantTypes []string
			for _, fact := range step.Inputs {
				gotTypes = append(gotTypes, fact.TypeID())
			}
			for _, read := range tc.wantRead {
				wantTypes = append(wantTypes, factTypes[read])
			}
			slices.Sort(gotTypes)
			slices.Sort(wantTypes)
			if !slices.Equal(gotTypes, wantTypes) {
				t.Errorf("step inputs = %v, want %v", gotTypes, wantTypes)
			}

			delivered := tc.wantKind == fabric.EntryDelivery
			if delivered != (len(journey.Deliveries) == 1) || len(journey.Deliveries) > 1 {
				t.Errorf("deliveries = %+v, want delivered %t", journey.Deliveries, delivered)
			}

			undecided := slices.ContainsFunc(journey.Metadata.Issues(), func(issue analysis.Issue) bool {
				return issue.Code == analysis.IssueCode(fabric.ReasonHostIPHeaderUndecodable) &&
					issue.Status == analysis.Incomplete &&
					issue.Scope.Compare(analysis.JourneyScope(strconv.FormatUint(uint64(journey.FrameID), 10))) == 0
			})
			if want := tc.wantKind == fabric.EntryUnresolved; undecided != want {
				t.Errorf("journey issues = %+v, undecodable issue present %t, want %t", journey.Metadata.Issues(), undecided, want)
			}
			if want := tc.wantKind == fabric.EntryUnresolved; (journey.Metadata.Status() == analysis.Incomplete) != want {
				t.Errorf("journey status = %s, want incomplete %t", journey.Metadata.Status(), want)
			}
		})
	}
}

func TestForeignUnicastIsRejectedUnlessPromiscuous(t *testing.T) {
	frame := ethernet.Frame{Dst: netaddr.MAC{0x02, 0, 0, 0, 0, 0x09}, EtherType: ethernet.EtherTypeIPv4, Payload: []byte("hello")}

	journey := arrivalAtReceiver(t, fabric.Host{}, frame)
	if len(journey.Deliveries) != 0 {
		t.Errorf("deliveries = %+v, want none", journey.Deliveries)
	}
	if last := journey.Entries[len(journey.Entries)-1]; last.Kind != fabric.EntryRejection || last.Reason != fabric.ReasonHostUnicastNotAddressed {
		t.Errorf("last entry = %s %q, want Rejection %q", last.Kind, last.Reason, fabric.ReasonHostUnicastNotAddressed)
	}

	promiscuous := arrivalAtReceiver(t, fabric.Host{Accept: fabric.HostAccept{Promiscuous: true}}, frame)
	if len(promiscuous.Deliveries) != 1 || promiscuous.Deliveries[0].Host != "h2" {
		t.Errorf("promiscuous deliveries = %+v, want one to h2", promiscuous.Deliveries)
	}
}

func TestHostAcceptFieldMatrix(t *testing.T) {
	groupA := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x05}
	groupB := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x06}
	base := func() fabric.ConstructionSpec {
		return constructionSpec(twoSwitchBaseConfig(t))
	}
	withAccept := func(accept fabric.HostAccept) fabric.ConstructionSpec {
		spec := base()
		h1 := spec.Hosts["h1"]
		h1.Accept = accept
		spec.Hosts["h1"] = h1
		return spec
	}

	tests := []struct {
		name      string
		accept    fabric.HostAccept
		wantField string
	}{
		{name: "setting promiscuous", accept: fabric.HostAccept{Promiscuous: true}, wantField: "accept.promiscuous"},
		{name: "setting all-multicast", accept: fabric.HostAccept{AllMulticast: true}, wantField: "accept.all_multicast"},
		{name: "listing a multicast MAC", accept: fabric.HostAccept{Multicast: []netaddr.MAC{groupA}}, wantField: "accept.multicast." + groupA.String()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, b := base(), withAccept(tc.accept)

			if a.Equal(b) {
				t.Error("Equal reports the changed specification equal")
			}
			if a.Config().Equal(b.Config()) {
				t.Error("Config.Equal reports the changed configuration equal")
			}
			changes, err := fabric.DiffSpecs(a, b)
			if err != nil {
				t.Fatalf("DiffSpecs: %v", err)
			}
			if len(changes) != 1 || changes[0].Subject != (trace.Subject{Kind: "host", Key: "h1"}) || changes[0].Field != tc.wantField {
				t.Errorf("DiffSpecs = %+v, want one host h1 %s change", changes, tc.wantField)
			}

			fab, err := fabric.NewWithSpec(b)
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}
			if !fab.Spec().Equal(b) {
				t.Error("Spec() does not round-trip the specification")
			}
			if !fab.Config().Equal(b.Config()) {
				t.Error("Config() does not carry the specification's configuration")
			}
			if a.Equal(fab.Spec()) {
				t.Error("Spec() lost the acceptance")
			}
		})
	}

	t.Run("a unicast multicast entry is invalid", func(t *testing.T) {
		_, err := fabric.NewWithSpec(withAccept(fabric.HostAccept{Multicast: []netaddr.MAC{acceptSender, groupA}}))
		if err == nil {
			t.Fatal("NewWithSpec accepted a unicast multicast entry")
		}
		if got := errs.Attributes(err)["field"]; got != "hosts.h1.accept.multicast.0" {
			t.Errorf("field = %v, want the submitted position hosts.h1.accept.multicast.0 (error %v)", got, err)
		}
	})

	t.Run("normalization sorts and deduplicates the multicast list", func(t *testing.T) {
		cfg := withAccept(fabric.HostAccept{Multicast: []netaddr.MAC{groupB, groupA, groupB}}).Config()
		if got := cfg.Normalize().Hosts["h1"].Accept.Multicast; !slices.Equal(got, []netaddr.MAC{groupA, groupB}) {
			t.Errorf("normalized multicast = %v, want [%s %s]", got, groupA, groupB)
		}
		if !cfg.Equal(withAccept(fabric.HostAccept{Multicast: []netaddr.MAC{groupA, groupB}}).Config()) {
			t.Error("the same multicast set in another order is not Equal")
		}
	})

	t.Run("clone", func(t *testing.T) {
		spec := withAccept(fabric.HostAccept{Multicast: []netaddr.MAC{groupA}})
		cfg := spec.Config()
		copies := map[string]fabric.Config{
			"ConstructionSpec.Config": spec.Config(),
			"ConstructionSpec.Clone":  spec.Clone().Config(),
			"Config.Clone":            cfg.Clone(),
			"Config.Normalize":        cfg.Normalize(),
		}
		spec.Hosts["h1"].Accept.Multicast[0] = groupB
		cfg.Hosts["h1"].Accept.Multicast[0] = groupB
		for name, c := range copies {
			if got := c.Hosts["h1"].Accept.Multicast[0]; got != groupA {
				t.Errorf("%s shares the multicast list: %s", name, got)
			}
		}
	})
}
