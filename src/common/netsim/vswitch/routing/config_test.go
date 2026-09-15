package routing_test

import (
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
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

func TestNewRejectsZeroStaticNeighborMACAtNeighborField(t *testing.T) {
	t.Parallel()

	cfg := validBaseConfig()
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Neighbors[0].MAC = netaddr.MAC{}
	cfg.VRFs[routing.DefaultVRF] = vrf

	_, err := routing.New(cfg, newTestPortTable(t), "sw1")
	if err == nil {
		t.Fatal("routing.New accepted a zero static neighbor MAC")
	}
	if got, want := errs.Attributes(err)["field"], "vrfs.default.neighbors.vlan10/10.0.10.7.mac"; got != want {
		t.Errorf("field = %v, want %q", got, want)
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
			wantErr: false,
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
			// The route is withdrawn when the table is built; the configuration stays valid.
			name: "route with next hop unreachable by any interface prefix in VRF",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:  netip.MustParsePrefix("10.0.40.0/24"),
					NextHop: netip.MustParseAddr("172.16.1.1"),
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: false,
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
			name: "second route on a prefix, differing in next hop",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:    netip.MustParsePrefix("10.0.30.0/24"),
					NextHop:   netip.MustParseAddr("10.0.10.253"),
					Interface: "vlan10",
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: false,
		},
		{
			name: "route repeated in every field within VRF",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, vrf.Routes[0])
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
		{
			name: "IPv6 next hop on an IPv4 prefix",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:  netip.MustParsePrefix("10.0.40.0/24"),
					NextHop: netip.MustParseAddr("2001:db8::1"),
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "IPv4 next hop on an IPv6 prefix",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:  netip.MustParsePrefix("2001:db8:1::/48"),
					NextHop: netip.MustParseAddr("10.0.10.254"),
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "unspecified next hop",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:  netip.MustParsePrefix("10.0.40.0/24"),
					NextHop: netip.MustParseAddr("0.0.0.0"),
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			name: "multicast next hop",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:  netip.MustParsePrefix("10.0.40.0/24"),
					NextHop: netip.MustParseAddr("224.0.0.5"),
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: true,
		},
		{
			// The families compared are the route's own, not those the egress interface carries.
			name: "IPv6 route on an interface that also carries IPv4",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				iface := vrf.Interfaces["vlan10"]
				iface.Prefixes = append(iface.Prefixes, netip.MustParsePrefix("2001:db8::1/64"))
				vrf.Interfaces["vlan10"] = iface
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:    netip.MustParsePrefix("2001:db8:1::/48"),
					NextHop:   netip.MustParseAddr("2001:db8::254"),
					Interface: "vlan10",
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: false,
		},
		{
			name: "route with no next hop naming an interface in the VRF",
			mutate: func(c *routing.Config) {
				vrf := c.VRFs[routing.DefaultVRF]
				vrf.Routes = append(vrf.Routes, routing.Route{
					Prefix:    netip.MustParsePrefix("10.0.40.0/24"),
					Interface: "vlan10",
				})
				c.VRFs[routing.DefaultVRF] = vrf
			},
			wantErr: false,
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

func TestValidateRejectsCrossFamilyNextHopAtNextHopField(t *testing.T) {
	t.Parallel()

	cfg := validBaseConfig()
	vrf := cfg.VRFs[routing.DefaultVRF]
	vrf.Routes = append(vrf.Routes, routing.Route{
		Prefix:  netip.MustParsePrefix("10.0.0.0/8"),
		NextHop: netip.MustParseAddr("2001:db8::1"),
	})
	cfg.VRFs[routing.DefaultVRF] = vrf

	err := cfg.Validate(newTestPortTable(t))
	if err == nil {
		t.Fatal("Validate accepted an IPv6 next hop on an IPv4 prefix")
	}
	if got, want := errs.Attributes(err)["field"], "vrfs.default.routes.10.0.0.0/8.next_hop"; got != want {
		t.Errorf("field = %v, want %q", got, want)
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
						Metric:    20,
					},
					{
						Prefix:    netip.MustParsePrefix("10.0.30.0/24"),
						NextHop:   netip.MustParseAddr("10.0.10.253"),
						Interface: "vlan10",
						Metric:    20,
					},
					{
						Prefix:     netip.MustParsePrefix("10.0.30.0/24"),
						NextHop:    netip.MustParseAddr("10.0.10.252"),
						Interface:  "vlan10",
						Preference: 5,
					},
					{
						Prefix:    netip.MustParsePrefix("10.0.30.0/24"),
						NextHop:   netip.MustParseAddr("10.0.10.251"),
						Interface: "vlan10",
						Metric:    10,
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

	// Check route prefixes masked, preferences floored, and routes sorted by the total order.
	wantRoutes := []routing.Route{
		{Prefix: netip.MustParsePrefix("10.0.10.0/24"), NextHop: netip.MustParseAddr("10.0.10.1"), Interface: "vlan10", Preference: 1},
		{Prefix: netip.MustParsePrefix("10.0.30.0/24"), NextHop: netip.MustParseAddr("10.0.10.251"), Interface: "vlan10", Preference: 1, Metric: 10},
		{Prefix: netip.MustParsePrefix("10.0.30.0/24"), NextHop: netip.MustParseAddr("10.0.10.253"), Interface: "vlan10", Preference: 1, Metric: 20},
		{Prefix: netip.MustParsePrefix("10.0.30.0/24"), NextHop: netip.MustParseAddr("10.0.10.254"), Interface: "vlan10", Preference: 1, Metric: 20},
		{Prefix: netip.MustParsePrefix("10.0.30.0/24"), NextHop: netip.MustParseAddr("10.0.10.252"), Interface: "vlan10", Preference: 5},
	}
	if len(vrf.Routes) != len(wantRoutes) {
		t.Fatalf("expected %d routes, got %d", len(wantRoutes), len(vrf.Routes))
	}
	for i, want := range wantRoutes {
		if vrf.Routes[i] != want {
			t.Errorf("route %d = %+v, want %+v", i, vrf.Routes[i], want)
		}
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
							Prefix:     netip.MustParsePrefix("10.0.30.0/24"),
							NextHop:    netip.MustParseAddr("10.0.10.254"),
							Interface:  "if1",
							Preference: 20,
							Metric:     5,
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
		expectedFields := []string{"vlan", "mac", "prefixes", "preference", "metric", "mac"}
		wantTypeIDs := map[string]string{
			"preference": "routing.route.preference",
			"metric":     "routing.route.metric",
		}
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
			wantTypeID, ok := wantTypeIDs[changes[i].Field]
			if !ok {
				continue
			}
			if changes[i].From.TypeID() != wantTypeID {
				t.Errorf("%s change From.TypeID() = %q, want %q", changes[i].Field, changes[i].From.TypeID(), wantTypeID)
			}
			if changes[i].To.TypeID() != wantTypeID {
				t.Errorf("%s change To.TypeID() = %q, want %q", changes[i].Field, changes[i].To.TypeID(), wantTypeID)
			}
		}
	})

	t.Run("preference and metric are field changes and an added next hop is an added route", func(t *testing.T) {
		t.Parallel()
		prefix := netip.MustParsePrefix("10.0.30.0/24")
		configWith := func(routes ...routing.Route) routing.Config {
			return routing.Config{VRFs: map[string]routing.VRF{
				routing.DefaultVRF: {
					Interfaces: map[string]routing.Interface{"if1": {VLAN: 10}},
					Routes:     routes,
				},
			}}
		}
		base := routing.Route{Prefix: prefix, NextHop: netip.MustParseAddr("10.0.10.254"), Interface: "if1", Preference: 1, Metric: 10}
		second := routing.Route{Prefix: prefix, NextHop: netip.MustParseAddr("10.0.10.253"), Interface: "if1", Preference: 1, Metric: 10}

		retunedMetric := base
		retunedMetric.Metric = 20
		retunedPreference := base
		retunedPreference.Preference = 250
		unsetPreference := base
		unsetPreference.Preference = 0

		cases := []struct {
			name      string
			a, b      routing.Config
			wantField string
		}{
			{name: "metric retuned", a: configWith(base), b: configWith(retunedMetric), wantField: "metric"},
			{name: "preference retuned", a: configWith(base), b: configWith(retunedPreference), wantField: "preference"},
			{name: "second next hop added", a: configWith(base), b: configWith(base, second), wantField: ""},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				changes := routing.Diff(tc.a, tc.b)
				if len(changes) != 1 {
					t.Fatalf("changes = %+v, want exactly one", changes)
				}
				if changes[0].Field != tc.wantField {
					t.Errorf("field = %q, want %q", changes[0].Field, tc.wantField)
				}
				if tc.wantField == "" && (changes[0].From != nil || changes[0].To == nil) {
					t.Errorf("change = %+v, want an added route", changes[0])
				}
			})
		}

		// Go cannot tell an unset Preference from an explicit 0, and normalization raises both
		// to 1, so neither reads as a change against a route configured at 1.
		t.Run("preference 0 against an explicit 1", func(t *testing.T) {
			t.Parallel()
			if changes := routing.Diff(configWith(unsetPreference), configWith(base)); len(changes) != 0 {
				t.Errorf("changes = %+v, want none", changes)
			}
		})
	})
}

func TestRouteCanonicalDistinguishesPreference(t *testing.T) {
	t.Parallel()

	prefix := netip.MustParsePrefix("10.0.30.0/24")
	nextHop := netip.MustParseAddr("10.0.10.254")
	a := routing.Route{Prefix: prefix, NextHop: nextHop, Interface: "if1", Preference: 1}
	b := routing.Route{Prefix: prefix, NextHop: nextHop, Interface: "if1", Preference: 250}
	if a.Canonical() == b.Canonical() {
		t.Errorf("routes differing only in preference share canonical form %q", a.Canonical())
	}

	c := a
	c.Metric = 10
	if a.Canonical() == c.Canonical() {
		t.Errorf("routes differing only in metric share canonical form %q", a.Canonical())
	}
}

func TestDiffCompositeSubjectKeysAreInjective(t *testing.T) {
	t.Parallel()

	pairs := []struct {
		vrf   string
		iface string
	}{
		{vrf: "blue/a", iface: `b/"edge\one`},
		{vrf: "blue", iface: `a/b/"edge\one`},
	}
	assertKeys := func(t *testing.T, changes []trace.Change, want []string) {
		t.Helper()
		if len(changes) != len(want) {
			t.Fatalf("changes = %+v, want %d", changes, len(want))
		}
		got := make(map[string]struct{}, len(changes))
		for _, change := range changes {
			got[change.Subject.Key] = struct{}{}
		}
		if len(got) != len(want) {
			t.Fatalf("subject keys = %v, want %d unique keys", got, len(want))
		}
		for _, key := range want {
			if _, ok := got[key]; !ok {
				t.Errorf("subject keys = %v, want %q", got, key)
			}
		}
	}

	t.Run("interfaces", func(t *testing.T) {
		a := routing.Config{VRFs: make(map[string]routing.VRF)}
		b := routing.Config{VRFs: make(map[string]routing.VRF)}
		var want []string
		for i, pair := range pairs {
			a.VRFs[pair.vrf] = routing.VRF{Interfaces: map[string]routing.Interface{
				pair.iface: {VLAN: vlan.ID(10 + i)},
			}}
			b.VRFs[pair.vrf] = routing.VRF{Interfaces: map[string]routing.Interface{
				pair.iface: {VLAN: vlan.ID(20 + i)},
			}}
			want = append(want, fmt.Sprintf("%q/%q", pair.vrf, pair.iface))
		}
		assertKeys(t, routing.Diff(a, b), want)
	})

	t.Run("neighbors", func(t *testing.T) {
		addr := netip.MustParseAddr("10.0.0.7")
		a := routing.Config{VRFs: make(map[string]routing.VRF)}
		b := routing.Config{VRFs: make(map[string]routing.VRF)}
		var want []string
		for _, pair := range pairs {
			a.VRFs[pair.vrf] = routing.VRF{Neighbors: []routing.Neighbor{{
				Interface: pair.iface, Addr: addr, MAC: netaddr.MAC{2},
			}}}
			b.VRFs[pair.vrf] = routing.VRF{Neighbors: []routing.Neighbor{{
				Interface: pair.iface, Addr: addr, MAC: netaddr.MAC{4},
			}}}
			want = append(want, fmt.Sprintf("%q/%q/%q", pair.vrf, pair.iface, addr.String()))
		}
		assertKeys(t, routing.Diff(a, b), want)
	})

	t.Run("routes", func(t *testing.T) {
		prefix := netip.MustParsePrefix("10.0.0.0/24")
		nextHop := netip.MustParseAddr("10.0.0.1")
		a := routing.Config{VRFs: make(map[string]routing.VRF)}
		b := routing.Config{VRFs: make(map[string]routing.VRF)}
		var want []string
		for _, pair := range pairs {
			a.VRFs[pair.vrf] = routing.VRF{Routes: []routing.Route{{
				Prefix: prefix, NextHop: nextHop, Interface: pair.iface, Metric: 10,
			}}}
			b.VRFs[pair.vrf] = routing.VRF{Routes: []routing.Route{{
				Prefix: prefix, NextHop: nextHop, Interface: pair.iface, Metric: 20,
			}}}
			want = append(want, fmt.Sprintf("%q/%q/%q/%q", pair.vrf, prefix.String(), nextHop.String(), pair.iface))
		}
		assertKeys(t, routing.Diff(a, b), want)
	})
}

func TestRoutingSnapshotFactsAreLosslessAndImmutable(t *testing.T) {
	t.Parallel()

	prefixesA := []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}
	prefixesB := []netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}
	configFor := func(prefixes []netip.Prefix) routing.Config {
		return routing.Config{VRFs: map[string]routing.VRF{
			"default": {Interfaces: map[string]routing.Interface{
				"vlan10": {VLAN: 10, Prefixes: prefixes},
			}},
		}}
	}

	factA := routing.Diff(routing.Config{}, configFor(prefixesA))[0].To
	factB := routing.Diff(routing.Config{}, configFor(prefixesB))[0].To
	if factA.TypeID() != "routing.vrf" {
		t.Errorf("TypeID() = %q, want routing.vrf", factA.TypeID())
	}
	if factA.Canonical() == factB.Canonical() {
		t.Errorf("different VRFs share canonical form %q", factA.Canonical())
	}
	before := factA.Canonical()
	prefixesA[0] = netip.MustParsePrefix("192.0.2.1/24")
	if got := factA.Canonical(); got != before {
		t.Errorf("VRF fact changed after source mutation: got %q, want %q", got, before)
	}

	base := routing.Config{VRFs: map[string]routing.VRF{"default": {Interfaces: map[string]routing.Interface{}}}}
	interfaceFactA := routing.Diff(base, configFor([]netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}))[0].To
	interfaceFactB := routing.Diff(base, configFor([]netip.Prefix{netip.MustParsePrefix("10.0.20.1/24")}))[0].To
	if interfaceFactA.TypeID() != "routing.interface" {
		t.Errorf("TypeID() = %q, want routing.interface", interfaceFactA.TypeID())
	}
	if interfaceFactA.Canonical() == interfaceFactB.Canonical() {
		t.Errorf("different interfaces share canonical form %q", interfaceFactA.Canonical())
	}
}

func TestFactTypeIDsUnique(t *testing.T) {
	t.Parallel()

	facts := []trace.Fact{
		routing.Route{},
		routing.Neighbor{},
		routing.VLANFact(0),
		routing.PortFact(""),
		routing.MACFact{},
		routing.PrefixesFact(nil),
		routing.AddrFact{},
		routing.RouteInterfaceFact(""),
		routing.RoutePreferenceFact(0),
		routing.RouteMetricFact(0),
	}

	seen := make(map[string]string)
	for _, f := range facts {
		tid := f.TypeID()
		if tid == "" {
			t.Errorf("fact %T has empty TypeID", f)
		}
		if !strings.HasPrefix(tid, "routing.") {
			t.Errorf("fact %T TypeID %q must be prefixed with 'routing.'", f, tid)
		}
		if prev, ok := seen[tid]; ok {
			t.Errorf("duplicate TypeID %q shared by %s and %T", tid, prev, f)
		}
		seen[tid] = fmt.Sprintf("%T", f)
	}
}
