package netsim_test

import (
	"net/netip"
	"reflect"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func TestFactConstructorsSnapshotCallerSlices(t *testing.T) {
	t.Parallel()

	bridgeVLANs := []vlan.ID{10}
	bridgeStrings := []string{"one"}
	routingPrefixes := []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}
	phySpeeds := []uint64{1_000_000_000}
	mcastPorts := []string{"1/1/1"}
	trafficPorts := []string{"1/1/2"}
	trafficVLANs := []vlan.ID{20}
	fabricPrefixes := []string{"198.51.100.1/24"}

	tests := []struct {
		name   string
		fact   trace.Fact
		mutate func()
	}{
		{name: "bridge VLANs", fact: bridge.VLANsFact(bridgeVLANs), mutate: func() { bridgeVLANs[0] = 11 }},
		{name: "bridge strings", fact: bridge.StringsFact(bridgeStrings), mutate: func() { bridgeStrings[0] = "two" }},
		{name: "routing prefixes", fact: routing.PrefixesFact(routingPrefixes), mutate: func() {
			routingPrefixes[0] = netip.MustParsePrefix("203.0.113.1/24")
		}},
		{name: "physical speeds", fact: phy.SpeedsFact(phySpeeds), mutate: func() { phySpeeds[0] = 10_000_000_000 }},
		{name: "multicast router ports", fact: mcast.RouterPortsFact(mcastPorts), mutate: func() { mcastPorts[0] = "1/1/3" }},
		{name: "traffic ports", fact: traffic.PortsFact(trafficPorts), mutate: func() { trafficPorts[0] = "1/1/4" }},
		{name: "traffic VLANs", fact: traffic.VLANsFact(trafficVLANs), mutate: func() { trafficVLANs[0] = 21 }},
		{name: "fabric prefixes", fact: fabric.PrefixesFact(fabricPrefixes), mutate: func() { fabricPrefixes[0] = "203.0.113.1/24" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.fact.Canonical()
			test.mutate()
			if got := test.fact.Canonical(); got != before {
				t.Errorf("fact changed after caller mutation: got %q, want %q", got, before)
			}
		})
	}
}

func TestDiffReturnsImmutableFactImplementations(t *testing.T) {
	t.Parallel()

	limit := uint32(15_400)
	flood := true
	tests := []struct {
		name      string
		fact      trace.Fact
		forbidden any
	}{
		{
			name: "virtual switch config",
			fact: firstAddedFact(t, fabric.Diff(fabric.Config{}, fabric.Config{Switches: map[string]vswitch.Config{
				"sw": {Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{"p": {SupportedSpeedsBPS: []uint64{1}}}}},
			}})),
			forbidden: vswitch.Config{},
		},
		{
			name: "Ethernet config",
			fact: firstRemovedFact(t, phy.Diff(phy.Config{Ethernet: map[string]phy.Ethernet{
				"p": {SupportedSpeedsBPS: []uint64{1}},
			}}, phy.Config{})),
			forbidden: phy.Ethernet{},
		},
		{
			name: "PSE port config",
			fact: firstRemovedFact(t, phy.Diff(phy.Config{PoE: &phy.PoE{Ports: map[string]phy.PsePort{
				"p": {Limit: &limit},
			}}}, phy.Config{})),
			forbidden: phy.PsePort{},
		},
		{
			name: "VLAN snooping config",
			fact: firstRemovedFact(t, mcast.Diff(mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
				10: {FloodUnregistered: &flood, RouterPorts: []string{"p"}},
			}}, mcast.Config{})),
			forbidden: mcast.VLANSnooping{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, forbidden := reflect.TypeOf(test.fact), reflect.TypeOf(test.forbidden); got == forbidden {
				t.Errorf("diff returned mutable fact implementation %v", got)
			}
		})
	}
}

func TestCompositeFactCanonicalEncodingsAreInjective(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    trace.Fact
		b    trace.Fact
	}{
		{
			name: "bridge string lists",
			a:    bridge.StringsFact([]string{"a,b", "c"}),
			b:    bridge.StringsFact([]string{"a", "b,c"}),
		},
		{
			name: "multicast port lists",
			a:    mcast.RouterPortsFact([]string{"a,b", "c"}),
			b:    mcast.RouterPortsFact([]string{"a", "b,c"}),
		},
		{
			name: "traffic port lists",
			a:    traffic.PortsFact([]string{"a,b", "c"}),
			b:    traffic.PortsFact([]string{"a", "b,c"}),
		},
		{
			name: "fabric prefix strings",
			a:    fabric.PrefixesFact([]string{"a,b", "c"}),
			b:    fabric.PrefixesFact([]string{"a", "b,c"}),
		},
		{
			name: "fabric endpoints",
			a:    fabric.Endpoint{Node: "node:port"},
			b:    fabric.Endpoint{Node: "node", Port: "port"},
		},
		{
			name: "port records",
			a:    port.Port{Name: "a,kind=b", Kind: "c"},
			b:    port.Port{Name: "a", Kind: "b,kind=c"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if trace.EqualFact(test.a, test.b) {
				t.Errorf("different facts share canonical encoding %q", test.a.Canonical())
			}
		})
	}

	cableA := fabric.Cable{
		A: fabric.Endpoint{Node: "left:right"},
		B: fabric.Endpoint{Node: "far"},
	}
	cableB := fabric.Cable{
		A: fabric.Endpoint{Node: "left", Port: "right"},
		B: fabric.Endpoint{Node: "far"},
	}
	keyA := firstAddedFactChange(t, fabric.Diff(fabric.Config{}, fabric.Config{Cables: []fabric.Cable{cableA}})).Subject.Key
	keyB := firstAddedFactChange(t, fabric.Diff(fabric.Config{}, fabric.Config{Cables: []fabric.Cable{cableB}})).Subject.Key
	if keyA == keyB {
		t.Errorf("different cable identities share subject key %q", keyA)
	}
}

func firstAddedFact(t *testing.T, changes []trace.Change) trace.Fact {
	t.Helper()

	return firstAddedFactChange(t, changes).To
}

func firstAddedFactChange(t *testing.T, changes []trace.Change) trace.Change {
	t.Helper()

	for _, change := range changes {
		if change.From == nil && change.To != nil {
			return change
		}
	}
	t.Fatal("no added fact change")

	return trace.Change{}
}

func firstRemovedFact(t *testing.T, changes []trace.Change) trace.Fact {
	t.Helper()

	for _, change := range changes {
		if change.From != nil && change.To == nil {
			return change.From
		}
	}
	t.Fatal("no removed fact change")

	return nil
}
