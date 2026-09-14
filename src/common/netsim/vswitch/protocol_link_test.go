package vswitch_test

import (
	"slices"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func TestLinkChangePortStaysUnknown(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	sw, err := vswitch.New(vswitch.Config{Ports: ports, Bridge: &bridge.Config{}})
	if err != nil {
		t.Fatalf("new switch: %v", err)
	}

	t0 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	sw.LinkChange(t0, "1/1/1", port.Unknown, vswitch.PointToPointTrue, 1_000_000_000)

	p, ok := sw.Ports().Port("1/1/1")
	if !ok {
		t.Fatalf("port 1/1/1 not found")
	}
	if p.OperStatus != port.Unknown {
		t.Errorf("port OperStatus = %v, want %v", p.OperStatus, port.Unknown)
	}
}

func TestUnknownUplinkAmongRedundantSTPPaths(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	macRoot := netaddr.MAC{0x00, 0x00, 0x00, 0x00, 0x00, 0x01}
	macHost := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	macDest := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	t0 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	// Switch with STP enabled across all three ports.
	swSTP, err := vswitch.New(vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports: map[string]stp.Port{
				"1/1/1": {PathCost: 20000, PointToPoint: stp.PointToPointForceTrue},
				"1/1/2": {PathCost: 40000, PointToPoint: stp.PointToPointForceTrue},
				"1/1/3": {PathCost: 20000, PointToPoint: stp.PointToPointForceTrue},
			},
		},
	})
	if err != nil {
		t.Fatalf("new stp switch: %v", err)
	}
	if err := swSTP.Learn([]bridge.Seed{{MAC: macDest, Port: "1/1/1", Static: true}}); err != nil {
		t.Fatalf("learn static entry: %v", err)
	}
	swSTP.Start(t0)

	// Elect 1/1/1 as root port and 1/1/2 as alternate port by receiving root BPDUs.
	rootBPDU := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: macRoot},
		RootPathCost: 0,
		BridgeID:     stp.BridgeID{Priority: 4096, Address: macRoot},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		MessageAge:   0,
	}
	swSTP.Forward(t0, "1/1/1", stp.Encode(rootBPDU, macRoot))
	swSTP.Forward(t0, "1/1/2", stp.Encode(rootBPDU, macRoot))

	// Redundant uplink 1/1/2 transitions to Unknown link state.
	swSTP.LinkChange(t0.Add(time.Second), "1/1/2", port.Unknown, vswitch.PointToPointTrue, 1_000_000_000)

	frame := ethernet.Frame{Src: macHost, Dst: macDest, EtherType: ethernet.EtherTypeIPv4}
	resSTP := swSTP.Forward(t0.Add(2*time.Second), "1/1/3", frame)

	if got := resSTP.Metadata.Status(); got != analysis.Incomplete {
		t.Errorf("STP forward status = %v, want %v", got, analysis.Incomplete)
	}
	hasProtocolIssue := slices.ContainsFunc(resSTP.Metadata.Issues(), func(iss analysis.Issue) bool {
		return iss.Code == vswitch.IssueProtocolLinkUnknown
	})
	if !hasProtocolIssue {
		t.Errorf("STP forward issues = %v, want issue with code %q", resSTP.Metadata.Issues(), vswitch.IssueProtocolLinkUnknown)
	}

	// Identical topology without STP: forward result stays Complete.
	swNoSTP, err := vswitch.New(vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
	})
	if err != nil {
		t.Fatalf("new non-stp switch: %v", err)
	}
	if err := swNoSTP.Learn([]bridge.Seed{{MAC: macDest, Port: "1/1/1", Static: true}}); err != nil {
		t.Fatalf("learn static entry on non-stp: %v", err)
	}
	swNoSTP.Start(t0)
	swNoSTP.LinkChange(t0.Add(time.Second), "1/1/2", port.Unknown, vswitch.PointToPointTrue, 1_000_000_000)

	resNoSTP := swNoSTP.Forward(t0.Add(2*time.Second), "1/1/3", frame)
	if got := resNoSTP.Metadata.Status(); got != analysis.Complete {
		t.Errorf("non-STP forward status = %v, want %v", got, analysis.Complete)
	}
}

func TestUnknownLAGMemberDowngradesOnlyLAG(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	macLagDest := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macPort4Dest := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0b}
	macSrc := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}

	sw, err := vswitch.New(vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		LAG: &lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {LACP: lag.LACPConfig{Mode: lag.Off}},
			},
		},
	})
	if err != nil {
		t.Fatalf("new switch: %v", err)
	}
	if err := sw.Learn([]bridge.Seed{
		{Port: "lag1", MAC: macLagDest, Static: true},
		{Port: "1/1/4", MAC: macPort4Dest, Static: true},
	}); err != nil {
		t.Fatalf("learn seeds: %v", err)
	}

	t0 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	sw.Start(t0)

	// Member 1/1/2 becomes Unknown. Member 1/1/1 is still Up, so lag1 remains forwarding.
	sw.LinkChange(t0.Add(time.Second), "1/1/2", port.Unknown, vswitch.PointToPointTrue, 1_000_000_000)

	// Traffic destined to lag1 consults lag1 and its members -> Incomplete with protocol-link-unknown.
	frameLag := ethernet.Frame{Src: macSrc, Dst: macLagDest, EtherType: ethernet.EtherTypeIPv4}
	resLag := sw.Forward(t0.Add(2*time.Second), "1/1/3", frameLag)

	if got := resLag.Metadata.Status(); got != analysis.Incomplete {
		t.Errorf("traffic through LAG status = %v, want %v", got, analysis.Incomplete)
	}
	hasProtocolIssue := slices.ContainsFunc(resLag.Metadata.Issues(), func(iss analysis.Issue) bool {
		return iss.Code == vswitch.IssueProtocolLinkUnknown
	})
	if !hasProtocolIssue {
		t.Errorf("traffic through LAG missing %q issue: %v", vswitch.IssueProtocolLinkUnknown, resLag.Metadata.Issues())
	}

	// Traffic destined to 1/1/4 does not consult lag1 -> Complete.
	frameOther := ethernet.Frame{Src: macSrc, Dst: macPort4Dest, EtherType: ethernet.EtherTypeIPv4}
	resOther := sw.Forward(t0.Add(2*time.Second), "1/1/3", frameOther)

	if got := resOther.Metadata.Status(); got != analysis.Complete {
		t.Errorf("traffic through other port status = %v, want %v", got, analysis.Complete)
	}
}

func TestLaterUpReportClearsIssue(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	macLagDest := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a}
	macSrc := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}

	sw, err := vswitch.New(vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		LAG: &lag.Config{
			LAGs: map[string]lag.LAG{
				"lag1": {LACP: lag.LACPConfig{Mode: lag.Off}},
			},
		},
	})
	if err != nil {
		t.Fatalf("new switch: %v", err)
	}
	if err := sw.Learn([]bridge.Seed{
		{Port: "lag1", MAC: macLagDest, Static: true},
	}); err != nil {
		t.Fatalf("learn seed: %v", err)
	}

	t0 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	sw.Start(t0)

	// Member 1/1/2 becomes Unknown.
	sw.LinkChange(t0.Add(time.Second), "1/1/2", port.Unknown, vswitch.PointToPointTrue, 1_000_000_000)

	frame := ethernet.Frame{Src: macSrc, Dst: macLagDest, EtherType: ethernet.EtherTypeIPv4}
	resIncomplete := sw.Forward(t0.Add(2*time.Second), "1/1/3", frame)
	if resIncomplete.Metadata.Status() != analysis.Incomplete {
		t.Fatalf("expected Incomplete while member is unknown, got %v", resIncomplete.Metadata.Status())
	}

	// Member 1/1/2 receives a known Up report -> clears the issue.
	sw.LinkChange(t0.Add(3*time.Second), "1/1/2", port.Up, vswitch.PointToPointTrue, 1_000_000_000)

	resCleared := sw.Forward(t0.Add(4*time.Second), "1/1/3", frame)
	if got := resCleared.Metadata.Status(); got != analysis.Complete {
		t.Errorf("status after known Up report = %v, want %v", got, analysis.Complete)
	}
	for _, iss := range resCleared.Metadata.Issues() {
		if iss.Code == vswitch.IssueProtocolLinkUnknown {
			t.Errorf("unexpected issue %q after known Up report", iss.Code)
		}
	}
}

func TestSTPUnknownDuplexPointToPoint(t *testing.T) {
	t.Parallel()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	macDest := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x02}
	macSrc := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}
	t0 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)

	// STP with Auto point-to-point (rests on duplex).
	swAuto, err := vswitch.New(vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports: map[string]stp.Port{
				"1/1/1": {PathCost: 20000, PointToPoint: stp.PointToPointAuto},
				"1/1/2": {PathCost: 20000, PointToPoint: stp.PointToPointAuto},
			},
		},
	})
	if err != nil {
		t.Fatalf("new switch: %v", err)
	}
	if err := swAuto.Learn([]bridge.Seed{
		{Port: "1/1/2", MAC: macDest, Static: true},
	}); err != nil {
		t.Fatalf("learn seed: %v", err)
	}
	swAuto.Start(t0)

	// 1/1/1 has unknown duplex -> point-to-point status is unknown.
	swAuto.LinkChange(t0.Add(time.Second), "1/1/1", port.Up, vswitch.PointToPointUnknown, 1_000_000_000)

	frame := ethernet.Frame{Src: macSrc, Dst: macDest, EtherType: ethernet.EtherTypeIPv4}
	resAuto := swAuto.Forward(t0.Add(2*time.Second), "1/1/1", frame)
	if got := resAuto.Metadata.Status(); got != analysis.Incomplete {
		t.Errorf("status with unknown duplex = %v, want %v", got, analysis.Incomplete)
	}
	hasProtocolIssue := slices.ContainsFunc(resAuto.Metadata.Issues(), func(iss analysis.Issue) bool {
		return iss.Code == vswitch.IssueProtocolLinkUnknown
	})
	if !hasProtocolIssue {
		t.Errorf("missing %q issue with unknown duplex", vswitch.IssueProtocolLinkUnknown)
	}

	// STP with ForceTrue point-to-point -> point-to-point does not rest on duplex.
	swForced, err := vswitch.New(vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports: map[string]stp.Port{
				"1/1/1": {PathCost: 20000, PointToPoint: stp.PointToPointForceTrue},
				"1/1/2": {PathCost: 20000, PointToPoint: stp.PointToPointForceTrue},
			},
		},
	})
	if err != nil {
		t.Fatalf("new forced switch: %v", err)
	}
	if err := swForced.Learn([]bridge.Seed{
		{Port: "1/1/2", MAC: macDest, Static: true},
	}); err != nil {
		t.Fatalf("learn seed: %v", err)
	}
	swForced.Start(t0)

	swForced.LinkChange(t0.Add(time.Second), "1/1/1", port.Up, vswitch.PointToPointUnknown, 1_000_000_000)
	resForced := swForced.Forward(t0.Add(2*time.Second), "1/1/1", frame)
	if got := resForced.Metadata.Status(); got != analysis.Complete {
		t.Errorf("status with forced point-to-point = %v, want %v", got, analysis.Complete)
	}
}

// newAutoSTPSwitch builds a three-port bridge whose spanning tree carries the
// given per-port configuration; ports it leaves out run on defaults.
func newAutoSTPSwitch(t *testing.T, stpPorts map[string]stp.Port) *vswitch.Switch {
	t.Helper()

	ports, err := port.NewBuilder().
		Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Add(port.Port{Name: "1/1/3", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up}).
		Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}
	sw, err := vswitch.New(vswitch.Config{
		Ports:  ports,
		Bridge: &bridge.Config{},
		STP: &stp.Config{
			Priority: 32768,
			Ports:    stpPorts,
		},
	})
	if err != nil {
		t.Fatalf("new switch: %v", err)
	}

	return sw
}

// allAutoSTPPorts configures every port of newAutoSTPSwitch with automatic
// point-to-point detection.
func allAutoSTPPorts() map[string]stp.Port {
	return map[string]stp.Port{
		"1/1/1": {PathCost: 20000, PointToPoint: stp.PointToPointAuto},
		"1/1/2": {PathCost: 20000, PointToPoint: stp.PointToPointAuto},
		"1/1/3": {PathCost: 20000, PointToPoint: stp.PointToPointAuto},
	}
}

func hasProtocolLinkIssue(res vswitch.ForwardResult) bool {
	return slices.ContainsFunc(res.Metadata.Issues(), func(iss analysis.Issue) bool {
		return iss.Code == vswitch.IssueProtocolLinkUnknown
	})
}

// Spanning tree computes roles on every non-member port, so an unknown link on
// a port without explicit spanning tree configuration still makes results
// through the others incomplete.
func TestUnknownLinkOnUnconfiguredSTPPortMarksSpanningTreePorts(t *testing.T) {
	t.Parallel()

	sw := newAutoSTPSwitch(t, map[string]stp.Port{"1/1/1": {PathCost: 20000}})
	t0 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	sw.Start(t0)
	sw.LinkChange(t0, "1/1/2", port.Unknown, vswitch.PointToPointFalse, 0)

	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		EtherType: ethernet.EtherTypeIPv4,
	}
	res := sw.Forward(t0.Add(time.Second), "1/1/3", frame)
	if !hasProtocolLinkIssue(res) {
		t.Errorf("issues = %v, want %q", res.Metadata.Issues(), vswitch.IssueProtocolLinkUnknown)
	}
}

// An unset point-to-point report says nothing about the link, so it must count
// as unknown on a port whose spanning tree point-to-point mode is automatic.
func TestLinkChangeUnsetPointToPointIsUnknown(t *testing.T) {
	t.Parallel()

	sw := newAutoSTPSwitch(t, allAutoSTPPorts())
	t0 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	sw.Start(t0)
	sw.LinkChange(t0, "1/1/2", port.Up, "", 1_000_000_000)

	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		EtherType: ethernet.EtherTypeIPv4,
	}
	res := sw.Forward(t0.Add(time.Second), "1/1/1", frame)
	if !hasProtocolLinkIssue(res) {
		t.Errorf("issues = %v, want %q", res.Metadata.Issues(), vswitch.IssueProtocolLinkUnknown)
	}
}

// Derive retains spanning tree state together with its point-to-point
// reports, so an unknown duplex heard before derivation stays visible after it.
func TestDeriveKeepsUnknownPointToPointIssue(t *testing.T) {
	t.Parallel()

	sw := newAutoSTPSwitch(t, allAutoSTPPorts())
	t0 := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	sw.Start(t0)
	sw.LinkChange(t0, "1/1/2", port.Up, vswitch.PointToPointUnknown, 1_000_000_000)

	frame := ethernet.Frame{
		Src:       netaddr.MAC{0x02, 0, 0, 0, 0, 1},
		Dst:       netaddr.MAC{0x02, 0, 0, 0, 0, 2},
		EtherType: ethernet.EtherTypeIPv4,
	}
	if !hasProtocolLinkIssue(sw.Peek(t0.Add(time.Second), "1/1/1", frame)) {
		t.Fatalf("precondition: current switch carries no %q issue", vswitch.IssueProtocolLinkUnknown)
	}

	next, err := vswitch.Derive(sw, sw.Spec())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	res := next.Forward(t0.Add(time.Second), "1/1/1", frame)
	if !hasProtocolLinkIssue(res) {
		t.Errorf("derived issues = %v, want %q", res.Metadata.Issues(), vswitch.IssueProtocolLinkUnknown)
	}
}
