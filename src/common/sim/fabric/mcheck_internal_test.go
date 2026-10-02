package fabric

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestFabricMcheckQueuesTheRSTReply(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ports, _ := b.Build()
	cfg := Config{
		Start: t0,
		PhyAssumption: &PhyAssumption{
			Medium: TwistedPair,
			Ethernet: phy.Ethernet{
				SupportedSpeedsBPS:       []uint64{10_000_000, 100_000_000, 1_000_000_000},
				AutoNegotiationSupported: phy.CapabilitySupported,
				Setting:                  &phy.Setting{AutoNegotiation: true},
			},
		},
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  ports,
				Bridge: &bridge.Config{},
				STP: &stp.Config{
					Priority: 32768,
					Address:  netaddr.MAC{0x02, 0, 0, 0, 0, 0x02},
					Ports:    map[string]stp.Port{"1/1/1": {}},
				},
			},
		},
		Hosts:  map[string]Host{"h1": {Address: netaddr.MAC{0x02, 0, 0, 0, 0, 0x11}}},
		Cables: []Cable{{A: Endpoint{Node: "sw1", Port: "1/1/1"}, B: Endpoint{Node: "h1"}, Medium: TwistedPair}},
	}
	fab, err := New(cfg)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	inferior := bpdu.BridgeID{Priority: 61440, Address: netaddr.MAC{0x02, 0, 0, 0, 0, 0x0c}}
	legacy := bpdu.BPDU{
		Type: bpdu.TypeConfiguration, RootID: inferior, BridgeID: inferior, PortID: 0x8001,
		HelloTime: 2 * time.Second, MaxAge: 20 * time.Second, ForwardDelay: 15 * time.Second,
	}
	legacy.SetRole(bpdu.RoleDesignated)
	frame, err := bpdu.Encode(legacy, netaddr.MAC{0x02, 0, 0, 0, 0, 0x0c})
	if err != nil {
		t.Fatalf("bpdu.Encode: %v", err)
	}
	if _, err := fab.Inject(Injection{
		At:     t0.Add(4 * time.Second),
		Origin: Endpoint{Node: "h1"},
		Frame:  frame,
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	for {
		entry, ok := fab.Step()
		if !ok || entry.At.After(t0.Add(5*time.Second)) {
			break
		}
	}
	if fab.Snapshot().Devices["sw1"].Roles["1/1/1"].SendRSTP {
		t.Fatal("the port did not migrate")
	}
	clock := fab.Snapshot().Clock
	if err := fab.mcheck("sw1", "1/1/1"); err != nil {
		t.Fatalf("mcheck: %v", err)
	}
	if !fab.Snapshot().Devices["sw1"].Roles["1/1/1"].SendRSTP {
		t.Fatal("mcheck left the port in compatibility mode")
	}
	var found bool
	for _, j := range fab.Report() {
		if j.Protocol && j.Injection.Origin.Node == "sw1" && j.Injection.At.Equal(clock) {
			decodedBPDU, err := bpdu.Decode(j.Injection.Frame)
			if err == nil && decodedBPDU.Type == bpdu.TypeRapid {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no RST BPDU journey dated the fabric clock %v after mcheck", clock)
	}
	if err := fab.mcheck("h1", ""); err == nil {
		t.Error("mcheck on a host returned no error")
	}
}
