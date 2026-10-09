package assembly_test

import (
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	reflectormodel "go.aledante.io/FlowSeer/src/common/sim/device/reflector"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/routing"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// routedConfig is a router and a host. The router's route is submitted with a
// prefix that has host bits set, which routing normalization masks.
func routedConfig(t *testing.T, routePrefix string) assembly.Config {
	t.Helper()
	ports := mustTable(t, port.NewBuilder().Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	swMAC := netaddr.MAC{2, 0, 0, 0, 0, 1}

	return statedPhysical(assembly.Config{
		Start: fixedTime,
		Switches: map[string]vswitch.Config{
			"sw1": {MAC: swMAC, Ports: ports, Routing: &routing.Config{VRFs: map[string]routing.VRF{routing.DefaultVRF: {
				Interfaces: map[string]routing.Interface{
					"host": {Port: "1/1/1", MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.10.1/24")}},
					"link": {Port: "1/1/2", MAC: swMAC, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.99.1/24")}},
				},
				Routes: []routing.Route{{Prefix: netip.MustParsePrefix(routePrefix), NextHop: netip.MustParseAddr("10.0.99.2"), Interface: "link"}},
			}}}},
		},
		Hosts: map[string]hostmodel.Config{"h1": {Address: netaddr.MAC{2, 0, 0, 0, 0, 7}}},
		Cables: []fabric.Cable{
			{Cable: cablemodel.Cable{LengthMeters: 5}, A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		},
	})
}

// assemblyEntries runs cfg through every way a configuration enters assembly.
// Each entry point returns its error and the test names it on failure. Configure
// takes only a switch configuration, so it is left out of a test about cables or hosts.
func assemblyEntries(t *testing.T, cfg assembly.Config, switchOnly bool) map[string]error {
	t.Helper()
	cur, err := assembly.New(routedConfig(t, "10.0.20.0/24"))
	if err != nil {
		t.Fatalf("assembly.New of the running fabric: %v", err)
	}

	out := make(map[string]error)
	_, out["New"] = assembly.New(cfg)
	_, out["NewWithSpec"] = assembly.NewWithSpec(constructionSpec(cfg))
	_, out["NewConstructionSpec"] = assembly.NewConstructionSpec(cfg)
	_, out["Derive"] = assembly.Derive(cur, constructionSpec(cfg))
	out["Configure"] = assembly.Configure(cur, "sw1", cfg.Switches["sw1"])
	if !switchOnly {
		delete(out, "Configure")
	}

	return out
}

func TestAssemblyAcceptsAConfigurationValidAfterNormalization(t *testing.T) {
	cfg := routedConfig(t, "10.0.20.5/24")
	for entry, err := range assemblyEntries(t, cfg, true) {
		if err != nil {
			t.Errorf("%s refused a route prefix that normalization masks: %v", entry, err)
		}
	}
}

func TestAssemblyJudgesTheSubmittedFault(t *testing.T) {
	cfg := routedConfig(t, "10.0.20.0/24")
	// Normalization clears N from a Cut, which would hide that it was submitted.
	cfg.Cables[0].Fault = cablemodel.Fault{Kind: cablemodel.FaultCut, N: 7}
	for entry, err := range assemblyEntries(t, cfg, false) {
		if err == nil {
			t.Errorf("%s accepted a Cut fault with an inactive N", entry)
		}
	}
}

func TestAssemblyNamesTheSubmittedMulticastPosition(t *testing.T) {
	cfg := routedConfig(t, "10.0.20.0/24")
	group := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}
	unicast := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	h1 := cfg.Hosts["h1"]
	// Normalization sorts the unicast entry from position 1 to position 0.
	h1.Accept.Multicast = []netaddr.MAC{group, unicast}
	cfg.Hosts["h1"] = h1
	for entry, err := range assemblyEntries(t, cfg, false) {
		if err == nil {
			t.Errorf("%s accepted a unicast multicast entry", entry)
			continue
		}
		if got, want := errs.Attributes(err)["field"], "hosts.h1.accept.multicast.1"; got != want {
			t.Errorf("%s field = %v, want %q: the submitted position", entry, got, want)
		}
	}
}

// TestAssemblyAcceptsInputsEveryNormalizerRepairs covers validators other than the
// switch's: a duplicate host or reflector address, and unsorted or repeated speeds in
// the host, the reflector port, and the assumed facts, are all repaired by Normalize.
func TestAssemblyAcceptsInputsEveryNormalizerRepairs(t *testing.T) {
	for name, mut := range map[string]func(*assembly.Config){
		"duplicate host address": func(c *assembly.Config) {
			c.Hosts["h2"] = hostmodel.Config{Address: netaddr.MAC{2, 0, 0, 0, 0, 8}, IP: &hostmodel.IP{Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.7/24"), netip.MustParsePrefix("10.0.10.7/24")}}}
			c.Cables = append(c.Cables, fabric.Cable{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}})
		},
		"unsorted assumed speeds": func(c *assembly.Config) {
			c.PhyAssumption.Ethernet.SupportedSpeedsBPS = []uint64{1_000_000_000, 10_000_000, 1_000_000_000, 100_000_000}
		},
		"unsorted host speeds": func(c *assembly.Config) {
			h := c.Hosts["h1"]
			h.Ethernet = phy.Ethernet{SupportedSpeedsBPS: []uint64{1_000_000_000, 10_000_000, 1_000_000_000}}
			c.Hosts["h1"] = h
		},
		"duplicate reflector attachment address and unsorted port speeds": func(c *assembly.Config) {
			c.Reflectors = map[string]reflectormodel.Config{"r": {
				Ports:       map[string]phy.Ethernet{"p": {SupportedSpeedsBPS: []uint64{1_000_000_000, 10_000_000}}},
				Attachments: map[string]reflectormodel.Attachment{"a": {Port: "p", Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.0.9/24"), netip.MustParsePrefix("10.0.0.9/24")}}},
			}}
			c.Cables = append(c.Cables, fabric.Cable{A: fabric.Endpoint{Node: "r", Port: "p"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}})
		},
	} {
		cfg := routedConfig(t, "10.0.20.0/24")
		cfg.Uncabled = nil
		mut(&cfg)
		if _, err := assembly.New(cfg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
