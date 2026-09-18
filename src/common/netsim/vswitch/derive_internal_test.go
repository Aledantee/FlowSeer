package vswitch

import (
	"math/rand/v2"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/mcast"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/routing"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// TestDeriveKeepsOneGateEntryPerScope is evidence that Derive's install of the
// retained spanning tree clone replaces the entry New already installed for the
// same scope. An appending install would leave the bridge consulting both the
// layer New built and the converged clone that replaced it.
func TestDeriveKeepsOneGateEntryPerScope(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	cfg := Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}},
		},
	}

	sw, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := sw.bridge.GateCount(); got != 1 {
		t.Fatalf("GateCount() on New = %d, want 1", got)
	}

	derived, err := Derive(sw, ConstructionSpec{Config: cfg})
	if err != nil {
		t.Fatalf("Derive() error = %v", err)
	}
	if got := derived.bridge.GateCount(); got != 1 {
		t.Errorf("GateCount() after Derive = %d, want 1", got)
	}
}

// Static state comes from the target construction specification: a configured
// forwarding entry survives a port rename elsewhere on the switch, and vanishes
// when the target Seeds drop it.
func TestDeriveConfiguredForwardingEntryPortRenameAndDropping(t *testing.T) {
	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	vid10 := vlan.ID(10)
	baseCfg := Config{
		Ports: ports,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
					"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}
	staticMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	staticSeed := bridge.Seed{
		FID:      10,
		MAC:      staticMAC,
		Port:     "1/1/1",
		Origin:   bridge.Configured,
		Lifetime: bridge.Static,
	}
	spec := ConstructionSpec{
		Config: baseCfg,
		Seeds:  []bridge.Seed{staticSeed},
	}
	sw, err := NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	// Rename 1/1/2 to 1/1/3 on the target, keeping static seed on 1/1/1
	targetPorts, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build target ports: %v", err)
	}
	targetCfg := baseCfg.Clone()
	targetCfg.Ports = targetPorts
	targetCfg.Bridge.VLAN.Switchports = map[string]bridge.Switchport{
		"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
		"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
	}
	targetSpec := ConstructionSpec{
		Config: targetCfg,
		Seeds:  []bridge.Seed{staticSeed},
	}
	derived, err := Derive(sw, targetSpec)
	if err != nil {
		t.Fatalf("Derive with port rename: %v", err)
	}
	var foundStatic bool
	for _, e := range derived.Entries() {
		if e.MAC == staticMAC && e.Port == "1/1/1" && e.Origin == bridge.Configured && e.Lifetime == bridge.Static {
			foundStatic = true
		}
	}
	if !foundStatic {
		t.Errorf("static entry on 1/1/1 did not survive port rename elsewhere on switch: %+v", derived.Entries())
	}

	// When target Seeds drop it, the configured entry vanishes
	dropSpec := ConstructionSpec{
		Config: targetCfg,
		Seeds:  nil,
	}
	derivedDrop, err := Derive(derived, dropSpec)
	if err != nil {
		t.Fatalf("Derive dropping static seed: %v", err)
	}
	for _, e := range derivedDrop.Entries() {
		if e.MAC == staticMAC {
			t.Errorf("static entry %v survived when target Seeds dropped it: %+v", staticMAC, e)
		}
	}
}

// TestLayerDependencyMutationMatrix exercises the constructor input dependencies
// for each of the six capability layers, asserting that mutating each dependency
// rebuilds the layer and names the difference, while an unrelated mutation retains it.
func TestLayerDependencyMutationMatrix(t *testing.T) {
	t.Run("STP", func(t *testing.T) {
		ports, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		base := Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			STP: &stp.Config{
				Priority:     32768,
				HelloTime:    2 * time.Second,
				MaxAge:       20 * time.Second,
				ForwardDelay: 15 * time.Second,
				TxHoldCount:  6,
				Ports: map[string]stp.Port{
					"1/1/1": {PathCost: 20000, PointToPoint: stp.PointToPointAuto},
					"1/1/2": {PathCost: 20000, PointToPoint: stp.PointToPointAuto},
				},
			},
			Phy: &phy.Config{
				Ethernet: map[string]phy.Ethernet{
					"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}, Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full}},
					"1/1/2": {SupportedSpeedsBPS: []uint64{1_000_000_000}, Setting: &phy.Setting{SpeedBPS: 1_000_000_000, Duplex: phy.Full}},
				},
			},
		}
		cur, err := New(base)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mutations := []struct {
			name      string
			mutate    func(*Config)
			wantMatch string
		}{
			{
				name: "port admin status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
					c.Ports, _ = b.Build()
				},
				wantMatch: "port-state",
			},
			{
				name: "port oper status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
					c.Ports, _ = b.Build()
				},
				wantMatch: "port-state",
			},
			{
				name: "resolved speed",
				mutate: func(c *Config) {
					c.Phy.Ethernet["1/1/1"] = phy.Ethernet{
						SupportedSpeedsBPS: []uint64{100_000_000},
						Setting:            &phy.Setting{SpeedBPS: 100_000_000, Duplex: phy.Full},
					}
				},
				wantMatch: "resolved-speed",
			},
			{
				name: "priority",
				mutate: func(c *Config) {
					c.STP.Priority = 16384
				},
				wantMatch: "config",
			},
			{
				name: "hello time",
				mutate: func(c *Config) {
					c.STP.HelloTime = time.Second
				},
				wantMatch: "config",
			},
			{
				name: "max age",
				mutate: func(c *Config) {
					c.STP.MaxAge = 10 * time.Second
				},
				wantMatch: "config",
			},
			{
				name: "forward delay",
				mutate: func(c *Config) {
					c.STP.ForwardDelay = 12 * time.Second
				},
				wantMatch: "config",
			},
			{
				name: "tx hold count",
				mutate: func(c *Config) {
					c.STP.TxHoldCount = 3
				},
				wantMatch: "config",
			},
			{
				name: "path cost",
				mutate: func(c *Config) {
					c.STP.Ports["1/1/1"] = stp.Port{PathCost: 10000}
				},
				wantMatch: "config",
			},
			{
				name: "point to point",
				mutate: func(c *Config) {
					c.STP.Ports["1/1/1"] = stp.Port{PointToPoint: stp.PointToPointForceTrue}
				},
				wantMatch: "config",
			},
		}

		for _, m := range mutations {
			t.Run(m.name, func(t *testing.T) {
				tgt := base.Clone()
				m.mutate(&tgt)
				derived, err := Derive(cur, ConstructionSpec{Config: tgt})
				if err != nil {
					t.Fatalf("Derive: %v", err)
				}
				ret := derived.Retention().STP
				if ret.Kept {
					t.Errorf("STP retained under mutation %q, want rebuilt", m.name)
				}
				if !strings.Contains(ret.Difference, m.wantMatch) {
					t.Errorf("STP difference %q does not mention %q", ret.Difference, m.wantMatch)
				}
			})
		}

		// Unrelated input: traffic policer
		t.Run("unrelated edit retains STP", func(t *testing.T) {
			tgt := base.Clone()
			tgt.Traffic = &traffic.Config{Policers: map[string]traffic.Policer{
				"1/1/1": {RateBPS: 1_000_000, BurstOctets: 1_000},
			}}
			derived, err := Derive(cur, ConstructionSpec{Config: tgt})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if !derived.Retention().STP.Kept {
				t.Errorf("STP rebuilt under unrelated edit, want retained: %+v", derived.Retention().STP)
			}
		})
	})

	t.Run("LoopProtect", func(t *testing.T) {
		ports, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		base := Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			MAC:    netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
			LoopProtect: &loopprotect.Config{
				Ports: map[string]loopprotect.Port{
					"1/1/1": {Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 300 * time.Second}},
				},
			},
		}
		cur, err := New(base)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mutations := []struct {
			name      string
			mutate    func(*Config)
			wantMatch string
		}{
			{
				name: "port admin status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
					c.Ports, _ = b.Build()
				},
				wantMatch: "port-state",
			},
			{
				name: "port oper status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
					c.Ports, _ = b.Build()
				},
				wantMatch: "port-state",
			},
			{
				name: "action",
				mutate: func(c *Config) {
					c.LoopProtect.Ports["1/1/1"] = loopprotect.Port{Action: loopprotect.Disable, Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 300 * time.Second}}
				},
				wantMatch: "config",
			},
			{
				name: "recovery timer",
				mutate: func(c *Config) {
					c.LoopProtect.Ports["1/1/1"] = loopprotect.Port{Action: loopprotect.Block, Recovery: loopprotect.Recovery{Mode: loopprotect.Timer, Duration: 60 * time.Second}}
				},
				wantMatch: "config",
			},
			{
				name: "base mac",
				mutate: func(c *Config) {
					c.MAC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
				},
				wantMatch: "mac",
			},
		}

		for _, m := range mutations {
			t.Run(m.name, func(t *testing.T) {
				tgt := base.Clone()
				m.mutate(&tgt)
				derived, err := Derive(cur, ConstructionSpec{Config: tgt})
				if err != nil {
					t.Fatalf("Derive: %v", err)
				}
				ret := derived.Retention().LoopProtect
				if ret.Kept {
					t.Errorf("LoopProtect retained under mutation %q, want rebuilt", m.name)
				}
				if !strings.Contains(ret.Difference, m.wantMatch) {
					t.Errorf("LoopProtect difference %q does not mention %q", ret.Difference, m.wantMatch)
				}
			})
		}

		t.Run("unrelated edit retains LoopProtect", func(t *testing.T) {
			tgt := base.Clone()
			tgt.Traffic = &traffic.Config{Policers: map[string]traffic.Policer{
				"1/1/1": {RateBPS: 1_000_000, BurstOctets: 1_000},
			}}
			derived, err := Derive(cur, ConstructionSpec{Config: tgt})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if !derived.Retention().LoopProtect.Kept {
				t.Errorf("LoopProtect rebuilt under unrelated edit, want retained: %+v", derived.Retention().LoopProtect)
			}
		})
	})

	t.Run("LAG", func(t *testing.T) {
		ports, err := port.NewBuilder().
			Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		base := Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			MAC:    netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
			LAG: &lag.Config{
				LAGs: map[string]lag.LAG{
					"lag1": {
						Mode:    lag.BalanceSLB,
						LACP:    lag.LACPConfig{SystemPriority: 32768},
						Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}},
					},
				},
			},
		}
		cur, err := New(base)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mutations := []struct {
			name      string
			mutate    func(*Config)
			wantMatch string
		}{
			{
				name: "member admin status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Down, OperStatus: port.Down})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up})
					c.Ports, _ = b.Build()
				},
				wantMatch: "member-state",
			},
			{
				name: "member oper status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up})
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Down})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up})
					c.Ports, _ = b.Build()
				},
				wantMatch: "member-state",
			},
			{
				name: "mode",
				mutate: func(c *Config) {
					c.LAG.LAGs["lag1"] = lag.LAG{
						Mode:    lag.BalanceTCP,
						LACP:    lag.LACPConfig{SystemPriority: 32768},
						Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}},
					}
				},
				wantMatch: "config",
			},
			{
				name: "system priority",
				mutate: func(c *Config) {
					c.LAG.LAGs["lag1"] = lag.LAG{
						Mode:    lag.BalanceSLB,
						LACP:    lag.LACPConfig{SystemPriority: 16384},
						Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}},
					}
				},
				wantMatch: "config",
			},
			{
				name: "system id / MAC",
				mutate: func(c *Config) {
					c.MAC = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
				},
				wantMatch: "system-id",
			},
		}

		for _, m := range mutations {
			t.Run(m.name, func(t *testing.T) {
				tgt := base.Clone()
				m.mutate(&tgt)
				derived, err := Derive(cur, ConstructionSpec{Config: tgt})
				if err != nil {
					t.Fatalf("Derive: %v", err)
				}
				ret := derived.Retention().LAG
				if ret.Kept {
					t.Errorf("LAG retained under mutation %q, want rebuilt", m.name)
				}
				if !strings.Contains(ret.Difference, m.wantMatch) {
					t.Errorf("LAG difference %q does not mention %q", ret.Difference, m.wantMatch)
				}
			})
		}

		t.Run("unrelated edit retains LAG", func(t *testing.T) {
			tgt := base.Clone()
			tgt.Traffic = &traffic.Config{Policers: map[string]traffic.Policer{
				"1/1/1": {RateBPS: 1_000_000, BurstOctets: 1_000},
			}}
			derived, err := Derive(cur, ConstructionSpec{Config: tgt})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if !derived.Retention().LAG.Kept {
				t.Errorf("LAG rebuilt under unrelated edit, want retained: %+v", derived.Retention().LAG)
			}
		})
	})

	t.Run("Mcast", func(t *testing.T) {
		ports, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		vid10 := vlan.ID(10)
		base := Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
					},
				},
			},
			Mcast: &mcast.Config{
				VLANs: map[vlan.ID]mcast.VLANSnooping{
					10: {
						MembershipInterval: 125 * time.Second,
						RouterPortInterval: 125 * time.Second,
						RouterPorts:        []string{"1/1/2"},
					},
				},
			},
		}
		cur, err := New(base)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mutations := []struct {
			name      string
			mutate    func(*Config)
			wantMatch string
		}{
			{
				name: "port admin status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
					c.Ports, _ = b.Build()
				},
				wantMatch: "port-state",
			},
			{
				name: "port oper status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})
					c.Ports, _ = b.Build()
				},
				wantMatch: "port-state",
			},
			{
				name: "membership interval",
				mutate: func(c *Config) {
					v := c.Mcast.VLANs[10]
					v.MembershipInterval = 60 * time.Second
					c.Mcast.VLANs[10] = v
				},
				wantMatch: "config",
			},
			{
				name: "router port interval",
				mutate: func(c *Config) {
					v := c.Mcast.VLANs[10]
					v.RouterPortInterval = 60 * time.Second
					c.Mcast.VLANs[10] = v
				},
				wantMatch: "config",
			},
			{
				name: "router ports",
				mutate: func(c *Config) {
					v := c.Mcast.VLANs[10]
					v.RouterPorts = []string{"1/1/1", "1/1/2"}
					c.Mcast.VLANs[10] = v
				},
				wantMatch: "port-state",
			},
		}

		for _, m := range mutations {
			t.Run(m.name, func(t *testing.T) {
				tgt := base.Clone()
				m.mutate(&tgt)
				derived, err := Derive(cur, ConstructionSpec{Config: tgt})
				if err != nil {
					t.Fatalf("Derive: %v", err)
				}
				ret := derived.Retention().Mcast
				if ret.Kept {
					t.Errorf("Mcast retained under mutation %q, want rebuilt", m.name)
				}
				if !strings.Contains(ret.Difference, m.wantMatch) {
					t.Errorf("Mcast difference %q does not mention %q", ret.Difference, m.wantMatch)
				}
			})
		}

		t.Run("unrelated edit retains Mcast", func(t *testing.T) {
			tgt := base.Clone()
			tgt.Traffic = &traffic.Config{Policers: map[string]traffic.Policer{
				"1/1/1": {RateBPS: 1_000_000, BurstOctets: 1_000},
			}}
			derived, err := Derive(cur, ConstructionSpec{Config: tgt})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if !derived.Retention().Mcast.Kept {
				t.Errorf("Mcast rebuilt under unrelated edit, want retained: %+v", derived.Retention().Mcast)
			}
		})
	})

	t.Run("Routing", func(t *testing.T) {
		ports, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		vid10 := vlan.ID(10)
		base := Config{
			Ports: ports,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "vlan10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
					},
				},
			},
			MAC: netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
			Routing: &routing.Config{
				VRFs: map[string]routing.VRF{
					routing.DefaultVRF: {
						Interfaces: map[string]routing.Interface{
							"eth1": {
								Port:     "1/1/1",
								MAC:      netaddr.MAC{0x00, 0x00, 0x5e, 0x00, 0x01, 0x01},
								Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")},
							},
						},
						NeighborPolicy: routing.NeighborPolicy{
							ResolutionTimeout: 3 * time.Second,
							HoldDepth:         3,
						},
					},
				},
			},
		}
		cur, err := New(base)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mutations := []struct {
			name      string
			mutate    func(*Config)
			wantMatch string
		}{
			{
				name: "port admin status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Down, OperStatus: port.Down})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
					c.Ports, _ = b.Build()
				},
				wantMatch: "port-state",
			},
			{
				name: "port oper status",
				mutate: func(c *Config) {
					b := port.NewBuilder()
					b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})
					b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
					c.Ports, _ = b.Build()
				},
				wantMatch: "port-state",
			},
			{
				name: "resolution timeout",
				mutate: func(c *Config) {
					v := c.Routing.VRFs[routing.DefaultVRF]
					v.NeighborPolicy.ResolutionTimeout = 10 * time.Second
					c.Routing.VRFs[routing.DefaultVRF] = v
				},
				wantMatch: "config",
			},
			{
				name: "hold depth",
				mutate: func(c *Config) {
					v := c.Routing.VRFs[routing.DefaultVRF]
					v.NeighborPolicy.HoldDepth = 5
					c.Routing.VRFs[routing.DefaultVRF] = v
				},
				wantMatch: "config",
			},
			{
				name: "interface prefix",
				mutate: func(c *Config) {
					v := c.Routing.VRFs[routing.DefaultVRF]
					iface := v.Interfaces["eth1"]
					iface.Prefixes = []netip.Prefix{netip.MustParsePrefix("10.0.99.1/24")}
					v.Interfaces["eth1"] = iface
					c.Routing.VRFs[routing.DefaultVRF] = v
				},
				wantMatch: "config",
			},
		}

		for _, m := range mutations {
			t.Run(m.name, func(t *testing.T) {
				tgt := base.Clone()
				m.mutate(&tgt)
				derived, err := Derive(cur, ConstructionSpec{Config: tgt})
				if err != nil {
					t.Fatalf("Derive: %v", err)
				}
				ret := derived.Retention().Routing
				if ret.Kept {
					t.Errorf("Routing retained under mutation %q, want rebuilt", m.name)
				}
				if !strings.Contains(ret.Difference, m.wantMatch) {
					t.Errorf("Routing difference %q does not mention %q", ret.Difference, m.wantMatch)
				}
			})
		}

		t.Run("unrelated edit retains Routing", func(t *testing.T) {
			tgt := base.Clone()
			tgt.Traffic = &traffic.Config{Policers: map[string]traffic.Policer{
				"1/1/1": {RateBPS: 1_000_000, BurstOctets: 1_000},
			}}
			derived, err := Derive(cur, ConstructionSpec{Config: tgt})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if !derived.Retention().Routing.Kept {
				t.Errorf("Routing rebuilt under unrelated edit, want retained: %+v", derived.Retention().Routing)
			}
		})
	})

	t.Run("Traffic", func(t *testing.T) {
		ports, err := port.NewBuilder().
			Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
			Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}
		base := Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			Traffic: &traffic.Config{
				Policers: map[string]traffic.Policer{
					"1/1/1": {RateBPS: 1_000_000, BurstOctets: 10_000},
				},
				Queues: map[string]traffic.PortQueues{
					"1/1/1": {MaxRateBPS: map[vlan.PCP]uint64{5: 100_000_000}},
				},
				Mirrors: []traffic.Mirror{
					{Name: "m1", SelectAll: true, OutputPort: "1/1/2"},
				},
			},
		}
		cur, err := New(base)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		mutations := []struct {
			name      string
			mutate    func(*Config)
			wantMatch string
		}{
			{
				name: "rate bps",
				mutate: func(c *Config) {
					c.Traffic.Policers["1/1/1"] = traffic.Policer{RateBPS: 2_000_000, BurstOctets: 10_000}
				},
				wantMatch: "config",
			},
			{
				name: "burst octets",
				mutate: func(c *Config) {
					c.Traffic.Policers["1/1/1"] = traffic.Policer{RateBPS: 1_000_000, BurstOctets: 20_000}
				},
				wantMatch: "config",
			},
			{
				name: "queue max rate",
				mutate: func(c *Config) {
					c.Traffic.Queues["1/1/1"] = traffic.PortQueues{MaxRateBPS: map[vlan.PCP]uint64{5: 200_000_000}}
				},
				wantMatch: "config",
			},
			{
				name: "mirrors",
				mutate: func(c *Config) {
					c.Traffic.Mirrors = []traffic.Mirror{
						{Name: "m1", SelectAll: true, OutputPort: "1/1/1"},
					}
				},
				wantMatch: "config",
			},
		}

		for _, m := range mutations {
			t.Run(m.name, func(t *testing.T) {
				tgt := base.Clone()
				m.mutate(&tgt)
				derived, err := Derive(cur, ConstructionSpec{Config: tgt})
				if err != nil {
					t.Fatalf("Derive: %v", err)
				}
				ret := derived.Retention().Traffic
				if ret.Kept {
					t.Errorf("Traffic retained under mutation %q, want rebuilt", m.name)
				}
				if !strings.Contains(ret.Difference, m.wantMatch) {
					t.Errorf("Traffic difference %q does not mention %q", ret.Difference, m.wantMatch)
				}
			})
		}

		t.Run("unrelated edit retains Traffic", func(t *testing.T) {
			tgt := base.Clone()
			tgt.STP = &stp.Config{Priority: 16384}
			derived, err := Derive(cur, ConstructionSpec{Config: tgt})
			if err != nil {
				t.Fatalf("Derive: %v", err)
			}
			if !derived.Retention().Traffic.Kept {
				t.Errorf("Traffic rebuilt under unrelated edit, want retained: %+v", derived.Retention().Traffic)
			}
		})
	})
}

// TestDeriveSwitchIdempotenceAndFiftyRandomConstructions asserts that two consecutive
// derives are idempotent (matching retention reports, configs, and entries), and that
// fifty constructions with randomized map insertion order produce identical results.
func TestDeriveSwitchIdempotenceAndFiftyRandomConstructions(t *testing.T) {
	buildSampleConfig := func(rng *rand.Rand) Config {
		portNames := []string{"1/1/1", "1/1/2", "1/1/3", "1/1/4"}
		rng.Shuffle(len(portNames), func(i, j int) { portNames[i], portNames[j] = portNames[j], portNames[i] })

		b := port.NewBuilder()
		for _, name := range portNames {
			b.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		tbl, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		vid10 := vlan.ID(10)

		switchports := make(map[string]bridge.Switchport)
		for _, name := range portNames {
			switchports[name] = bridge.Switchport{PVID: &vid10, Untagged: []vlan.ID{10, 20}}
		}

		stpPorts := make(map[string]stp.Port)
		for _, name := range portNames {
			stpPorts[name] = stp.Port{PathCost: 20000, PointToPoint: stp.PointToPointAuto}
		}

		policers := make(map[string]traffic.Policer)
		for _, name := range portNames {
			policers[name] = traffic.Policer{RateBPS: 1_000_000, BurstOctets: 10_000}
		}

		return Config{
			Ports: tbl,
			Bridge: &bridge.Config{
				VLAN: &bridge.VLAN{
					Table:       map[vlan.ID]string{10: "users", 20: "voice"},
					Switchports: switchports,
				},
			},
			STP: &stp.Config{
				Priority: 32768,
				Ports:    stpPorts,
			},
			Traffic: &traffic.Config{
				Policers: policers,
			},
		}
	}

	baseRNG := rand.New(rand.NewPCG(42, 100))
	baseCfg := buildSampleConfig(baseRNG)
	sw, err := New(baseCfg)
	if err != nil {
		t.Fatalf("New base switch: %v", err)
	}

	// Idempotence over two consecutive derives
	spec := ConstructionSpec{Config: baseCfg}
	d1, err := Derive(sw, spec)
	if err != nil {
		t.Fatalf("Derive 1: %v", err)
	}
	d2, err := Derive(d1, spec)
	if err != nil {
		t.Fatalf("Derive 2: %v", err)
	}

	if !reflect.DeepEqual(d1.Retention(), d2.Retention()) {
		t.Errorf("Retention mismatch between consecutive derives:\nd1: %+v\nd2: %+v", d1.Retention(), d2.Retention())
	}
	if diff := Diff(d1.Config(), d2.Config()); len(diff) != 0 {
		t.Errorf("Config diff between consecutive derives: %+v", diff)
	}
	if !reflect.DeepEqual(d1.Entries(), d2.Entries()) {
		t.Errorf("Entries mismatch between consecutive derives")
	}

	// 50 constructions with randomized map insertion order producing identical results
	firstDerived, err := Derive(sw, ConstructionSpec{Config: buildSampleConfig(rand.New(rand.NewPCG(1, 1)))})
	if err != nil {
		t.Fatalf("Derive first: %v", err)
	}
	firstRet := firstDerived.Retention()
	firstCfg := firstDerived.Config()

	for i := range 50 {
		rng := rand.New(rand.NewPCG(uint64(i+2), uint64(i*7+3)))
		shuffledCfg := buildSampleConfig(rng)
		derived, err := Derive(sw, ConstructionSpec{Config: shuffledCfg})
		if err != nil {
			t.Fatalf("iteration %d Derive failed: %v", i, err)
		}
		if !reflect.DeepEqual(derived.Retention(), firstRet) {
			t.Errorf("iteration %d retention report differs from first:\ngot:  %+v\nwant: %+v", i, derived.Retention(), firstRet)
		}
		if diff := Diff(firstCfg, derived.Config()); len(diff) != 0 {
			t.Errorf("iteration %d config diff against first non-empty: %+v", i, diff)
		}
	}
}
