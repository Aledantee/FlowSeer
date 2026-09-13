package phy_test

import (
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

func TestVSwitchConfigCloneIsolatesPDClass(t *testing.T) {
	t.Parallel()

	pdClass := uint8(4)
	original := vswitch.Config{Phy: &phy.Config{PoE: &phy.PoE{
		Ports: map[string]phy.PsePort{"1/1/1": {PDClass: &pdClass}},
	}}}
	cloned := original.Clone()
	clonedPort := cloned.Phy.PoE.Ports["1/1/1"]
	*clonedPort.PDClass = 5

	if got := *original.Phy.PoE.Ports["1/1/1"].PDClass; got != 4 {
		t.Errorf("original PDClass = %d, want 4", got)
	}
}

func TestSwitchConfigAndSpecIsolatePDClass(t *testing.T) {
	t.Parallel()

	pdClass := uint8(4)
	ports := mustTable(t, port.Port{Name: "1/1/1", Kind: port.Physical})
	cfg := vswitch.Config{
		Ports: ports,
		Phy: &phy.Config{PoE: &phy.PoE{
			Groups: map[string]phy.Group{"1": {PowerMilliwatts: 60_000}},
			Ports: map[string]phy.PsePort{
				"1/1/1": {Group: "1", MaxClass: 8, Enabled: true, PD: phy.PDAttached, PDClass: &pdClass},
			},
		}},
	}
	sw, err := vswitch.New(cfg)
	if err != nil {
		t.Fatalf("vswitch.New: %v", err)
	}

	fromConfig := sw.Config()
	configPort := fromConfig.Phy.PoE.Ports["1/1/1"]
	*configPort.PDClass = 5
	fromSpec := sw.Spec()
	specPort := fromSpec.Config.Phy.PoE.Ports["1/1/1"]
	*specPort.PDClass = 6

	if got := *sw.Config().Phy.PoE.Ports["1/1/1"].PDClass; got != 4 {
		t.Errorf("stored PDClass = %d, want 4", got)
	}
}

func TestVSwitchRejectsGroupBaseMAC(t *testing.T) {
	t.Parallel()

	cfg := vswitch.Config{
		MAC:   netaddr.MAC{0x01, 0, 0, 0, 0, 1},
		Ports: mustTable(t, port.Port{Name: "1/1/1", Kind: port.Physical}),
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Config.Validate() = nil, want error")
	}
	if _, err := vswitch.New(cfg); err == nil {
		t.Fatal("vswitch.New() error = nil, want error")
	}
}

func TestVSwitchConfigFactIsLosslessAndSourceIsolated(t *testing.T) {
	t.Parallel()

	a := vswitch.Config{Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{
		"1/1/1": {SupportedSpeedsBPS: []uint64{100_000_000}},
	}}}
	b := vswitch.Config{Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{
		"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
	}}}

	fact := vswitch.ConfigFact(a)
	before := fact.Canonical()
	if got := vswitch.ConfigFact(b).Canonical(); got == before {
		t.Fatalf("different effective configurations share canonical form %q", got)
	}
	a.Phy.Ethernet["1/1/1"] = phy.Ethernet{SupportedSpeedsBPS: []uint64{10_000_000_000}}
	if got := fact.Canonical(); got != before {
		t.Errorf("snapshot canonical changed after source mutation: got %q, want %q", got, before)
	}

	portA := vswitch.Config{Ports: mustTable(t, port.Port{Name: "1/1/1", Kind: port.Physical})}
	portB := vswitch.Config{Ports: mustTable(t, port.Port{Name: "1/1/2", Kind: port.Physical})}
	if vswitch.ConfigFact(portA).Canonical() == vswitch.ConfigFact(portB).Canonical() {
		t.Errorf("configurations with different ports share canonical form %q", vswitch.ConfigFact(portA).Canonical())
	}
}
