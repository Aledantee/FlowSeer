package vswitch

import (
	"fmt"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/layer/filter"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

func TestForkCapabilityMatrix(t *testing.T) {
	const (
		withPhy = 1 << iota
		withLAG
		withSTP
		withLoopProtect
		withMcast
		withRouting
		withFilter
		withTraffic
	)
	base := newTestSwitchForFork(t).Spec().Config
	frames := forkMatrixFrames(t, base.MAC)
	built, skipped, comparisons, filterDrops := 0, 0, 0, 0
	for mask := range 256 {
		if mask&withFilter != 0 && mask&withRouting == 0 {
			skipped++
			continue
		}
		t.Run(fmt.Sprintf("capabilities_%02x", mask), func(t *testing.T) {
			cfg := base.Clone()
			if mask&withPhy == 0 {
				cfg.Phy = nil
			}
			if mask&withLAG == 0 {
				cfg.LAG = nil
				builder := port.NewBuilder()
				for _, p := range cfg.Ports.Ports() {
					if p.Kind != port.LAG && p.LagParent == "" {
						builder.Add(p)
					}
				}
				var err error
				cfg.Ports, err = builder.Build()
				if err != nil {
					t.Fatal(err)
				}
				delete(cfg.Bridge.VLAN.Switchports, "lag1")
			}
			if mask&withSTP == 0 {
				cfg.STP = nil
			}
			if mask&withLoopProtect == 0 {
				cfg.LoopProtect = nil
			}
			if mask&withMcast == 0 {
				cfg.Mcast = nil
			}
			if mask&withRouting == 0 {
				cfg.Routing = nil
			}
			if mask&withFilter == 0 {
				cfg.Filter = nil
			}
			if mask&withTraffic == 0 {
				cfg.Traffic = nil
			}
			source, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			built++
			fork := source.Fork()
			now := time.Unix(1000, 0)
			check := func(name string, got, want Output) {
				t.Helper()
				comparisons++
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: fork output = %+v, source output = %+v", name, got, want)
				}
			}
			check("start", fork.Start(now), source.Start(now))
			for i, probe := range frames {
				at := now.Add(time.Duration(i+1) * time.Second)
				got := fork.Receive(at, probe.ingress, probe.frame)
				want := source.Receive(at, probe.ingress, probe.frame)
				check(probe.name, got, want)
				if probe.name == "routed-ipv4" && mask&withFilter != 0 {
					filterDrops++
					if got.Reason != filter.ReasonFilterDrop || want.Reason != filter.ReasonFilterDrop {
						t.Errorf("filter reasons = %v/%v, want filter-drop/filter-drop", got.Reason, want.Reason)
					}
				}
			}
			second := source.Fork()
			fork.Advance(now.Add(time.Hour))
			at := now.Add(9 * time.Second)
			check("source after fork hour", source.Receive(at, "1/1/1", frames[0].frame), second.Receive(at, "1/1/1", frames[0].frame))
		})
	}
	if built != 192 || skipped != 64 || comparisons != 1920 || filterDrops != 64 {
		t.Fatalf("built/skipped/comparisons/filter drops = %d/%d/%d/%d, want 192/64/1920/64", built, skipped, comparisons, filterDrops)
	}
	t.Logf("%d forks, %d skipped subsets, %d output comparisons, %d concrete filter drops", built, skipped, comparisons, filterDrops)
}

type forkMatrixFrame struct {
	name    string
	ingress string
	frame   ethernet.Frame
}

func forkMatrixFrames(t *testing.T, router netaddr.MAC) []forkMatrixFrame {
	t.Helper()
	src := netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 0x52}
	unicast := ethernet.Frame{Src: src, Dst: netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 0x99}, EtherType: ethernet.EtherTypeIPv4}
	broadcast := unicast
	broadcast.Dst = netaddr.MAC{255, 255, 255, 255, 255, 255}
	group := netip.MustParseAddr("239.1.1.1")
	report, err := igmp.Encode(igmp.Message{Type: igmp.ReportV2, Group: group})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := (ip.Header{Src: netip.MustParseAddr("10.0.10.52"), Dst: group, HopLimit: 1, Protocol: 2, V4: &ip.V4{Options: []byte{0x94, 4, 0, 0}}}).Encode(report)
	if err != nil {
		t.Fatal(err)
	}
	request, err := arp.Encode(arp.Message{HardwareType: 1, ProtocolType: 0x0800, Operation: arp.Request, SenderMAC: src, SenderAddr: netip.MustParseAddr("10.0.10.52"), TargetAddr: netip.MustParseAddr("10.0.10.1")}, broadcast.Dst)
	if err != nil {
		t.Fatal(err)
	}
	bpduFrame, err := bpdu.Encode(bpdu.BPDU{Type: bpdu.TypeRapid, RootID: bpdu.BridgeID{Priority: 4096, Address: src}, BridgeID: bpdu.BridgeID{Priority: 4096, Address: src}, PortID: 0x8001, HelloTime: stp.DefaultHelloTime, MaxAge: stp.DefaultMaxAge, ForwardDelay: stp.DefaultForwardDelay}, src)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := loopprotect.Encode(loopprotect.Probe{OriginMAC: router, VID: 10, Sequence: 1, Port: "1/1/1"}, router)
	if err != nil {
		t.Fatal(err)
	}
	return []forkMatrixFrame{
		{"unicast", "1/1/1", unicast},
		{"broadcast", "1/1/1", broadcast},
		{"igmp-report", "1/1/1", ethernet.Frame{Src: src, Dst: netaddr.MAC{1, 0, 0x5e, 1, 1, 1}, EtherType: ethernet.EtherTypeIPv4, Payload: payload}},
		{"arp-request", "1/1/1", request},
		{"routed-ipv4", "1/1/1", forkFilterFrame(t, router)},
		{"bpdu", "1/1/2", bpduFrame},
		{"lacpdu", "1/1/3", lacp.Encode(lacp.PDU{Actor: lacp.Info{SystemID: src, SystemPriority: 32768, PortPriority: 32768, PortID: 1, Key: 1, State: lacp.StateActive | lacp.StateAggregation}}, src)},
		{"loop-protection", "1/1/1", probe},
	}
}
