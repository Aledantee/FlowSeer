package fabric_test

import (
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestPortWithNoCableIsOperDownAndAbsentFromFloodSets(t *testing.T) {
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
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	sw := fab.Switch("sw1")
	p3, ok := sw.Ports().Port("1/1/3")
	if !ok {
		t.Fatal("port 1/1/3 not found")
	}
	if p3.OperStatus != port.Down {
		t.Errorf("port 1/1/3 OperStatus = %v, want Down", p3.OperStatus)
	}

	unlinked := fab.Unlinked("sw1")
	if !slices.Equal(unlinked, []string{"1/1/3"}) {
		t.Errorf("Unlinked(sw1) = %v, want [1/1/3]", unlinked)
	}

	unknownDst := netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x55}
	frame := ethernet.Frame{
		Dst:       unknownDst,
		Src:       macH1,
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   []byte("flood payload"),
	}

	res := sw.Forward(fixedTime, "1/1/1", frame)
	if res.Outcome != trace.Flooded {
		t.Fatalf("Forward outcome = %v, want Flooded", res.Outcome)
	}
	if len(res.Egress) != 1 || res.Egress[0].Port != "1/1/2" {
		t.Errorf("Forward egress = %+v, want single egress on cabled port 1/1/2", res.Egress)
	}
}

func TestPortWhosePeerIsAdminDownIsOperDown(t *testing.T) {
	b1 := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2 := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Up})

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

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	sw1 := fab.Switch("sw1")
	p1, _ := sw1.Ports().Port("1/1/1")
	if p1.OperStatus != port.Down {
		t.Errorf("sw1 port 1/1/1 OperStatus = %v, want Down", p1.OperStatus)
	}

	sw2 := fab.Switch("sw2")
	p2, _ := sw2.Ports().Port("1/1/1")
	if p2.OperStatus != port.Down {
		t.Errorf("sw2 port 1/1/1 OperStatus = %v, want Down", p2.OperStatus)
	}

	links := fab.Links()
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	l := links[0]
	if l.A.Oper != port.Down || l.A.Reason != fabric.ReasonPeerDown {
		t.Errorf("link end A = %+v, want Down with reason peer-down", l.A)
	}
	if l.B.Oper != port.Down || l.B.Reason != fabric.ReasonPeerDown {
		t.Errorf("link end B = %+v, want Down with reason peer-down", l.B)
	}
}

func TestCutCableBringsBothEndsDownWithReasonCut(t *testing.T) {
	b1 := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2 := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, b1)},
			"sw2": {Ports: mustTable(t, b2)},
		},
		Cables: []fabric.Cable{
			{
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				Fault: fabric.Fault{Kind: fabric.FaultCut},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	sw1 := fab.Switch("sw1")
	p1, _ := sw1.Ports().Port("1/1/1")
	if p1.OperStatus != port.Down {
		t.Errorf("sw1 port 1/1/1 OperStatus = %v, want Down", p1.OperStatus)
	}

	sw2 := fab.Switch("sw2")
	p2, _ := sw2.Ports().Port("1/1/1")
	if p2.OperStatus != port.Down {
		t.Errorf("sw2 port 1/1/1 OperStatus = %v, want Down", p2.OperStatus)
	}

	links := fab.Links()
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	l := links[0]
	if l.A.Oper != port.Down || l.A.Reason != fabric.ReasonCut {
		t.Errorf("link end A = %+v, want Down with reason cut", l.A)
	}
	if l.B.Oper != port.Down || l.B.Reason != fabric.ReasonCut {
		t.Errorf("link end B = %+v, want Down with reason cut", l.B)
	}
}

func TestDeadDirectionCableLeavesBothEndsDownWhenEitherEndAutoNegotiates(t *testing.T) {
	cases := []struct {
		name      string
		faultKind fabric.FaultKind
		ethA      *phy.Ethernet
		ethB      *phy.Ethernet
	}{
		{
			name:      "both ends auto A-to-B",
			faultKind: fabric.FaultDeadAToB,
			ethA:      nil,
			ethB:      nil,
		},
		{
			name:      "both ends auto B-to-A",
			faultKind: fabric.FaultDeadBToA,
			ethA:      nil,
			ethB:      nil,
		},
		{
			name:      "forced A against auto B",
			faultKind: fabric.FaultDeadAToB,
			ethA: &phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full, AutoNegotiation: false},
			},
			ethB: nil,
		},
		{
			name:      "auto A against forced B",
			faultKind: fabric.FaultDeadBToA,
			ethA:      nil,
			ethB: &phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full, AutoNegotiation: false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b1 := port.NewBuilder().Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
			b2 := port.NewBuilder().Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

			var phy1, phy2 *phy.Config
			if tc.ethA != nil {
				eth := *tc.ethA
				eth.SupportedSpeedsBPS = []uint64{eth.Setting.SpeedBPS}
				phy1 = &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": eth}}
			}
			if tc.ethB != nil {
				eth := *tc.ethB
				eth.SupportedSpeedsBPS = []uint64{eth.Setting.SpeedBPS}
				phy2 = &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": eth}}
			}

			cfg := fabric.Config{
				Switches: map[string]vswitch.Config{
					"sw1": {Ports: mustTable(t, b1), Phy: phy1},
					"sw2": {Ports: mustTable(t, b2), Phy: phy2},
				},
				Cables: []fabric.Cable{
					{
						A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
						B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
						Fault: fabric.Fault{Kind: tc.faultKind},
					},
				},
			}

			fab, err := fabric.New(cfg)
			if err != nil {
				t.Fatalf("New fabric: %v", err)
			}

			sw1 := fab.Switch("sw1")
			p1, _ := sw1.Ports().Port("1/1/1")
			if p1.OperStatus != port.Down {
				t.Errorf("sw1 port OperStatus = %v, want Down", p1.OperStatus)
			}

			sw2 := fab.Switch("sw2")
			p2, _ := sw2.Ports().Port("1/1/1")
			if p2.OperStatus != port.Down {
				t.Errorf("sw2 port OperStatus = %v, want Down", p2.OperStatus)
			}

			l := fab.Links()[0]
			if l.A.Oper != port.Down || l.A.Reason != fabric.ReasonDeadDirection {
				t.Errorf("link end A = %+v, want Down with dead-direction", l.A)
			}
			if l.B.Oper != port.Down || l.B.Reason != fabric.ReasonDeadDirection {
				t.Errorf("link end B = %+v, want Down with dead-direction", l.B)
			}
		})
	}
}

func TestDeadDirectionCableLeavesBothEndsUpWhenBothAreForced(t *testing.T) {
	cases := []struct {
		name      string
		faultKind fabric.FaultKind
	}{
		{name: "DeadAToB", faultKind: fabric.FaultDeadAToB},
		{name: "DeadBToA", faultKind: fabric.FaultDeadBToA},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b1 := port.NewBuilder().Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
			b2 := port.NewBuilder().Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

			forcedEth := phy.Ethernet{
				SupportedSpeedsBPS: []uint64{100_000_000},
				Setting: &phy.Setting{
					SpeedBPS:        100_000_000,
					Duplex:          phy.Full,
					AutoNegotiation: false,
				},
			}

			cfg := fabric.Config{
				Switches: map[string]vswitch.Config{
					"sw1": {
						Ports: mustTable(t, b1),
						Phy:   &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": forcedEth}},
					},
					"sw2": {
						Ports: mustTable(t, b2),
						Phy:   &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": forcedEth}},
					},
				},
				Cables: []fabric.Cable{
					{
						A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
						B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
						Fault: fabric.Fault{Kind: tc.faultKind},
					},
				},
			}

			fab, err := fabric.New(cfg)
			if err != nil {
				t.Fatalf("New fabric: %v", err)
			}

			sw1 := fab.Switch("sw1")
			p1, _ := sw1.Ports().Port("1/1/1")
			if p1.OperStatus != port.Up {
				t.Errorf("sw1 port OperStatus = %v, want Up", p1.OperStatus)
			}

			sw2 := fab.Switch("sw2")
			p2, _ := sw2.Ports().Port("1/1/1")
			if p2.OperStatus != port.Up {
				t.Errorf("sw2 port OperStatus = %v, want Up", p2.OperStatus)
			}

			l := fab.Links()[0]
			if l.A.Oper != port.Up || l.A.Reason != "" || l.A.Speed.SpeedBPS != 100_000_000 {
				t.Errorf("link end A = %+v, want Up at 100M", l.A)
			}
			if l.B.Oper != port.Up || l.B.Reason != "" || l.B.Speed.SpeedBPS != 100_000_000 {
				t.Errorf("link end B = %+v, want Up at 100M", l.B)
			}
		})
	}
}

func TestSpecPortOperUpWithNoCableBuildsWithPortDown(t *testing.T) {
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
	if p.OperStatus != port.Down {
		t.Errorf("port 1/1/1 OperStatus = %v, want Down", p.OperStatus)
	}
}

func TestLagWithOneMemberCabledIsUp(t *testing.T) {
	t.Run("one member cabled brings LAG oper Up", func(t *testing.T) {
		b1 := port.NewBuilder()
		b1.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Down})
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

		fab, err := fabric.New(cfg)
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
		b1.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Down})
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
					A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
					Fault: fabric.Fault{Kind: fabric.FaultCut},
				},
			},
		}

		fab, err := fabric.New(cfg)
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

func TestHostAgainstPortForcedTo10ResolvesTo10(t *testing.T) {
	b := port.NewBuilder().Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	forced10 := phy.Ethernet{
		SupportedSpeedsBPS: []uint64{10_000_000},
		Setting: &phy.Setting{
			SpeedBPS:        10_000_000,
			Duplex:          phy.Full,
			AutoNegotiation: false,
		},
	}

	macH := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: mustTable(t, b),
				Phy:   &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": forced10}},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	links := fab.Links()
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	l := links[0]
	if l.A.Oper != port.Up || l.A.Speed.SpeedBPS != 10_000_000 {
		t.Errorf("host link end = %+v, want Up at 10M", l.A)
	}
	if l.B.Oper != port.Up || l.B.Speed.SpeedBPS != 10_000_000 {
		t.Errorf("switch link end = %+v, want Up at 10M", l.B)
	}
}

func TestTwoHostsOnOneCableResolveTo1000(t *testing.T) {
	mac1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	mac2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := fabric.Config{
		Hosts: map[string]fabric.Host{
			"h1": {Address: mac1},
			"h2": {Address: mac2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	links := fab.Links()
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	l := links[0]
	if l.A.Oper != port.Up || l.A.Speed.SpeedBPS != 1_000_000_000 || l.A.Speed.Duplex != phy.Full {
		t.Errorf("host 1 link end = %+v, want Up at 1000M Full", l.A)
	}
	if l.B.Oper != port.Up || l.B.Speed.SpeedBPS != 1_000_000_000 || l.B.Speed.Duplex != phy.Full {
		t.Errorf("host 2 link end = %+v, want Up at 1000M Full", l.B)
	}
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
		Hosts: map[string]fabric.Host{
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

	fab, err := fabric.New(cfg)
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
		{Node: "sw2", Port: "1/1/3"},
	}

	for i, want := range wantOrder {
		if links[i].A.Endpoint != want {
			t.Errorf("link %d A endpoint = %+v, want %+v", i, links[i].A.Endpoint, want)
		}
	}
}
