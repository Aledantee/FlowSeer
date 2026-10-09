package fabric_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
)

func TestFilterChangeComparesDifferent(t *testing.T) {
	cfg, src, _ := makeTwoSwitchConfigs(t)
	routerMAC := netaddr.MAC{2, 0, 0, 0, 0, 1}
	sw := cfg.Switches["sw1"]
	sw.MAC = routerMAC
	sw.Routing = &routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
		Interfaces: map[string]routing.Interface{"vlan10": {VLAN: 10, MAC: routerMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}}},
	}}}
	sw.Filter = &filter.Config{Sets: map[string]filter.RuleSet{"ingress": {Default: filter.Accept}}, Bindings: []filter.Binding{{Interface: "vlan10", Direction: filter.In, Set: "ingress"}}}
	cfg.Switches["sw1"] = sw
	a, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatal(err)
	}
	candidate := cfg.Clone()
	candidate.Switches["sw1"].Filter.Sets["ingress"] = filter.RuleSet{Default: filter.Drop}
	b, err := assembly.New(statedPhysical(candidate))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := (ip.Header{Src: netip.MustParseAddr("10.0.10.2"), Dst: netip.MustParseAddr("10.0.10.1"), HopLimit: 64, Protocol: 17, V4: &ip.V4{}}).Encode([]byte("filter comparison"))
	if err != nil {
		t.Fatal(err)
	}
	scenario := scenarioFromInjections([]fabric.Injection{{At: time.Unix(1000, 0), Origin: fabric.Endpoint{Node: "h1"}, Frame: ethernet.Frame{Src: src, Dst: routerMAC, EtherType: ethernet.EtherTypeIPv4, Payload: payload}}})
	got := fabric.Compare(a, b, scenario, 20)
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	if got.Disposition != analysis.Different {
		t.Fatalf("disposition = %v, want Different", got.Disposition)
	}
}
