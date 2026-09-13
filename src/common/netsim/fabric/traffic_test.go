package fabric_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/lag"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

func newTrafficTopology(t *testing.T, trafficCfg *traffic.Config) (*fabric.Fabric, map[string]netaddr.MAC) {
	t.Helper()

	ports := func(names ...string) port.Table {
		t.Helper()
		b := port.NewBuilder()
		for _, name := range names {
			b.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		table, err := b.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		return table
	}

	vid10 := vlan.ID(10)
	macs := map[string]netaddr.MAC{
		"h1": {0x00, 0x11, 0x22, 0x33, 0x44, 0x01},
		"h2": {0x00, 0x11, 0x22, 0x33, 0x44, 0x02},
		"h3": {0x00, 0x11, 0x22, 0x33, 0x44, 0x03},
		"h4": {0x00, 0x11, 0x22, 0x33, 0x44, 0x04},
	}
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports("1/1/1", "1/1/2", "1/1/4", "1/1/24"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "VLAN10", 99: "MIRROR"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/2":  {Tagged: []vlan.ID{10}},
						"1/1/4":  {PVID: &vid10, Untagged: []vlan.ID{10, 99}},
						"1/1/24": {Tagged: []vlan.ID{10, 99}},
					},
				}},
				Traffic: trafficCfg,
			},
			"sw2": {
				Ports: ports("1/1/1", "1/1/24"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "VLAN10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/24": {Tagged: []vlan.ID{10}},
					},
				}},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macs["h1"]},
			"h2": {Address: macs["h2"]},
			"h3": {Address: macs["h3"]},
			"h4": {Address: macs["h4"], VLAN: &vid10},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/24"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/24"}, LengthMeters: 300, Medium: fabric.MultimodeFiber},
			{A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/4"}, B: fabric.Endpoint{Node: "h3"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h4"}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return fab, macs
}

func TestEgressQueueServesStrictPriority(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fab, macs := newTrafficTopology(t, nil)
	frame := func(pcp vlan.PCP, source byte) ethernet.Frame {
		return ethernet.Frame{
			Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, source},
			Src:     macs["h1"],
			Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: pcp, VID: 10}},
			Payload: make([]byte, 46),
		}
	}

	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Frame: frame(0, 1)}); err != nil {
		t.Fatalf("Inject PCP 0: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, Frame: frame(7, 2)}); err != nil {
		t.Fatalf("Inject PCP 7: %v", err)
	}

	if entry, ok := fab.Step(); !ok || entry.Kind != fabric.EntryHop {
		t.Fatalf("first Step() = (%+v, %t), want a hop", entry, ok)
	}
	if entry, ok := fab.Step(); !ok || entry.Kind != fabric.EntryHop {
		t.Fatalf("second Step() = (%+v, %t), want a hop", entry, ok)
	}
	trunk := fabric.Endpoint{Node: "sw1", Port: "1/1/24"}
	if got := fab.Snapshot().Queued[trunk]; got != 2 {
		t.Errorf("queued trunk frames = %d, want 2", got)
	}

	for {
		entry, ok := fab.Step()
		if !ok {
			t.Fatal("Step() exhausted the queue before the trunk dequeue")
		}
		if entry.Kind == fabric.EntryDequeue && entry.Device == "sw1" && entry.Port == "1/1/24" {
			break
		}
	}
	fab.Run(20)

	var crossings []fabric.Entry
	for _, journey := range fab.Report() {
		for _, candidate := range journey.Entries {
			if candidate.Kind == fabric.EntryCrossing && candidate.Device == "sw2" && candidate.Port == "1/1/24" {
				crossings = append(crossings, candidate)
			}
		}
	}
	if len(crossings) != 2 {
		t.Fatalf("trunk crossings = %+v, want two", crossings)
	}
	slices.SortFunc(crossings, func(a, b fabric.Entry) int {
		return a.At.Compare(b.At)
	})
	if crossings[0].PCP != 7 || !crossings[0].At.Equal(t0) || crossings[0].Wait != 0 {
		t.Errorf("first trunk crossing = %+v, want PCP 7 at t0 with no wait", crossings[0])
	}
	if crossings[1].PCP != 0 || !crossings[1].At.Equal(t0.Add(704*time.Nanosecond)) {
		t.Errorf("second trunk crossing = %+v, want PCP 0 at t0+704ns", crossings[1])
	}
	if got := crossings[0].At.Add(crossings[0].Serialization + crossings[0].Latency); !got.Equal(t0.Add(2198 * time.Nanosecond)) {
		t.Errorf("PCP 7 hop time = %v, want %v", got, t0.Add(2198*time.Nanosecond))
	}
	if got := crossings[1].At.Add(crossings[1].Serialization + crossings[1].Latency); !got.Equal(t0.Add(2902 * time.Nanosecond)) {
		t.Errorf("PCP 0 hop time = %v, want %v", got, t0.Add(2902*time.Nanosecond))
	}
}

func TestEgressQueueTracksPendingDequeueAtZeroTime(t *testing.T) {
	fab, macs := newTrafficTopology(t, nil)
	frame := func(pcp vlan.PCP, source byte) ethernet.Frame {
		return ethernet.Frame{
			Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, source},
			Src:     macs["h1"],
			Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: pcp, VID: 10}},
			Payload: make([]byte, 46),
		}
	}

	for pcp := range vlan.PCP(2) {
		if _, err := fab.Inject(fabric.Injection{
			Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
			Frame:  frame(pcp, byte(pcp+1)),
		}); err != nil {
			t.Fatalf("Inject PCP %d: %v", pcp, err)
		}
	}
	for range 2 {
		if entry, ok := fab.Step(); !ok || entry.Kind != fabric.EntryHop {
			t.Fatalf("Step() = (%+v, %t), want a hop", entry, ok)
		}
	}

	trunk := fabric.Endpoint{Node: "sw1", Port: "1/1/24"}
	snapshot := fab.Snapshot()
	if got := snapshot.Queued[trunk]; got != 2 {
		t.Errorf("queued trunk frames = %d, want 2", got)
	}
	dequeues := 0
	for _, arrival := range snapshot.Queue {
		if arrival.Kind == fabric.ArrivalDequeue && arrival.Device == trunk.Node && arrival.Port == trunk.Port {
			dequeues++
		}
	}
	if dequeues != 1 {
		t.Errorf("pending trunk dequeues = %d, want 1", dequeues)
	}
}

func TestMirrorCopyJourneyAndReservedOutput(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fab, macs := newTrafficTopology(t, &traffic.Config{Mirrors: []traffic.Mirror{{
		Name:           "m1",
		SelectSrcPorts: []string{"1/1/1"},
		OutputPort:     "1/1/4",
		SnapLen:        64,
	}}})
	payload := make([]byte, 200)
	for i := range payload {
		payload[i] = byte(i)
	}
	originalID, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:     macs["h2"],
			Src:     macs["h1"],
			Payload: payload,
		},
	})
	if err != nil {
		t.Fatalf("Inject h1: %v", err)
	}
	fab.Run(100)

	var copyJourney *fabric.Journey
	for _, journey := range fab.Report() {
		if journey.Mirror == "m1" {
			journey := journey
			copyJourney = &journey
		}
	}
	if copyJourney == nil {
		t.Fatal("Report() has no m1 copy journey")
	}
	if copyJourney.Parent != originalID {
		t.Errorf("copy Parent = %d, want %d", copyJourney.Parent, originalID)
	}
	if copyJourney.Injection.Origin != (fabric.Endpoint{Node: "sw1", Port: "1/1/4"}) {
		t.Errorf("copy origin = %+v, want sw1:1/1/4", copyJourney.Injection.Origin)
	}
	if len(copyJourney.Deliveries) != 1 || copyJourney.Deliveries[0].Host != "h3" {
		t.Fatalf("copy deliveries = %+v, want h3", copyJourney.Deliveries)
	}
	raw, err := copyJourney.Deliveries[0].Frame.Encode()
	if err != nil {
		t.Fatalf("encode mirror delivery: %v", err)
	}
	if len(raw) != 64 {
		t.Errorf("mirror delivery length = %d, want 64", len(raw))
	}
	if !slices.Equal(copyJourney.Deliveries[0].Frame.Payload, payload[:50]) {
		t.Errorf("mirror payload = %v, want first 50 payload octets", copyJourney.Deliveries[0].Frame.Payload)
	}

	h3ID, err := fab.Inject(fabric.Injection{
		At:     t0.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "h3"},
		Frame:  ethernet.Frame{Dst: macs["h2"], Src: macs["h3"]},
	})
	if err != nil {
		t.Fatalf("Inject h3: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{
		At:     t0.Add(2 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h2"},
		Frame:  ethernet.Frame{Dst: netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee}, Src: macs["h2"]},
	}); err != nil {
		t.Fatalf("Inject h2 flood: %v", err)
	}
	fab.Run(200)

	var h3Dropped bool
	h3Deliveries := 0
	for _, journey := range fab.Report() {
		if journey.FrameID == h3ID {
			for _, entry := range journey.Entries {
				if entry.Kind == fabric.EntryDrop && entry.Device == "sw1" && entry.Reason == traffic.ReasonMirrorOutput {
					h3Dropped = true
				}
			}
		}
		for _, delivery := range journey.Deliveries {
			if delivery.Host == "h3" {
				h3Deliveries++
			}
		}
	}
	if !h3Dropped {
		t.Error("frame from h3 has no mirror-output drop at sw1")
	}
	if h3Deliveries != 1 {
		t.Errorf("deliveries to h3 = %d, want only the m1 copy", h3Deliveries)
	}
}

func TestMirrorToVLANUsesEachPortsTagForm(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	vid99 := vlan.ID(99)
	fab, macs := newTrafficTopology(t, &traffic.Config{Mirrors: []traffic.Mirror{{
		Name:           "m2",
		SelectSrcPorts: []string{"1/1/1"},
		OutputVLAN:     &vid99,
	}}})
	if _, err := fab.Inject(fabric.Injection{
		At:     t0,
		Origin: fabric.Endpoint{Node: "h1"},
		Frame:  ethernet.Frame{Dst: macs["h2"], Src: macs["h1"]},
	}); err != nil {
		t.Fatalf("Inject h1: %v", err)
	}
	fab.Run(100)

	var trunkCopy, hostCopy *fabric.Journey
	for _, journey := range fab.Report() {
		if journey.Mirror != "m2" {
			continue
		}
		journey := journey
		switch journey.Injection.Origin.Port {
		case "1/1/24":
			trunkCopy = &journey
		case "1/1/4":
			hostCopy = &journey
		case "1/1/1":
			t.Error("mirror produced a copy on its ingress port")
		}
	}
	if trunkCopy == nil || hostCopy == nil {
		t.Fatalf("m2 journeys = trunk %v, host %v; want both", trunkCopy != nil, hostCopy != nil)
	}
	if len(trunkCopy.Injection.Frame.Tags) != 1 || trunkCopy.Injection.Frame.Tags[0].VID != 99 || trunkCopy.Injection.Frame.Tags[0].TPID != uint16(ethernet.EtherTypeDot1Q) {
		t.Errorf("trunk copy tags = %+v, want one C-tag with VID 99", trunkCopy.Injection.Frame.Tags)
	}
	var crossedTrunk bool
	for _, entry := range trunkCopy.Entries {
		if entry.Kind == fabric.EntryCrossing && entry.Device == "sw2" && entry.Port == "1/1/24" {
			crossedTrunk = true
		}
	}
	if !crossedTrunk {
		t.Errorf("trunk copy entries = %+v, want crossing to sw2:1/1/24", trunkCopy.Entries)
	}
	if len(hostCopy.Injection.Frame.Tags) != 0 {
		t.Errorf("host copy tags = %+v, want untagged", hostCopy.Injection.Frame.Tags)
	}
	if len(hostCopy.Deliveries) != 1 || hostCopy.Deliveries[0].Host != "h3" {
		t.Errorf("host copy deliveries = %+v, want h3", hostCopy.Deliveries)
	}

	before := len(fab.Report())
	if _, err := fab.Inject(fabric.Injection{
		At:     t0.Add(time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst: netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0e},
			Src: macs["h1"],
		},
	}); err != nil {
		t.Fatalf("Inject reserved destination: %v", err)
	}
	fab.Run(100)
	for _, journey := range fab.Report()[before:] {
		if journey.Mirror == "m2" {
			t.Errorf("reserved destination produced mirror journey %+v", journey)
		}
	}
}

func TestOutputVLANMirrorTransmitsOnTracedLAGMember(t *testing.T) {
	t0 := time.Date(2026, 9, 13, 9, 0, 0, 0, time.UTC)
	const (
		inputVLAN  vlan.ID = 10
		outputVLAN vlan.ID = 99
	)

	for _, test := range []struct {
		name       string
		switchport bridge.Switchport
	}{
		{name: "untagged", switchport: bridge.Switchport{Untagged: []vlan.ID{outputVLAN}}},
		{name: "tunnel", switchport: bridge.Switchport{Tunnel: &bridge.Tunnel{VID: outputVLAN}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := port.NewBuilder()
			for _, p := range []port.Port{
				{Name: "in", AdminStatus: port.Up, OperStatus: port.Up},
				{Name: "ordinary", AdminStatus: port.Up, OperStatus: port.Up},
				{Name: "lag1", Kind: port.Lag, AdminStatus: port.Up, OperStatus: port.Up},
				{Name: "member-a", LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up},
				{Name: "member-b", LagParent: "lag1", AdminStatus: port.Up, OperStatus: port.Up},
			} {
				builder.Add(p)
			}
			ports, err := builder.Build()
			if err != nil {
				t.Fatalf("build ports: %v", err)
			}

			macSource := netaddr.MAC{2, 0, 0, 0, 0, 1}
			macDestination := netaddr.MAC{2, 0, 0, 0, 0, 2}
			frame := ethernet.Frame{Src: macSource, Dst: macDestination, Payload: []byte("mirror member")}
			fab, err := fabric.New(fabric.Config{
				Start: t0,
				Switches: map[string]vswitch.Config{
					"sw1": {
						Ports: ports,
						Bridge: &bridge.Config{VLAN: &bridge.VLAN{
							Table: map[vlan.ID]string{inputVLAN: "input", outputVLAN: "mirror"},
							Switchports: map[string]bridge.Switchport{
								"in":       {PVID: new(inputVLAN), Untagged: []vlan.ID{inputVLAN}},
								"ordinary": {PVID: new(inputVLAN), Untagged: []vlan.ID{inputVLAN}},
								"lag1":     test.switchport,
							},
						}},
						LAG: &lag.Config{LAGs: map[string]lag.LAG{
							"lag1": {Mode: lag.BalanceSLB},
						}},
						Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
							Name: "span", SelectAll: true, OutputVLAN: new(outputVLAN),
						}}},
					},
				},
				Hosts: map[string]fabric.Host{
					"source":      {Address: macSource},
					"destination": {Address: macDestination},
					"sink-a":      {Address: netaddr.MAC{2, 0, 0, 0, 0, 3}},
					"sink-b":      {Address: netaddr.MAC{2, 0, 0, 0, 0, 4}},
				},
				Cables: []fabric.Cable{
					{A: fabric.Endpoint{Node: "source"}, B: fabric.Endpoint{Node: "sw1", Port: "in"}},
					{A: fabric.Endpoint{Node: "destination"}, B: fabric.Endpoint{Node: "sw1", Port: "ordinary"}},
					{A: fabric.Endpoint{Node: "sink-a"}, B: fabric.Endpoint{Node: "sw1", Port: "member-a"}},
					{A: fabric.Endpoint{Node: "sink-b"}, B: fabric.Endpoint{Node: "sw1", Port: "member-b"}},
				},
			})
			if err != nil {
				t.Fatalf("fabric.New: %v", err)
			}

			wantMember, ok := fab.Switch("sw1").SelectMember("lag1", frame, outputVLAN)
			if !ok {
				t.Fatal("mirror output has no selected member")
			}
			frameID, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "source"}, Frame: frame})
			if err != nil {
				t.Fatalf("Inject: %v", err)
			}
			fab.Run(100)

			var tracedMember string
			var copyJourney *fabric.Journey
			for _, journey := range fab.Report() {
				if journey.Parent == frameID && journey.Mirror == "span" {
					journey := journey
					copyJourney = &journey
				}
				if journey.FrameID != frameID {
					continue
				}
				for _, entry := range journey.Entries {
					if entry.Result == nil {
						continue
					}
					for _, step := range entry.Result.Steps {
						if step.RuleID != traffic.RuleMirrorCopy {
							continue
						}
						for _, fact := range step.Inputs {
							if fact.TypeID() == "lag.selection" && strings.Contains(fact.Canonical(), `;member="`+wantMember+`";`) {
								tracedMember = wantMember
							}
						}
					}
				}
			}
			if tracedMember != wantMember {
				t.Errorf("traced member = %q, want %q", tracedMember, wantMember)
			}
			if copyJourney == nil {
				t.Fatal("mirror copy journey is absent")
			}
			wantHost := map[string]string{"member-a": "sink-a", "member-b": "sink-b"}[wantMember]
			if len(copyJourney.Deliveries) != 1 || copyJourney.Deliveries[0].Host != wantHost {
				t.Errorf("mirror deliveries = %+v, want selected-member host %q", copyJourney.Deliveries, wantHost)
			}
		})
	}
}

func TestIngressPolicerDropsBeforeForwarding(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	fab, macs := newTrafficTopology(t, &traffic.Config{Policers: map[string]traffic.Policer{
		"1/1/1": {RateBPS: 1_000_000, BurstOctets: 10_000},
	}})
	spec := fab.Spec()
	catalog := analysis.EvidenceCatalog{}
	issues := make([]analysis.Issue, 0, 3)
	for _, scoped := range []struct {
		code  analysis.IssueCode
		scope analysis.Scope
	}{
		{code: "test.node-conflict", scope: analysis.NodeScope("sw1")},
		{code: "test.ingress-conflict", scope: analysis.PortScope("sw1", "1/1/1")},
		{code: "test.unrelated-conflict", scope: analysis.PortScope("sw1", "1/1/2")},
	} {
		var ref trace.EvidenceRef
		catalog, ref = catalog.Add(analysis.Evidence{
			Kind:    "snapshot",
			Origin:  scoped.code.String(),
			Context: "conflicting loaded state",
		})
		issues = append(issues, analysis.Issue{
			Code:     scoped.code,
			Status:   analysis.Unstable,
			Scope:    scoped.scope,
			Message:  "loaded state conflicts",
			Evidence: []trace.EvidenceRef{ref},
		})
	}
	sw1Spec := spec.Switches["sw1"]
	sw1Spec.Metadata = analysis.NewMetadata(analysis.NodeScope("sw1"), issues, catalog, nil)
	spec.Switches["sw1"] = sw1Spec
	fab, err := fabric.NewWithSpec(spec)
	if err != nil {
		t.Fatalf("NewWithSpec: %v", err)
	}
	var ids []fabric.FrameID
	for i := 0; i < 10; i++ {
		fid, err := fab.Inject(fabric.Injection{
			At:     t0,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:     macs["h2"],
				Src:     macs["h1"],
				Payload: make([]byte, 1000),
			},
		})
		if err != nil {
			t.Fatalf("Inject frame %d: %v", i+1, err)
		}
		ids = append(ids, fid)
	}
	fab.Run(1000)

	hops := 0
	var tenth fabric.Journey
	for _, journey := range fab.Report() {
		if journey.FrameID == ids[9] {
			tenth = journey
		}
		for _, entry := range journey.Entries {
			if entry.Kind == fabric.EntryHop && entry.Device == "sw1" && entry.Port == "1/1/1" {
				hops++
			}
		}
	}
	if hops != 9 {
		t.Errorf("sw1 ingress hops = %d, want 9", hops)
	}
	if len(tenth.Entries) == 0 || tenth.Entries[len(tenth.Entries)-1].Reason != traffic.ReasonPoliced {
		t.Errorf("tenth journey = %+v, want final policed drop", tenth.Entries)
	}
	policed := tenth.Entries[len(tenth.Entries)-1]
	if policed.Result == nil {
		t.Fatal("policed drop has no forwarding result")
	}
	wantStep := trace.Step{
		Layer:   traffic.Layer,
		Op:      trace.OpDrop,
		RuleID:  traffic.RulePolicerRefuse,
		Subject: trace.Subject{Kind: "port", Key: "1/1/1"},
		Outputs: []trace.Fact{traffic.PolicerDecisionFact(1_000_000, 10_000, 1038, false)},
	}
	if len(policed.Result.Steps) != 1 || !policed.Result.Steps[0].Equal(wantStep) {
		t.Errorf("policed trace steps = %+v, want %+v", policed.Result.Steps, []trace.Step{wantStep})
	}
	if policed.Result.Metadata.Status() != analysis.Unstable {
		t.Errorf("policed metadata status = %s, want Unstable", policed.Result.Metadata.Status())
	}
	wantIssues := map[analysis.IssueCode]bool{
		"test.node-conflict":    false,
		"test.ingress-conflict": false,
	}
	for _, issue := range policed.Result.Metadata.Issues() {
		if _, ok := wantIssues[issue.Code]; !ok {
			t.Errorf("policed metadata contains unrelated issue %q", issue.Code)
			continue
		}
		wantIssues[issue.Code] = true
		if len(issue.Evidence) != 1 {
			t.Errorf("policed issue %q evidence = %+v, want one reference", issue.Code, issue.Evidence)
		}
	}
	for code, found := range wantIssues {
		if !found {
			t.Errorf("policed metadata missing issue %q", code)
		}
	}
	counters := fab.Snapshot().Devices["sw1"].Counters["1/1/1"]
	if counters.InDiscards != 1 || counters.Discards[traffic.ReasonPoliced] != 1 {
		t.Errorf("sw1:1/1/1 counters = %+v, want one policed ingress discard", counters)
	}

	lastID, err := fab.Inject(fabric.Injection{
		At:     t0.Add(10 * time.Millisecond),
		Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{
			Dst:     macs["h2"],
			Src:     macs["h1"],
			Payload: make([]byte, 1000),
		},
	})
	if err != nil {
		t.Fatalf("Inject after refill: %v", err)
	}
	fab.Run(100)
	for _, journey := range fab.Report() {
		if journey.FrameID != lastID {
			continue
		}
		for _, entry := range journey.Entries {
			if entry.Kind == fabric.EntryHop && entry.Device == "sw1" && entry.Port == "1/1/1" {
				return
			}
		}
	}
	t.Error("frame after 10 ms refill did not reach sw1 forwarding")
}

func TestQueueMaximumRateAndPriorityCompose(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	newFabric := func() (*fabric.Fabric, map[string]netaddr.MAC) {
		return newTrafficTopology(t, &traffic.Config{Queues: map[string]traffic.PortQueues{
			"1/1/24": {MaxRateBPS: map[vlan.PCP]uint64{0: 100_000_000}},
		}})
	}
	injectLow := func(fab *fabric.Fabric, macs map[string]netaddr.MAC, at time.Time, dst byte) fabric.FrameID {
		t.Helper()
		fid, err := fab.Inject(fabric.Injection{
			At:     at,
			Origin: fabric.Endpoint{Node: "h1"},
			Frame: ethernet.Frame{
				Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, dst},
				Src:     macs["h1"],
				Payload: make([]byte, 46),
			},
		})
		if err != nil {
			t.Fatalf("Inject PCP 0: %v", err)
		}

		return fid
	}
	hopAt := func(fab *fabric.Fabric, fid fabric.FrameID) time.Time {
		t.Helper()
		for _, journey := range fab.Report() {
			if journey.FrameID != fid {
				continue
			}
			for _, entry := range journey.Entries {
				if entry.Kind == fabric.EntryHop && entry.Device == "sw2" && entry.Port == "1/1/24" {
					return entry.At
				}
			}
		}
		t.Fatalf("journey %d has no hop on sw2:1/1/24", fid)

		return time.Time{}
	}

	fab, macs := newFabric()
	firstID := injectLow(fab, macs, t0, 1)
	secondID := injectLow(fab, macs, t0.Add(time.Nanosecond), 2)
	fab.Run(100)
	if got := hopAt(fab, firstID); !got.Equal(t0.Add(2870 * time.Nanosecond)) {
		t.Errorf("first PCP 0 hop = %v, want %v", got, t0.Add(2870*time.Nanosecond))
	}
	if got := hopAt(fab, secondID); !got.Equal(t0.Add(9910 * time.Nanosecond)) {
		t.Errorf("second PCP 0 hop = %v, want %v", got, t0.Add(9910*time.Nanosecond))
	}

	fab, macs = newFabric()
	firstID = injectLow(fab, macs, t0, 1)
	secondID = injectLow(fab, macs, t0.Add(time.Nanosecond), 2)
	highID, err := fab.Inject(fabric.Injection{
		At:     t0.Add(700 * time.Nanosecond),
		Origin: fabric.Endpoint{Node: "sw1", Port: "1/1/2"},
		Frame: ethernet.Frame{
			Dst:     netaddr.MAC{0x00, 0xaa, 0xbb, 0xcc, 0xdd, 3},
			Src:     macs["h4"],
			Tags:    []vlan.Tag{{TPID: uint16(ethernet.EtherTypeDot1Q), PCP: 7, VID: 10}},
			Payload: make([]byte, 46),
		},
	})
	if err != nil {
		t.Fatalf("Inject PCP 7: %v", err)
	}
	fab.Run(100)
	highAt := hopAt(fab, highID)
	if want := t0.Add(3574 * time.Nanosecond); !highAt.Equal(want) {
		t.Errorf("PCP 7 hop = %v, want idle-link service at %v", highAt, want)
	}
	if lowAt := hopAt(fab, secondID); !highAt.Before(lowAt) {
		t.Errorf("PCP 7 hop = %v, second PCP 0 hop = %v; want PCP 7 first", highAt, lowAt)
	}
	if got := hopAt(fab, firstID); !got.Equal(t0.Add(2870 * time.Nanosecond)) {
		t.Errorf("first PCP 0 hop with priority traffic = %v, want %v", got, t0.Add(2870*time.Nanosecond))
	}
}

func TestMirrorJourneySkipsDownstreamPolicingAndMirroring(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	ports := func(names ...string) port.Table {
		t.Helper()
		builder := port.NewBuilder()
		for _, name := range names {
			builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		table, err := builder.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		return table
	}
	vid10 := vlan.ID(10)
	vid99 := vlan.ID(99)
	macH1 := netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	macH2 := netaddr.MAC{0x02, 0, 0, 0, 0, 2}
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports("1/1/1", "1/1/24"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "users", 99: "mirror"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1":  {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/24": {Tagged: []vlan.ID{99}},
					},
				}},
				Traffic: &traffic.Config{Mirrors: []traffic.Mirror{{
					Name: "upstream", SelectSrcPorts: []string{"1/1/1"}, OutputVLAN: &vid99,
				}}},
			},
			"sw2": {
				Ports: ports("1/1/1", "1/1/2", "1/1/24"),
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{99: "mirror"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1":  {PVID: &vid99, Untagged: []vlan.ID{99}},
						"1/1/2":  {PVID: &vid99, Untagged: []vlan.ID{99}},
						"1/1/24": {Tagged: []vlan.ID{99}},
					},
				}},
				Traffic: &traffic.Config{
					Mirrors:  []traffic.Mirror{{Name: "downstream", SelectAll: true, OutputPort: "1/1/2"}},
					Policers: map[string]traffic.Policer{"1/1/24": {RateBPS: 1, BurstOctets: 1}},
				},
			},
		},
		Hosts: map[string]fabric.Host{
			"h1": {Address: macH1},
			"h2": {Address: macH2},
			"h3": {Address: netaddr.MAC{0x02, 0, 0, 0, 0, 3}},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/24"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/24"}},
			{A: fabric.Endpoint{Node: "sw2", Port: "1/1/1"}, B: fabric.Endpoint{Node: "h2"}},
			{A: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}, B: fabric.Endpoint{Node: "h3"}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{
		At: t0, Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{Src: macH1, Dst: macH2, Payload: make([]byte, 46)},
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(100)

	var upstream *fabric.Journey
	for _, journey := range fab.Report() {
		if journey.Mirror == "downstream" {
			t.Errorf("downstream switch created another mirror journey: %+v", journey)
		}
		if journey.Mirror == "upstream" {
			journey := journey
			upstream = &journey
		}
	}
	if upstream == nil || len(upstream.Deliveries) != 1 || upstream.Deliveries[0].Host != "h2" {
		t.Fatalf("upstream mirror deliveries = %+v, want h2", upstream)
	}
	counters := fab.Snapshot().Devices["sw2"].Counters["1/1/24"]
	if counters.Discards[traffic.ReasonPoliced] != 0 {
		t.Errorf("downstream policer discards = %d, want 0", counters.Discards[traffic.ReasonPoliced])
	}
}

func TestCorruptArrivalSpendsNoPolicerTokens(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	base, macs := newTrafficTopology(t, &traffic.Config{Policers: map[string]traffic.Policer{
		"1/1/1": {RateBPS: 1, BurstOctets: 84},
	}})
	cfg := base.Config()
	cfg.Cables[0].Fault = fabric.Fault{Kind: fabric.FaultCorruptEveryNth, N: 1}
	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frame := ethernet.Frame{Src: macs["h1"], Dst: macs["h2"], Payload: make([]byte, 46)}
	if _, err := fab.Inject(fabric.Injection{At: t0, Origin: fabric.Endpoint{Node: "h1"}, Frame: frame}); err != nil {
		t.Fatalf("Inject corrupt frame: %v", err)
	}
	fab.Run(20)
	if err := fab.SetFault(cfg.Cables[0].A, cfg.Cables[0].B, fabric.Fault{}); err != nil {
		t.Fatalf("clear fault: %v", err)
	}
	goodID, err := fab.Inject(fabric.Injection{At: t0.Add(time.Millisecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: frame})
	if err != nil {
		t.Fatalf("Inject good frame: %v", err)
	}
	fab.Run(50)

	for _, journey := range fab.Report() {
		if journey.FrameID != goodID {
			continue
		}
		for _, entry := range journey.Entries {
			if entry.Kind == fabric.EntryHop && entry.Device == "sw1" {
				return
			}
		}
	}
	t.Error("good frame was not admitted after the corrupt arrival")
}

func TestLoopReentryIsPoliced(t *testing.T) {
	buildPorts := func() port.Table {
		builder := port.NewBuilder()
		for i := 1; i <= 3; i++ {
			builder.Add(port.Port{Name: fmt.Sprintf("1/1/%d", i), Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
		}
		table, err := builder.Build()
		if err != nil {
			t.Fatalf("build ports: %v", err)
		}

		return table
	}
	macH1 := netaddr.MAC{0x02, 0, 0, 0, 0, 1}
	fab, err := fabric.New(fabric.Config{
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: buildPorts(),
				Traffic: &traffic.Config{Policers: map[string]traffic.Policer{
					"1/1/2": {RateBPS: 1, BurstOctets: 84},
				}},
			},
			"sw2": {Ports: buildPorts()},
		},
		Hosts: map[string]fabric.Host{"h1": {Address: macH1}},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "sw2", Port: "1/1/3"}},
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := fab.Inject(fabric.Injection{
		At: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC), Origin: fabric.Endpoint{Node: "h1"},
		Frame: ethernet.Frame{Src: macH1, Dst: netaddr.MAC{0x02, 0, 0, 0, 0, 99}, Payload: make([]byte, 46)},
	}); err != nil {
		t.Fatalf("Inject: %v", err)
	}
	fab.Run(100)

	for _, journey := range fab.Report() {
		for i, entry := range journey.Entries {
			if entry.Kind != fabric.EntryLoop || entry.Device != "sw1" || entry.Port != "1/1/2" {
				continue
			}
			for _, later := range journey.Entries[i+1:] {
				if later.Kind == fabric.EntryDrop && later.Device == "sw1" && later.Port == "1/1/2" && later.Reason == traffic.ReasonPoliced {
					return
				}
			}
		}
	}
	t.Error("loop re-entry on sw1:1/1/2 was not policed")
}

func TestLogicalLAGQueueRateUsesPhysicalMemberClock(t *testing.T) {
	t0 := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	lagCfg := &lag.Config{LAGs: map[string]lag.LAG{
		"lag1": {Mode: lag.BalanceSLB, Members: map[string]lag.Member{"1/1/1": {}, "1/1/2": {}}},
	}}
	base, macH1, macH2 := newLagTopology(t, t0, lagCfg, nil)
	cfg := base.Config()
	swA := cfg.Switches["A"]
	swA.Traffic = &traffic.Config{Queues: map[string]traffic.PortQueues{
		"lag1": {MaxRateBPS: map[vlan.PCP]uint64{0: 100_000_000}},
	}}
	cfg.Switches["A"] = swA
	fab, err := fabric.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	frame := ethernet.Frame{Src: macH1, Dst: macH2, Payload: make([]byte, 46)}
	var ids []fabric.FrameID
	for i := range 2 {
		id, err := fab.Inject(fabric.Injection{At: t0.Add(time.Duration(i) * time.Nanosecond), Origin: fabric.Endpoint{Node: "h1"}, Frame: frame})
		if err != nil {
			t.Fatalf("Inject frame %d: %v", i+1, err)
		}
		ids = append(ids, id)
	}
	fab.Run(100)

	starts := make(map[fabric.FrameID]time.Time)
	ports := make(map[fabric.FrameID]string)
	for _, journey := range fab.Report() {
		for _, entry := range journey.Entries {
			if entry.Kind == fabric.EntryCrossing && entry.Device == "B" && (entry.Port == "1/1/1" || entry.Port == "1/1/2") {
				starts[journey.FrameID] = entry.At
				ports[journey.FrameID] = entry.Port
			}
		}
	}
	if ports[ids[0]] == "" || ports[ids[0]] != ports[ids[1]] {
		t.Fatalf("physical member ports = %q/%q, want the same selected member", ports[ids[0]], ports[ids[1]])
	}
	if got := starts[ids[1]].Sub(starts[ids[0]]); got != 6720*time.Nanosecond {
		t.Errorf("LAG member starts differ by %v, want 6720ns", got)
	}
}
