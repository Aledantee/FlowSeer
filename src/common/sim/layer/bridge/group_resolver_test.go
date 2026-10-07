package bridge_test

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestGroupResolverSelectsReplicationPorts(t *testing.T) {
	ports := buildTestPorts(t, 4)
	br := mustNewBridge(t, bridge.Config{VLAN: &bridge.VLAN{
		Table: map[vlan.ID]string{10: "ten"},
		Switchports: map[string]bridge.Switchport{
			"1/1/1": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/2": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/3": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
			"1/1/4": {PVID: mustVLAN(10), Untagged: []vlan.ID{10}},
		},
	}}, ports)
	br.SetGroupResolver(testGroupResolver{ports: []string{"1/1/2", "1/1/4"}, decided: true}, analysis.ProtocolScope("sw1", "mcast", "0"), "mcast", "group-members")

	res := br.Forward(testTime0, "1/1/1", ethernet.Frame{
		Dst:       netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01},
		Src:       macA,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("group"),
	})

	var got []string
	for _, egress := range res.Egress {
		if egress.Dropped == "" {
			got = append(got, egress.Port)
		}
	}
	if !slices.Equal(got, []string{"1/1/2", "1/1/4"}) {
		t.Errorf("forwarded ports = %v, want [1/1/2 1/1/4]", got)
	}
	if !slices.ContainsFunc(res.Steps, func(step trace.Step) bool {
		return step.Op == trace.OpReplicate && step.RuleID == "group-members"
	}) {
		t.Errorf("steps = %+v, want group members replication", res.Steps)
	}
}

func TestGroupResolverEmptyDecisionUsesUnregisteredReason(t *testing.T) {
	br := mustNewBridge(t, bridge.Config{}, buildTestPorts(t, 2))
	br.SetGroupResolver(testGroupResolver{decided: true}, analysis.ProtocolScope("sw1", "mcast", "0"), "mcast", "group-members")

	res := br.Forward(testTime0, "1/1/1", ethernet.Frame{
		Dst: netaddr.MAC{0x01, 0x00, 0x5e, 0x02, 0x02, 0x02},
		Src: macA,
	})
	if res.Outcome != trace.Dropped || res.Reason != trace.Reason("unregistered") {
		t.Errorf("Forward = %s/%s, want Dropped/unregistered", res.Outcome, res.Reason)
	}
}
