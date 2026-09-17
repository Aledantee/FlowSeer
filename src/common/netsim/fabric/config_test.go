package fabric_test

import (
	"maps"
	"math"
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

var fixedTime = time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)

func mustTable(t *testing.T, b *port.Builder) port.Table {
	t.Helper()
	tbl, err := b.Build()
	if err != nil {
		t.Fatalf("build port table: %v", err)
	}

	return tbl
}

func constructionSpec(cfg fabric.Config) fabric.ConstructionSpec {
	switches := make(map[string]vswitch.ConstructionSpec, len(cfg.Switches))
	for name, swCfg := range cfg.Switches {
		switches[name] = vswitch.ConstructionSpec{Config: swCfg, NodeID: name}
	}

	return fabric.ConstructionSpec{
		Start:         cfg.Start,
		Switches:      switches,
		Hosts:         cfg.Hosts,
		Reflectors:    cfg.Reflectors,
		Cables:        cfg.Cables,
		Uncabled:      cfg.Uncabled,
		PhyAssumption: cfg.PhyAssumption,
	}
}

// gigabitCopper is the profile a fixture assumes when its test is not about
// negotiation: auto-negotiating 10 Mb/s to 1 Gb/s ends over twisted pair, so two
// ends with no facts of their own resolve 1 Gb/s full duplex.
func gigabitCopper() *fabric.PhyAssumption {
	return &fabric.PhyAssumption{
		Medium: fabric.TwistedPair,
		Ethernet: phy.Ethernet{
			SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
			AutoNegotiationSupported: phy.CapabilitySupported,
			Setting:                  &phy.Setting{AutoNegotiation: true},
		},
	}
}

// statedPhysical returns a copy of cfg that states what its fixture leaves
// unreported: the gigabit copper profile for every missing physical fact, and
// an Uncabled entry for every non-LAG switch port no cable names. A fixture
// written before unreported facts became unknown keeps the links it was
// written against. A test of unknown facts builds its configuration without it.
//
// An added Uncabled port's configured operational status is cleared, since the
// fixture observed nothing about it; left Up, it would conflict with the Down
// its entry derives and put that conflict into every journey consulting it.
func statedPhysical(cfg fabric.Config) fabric.Config {
	cfg = cfg.Clone()
	if cfg.PhyAssumption == nil {
		cfg.PhyAssumption = gigabitCopper()
	}

	named := make(map[fabric.Endpoint]bool)
	for _, cable := range cfg.Cables {
		named[cable.A] = true
		named[cable.B] = true
	}
	for _, entry := range cfg.Uncabled {
		named[entry.Endpoint] = true
	}
	switchNames := slices.Sorted(maps.Keys(cfg.Switches))
	for _, name := range switchNames {
		swCfg := cfg.Switches[name]
		b := port.NewBuilder()
		for _, p := range swCfg.Ports.Ports() {
			ep := fabric.Endpoint{Node: name, Port: p.Name}
			if p.Kind != port.Lag && !named[ep] {
				cfg.Uncabled = append(cfg.Uncabled, fabric.Uncabled{Endpoint: ep})
				p.OperStatus = ""
			}
			b.Add(p)
		}
		// The table already built from these ports, so it builds again.
		swCfg.Ports, _ = b.Build()
		cfg.Switches[name] = swCfg
	}

	return cfg
}

func twoSwitchBaseConfig(t *testing.T) fabric.Config {
	t.Helper()
	b1 := port.NewBuilder()
	b1.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})

	b2 := port.NewBuilder()
	b2.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	return fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, b1)},
			"sw2": {Ports: mustTable(t, b2)},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A:            fabric.Endpoint{Node: "h1"},
				B:            fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
				LengthMeters: 5,
			},
			{
				A:            fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				LengthMeters: 20,
			},
			{
				A:            fabric.Endpoint{Node: "h2"},
				B:            fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				LengthMeters: 5,
			},
		},
	}
}

func TestConfigValidateRules(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*fabric.Config)
		wantError bool
	}{
		{
			name: "switch config fails validation",
			mutate: func(c *fabric.Config) {
				c.Switches["sw1"] = vswitch.Config{
					Ports: c.Switches["sw1"].Ports,
					Bridge: &bridge.Config{
						VLAN: &bridge.VLAN{
							Table: map[vlan.ID]string{0: "invalid-vid"},
						},
					},
				}
			},
			wantError: true,
		},
		{
			name: "cable endpoint naming absent node",
			mutate: func(c *fabric.Config) {
				c.Cables[0].A = fabric.Endpoint{Node: "absent_node"}
			},
			wantError: true,
		},
		{
			name: "cable endpoint naming absent switch port",
			mutate: func(c *fabric.Config) {
				c.Cables[0].B = fabric.Endpoint{Node: "sw1", Port: "1/1/99"}
			},
			wantError: true,
		},
		{
			name: "cable endpoint naming host with non-empty port",
			mutate: func(c *fabric.Config) {
				c.Cables[0].A = fabric.Endpoint{Node: "h1", Port: "eth0"}
			},
			wantError: true,
		},
		{
			name: "cable endpoint naming LAG directly",
			mutate: func(c *fabric.Config) {
				c.Cables[0].B = fabric.Endpoint{Node: "sw1", Port: "lag1"}
			},
			wantError: true,
		},
		{
			name: "switch port on two cables",
			mutate: func(c *fabric.Config) {
				c.Cables = append(c.Cables, fabric.Cable{
					A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
					B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				})
			},
			wantError: true,
		},
		{
			name: "host on two cables",
			mutate: func(c *fabric.Config) {
				c.Cables = append(c.Cables, fabric.Cable{
					A: fabric.Endpoint{Node: "h1"},
					B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"},
				})
			},
			wantError: true,
		},
		{
			name: "host on no cables",
			mutate: func(c *fabric.Config) {
				c.Hosts["h3"] = fabric.Host{Address: netaddr.MAC{0, 0, 0, 0, 0, 3}}
			},
			wantError: true,
		},
		{
			name: "cable with negative length",
			mutate: func(c *fabric.Config) {
				c.Cables[0].LengthMeters = -1.0
			},
			wantError: true,
		},
		{
			name: "cable with NaN length",
			mutate: func(c *fabric.Config) {
				c.Cables[0].LengthMeters = math.NaN()
			},
			wantError: true,
		},
		{
			name: "cable with positive infinite length",
			mutate: func(c *fabric.Config) {
				c.Cables[0].LengthMeters = math.Inf(1)
			},
			wantError: true,
		},
		{
			name: "cable with negative infinite length",
			mutate: func(c *fabric.Config) {
				c.Cables[0].LengthMeters = math.Inf(-1)
			},
			wantError: true,
		},
		{
			name: "cable with zero length",
			mutate: func(c *fabric.Config) {
				c.Cables[0].LengthMeters = 0
			},
			wantError: false,
		},
		{
			name: "cable with maximum finite length",
			mutate: func(c *fabric.Config) {
				c.Cables[0].LengthMeters = math.MaxFloat64
			},
			wantError: false,
		},
		{
			name: "cable with negative delay",
			mutate: func(c *fabric.Config) {
				d := -1 * time.Nanosecond
				c.Cables[0].Delay = &d
			},
			wantError: true,
		},
		{
			name: "cable with unknown medium coax",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Medium = "coax"
			},
			wantError: true,
		},
		{
			name: "cable with empty medium accepted",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Medium = ""
			},
			wantError: false,
		},
		{
			name: "cable fault LoseEveryNth with N zero",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultLoseEveryNth, N: 0}
			},
			wantError: true,
		},
		{
			name: "cable fault CorruptEveryNth with N zero",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultCorruptEveryNth, N: 0}
			},
			wantError: true,
		},
		{
			name: "cable fault LoseSequence with empty sequence",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultLoseSequence, Sequence: nil}
			},
			wantError: true,
		},
		{
			name: "inactive N on cut fault",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultCut, N: 2}
			},
			wantError: true,
		},
		{
			name: "inactive sequence on every Nth fault",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultLoseEveryNth, N: 2, Sequence: []uint{1}}
			},
			wantError: true,
		},
		{
			name: "inactive N on sequence fault",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultLoseSequence, N: 2, Sequence: []uint{1}}
			},
			wantError: true,
		},
		{
			name: "sequence fault with zero position",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultLoseSequence, Sequence: []uint{0, 1}}
			},
			wantError: true,
		},
		{
			name: "cable fault with unknown kind",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: "InvalidKind"}
			},
			wantError: true,
		},
		{
			name: "host with invalid VLAN ID zero",
			mutate: func(c *fabric.Config) {
				vid := vlan.ID(0)
				c.Hosts["h1"] = fabric.Host{
					Address: c.Hosts["h1"].Address,
					VLAN:    &vid,
				}
			},
			wantError: true,
		},
		{
			name: "host with invalid VLAN ID above 4094",
			mutate: func(c *fabric.Config) {
				vid := vlan.ID(4095)
				c.Hosts["h1"] = fabric.Host{
					Address: c.Hosts["h1"].Address,
					VLAN:    &vid,
				}
			},
			wantError: true,
		},
		{
			name: "node name collision between switch and host",
			mutate: func(c *fabric.Config) {
				c.Hosts["sw1"] = fabric.Host{Address: netaddr.MAC{0, 0, 0, 0, 0, 1}}
			},
			wantError: true,
		},
		{
			name: "switch name empty",
			mutate: func(c *fabric.Config) {
				c.Switches[""] = c.Switches["sw1"]
				delete(c.Switches, "sw1")
			},
			wantError: true,
		},
		{
			name: "host name empty",
			mutate: func(c *fabric.Config) {
				c.Hosts[""] = c.Hosts["h1"]
				delete(c.Hosts, "h1")
			},
			wantError: true,
		},
		{
			name: "endpoint with empty node name",
			mutate: func(c *fabric.Config) {
				c.Cables[0].A = fabric.Endpoint{Node: "", Port: ""}
			},
			wantError: true,
		},
		{
			name: "switch endpoint with empty port name",
			mutate: func(c *fabric.Config) {
				c.Cables[0].B = fabric.Endpoint{Node: "sw1", Port: ""}
			},
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := twoSwitchBaseConfig(t)
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantError {
				t.Errorf("Validate() error = %v, wantError = %v", err, tc.wantError)
			}
		})
	}
}

func TestTwoSwitchConfigValidation(t *testing.T) {
	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)

	cases := []struct {
		name      string
		mutate    func(*fabric.Config)
		wantError bool
	}{
		{
			name:      "baseline valid two switch spec",
			mutate:    func(_ *fabric.Config) {},
			wantError: false,
		},
		{
			name: "valid with tagged host VLAN",
			mutate: func(c *fabric.Config) {
				h1 := c.Hosts["h1"]
				h1.VLAN = &vid10
				c.Hosts["h1"] = h1

				h2 := c.Hosts["h2"]
				h2.VLAN = &vid20
				c.Hosts["h2"] = h2
			},
			wantError: false,
		},
		{
			name: "valid with cable faults",
			mutate: func(c *fabric.Config) {
				c.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultCut}
				c.Cables[1].Fault = fabric.Fault{Kind: fabric.FaultLoseEveryNth, N: 3}
				c.Cables[2].Fault = fabric.Fault{Kind: fabric.FaultLoseSequence, Sequence: []uint{1, 5, 10}}
			},
			wantError: false,
		},
		{
			name: "valid with top speed limit and length",
			mutate: func(c *fabric.Config) {
				c.Cables[1].TopSpeedBPS = 100_000_000
				c.Cables[1].LengthMeters = 300.5
				c.Cables[1].Medium = fabric.MultimodeFiber
			},
			wantError: false,
		},
		{
			name: "duplicate host addresses",
			mutate: func(c *fabric.Config) {
				h2 := c.Hosts["h2"]
				h2.Address = c.Hosts["h1"].Address
				c.Hosts["h2"] = h2
			},
			wantError: true,
		},
		{
			name: "host address equals switch base MAC",
			mutate: func(c *fabric.Config) {
				sw1 := c.Switches["sw1"]
				sw1.MAC = c.Hosts["h1"].Address
				c.Switches["sw1"] = sw1
			},
			wantError: true,
		},
		{
			name: "host address equals switch routed interface MAC",
			mutate: func(c *fabric.Config) {
				sw1 := c.Switches["sw1"]
				sw1.Routing = &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"1/1/1": {
									Port:     "1/1/1",
									MAC:      c.Hosts["h1"].Address,
									Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.1.1/24")},
								},
							},
						},
					},
				}
				c.Switches["sw1"] = sw1
			},
			wantError: true,
		},
		{
			name: "host address equals switch STP address",
			mutate: func(c *fabric.Config) {
				sw1 := c.Switches["sw1"]
				c.Start = fixedTime
				sw1.Bridge = &bridge.Config{}
				sw1.STP = &stp.Config{
					Address: c.Hosts["h1"].Address,
				}
				c.Switches["sw1"] = sw1
			},
			wantError: true,
		},
		{
			name: "duplicate switch base MACs",
			mutate: func(c *fabric.Config) {
				mac := netaddr.MAC{0x00, 0x5e, 0x00, 0x01, 0x01, 0x01}
				sw1 := c.Switches["sw1"]
				sw1.MAC = mac
				c.Switches["sw1"] = sw1
				sw2 := c.Switches["sw2"]
				sw2.MAC = mac
				c.Switches["sw2"] = sw2
			},
			wantError: true,
		},
		{
			name: "switch base MAC equals another switch routed interface MAC",
			mutate: func(c *fabric.Config) {
				mac := netaddr.MAC{0x00, 0x5e, 0x00, 0x01, 0x01, 0x01}
				sw1 := c.Switches["sw1"]
				sw1.MAC = mac
				c.Switches["sw1"] = sw1
				sw2 := c.Switches["sw2"]
				sw2.Routing = &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"1/1/1": {
									Port:     "1/1/1",
									MAC:      mac,
									Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.2.1/24")},
								},
							},
						},
					},
				}
				c.Switches["sw2"] = sw2
			},
			wantError: true,
		},
		{
			name: "switch routed interface MAC equals same switch base MAC",
			mutate: func(c *fabric.Config) {
				mac := netaddr.MAC{0x00, 0x5e, 0x00, 0x01, 0x01, 0x01}
				b := port.NewBuilder()
				b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
				pTable, _ := b.Build()
				sw1 := c.Switches["sw1"]
				sw1.Ports = pTable
				sw1.MAC = mac
				sw1.Routing = &routing.Config{
					VRFs: map[string]routing.VRF{
						routing.DefaultVRF: {
							Interfaces: map[string]routing.Interface{
								"1/1/1": {
									Port:     "1/1/1",
									MAC:      mac,
									Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.1.1/24")},
								},
								"1/1/2": {
									Port:     "1/1/2",
									Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.2.1/24")},
								},
							},
						},
					},
				}
				c.Switches["sw1"] = sw1
			},
			wantError: false,
		},
		{
			name: "host with IP stack but no addresses",
			mutate: func(c *fabric.Config) {
				h1 := c.Hosts["h1"]
				h1.IP = &fabric.HostIP{Addresses: nil}
				c.Hosts["h1"] = h1
			},
			wantError: true,
		},
		{
			// The host constructs and its default route is withdrawn; see
			// TestHostWithOffLinkGatewayHasNoDefaultRoute.
			name: "host with IP stack gateway unreachable",
			mutate: func(c *fabric.Config) {
				h1 := c.Hosts["h1"]
				h1.IP = &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.20.1"),
				}
				c.Hosts["h1"] = h1
			},
			wantError: false,
		},
		{
			name: "host with IP stack prefix length 0",
			mutate: func(c *fabric.Config) {
				h1 := c.Hosts["h1"]
				h1.IP = &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/0")},
				}
				c.Hosts["h1"] = h1
			},
			wantError: false,
		},
		{
			name: "valid host with IP stack",
			mutate: func(c *fabric.Config) {
				gw := netip.MustParseAddr("10.0.10.1")
				h1 := c.Hosts["h1"]
				h1.IP = &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   gw,
					Neighbors: map[netip.Addr]netaddr.MAC{
						gw: {0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
					},
				}
				c.Hosts["h1"] = h1
			},
			wantError: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := twoSwitchBaseConfig(t)
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantError {
				t.Errorf("Validate() error = %v, wantError = %v", err, tc.wantError)
			}
		})
	}
}

// reflectorBaseConfig extends twoSwitchBaseConfig with a free port on sw1 and
// a reflector r1 cabled to it, attached on VLAN 10.
func reflectorBaseConfig(t *testing.T) fabric.Config {
	t.Helper()
	cfg := twoSwitchBaseConfig(t)

	b := port.NewBuilder()
	for _, p := range cfg.Switches["sw1"].Ports.Ports() {
		b.Add(p)
	}
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	sw1 := cfg.Switches["sw1"]
	sw1.Ports = mustTable(t, b)
	cfg.Switches["sw1"] = sw1

	vid10 := vlan.ID(10)
	cfg.Reflectors = map[string]fabric.Reflector{
		"r1": {
			Ports: map[string]phy.Ethernet{"p1": {}},
			Attachments: map[string]fabric.Attachment{
				"a": {
					Port:      "p1",
					VLAN:      &vid10,
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")},
				},
			},
		},
	}
	cfg.Cables = append(cfg.Cables, fabric.Cable{
		A:            fabric.Endpoint{Node: "sw1", Port: "1/1/3"},
		B:            fabric.Endpoint{Node: "r1", Port: "p1"},
		LengthMeters: 5,
	})

	return cfg
}

func TestReflectorValidateRules(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*fabric.Config)
		wantError bool
	}{
		{
			name:      "unmodified baseline passes",
			mutate:    func(*fabric.Config) {},
			wantError: false,
		},
		{
			name: "node name collision between reflector and switch",
			mutate: func(c *fabric.Config) {
				c.Switches["r1"] = c.Switches["sw1"]
			},
			wantError: true,
		},
		{
			name: "reflector MAC collides with a host",
			mutate: func(c *fabric.Config) {
				r1 := c.Reflectors["r1"]
				r1.Address = c.Hosts["h1"].Address
				c.Reflectors["r1"] = r1
			},
			wantError: true,
		},
		{
			name: "attachment VLAN above the valid range",
			mutate: func(c *fabric.Config) {
				vid := vlan.ID(4095)
				r1 := c.Reflectors["r1"]
				a := r1.Attachments["a"]
				a.VLAN = &vid
				r1.Attachments["a"] = a
				c.Reflectors["r1"] = r1
			},
			wantError: true,
		},
		{
			name: "reflector port with invalid ethernet facts",
			mutate: func(c *fabric.Config) {
				r1 := c.Reflectors["r1"]
				r1.Ports["p1"] = phy.Ethernet{Setting: &phy.Setting{Duplex: "Bogus"}}
				c.Reflectors["r1"] = r1
			},
			wantError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := reflectorBaseConfig(t)
			tc.mutate(&cfg)
			err := cfg.Validate()
			if (err != nil) != tc.wantError {
				t.Errorf("Validate() error = %v, wantError = %v", err, tc.wantError)
			}
		})
	}
}

// TestReflectorValidateRejectionsNameTheirField asserts the error's field
// attribute rather than only that an error occurred: an uncabled port, a port
// named in Uncabled, an attachment naming an unknown port, two attachments
// sharing a port and VLAN, and an attachment with no address would all
// otherwise pass any test that stopped at err != nil. A node name collision
// between a reflector and a host, a reflector with an empty name, a cable
// endpoint naming an unknown reflector port, and a reflector endpoint with an
// empty port name need the same treatment: deleting the guard each names
// still leaves a different rule refusing the configuration, and the field
// attribute is what tells the two apart. The collision guard and the
// empty-port guard carry their own field; the empty-name guard and the
// unknown-port guard carry none, so their cases want nil, which is what a
// different rule's field would displace.
func TestReflectorValidateRejectionsNameTheirField(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*fabric.Config)
		wantField any
	}{
		{
			name: "node name collision between reflector and host",
			mutate: func(c *fabric.Config) {
				c.Hosts["r1"] = fabric.Host{Address: netaddr.MAC{0, 0, 0, 0, 0, 9}}
			},
			wantField: "hosts.r1",
		},
		{
			// Deleting this guard leaves the reflector's name "" reaching the
			// ports-must-be-cabled check, which carries a field of its own;
			// the guard's own error carries none, so the intact case wants
			// nil, not empty string.
			name: "reflector name empty",
			mutate: func(c *fabric.Config) {
				c.Reflectors[""] = c.Reflectors["r1"]
				delete(c.Reflectors, "r1")
				// The cable still names r1, which now names nothing.
				c.Cables = c.Cables[:len(c.Cables)-1]
			},
			wantField: nil,
		},
		{
			// Deleting this guard leaves port "p9" accepted, which reaches
			// the ports-must-be-cabled check for the now-uncabled p1; that
			// error carries a field, the guard's own error does not.
			name: "cable endpoint naming an unknown reflector port",
			mutate: func(c *fabric.Config) {
				c.Cables[len(c.Cables)-1].B.Port = "p9"
			},
			wantField: nil,
		},
		{
			name: "reflector endpoint with empty port name",
			mutate: func(c *fabric.Config) {
				c.Cables[len(c.Cables)-1].B.Port = ""
			},
			wantField: "reflectors.r1.ports",
		},
		{
			name: "an uncabled port",
			mutate: func(c *fabric.Config) {
				r1 := c.Reflectors["r1"]
				r1.Ports["p2"] = phy.Ethernet{}
				c.Reflectors["r1"] = r1
			},
			wantField: "reflectors.r1.ports.p2",
		},
		{
			name: "a port named in Uncabled",
			mutate: func(c *fabric.Config) {
				c.Uncabled = append(c.Uncabled, fabric.Uncabled{Endpoint: fabric.Endpoint{Node: "r1", Port: "p1"}})
			},
			wantField: "uncabled.0",
		},
		{
			name: "an attachment naming an unknown port",
			mutate: func(c *fabric.Config) {
				r1 := c.Reflectors["r1"]
				a := r1.Attachments["a"]
				a.Port = "p9"
				r1.Attachments["a"] = a
				c.Reflectors["r1"] = r1
			},
			wantField: "reflectors.r1.attachments.a.port",
		},
		{
			name: "two attachments sharing a port and VLAN",
			mutate: func(c *fabric.Config) {
				r1 := c.Reflectors["r1"]
				a := r1.Attachments["a"]
				r1.Attachments["b"] = fabric.Attachment{Port: a.Port, VLAN: a.VLAN, Addresses: a.Addresses}
				c.Reflectors["r1"] = r1
			},
			wantField: "reflectors.r1.attachments.b.vlan",
		},
		{
			name: "an attachment with no address",
			mutate: func(c *fabric.Config) {
				r1 := c.Reflectors["r1"]
				a := r1.Attachments["a"]
				a.Addresses = nil
				r1.Attachments["a"] = a
				c.Reflectors["r1"] = r1
			},
			wantField: "reflectors.r1.attachments.a.addresses",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := reflectorBaseConfig(t)
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if got := errs.Attributes(err)["field"]; got != tc.wantField {
				t.Errorf("field = %v, want %v (error %v)", got, tc.wantField, err)
			}
		})
	}
}

func TestReflectorClone(t *testing.T) {
	cfg := reflectorBaseConfig(t)
	original := cfg.Reflectors["r1"]
	cloned := original.Clone()
	sibling := original.Clone()

	if !cloned.Equal(sibling) {
		t.Error("Equal reports two independent clones of the same reflector as different")
	}

	r1 := cfg.Reflectors["r1"]
	r1.Ports["p1"] = phy.Ethernet{SupportedSpeedsBPS: []uint64{999}}
	a := r1.Attachments["a"]
	a.Addresses[0] = netip.MustParsePrefix("192.0.2.1/24")
	*a.VLAN = 999
	r1.Attachments["a"] = a

	if cloned.Ports["p1"].Canonical() == r1.Ports["p1"].Canonical() {
		t.Error("cloned reflector shares its Ports map with the original")
	}
	if cloned.Attachments["a"].Addresses[0] == a.Addresses[0] {
		t.Error("cloned attachment shares its Addresses slice with the original")
	}
	if *cloned.Attachments["a"].VLAN == 999 {
		t.Error("cloned attachment shares its VLAN pointer with the original")
	}
	if !cloned.Equal(sibling) {
		t.Error("mutating the original through its shared maps changed an unrelated clone")
	}
}

func TestConfigNormalizeAssignsReflectorMACAndNormalizesFacts(t *testing.T) {
	cfg := reflectorBaseConfig(t)
	r1 := cfg.Reflectors["r1"]
	r1.Ports["p1"] = phy.Ethernet{SupportedSpeedsBPS: []uint64{1_000_000_000, 100_000_000, 100_000_000}}
	a := r1.Attachments["a"]
	dup := netip.MustParsePrefix("10.0.10.9/24")
	unsorted := netip.MustParsePrefix("10.0.10.1/24")
	a.Addresses = []netip.Prefix{dup, unsorted, dup}
	r1.Attachments["a"] = a
	cfg.Reflectors["r1"] = r1

	norm := cfg.Normalize()
	got := norm.Reflectors["r1"]

	if got.Address == (netaddr.MAC{}) {
		t.Error("Normalize left the reflector's Address unassigned")
	}
	if speeds := got.Ports["p1"].SupportedSpeedsBPS; !slices.Equal(speeds, []uint64{100_000_000, 1_000_000_000}) {
		t.Errorf("normalized supported speeds = %v, want sorted and deduplicated", speeds)
	}
	wantAddresses := []netip.Prefix{unsorted, dup}
	if addrs := got.Attachments["a"].Addresses; !slices.Equal(addrs, wantAddresses) {
		t.Errorf("normalized attachment addresses = %v, want %v", addrs, wantAddresses)
	}
}

func TestConfigClone(t *testing.T) {
	cfg := twoSwitchBaseConfig(t)
	vid := vlan.ID(10)
	h1 := cfg.Hosts["h1"]
	h1.VLAN = &vid
	gw := netip.MustParseAddr("10.0.10.1")
	h1.IP = &fabric.HostIP{
		Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
		Gateway:   gw,
		Neighbors: map[netip.Addr]netaddr.MAC{
			gw: {0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		},
	}
	cfg.Hosts["h1"] = h1
	cfg.Cables[0].Fault = fabric.Fault{
		Kind:     fabric.FaultLoseSequence,
		Sequence: []uint{1, 2, 3},
	}
	d := 10 * time.Microsecond
	cfg.Cables[0].Delay = &d

	cloned := cfg.Clone()

	// Mutate original and assert clone is unaffected.
	delete(cfg.Switches, "sw1")
	delete(cfg.Hosts, "h1")
	cfg.Cables[0].LengthMeters = 999
	cfg.Cables[0].Fault.Sequence[0] = 999
	*cfg.Cables[0].Delay = 999 * time.Microsecond

	if len(cloned.Switches) != 2 {
		t.Errorf("cloned switches modified: len = %d, want 2", len(cloned.Switches))
	}
	if len(cloned.Hosts) != 2 {
		t.Errorf("cloned hosts modified: len = %d, want 2", len(cloned.Hosts))
	}
	if cloned.Cables[0].LengthMeters == 999 {
		t.Errorf("cloned cable length modified: got 999, want original")
	}
	if cloned.Cables[0].Fault.Sequence[0] != 1 {
		t.Errorf("cloned fault sequence modified: got %d, want 1", cloned.Cables[0].Fault.Sequence[0])
	}
	if cloned.Cables[0].Delay == nil || *cloned.Cables[0].Delay != 10*time.Microsecond {
		t.Errorf("cloned delay modified or nil: got %v, want 10µs", cloned.Cables[0].Delay)
	}
	if cloned.Hosts["h1"].IP == nil || len(cloned.Hosts["h1"].IP.Addresses) != 1 {
		t.Errorf("cloned host IP not cloned properly: %+v", cloned.Hosts["h1"].IP)
	}
}

func TestConfigNormalizeDefinesBehavioralEquivalence(t *testing.T) {
	cfgA := twoSwitchBaseConfig(t)
	cfgB := cfgA.Clone()

	cfgA.Cables[0].Fault = fabric.Fault{}
	cfgB.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultNone}

	cfgA.Cables[1].Fault = fabric.Fault{
		Kind:     fabric.FaultLoseSequence,
		Sequence: []uint{5, 1, 3},
	}
	cfgB.Cables[1].Fault = fabric.Fault{
		Kind:     fabric.FaultLoseSequence,
		Sequence: []uint{1, 3, 5},
	}

	addresses := []netip.Prefix{
		netip.MustParsePrefix("fd00::10/64"),
		netip.MustParsePrefix("10.0.10.10/24"),
	}
	hostA := cfgA.Hosts["h1"]
	hostA.IP = &fabric.HostIP{Addresses: slices.Clone(addresses)}
	cfgA.Hosts["h1"] = hostA
	hostB := cfgB.Hosts["h1"]
	hostB.IP = &fabric.HostIP{Addresses: []netip.Prefix{addresses[1], addresses[0]}}
	cfgB.Hosts["h1"] = hostB

	norm := cfgA.Normalize()
	if got := norm.Cables[0].Fault.Kind; got != fabric.FaultNone {
		t.Errorf("normalized fault kind = %q, want %q", got, fabric.FaultNone)
	}
	var normalizedSequence []uint
	for _, cable := range norm.Cables {
		if cable.Fault.Kind == fabric.FaultLoseSequence {
			normalizedSequence = cable.Fault.Sequence
		}
	}
	if !slices.Equal(normalizedSequence, []uint{1, 3, 5}) {
		t.Errorf("normalized fault sequence = %v, want [1 3 5]", normalizedSequence)
	}
	wantAddresses := []netip.Prefix{addresses[1], addresses[0]}
	if got := norm.Hosts["h1"].IP.Addresses; !slices.Equal(got, wantAddresses) {
		t.Errorf("normalized host addresses = %v, want %v", got, wantAddresses)
	}
	if !cfgA.Equal(cfgB) {
		t.Error("Config.Equal reports behaviorally equivalent configurations as different")
	}
	if changes := fabric.Diff(cfgA, cfgB); len(changes) != 0 {
		t.Errorf("Diff returned %d changes for behaviorally equivalent configurations: %v", len(changes), changes)
	}
}

func TestConfigNormalizeKeepsAnUnspecifiedMedium(t *testing.T) {
	unspecified := twoSwitchBaseConfig(t)
	unspecified.Cables[0].Medium = fabric.MediumUnspecified
	twistedPair := unspecified.Clone()
	twistedPair.Cables[0].Medium = fabric.TwistedPair

	if got := unspecified.Normalize().Cables[0].Medium; got != fabric.MediumUnspecified {
		t.Errorf("normalized medium = %q, want unspecified", got)
	}
	if unspecified.Equal(twistedPair) {
		t.Error("Config.Equal reports an unspecified medium equal to twisted pair")
	}
}

func TestConfigNormalizeClearsInactiveFaultParametersAndNegativeZero(t *testing.T) {
	cfg := twoSwitchBaseConfig(t)
	cfg.Cables[0].LengthMeters = math.Copysign(0, -1)
	cfg.Cables[0].Fault = fabric.Fault{
		Kind:     fabric.FaultCut,
		N:        7,
		Sequence: []uint{3, 1},
	}
	cfg.Cables[1].Fault = fabric.Fault{
		Kind:     fabric.FaultLoseEveryNth,
		N:        3,
		Sequence: []uint{4, 2},
	}

	norm := cfg.Normalize()
	for _, cable := range norm.Cables {
		if math.Signbit(cable.LengthMeters) && cable.LengthMeters == 0 {
			t.Error("normalized cable retained negative zero length")
		}
		switch cable.Fault.Kind {
		case fabric.FaultCut:
			if cable.Fault.N != 0 || cable.Fault.Sequence != nil {
				t.Errorf("normalized Cut fault retained inactive parameters: %+v", cable.Fault)
			}
		case fabric.FaultLoseEveryNth:
			if cable.Fault.N != 3 || cable.Fault.Sequence != nil {
				t.Errorf("normalized LoseEveryNth fault = %+v, want N only", cable.Fault)
			}
		}
	}
}

func TestCableLengthEqualityAndDiffAreReflexive(t *testing.T) {
	tests := []struct {
		name   string
		length float64
	}{
		{name: "zero", length: 0},
		{name: "negative zero", length: math.Copysign(0, -1)},
		{name: "maximum finite", length: math.MaxFloat64},
		{name: "NaN", length: math.NaN()},
		{name: "positive infinity", length: math.Inf(1)},
		{name: "negative infinity", length: math.Inf(-1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cable := fabric.Cable{LengthMeters: tt.length}
			if !cable.Equal(cable.Clone()) {
				t.Error("Cable.Equal is not reflexive")
			}

			cfg := fabric.Config{Cables: []fabric.Cable{cable}}
			if !cfg.Equal(cfg.Clone()) {
				t.Error("Config.Equal is not reflexive")
			}
			if changes := fabric.Diff(cfg, cfg.Clone()); len(changes) != 0 {
				t.Errorf("Diff(config, config) = %+v, want no changes", changes)
			}
		})
	}

	negativeZero := fabric.Cable{LengthMeters: math.Copysign(0, -1)}
	positiveZero := fabric.Cable{LengthMeters: 0}
	if !negativeZero.Equal(positiveZero) {
		t.Error("negative and positive zero lengths are not semantically equal")
	}
}

func TestConfigNormalizeCanonicalizesCableOrientation(t *testing.T) {
	cfg := twoSwitchBaseConfig(t)
	cable := cfg.Cables[1]
	cable.A, cable.B = cable.B, cable.A
	cable.Fault.Kind = fabric.FaultDeadBToA
	cfg.Cables = []fabric.Cable{cable}

	norm := cfg.Normalize()
	if got := norm.Cables[0]; got.A != cable.B || got.B != cable.A {
		t.Fatalf("normalized endpoints = %v -> %v, want %v -> %v", got.A, got.B, cable.B, cable.A)
	}
	if got := norm.Cables[0].Fault.Kind; got != fabric.FaultDeadAToB {
		t.Errorf("normalized directional fault = %s, want %s", got, fabric.FaultDeadAToB)
	}

	equivalent := cfg.Clone()
	equivalent.Cables[0].A, equivalent.Cables[0].B = equivalent.Cables[0].B, equivalent.Cables[0].A
	equivalent.Cables[0].Fault.Kind = fabric.FaultDeadAToB
	if !cfg.Equal(equivalent) {
		t.Error("reversing endpoints and the directional fault changed config equality")
	}
}

func TestCableDiffKeysAreInjectiveForDelimiterHeavyEndpoints(t *testing.T) {
	cfg := fabric.Config{Cables: []fabric.Cable{
		{
			A: fabric.Endpoint{Node: "left-right", Port: "port:one%"},
			B: fabric.Endpoint{Node: "peer", Port: "two-three"},
		},
		{
			A: fabric.Endpoint{Node: "left", Port: "right-port:one%"},
			B: fabric.Endpoint{Node: "peer-two", Port: "three"},
		},
	}}
	changes := fabric.Diff(fabric.Config{}, cfg)
	keys := make(map[string]struct{})
	for _, change := range changes {
		if change.Subject.Kind == "cable" {
			keys[change.Subject.Key] = struct{}{}
		}
	}
	if len(keys) != 2 {
		t.Errorf("delimiter-heavy cables produced %d distinct diff keys, want 2: %+v", len(keys), changes)
	}
}

func TestNestedSwitchDiffKeysAreInjective(t *testing.T) {
	ports := func(name string, down bool) port.Table {
		status := port.Up
		if down {
			status = port.Down
		}
		builder := port.NewBuilder()
		builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: status, OperStatus: port.Up})
		table, err := builder.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		return table
	}

	a := fabric.Config{Switches: map[string]vswitch.Config{
		"edge/a": {Ports: ports("b", false)},
		"edge":   {Ports: ports("a/b", false)},
	}}
	b := fabric.Config{Switches: map[string]vswitch.Config{
		"edge/a": {Ports: ports("b", true)},
		"edge":   {Ports: ports("a/b", true)},
	}}

	keys := make(map[string]struct{})
	for _, change := range fabric.Diff(a, b) {
		if change.Subject.Kind == "port" && change.Field == "admin_status" {
			keys[change.Subject.Key] = struct{}{}
		}
	}
	for _, key := range []string{"edge%2Fa/b", "edge/a%2Fb"} {
		if _, ok := keys[key]; !ok {
			t.Errorf("nested switch diff keys = %v, want injective key %q", keys, key)
		}
	}
}

func TestHostWithOffLinkGatewayHasNoDefaultRoute(t *testing.T) {
	t.Parallel()

	h := fabric.Host{
		Address: netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		IP: &fabric.HostIP{
			Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
			Gateway:   netip.MustParseAddr("10.0.20.1"),
		},
	}

	rtCfg, tbl := fabric.HostRoutingConfig("h1", h)
	stack, err := routing.New(rtCfg, tbl, "h1")
	if err != nil {
		t.Fatalf("routing.New for a host with an off-link gateway: %v", err)
	}

	withdrawn := stack.WithdrawnRoutes(routing.DefaultVRF)
	if len(withdrawn) != 1 || withdrawn[0].Prefix != netip.MustParsePrefix("0.0.0.0/0") {
		t.Fatalf("withdrawn = %+v, want the default route alone", withdrawn)
	}
	if withdrawn[0].Reason != routing.WithdrawnUnresolved {
		t.Errorf("reason = %q, want %q", withdrawn[0].Reason, routing.WithdrawnUnresolved)
	}

	res := stack.Originate(fixedTime, routing.DefaultVRF, netip.MustParseAddr("192.0.2.9"), 17, []byte("payload"), true)
	if res.Reason != routing.ReasonNoRoute {
		t.Errorf("off-link send reason = %q, want %q", res.Reason, routing.ReasonNoRoute)
	}
}
