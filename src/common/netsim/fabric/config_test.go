package fabric_test

import (
	"net/netip"
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
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
		Start:    cfg.Start,
		Switches: switches,
		Hosts:    cfg.Hosts,
		Cables:   cfg.Cables,
	}
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
			name: "host with IP stack gateway unreachable",
			mutate: func(c *fabric.Config) {
				h1 := c.Hosts["h1"]
				h1.IP = &fabric.HostIP{
					Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24")},
					Gateway:   netip.MustParseAddr("10.0.20.1"),
				}
				c.Hosts["h1"] = h1
			},
			wantError: true,
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

	cfgA.Cables[0].Medium = ""
	cfgA.Cables[0].Fault = fabric.Fault{}
	cfgB.Cables[0].Medium = fabric.TwistedPair
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
	if got := norm.Cables[0].Medium; got != fabric.TwistedPair {
		t.Errorf("normalized medium = %q, want %q", got, fabric.TwistedPair)
	}
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
