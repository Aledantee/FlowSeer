package fabric_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func autoEthernet(speeds ...uint64) phy.Ethernet {
	return phy.Ethernet{
		SupportedSpeedsBPS:       speeds,
		AutoNegotiationSupported: phy.CapabilitySupported,
		Setting:                  &phy.Setting{AutoNegotiation: true},
	}
}

func forcedEthernet(speed uint64, duplex phy.Duplex) phy.Ethernet {
	return phy.Ethernet{
		SupportedSpeedsBPS: []uint64{speed},
		Setting:            &phy.Setting{SpeedBPS: speed, Duplex: duplex},
	}
}

// switchPair is two one-port switches joined by cable, each port with the given facts.
func switchPair(t *testing.T, ethA, ethB phy.Ethernet, cable fabric.Cable) fabric.Config {
	t.Helper()
	sw := func(eth phy.Ethernet) vswitch.Config {
		return vswitch.Config{
			Ports: mustTable(t, port.NewBuilder().Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
			Phy:   &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": eth}},
		}
	}
	cable.A = fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	cable.B = fabric.Endpoint{Node: "sw2", Port: "1/1/1"}

	return fabric.Config{
		Switches: map[string]vswitch.Config{"sw1": sw(ethA), "sw2": sw(ethB)},
		Cables:   []fabric.Cable{cable},
	}
}

const pairLinkKey = "sw1:1/1/1-sw2:1/1/1"

func issueCodes(issues []analysis.Issue) []analysis.IssueCode {
	codes := make([]analysis.IssueCode, 0, len(issues))
	for _, issue := range issues {
		codes = append(codes, issue.Code)
	}

	return codes
}

func TestLinkReachFollowsItsTable(t *testing.T) {
	const (
		m100 = uint64(100_000_000)
		g1   = uint64(1_000_000_000)
		g10  = uint64(10_000_000_000)
		g25  = uint64(25_000_000_000)
	)
	delay := time.Microsecond

	tests := []struct {
		name       string
		ethA, ethB phy.Ethernet
		cable      fabric.Cable
		wantOper   port.LinkState
		wantReason trace.Reason
		wantSpeed  uint64
		wantSource phy.Source
	}{
		{
			name: "stated medium in range at the negotiated speed is Up",
			ethA: autoEthernet(g1), ethB: autoEthernet(g1),
			cable:    fabric.Cable{LengthMeters: 90, Medium: fabric.TwistedPair},
			wantOper: port.Up, wantSpeed: g1, wantSource: phy.SourceNegotiated,
		},
		{
			name: "every candidate exceeded is Down reach-exceeded",
			ethA: autoEthernet(g1, g10), ethB: autoEthernet(g1, g10),
			cable:    fabric.Cable{LengthMeters: 600, Medium: fabric.MultimodeFiber},
			wantOper: port.Down, wantReason: fabric.ReasonReachExceeded,
		},
		{
			name: "an exceeded higher candidate is removed before negotiation",
			ethA: autoEthernet(g1, g10), ethB: autoEthernet(g1, g10),
			cable:    fabric.Cable{LengthMeters: 400, Medium: fabric.MultimodeFiber},
			wantOper: port.Up, wantSpeed: g1, wantSource: phy.SourceNegotiated,
		},
		{
			name: "the negotiated speed without a row is Unknown reach-unknown",
			ethA: autoEthernet(g1, g25), ethB: autoEthernet(g1, g25),
			cable:    fabric.Cable{LengthMeters: 5, Medium: fabric.TwistedPair},
			wantOper: port.Unknown, wantReason: fabric.ReasonReachUnknown,
		},
		{
			name: "the remaining lower candidate without a row is Unknown reach-unknown",
			ethA: autoEthernet(m100, g10), ethB: autoEthernet(m100, g10),
			cable:    fabric.Cable{LengthMeters: 400, Medium: fabric.MultimodeFiber},
			wantOper: port.Unknown, wantReason: fabric.ReasonReachUnknown,
		},
		{
			name: "a speed above the top speed is no candidate",
			ethA: autoEthernet(g1, g25), ethB: autoEthernet(g1, g25),
			cable:    fabric.Cable{LengthMeters: 5, Medium: fabric.TwistedPair, TopSpeedBPS: g1},
			wantOper: port.Up, wantSpeed: g1, wantSource: phy.SourceNegotiated,
		},
		{
			name: "a forced speed past reach is Down reach-exceeded",
			ethA: forcedEthernet(g10, phy.Full), ethB: autoEthernet(g1, g10),
			cable:    fabric.Cable{LengthMeters: 400, Medium: fabric.MultimodeFiber},
			wantOper: port.Down, wantReason: fabric.ReasonReachExceeded,
		},
		{
			name: "a delay does not resolve an unspecified medium",
			ethA: autoEthernet(g1), ethB: autoEthernet(g1),
			cable:    fabric.Cable{LengthMeters: 2, Delay: &delay},
			wantOper: port.Unknown, wantReason: fabric.ReasonReachUnknown,
		},
		{
			name: "matching observations resolve an unknown reach",
			ethA: func() phy.Ethernet {
				e := autoEthernet(g1)
				e.Observed = &phy.Observed{SpeedBPS: g1, Duplex: phy.Full}
				return e
			}(),
			ethB: func() phy.Ethernet {
				e := autoEthernet(g1)
				e.Observed = &phy.Observed{SpeedBPS: g1, Duplex: phy.Full}
				return e
			}(),
			cable:    fabric.Cable{LengthMeters: 2},
			wantOper: port.Up, wantSpeed: g1, wantSource: phy.SourceObserved,
		},
		{
			name: "no candidate leaves negotiation to decide",
			ethA: phy.Ethernet{}, ethB: autoEthernet(g1),
			cable:    fabric.Cable{LengthMeters: 400, Medium: fabric.TwistedPair},
			wantOper: port.Unknown, wantReason: phy.ReasonCapabilityUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fab, err := fabric.New(switchPair(t, tc.ethA, tc.ethB, tc.cable))
			if err != nil {
				t.Fatalf("New fabric: %v", err)
			}

			l := fab.Links()[0]
			for _, end := range []fabric.LinkEnd{l.A, l.B} {
				if end.Oper != tc.wantOper || end.Reason != tc.wantReason {
					t.Errorf("end %v = %s %q, want %s %q", end.Endpoint, end.Oper, end.Reason, tc.wantOper, tc.wantReason)
				}
				if end.Speed.SpeedBPS != tc.wantSpeed || end.Speed.Source != tc.wantSource {
					t.Errorf("end %v speed = %+v, want %d from %q", end.Endpoint, end.Speed, tc.wantSpeed, tc.wantSource)
				}
			}

			linkIssues := fab.Metadata().IssuesFor(analysis.LinkScope(pairLinkKey))
			reachIssue := slices.ContainsFunc(linkIssues, func(issue analysis.Issue) bool {
				return issue.Code == analysis.IssueCode(fabric.ReasonReachUnknown)
			})
			if want := tc.wantReason == fabric.ReasonReachUnknown; reachIssue != want {
				t.Errorf("link issues = %+v, reach-unknown present %t, want %t", linkIssues, reachIssue, want)
			}
		})
	}
}

func TestUnspecifiedMediumBetweenGigabitEndsIsReachUnknownNotExceeded(t *testing.T) {
	catalog, cableRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "inventory", Origin: "cable-plan", Context: "sw1 1/1/1"})
	cfg := switchPair(t, autoEthernet(1_000_000_000), autoEthernet(1_000_000_000), fabric.Cable{
		LengthMeters: 2,
		Evidence:     []trace.EvidenceRef{cableRef},
	})
	spec := constructionSpec(cfg)
	spec.Evidence = catalog

	fab, err := fabric.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	l := fab.Links()[0]
	if l.A.Oper != port.Unknown || l.A.Reason != fabric.ReasonReachUnknown || l.B.Oper != port.Unknown {
		t.Errorf("link ends = %+v / %+v, want both Unknown with reach-unknown", l.A, l.B)
	}
	if l.A.Reason == fabric.ReasonReachExceeded {
		t.Error("an unspecified medium reads reach-exceeded")
	}
	for _, name := range []string{"sw1", "sw2"} {
		if p, _ := fab.Switch(name).Ports().Port("1/1/1"); p.OperStatus != port.Unknown {
			t.Errorf("%s port OperStatus = %s, want Unknown", name, p.OperStatus)
		}
	}

	meta := fab.Metadata()
	issues := meta.IssuesFor(analysis.LinkScope(pairLinkKey))
	if len(issues) != 1 {
		t.Fatalf("link issues = %+v, want one", issues)
	}
	issue := issues[0]
	if issue.Code != analysis.IssueCode(fabric.ReasonReachUnknown) || issue.Status != analysis.Incomplete {
		t.Errorf("link issue = %+v, want Incomplete reach-unknown", issue)
	}
	if !slices.Equal(issue.Evidence, []trace.EvidenceRef{cableRef}) {
		t.Errorf("link issue evidence = %v, want the cable's %v", issue.Evidence, cableRef)
	}
	if _, ok := meta.Evidence().Lookup(cableRef); !ok {
		t.Errorf("metadata evidence lacks the cable reference %v", cableRef)
	}
	if got := meta.Status(); got != analysis.Incomplete {
		t.Errorf("fabric status = %s, want incomplete", got)
	}
}

func TestObservedLinkOverUnspecifiedMediumReportsUnknownPropagation(t *testing.T) {
	observed := autoEthernet(1_000_000_000)
	observed.Observed = &phy.Observed{SpeedBPS: 1_000_000_000, Duplex: phy.Full}

	fab, err := fabric.New(switchPair(t, observed, observed, fabric.Cable{LengthMeters: 2}))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	if got := issueCodes(fab.Metadata().IssuesFor(analysis.LinkScope(pairLinkKey))); !slices.Equal(got, []analysis.IssueCode{fabric.IssuePropagationUnknown}) {
		t.Errorf("link issue codes = %v, want [%s]", got, fabric.IssuePropagationUnknown)
	}

	delay := time.Nanosecond
	withDelay, err := fabric.New(switchPair(t, observed, observed, fabric.Cable{LengthMeters: 2, Delay: &delay}))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	if issues := withDelay.Metadata().Issues(); len(issues) != 0 {
		t.Errorf("issues with a stated delay = %+v, want none", issues)
	}
}

func TestObservedSpeedThatDisagreesWithNegotiationConflicts(t *testing.T) {
	observed := autoEthernet(100_000_000, 1_000_000_000)
	observed.Observed = &phy.Observed{SpeedBPS: 100_000_000, Duplex: phy.Full}

	fab, err := fabric.New(switchPair(t, observed, autoEthernet(100_000_000, 1_000_000_000), fabric.Cable{Medium: fabric.TwistedPair}))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	if l := fab.Links()[0]; l.A.Oper != port.Up || l.A.Speed.SpeedBPS != 1_000_000_000 {
		t.Fatalf("link end A = %+v, want Up at 1 Gb/s", l.A)
	}
	meta := fab.Metadata()
	if got := issueCodes(meta.IssuesFor(analysis.PortScope("sw1", "1/1/1"))); !slices.Equal(got, []analysis.IssueCode{fabric.IssueObservedSpeedConflict}) {
		t.Errorf("sw1 port issue codes = %v, want [%s]", got, fabric.IssueObservedSpeedConflict)
	}
	if got := meta.IssuesFor(analysis.PortScope("sw2", "1/1/1")); len(got) != 0 {
		t.Errorf("sw2 port issues = %+v, want none", got)
	}
}

func TestHostWithoutEthernetFactsLeavesLinkUnknownAndFloodsNothing(t *testing.T) {
	gigabit := autoEthernet(10_000_000, 100_000_000, 1_000_000_000)
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}
	macH3 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03}

	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  mustTable(t, b),
				Bridge: &bridge.Config{},
				Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit, "1/1/3": gigabit}},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2, Ethernet: gigabit},
			"h3": {Address: macH3, Ethernet: gigabit},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, Medium: fabric.TwistedPair},
		},
	})
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	l := fab.Links()[0]
	if l.A.Node != "h1" || l.A.Oper != port.Unknown || l.B.Oper != port.Unknown || l.A.Reason != phy.ReasonCapabilityUnknown {
		t.Errorf("h1 link = %+v / %+v, want both ends Unknown with capability-unknown", l.A, l.B)
	}
	if p, _ := fab.Switch("sw1").Ports().Port("1/1/1"); p.OperStatus != port.Unknown {
		t.Errorf("sw1 1/1/1 OperStatus = %s, want Unknown", p.OperStatus)
	}
	if got := issueCodes(fab.Metadata().IssuesFor(analysis.LinkScope("h1:-sw1:1/1/1"))); !slices.Equal(got, []analysis.IssueCode{analysis.IssueCode(phy.ReasonCapabilityUnknown)}) {
		t.Errorf("h1 link issue codes = %v, want [capability-unknown]", got)
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     fixedTime,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame:  ethernet.Frame{Dst: macH3, Src: macH2, EtherType: ethernet.EtherTypeIPv4},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(20)

	journey := fab.Report()[fid-1]
	var hop *vswitch.ForwardResult
	for _, entry := range journey.Entries {
		if entry.Kind == fabric.EntryHop {
			hop = entry.Result
		}
	}
	if hop == nil {
		t.Fatalf("journey entries = %+v, want a hop", journey.Entries)
	}
	for _, egress := range hop.Egress {
		if egress.Port == "1/1/1" {
			t.Errorf("flood egress = %+v, want nothing onto the Unknown port 1/1/1", hop.Egress)
		}
	}
	if len(journey.Deliveries) != 1 || journey.Deliveries[0].Host != "h3" {
		t.Errorf("deliveries = %+v, want h3 only", journey.Deliveries)
	}
}

func TestForcedHundredFullAgainstKnownAutoResolvesWithDuplexMismatch(t *testing.T) {
	fab, err := fabric.New(switchPair(t,
		forcedEthernet(100_000_000, phy.Full),
		autoEthernet(10_000_000, 100_000_000, 1_000_000_000),
		fabric.Cable{Medium: fabric.TwistedPair, LengthMeters: 10},
	))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	l := fab.Links()[0]
	if l.A.Oper != port.Up || l.A.Speed.SpeedBPS != 100_000_000 || l.A.Speed.Reason != phy.ReasonDuplexMismatch {
		t.Errorf("forced end = %+v, want Up at 100 Mb/s with duplex-mismatch", l.A)
	}
	if l.A.Speed.DuplexA != phy.Full || l.A.Speed.DuplexB != phy.Half || l.B.Speed.DuplexA != phy.Half {
		t.Errorf("duplexes A=%+v B=%+v, want Full on the forced end and Half on the auto end", l.A.Speed, l.B.Speed)
	}
	if issues := fab.Metadata().Issues(); len(issues) != 0 {
		t.Errorf("issues = %+v, want none: a duplex mismatch is recorded on the link only", issues)
	}
}

func TestForcedGigabitAgainstAutoIsUnsupported(t *testing.T) {
	fab, err := fabric.New(switchPair(t,
		forcedEthernet(1_000_000_000, phy.Full),
		autoEthernet(1_000_000_000, 10_000_000_000),
		fabric.Cable{Medium: fabric.MultimodeFiber, LengthMeters: 400},
	))
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	l := fab.Links()[0]
	if l.A.Oper != port.Unknown || l.A.Reason != phy.ReasonForcedAgainstAutoUnmodeled {
		t.Errorf("link end A = %+v, want Unknown with forced-against-auto-unmodeled", l.A)
	}
	issues := fab.Metadata().IssuesFor(analysis.LinkScope(pairLinkKey))
	if len(issues) != 1 || issues[0].Status != analysis.Unsupported {
		t.Errorf("link issues = %+v, want one Unsupported issue", issues)
	}
}

func TestUncabledPortDropsDefinitelyAndOmittedPortIsUnresolved(t *testing.T) {
	gigabit := autoEthernet(1_000_000_000)
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	behind3 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x03}
	behind4 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x04}
	catalog, uncabledRef := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "survey", Origin: "rack-walk", Context: "sw1 port 3 empty"})

	// Ports 2 to 4 report no operational status, so none conflicts with what
	// the topology derives.
	b := port.NewBuilder()
	b.Add(port.Port{Name: "1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Range("%d", 2, 4, port.Port{Kind: port.Physical, AdminStatus: port.Up})
	cfg := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, b), Bridge: &bridge.Config{}, Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{"1": gigabit}}},
		},
		Hosts:    map[string]fabric.Host{"h1": {Address: macH1, Ethernet: gigabit}},
		Cables:   []fabric.Cable{{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1"}, Medium: fabric.TwistedPair}},
		Uncabled: []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "3"}, Evidence: []trace.EvidenceRef{uncabledRef}}},
	}
	spec := constructionSpec(cfg)
	spec.Evidence = catalog
	sw1 := spec.Switches["sw1"]
	sw1.Seeds = []bridge.Seed{
		{MAC: behind3, Port: "3", Static: true},
		{MAC: behind4, Port: "4", Static: true},
	}
	spec.Switches["sw1"] = sw1

	fab, err := fabric.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	wantUnlinked := []fabric.LinkEnd{
		{Endpoint: fabric.Endpoint{Node: "sw1", Port: "2"}, Oper: port.Unknown, Reason: fabric.ReasonAdjacencyUnresolved},
		{Endpoint: fabric.Endpoint{Node: "sw1", Port: "3"}, Oper: port.Down, Reason: fabric.ReasonNoCable},
		{Endpoint: fabric.Endpoint{Node: "sw1", Port: "4"}, Oper: port.Unknown, Reason: fabric.ReasonAdjacencyUnresolved},
	}
	got := fab.Unlinked("sw1")
	if len(got) != len(wantUnlinked) {
		t.Fatalf("Unlinked(sw1) = %+v, want %+v", got, wantUnlinked)
	}
	for i := range wantUnlinked {
		if got[i].Endpoint != wantUnlinked[i].Endpoint || got[i].Oper != wantUnlinked[i].Oper || got[i].Reason != wantUnlinked[i].Reason {
			t.Errorf("Unlinked(sw1)[%d] = %+v, want %+v", i, got[i], wantUnlinked[i])
		}
	}
	ports := fab.Switch("sw1").Ports()
	if p, _ := ports.Port("3"); p.OperStatus != port.Down {
		t.Errorf("port 3 OperStatus = %s, want Down", p.OperStatus)
	}
	if p, _ := ports.Port("4"); p.OperStatus != port.Unknown {
		t.Errorf("port 4 OperStatus = %s, want Unknown", p.OperStatus)
	}

	meta := fab.Metadata()
	if issues := meta.IssuesFor(analysis.PortScope("sw1", "3")); len(issues) != 0 {
		t.Errorf("port 3 issues = %+v, want none: its state is stated", issues)
	}
	unresolved := meta.IssuesFor(analysis.PortScope("sw1", "4"))
	if len(unresolved) != 1 || unresolved[0].Code != analysis.IssueCode(fabric.ReasonAdjacencyUnresolved) || unresolved[0].Status != analysis.Incomplete {
		t.Errorf("port 4 issues = %+v, want one Incomplete adjacency-unresolved", unresolved)
	}

	hopTo := func(dst netaddr.MAC) (*vswitch.ForwardResult, fabric.Journey) {
		t.Helper()
		fid, err := fab.Inject(fabric.Injection{
			At:     fab.Snapshot().Clock,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame:  ethernet.Frame{Dst: dst, Src: macH1, EtherType: ethernet.EtherTypeIPv4},
		})
		if err != nil {
			t.Fatalf("Inject: %v", err)
		}
		fab.Run(10)
		journey := fab.Report()[fid-1]
		for _, entry := range journey.Entries {
			if entry.Kind == fabric.EntryHop {
				return entry.Result, journey
			}
		}
		t.Fatalf("frame %d has no hop", fid)

		return nil, fabric.Journey{}
	}

	out3, journey3 := hopTo(behind3)
	if out3.Outcome != trace.Dropped || out3.Reason != port.ReasonPortDown {
		t.Errorf("hop out 3 = %s %q, want dropped with port-down", out3.Outcome, out3.Reason)
	}
	if got := out3.Metadata.Status(); got != analysis.Complete {
		t.Errorf("hop out 3 status = %s, want complete; issues %+v", got, out3.Metadata.Issues())
	}
	if got := journey3.Metadata.Status(); got != analysis.Complete {
		t.Errorf("journey out 3 status = %s, want complete; issues %+v", got, journey3.Metadata.Issues())
	}

	out4, journey4 := hopTo(behind4)
	consulted4 := slices.ContainsFunc(out4.ConsultedPorts(), func(p port.Port) bool { return p.Name == "4" })
	if !consulted4 {
		t.Errorf("hop out 4 consulted %+v, want port 4", out4.ConsultedPorts())
	}
	if got := out4.Metadata.Status(); got != analysis.Incomplete {
		t.Errorf("hop out 4 status = %s, want incomplete", got)
	}
	if got := journey4.Metadata.Status(); got != analysis.Incomplete {
		t.Errorf("journey out 4 status = %s, want incomplete", got)
	}
	if !hasIssue(journey4.Metadata.IssuesFor(analysis.PortScope("sw1", "4")), analysis.IssueCode(fabric.ReasonAdjacencyUnresolved)) {
		t.Errorf("journey out 4 issues = %+v, want adjacency-unresolved on sw1 port 4", journey4.Metadata.Issues())
	}
}

func TestOperStatusConflictStaysOnItsPort(t *testing.T) {
	gigabit := autoEthernet(1_000_000_000)
	macs := map[string]netaddr.MAC{
		"h1": {0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		"h2": {0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		"h3": {0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
	}

	b := port.NewBuilder()
	b.Add(port.Port{Name: "1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Down})
	b.Add(port.Port{Name: "2", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "3", Kind: port.Physical, AdminStatus: port.Up})
	b.Add(port.Port{Name: "4", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Unknown})
	b.Add(port.Port{Name: "5", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	b.Add(port.Port{Name: "6", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	ethernetFacts := map[string]phy.Ethernet{"1": gigabit, "2": gigabit, "3": gigabit, "4": gigabit}
	hosts := map[string]fabric.Host{}
	var cables []fabric.Cable
	for i, name := range []string{"h1", "h2", "h3"} {
		hosts[name] = fabric.Host{Address: macs[name], Ethernet: gigabit}
		cables = append(cables, fabric.Cable{A: fabric.Endpoint{Node: name}, B: fabric.Endpoint{Node: "sw1", Port: string(rune('1' + i))}, Medium: fabric.TwistedPair})
	}
	hosts["h4"] = fabric.Host{Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x04}, Ethernet: gigabit}
	cables = append(cables, fabric.Cable{A: fabric.Endpoint{Node: "h4"}, B: fabric.Endpoint{Node: "sw1", Port: "4"}, Medium: fabric.TwistedPair})

	spec := constructionSpec(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, b), Bridge: &bridge.Config{}, Phy: &phy.Config{Ethernet: ethernetFacts}},
		},
		Hosts:    hosts,
		Cables:   cables,
		Uncabled: []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "5"}}},
	})
	sw1 := spec.Switches["sw1"]
	sw1.Seeds = []bridge.Seed{{MAC: macs["h3"], Port: "3", Static: true}}
	spec.Switches["sw1"] = sw1

	fab, err := fabric.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	meta := fab.Metadata()
	conflicts := slices.DeleteFunc(meta.Issues(), func(issue analysis.Issue) bool {
		return issue.Code != fabric.IssueOperStatusConflict
	})
	wantScopes := []analysis.Scope{analysis.PortScope("sw1", "1"), analysis.PortScope("sw1", "5")}
	if len(conflicts) != len(wantScopes) {
		t.Fatalf("oper-status-conflict issues = %+v, want ports 1 and 5 only", conflicts)
	}
	for i, scope := range wantScopes {
		if conflicts[i].Scope.Compare(scope) != 0 || conflicts[i].Status != analysis.Incomplete {
			t.Errorf("conflict %d = %+v, want Incomplete on %s", i, conflicts[i], scope)
		}
	}
	if got := issueCodes(meta.IssuesFor(analysis.PortScope("sw1", "6"))); !slices.Equal(got, []analysis.IssueCode{analysis.IssueCode(fabric.ReasonAdjacencyUnresolved)}) {
		t.Errorf("port 6 issue codes = %v, want only adjacency-unresolved: an unresolved port observes nothing to conflict with", got)
	}

	if p, _ := fab.Switch("sw1").Ports().Port("1"); p.OperStatus != port.Up {
		t.Errorf("effective port 1 OperStatus = %s, want the derived Up", p.OperStatus)
	}
	if p, _ := fab.Config().Switches["sw1"].Ports.Port("1"); p.OperStatus != port.Down {
		t.Errorf("Config() port 1 OperStatus = %s, want the configured Down", p.OperStatus)
	}
	if p, _ := fab.Spec().Switches["sw1"].Config.Ports.Port("1"); p.OperStatus != port.Down {
		t.Errorf("Spec() port 1 OperStatus = %s, want the configured Down", p.OperStatus)
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     fixedTime,
		Origin: fabric.Endpoint{Node: "h2"},
		Frame:  ethernet.Frame{Dst: macs["h3"], Src: macs["h2"], EtherType: ethernet.EtherTypeIPv4},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(10)
	journey := fab.Report()[fid-1]
	for _, entry := range journey.Entries {
		if entry.Kind != fabric.EntryHop {
			continue
		}
		if entry.Result.Outcome != trace.Forwarded || entry.Result.Metadata.Status() != analysis.Complete {
			t.Errorf("known-unicast hop = %s, status %s, issues %+v; want forwarded and complete",
				entry.Result.Outcome, entry.Result.Metadata.Status(), entry.Result.Metadata.Issues())
		}
		for _, p := range entry.Result.ConsultedPorts() {
			if issues := meta.IssuesFor(analysis.PortScope("sw1", p.Name)); len(issues) != 0 {
				t.Errorf("consulted port %s carries fabric issues %+v, want none", p.Name, issues)
			}
		}
	}
	if len(journey.Deliveries) != 1 || journey.Deliveries[0].Host != "h3" {
		t.Errorf("deliveries = %+v, want h3", journey.Deliveries)
	}
	if got := journey.Metadata.Status(); got != analysis.Complete {
		t.Errorf("known-unicast journey status = %s, issues %+v; want complete", got, journey.Metadata.Issues())
	}
}

func TestUnknownRedundantUplinkMarksSpanningTreeForwarding(t *testing.T) {
	t0 := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	gigabit := autoEthernet(1_000_000_000)
	macH1 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}
	macH2 := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}

	switchConfig := func(priority uint16, address netaddr.MAC, uplinkFacts map[string]phy.Ethernet) vswitch.Config {
		b := port.NewBuilder()
		b.Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		return vswitch.Config{
			Ports:  mustTable(t, b),
			Bridge: &bridge.Config{},
			STP: &stp.Config{
				Priority: priority,
				Address:  address,
				Ports:    map[string]stp.Port{"1/1/1": {}, "1/1/2": {}, "1/1/3": {}},
			},
			Phy: &phy.Config{Ethernet: uplinkFacts},
		}
	}
	fab, err := fabric.New(fabric.Config{
		Start: t0,
		Switches: map[string]vswitch.Config{
			"sw1": switchConfig(4096, netaddr.MAC{0, 0, 0, 0, 1, 1}, map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit, "1/1/3": gigabit}),
			"sw2": switchConfig(8192, netaddr.MAC{0, 0, 0, 0, 1, 2}, map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/3": gigabit}),
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1, Ethernet: gigabit},
			"h2": {Address: macH2, Ethernet: gigabit},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/3"}, Medium: fabric.TwistedPair},
		},
	})
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}
	if got := fab.Links()[3]; got.A.Endpoint != (fabric.Endpoint{Node: "sw1", Port: "1/1/2"}) || got.A.Oper != port.Unknown {
		t.Fatalf("redundant uplink = %+v, want Unknown", got.A)
	}
	fab.Run(200)

	for _, name := range []string{"sw1", "sw2"} {
		for _, p := range []string{"1/1/1", "1/1/2", "1/1/3"} {
			issues := fab.Switch(name).Peek(t0, p, ethernet.Frame{}).Metadata.IssuesFor(analysis.PortScope(name, p))
			if !slices.ContainsFunc(issues, func(issue analysis.Issue) bool { return issue.Code == vswitch.IssueProtocolLinkUnknown }) {
				t.Errorf("%s %s issues = %+v, want protocol-link-unknown", name, p, issues)
			}
		}
	}

	fid, err := fab.Inject(fabric.Injection{
		At:     fab.Snapshot().Clock,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Dst: macH2, Src: macH1, EtherType: ethernet.EtherTypeIPv4},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(50)
	journey := fab.Report()[fid-1]
	if got := journey.Metadata.Status(); got != analysis.Incomplete || !hasIssue(journey.Metadata.Issues(), vswitch.IssueProtocolLinkUnknown) {
		t.Errorf("journey status = %s, issues %+v; want incomplete with protocol-link-unknown", got, journey.Metadata.Issues())
	}
	for _, entry := range journey.Entries {
		if entry.Kind != fabric.EntryHop || entry.Device != "sw1" {
			continue
		}
		if !slices.ContainsFunc(entry.Result.Metadata.Issues(), func(issue analysis.Issue) bool {
			return issue.Code == vswitch.IssueProtocolLinkUnknown
		}) {
			t.Errorf("sw1 hop issues = %+v, want protocol-link-unknown", entry.Result.Metadata.Issues())
		}
		return
	}
	t.Error("the frame never hopped through sw1")
}

func TestPhyAssumptionRecordsOneAssumptionPerFilledLink(t *testing.T) {
	gigabit := autoEthernet(10_000_000, 100_000_000, 1_000_000_000)
	b := port.NewBuilder()
	b.Range("1/1/%d", 1, 2, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	stated := fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {Ports: mustTable(t, b), Phy: &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": gigabit}}},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x01}, Ethernet: gigabit},
			"h2": {Address: netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x02}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
		},
	}
	assumed := stated.Clone()
	assumed.PhyAssumption = gigabitCopper()

	fab, err := fabric.New(assumed)
	if err != nil {
		t.Fatalf("New fabric: %v", err)
	}

	assumptions := fab.Metadata().Assumptions()
	if len(assumptions) != 1 {
		t.Fatalf("assumptions = %+v, want one for the h2 link", assumptions)
	}
	if want := analysis.LinkScope("h2:-sw1:1/1/2"); assumptions[0].Scope.Compare(want) != 0 {
		t.Errorf("assumption scope = %s, want %s", assumptions[0].Scope, want)
	}
	for _, fact := range []string{"medium", `"h2" supported_speeds_bps`, `"h2" auto_negotiation_supported`, `"h2" setting`} {
		if !strings.Contains(assumptions[0].Statement, fact) {
			t.Errorf("assumption statement %q does not name %s", assumptions[0].Statement, fact)
		}
	}
	if strings.Contains(assumptions[0].Statement, "sw1") {
		t.Errorf("assumption statement %q names a stated switch port", assumptions[0].Statement)
	}

	h2Link := fab.Links()[1]
	if h2Link.Medium != fabric.TwistedPair || h2Link.A.Ethernet.Setting == nil || len(h2Link.A.Ethernet.SupportedSpeedsBPS) != 3 {
		t.Errorf("Links() h2 link = %+v, want the filled medium and Ethernet facts", h2Link)
	}
	if h2Link.A.Oper != port.Up || h2Link.A.Speed.SpeedBPS != 1_000_000_000 {
		t.Errorf("h2 link end = %+v, want Up at 1 Gb/s", h2Link.A)
	}
	if cfg := fab.Config(); cfg.Cables[1].Medium != fabric.MediumUnspecified || cfg.Hosts["h2"].Ethernet.Setting != nil {
		t.Errorf("Config() shows filled values: cable %+v, host %+v", cfg.Cables[1], cfg.Hosts["h2"])
	}
	spec := fab.Spec()
	if spec.Cables[1].Medium != fabric.MediumUnspecified || spec.Hosts["h2"].Ethernet.Setting != nil || !spec.PhyAssumption.Equal(gigabitCopper()) {
		t.Errorf("Spec() = cable %+v, host %+v, assumption %+v; want configured values and the assumption", spec.Cables[1], spec.Hosts["h2"], spec.PhyAssumption)
	}
	changes := fabric.Diff(stated, assumed)
	if len(changes) != 1 || changes[0].Subject.Kind != "fabric" || changes[0].Field != "phy_assumption" {
		t.Errorf("Diff = %+v, want the phy_assumption field only", changes)
	}
}

func TestUncabledValidationNamesTheField(t *testing.T) {
	// Only the trunk stays, so sw1:1/1/1 and both switches' other ports are free.
	base := func() fabric.Config {
		cfg := twoSwitchBaseConfig(t)
		cfg.Cables = cfg.Cables[1:2]
		cfg.Hosts = nil
		return cfg
	}
	tests := []struct {
		name      string
		uncabled  []fabric.Uncabled
		wantField string
	}{
		{name: "an absent node", uncabled: []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw9", Port: "1/1/1"}}}, wantField: "uncabled.0"},
		{name: "an absent port", uncabled: []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/9"}}}, wantField: "uncabled.0"},
		{name: "a LAG", uncabled: []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "lag1"}}}, wantField: "uncabled.0"},
		{name: "a cabled port", uncabled: []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}}}, wantField: "uncabled.0"},
		{
			name: "a duplicate at its submitted position",
			uncabled: []fabric.Uncabled{
				{Endpoint: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
				{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
				{Endpoint: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}},
			},
			wantField: "uncabled.2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base()
			cfg.Uncabled = tc.uncabled

			_, err := fabric.NewConstructionSpec(cfg)
			if err == nil {
				t.Fatal("NewConstructionSpec accepted the entry")
			}
			if got := errs.Attributes(err)["field"]; got != tc.wantField {
				t.Errorf("field = %v, want %q (error %v)", got, tc.wantField, err)
			}
			if cfg.Validate() == nil {
				t.Error("Validate accepted the entry")
			}
		})
	}

	t.Run("a valid entry", func(t *testing.T) {
		cfg := base()
		cfg.Uncabled = []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}}}
		if _, err := fabric.NewConstructionSpec(cfg); err != nil {
			t.Errorf("NewConstructionSpec: %v", err)
		}
	})
}

func TestConstructionFieldPathsForEvidenceAndPhysicalFacts(t *testing.T) {
	ref := trace.EvidenceRef("evidence:missing")
	tests := []struct {
		name      string
		mutate    func(*fabric.ConstructionSpec)
		wantField string
	}{
		{
			name:      "a cable reference outside the catalog",
			mutate:    func(s *fabric.ConstructionSpec) { s.Cables[1].Evidence = []trace.EvidenceRef{ref} },
			wantField: "cables.1.evidence.0",
		},
		{
			name: "an uncabled reference outside the catalog",
			mutate: func(s *fabric.ConstructionSpec) {
				s.Cables = s.Cables[1:2]
				s.Hosts = nil
				s.Uncabled = []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Evidence: []trace.EvidenceRef{ref}}}
			},
			wantField: "uncabled.0.evidence.0",
		},
		{
			name: "an unknown host duplex",
			mutate: func(s *fabric.ConstructionSpec) {
				h := s.Hosts["h1"]
				h.Ethernet = phy.Ethernet{Setting: &phy.Setting{Duplex: "Bogus"}}
				s.Hosts["h1"] = h
			},
			wantField: "hosts.h1.ethernet.duplex",
		},
		{
			name: "a host forced speed it does not support",
			mutate: func(s *fabric.ConstructionSpec) {
				h := s.Hosts["h1"]
				h.Ethernet = phy.Ethernet{SupportedSpeedsBPS: []uint64{100_000_000}, Setting: &phy.Setting{SpeedBPS: 1_000_000_000}}
				s.Hosts["h1"] = h
			},
			wantField: "hosts.h1.ethernet.speed_bps",
		},
		{
			name: "an assumed observation",
			mutate: func(s *fabric.ConstructionSpec) {
				s.PhyAssumption = &fabric.PhyAssumption{Ethernet: phy.Ethernet{Observed: &phy.Observed{SpeedBPS: 1}}}
			},
			wantField: "phy_assumption.ethernet.observed",
		},
		{
			name: "an assumed unknown medium",
			mutate: func(s *fabric.ConstructionSpec) {
				s.PhyAssumption = &fabric.PhyAssumption{Medium: "coax"}
			},
			wantField: "phy_assumption.medium",
		},
		{
			name: "an assumed auto-negotiation the profile does not support",
			mutate: func(s *fabric.ConstructionSpec) {
				s.PhyAssumption = &fabric.PhyAssumption{Ethernet: phy.Ethernet{
					AutoNegotiationSupported: phy.CapabilityUnsupported,
					Setting:                  &phy.Setting{AutoNegotiation: true},
				}}
			},
			wantField: "phy_assumption.ethernet.auto_negotiation",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := constructionSpec(twoSwitchBaseConfig(t))
			tc.mutate(&spec)
			_, err := fabric.NewWithSpec(spec)
			if err == nil {
				t.Fatal("NewWithSpec accepted the specification")
			}
			if got := errs.Attributes(err)["field"]; got != tc.wantField {
				t.Errorf("field = %v, want %q (error %v)", got, tc.wantField, err)
			}
		})
	}
}

func TestAddedFabricFieldsFieldMatrix(t *testing.T) {
	catalog, refA := analysis.EvidenceCatalog{}.Add(analysis.Evidence{Kind: "survey", Origin: "a"})
	catalog, refB := catalog.Add(analysis.Evidence{Kind: "survey", Origin: "b"})

	base := func() fabric.ConstructionSpec {
		cfg := twoSwitchBaseConfig(t)
		cfg.Cables = cfg.Cables[1:2]
		cfg.Hosts = nil
		spec := constructionSpec(cfg)
		spec.Evidence = catalog
		return spec
	}

	tests := []struct {
		name        string
		mutate      func(*fabric.ConstructionSpec)
		wantChanges int
		wantKind    string
		wantField   string
	}{
		{
			name: "adding an uncabled entry",
			mutate: func(s *fabric.ConstructionSpec) {
				s.Uncabled = append(s.Uncabled, fabric.Uncabled{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}})
			},
			wantChanges: 1, wantKind: "uncabled",
		},
		{
			name:        "setting the physical assumption",
			mutate:      func(s *fabric.ConstructionSpec) { s.PhyAssumption = gigabitCopper() },
			wantChanges: 1, wantKind: "fabric", wantField: "phy_assumption",
		},
		{
			name: "changing the assumed medium",
			mutate: func(s *fabric.ConstructionSpec) {
				s.PhyAssumption = gigabitCopper()
				s.PhyAssumption.Medium = fabric.Twinax
			},
			wantChanges: 1, wantKind: "fabric", wantField: "phy_assumption",
		},
		{
			name:        "citing evidence on a cable",
			mutate:      func(s *fabric.ConstructionSpec) { s.Cables[0].Evidence = []trace.EvidenceRef{refA} },
			wantChanges: 0,
		},
		{
			name: "citing evidence on an uncabled entry",
			mutate: func(s *fabric.ConstructionSpec) {
				s.Uncabled = []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Evidence: []trace.EvidenceRef{refB}}}
			},
			wantChanges: 1, wantKind: "uncabled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := base()
			b := base()
			tc.mutate(&b)

			if a.Equal(b) {
				t.Error("Equal reports the changed specification equal")
			}
			if a.Config().Equal(b.Config()) {
				t.Error("Config.Equal reports the changed configuration equal")
			}

			changes, err := fabric.DiffSpecs(a, b)
			if err != nil {
				t.Fatalf("DiffSpecs: %v", err)
			}
			if len(changes) != tc.wantChanges {
				t.Fatalf("DiffSpecs = %+v, want %d changes", changes, tc.wantChanges)
			}
			if tc.wantChanges > 0 && (changes[0].Subject.Kind != tc.wantKind || changes[0].Field != tc.wantField) {
				t.Errorf("change = %+v, want subject kind %q field %q", changes[0], tc.wantKind, tc.wantField)
			}

			fab, err := fabric.NewWithSpec(b)
			if err != nil {
				t.Fatalf("NewWithSpec: %v", err)
			}
			if !fab.Spec().Equal(b) {
				t.Error("Spec() does not round-trip the specification")
			}
			if !fab.Config().Equal(b.Config()) {
				t.Error("Config() does not carry the specification's configuration")
			}
		})
	}

	t.Run("host ethernet reports phy fields under the host", func(t *testing.T) {
		a := base()
		a.Hosts = map[string]fabric.Host{"h1": {}}
		a.Cables = append(a.Cables, fabric.Cable{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}})
		b := a.Clone()
		b.Hosts["h1"] = fabric.Host{Ethernet: phy.Ethernet{AutoNegotiationSupported: phy.CapabilitySupported}}

		changes, err := fabric.DiffSpecs(a, b)
		if err != nil {
			t.Fatalf("DiffSpecs: %v", err)
		}
		if len(changes) != 1 || changes[0].Subject != (trace.Subject{Kind: "host", Key: "h1"}) || changes[0].Field != "ethernet.auto_negotiation_supported" {
			t.Errorf("DiffSpecs = %+v, want one host ethernet.auto_negotiation_supported change", changes)
		}
		if a.Equal(b) {
			t.Error("Equal reports different host Ethernet facts equal")
		}
	})

	t.Run("normalization", func(t *testing.T) {
		cfg := twoSwitchBaseConfig(t)
		cfg.Cables = cfg.Cables[1:2]
		cfg.Hosts = nil
		cfg.Cables[0].Evidence = []trace.EvidenceRef{refB, refA, refB}
		cfg.Uncabled = []fabric.Uncabled{
			{Endpoint: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, Evidence: []trace.EvidenceRef{refB, refA, refA}},
			{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
		}
		cfg.PhyAssumption = &fabric.PhyAssumption{Ethernet: phy.Ethernet{
			SupportedSpeedsBPS: []uint64{100_000_000, 10_000_000, 100_000_000},
			Setting:            &phy.Setting{SpeedBPS: 10_000_000},
		}}

		norm := cfg.Normalize()
		sorted := []trace.EvidenceRef{refA, refB}
		if refB < refA {
			sorted = []trace.EvidenceRef{refB, refA}
		}
		if !slices.Equal(norm.Cables[0].Evidence, sorted) {
			t.Errorf("cable evidence = %v, want %v", norm.Cables[0].Evidence, sorted)
		}
		if norm.Uncabled[0].Endpoint.Node != "sw1" || !slices.Equal(norm.Uncabled[1].Evidence, sorted) {
			t.Errorf("uncabled = %+v, want sorted entries with sorted, deduplicated evidence", norm.Uncabled)
		}
		if got := norm.PhyAssumption.Ethernet; !slices.Equal(got.SupportedSpeedsBPS, []uint64{10_000_000, 100_000_000}) || got.Setting.Duplex != phy.Unknown {
			t.Errorf("assumed ethernet = %+v, want sorted speeds and an Unknown forced duplex", got)
		}
		if !cfg.Equal(norm) {
			t.Error("a configuration is not Equal to its normalization")
		}
	})

	t.Run("clone", func(t *testing.T) {
		cfg := twoSwitchBaseConfig(t)
		cfg.Cables[0].Evidence = []trace.EvidenceRef{refA}
		cfg.Uncabled = []fabric.Uncabled{{Endpoint: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Evidence: []trace.EvidenceRef{refA}}}
		cfg.PhyAssumption = gigabitCopper()
		h1 := cfg.Hosts["h1"]
		h1.Ethernet = autoEthernet(1_000_000_000)
		cfg.Hosts["h1"] = h1
		spec := constructionSpec(cfg)
		spec.Evidence = catalog

		cloned := cfg.Clone()
		clonedSpec := spec.Clone()
		cfg.Cables[0].Evidence[0] = refB
		cfg.Uncabled[0].Evidence[0] = refB
		cfg.Uncabled[0].Endpoint.Port = "1/1/2"
		cfg.PhyAssumption.Ethernet.SupportedSpeedsBPS[0] = 1
		cfg.PhyAssumption.Ethernet.Setting.AutoNegotiation = false
		cfg.Hosts["h1"].Ethernet.SupportedSpeedsBPS[0] = 1
		cfg.Hosts["h1"].Ethernet.Setting.AutoNegotiation = false

		for name, c := range map[string]fabric.Config{"Config.Clone": cloned, "ConstructionSpec.Clone": clonedSpec.Config()} {
			if c.Cables[0].Evidence[0] != refA || c.Uncabled[0].Evidence[0] != refA || c.Uncabled[0].Endpoint.Port != "1/1/1" {
				t.Errorf("%s shares cable or uncabled state: %+v %+v", name, c.Cables[0], c.Uncabled)
			}
			if c.PhyAssumption.Ethernet.SupportedSpeedsBPS[0] != 10_000_000 || !c.PhyAssumption.Ethernet.Setting.AutoNegotiation {
				t.Errorf("%s shares assumption state: %+v", name, c.PhyAssumption)
			}
			if h := c.Hosts["h1"].Ethernet; h.SupportedSpeedsBPS[0] != 1_000_000_000 || !h.Setting.AutoNegotiation {
				t.Errorf("%s shares host Ethernet state: %+v", name, h)
			}
		}
		if !slices.Equal(clonedSpec.Evidence.Entries(), catalog.Entries()) {
			t.Error("ConstructionSpec.Clone dropped the evidence catalog")
		}
	})
}
