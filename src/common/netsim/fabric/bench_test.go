package fabric_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

const (
	aggregateBatchSize    = 10_000
	aggregateBatches      = 100
	aggregateJourneyCount = 10
)

// benchmarkChainFabric builds the two-switch chain the aggregate benchmarks
// run their load over: h1 on sw1, a fiber trunk, and h2 on sw2.
func benchmarkChainFabric(b *testing.B) (*fabric.Fabric, netaddr.MAC, netaddr.MAC) {
	b.Helper()

	ports := func() port.Table {
		table, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		if err != nil {
			b.Fatalf("build ports: %v", err)
		}

		return table
	}

	vid10 := vlan.ID(10)
	bridgeCfg := func() *bridge.Config {
		return &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
				"1/1/24": {Tagged: []vlan.ID{10}},
			},
		}}
	}

	src := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	dst := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	fab, err := fabric.New(statedPhysical(fabric.Config{
		Start: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: ports(), Bridge: bridgeCfg()},
			"sw2": {Ports: ports(), Bridge: bridgeCfg()},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: src},
			"h2": {Address: dst},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/24"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/24"}, LengthMeters: 300, Medium: fabric.MultimodeFiber},
			{A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
		},
	}))
	if err != nil {
		b.Fatalf("New: %v", err)
	}

	return fab, src, dst
}

// runAggregateBatches injects frames in batches of aggregateBatchSize, each
// batch at the run's current clock and followed by a draining run, and fails
// the benchmark if a batch leaves the arrival queue non-empty. It returns the
// total frames injected.
func runAggregateBatches(b *testing.B, fab *fabric.Fabric, src, dst netaddr.MAC, batches int, retention fabric.Retention) int {
	b.Helper()
	frame := ethernet.Frame{Src: src, Dst: dst, Payload: make([]byte, 50)}
	injected := 0
	for range batches {
		clock := fab.Snapshot().Clock
		for range aggregateBatchSize {
			if _, err := fab.Inject(fabric.Injection{
				At:        clock,
				Origin:    fabric.Endpoint{Node: "h1"},
				Frame:     frame,
				Retention: retention,
				Flow:      1,
			}); err != nil {
				b.Fatalf("Inject: %v", err)
			}
			injected++
		}
		if res := fab.Run(1_000_000); res.Stop != fabric.StopQueueDrained {
			b.Fatalf("Run stop = %s after %d frames, want %s", res.Stop, injected, fabric.StopQueueDrained)
		}
		if queued := len(fab.Snapshot().Queue); queued != 0 {
			b.Fatalf("arrival queue holds %d after draining %d frames", queued, injected)
		}
	}

	return injected
}

// BenchmarkAggregateMillion injects one million 64-octet frames in aggregate
// retention, so each settled frame folds into its flow and leaves no journey
// behind.
func BenchmarkAggregateMillion(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fab, src, dst := benchmarkChainFabric(b)
		injected := runAggregateBatches(b, fab, src, dst, aggregateBatches, fabric.RetainAggregate)
		if injected != aggregateBatches*aggregateBatchSize {
			b.Fatalf("injected %d frames, want %d", injected, aggregateBatches*aggregateBatchSize)
		}
	}
}

// BenchmarkAggregateHundredThousandJourneys runs the same load at a hundred
// thousand frames with journey retention, so a missed budget can be laid at
// the per-frame record rather than at retention.
func BenchmarkAggregateHundredThousandJourneys(b *testing.B) {
	for i := 0; i < b.N; i++ {
		fab, src, dst := benchmarkChainFabric(b)
		runAggregateBatches(b, fab, src, dst, aggregateJourneyCount, fabric.RetainJourney)
	}
}
