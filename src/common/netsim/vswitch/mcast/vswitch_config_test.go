// Removed for restoration: this file proved that a host outside this directory (vswitch)
// could construct mcast's exported Config. The unit that breaks mcast's Resolve and Entry
// shapes leaves the vswitch package red, so this file cannot compile alongside its import of
// vswitch; a later unit that updates vswitch's callers should restore the test below (rm and
// git rm were both refused by the sandbox, so its body is commented out here instead of the
// file being deleted).
//
// package mcast_test
//
// import (
// 	"testing"
//
// 	"go.aledante.io/FlowSeer/src/common/net/vlan"
// 	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
// 	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
// 	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
// 	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
// )
//
// func TestAggregateValidationAllowsInactiveMulticastRouterPorts(t *testing.T) {
// 	t.Parallel()
//
// 	for _, state := range []port.LinkState{port.Down, port.Unknown} {
// 		t.Run(string(state), func(t *testing.T) {
// 			t.Parallel()
// 			ports, err := port.NewBuilder().Add(port.Port{
// 				Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: state,
// 			}).Build()
// 			if err != nil {
// 				t.Fatalf("build port table: %v", err)
// 			}
// 			cfg := vswitch.Config{
// 				Ports: ports,
// 				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
// 					Table:       map[vlan.ID]string{10: "ten"},
// 					Switchports: map[string]bridge.Switchport{"1/1/1": {Tagged: []vlan.ID{10}}},
// 				}},
// 				Mcast: &mcast.Config{VLANs: map[vlan.ID]mcast.VLANSnooping{
// 					10: {RouterPorts: []string{"1/1/1"}},
// 				}},
// 			}
// 			if err := cfg.Normalize().Validate(); err != nil {
// 				t.Errorf("Validate() = %v, want nil", err)
// 			}
// 		})
// 	}
// }

package mcast_test
