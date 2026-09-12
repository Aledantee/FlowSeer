package routing_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
)

func newTestPortTable(t *testing.T) port.Table {
	t.Helper()
	tbl, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical}).
		Add(port.Port{Name: "lag1", Kind: port.Lag}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, LagParent: "lag1"}).
		Build()
	if err != nil {
		t.Fatalf("port.NewBuilder: %v", err)
	}
	return tbl
}

func validBaseConfig() routing.Config {
	return routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN:     10,
						MAC:      netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
					},
					"1/1/1": {
						Port:     "1/1/1",
						MAC:      netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x02},
						Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")},
					},
				},
				Routes: []routing.Route{
					{
						Prefix:    netip.MustParsePrefix("10.0.30.0/24"),
						NextHop:   netip.MustParseAddr("10.0.10.254"),
						Interface: "vlan10",
					},
				},
				Neighbors: []routing.Neighbor{
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.10.7"),
						MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77},
					},
				},
			},
		},
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	ports := newTestPortTable(t)

	tests := []struct {
		name    string
		mutate  func(*routing.Config)
		wantErr bool
	}{
		{
			name:    "valid configuration",
			mutate:  func(_ *routing.Config) {},
			wantErr: false,
		},
		{
			name: "empty VRF name",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				delete(c.VRFs, routing.DefaultVRF)
				c.VRFs[""] = vrf
			},
			wantErr: true,
		},
		{
			name: "VRF with no interfaces",
			mutate: func(c *routing.Config) {
				c.VRFs["empty"] = routing.VRF{
					Interfaces: map[string]routing.Interface{},
				}
			},
			wantErr: true,
		},
		{
			name: "empty interface name",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Interfaces[""] = routing.Interface{
					VLAN:     30,
					Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")},
				}
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "interface with neither VLAN nor Port",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Interfaces["unbound"] = routing.Interface{
					Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")},
				}
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "interface with both VLAN and Port",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Interfaces["hybrid"] = routing.Interface{
					VLAN:     30,
					Port:     "1/1/2",
					Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.30.1/24")},
				}
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "duplicate VLAN across VRFs",
			mutate: func(c *routing.Config) {
				c.VRFs["tenant"] = routing.VRF{
					Interfaces: map[string]routing.Interface{
						"vlan10_duplicate": {
							VLAN:     10,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.1/24")},
						},
					},
				}
			},
			wantErr: true,
		},
		{
			name: "duplicate port across VRFs",
			mutate: func(c *routing.Config) {
				c.VRFs["tenant"] = routing.VRF{
					Interfaces: map[string]routing.Interface{
						"port_duplicate": {
							Port:     "1/1/1",
							Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.1/24")},
						},
					},
				}
			},
			wantErr: true,
		},
		{
			name: "routed port absent from port table",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Interfaces["missing"] = routing.Interface{
					Port:     "1/1/99",
					Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.99.1/24")},
				}
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "LAG member configured as routed port",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Interfaces["member"] = routing.Interface{
					Port:     "1/1/3",
					Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.3.1/24")},
				}
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "interface with group MAC",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				iface := vrf.Interfaces["vlan10"]
				iface.MAC = netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x01}
				vrf.Interfaces["vlan10"] = iface
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "neighbor with group MAC",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Neighbors = append(vrf.Neighbors, routing.Neighbor{
					Interface: "vlan10",
					Addr:      netip.MustParseAddr("10.0.10.8"),
					MAC:       netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0x02},
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "route prefix not masked",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:    netip.MustParsePrefix("10.0.20.5/24"),
					NextHop:   netip.MustParseAddr("10.0.10.254"),
					Interface: "vlan10",
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "interface address prefix length 0",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				iface := vrf.Interfaces["vlan10"]
				iface.Prefixes = append(iface.Prefixes, netip.MustParsePrefix("0.0.0.0/0"))
				vrf.Interfaces["vlan10"] = iface
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "route naming neither next hop nor interface",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix: netip.MustParsePrefix("10.0.40.0/24"),
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "route naming interface outside VRF",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:    netip.MustParsePrefix("10.0.40.0/24"),
					Interface: "nonexistent",
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "neighbor naming interface outside VRF",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Neighbors = append(vrf.Neighbors, routing.Neighbor{
					Interface: "nonexistent",
					Addr:      netip.MustParseAddr("10.0.10.99"),
					MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99},
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "route with next hop unreachable by any interface prefix in VRF",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:  netip.MustParsePrefix("10.0.40.0/24"),
					NextHop: netip.MustParseAddr("172.16.1.1"),
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "neighbor address family mismatching all interface prefixes",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Neighbors = append(vrf.Neighbors, routing.Neighbor{
					Interface: "vlan10",
					Addr:      netip.MustParseAddr("2001:db8::1"),
					MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x88},
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "duplicate neighbor key within VRF",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Neighbors = append(vrf.Neighbors, routing.Neighbor{
					Interface: "vlan10",
					Addr:      netip.MustParseAddr("10.0.10.7"),
					MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99},
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "duplicate interface name across VRFs",
			mutate: func(c *routing.Config) {
				c.VRFs["tenant"] = routing.VRF{
					Interfaces: map[string]routing.Interface{
						"vlan10": {
							VLAN:     30,
							Prefixes: []netip.Prefix{netip.MustParsePrefix("192.168.1.1/24")},
						},
					},
				}
			},
			wantErr: true,
		},
		{
			name: "duplicate route prefix within VRF",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:    netip.MustParsePrefix("10.0.30.0/24"),
					NextHop:   netip.MustParseAddr("10.0.10.253"),
					Interface: "vlan10",
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "IPv4-mapped interface prefix",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				iface := vrf.Interfaces["vlan10"]
				iface.Prefixes = append(iface.Prefixes, netip.MustParsePrefix("::ffff:10.0.10.1/120"))
				vrf.Interfaces["vlan10"] = iface
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "IPv4-mapped neighbor address",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				iface := vrf.Interfaces["vlan10"]
				iface.Prefixes = append(iface.Prefixes, netip.MustParsePrefix("2001:db8:10::1/64"))
				vrf.Interfaces["vlan10"] = iface
				vrf.Neighbors = append(vrf.Neighbors, routing.Neighbor{
					Interface: "vlan10",
					Addr:      netip.MustParseAddr("::ffff:10.0.10.9"),
					MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99},
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "IPv4-mapped next hop",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:    netip.MustParsePrefix("10.0.40.0/24"),
					NextHop:   netip.MustParseAddr("::ffff:10.0.10.254"),
					Interface: "vlan10",
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := validBaseConfig()
			tt.mutate(&cfg)
			err := cfg.Validate(ports)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestClone(t *testing.T) {
	t.Parallel()
	cfg := validBaseConfig()
	cloned := cfg.Clone()

	// Modify original configuration.
	vrf := cfg.VRFs[routing.DefaultVRF]
	iface := vrf.Interfaces["vlan10"]
	iface.Prefixes = append(iface.Prefixes, netip.MustParsePrefix("10.0.100.1/24"))
	vrf.Interfaces["vlan10"] = iface
	cfg.VRFs[routing.DefaultVRF] = vrf

	clonedVRF := cloned.VRFs[routing.DefaultVRF]
	if len(clonedVRF.Interfaces["vlan10"].Prefixes) != 1 {
		t.Fatalf("cloned prefixes modified: got %d, want 1", len(clonedVRF.Interfaces["vlan10"].Prefixes))
	}
}

func TestNormalize(t *testing.T) {
	t.Parallel()
	cfg := routing.Config{
		VRFs: map[string]routing.VRF{
			routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"vlan10": {
						VLAN: 10,
						Prefixes: []netip.Prefix{
							netip.MustParsePrefix("10.0.20.1/24"),
							netip.MustParsePrefix("10.0.10.1/24"),
						},
					},
				},
				Routes: []routing.Route{
					{
						Prefix:    netip.MustParsePrefix("10.0.30.5/24"),
						NextHop:   netip.MustParseAddr("10.0.10.254"),
						Interface: "vlan10",
					},
					{
						Prefix:    netip.MustParsePrefix("10.0.10.0/24"),
						NextHop:   netip.MustParseAddr("10.0.10.1"),
						Interface: "vlan10",
					},
				},
				Neighbors: []routing.Neighbor{
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.10.9"),
						MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99},
					},
					{
						Interface: "vlan10",
						Addr:      netip.MustParseAddr("10.0.10.7"),
						MAC:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x77},
					},
				},
			},
		},
	}

	norm := cfg.Normalize()
	vrf := norm.VRFs[routing.DefaultVRF]

	// Check interface prefixes sorted
	iface := vrf.Interfaces["vlan10"]
	if len(iface.Prefixes) != 2 || iface.Prefixes[0].String() != "10.0.10.1/24" || iface.Prefixes[1].String() != "10.0.20.1/24" {
		t.Fatalf("prefixes not sorted: %v", iface.Prefixes)
	}

	// Check route prefix masked and routes sorted
	if len(vrf.Routes) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(vrf.Routes))
	}
	if vrf.Routes[0].Prefix.String() != "10.0.10.0/24" {
		t.Errorf("route 0 prefix: got %s, want 10.0.10.0/24", vrf.Routes[0].Prefix)
	}
	if vrf.Routes[1].Prefix.String() != "10.0.30.0/24" {
		t.Errorf("route 1 prefix not masked or sorted: got %s, want 10.0.30.0/24", vrf.Routes[1].Prefix)
	}

	// Check neighbors sorted
	if len(vrf.Neighbors) != 2 {
		t.Fatalf("expected 2 neighbors, got %d", len(vrf.Neighbors))
	}
	if vrf.Neighbors[0].Addr.String() != "10.0.10.7" {
		t.Errorf("neighbor 0: got %s, want 10.0.10.7", vrf.Neighbors[0].Addr)
	}
	if vrf.Neighbors[1].Addr.String() != "10.0.10.9" {
		t.Errorf("neighbor 1: got %s, want 10.0.10.9", vrf.Neighbors[1].Addr)
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()

	t.Run("VRF added and removed", func(t *testing.T) {
		t.Parallel()
		a := routing.Config{
			VRFs: map[string]routing.VRF{
				"vrf1": {
					Interfaces: map[string]routing.Interface{
						"vlan10": {VLAN: 10},
					},
				},
			},
		}
		b := routing.Config{
			VRFs: map[string]routing.VRF{
				"vrf2": {
					Interfaces: map[string]routing.Interface{
						"vlan20": {VLAN: 20},
					},
				},
			},
		}

		changes := routing.Diff(a, b)
		if len(changes) != 2 {
			t.Fatalf("expected 2 changes, got %d: %+v", len(changes), changes)
		}
		if changes[0].Subject.Kind != "vrf" || changes[0].Subject.Key != "vrf1" || changes[0].To != nil {
			t.Errorf("change 0 unexpected: %+v", changes[0])
		}
		if changes[1].Subject.Kind != "vrf" || changes[1].Subject.Key != "vrf2" || changes[1].From != nil {
			t.Errorf("change 1 unexpected: %+v", changes[1])
		}
	})

	t.Run("interface changing shape from VLAN to port", func(t *testing.T) {
		t.Parallel()
		a := routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"eth1": {
							VLAN: 10,
						},
					},
				},
			},
		}
		b := routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"eth1": {
							Port: "1/1/1",
						},
					},
				},
			},
		}

		changes := routing.Diff(a, b)
		if len(changes) != 2 {
			t.Fatalf("expected 2 changes, got %d: %+v", len(changes), changes)
		}
		if changes[0].Field != "vlan" || changes[0].From != routing.VLANFact(10) || changes[0].To != routing.VLANFact(0) {
			t.Errorf("expected vlan change, got %+v", changes[0])
		}
		if changes[1].Field != "port" || changes[1].From != routing.PortFact("") || changes[1].To != routing.PortFact("1/1/1") {
			t.Errorf("expected port change, got %+v", changes[1])
		}
	})

	t.Run("all field changes in interface route and neighbor", func(t *testing.T) {
		t.Parallel()
		mac1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
		mac2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x22}
		pfx1 := netip.MustParsePrefix("10.0.10.1/24")
		pfx2 := netip.MustParsePrefix("10.0.20.1/24")

		a := routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"if1": {
							VLAN:     10,
							MAC:      mac1,
							Prefixes: []netip.Prefix{pfx1},
						},
					},
					Routes: []routing.Route{
						{
							Prefix:    netip.MustParsePrefix("10.0.30.0/24"),
							NextHop:   netip.MustParseAddr("10.0.10.254"),
							Interface: "if1",
						},
					},
					Neighbors: []routing.Neighbor{
						{
							Interface: "if1",
							Addr:      netip.MustParseAddr("10.0.10.7"),
							MAC:       mac1,
						},
					},
				},
			},
		}

		b := routing.Config{
			VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{
						"if1": {
							VLAN:     20,
							MAC:      mac2,
							Prefixes: []netip.Prefix{pfx2},
						},
					},
					Routes: []routing.Route{
						{
							Prefix:    netip.MustParsePrefix("10.0.30.0/24"),
							NextHop:   netip.MustParseAddr("10.0.10.253"),
							Interface: "if2",
						},
					},
					Neighbors: []routing.Neighbor{
						{
							Interface: "if1",
							Addr:      netip.MustParseAddr("10.0.10.7"),
							MAC:       mac2,
						},
					},
				},
			},
		}

		changes := routing.Diff(a, b)
		expectedFields := []string{"vlan", "mac", "prefixes", "next_hop", "interface", "mac"}
		if len(changes) != len(expectedFields) {
			t.Fatalf("expected %d changes, got %d: %+v", len(expectedFields), len(changes), changes)
		}
		for i, field := range expectedFields {
			if changes[i].Field != field {
				t.Errorf("change %d field: got %q, want %q", i, changes[i].Field, field)
			}
			if changes[i].Layer != port.LayerRouting {
				t.Errorf("change %d layer: got %q, want %q", i, changes[i].Layer, port.LayerRouting)
			}
		}
	})
}
