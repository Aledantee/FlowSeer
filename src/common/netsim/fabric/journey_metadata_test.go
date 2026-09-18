package fabric_test

import (
	"net/netip"
	"slices"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/phy"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
)

const (
	h1LinkKey = "h1:-sw1:1/1/1"
	h2LinkKey = "h2:-sw1:1/1/2"
	h3LinkKey = "h3:-sw1:1/1/3"
)

var (
	journeyH1 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x01}
	journeyH2 = netaddr.MAC{0x02, 0, 0, 0, 1, 0x02}
)

// journeyFabric builds one bridge with three hosts. The h1 link is Up without
// issues, the h2 link runs on matching observations over an unspecified medium
// and so carries propagation-unknown, and h3 reports no facts, so its link is
// Unknown with capability-unknown. With seedH2, a static entry makes h2 known
// unicast, and a frame to it consults only the ingress and egress ports.
func journeyFabric(t *testing.T, seedH2 bool) *fabric.Fabric {
	t.Helper()
	gigabit := autoEthernet(1_000_000_000)
	observed := autoEthernet(1_000_000_000)
	observed.Observed = &phy.Observed{SpeedBPS: 1_000_000_000, Duplex: phy.Full}

	spec := constructionSpec(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports:  mustTable(t, port.NewBuilder().Range("1/1/%d", 1, 3, port.Port{Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})),
				Bridge: &bridge.Config{},
				Phy:    &phy.Config{Ethernet: map[string]phy.Ethernet{"1/1/1": gigabit, "1/1/2": observed, "1/1/3": gigabit}},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: journeyH1, Ethernet: gigabit},
			"h2": {Address: journeyH2, Ethernet: observed},
			"h3": {Address: netaddr.MAC{0x02, 0, 0, 0, 1, 0x03}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, LengthMeters: 2},
			{A: fabric.Endpoint{Node: "h3"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, Medium: fabric.TwistedPair},
		},
	})
	if seedH2 {
		sw1 := spec.Switches["sw1"]
		sw1.Seeds = []bridge.Seed{{MAC: journeyH2, Port: "1/1/2", Lifetime: bridge.Static}}
		spec.Switches["sw1"] = sw1
	}
	fab, err := fabric.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}

	return fab
}

func sendH1ToH2(t *testing.T, fab *fabric.Fabric) fabric.Journey {
	t.Helper()
	fid, err := fab.Inject(fabric.Injection{
		At:     fab.Snapshot().Clock,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Dst: journeyH2, Src: journeyH1, EtherType: ethernet.EtherTypeIPv4},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(10)

	return fab.Report()[fid-1]
}

func hasIssue(issues []analysis.Issue, code analysis.IssueCode) bool {
	return slices.ContainsFunc(issues, func(issue analysis.Issue) bool { return issue.Code == code })
}

func TestJourneyMetadataHoldsTheIssuesOfWhatItCrossed(t *testing.T) {
	fab := journeyFabric(t, true)
	if got := fab.Metadata().IssuesFor(analysis.LinkScope(h3LinkKey)); len(got) == 0 {
		t.Fatalf("h3 link issues = %+v, want the fixture's capability-unknown", got)
	}

	journey := sendH1ToH2(t, fab)
	if len(journey.Deliveries) != 1 || journey.Deliveries[0].Host != "h2" {
		t.Fatalf("deliveries = %+v, want h2", journey.Deliveries)
	}
	meta := journey.Metadata
	if got := meta.Status(); got != analysis.Incomplete {
		t.Errorf("journey status = %s, want incomplete", got)
	}
	if got := meta.Scope(); got.Compare(analysis.WholeScope()) != 0 {
		t.Errorf("journey metadata scope = %s, want the whole analysis", got)
	}
	if !hasIssue(meta.IssuesFor(analysis.LinkScope(h2LinkKey)), fabric.IssuePropagationUnknown) {
		t.Errorf("journey issues = %+v, want propagation-unknown on the crossed h2 link", meta.Issues())
	}
	if got := meta.IssuesFor(analysis.LinkScope(h3LinkKey)); len(got) != 0 {
		t.Errorf("journey carries uncrossed h3 link issues %+v", got)
	}

	injection := journey.Entries[0]
	if injection.Cable == nil || injection.Cable.A != (fabric.Endpoint{Node: "h1"}) || injection.Port != "" {
		t.Errorf("injection entry = %+v, want host h1's cable", injection)
	}
	delivery := journey.Entries[len(journey.Entries)-1]
	if delivery.Kind != fabric.EntryDelivery || delivery.Cable == nil || delivery.Cable.A != (fabric.Endpoint{Node: "h2"}) {
		t.Errorf("delivery entry = %+v, want host h2's cable", delivery)
	}

	before := meta.Issues()
	if err := fab.SetFault(fabric.Endpoint{Node: "h2"}, fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, fabric.Fault{Kind: fabric.FaultCut}); err != nil {
		t.Fatalf("SetFault: %v", err)
	}
	if got := fab.Metadata().IssuesFor(analysis.LinkScope(h2LinkKey)); len(got) != 0 {
		t.Fatalf("h2 link issues after the cut = %+v, want none", got)
	}
	after := fab.Report()[journey.FrameID-1].Metadata
	if !slices.EqualFunc(after.Issues(), before, func(a, b analysis.Issue) bool {
		return a.Code == b.Code && a.Scope.Compare(b.Scope) == 0 && a.Status == b.Status
	}) || after.Status() != analysis.Incomplete {
		t.Errorf("recorded journey issues after SetFault = %+v, want the %+v captured when it ran", after.Issues(), before)
	}
}

func TestSwitchOriginInjectionEntryCarriesItsPortAndCable(t *testing.T) {
	fab := journeyFabric(t, true)
	fid, err := fab.Inject(fabric.Injection{
		At:     fixedTime,
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/1"},
		Frame:  ethernet.Frame{Dst: journeyH2, Src: journeyH1},
	})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(10)

	injection := fab.Report()[fid-1].Entries[0]
	if injection.Port != "1/1/1" || injection.Cable == nil || injection.Cable.B != (fabric.Endpoint{Node: "sw1", Port: "1/1/1"}) {
		t.Errorf("injection entry = %+v, want port 1/1/1 with its cable", injection)
	}
}

// reflectMetadataConfig cables a reflector to h1 (the arrival side, VLAN 10,
// plain assumed physical facts) and to h2 (VLAN 20). Neither h2 nor the
// reflector's port toward it reports any physical fact, so that link
// resolves Unknown with capability-unknown and stops a reflected copy before
// it ever reaches h2: the reflector-side entries are then the only possible
// source of the issue in the copy's journey, unlike a copy that is also
// delivered over the same cable and could pick the issue up from either
// side.
func reflectMetadataConfig(t *testing.T) fabric.Config {
	t.Helper()
	gigabit := autoEthernet(1_000_000_000)
	vid10, vid20 := vlan.ID(10), vlan.ID(20)

	return fabric.Config{
		Hosts: map[string]fabric.Host{
			"h1": {Address: reflectH1Address, VLAN: &vid10, Ethernet: gigabit},
			"h2": {Address: reflectH2Address, VLAN: &vid20},
		},
		Reflectors: map[string]fabric.Reflector{
			"r1": {
				Address: reflectR1Address,
				Ports:   map[string]phy.Ethernet{"rp1": gigabit, "rp2": {}},
				Attachments: map[string]fabric.Attachment{
					"a": {Port: "rp1", VLAN: &vid10, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.10.9/24")}},
					"b": {Port: "rp2", VLAN: &vid20, Addresses: []netip.Prefix{netip.MustParsePrefix("10.0.20.9/24")}},
				},
			},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "r1", Port: "rp1"}, Medium: fabric.TwistedPair},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "r1", Port: "rp2"}, LengthMeters: 2},
		},
	}
}

// TestReflectedCopyJourneyFoldsItsCableMetadata proves that a copy's journey
// folds in the issues of the cables and links it depends on, exactly as a
// host delivery's journey does, because both go through the same generic
// [Fabric.record] dependency fold rather than a reflector-specific path.
func TestReflectedCopyJourneyFoldsItsCableMetadata(t *testing.T) {
	fab, err := fabric.NewWithSpec(constructionSpec(reflectMetadataConfig(t)))
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	if !hasIssue(fab.Metadata().Issues(), analysis.IssueCode(phy.ReasonCapabilityUnknown)) {
		t.Fatalf("fabric issues = %+v, want the fixture's capability-unknown on r1's link to h2", fab.Metadata().Issues())
	}

	frame := mdnsFrame(t, reflectH1Address, nil, reflectorGroupMAC, reflectorGroupAddr, 17, 5353)
	parentID, err := fab.Inject(fabric.Injection{At: fixedTime, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(20)

	var copyJourney fabric.Journey
	for _, j := range fab.Report() {
		if j.Parent == parentID {
			copyJourney = j
		}
	}
	if copyJourney.FrameID == 0 {
		t.Fatalf("no copy journey found for parent %d", parentID)
	}
	if len(copyJourney.Deliveries) != 0 {
		t.Fatalf("copy deliveries = %+v, want none: the egress link to h2 is Unknown", copyJourney.Deliveries)
	}
	if !hasIssue(copyJourney.Metadata.Issues(), analysis.IssueCode(phy.ReasonCapabilityUnknown)) {
		t.Errorf("copy journey issues = %+v, want capability-unknown from its own unresolved egress link", copyJourney.Metadata.Issues())
	}
}

func TestFloodJourneyCarriesTheIssueOfAConsultedPortItDidNotEgress(t *testing.T) {
	fab := journeyFabric(t, false)

	journey := sendH1ToH2(t, fab)
	var hop *vswitch.ForwardResult
	for _, entry := range journey.Entries {
		if entry.Kind == fabric.EntryHop {
			hop = entry.Result
		}
	}
	if hop == nil || hop.Outcome != trace.Flooded {
		t.Fatalf("entries = %+v, want a flooding hop", journey.Entries)
	}
	if slices.ContainsFunc(hop.Egress, func(e bridge.Egress) bool { return e.Port == "1/1/3" }) {
		t.Fatalf("flood egress = %+v, want nothing onto the Unknown port 1/1/3", hop.Egress)
	}
	if !slices.ContainsFunc(hop.ConsultedPorts(), func(p port.Port) bool { return p.Name == "1/1/3" }) {
		t.Fatalf("hop consulted %+v, want port 1/1/3", hop.ConsultedPorts())
	}

	if !hasIssue(journey.Metadata.IssuesFor(analysis.LinkScope(h3LinkKey)), analysis.IssueCode(phy.ReasonCapabilityUnknown)) {
		t.Errorf("journey issues = %+v, want capability-unknown on the consulted h3 link", journey.Metadata.Issues())
	}
}
