package bridge_test

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestForwardingDependenciesMatchConsultedPorts(t *testing.T) {
	t.Run("known unicast excludes an unrelated unknown port", func(t *testing.T) {
		ports := dependencyPorts(t,
			port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "unknown", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown},
		)
		br := mustNewBridge(t, bridge.Config{}, ports)
		mustLearn(t, br, []bridge.Seed{{MAC: macB, Port: "out", Static: true}})

		res := br.Forward(testTime0, "in", ethernet.Frame{Src: macA, Dst: macB})

		if got, want := consultedNames(res), []string{"in", "out"}; !slices.Equal(got, want) {
			t.Errorf("consulted ports = %v, want %v", got, want)
		}
	})

	t.Run("ordinary flood retains a non-forwarding unknown candidate", func(t *testing.T) {
		ports := dependencyPorts(t,
			port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "unknown", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown},
		)
		br := mustNewBridge(t, bridge.Config{}, ports)

		res := br.Forward(testTime0, "in", ethernet.Frame{Src: macA, Dst: macB})

		if got, want := consultedNames(res), []string{"in", "out", "unknown"}; !slices.Equal(got, want) {
			t.Errorf("consulted ports = %v, want %v", got, want)
		}
	})

	t.Run("VLAN flood excludes an unknown non-member", func(t *testing.T) {
		vid := vlan.ID(10)
		ports := dependencyPorts(t,
			port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "out", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "unknown", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown},
		)
		br := mustNewBridge(t, bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{vid: "ten"},
			Switchports: map[string]bridge.Switchport{
				"in":  {PVID: &vid, Untagged: []vlan.ID{vid}},
				"out": {PVID: &vid, Untagged: []vlan.ID{vid}},
			},
		}}, ports)

		res := br.Forward(testTime0, "in", ethernet.Frame{Src: macA, Dst: macB})

		if got, want := consultedNames(res), []string{"in", "out"}; !slices.Equal(got, want) {
			t.Errorf("consulted ports = %v, want %v", got, want)
		}
	})

	t.Run("multicast selection retains an unknown selected port", func(t *testing.T) {
		ports := dependencyPorts(t,
			port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "unknown", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown},
		)
		br := mustNewBridge(t, bridge.Config{}, ports)
		br.SetGroupResolver(testGroupResolver{ports: []string{"unknown"}, decided: true})

		res := br.Forward(testTime0, "in", ethernet.Frame{Src: macA, Dst: netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01}})

		if got, want := consultedNames(res), []string{"in", "unknown"}; !slices.Equal(got, want) {
			t.Errorf("consulted ports = %v, want %v", got, want)
		}
	})

	t.Run("LAG selection retains every member snapshot", func(t *testing.T) {
		ports := dependencyPorts(t,
			port.Port{Name: "in", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
			port.Port{Name: "member-a", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"},
			port.Port{Name: "member-b", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown, LagParent: "lag1"},
			port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up},
		)
		br := mustNewBridge(t, bridge.Config{}, ports)
		br.SetSelector(stubSelector{member: "member-a", ok: true})
		mustLearn(t, br, []bridge.Seed{{MAC: macB, Port: "lag1", Static: true}})

		res := br.Forward(testTime0, "in", ethernet.Frame{Src: macA, Dst: macB})

		if got, want := consultedNames(res), []string{"in", "lag1", "member-a", "member-b"}; !slices.Equal(got, want) {
			t.Errorf("consulted ports = %v, want %v", got, want)
		}
	})
}

func dependencyPorts(t *testing.T, ports ...port.Port) port.Table {
	t.Helper()
	builder := port.NewBuilder()
	for _, p := range ports {
		builder.Add(p)
	}
	table, err := builder.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return table
}

func consultedNames(res bridge.Result) []string {
	ports := res.ConsultedPorts()
	names := make([]string, len(ports))
	for i, p := range ports {
		names[i] = p.Name
	}

	return names
}
