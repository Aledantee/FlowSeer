package assembly_test

import (
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestDeriveRetainsAdmittedDynamicEntries(t *testing.T) {
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, err := b.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)

	macA := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macB := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0b}
	macC := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x0c}

	curCfg := assembly.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports,
				Bridge: &bridge.Config{
					VLAN: &bridge.VLAN{
						Table: map[vlan.ID]string{
							10: "vlan10",
							20: "vlan20",
						},
						Switchports: map[string]bridge.Switchport{
							"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
							"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
						},
					},
				},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macB},
			"h2": {Address: macA},
			"h3": {Address: macC},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}},
		},
	}

	curFab, err := assembly.New(statedPhysical(curCfg))
	if err != nil {
		t.Fatalf("New curFab: %v", err)
	}

	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	// Inject frame from h2 to learn (10, macA) on 1/1/2.
	_, err = curFab.Inject(fabric.Injection{
		At:     now,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame: ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       macA,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("frame A"),
		},
	})
	if err != nil {
		t.Fatalf("Inject h2: %v", err)
	}

	// Inject frame from h1 to learn (10, macB) on 1/1/1.
	_, err = curFab.Inject(fabric.Injection{
		At:     now.Add(time.Second),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
			Src:       macB,
			EtherType: ethernet.EtherTypeIPv4,
			Payload:   []byte("frame B"),
		},
	})
	if err != nil {
		t.Fatalf("Inject h1: %v", err)
	}

	curFab.Run(10, 0)

	entries := curFab.Device("sw1").(*vswitch.Switch).Entries()
	if len(entries) != 2 {
		t.Fatalf("sw1 learned entries = %d, want 2", len(entries))
	}

	// In the expected config, 1/1/2 moves to VLAN 20.
	expCfg := curCfg.Clone()
	expSw := expCfg.Switches["sw1"]
	expSw.Bridge.VLAN.Switchports["1/1/2"] = bridge.Switchport{
		PVID:     &vid20,
		Untagged: []vlan.ID{20},
	}
	expCfg.Switches["sw1"] = expSw

	derivedFab, err := assembly.Derive(curFab, constructionSpec(statedPhysical(expCfg)))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	derivedEntries := derivedFab.Device("sw1").(*vswitch.Switch).Entries()
	if len(derivedEntries) != 1 {
		t.Fatalf("derived sw1 entries = %d, want 1: %v", len(derivedEntries), derivedEntries)
	}
	if derivedEntries[0].FID != 10 || derivedEntries[0].MAC != macB || derivedEntries[0].Port != "1/1/1" {
		t.Errorf("derived entry mismatch: got %v, want (10, %s) -> 1/1/1", derivedEntries[0], macB)
	}
}

func TestDeriveBuildsFreshSwitchWhenNoNamesake(t *testing.T) {
	curCfg, _ := makeTwoSwitchConfigs(t)
	curFab, err := assembly.New(statedPhysical(curCfg))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	expCfg := curCfg.Clone()
	b3 := port.NewBuilder()
	b3.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable3, err := b3.Build()
	if err != nil {
		t.Fatalf("build sw3 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	expCfg.Switches["sw3"] = vswitch.Config{
		Ports: pTable3,
		Bridge: &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{10: "vlan10"},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
				},
			},
		},
	}
	expCfg.Hosts["h3"] = hostmodel.Config{Address: netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x03}}
	expCfg.Cables = append(expCfg.Cables, fabric.Cable{
		A: fabric.Endpoint{Node: "h3"},
		B: fabric.Endpoint{Node: "sw3", Port: "1/1/1"},
	})

	derived, err := assembly.Derive(curFab, constructionSpec(statedPhysical(expCfg)))
	if err != nil {
		t.Fatalf("Derive with new switch: %v", err)
	}
	if derived.Device("sw3").(*vswitch.Switch) == nil {
		t.Errorf("derived switch sw3 is nil, want fresh switch")
	}

	// From nil cur
	fromNil, err := assembly.Derive(nil, constructionSpec(statedPhysical(expCfg)))
	if err != nil {
		t.Fatalf("Derive from nil: %v", err)
	}
	if fromNil.Device("sw3").(*vswitch.Switch) == nil {
		t.Errorf("derive from nil sw3 is nil, want fresh switch")
	}
}

func TestDiffAllSubjectKinds(t *testing.T) {
	curCfg, macH1 := makeTwoSwitchConfigs(t)

	t.Run("equal configs return empty diff", func(t *testing.T) {
		changes := assembly.Diff(curCfg, curCfg.Clone())
		if len(changes) != 0 {
			t.Errorf("Diff returned %d changes for equal configs, want 0", len(changes))
		}
	})

	t.Run("cable modifications and added/removed cables", func(t *testing.T) {
		expCfg := curCfg.Clone()
		// Modify first cable length and top speed
		expCfg.Cables[0].LengthMeters = 50
		expCfg.Cables[0].TopSpeedBPS = 100_000_000

		changes := assembly.Diff(curCfg, expCfg)
		var foundLen, foundSpeed bool
		for _, ch := range changes {
			if ch.Subject.Kind == "cable" && ch.Subject.Key == "h1:-sw1:1/1/1" {
				if ch.Field == "length" && isFact(ch.From, "fabric.length_meters", "0") && isFact(ch.To, "fabric.length_meters", "50") {
					foundLen = true
				}
				if ch.Field == "top_speed" && isFact(ch.From, "fabric.top_speed_bps", "0") && isFact(ch.To, "fabric.top_speed_bps", "100000000") {
					foundSpeed = true
				}
			}
		}
		if !foundLen || !foundSpeed {
			t.Errorf("missing cable length or top_speed change: %v", changes)
		}
	})

	t.Run("unspecified medium diffs against twisted pair", func(t *testing.T) {
		cfgA := curCfg.Clone()
		cfgA.Cables[0].Medium = cablemodel.MediumUnspecified

		cfgB := cfgA.Clone()
		cfgB.Cables[0].Medium = cablemodel.TwistedPair

		from, to := changedFieldFacts(t, "medium", cfgA, cfgB)
		if from != cablemodel.MediumUnspecified || to != cablemodel.TwistedPair {
			t.Errorf("medium change = %v -> %v, want Unspecified -> TwistedPair", from, to)
		}
	})

	t.Run("cable medium and delay modifications", func(t *testing.T) {
		cfgA := curCfg.Clone()
		cfgA.Cables[0].Medium = cablemodel.TwistedPair
		cfgA.Cables[0].Delay = nil

		cfgB := cfgA.Clone()
		d := 1 * time.Microsecond
		cfgB.Cables[0].Medium = cablemodel.SinglemodeFiber
		cfgB.Cables[0].Delay = &d

		changes := assembly.Diff(cfgA, cfgB)
		var foundMedium, foundDelay bool
		for _, ch := range changes {
			if ch.Subject.Kind == "cable" && ch.Subject.Key == "h1:-sw1:1/1/1" {
				if ch.Field == "medium" && ch.From == cablemodel.TwistedPair && ch.To == cablemodel.SinglemodeFiber {
					foundMedium = true
				}
				if ch.Field == "delay" && ch.From == nil && isFact(ch.To, "fabric.delay", (1*time.Microsecond).String()) {
					foundDelay = true
				}
			}
		}
		if !foundMedium {
			t.Errorf("missing cable medium change: %v", changes)
		}
		if !foundDelay {
			t.Errorf("missing cable delay change: %v", changes)
		}
	})

	t.Run("host moved port, address changed, vlan changed", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		b.Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, _ := b.Build()

		vid10 := vlan.ID(10)
		vid20 := vlan.ID(20)

		cfgA := assembly.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {Ports: tbl},
			},
			Hosts: map[string]hostmodel.Config{
				"h1": {Address: macH1, VLAN: &vid10},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			},
		}

		macH1New := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99}
		cfgB := assembly.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {Ports: tbl},
			},
			Hosts: map[string]hostmodel.Config{
				"h1": {Address: macH1New, VLAN: &vid20},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			},
		}

		changes := assembly.Diff(cfgA, cfgB)

		var foundPort, foundAddr, foundVLAN bool
		for _, ch := range changes {
			if ch.Subject.Kind == "host" && ch.Subject.Key == "h1" {
				if ch.Field == "port" && ch.From == (fabric.Endpoint{Node: "sw1", Port: "1/1/1"}) &&
					ch.To == (fabric.Endpoint{Node: "sw1", Port: "1/1/2"}) {
					foundPort = true
				}
				if ch.Field == "address" && isFact(ch.From, "fabric.mac", macH1.String()) && isFact(ch.To, "fabric.mac", macH1New.String()) {
					foundAddr = true
				}
				if ch.Field == "vlan" && isFact(ch.From, "fabric.vlan", strconv.Itoa(int(vid10))) && isFact(ch.To, "fabric.vlan", strconv.Itoa(int(vid20))) {
					foundVLAN = true
				}
			}
		}

		if !foundPort {
			t.Errorf("missing host port move change: %v", changes)
		}
		if !foundAddr {
			t.Errorf("missing host address change: %v", changes)
		}
		if !foundVLAN {
			t.Errorf("missing host vlan change: %v", changes)
		}
	})

	t.Run("switch and host added and removed", func(t *testing.T) {
		b := port.NewBuilder()
		b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		tbl, _ := b.Build()

		cfgA := assembly.Config{
			Switches: map[string]vswitch.Config{
				"sw1": {Ports: tbl},
			},
			Hosts: map[string]hostmodel.Config{
				"h1": {Address: macH1},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			},
		}

		cfgB := assembly.Config{
			Switches: map[string]vswitch.Config{
				"sw2": {Ports: tbl},
			},
			Hosts: map[string]hostmodel.Config{
				"h2": {Address: macH1},
			},
			Cables: []fabric.Cable{
				{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			},
		}

		changes := assembly.Diff(cfgA, cfgB)

		var (
			sw1Removed, sw2Added bool
			h1Removed, h2Added   bool
		)

		for _, ch := range changes {
			if ch.Subject.Kind == "switch" {
				if ch.Subject.Key == "sw1" && ch.Field == "" && ch.To == nil {
					sw1Removed = true
				}
				if ch.Subject.Key == "sw2" && ch.Field == "" && ch.From == nil {
					sw2Added = true
				}
			}
			if ch.Subject.Kind == "host" {
				if ch.Subject.Key == "h1" && ch.Field == "" && ch.To == nil {
					h1Removed = true
				}
				if ch.Subject.Key == "h2" && ch.Field == "" && ch.From == nil {
					h2Added = true
				}
			}
		}

		if !sw1Removed || !sw2Added {
			t.Errorf("switch add/remove diff failed: %v", changes)
		}
		if !h1Removed || !h2Added {
			t.Errorf("host add/remove diff failed: %v", changes)
		}
	})

	t.Run("host IP stack diff reports gateway and neighbor changes", func(t *testing.T) {
		gw1 := netip.MustParseAddr("10.0.10.1")
		gw2 := netip.MustParseAddr("10.0.10.254")
		nbr1 := netip.MustParseAddr("10.0.10.2")
		nbr2 := netip.MustParseAddr("10.0.10.3")
		mac1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
		mac2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x22}

		cfgA := assembly.Config{
			Hosts: map[string]hostmodel.Config{
				"h1": {
					Address: macH1,
					IP: &hostmodel.IP{
						Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.10/24")},
						Gateway:   gw1,
						Neighbors: map[netip.Addr]netaddr.MAC{
							nbr1: mac1,
						},
					},
				},
			},
		}

		cfgB := assembly.Config{
			Hosts: map[string]hostmodel.Config{
				"h1": {
					Address: macH1,
					IP: &hostmodel.IP{
						Addresses: []netip.Prefix{
							netip.MustParsePrefix("10.0.10.10/24"),
							netip.MustParsePrefix("fd00::10/64"),
						},
						Gateway: gw2,
						Neighbors: map[netip.Addr]netaddr.MAC{
							nbr1: mac1,
							nbr2: mac2,
						},
					},
				},
			},
		}

		changes := assembly.Diff(cfgA, cfgB)

		var foundAddrs, foundGW, foundNbr bool
		for _, ch := range changes {
			if ch.Subject.Kind == "host" && ch.Subject.Key == "h1" {
				if ch.Field == "addresses" {
					foundAddrs = true
					if !trace.EqualFact(ch.From, assembly.PrefixesFact([]string{"10.0.10.10/24"})) ||
						!trace.EqualFact(ch.To, assembly.PrefixesFact([]string{"10.0.10.10/24", "fd00::10/64"})) {
						t.Errorf("addresses diff = %v -> %v", ch.From, ch.To)
					}
				}
				if ch.Field == "gateway" {
					foundGW = true
					if !isFact(ch.From, "fabric.gateway", gw1.String()) || !isFact(ch.To, "fabric.gateway", gw2.String()) {
						t.Errorf("gateway diff = %v -> %v, want %v -> %v", ch.From, ch.To, gw1, gw2)
					}
				}
				if ch.Field == "neighbors.10.0.10.3" {
					foundNbr = true
					if ch.From != nil || !isFact(ch.To, "fabric.mac", mac2.String()) {
						t.Errorf("neighbor diff = %v -> %v, want nil -> %v", ch.From, ch.To, mac2)
					}
				}
			}
		}

		if !foundAddrs {
			t.Errorf("missing addresses diff: %v", changes)
		}
		if !foundGW {
			t.Errorf("missing gateway diff: %v", changes)
		}
		if !foundNbr {
			t.Errorf("missing neighbor diff: %v", changes)
		}
	})
}

func TestDiffDetectsReversedDirectionalFaults(t *testing.T) {
	for _, kind := range []cablemodel.FaultKind{cablemodel.FaultDeadAToB, cablemodel.FaultDeadBToA} {
		t.Run(string(kind), func(t *testing.T) {
			cfgA, _ := makeTwoSwitchConfigs(t)
			cfgA.Cables[1].Fault = cablemodel.Fault{Kind: kind}
			cfgB := cfgA.Clone()
			cfgB.Cables[1].A, cfgB.Cables[1].B = cfgB.Cables[1].B, cfgB.Cables[1].A

			changes := assembly.Diff(cfgA, cfgB)
			var faultChange *trace.Change
			for i := range changes {
				if changes[i].Subject.Kind == "cable" && changes[i].Field == "fault" {
					faultChange = &changes[i]
					break
				}
			}
			if faultChange == nil {
				t.Fatal("Diff missed behavior change from reversing a directional cable fault")
			}
			if trace.EqualFact(faultChange.From, faultChange.To) {
				t.Errorf("fault change has equal facts: %v -> %v", faultChange.From, faultChange.To)
			}
			wantFrom, wantTo := kind, cablemodel.FaultDeadAToB
			if kind == cablemodel.FaultDeadAToB {
				wantTo = cablemodel.FaultDeadBToA
			}
			if !strings.Contains(faultChange.From.Canonical(), string(wantFrom)) ||
				!strings.Contains(faultChange.To.Canonical(), string(wantTo)) {
				t.Errorf("fault change = %q -> %q, want %q -> %q", faultChange.From.Canonical(), faultChange.To.Canonical(), wantFrom, wantTo)
			}

			reverseFrom, reverseTo := changedFieldFacts(t, "fault", cfgB, cfgA)
			if !strings.Contains(reverseFrom.Canonical(), string(wantTo)) ||
				!strings.Contains(reverseTo.Canonical(), string(wantFrom)) {
				t.Errorf("reverse fault change = %q -> %q, want %q -> %q", reverseFrom.Canonical(), reverseTo.Canonical(), wantTo, wantFrom)
			}

			equivalent := cfgA.Clone()
			equivalent.Cables[1].A, equivalent.Cables[1].B = equivalent.Cables[1].B, equivalent.Cables[1].A
			if kind == cablemodel.FaultDeadAToB {
				equivalent.Cables[1].Fault.Kind = cablemodel.FaultDeadBToA
			} else {
				equivalent.Cables[1].Fault.Kind = cablemodel.FaultDeadAToB
			}
			if changes := assembly.Diff(cfgA, equivalent); len(changes) != 0 {
				t.Errorf("equivalent reversed cable diff = %v, want no changes", changes)
			}
		})
	}
}

func TestDiffUsesLosslessImmutableSnapshotFacts(t *testing.T) {
	vid10 := vlan.ID(10)
	vid20 := vlan.ID(20)
	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	gateway := netip.MustParseAddr("10.0.10.1")
	host := hostmodel.Config{
		Address: mac,
		VLAN:    &vid10,
		IP: &hostmodel.IP{
			Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.10/24")},
			Gateway:   gateway,
			Neighbors: map[netip.Addr]netaddr.MAC{
				gateway: {0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
			},
		},
	}

	hostCfgA := assembly.Config{Hosts: map[string]hostmodel.Config{"h1": host}}
	hostCfgB := hostCfgA.Clone()
	hostB := hostCfgB.Hosts["h1"]
	hostB.VLAN = &vid20
	hostCfgB.Hosts["h1"] = hostB
	hostFactA := addedFact(t, "host", hostCfgA)
	hostFactB := addedFact(t, "host", hostCfgB)
	if trace.EqualFact(hostFactA, hostFactB) {
		t.Error("host snapshot facts collide when VLAN behavior differs")
	}
	if got := reflect.TypeOf(hostFactA); got == reflect.TypeOf(hostmodel.Config{}) {
		t.Errorf("host diff fact has mutable config type %v", got)
	}
	hostVariants := []struct {
		name   string
		mutate func(*hostmodel.Config)
	}{
		{name: "address", mutate: func(h *hostmodel.Config) { h.Address[5]++ }},
		{name: "IP presence", mutate: func(h *hostmodel.Config) { h.IP = nil }},
		{name: "IP address", mutate: func(h *hostmodel.Config) {
			h.IP.Addresses[0] = netip.MustParsePrefix("10.0.20.10/24")
		}},
		{name: "gateway", mutate: func(h *hostmodel.Config) {
			h.IP.Gateway = netip.MustParseAddr("10.0.10.254")
		}},
		{name: "neighbor", mutate: func(h *hostmodel.Config) {
			h.IP.Neighbors[gateway] = netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
		}},
	}
	for _, variant := range hostVariants {
		t.Run("host "+variant.name, func(t *testing.T) {
			changed := host.Clone()
			variant.mutate(&changed)
			fact := addedFact(t, "host", assembly.Config{Hosts: map[string]hostmodel.Config{"h1": changed}})
			if trace.EqualFact(hostFactA, fact) {
				t.Errorf("host snapshot facts collide when %s differs", variant.name)
			}
		})
	}

	delay := time.Nanosecond
	cableA := fabric.Cable{
		A: fabric.Endpoint{Node: "left", Port: "one"},
		B: fabric.Endpoint{Node: "right", Port: "two"},
	}
	cableB := cableA.Clone()
	cableB.Delay = &delay
	cableB.Fault = cablemodel.Fault{Kind: cablemodel.FaultCut}
	cableCfgA := assembly.Config{Cables: []fabric.Cable{cableA}}
	cableCfgB := assembly.Config{Cables: []fabric.Cable{cableB}}
	cableFactA := addedFact(t, "cable", cableCfgA)
	cableFactB := addedFact(t, "cable", cableCfgB)
	if got := reflect.TypeOf(cableFactA); got == reflect.TypeOf(fabric.Cable{}) {
		t.Errorf("cable diff fact has mutable config type %v", got)
	}
	cableVariants := []struct {
		name   string
		mutate func(*fabric.Cable)
	}{
		{name: "endpoint node", mutate: func(c *fabric.Cable) { c.A.Node = "left:one" }},
		{name: "endpoint port", mutate: func(c *fabric.Cable) { c.A.Port = "one:two" }},
		{name: "length", mutate: func(c *fabric.Cable) { c.LengthMeters = 1.5 }},
		{name: "top speed", mutate: func(c *fabric.Cable) { c.TopSpeedBPS = 1_000_000_000 }},
		{name: "fault", mutate: func(c *fabric.Cable) { c.Fault = cablemodel.Fault{Kind: cablemodel.FaultCut} }},
		{name: "medium", mutate: func(c *fabric.Cable) { c.Medium = cablemodel.SinglemodeFiber }},
		{name: "delay", mutate: func(c *fabric.Cable) { c.Delay = &delay }},
	}
	for _, variant := range cableVariants {
		t.Run("cable "+variant.name, func(t *testing.T) {
			changed := cableA.Clone()
			variant.mutate(&changed)
			fact := addedFact(t, "cable", assembly.Config{Cables: []fabric.Cable{changed}})
			if trace.EqualFact(cableFactA, fact) {
				t.Errorf("cable snapshot facts collide when %s differs", variant.name)
			}
		})
	}

	faultCfgA := assembly.Config{Cables: []fabric.Cable{{
		Cable: cablemodel.Cable{Fault: cablemodel.Fault{Kind: cablemodel.FaultLoseSequence, Sequence: []uint{1}}},
		A:     cableA.A,
		B:     cableA.B,
	}}}
	faultCfgB := faultCfgA.Clone()
	faultCfgB.Cables[0].Fault.Sequence[0] = 2
	faultFactA, faultFactB := changedFieldFacts(t, "fault", faultCfgA, faultCfgB)
	if trace.EqualFact(faultFactA, faultFactB) {
		t.Error("fault snapshot facts collide when loss sequence differs")
	}
	if got := reflect.TypeOf(faultFactA); got == reflect.TypeOf(cablemodel.Fault{}) {
		t.Errorf("fault diff fact has mutable config type %v", got)
	}

	wantHost := hostFactA.Canonical()
	wantCable := cableFactB.Canonical()
	wantFault := faultFactA.Canonical()
	*hostCfgA.Hosts["h1"].VLAN = 30
	hostCfgA.Hosts["h1"].IP.Addresses[0] = netip.MustParsePrefix("10.0.30.10/24")
	hostCfgA.Hosts["h1"].IP.Neighbors[gateway] = netaddr.MAC{}
	*cableCfgB.Cables[0].Delay = 2 * time.Nanosecond
	cableCfgB.Cables[0].Fault.Kind = cablemodel.FaultNone
	faultCfgA.Cables[0].Fault.Sequence[0] = 99
	if got := hostFactA.Canonical(); got != wantHost {
		t.Errorf("host fact changed after source mutation: got %q, want %q", got, wantHost)
	}
	if got := cableFactB.Canonical(); got != wantCable {
		t.Errorf("cable fact changed after source mutation: got %q, want %q", got, wantCable)
	}
	if got := faultFactA.Canonical(); got != wantFault {
		t.Errorf("fault fact changed after source mutation: got %q, want %q", got, wantFault)
	}
}

func addedFact(t *testing.T, kind string, cfg assembly.Config) trace.Fact {
	t.Helper()

	for _, change := range assembly.Diff(assembly.Config{}, cfg) {
		if change.Subject.Kind == kind && change.Field == "" && change.To != nil {
			return change.To
		}
	}
	t.Fatalf("missing added %s fact", kind)

	return nil
}

func changedFieldFacts(t *testing.T, field string, a, b assembly.Config) (trace.Fact, trace.Fact) {
	t.Helper()

	for _, change := range assembly.Diff(a, b) {
		if change.Field == field {
			return change.From, change.To
		}
	}
	t.Fatalf("missing %s change", field)

	return nil, nil
}

func isFact(f trace.Fact, typeID, canonical string) bool {
	return f != nil && f.TypeID() == typeID && f.Canonical() == canonical
}

func makeTwoSwitchConfigs(t *testing.T) (assembly.Config, netaddr.MAC) {
	t.Helper()

	b1 := port.NewBuilder()
	b1.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b1.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable1, err := b1.Build()
	if err != nil {
		t.Fatalf("build sw1 ports: %v", err)
	}

	b2 := port.NewBuilder()
	b2.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b2.Add(port.Port{Name: "1/1/24", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	pTable2, err := b2.Build()
	if err != nil {
		t.Fatalf("build sw2 ports: %v", err)
	}

	vid10 := vlan.ID(10)
	newBridgeCfg := func() *bridge.Config {
		return &bridge.Config{
			VLAN: &bridge.VLAN{
				Table: map[vlan.ID]string{
					10: "VLAN10",
				},
				Switchports: map[string]bridge.Switchport{
					"1/1/1": {
						PVID:     &vid10,
						Untagged: []vlan.ID{10},
					},
					"1/1/24": {
						Tagged: []vlan.ID{10},
					},
				},
			},
		}
	}

	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	cfg := assembly.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  pTable1,
				Bridge: newBridgeCfg(),
			},
			"sw2": {
				Ports:  pTable2,
				Bridge: newBridgeCfg(),
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
		},
		Cables: []fabric.Cable{
			{
				A: fabric.Endpoint{Node: "h1"},
				B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
			},
			{
				Cable: cablemodel.Cable{LengthMeters: 300, Medium: cablemodel.MultimodeFiber},
				A:     fabric.Endpoint{Node: "sw1", Port: "1/1/24"},
				B:     fabric.Endpoint{Node: "sw2", Port: "1/1/24"},
			},
			{
				A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"},
				B: fabric.Endpoint{Node: "h2"},
			},
		},
	}

	return cfg, macH1
}
