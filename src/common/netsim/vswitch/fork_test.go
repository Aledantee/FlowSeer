package vswitch

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/arp"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/igmp"
	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

var switchDeepCopiedProbes = map[string]func(t *testing.T){
	"bridge":           probeSwitchBridge,
	"stp":              probeSwitchSTP,
	"loopprotect":      probeSwitchLoopProtect,
	"lag":              probeSwitchLAG,
	"mcast":            probeSwitchMcast,
	"routing":          probeSwitchRouting,
	"buckets":          probeSwitchBuckets,
	"copies":           probeSwitchCopies,
	"emissions":        probeSwitchEmissions,
	"portP2P":          probeSwitchPortP2P,
	"portSpeed":        probeSwitchPortSpeed,
	"protocolIssues":   probeSwitchProtocolIssues,
	"seeds":            probeSwitchSeeds,
	"operErr":          probeSwitchOperErr,
	"neighborFailures": probeSwitchNeighborFailures,
	"retention":        probeSwitchRetention,
}

func probeSwitchBridge(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	macSrc := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x11}
	macFork := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x22}

	if err := sw.Learn([]bridge.Seed{{FID: 10, MAC: macSrc, Port: "1/1/1", Lifetime: bridge.Aging}}); err != nil {
		t.Fatalf("sw.Learn: %v", err)
	}
	if fork.Forget(10, macSrc) {
		t.Errorf("fork saw entry learned on source")
	}

	if err := fork.Learn([]bridge.Seed{{FID: 10, MAC: macFork, Port: "1/1/2", Lifetime: bridge.Aging}}); err != nil {
		t.Fatalf("fork.Learn: %v", err)
	}
	if sw.Forget(10, macFork) {
		t.Errorf("source saw entry learned on fork")
	}
}

func probeSwitchSTP(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	// LinkChange on source moves STP state
	sw.LinkChange(time.Unix(2000, 0), "1/1/1", port.Down, PointToPointFalse, 0)
	if fork.Roles()["1/1/1"].Role == stp.RoleDisabled {
		t.Errorf("fork STP port role moved when source changed link")
	}

	fork.LinkChange(time.Unix(2000, 0), "1/1/2", port.Down, PointToPointFalse, 0)
	if sw.Roles()["1/1/2"].Role == stp.RoleDisabled {
		t.Errorf("source STP port role moved when fork changed link")
	}
}

func probeSwitchLoopProtect(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	now := time.Unix(3000, 0)
	sw.LinkChange(now, "1/1/1", port.Down, PointToPointTrue, 1_000_000_000)
	if sw.loopprotect == nil || fork.loopprotect == nil {
		t.Fatalf("loopprotect nil")
	}
	// Verify pointers to loopprotect layers are distinct
	if sw.loopprotect == fork.loopprotect {
		t.Errorf("loopprotect pointer aliased across fork")
	}
}

func probeSwitchLAG(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	now := time.Unix(4000, 0)
	// Mutate source LAG
	sw.LinkChange(now, "1/1/3", port.Down, PointToPointTrue, 1_000_000_000)
	if fork.lag == nil || sw.lag == nil {
		t.Fatalf("lag nil")
	}
	if sw.lag == fork.lag {
		t.Errorf("lag pointer aliased across fork")
	}
}

func probeSwitchMcast(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	now := time.Unix(5000, 0)
	group := netip.MustParseAddr("239.1.1.1")
	igmpPayload, err := igmp.Encode(igmp.Message{Type: igmp.ReportV2, Group: group})
	if err != nil {
		t.Fatalf("igmp.Encode: %v", err)
	}
	hdr := ip.Header{
		Src:      netip.MustParseAddr("10.0.10.50"),
		Dst:      group,
		HopLimit: 1,
		Protocol: 2,
		V4:       &ip.V4{Options: []byte{0x94, 0x04, 0x00, 0x00}},
	}
	payload, err := hdr.Encode(igmpPayload)
	if err != nil {
		t.Fatalf("hdr.Encode: %v", err)
	}
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x50},
		Dst:       netaddr.MAC{0x01, 0x00, 0x5e, 0x01, 0x01, 0x01},
		EtherType: ethernet.EtherTypeIPv4,
		Payload:   payload,
	}

	sw.Forward(now, "1/1/1", frame)
	if len(fork.Groups(10)) != 0 {
		t.Errorf("fork learned multicast group from source IGMP forward")
	}

	fork.Forward(now, "1/1/2", frame)
	// Assert fork now has the group, but source doesn't have 1/1/2 membership
	for _, g := range sw.Groups(10) {
		if g.Port == "1/1/2" {
			t.Errorf("source learned port 1/1/2 membership from fork")
		}
	}
}

func probeSwitchRouting(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	now := time.Unix(6000, 0)
	// Mutate source routing via ARP reply
	frame, err := arp.Encode(arp.Message{
		HardwareType: 1,
		ProtocolType: 0x0800,
		Operation:    arp.Reply,
		SenderMAC:    netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
		SenderAddr:   netip.MustParseAddr("10.0.10.99"),
		TargetMAC:    netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		TargetAddr:   netip.MustParseAddr("10.0.10.1"),
	}, netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01})
	if err != nil {
		t.Fatalf("arp.Encode: %v", err)
	}

	sw.Forward(now, "1/1/1", frame)
	// Fork neighbor table should not see this ARP
	for _, n := range fork.Neighbors() {
		if n.Addr == netip.MustParseAddr("10.0.10.99") {
			t.Errorf("fork saw neighbor learned on source")
		}
	}
}

func probeSwitchBuckets(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	now := time.Unix(7000, 0)
	// Mutate source bucket capacity
	if !sw.Police(now, "1/1/1", 400) {
		t.Errorf("police rejected on source")
	}
	// Mutating fork
	if !fork.Police(now, "1/1/1", 400) {
		t.Errorf("police rejected on fork")
	}
}

func probeSwitchCopies(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	swCopies := sw.Copies()
	if len(swCopies) == 0 {
		t.Errorf("source had no copies before clear")
	}
	forkCopies := fork.Copies()
	if len(forkCopies) == 0 {
		t.Errorf("fork had no copies before clear")
	}
	// Now source is cleared
	if len(sw.Copies()) != 0 {
		t.Errorf("source copies not cleared")
	}
	// Mutating fork copies
	fork.copies = []traffic.Copy{{Port: "1/1/1"}}
	if len(sw.Copies()) != 0 {
		t.Errorf("mutating fork copies affected source")
	}
}

func probeSwitchEmissions(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	srcDrained := sw.Drain()
	if len(srcDrained) == 0 {
		t.Errorf("source had no emissions")
	}
	// Fork emissions should still be intact
	forkDrained := fork.Drain()
	if len(forkDrained) == 0 {
		t.Errorf("fork emissions drained when source drained")
	}
}

func probeSwitchPortP2P(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	now := time.Unix(8000, 0)
	sw.LinkChange(now, "1/1/1", port.Up, PointToPointFalse, 1_000_000_000)
	if fork.portP2P["1/1/1"] != PointToPointTrue {
		t.Errorf("fork portP2P changed when source changed: got %v, want %v", fork.portP2P["1/1/1"], PointToPointTrue)
	}

	fork.LinkChange(now, "1/1/1", port.Up, PointToPointTrue, 1_000_000_000)
	if sw.portP2P["1/1/1"] != PointToPointFalse {
		t.Errorf("source portP2P changed when fork changed: got %v, want %v", sw.portP2P["1/1/1"], PointToPointFalse)
	}
}

func probeSwitchPortSpeed(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	now := time.Unix(8000, 0)
	sw.LinkChange(now, "1/1/1", port.Up, PointToPointTrue, 10_000_000_000)
	if fork.portSpeed["1/1/1"] != 1_000_000_000 {
		t.Errorf("fork portSpeed changed when source changed: got %d, want 1000000000", fork.portSpeed["1/1/1"])
	}

	fork.LinkChange(now, "1/1/1", port.Up, PointToPointTrue, 2_500_000_000)
	if sw.portSpeed["1/1/1"] != 10_000_000_000 {
		t.Errorf("source portSpeed changed when fork changed: got %d, want 10000000000", sw.portSpeed["1/1/1"])
	}
}

func probeSwitchProtocolIssues(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	sw.protocolIssues["1/1/1"] = analysis.Issue{Code: "source-issue"}
	if fork.protocolIssues["1/1/1"].Code == "source-issue" {
		t.Errorf("fork protocolIssues changed when source mutated")
	}

	fork.protocolIssues["1/1/1"] = analysis.Issue{Code: "fork-issue"}
	if sw.protocolIssues["1/1/1"].Code != "source-issue" {
		t.Errorf("source protocolIssues changed when fork mutated")
	}
}

func probeSwitchSeeds(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	// "seeds gets its own probe: Learn on the fork, then the source's Spec().Seeds is unchanged."
	initialSeeds := len(sw.Spec().Seeds)
	newSeed := bridge.Seed{FID: 10, MAC: netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x55}, Port: "1/1/1", Lifetime: bridge.Static}
	if err := fork.Learn([]bridge.Seed{newSeed}); err != nil {
		t.Fatalf("fork.Learn: %v", err)
	}

	if len(sw.Spec().Seeds) != initialSeeds {
		t.Errorf("Learn on fork changed source Spec().Seeds: got %d, want %d", len(sw.Spec().Seeds), initialSeeds)
	}

	// And vice-versa: Learn on source, then fork's Spec().Seeds is unchanged.
	forkSeeds := len(fork.Spec().Seeds)
	srcSeed := bridge.Seed{FID: 10, MAC: netaddr.MAC{0x00, 0x99, 0x88, 0x77, 0x66, 0x44}, Port: "1/1/1", Lifetime: bridge.Static}
	if err := sw.Learn([]bridge.Seed{srcSeed}); err != nil {
		t.Fatalf("sw.Learn: %v", err)
	}
	if len(fork.Spec().Seeds) != forkSeeds {
		t.Errorf("Learn on source changed fork Spec().Seeds: got %d, want %d", len(fork.Spec().Seeds), forkSeeds)
	}
}

func probeSwitchOperErr(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	sw.operErr = errors.New("source-oper-err")
	if fork.Err() != nil {
		t.Errorf("fork has error when source operErr set: %v", fork.Err())
	}

	fork.operErr = errors.New("fork-oper-err")
	if sw.Err().Error() != "source-oper-err" {
		t.Errorf("source error changed when fork operErr set: %v", sw.Err())
	}
}

func probeSwitchNeighborFailures(t *testing.T) {
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	srcFailures := sw.DrainNeighborFailures()
	if len(srcFailures) == 0 {
		t.Errorf("source had no neighbor failures")
	}
	forkFailures := fork.DrainNeighborFailures()
	if len(forkFailures) == 0 {
		t.Errorf("fork neighbor failures drained when source drained")
	}
}

func TestForkBackPointerLagMemberRemoved(t *testing.T) {
	// "The back-pointer probe removes a LAG member on the source and asserts the fork selects from its own port table."
	sw := newTestSwitchForFork(t)
	fork := sw.Fork()

	now := time.Unix(9000, 0)
	// Bring down 1/1/3 on source
	sw.LinkChange(now, "1/1/3", port.Down, PointToPointTrue, 1_000_000_000)

	// Source LAG has only 1/1/4 forwarding
	srcMem, ok := sw.ports.Port("1/1/3")
	if !ok || srcMem.Forwards() {
		t.Errorf("source 1/1/3 should not be forwarding")
	}

	// Fork LAG should still have 1/1/3 forwarding
	forkMem, ok := fork.ports.Port("1/1/3")
	if !ok || !forkMem.Forwards() {
		t.Errorf("fork 1/1/3 should still be forwarding")
	}

	// Forward packet over lag1 on fork
	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
		Dst:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x99},
		EtherType: ethernet.EtherTypeIPv4,
	}
	res := fork.Forward(now, "1/1/1", frame)
	if res.Outcome != trace.Forwarded {
		t.Errorf("fork forward outcome = %v, want Forwarded", res.Outcome)
	}
}

func probeSwitchRetention(t *testing.T) {
	sw := newTestSwitchForFork(t)
	sw.retention = Retention{
		STP: LayerRetention{Kept: false, Difference: "port-state"},
	}
	fork := sw.Fork()
	if !fork.Retention().STP.Kept {
		t.Errorf("fork STP kept = false, want true")
	}
	if sw.Retention().STP.Kept {
		t.Errorf("source STP kept = true, want false")
	}
}
