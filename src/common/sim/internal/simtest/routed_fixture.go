package simtest

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
)

// RoutedFixture composes routed test topologies from the test's device and
// adjacency facts. It takes ownership of the maps and cables and supplies no
// operational assumptions, so unresolved-link tests retain their stated inputs.
func RoutedFixture(start time.Time, switches map[string]vswitch.Config, hosts map[string]hostmodel.Config, cables []fabric.Cable) assembly.Config {
	return assembly.Config{Start: start, Switches: switches, Hosts: hosts, Cables: cables}
}
