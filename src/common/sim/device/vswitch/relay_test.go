package vswitch_test

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestSwitchEgressKeepsClassifiedPCP(t *testing.T) {
	ports := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))
	vid10 := vlan.ID(10)
	sw := mustSwitch(t, vswitch.Config{
		Ports: ports,
		Bridge: &bridge.Config{VLAN: &bridge.VLAN{
			Table: map[vlan.ID]string{10: "vlan10"},
			Switchports: map[string]bridge.Switchport{
				"1/1/1": {Tagged: []vlan.ID{10}},
				"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
			},
		}},
	})
	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		Src:       netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		EtherType: ethernet.EtherTypeIPv4,
		Tags: []vlan.Tag{{
			TPID: uint16(ethernet.EtherTypeDot1Q),
			VID:  10,
			PCP:  5,
		}},
	}

	res := sw.Receive(fixedTime, "1/1/1", frame)
	if len(res.Egress) != 1 {
		t.Fatalf("len(Egress) = %d, want 1", len(res.Egress))
	}
	if got := res.Egress[0].PCP; got != 5 {
		t.Errorf("Egress PCP = %d, want 5", got)
	}
	if len(res.Egress[0].Frame.Tags) != 0 {
		t.Errorf("egress frame tags = %+v, want untagged", res.Egress[0].Frame.Tags)
	}
}

func TestAdvanceAgesTheForwardingDatabase(t *testing.T) {
	tbl := mustTable(t, port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}))

	t.Run("ages dynamic entries", func(t *testing.T) {
		cfg := vswitch.Config{
			Ports: tbl,
			Bridge: &bridge.Config{
				AgingTime: 300 * time.Second,
			},
		}
		sw := mustSwitch(t, cfg)

		t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
		macA := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
		macB := netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}

		sw.Receive(t0, "1/1/1", ethernet.Frame{Dst: macB, Src: macA})

		t200 := t0.Add(200 * time.Second)
		sw.Receive(t200, "1/1/2", ethernet.Frame{Dst: macA, Src: macB})

		if len(sw.Entries()) != 2 {
			t.Fatalf("got %d entries, want 2", len(sw.Entries()))
		}

		t250 := t0.Add(250 * time.Second)
		sw.Advance(t250)
		if len(sw.Entries()) != 2 {
			t.Fatalf("at t0+250s got %d entries, want 2", len(sw.Entries()))
		}

		t301 := t0.Add(301 * time.Second)
		sw.Advance(t301)
		entries := sw.Entries()
		if len(entries) != 1 {
			t.Fatalf("at t0+301s got %d entries, want 1", len(entries))
		}
		if entries[0].MAC != macB {
			t.Errorf("remaining entry MAC = %v, want %v", entries[0].MAC, macB)
		}

		t501 := t0.Add(501 * time.Second)
		sw.Advance(t501)
		if len(sw.Entries()) != 0 {
			t.Fatalf("at t0+501s got %d entries, want 0", len(sw.Entries()))
		}
	})

	t.Run("no-op on hub", func(t *testing.T) {
		cfg := vswitch.Config{Ports: tbl}
		sw := mustSwitch(t, cfg)

		t0 := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
		sw.Advance(t0)
		if entries := sw.Entries(); entries != nil {
			t.Errorf("hub entries = %v, want nil", entries)
		}
	})
}
