package fabric_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/phy"
	"go.aledante.io/FlowSeer/src/common/sim/layer/stp"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/port"
)

// newUnknownUplinkSTPFabric returns two spanning tree switches joined by two
// cables where sw2 reports no Ethernet facts for 1/1/2. The BPDUs the
// switches exchange are protocol journeys, and each carries
// protocol-link-unknown once it is received.
func newUnknownUplinkSTPFabric(t *testing.T) *fabric.Fabric {
	t.Helper()
	ports := func() port.Table {
		tbl, err := port.NewBuilder().Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).Build()
		if err != nil {
			t.Fatal(err)
		}
		return tbl
	}
	gigabit := phy.Ethernet{
		SupportedSpeedsBPS:       []uint64{1_000_000_000},
		AutoNegotiationSupported: phy.CapabilitySupported,
		Setting:                  &phy.Setting{AutoNegotiation: true},
	}
	stpPorts := map[string]stp.Port{"1/1/1": {}, "1/1/2": {}}
	cable := cablemodel.Cable{Medium: cablemodel.TwistedPair}
	fab, err := assembly.New(assembly.Config{
		Start: time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports(), Bridge: &bridge.Config{}, STP: &stp.Config{Priority: 4096, Address: netaddr.MAC{0, 0, 0, 0, 1, 1}, Ports: stpPorts},
				Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit}},
			},
			"sw2": {
				Ports: ports(), Bridge: &bridge.Config{}, STP: &stp.Config{Priority: 8192, Address: netaddr.MAC{0, 0, 0, 0, 1, 2}, Ports: stpPorts},
				Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit}},
			},
		},
		Cables: []fabric.Cable{
			{Cable: cable, A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			{Cable: cable, A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return fab
}

func distinctIssueCodes(issues []analysis.Issue) []analysis.IssueCode {
	var codes []analysis.IssueCode
	for _, iss := range issues {
		codes = append(codes, iss.Code)
	}
	slices.Sort(codes)

	return slices.Compact(codes)
}

func TestSettledProtocolJourneyKeepsItsIssues(t *testing.T) {
	// Budget 8 stops with every protocol journey freed. Budget 12 stops with
	// one in flight. The trust result is the same at both.
	var results []fabric.RunResult
	for _, budget := range []int{8, 12} {
		fab := newUnknownUplinkSTPFabric(t)
		res := fab.Run(fabric.RunOptions{Budget: budget})
		live := 0
		for _, j := range fab.Report() {
			if j.Protocol {
				live++
			}
		}
		if (budget == 8) != (live == 0) {
			t.Fatalf("budget %d left %d live protocol journeys, fixture no longer separates the stops", budget, live)
		}
		if !hasIssueCode(res.Issues, vswitch.IssueProtocolLinkUnknown) {
			t.Errorf("budget %d issues = %v, want %q from the protocol journeys", budget, distinctIssueCodes(res.Issues), vswitch.IssueProtocolLinkUnknown)
		}
		results = append(results, res)
	}
	if results[0].Status != results[1].Status || !slices.Equal(distinctIssueCodes(results[0].Issues), distinctIssueCodes(results[1].Issues)) {
		t.Errorf("trust result differs by stop step: %v %v vs %v %v", results[0].Status, distinctIssueCodes(results[0].Issues), results[1].Status, distinctIssueCodes(results[1].Issues))
	}
}

func TestProtocolIssuesCarryAcrossForkAndAreBounded(t *testing.T) {
	fab := newUnknownUplinkSTPFabric(t)
	fab.Run(fabric.RunOptions{Budget: 60})
	held := len(*fabric.ExportFabricProtocolIssues(fab))
	if held == 0 {
		t.Fatal("no protocol issue stored after the run")
	}
	fab.Run(fabric.RunOptions{Budget: 400})
	if got := len(*fabric.ExportFabricProtocolIssues(fab)); got > held*2 {
		t.Errorf("stored protocol issues grew from %d to %d over 340 more steps, want bounded by the distinct issues", held, got)
	}

	res, err := fab.RunScenario(fabric.Scenario{Name: "carry"}, fabric.RunOptions{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	for i, iss := range res.Issues {
		for _, other := range res.Issues[i+1:] {
			if analysis.SameIssue(iss, other) {
				t.Errorf("issue %q appears twice in the result", iss.Code)
			}
		}
	}
	fork := fab.Fork()
	if !hasIssueCode(fork.Run(fabric.RunOptions{Budget: 0}).Issues, vswitch.IssueProtocolLinkUnknown) {
		t.Error("fork lost the protocol issue")
	}
	if !hasIssueCode(res.Replay.Checkpoint().Run(fabric.RunOptions{Budget: 0}).Issues, vswitch.IssueProtocolLinkUnknown) {
		t.Error("replay checkpoint lost the protocol issue")
	}
}
