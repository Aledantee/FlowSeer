package vswitch_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func TestRepresentativeProductionTraceFactsAreTyped(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
	res := sw.Forward(fixedTime, "in", ethernet.Frame{Src: macH1, Dst: macH2, Payload: []byte("payload")})
	if len(res.Steps) == 0 {
		t.Fatal("representative bridge trace has no steps")
	}
	seen := make(map[string]bool)
	for i, step := range res.Steps {
		facts := append(append([]trace.Fact(nil), step.Inputs...), step.Outputs...)
		if len(facts) == 0 {
			t.Errorf("step %d (%s) has no semantic facts", i, step.RuleID)
		}
		for _, fact := range facts {
			if fact == nil || fact.TypeID() == "" || fact.Canonical() == "" {
				t.Errorf("step %d (%s) has invalid fact %#v", i, step.RuleID, fact)
				continue
			}
			seen[fact.TypeID()] = true
		}
	}
	for _, typeID := range []string{"bridge.frame", "bridge.vlan_decision", "bridge.fdb_decision", "bridge.egress_decision"} {
		if !seen[typeID] {
			t.Errorf("representative bridge trace has no %s fact: %+v", typeID, res.Steps)
		}
	}
}

func TestCapabilityFactsAreImmutableAndDecisionSensitive(t *testing.T) {
	ports := []string{"one", "two"}
	membership := mcast.MembershipDecisionFact(10, ipH2, ports, true, true)
	before := membership.Canonical()
	ports[0] = "changed"
	if got := membership.Canonical(); got != before {
		t.Errorf("membership fact changed after source mutation: got %q, want %q", got, before)
	}

	facts := []trace.Fact{
		port.ForwardingFact("one", port.Port{Name: "one", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}, true, ""),
		routing.EgressFact("outside", "one", "", ""),
		lag.LACPDecodeFact(ethernet.Frame{EtherType: ethernet.EtherTypeSlowProtocols}, true, ""),
		stp.BPDUDecodeFact(ethernet.Frame{Dst: netaddr.MAC{0x01, 0x80, 0xc2}}, true, ""),
		membership,
		traffic.MirrorDecisionFact("mirror", "one", 64, ""),
		traffic.PolicerDecisionFact(1_000_000, 10_000, 64, true),
	}
	for _, fact := range facts {
		if fact.TypeID() == "" || fact.Canonical() == "" {
			t.Errorf("fact is not self-describing: %#v", fact)
		}
	}

	admitted := trace.Step{Outputs: []trace.Fact{traffic.PolicerDecisionFact(1_000_000, 10_000, 64, true)}}
	refused := trace.Step{Outputs: []trace.Fact{traffic.PolicerDecisionFact(1_000_000, 10_000, 64, false)}}
	if admitted.Equal(refused) {
		t.Fatal("different policer decisions compare equal")
	}

	stpBefore := stp.PortInfo{State: stp.StateDiscarding}
	stpAfter := stpBefore
	stpAfter.State = stp.StateForwarding
	if (trace.Step{Outputs: []trace.Fact{stp.PortTransitionFact("one", "bpdu", stpBefore, stpBefore)}}).
		Equal(trace.Step{Outputs: []trace.Fact{stp.PortTransitionFact("one", "bpdu", stpBefore, stpAfter)}}) {
		t.Fatal("different spanning-tree transitions compare equal")
	}

	lagBefore := lag.MemberInfo{LACPDUsRx: 1}
	lagAfter := lagBefore
	lagAfter.LACPDUsRx++
	if (trace.Step{Outputs: []trace.Fact{lag.MemberTransitionFact("one", "lacpdu", lagBefore, lagBefore)}}).
		Equal(trace.Step{Outputs: []trace.Fact{lag.MemberTransitionFact("one", "lacpdu", lagBefore, lagAfter)}}) {
		t.Fatal("different LAG member transitions compare equal")
	}
}

func TestConfigFactAgreesWithSemanticConfigEquality(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "p1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	tests := []struct {
		name string
		a    vswitch.Config
		b    vswitch.Config
	}{
		{
			name: "nil and empty traffic collections",
			a:    vswitch.Config{Ports: ports, Traffic: &traffic.Config{}},
			b: vswitch.Config{Ports: ports, Traffic: &traffic.Config{
				Mirrors:  []traffic.Mirror{},
				Policers: map[string]traffic.Policer{},
				Queues:   map[string]traffic.PortQueues{},
			}},
		},
		{
			name: "nil and empty bridge collections",
			a:    vswitch.Config{Ports: ports, Bridge: &bridge.Config{}},
			b: vswitch.Config{Ports: ports, Bridge: &bridge.Config{
				FloodVLANs:     []vlan.ID{},
				ProtectedPorts: []string{},
			}},
		},
		{
			name: "defaulted bridge aging time",
			a:    vswitch.Config{Ports: ports, Bridge: &bridge.Config{}},
			b:    vswitch.Config{Ports: ports, Bridge: &bridge.Config{AgingTime: bridge.DefaultAgingTime}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !test.a.Equal(test.b) {
				t.Fatal("test configurations are not semantically equal")
			}
			if !trace.EqualFact(vswitch.ConfigFact(test.a), vswitch.ConfigFact(test.b)) {
				t.Errorf("equal configs have different facts:\n a: %s\n b: %s", vswitch.ConfigFact(test.a).Canonical(), vswitch.ConfigFact(test.b).Canonical())
			}
		})
	}
}

func traceHasFactType(steps []trace.Step, typeID string) bool {
	for _, step := range steps {
		for _, facts := range [][]trace.Fact{step.Inputs, step.Outputs} {
			for _, fact := range facts {
				if fact != nil && fact.TypeID() == typeID {
					return true
				}
			}
		}
	}

	return false
}
