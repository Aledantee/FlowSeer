package loopprotect_test

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// Example is the package README's example, compiled and run so the two cannot
// drift apart.
func Example() {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Build()
	if err != nil {
		panic(err)
	}

	mac, err := netaddr.Parse("00:11:22:33:44:01")
	if err != nil {
		panic(err)
	}

	layer, err := loopprotect.New(loopprotect.Config{
		Interval: 5 * time.Second,
		Ports: map[string]loopprotect.Port{
			"1/1/1": {Action: loopprotect.Block},
			"1/1/2": {Action: loopprotect.Block},
		},
	}, ports, mac)
	if err != nil {
		panic(err)
	}

	t0 := time.Unix(1700000000, 0)
	layer.LinkChange(t0, "1/1/1", true)
	layer.LinkChange(t0, "1/1/2", true)

	// The first probes go out one interval after the links came up.
	fx := layer.Wake(t0.Add(5 * time.Second))

	// The unmanaged hub loops 1/1/1's probe back onto the switch; the
	// switch decodes it and finds it names this switch as sender.
	probe, err := loopprotect.Decode(fx.Emissions[0].Frame)
	if err != nil {
		panic(err)
	}
	layer.Receive(t0, probe.VID, probe)

	fmt.Printf("1/1/1: %s\n", layer.PortInfo("1/1/1").Action)
	fmt.Printf("1/1/2: %s\n", layer.PortInfo("1/1/2").Action)
	// Output:
	// 1/1/1: Block
	// 1/1/2:
}
