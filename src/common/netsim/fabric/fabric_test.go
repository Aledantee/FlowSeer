package fabric_test

import (
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
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
			// A probe leaves only a port whose link is up, so sw1's
			// protected port is cabled to a switch that runs nothing.
			"sw2": {
				Ports:  tbl,
				Bridge: &bridge.Config{},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	sawWake := false

	for steps := 0; steps < 10 && !sawWake; steps++ {
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

	sawProbe := false
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

// TestFabricRoundTripsAReflector proves that New followed by Spec and Config
// still carries a configured reflector. NewConstructionSpec and Fabric.Spec
// assign Hosts field by field into a fresh map; a matching assignment
// dropped for Reflectors would build a fabric with no such node and no
// error, surfacing only several calls later as an endpoint-not-found error
// that names the cable rather than the missing map.
func TestFabricRoundTripsAReflector(t *testing.T) {
	cfg := reflectorBaseConfig(t)

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	spec := fab.Spec()
	refl, ok := spec.Reflectors["r1"]
	if !ok {
		t.Fatal("Spec() dropped reflector r1")
	}
	if _, ok := refl.Attachments["a"]; !ok {
		t.Fatal("Spec() dropped reflector r1's attachment a")
	}

	if _, ok := fab.Config().Reflectors["r1"]; !ok {
		t.Fatal("Config() dropped reflector r1")
	}
}

// TestFabricUsesTheReflectorsOwnPortFacts proves that link resolution reads a
// reflector's declared port facts rather than falling through the switch
// lookup and treating the reflector as an absent switch, which would leave
// its end of the link with no reported capability at all: forced against an
// unstated capability resolves Unknown, never Up.
func TestFabricUsesTheReflectorsOwnPortFacts(t *testing.T) {
	forced100 := phy.Ethernet{
		SupportedSpeedsBPS: []uint64{100_000_000},
		Setting:            &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full, AutoNegotiation: false},
	}

	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: mustFabricPortTable(t, port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}),
				Phy:   &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": forced100}},
			},
		},
		Reflectors: map[string]fabric.Reflector{
			"r1": {
				Ports: map[string]phy.Ethernet{"p1": forced100},
				Attachments: map[string]fabric.Attachment{
					"a": {Port: "p1", Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.0.9/24")}},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "r1", Port: "p1"}, Medium: fabric.TwistedPair},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("fabric.New: %v", err)
	}

	links := fab.Links()
	if len(links) != 1 {
		t.Fatalf("Links() = %d entries, want 1", len(links))
	}
	if links[0].A.Oper != port.Up || links[0].B.Oper != port.Up {
		t.Fatalf("link = %+v, want both ends Up", links[0])
	}
	if links[0].A.Speed.SpeedBPS != 100_000_000 {
		t.Errorf("negotiated speed = %d, want 100_000_000", links[0].A.Speed.SpeedBPS)
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
