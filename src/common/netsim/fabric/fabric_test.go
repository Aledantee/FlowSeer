package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

// TestFabricSchedulesLoopProtectWakeWithNoSTPOrLAG proves that a fabric
// admits a switch configured with loop protection alone into its layer
// clock: without spanning tree or link aggregation, startLayers used to skip
// such a switch entirely, so it never reported its links, never scheduled a
// wake, and never emitted a probe. With no traffic injected, the fabric must
// still schedule the switch's wake on its own and, once that wake fires,
// emit a loop-protection probe frame.
func TestFabricSchedulesLoopProtectWakeWithNoSTPOrLAG(t *testing.T) {
	tbl := mustFabricPortTable(t,
		port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
		port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up},
	)

	cfg := fabric.Config{
		Start: time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  tbl,
				Bridge: &bridge.Config{},
				LoopProtect: &loopprotect.Config{
					Interval: 5 * time.Second,
					Ports: map[string]loopprotect.Port{
						"1/1/1": {Action: loopprotect.Block},
					},
				},
			},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	sawWake := false
	sawProbe := false

	for steps := 0; steps < 10 && !(sawWake && sawProbe); steps++ {
		entry, ok := fab.Step()
		if !ok {
			break
		}
		if entry.Kind == fabric.EntryWake && entry.Device == "sw1" {
			sawWake = true
		}
	}

	if !sawWake {
		t.Fatalf("fabric.Step() never produced a Wake entry for sw1 with no spanning tree and no LAG configured")
	}

	for _, journey := range fab.Report() {
		if !journey.Protocol || journey.Injection.Origin.Node != "sw1" {
			continue
		}
		if _, err := loopprotect.Decode(journey.Injection.Frame); err == nil {
			sawProbe = true
		}
	}

	if !sawProbe {
		t.Fatalf("fabric never emitted a loop-protection probe from sw1 with no traffic injected: %+v", fab.Report())
	}
}

func mustFabricPortTable(t *testing.T, ports ...port.Port) port.Table {
	t.Helper()

	b := port.NewBuilder()
	for _, p := range ports {
		b.Add(p)
	}
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
}
