package bridge_test

import (
	"strconv"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

var (
	testTime0 = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

	macA = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	macB = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66}
	macC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77}
	macD = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88}
)

func mustVLAN(id uint16) *vlan.ID {
	v := vlan.ID(id)

	return &v
}

func buildTestPorts(t *testing.T, count int) port.Table {
	t.Helper()
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, count, port.Port{
		Kind:        port.Physical,
		AdminStatus: port.Up,
		OperStatus:  port.Up,
	})
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build test ports: %v", err)
	}

	return tbl
}

func mustNewBridge(t *testing.T, cfg bridge.Config, ports port.Table) *bridge.Layer {
	t.Helper()
	br, err := bridge.New(cfg, layer.Env{Ports: ports})
	if err != nil {
		t.Fatalf("bridge.New: %v", err)
	}
	return br
}

func mustLearn(t *testing.T, br *bridge.Layer, seeds []bridge.Seed) {
	t.Helper()
	if err := br.InstallSeeds(seeds); err != nil {
		t.Fatalf("InstallSeeds: %v", err)
	}
}

type stubSelector struct {
	member string
	ok     bool
}

func (s stubSelector) Select(_ time.Time, _ string, _ bool, _ ethernet.Frame, _ vlan.ID) bridge.Selection {
	return bridge.Selection{Member: s.member, OK: s.ok}
}

type testGate struct {
	learns   map[string]bool
	forwards map[string]bool
}

func (g testGate) Learns(p string, _ vlan.ID) bool {
	if g.learns == nil {
		return true
	}

	return g.learns[p]
}

func (g testGate) Forwards(p string, _ vlan.ID) bool {
	if g.forwards == nil {
		return true
	}

	return g.forwards[p]
}

// gateVLANConfig is a two-port bridge where 1/1/1 admits VLAN 10 untagged and
// filters everything else, which is what lets one frame fail classification and
// another pass it on the same blocked port.
func gateVLANConfig() bridge.Config {
	return bridge.Config{
		VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10", 20: "vlan20"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {
					PVID:             mustVLAN(10),
					Untagged:         []vlan.ID{10},
					IngressFiltering: true,
					Admission:        bridge.All,
				},
				"1/1/2": {
					PVID:     mustVLAN(10),
					Untagged: []vlan.ID{10},
					Tagged:   []vlan.ID{20},
				},
			},
		},
	}
}

// testSemanticGate is a stub [bridge.Gate] that also implements the
// unexported semanticGate contract, so a multi-gate test can tell which
// installed gate a recorded fact came from.
type testSemanticGate struct {
	name     string
	learns   map[string]bool
	forwards map[string]bool
}

func (g testSemanticGate) Learns(p string, _ vlan.ID) bool {
	if g.learns == nil {
		return true
	}

	return g.learns[p]
}

func (g testSemanticGate) Forwards(p string, _ vlan.ID) bool {
	if g.forwards == nil {
		return true
	}

	return g.forwards[p]
}

func (g testSemanticGate) ForwardingFact(port string, _ vlan.ID, learns, forwards bool) trace.Fact {
	return testSemanticFact(g.name + ":" + port + ":" + strconv.FormatBool(learns) + ":" + strconv.FormatBool(forwards))
}

type testSemanticFact string

func (f testSemanticFact) TypeID() string    { return "test.semantic_gate" }
func (f testSemanticFact) Canonical() string { return string(f) }

// gateFactIn returns the first testSemanticFact found among a step's Inputs,
// so a test can assert which installed gate decided a drop.
func gateFactIn(inputs []trace.Fact) (testSemanticFact, bool) {
	for _, input := range inputs {
		if fact, ok := input.(testSemanticFact); ok {
			return fact, true
		}
	}

	return "", false
}

func portBlockedStep(steps []trace.Step) (trace.Step, bool) {
	for _, step := range steps {
		if step.RuleID == trace.RuleID("port-blocked") {
			return step, true
		}
	}

	return trace.Step{}, false
}

type testGroupResolver struct {
	ports   []string
	decided bool
}

func (r testGroupResolver) Resolve(time.Time, vlan.ID, ethernet.Frame) ([]string, bool) {
	return r.ports, r.decided
}
