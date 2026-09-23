package fabric_test

import (
	"fmt"
	"testing"

	"go.aledante.io/FlowSeer/src/common/netsim/internal/netsimtest"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

// forkAllocationBaseline is the measured heap allocation count of Fabric.Fork
// at representative scale (8 nodes of 32 ports, 16 VLANs, 64 routes and 64 neighbors
// per node, 64 hosts, 2048 learned forwarding entries, and 4096 queued arrivals).
// Measured on 2026-09-23.
const forkAllocationBaseline = 16815

// forkAllocationFixedFloor is the measured heap allocation count of Fabric.Fork
// at representative scale with zero queued arrivals. Measured on 2026-09-23.
const forkAllocationFixedFloor = 4515

func TestRepresentativeFabricEnvelope(t *testing.T) {
	fab := netsimtest.RepresentativeFabric()
	cfg := fab.Config()

	if len(cfg.Switches) != netsimtest.RepresentativeScaleNodeCount {
		t.Fatalf("switch count = %d, want %d", len(cfg.Switches), netsimtest.RepresentativeScaleNodeCount)
	}
	if len(cfg.Hosts) != netsimtest.RepresentativeScaleHostCount {
		t.Fatalf("host count = %d, want %d", len(cfg.Hosts), netsimtest.RepresentativeScaleHostCount)
	}

	totalPhysicalPorts := 0
	totalRoutes := 0
	totalNeighbors := 0

	for s := 1; s <= netsimtest.RepresentativeScaleNodeCount; s++ {
		swName := fmt.Sprintf("sw%d", s)
		sw := fab.Switch(swName)
		if sw == nil {
			t.Fatalf("switch %s missing", swName)
		}

		physCount := 0
		for _, p := range sw.Ports().Ports() {
			if p.Kind == port.Physical {
				physCount++
			}
		}
		if physCount != netsimtest.RepresentativeScalePortsPerNode {
			t.Errorf("switch %s physical ports = %d, want %d", swName, physCount, netsimtest.RepresentativeScalePortsPerNode)
		}
		totalPhysicalPorts += physCount

		vlanCount := len(sw.Config().Bridge.VLAN.Table)
		if vlanCount != netsimtest.RepresentativeScaleVLANCount {
			t.Errorf("switch %s VLAN count = %d, want %d", swName, vlanCount, netsimtest.RepresentativeScaleVLANCount)
		}

		lagCfg := sw.Config().LAG
		if lagCfg == nil || len(lagCfg.LAGs) != 1 {
			t.Fatalf("switch %s expected 1 LAG", swName)
		}
		for _, l := range lagCfg.LAGs {
			if len(l.Members) != netsimtest.RepresentativeScaleLAGMembers {
				t.Errorf("switch %s LAG members = %d, want %d", swName, len(l.Members), netsimtest.RepresentativeScaleLAGMembers)
			}
		}

		vrf := sw.Config().Routing.VRFs[routing.DefaultVRF]
		routes := len(vrf.Routes)
		if routes != netsimtest.RepresentativeScaleRoutesPerVRF {
			t.Errorf("switch %s routes = %d, want %d", swName, routes, netsimtest.RepresentativeScaleRoutesPerVRF)
		}
		totalRoutes += routes

		neighbors := len(vrf.Neighbors)
		if neighbors != netsimtest.RepresentativeScaleNeighborsPerVRF {
			t.Errorf("switch %s neighbors = %d, want %d", swName, neighbors, netsimtest.RepresentativeScaleNeighborsPerVRF)
		}
		totalNeighbors += neighbors
	}

	wantPhysicalPorts := netsimtest.RepresentativeScaleNodeCount * netsimtest.RepresentativeScalePortsPerNode
	if totalPhysicalPorts != wantPhysicalPorts {
		t.Errorf("total physical ports = %d, want %d", totalPhysicalPorts, wantPhysicalPorts)
	}

	wantRoutes := netsimtest.RepresentativeScaleNodeCount * netsimtest.RepresentativeScaleRoutesPerVRF
	if totalRoutes != wantRoutes {
		t.Errorf("total routes = %d, want %d", totalRoutes, wantRoutes)
	}

	wantNeighbors := netsimtest.RepresentativeScaleNodeCount * netsimtest.RepresentativeScaleNeighborsPerVRF
	if totalNeighbors != wantNeighbors {
		t.Errorf("total neighbors = %d, want %d", totalNeighbors, wantNeighbors)
	}

	snap := fab.Snapshot()

	totalEntries := 0
	for _, dev := range snap.Devices {
		totalEntries += len(dev.Entries)
	}
	if totalEntries != netsimtest.RepresentativeScaleLearnedEntries {
		t.Errorf("total learned entries = %d, want %d", totalEntries, netsimtest.RepresentativeScaleLearnedEntries)
	}

	if len(snap.Queue) != netsimtest.RepresentativeScaleQueueDepth {
		t.Errorf("queued arrivals = %d, want %d", len(snap.Queue), netsimtest.RepresentativeScaleQueueDepth)
	}
}

func TestRepresentativeFabricForkAllocs(t *testing.T) {
	fab := netsimtest.RepresentativeFabric()

	allocs := testing.AllocsPerRun(5, func() {
		_ = fab.Fork()
	})

	if allocs > forkAllocationBaseline {
		t.Fatalf("Fabric.Fork allocated %.0f objects, want <= %d (baseline measured 2026-09-18)", allocs, forkAllocationBaseline)
	}
}

func TestRepresentativeFabricForkAllocsQueueScaling(t *testing.T) {
	fab1x := netsimtest.RepresentativeFabric()
	allocs1x := testing.AllocsPerRun(5, func() {
		_ = fab1x.Fork()
	})

	fab4x := netsimtest.RepresentativeFabricAtQueueDepth(4 * netsimtest.RepresentativeScaleQueueDepth)
	allocs4x := testing.AllocsPerRun(5, func() {
		_ = fab4x.Fork()
	})

	// Incremental allocations scale linearly with queue depth (3x queue delta).
	// Allow slight headroom for map bucket growth over the linear multiple.
	incremental1x := allocs1x - float64(forkAllocationFixedFloor)
	const linearMultiple = 3.05
	maxAllowed := allocs1x + linearMultiple*incremental1x
	if allocs4x > maxAllowed {
		t.Fatalf("Fabric.Fork at 4x queue depth allocated %.0f objects, want <= %.0f (incremental growth %.0f > %.2fx 1x incremental %.0f)",
			allocs4x, maxAllowed, allocs4x-allocs1x, linearMultiple, incremental1x)
	}
}
