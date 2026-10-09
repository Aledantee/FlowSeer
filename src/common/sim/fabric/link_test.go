package fabric_test

import (
	"testing"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestOmittedPortIsUnknownAndAbsentFromFloodSets(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  mustTable(t, b),
				Bridge: &bridge.Config{},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
		},
	}

	cfg.PhyAssumption = gigabitCopper()
	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	sw := fab.Switch("sw1")
	p3, ok := sw.Ports().Port("1/1/3")
	if !ok {
		t.Fatal("port 1/1/3 not found")
	}
	if p3.OperStatus != port.Unknown {
		t.Errorf("port 1/1/3 OperStatus = %v, want Unknown", p3.OperStatus)
	}

	unknownDst := netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x55}
	frame := ethernet.Frame{
		Dst:       unknownDst,
		Src:       macH1,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("flood payload"),
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("Forward outcome = %v, want Flooded", res.Outcome)
	}
	if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/2" {
		t.Errorf("Forward egress = %+v, want single egress on cabled port 1/1/2", res.Egress)
	}
}

func TestSpecPortOperUpWithNoCableBuildsUnresolved(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, b)},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	sw := fab.Switch("sw1")
	p, ok := sw.Ports().Port("1/1/1")
	if !ok {
		t.Fatal("port 1/1/1 not found")
	}
	if p.OperStatus != port.Unknown {
		t.Errorf("port 1/1/1 OperStatus = %v, want Unknown", p.OperStatus)
	}
	if configured, _ := fab.Config().Switches["sw1"].Ports.Port("1/1/1"); configured.OperStatus != port.Up {
		t.Errorf("Config() port 1/1/1 OperStatus = %v, want the configured Up", configured.OperStatus)
	}
	issues := fab.Metadata().IssuesFor(analysis.PortScope("sw1", "1/1/1"))
	if len(issues) != 1 || issues[0].Code != analysis.IssueCode(fabric.ReasonAdjacencyUnresolved) {
		t.Errorf("port issues = %+v, want adjacency-unresolved alone", issues)
	}
}

func TestLagWithOneMemberCabledIsUp(t *testing.T) {
	t.Run("one member cabled brings LAG oper Up", func(t *testing.T) {
		b1 := port.NewBuilder()
		b1.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Down})
		b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down, LagParent: "lag1"})
		b1.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down, LagParent: "lag1"})

		b2 := port.NewBuilder()
		b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})

		cfg := fabric.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {Ports: mustTable(t, b1)},
				"sw2": {Ports: mustTable(t, b2)},
			},
			Cables: []fabric.Cable{
				{
					A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				},
			},
		}

		fab, err := fabric.New(statedPhysical(cfg))
		if err != nil {
			t.Fatalf("New fabric: %v", err)
		}

		sw1 := fab.Switch("sw1")
		lag, _ := sw1.Ports().Port("lag1")
		if lag.OperStatus != port.Up {
			t.Errorf("lag1 OperStatus = %v, want Up", lag.OperStatus)
		}

		p1, _ := sw1.Ports().Port("1/1/1")
		if p1.OperStatus != port.Up {
			t.Errorf("1/1/1 OperStatus = %v, want Up", p1.OperStatus)
		}

		p2, _ := sw1.Ports().Port("1/1/2")
		if p2.OperStatus != port.Down {
			t.Errorf("1/1/2 OperStatus = %v, want Down", p2.OperStatus)
		}
	})

	t.Run("cabled member on cut cable leaves LAG oper Down", func(t *testing.T) {
		b1 := port.NewBuilder()
		b1.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Down})
		b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down, LagParent: "lag1"})

		b2 := port.NewBuilder()
		b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})

		cfg := fabric.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {Ports: mustTable(t, b1)},
				"sw2": {Ports: mustTable(t, b2)},
			},
			Cables: []fabric.Cable{
				{
					Cable: cablemodel.Cable{Fault: cablemodel.Fault{Kind: cablemodel.FaultCut}},
					A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				},
			},
		}

		fab, err := fabric.New(statedPhysical(cfg))
		if err != nil {
			t.Fatalf("New fabric: %v", err)
		}

		sw1 := fab.Switch("sw1")
		lag, _ := sw1.Ports().Port("lag1")
		if lag.OperStatus != port.Down {
			t.Errorf("lag1 OperStatus = %v, want Down", lag.OperStatus)
		}
	})
}

func TestStableCableOrderingInFabricLinks(t *testing.T) {
	b1 := port.NewBuilder()
	b1.Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2 := port.NewBuilder()
	b2.Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	// Insert cables in deliberately reversed order.
	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, b1)},
			"sw2": {Ports: mustTable(t, b2)},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw2", Port: "1/1/3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		},
	}

	fab, err := fabric.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	links := fab.Links()
	if len(links) != 4 {
		t.Fatalf("got %d links, want 4", len(links))
	}

	wantOrder := []fabric.Endpoint{
		{Node: "h1"},
		{Node: "h2"},
		{Node: "sw1", Port: "1/1/2"},
		{Node: "sw1", Port: "1/1/3"},
	}

	for i, want := range wantOrder {
		if links[i].A.Endpoint != want {
			t.Errorf("link %d A endpoint = %+v, want %+v", i, links[i].A.Endpoint, want)
		}
	}
}
