package vswitch_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// TestEntriesCarryOriginAndLifetime verifies at the switch level that a seeded entry and a
// live-learned one for different MACs both appear in Entries() reporting their own
// Origin and Lifetime.
func TestEntriesCarryOriginAndLifetime(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	sw := mustSwitch(t, vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})

	mustSwitchLearn(t, sw, []bridge.Seed{{MAC: macH1, Port: "1/1/1", Lifetime: bridge.Static, LearnedAt: fixedTime}})
	sw.Receive(fixedTime, "1/1/2", ethernet.Frame{Src: macH2, Dst: macH1})

	entries := sw.Entries()
	if len(entries) != 2 {
		t.Fatalf("len(Entries()) = %d, want 2, got %+v", len(entries), entries)
	}
	byMAC := make(map[netaddr.MAC]bridge.Entry, len(entries))
	for _, e := range entries {
		byMAC[e.MAC] = e
	}
	if seeded, ok := byMAC[macH1]; !ok || seeded.Origin != bridge.Configured || seeded.Lifetime != bridge.Static {
		t.Errorf("seeded entry = %+v, ok=%v, want Origin: Configured, Lifetime: Static", seeded, ok)
	}
	if learned, ok := byMAC[macH2]; !ok || learned.Origin != bridge.Observed || learned.Lifetime != bridge.Aging {
		t.Errorf("learned entry = %+v, ok=%v, want Origin: Observed, Lifetime: Aging", learned, ok)
	}
}

func TestSwitchReadableState(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical}))

	cfg := vswitch.Config{
		Ports: tbl,
		Phy: &phy.Config{
			Ethernet: map[string]phy.Ethernet{
				"1/1/1": {SupportedSpeedsBPS: []uint64{1_000_000_000}},
			},
			PoE: &phy.PoE{
				Groups: map[string]phy.Group{"g1": {PowerNanowatts: 15_400_000_000}},
				Ports:  map[string]phy.PsePort{"1/1/1": {Group: "g1", Enabled: true, MaxClass: 3}},
			},
		},
	}

	sw := mustSwitch(t, cfg)

	// Ports
	if sw.Ports().Len() != 1 {
		t.Errorf("got %d ports, want 1", sw.Ports().Len())
	}

	// Power
	power := sw.Power()
	if power.Groups == nil || len(power.Groups) != 1 {
		t.Errorf("got group allocations %v, want exactly 1", power.Groups)
	}
	if issues := power.Metadata.Issues(); len(issues) != 1 || issues[0].Code != "poe-demand-unknown" {
		t.Errorf("got poe-demand-unknown issues %v, want exactly 1", issues)
	}

	// Config clone
	cloned := sw.Config()
	if cloned.Phy == nil {
		t.Errorf("cloned config missing phy")
	}
}

func TestSwitchPowerMetadata(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	spec := vswitch.ConstructionSpec{
		NodeID: "sw1",
		Config: vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
			Phy: &phy.Config{
				PoE: &phy.PoE{
					Groups: map[string]phy.Group{"g1": {PowerNanowatts: 60_000_000_000}},
					Ports: map[string]phy.PsePort{
						"1/1/1": {Group: "g1", Enabled: true, MaxClass: 4, PD: phy.PDAttached, PDClass: phy.Class(4)},
						"1/1/2": {Group: "g1", Enabled: true, MaxClass: 4, PD: phy.PDUnknown},
					},
				},
			},
		},
	}

	sw, err := vswitch.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("vswitch.NewWithSpec() error = %v", err)
	}

	power := sw.Power()
	if got, want := power.Ports["1/1/1"].State, phy.PowerDelivered; got != want {
		t.Errorf("port 1/1/1 state = %v, want %v", got, want)
	}
	if got, want := power.Ports["1/1/2"].State, phy.PowerUnknown; got != want {
		t.Errorf("port 1/1/2 state = %v, want %v", got, want)
	}

	wantEvaluatedScope := analysis.FieldScope(analysis.NodeScope("sw1"), "poe")
	if got := power.Metadata.Scope(); got != wantEvaluatedScope {
		t.Errorf("power.Metadata.Scope() = %v, want %v", got, wantEvaluatedScope)
	}
	if got, want := power.Metadata.Status(), analysis.Incomplete; got != want {
		t.Errorf("power.Metadata.Status() = %v, want %v", got, want)
	}

	issues := power.Metadata.Issues()
	if len(issues) != 1 {
		t.Fatalf("len(power.Metadata.Issues()) = %d, want 1", len(issues))
	}
	issue := issues[0]
	if issue.Code != "poe-demand-unknown" {
		t.Errorf("issue.Code = %q, want %q", issue.Code, "poe-demand-unknown")
	}
	if issue.Status != analysis.Incomplete {
		t.Errorf("issue.Status = %v, want %v", issue.Status, analysis.Incomplete)
	}
	wantPortScope := analysis.FieldScope(analysis.NodeScope("sw1"), "poe", "1/1/2")
	if issue.Scope != wantPortScope {
		t.Errorf("issue.Scope = %v, want %v", issue.Scope, wantPortScope)
	}

	// Forwarding metadata must not contain poe-demand-unknown
	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
	}
	fwd := sw.Receive(fixedTime, "1/1/1", frame)
	for _, fi := range fwd.Metadata.Issues() {
		if fi.Code == "poe-demand-unknown" {
			t.Errorf("forwarding metadata contains poe-demand-unknown: %+v", fi)
		}
	}
}

func TestSwitchLearnForgetAndRelayCounters(t *testing.T) {
	now := time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports := mustTable(t, b)

	t.Run("switch with bridge relay", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports:  ports,
			Bridge: &bridge.Config{},
		}
		sw := mustSwitch(t, cfg)

		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		mustSwitchLearn(t, sw, []bridge.Seed{
			{FID: 0, MAC: mac, Port: "1/1/1", Lifetime: bridge.Static, LearnedAt: now},
		})
		entries := sw.Entries()
		if len(entries) != 1 || entries[0].MAC != mac {
			t.Fatalf("Entries() after Learn = %+v, want 1 entry with MAC %s", entries, mac)
		}

		if !sw.Forget(0, mac) {
			t.Errorf("Forget() = false, want true")
		}
		if entries := sw.Entries(); len(entries) != 0 {
			t.Errorf("Entries() after Forget = %+v, want empty", entries)
		}

		frame := ethernet.Frame{
			Dst: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x66},
			Src: mac,
		}
		sw.Receive(now, "1/1/1", frame)

		counters := sw.RelayCounters()
		if counters.Learned != 1 {
			t.Errorf("RelayCounters().Learned = %d, want 1", counters.Learned)
		}
	})

	t.Run("switch without bridge relay", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: ports,
		}
		sw := mustSwitch(t, cfg)

		mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		err := sw.Learn([]bridge.Seed{
			{FID: 0, MAC: mac, Port: "1/1/1", Lifetime: bridge.Static, LearnedAt: now},
		})
		if err == nil {
			t.Fatal("Learn() on hub = nil error, want bridge-required error")
		}
		if got := errs.Attributes(err)["field"]; got != "seeds" {
			t.Errorf("field = %v, want %q", got, "seeds")
		}
		if sw.Forget(0, mac) {
			t.Errorf("Forget() on hub = true, want false")
		}
		if counters := sw.RelayCounters(); counters != (bridge.Counters{}) {
			t.Errorf("RelayCounters() on hub = %+v, want zero", counters)
		}
	})
}
