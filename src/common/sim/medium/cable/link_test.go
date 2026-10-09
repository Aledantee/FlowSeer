package cable_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestPortWhosePeerIsAdminDownIsOperDown(t *testing.T) {
	b1 := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2 := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Up})

	cfg := assembly.Config{
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

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	sw1 := fab.Device("sw1").(*vswitch.Switch)
	p1, _ := sw1.Ports().Port("1/1/1")
	if p1.OperStatus != port.Down {
		t.Errorf("sw1 port 1/1/1 OperStatus = %v, want Down", p1.OperStatus)
	}

	sw2 := fab.Device("sw2").(*vswitch.Switch)
	p2, _ := sw2.Ports().Port("1/1/1")
	if p2.OperStatus != port.Down {
		t.Errorf("sw2 port 1/1/1 OperStatus = %v, want Down", p2.OperStatus)
	}

	links := fab.Links()
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	l := links[0]
	down, up := l.A, l.B
	if down.Node != "sw2" {
		down, up = l.B, l.A
	}
	if down.Oper != port.Down || down.Reason != cablemodel.ReasonAdminDown {
		t.Errorf("the disabled end = %+v, want Down with reason admin-down", down)
	}
	if up.Oper != port.Down || up.Reason != cablemodel.ReasonPeerDown {
		t.Errorf("the other end = %+v, want Down with reason peer-down", up)
	}
}

func TestCutCableBringsBothEndsDownWithReasonCut(t *testing.T) {
	b1 := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2 := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})

	cfg := assembly.Config{
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

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	sw1 := fab.Device("sw1").(*vswitch.Switch)
	p1, _ := sw1.Ports().Port("1/1/1")
	if p1.OperStatus != port.Down {
		t.Errorf("sw1 port 1/1/1 OperStatus = %v, want Down", p1.OperStatus)
	}

	sw2 := fab.Device("sw2").(*vswitch.Switch)
	p2, _ := sw2.Ports().Port("1/1/1")
	if p2.OperStatus != port.Down {
		t.Errorf("sw2 port 1/1/1 OperStatus = %v, want Down", p2.OperStatus)
	}

	links := fab.Links()
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	l := links[0]
	if l.A.Oper != port.Down || l.A.Reason != cablemodel.ReasonCut {
		t.Errorf("link end A = %+v, want Down with reason cut", l.A)
	}
	if l.B.Oper != port.Down || l.B.Reason != cablemodel.ReasonCut {
		t.Errorf("link end B = %+v, want Down with reason cut", l.B)
	}
}

func TestDeadDirectionCableLeavesBothEndsDownWhenEitherEndAutoNegotiates(t *testing.T) {
	cases := []struct {
		name      string
		faultKind cablemodel.FaultKind
		ethA      *phy.Ethernet
		ethB      *phy.Ethernet
	}{
		{
			name:      "both ends auto A-to-B",
			faultKind: cablemodel.FaultDeadAToB,
			ethA:      nil,
			ethB:      nil,
		},
		{
			name:      "both ends auto B-to-A",
			faultKind: cablemodel.FaultDeadBToA,
			ethA:      nil,
			ethB:      nil,
		},
		{
			name:      "forced A against auto B",
			faultKind: cablemodel.FaultDeadAToB,
			ethA: &phy.Ethernet{
				Setting: &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full, AutoNegotiation: false},
			},
			ethB: nil,
		},
		{
			name:      "auto A against forced B",
			faultKind: cablemodel.FaultDeadBToA,
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

			cfg := assembly.Config{
				Switches: map[string]vswitch.Config{
					"sw1": {Ports: mustTable(t, b1), Phy: phy1},
					"sw2": {Ports: mustTable(t, b2), Phy: phy2},
				},
				Cables: []fabric.Cable{
					{
						Cable: cablemodel.Cable{Fault: cablemodel.Fault{Kind: tc.faultKind}},
						A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
						B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
					},
				},
			}

			fab, err := assembly.New(statedPhysical(cfg))
			if err != nil {
				t.Fatalf("New fabric: %v", err)
			}

			sw1 := fab.Device("sw1").(*vswitch.Switch)
			p1, _ := sw1.Ports().Port("1/1/1")
			if p1.OperStatus != port.Down {
				t.Errorf("sw1 port OperStatus = %v, want Down", p1.OperStatus)
			}

			sw2 := fab.Device("sw2").(*vswitch.Switch)
			p2, _ := sw2.Ports().Port("1/1/1")
			if p2.OperStatus != port.Down {
				t.Errorf("sw2 port OperStatus = %v, want Down", p2.OperStatus)
			}

			l := fab.Links()[0]
			if l.A.Oper != port.Down || l.A.Reason != cablemodel.ReasonDeadDirection {
				t.Errorf("link end A = %+v, want Down with dead-direction", l.A)
			}
			if l.B.Oper != port.Down || l.B.Reason != cablemodel.ReasonDeadDirection {
				t.Errorf("link end B = %+v, want Down with dead-direction", l.B)
			}
		})
	}
}

func TestDeadDirectionCableLeavesBothEndsUpWhenBothAreForced(t *testing.T) {
	cases := []struct {
		name      string
		faultKind cablemodel.FaultKind
	}{
		{name: "DeadAToB", faultKind: cablemodel.FaultDeadAToB},
		{name: "DeadBToA", faultKind: cablemodel.FaultDeadBToA},
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

			cfg := assembly.Config{
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
						Cable: cablemodel.Cable{Fault: cablemodel.Fault{Kind: tc.faultKind}},
						A:     fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
						B:     fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
					},
				},
			}

			fab, err := assembly.New(statedPhysical(cfg))
			if err != nil {
				t.Fatalf("New fabric: %v", err)
			}

			sw1 := fab.Device("sw1").(*vswitch.Switch)
			p1, _ := sw1.Ports().Port("1/1/1")
			if p1.OperStatus != port.Up {
				t.Errorf("sw1 port OperStatus = %v, want Up", p1.OperStatus)
			}

			sw2 := fab.Device("sw2").(*vswitch.Switch)
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
	cfg := assembly.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: mustTable(t, b),
				Phy:   &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": forced10}},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
		},
	}

	fab, err := assembly.New(statedPhysical(cfg))
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

	cfg := assembly.Config{
		Hosts: map[string]hostmodel.Config{
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

	fab, err := assembly.New(statedPhysical(cfg))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	links := fab.Links()
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	l := links[0]
	if l.A.Oper != port.Up || l.A.Speed.SpeedBPS != 1_000_000_000 || l.A.Speed.DuplexA != phy.Full {
		t.Errorf("host 1 link end = %+v, want Up at 1000M Full", l.A)
	}
	if l.B.Oper != port.Up || l.B.Speed.SpeedBPS != 1_000_000_000 || l.B.Speed.DuplexA != phy.Full {
		t.Errorf("host 2 link end = %+v, want Up at 1000M Full", l.B)
	}
}

func TestCableReachBoundsLinkNegotiation(t *testing.T) {
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	eth1G := phy.Ethernet{SupportedSpeedsBPS: []uint64{1_000_000_000}}
	eth1G10G := phy.Ethernet{SupportedSpeedsBPS: []uint64{1_000_000_000, 10_000_000_000}}
	forced1G := phy.Ethernet{
		SupportedSpeedsBPS: []uint64{1_000_000_000},
		Setting:            &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full, AutoNegotiation: false},
	}
	forced10G := phy.Ethernet{
		SupportedSpeedsBPS: []uint64{10_000_000_000},
		Setting:            &phy.Setting{SpeedBPS: 10_000_000_000, Duplex: phy.Full, AutoNegotiation: false},
	}

	tests := []struct {
		name         string
		ethA         *phy.Ethernet
		ethB         *phy.Ethernet
		nodeA        fabric.Endpoint
		nodeB        fabric.Endpoint
		hosts        map[string]hostmodel.Config
		medium       cablemodel.Medium
		lengthMeters float64
		wantOper     port.LinkState
		wantReason   trace.Reason
		wantSpeedBPS uint64
	}{
		{
			name:         "120m twisted pair between 1G ends is reach exceeded",
			ethA:         &eth1G,
			ethB:         &eth1G,
			nodeA:        fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			nodeB:        fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			medium:       cablemodel.TwistedPair,
			lengthMeters: 120,
			wantOper:     port.Down,
			wantReason:   cablemodel.ReasonReachExceeded,
		},
		{
			name:         "90m multimode between 1G and 10G ends negotiates 10G",
			ethA:         &eth1G10G,
			ethB:         &eth1G10G,
			nodeA:        fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			nodeB:        fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			medium:       cablemodel.MultimodeFiber,
			lengthMeters: 90,
			wantOper:     port.Up,
			wantSpeedBPS: 10_000_000_000,
		},
		{
			name:         "400m multimode between 1G and 10G ends negotiates 1G",
			ethA:         &eth1G10G,
			ethB:         &eth1G10G,
			nodeA:        fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			nodeB:        fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			medium:       cablemodel.MultimodeFiber,
			lengthMeters: 400,
			wantOper:     port.Up,
			wantSpeedBPS: 1_000_000_000,
		},
		{
			name:         "600m multimode between 1G and 10G ends is reach exceeded",
			ethA:         &eth1G10G,
			ethB:         &eth1G10G,
			nodeA:        fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			nodeB:        fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			medium:       cablemodel.MultimodeFiber,
			lengthMeters: 600,
			wantOper:     port.Down,
			wantReason:   cablemodel.ReasonReachExceeded,
		},
		{
			name:         "0m twisted pair between 1G and 10G ends negotiates 10G",
			ethA:         &eth1G10G,
			ethB:         &eth1G10G,
			nodeA:        fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			nodeB:        fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			medium:       cablemodel.TwistedPair,
			lengthMeters: 0,
			wantOper:     port.Up,
			wantSpeedBPS: 10_000_000_000,
		},
		{
			name:         "300m twisted pair between two hosts is reach exceeded",
			nodeA:        fabric.Endpoint{Node: "h1"},
			nodeB:        fabric.Endpoint{Node: "h2"},
			hosts:        map[string]hostmodel.Config{"h1": {Address: macH1}, "h2": {Address: macH2}},
			medium:       cablemodel.TwistedPair,
			lengthMeters: 300,
			wantOper:     port.Down,
			wantReason:   cablemodel.ReasonReachExceeded,
		},
		{
			name:         "300m twisted pair between host and undeclared port is reach exceeded",
			nodeA:        fabric.Endpoint{Node: "h1"},
			nodeB:        fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			hosts:        map[string]hostmodel.Config{"h1": {Address: macH1}},
			medium:       cablemodel.TwistedPair,
			lengthMeters: 300,
			wantOper:     port.Down,
			wantReason:   cablemodel.ReasonReachExceeded,
		},
		{
			name:         "forced 1G against auto 1G and 10G on 400m multimode is unmodeled",
			ethA:         &forced1G,
			ethB:         &eth1G10G,
			nodeA:        fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			nodeB:        fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			medium:       cablemodel.MultimodeFiber,
			lengthMeters: 400,
			wantOper:     port.Unknown,
			wantReason:   phy.ReasonForcedAgainstAutoUnmodeled,
		},
		{
			name:         "forced 10G against auto 1G and 10G on 400m multimode is reach exceeded",
			ethA:         &forced10G,
			ethB:         &eth1G10G,
			nodeA:        fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			nodeB:        fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
			medium:       cablemodel.MultimodeFiber,
			lengthMeters: 400,
			wantOper:     port.Down,
			wantReason:   cablemodel.ReasonReachExceeded,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := assembly.Config{
				Switches: make(map[string]vswitch.Config),
				Hosts:    tc.hosts,
				Cables: []fabric.Cable{
					{
						Cable: cablemodel.Cable{LengthMeters: tc.lengthMeters, Medium: tc.medium},
						A:     tc.nodeA,
						B:     tc.nodeB,
					},
				},
			}

			if tc.nodeA.Port != "" {
				b := port.NewBuilder().Add(port.Port{Name: tc.nodeA.Port, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
				sw := vswitch.Config{Ports: mustTable(t, b)}
				if tc.ethA != nil {
					sw.Phy = &phy.Config{Ethernet: map[string]phy.Ethernet{tc.nodeA.Port: *tc.ethA}}
				}
				cfg.Switches[tc.nodeA.Node] = sw
			}
			if tc.nodeB.Port != "" {
				b := port.NewBuilder().Add(port.Port{Name: tc.nodeB.Port, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
				sw := vswitch.Config{Ports: mustTable(t, b)}
				if tc.ethB != nil {
					sw.Phy = &phy.Config{Ethernet: map[string]phy.Ethernet{tc.nodeB.Port: *tc.ethB}}
				}
				cfg.Switches[tc.nodeB.Node] = sw
			}

			fab, err := assembly.New(statedPhysical(cfg))
			if err != nil {
				t.Fatalf("New fabric: %v", err)
			}

			links := fab.Links()
			if len(links) != 1 {
				t.Fatalf("got %d links, want 1", len(links))
			}
			l := links[0]

			if l.A.Oper != tc.wantOper || l.B.Oper != tc.wantOper {
				t.Fatalf("link oper status A=%v B=%v, want %v", l.A.Oper, l.B.Oper, tc.wantOper)
			}
			if tc.wantOper != port.Up {
				if l.A.Reason != tc.wantReason || l.B.Reason != tc.wantReason {
					t.Errorf("link reasons A=%q B=%q, want %q", l.A.Reason, l.B.Reason, tc.wantReason)
				}
				if tc.wantReason == cablemodel.ReasonReachExceeded {
					if l.A.Reason == phy.ReasonSpeedMismatch || l.B.Reason == phy.ReasonSpeedMismatch {
						t.Errorf("link reported speed-mismatch instead of reach-exceeded")
					}
				}
			} else {
				if l.A.Speed.SpeedBPS != tc.wantSpeedBPS || l.B.Speed.SpeedBPS != tc.wantSpeedBPS {
					t.Errorf("link speeds A=%d B=%d, want %d", l.A.Speed.SpeedBPS, l.B.Speed.SpeedBPS, tc.wantSpeedBPS)
				}
				if l.A.Speed.DuplexA != phy.Full || l.B.Speed.DuplexA != phy.Full {
					t.Errorf("link duplex A=%v B=%v, want Full", l.A.Speed.DuplexA, l.B.Speed.DuplexA)
				}
			}
		})
	}
}
