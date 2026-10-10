package fabric_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"
	"go.aledante.io/FlowSeer/src/common/sim/flow"

	hostmodel "go.aledante.io/FlowSeer/src/common/sim/device/host"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/layer/bridge"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

func TestLoneJumboThreshold(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mtu      int
		frameLen int
		issues   int
		fact     string
	}{
		{"unstated MTU", 0, 9018, 1, "depth_before_octets=0;frame_octets=9018;threshold_octets=1518"},
		{"stated MTU", 9000, 9018, 0, ""},
		{"stated MTU one octet over", 9000, 9019, 1, "depth_before_octets=0;frame_octets=9019;threshold_octets=9018"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fab, ep := mtuAccountingFabric(t, tc.mtu, nil)
			if (*fabric.ExportFabricEgress(fab))[ep] != nil {
				t.Fatal("queue must be empty before the lone frame")
			}
			payloadLen := tc.frameLen - 14
			frame := ethernet.Frame{Src: netaddr.MAC{2}, Dst: netaddr.MAC{3}, Payload: make([]byte, payloadLen)}
			raw, err := frame.Encode()
			if err != nil || len(raw) != tc.frameLen {
				t.Fatalf("encoded frame = %d octets, error %v, want %d", len(raw), err, tc.frameLen)
			}
			journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
			if !fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, journey, 0, "") {
				t.Fatal("unstated buffer refused the jumbo frame")
			}
			if got := fabric.ExportEgressQueueDepth((*fabric.ExportFabricEgress(fab))[ep])[0]; got != uint64(tc.frameLen) {
				t.Fatalf("depth = %d, want %d", got, tc.frameLen)
			}
			if got := countIssueScope(fab.Metadata().Issues(), fabric.IssueQueueBufferUnstated, analysis.PortScope(ep.Node, ep.Port)); got != tc.issues {
				t.Fatalf("queue-buffer-unstated issues = %d, want %d", got, tc.issues)
			}
			if len(journey.Entries) != tc.issues {
				t.Fatalf("threshold entries = %d, want %d", len(journey.Entries), tc.issues)
			}
			if tc.issues == 1 && journey.Entries[0].Step.Inputs[0].Canonical() != tc.fact {
				t.Fatalf("threshold fact = %s, want %s", journey.Entries[0].Step.Inputs[0].Canonical(), tc.fact)
			}
		})
	}
}

// queueBufferTopology builds one untagged switch joining h1, h2, and h3 at a
// gigabit, with an optional buffer stated on the port toward h3. A nil buffer
// leaves every queue unstated.
func queueBufferTopology(t *testing.T, buffer *uint64) (*fabric.Fabric, map[string]netaddr.MAC) {
	t.Helper()

	builder := port.NewBuilder()
	for _, name := range []string{"1/1/1", "1/1/2", "1/1/3"} {
		builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	}
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	vid10 := vlan.ID(10)
	var queues map[string]traffic.PortQueues
	if buffer != nil {
		queues = map[string]traffic.PortQueues{
			"1/1/3": {BufferOctets: map[vlan.PCP]uint64{0: *buffer}},
		}
	}

	macs := map[string]netaddr.MAC{
		"h1": {0x03, 0, 0, 0, 0, 0x01},
		"h2": {0x03, 0, 0, 0, 0, 0x02},
		"h3": {0x03, 0, 0, 0, 0, 0x03},
	}

	fab, err := assembly.New(assembly.Config{
		Start: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		Switches: map[string]vswitch.Config{
			"sw1": {
				Ports: ports,
				Bridge: &bridge.Config{VLAN: &bridge.VLAN{
					Table: map[vlan.ID]string{10: "VLAN10"},
					Switchports: map[string]bridge.Switchport{
						"1/1/1": {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/2": {PVID: &vid10, Untagged: []vlan.ID{10}},
						"1/1/3": {PVID: &vid10, Untagged: []vlan.ID{10}},
					},
				}},
				Traffic: &traffic.Config{Queues: queues},
			},
		},
		Hosts: map[string]hostmodel.Config{
			"h1": {Address: macs["h1"]},
			"h2": {Address: macs["h2"]},
			"h3": {Address: macs["h3"]},
		},
		Cables: []fabric.Cable{
			{A: fabric.Endpoint{Node: "h1"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: fabric.Endpoint{Node: "h2"}, B: fabric.Endpoint{Node: "sw1", Port: "1/1/2"}},
			{A: fabric.Endpoint{Node: "sw1", Port: "1/1/3"}, B: fabric.Endpoint{Node: "h3"}},
		},
		PhyAssumption: defaultPhyAssumption(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return fab, macs
}

func queueBufferFrame(src, dst netaddr.MAC) ethernet.Frame {
	return ethernet.Frame{Src: src, Dst: dst, Payload: make([]byte, 1000)}
}

// primeQueueBufferLearning teaches sw1 h3's address on the port toward h3, so
// a later h1 or h2 frame is known unicast rather than a flood.
func primeQueueBufferLearning(t *testing.T, fab *fabric.Fabric, macs map[string]netaddr.MAC) {
	t.Helper()
	if _, err := fab.Inject(fabric.Injection{
		At:     fab.Clock(),
		Origin: fabric.Endpoint{Node: "h3"},
		Frame:  ethernet.Frame{Src: macs["h3"], Dst: macs["h1"]},
	}); err != nil {
		t.Fatalf("prime Inject: %v", err)
	}
	fab.Run(fabric.RunOptions{Budget: 1000, Window: 0})
}

// unstatedBackedFabric returns a one-switch fabric with one port per name, all
// busy, so enqueueing two frames onto each marks every endpoint as an
// unstated-buffer queue that backed up.
func unstatedBackedFabric(t *testing.T, names []string) (*fabric.Fabric, []fabric.Endpoint) {
	t.Helper()

	builder := port.NewBuilder()
	for _, name := range names {
		builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	}
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	fab, err := assembly.New(assembly.Config{Switches: map[string]vswitch.Config{
		"sw1": {Ports: ports},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	eps := make([]fabric.Endpoint, 0, len(names))
	for _, name := range names {
		ep := fabric.Endpoint{Node: "sw1", Port: name}
		(*fabric.ExportFabricBusyUntil(fab))[ep] = fabric.ExportFabricClock(fab).Add(time.Hour)
		eps = append(eps, ep)
	}

	return fab, eps
}

func hasIssueScope(issues []analysis.Issue, code analysis.IssueCode, scope analysis.Scope) bool {
	return countIssueScope(issues, code, scope) > 0
}

func countIssueScope(issues []analysis.Issue, code analysis.IssueCode, scope analysis.Scope) int {
	count := 0
	for _, issue := range issues {
		if issue.Code == code && issue.Scope == scope {
			count++
		}
	}

	return count
}

// TestQueueBufferUnstatedIssueReachesFlow asserts a run whose egress queue with
// no stated buffer backs up raises queue-buffer-unstated on the port's scope,
// Fabric.Metadata reports it once, and the crossing frame's flow carries it. A
// Metadata read before the crossing does not.
func TestQueueBufferUnstatedIssueReachesFlow(t *testing.T) {
	fab, macs := queueBufferTopology(t, nil)
	primeQueueBufferLearning(t, fab, macs)
	constructionEvidence := assembly.Spec(fab).Evidence.Entries()

	scope := analysis.PortScope("sw1", "1/1/3")
	if got := fab.Metadata().IssuesFor(scope); len(got) != 0 {
		t.Fatalf("Metadata before the crossing holds %+v, want no issue", got)
	}

	// h1's and h2's frames arrive together, so the second appends at 2028
	// octets and crosses the 1518-octet threshold.
	at := fab.Clock()
	for _, host := range []string{"h1", "h2"} {
		if _, err := fab.Inject(fabric.Injection{
			At:        at,
			Origin:    fabric.Endpoint{Node: host},
			Frame:     queueBufferFrame(macs[host], macs["h3"]),
			Retention: fabric.RetainAggregate,
			Flow:      1,
		}); err != nil {
			t.Fatalf("Inject %s: %v", host, err)
		}
	}
	fab.Run(fabric.RunOptions{Budget: 1_000_000, Window: 0})

	issues := fab.Metadata().IssuesFor(scope)
	if len(issues) != 1 || issues[0].Code != fabric.IssueQueueBufferUnstated || issues[0].Status != analysis.Incomplete {
		t.Fatalf("Metadata issues on %v = %+v, want one queue-buffer-unstated at Incomplete", scope, issues)
	}

	flow := fab.Flows()[flow.ID(1)]
	if !hasIssueScope(flow.Metadata.Issues(), fabric.IssueQueueBufferUnstated, scope) {
		t.Errorf("flow metadata issues = %+v, want queue-buffer-unstated on %v", flow.Metadata.Issues(), scope)
	}
	var ref trace.EvidenceRef
	for _, issue := range flow.Metadata.Issues() {
		if issue.Code == fabric.IssueQueueBufferUnstated {
			if len(issue.Evidence) != 1 {
				t.Fatalf("flow queue issue evidence = %v, want one reference", issue.Evidence)
			}
			ref = issue.Evidence[0]
		}
	}
	if ref == "" {
		t.Fatal("aggregate flow lost its queue evidence reference")
	}
	if _, ok := flow.Metadata.Evidence().Lookup(ref); !ok {
		t.Errorf("aggregate flow catalog does not resolve %q", ref)
	}
	if got := assembly.Spec(fab).Evidence.Entries(); !slices.Equal(got, constructionEvidence) {
		t.Errorf("runtime crossing changed Spec evidence from %+v to %+v", constructionEvidence, got)
	}
}

// TestQueueBufferUnstatedThreshold asserts one 1014-octet frame below the
// 1518-octet threshold raises no issue, and the frame queued behind it at 2028
// octets raises one, because the crossing counts the enqueued frame.
func TestQueueBufferUnstatedThreshold(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := accountingEgressFrame()

	first := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, first, 0, "")
	if len((*fabric.ExportFabricUnstatedBacked(fab))) != 0 {
		t.Fatalf("one 1014-octet frame marked %v, want no mark below the 1518-octet threshold", (*fabric.ExportFabricUnstatedBacked(fab)))
	}
	if len(first.Entries) != 0 || len(fabric.ExportFabricRuntimeEvidence(fab).Entries()) != 0 {
		t.Fatalf("below-threshold enqueue recorded %d entries and %d evidence entries", len(first.Entries), len(fabric.ExportFabricRuntimeEvidence(fab).Entries()))
	}

	second := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 2, 2, second, 0, "")
	if _, marked := (*fabric.ExportFabricUnstatedBacked(fab))[ep]; !marked {
		t.Fatalf("the second frame left %v unmarked, want a mark at 2028 octets", (*fabric.ExportFabricUnstatedBacked(fab)))
	}
	if len(second.Entries) != 1 || second.Entries[0].Kind != fabric.EntryQueueThreshold {
		t.Errorf("crossing enqueue entries = %+v, want one queue threshold", second.Entries)
	}
}

// TestQueueBufferUnstatedMetadataIsStable covers the issue set across reads:
// an earlier Metadata value is unchanged by a later crossing, and the read after
// the crossings carries exactly one queue-buffer-unstated issue per marked
// endpoint.
func TestQueueBufferUnstatedMetadataIsStable(t *testing.T) {
	fab, eps := unstatedBackedFabric(t, []string{"1/1/1", "1/1/2", "1/1/3"})
	before := fab.Metadata()
	beforeCatalog := before.Evidence().Entries()

	frame := accountingEgressFrame()
	for _, ep := range eps {
		for range 2 {
			fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{}), 0, "")
		}
	}

	after := fab.Metadata()
	if got := before.Evidence().Entries(); !slices.Equal(got, beforeCatalog) {
		t.Errorf("old metadata evidence changed from %+v to %+v", beforeCatalog, got)
	}
	for _, ep := range eps {
		scope := analysis.PortScope(ep.Node, ep.Port)
		if got := countIssueScope(before.IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 0 {
			t.Errorf("the earlier Metadata value gained %d queue-buffer-unstated issues on %v after the crossing", got, scope)
		}
		if got := countIssueScope(after.IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 1 {
			t.Errorf("the read after the crossing holds %d queue-buffer-unstated issues on %v, want one", got, scope)
		}
	}
}

// TestQueueBufferUnstatedMarksOncePerEndpoint covers two PCPs crossing on one
// port: the endpoint is marked once and Metadata reports one issue.
func TestQueueBufferUnstatedMarksOncePerEndpoint(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := accountingEgressFrame()
	var recorded int
	for _, pcp := range []vlan.PCP{0, 7} {
		for range 2 {
			journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
			fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, journey, pcp, "")
			recorded += len(journey.Entries)
		}
	}

	if len((*fabric.ExportFabricUnstatedBacked(fab))) != 1 {
		t.Fatalf("marked endpoints = %v, want one", (*fabric.ExportFabricUnstatedBacked(fab)))
	}
	if recorded != 1 || len(fabric.ExportFabricRuntimeEvidence(fab).Entries()) != 1 {
		t.Errorf("two PCP crossings recorded %d entries and %d evidence values, want one each", recorded, len(fabric.ExportFabricRuntimeEvidence(fab).Entries()))
	}
	scope := analysis.PortScope(ep.Node, ep.Port)
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 1 {
		t.Errorf("Metadata holds %d queue-buffer-unstated issues, want one for the marked endpoint", got)
	}
}

// TestQueueBufferUnstatedAlreadyAboveThresholdRecordsNothing asserts that a queue
// already above threshold at its first unstated enqueue records nothing, because
// the ruling requires the enqueue to transition depth from at most one
// maximum-size frame to above it.
func TestQueueBufferUnstatedAlreadyAboveThresholdRecordsNothing(t *testing.T) {
	fab, members := lagAccountingFabric(t, 9000, nil)
	member := members[0]
	frame := accountingEgressFrame()

	for i := range 5 {
		j := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
		fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), member, "lag1", frame, uint64(i+1), fabric.FrameID(i+1), j, 0, "")
		if len(j.Entries) != 0 {
			t.Fatalf("enqueue %d under lag1 recorded entries: %+v", i, j.Entries)
		}
	}
	if len((*fabric.ExportFabricUnstatedBacked(fab))) != 0 {
		t.Fatalf("unstatedBacked marked unexpectedly: %v", (*fabric.ExportFabricUnstatedBacked(fab)))
	}

	journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), member, member.Port, frame, 6, 6, journey, 0, "")
	if len(journey.Entries) != 0 {
		t.Errorf("enqueue when depthBefore > threshold recorded entries = %+v, want none", journey.Entries)
	}
	if _, marked := (*fabric.ExportFabricUnstatedBacked(fab))[member]; marked {
		t.Errorf("member was marked when depthBefore > threshold: %v", (*fabric.ExportFabricUnstatedBacked(fab)))
	}
	if len(fabric.ExportFabricRuntimeEvidence(fab).Entries()) != 0 {
		t.Errorf("runtime evidence recorded when depthBefore > threshold: %+v", fabric.ExportFabricRuntimeEvidence(fab).Entries())
	}
}

// TestQueueBufferUnstatedHostScope covers a host's cable end: with no switch
// queue to state a buffer, the mark scopes to the host's node, uses kind host,
// and names the node in context.
func TestQueueBufferUnstatedHostScope(t *testing.T) {
	fab, _ := bufferAccountingFabric(t, nil)
	hostEnd := fabric.Endpoint{Node: "h1"}
	(*fabric.ExportFabricBusyUntil(fab))[hostEnd] = fabric.ExportFabricClock(fab).Add(time.Hour)
	frame := accountingEgressFrame()
	first := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), hostEnd, "", frame, 1, 1, first, 0, "")
	second := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), hostEnd, "", frame, 2, 2, second, 0, "")

	if _, marked := (*fabric.ExportFabricUnstatedBacked(fab))[hostEnd]; !marked {
		t.Fatalf("host end left unmarked: %v", (*fabric.ExportFabricUnstatedBacked(fab)))
	}
	scope := analysis.NodeScope("h1")
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 1 {
		t.Errorf("Metadata holds %d queue-buffer-unstated issues on the host node, want one", got)
	}
	if len(second.Entries) != 1 || second.Entries[0].Step == nil {
		t.Fatalf("host crossing entries = %+v, want queue step", second.Entries)
	}
	wantSubject := trace.Subject{Kind: "host", Key: `"h1"/"0"`}
	if got := second.Entries[0].Step.Subject; got != wantSubject {
		t.Errorf("host queue subject = %+v, want %+v", got, wantSubject)
	}
	ref := (*fabric.ExportFabricUnstatedBacked(fab))[hostEnd]
	ev, ok := fabric.ExportFabricRuntimeEvidence(fab).Lookup(ref)
	if !ok {
		t.Fatalf("runtime evidence for %q not found", ref)
	}
	wantContext := `;egress_port="h1";`
	if !strings.Contains(ev.Context, wantContext) {
		t.Errorf("evidence context %q does not contain %q", ev.Context, wantContext)
	}
}

// TestQueueBufferUnstatedForkIsIndependent covers a fork: marking on the fork
// leaves the source's cached Metadata unchanged.
func TestQueueBufferUnstatedForkIsIndependent(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	scope := analysis.PortScope(ep.Node, ep.Port)
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 0 {
		t.Fatalf("source Metadata before the fork holds %d queue-buffer-unstated issues, want none", got)
	}

	fork := fab.Fork()
	frame := accountingEgressFrame()
	for range 2 {
		fabric.ExportFabricEnqueueEgress(fork, (*fabric.ExportFabricClock(fork)), ep, ep.Port, frame, 1, 1, fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{}), 0, "")
	}

	if got := countIssueScope(fork.Metadata().IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 1 {
		t.Errorf("fork Metadata holds %d queue-buffer-unstated issues, want one", got)
	}
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 0 {
		t.Errorf("source Metadata holds %d queue-buffer-unstated issues after the fork marked, want none", got)
	}
	if len(fabric.ExportFabricRuntimeEvidence(fab).Entries()) != 0 {
		t.Errorf("source gained runtime evidence after fork marked: %+v", fabric.ExportFabricRuntimeEvidence(fab).Entries())
	}
	ref := (*fabric.ExportFabricUnstatedBacked(fork))[ep]
	if _, ok := fork.Metadata().Evidence().Lookup(ref); !ok {
		t.Errorf("fork catalog does not resolve %q", ref)
	}
	copyAfterCrossing := fork.Fork()
	if got := (*fabric.ExportFabricUnstatedBacked(copyAfterCrossing))[ep]; got != ref {
		t.Errorf("forked crossing ref = %q, want %q", got, ref)
	}
	if _, ok := copyAfterCrossing.Metadata().Evidence().Lookup(ref); !ok {
		t.Errorf("forked crossing catalog does not resolve %q", ref)
	}
}
