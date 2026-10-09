package fabric_test

import (
	"strconv"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/sim/assembly"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device/vswitch"
	"go.aledante.io/FlowSeer/src/common/sim/fabric"
	"go.aledante.io/FlowSeer/src/common/sim/layer/traffic"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// bufferAccountingFabric returns a one-port fabric whose endpoint is already
// busy, so enqueueEgress records egress depth instead of draining the queue
// through a link it does not have. A nil buffer leaves the queue unstated.
func bufferAccountingFabric(t *testing.T, buffer *uint64) (*fabric.Fabric, fabric.Endpoint) {
	t.Helper()

	return mtuAccountingFabric(t, 0, buffer)
}

// mtuAccountingFabric is [bufferAccountingFabric] with a stated port MTU, so a
// test can pin the unstated-buffer threshold to the port's own maximum-size
// frame.
func mtuAccountingFabric(t *testing.T, mtu int, buffer *uint64) (*fabric.Fabric, fabric.Endpoint) {
	t.Helper()

	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "1/1/1", Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, MTU: mtu})
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	var queues map[string]traffic.PortQueues
	if buffer != nil {
		queues = map[string]traffic.PortQueues{
			"1/1/1": {BufferOctets: map[vlan.PCP]uint64{0: *buffer}},
		}
	}

	fab, err := assembly.New(assembly.Config{Switches: map[string]vswitch.Config{
		"sw1": {Ports: ports, Traffic: &traffic.Config{Queues: queues}},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fabric.ExportFabricInitRunState(fab)

	ep := fabric.Endpoint{Node: "sw1", Port: "1/1/1"}
	(*fabric.ExportFabricBusyUntil(fab))[ep] = fabric.ExportFabricClock(fab).Add(time.Hour)

	return fab, ep
}

// lagAccountingFabric returns a one-switch fabric with a LAG "lag1" over
// members "1/1/1" and "1/1/2". The LAG states the MTU and any buffer; the
// members state neither, so a lookup against a member would miss both. Every
// member endpoint is busy, so enqueueEgress records depth instead of draining
// through a link the fabric does not have.
func lagAccountingFabric(t *testing.T, lagMTU int, buffer *uint64) (*fabric.Fabric, []fabric.Endpoint) {
	t.Helper()

	builder := port.NewBuilder()
	builder.Add(port.Port{Name: "lag1", Kind: port.LAG, AdminStatus: port.Up, OperStatus: port.Up, MTU: lagMTU})
	members := []string{"1/1/1", "1/1/2"}
	for _, name := range members {
		builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up, LagParent: "lag1"})
	}
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	var queues map[string]traffic.PortQueues
	if buffer != nil {
		queues = map[string]traffic.PortQueues{
			"lag1": {BufferOctets: map[vlan.PCP]uint64{0: *buffer}},
		}
	}

	fab, err := assembly.New(assembly.Config{Switches: map[string]vswitch.Config{
		"sw1": {Ports: ports, Traffic: &traffic.Config{Queues: queues}},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fabric.ExportFabricInitRunState(fab)

	eps := make([]fabric.Endpoint, 0, len(members))
	for _, name := range members {
		ep := fabric.Endpoint{Node: "sw1", Port: name}
		(*fabric.ExportFabricBusyUntil(fab))[ep] = fabric.ExportFabricClock(fab).Add(time.Hour)
		eps = append(eps, ep)
	}

	return fab, eps
}

func accountingEgressFrame() ethernet.Frame {
	return ethernet.Frame{Src: netaddr.MAC{0x02}, Dst: netaddr.MAC{0x03}, Payload: make([]byte, 1000)}
}

func TestEgressUnstatedThresholdRecordsEvidenceAtEnqueue(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := accountingEgressFrame()
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{}), 0, "")
	journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 2, 2, journey, 0, "")

	if got, want := len(journey.Entries), 1; got != want {
		t.Fatalf("entries at enqueue = %d, want %d", got, want)
	}
	entry := journey.Entries[0]
	if entry.Kind != fabric.EntryQueueThreshold || entry.Step == nil {
		t.Fatalf("entry = %+v, want queue threshold step", entry)
	}
	if entry.Step.Op != trace.OpQueue || entry.Step.RuleID != traffic.RuleQueueBufferUnstated {
		t.Errorf("step = %+v, want queue operation and unstated-buffer rule", entry.Step)
	}
	if got, want := entry.Step.Inputs[0].Canonical(), "depth_before_octets=1014;frame_octets=1014;threshold_octets=1518"; got != want {
		t.Errorf("queue fact = %q, want %q", got, want)
	}
	if got, want := entry.Step.Subject, (trace.Subject{Kind: "port", Key: strconv.Quote(ep.Port) + "/" + strconv.Quote("0")}); got != want {
		t.Errorf("subject = %+v, want %+v", got, want)
	}
	if len(entry.Step.Evidence) != 1 {
		t.Fatalf("step evidence = %v, want one reference", entry.Step.Evidence)
	}
	ref := entry.Step.Evidence[0]
	value, ok := fabric.ExportFabricRuntimeEvidence(fab).Lookup(ref)
	if !ok || value.Kind != "fabric.runtime" || value.Origin != "egress-queue" {
		t.Fatalf("runtime evidence = %+v, found %t", value, ok)
	}
	if _, ok := fab.Metadata().Evidence().Lookup(ref); !ok {
		t.Error("queue evidence is missing from the catalog")
	}
	var matching []analysis.Issue
	for _, issue := range fab.Metadata().IssuesFor(analysis.PortScope(ep.Node, ep.Port)) {
		if issue.Code == fabric.IssueQueueBufferUnstated {
			matching = append(matching, issue)
		}
	}
	if len(matching) != 1 || len(matching[0].Evidence) != 1 || matching[0].Evidence[0] != ref {
		t.Fatalf("queue issues = %+v, want same reference %q", matching, ref)
	}
	for name, metadata := range map[string]analysis.Metadata{"fabric": fab.Metadata(), "journey": journey.Metadata} {
		if _, ok := metadata.Evidence().Lookup(ref); !ok {
			t.Errorf("%s catalog does not resolve %q", name, ref)
		}
	}
}

func TestEgressLagThresholdKeepsLogicalSubjectAndPhysicalIssue(t *testing.T) {
	fab, members := lagAccountingFabric(t, 0, nil)
	member := members[0]
	frame := accountingEgressFrame()
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), member, "lag1", frame, 1, 1, fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{}), 5, "")
	journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), member, "lag1", frame, 2, 2, journey, 5, "")
	if len(journey.Entries) != 1 || journey.Entries[0].Step == nil {
		t.Fatalf("LAG crossing entries = %+v, want queue step", journey.Entries)
	}
	entry := journey.Entries[0]
	if got, want := entry.Step.Subject, (trace.Subject{Kind: "port", Key: `"lag1"/"5"`}); got != want {
		t.Errorf("logical queue subject = %+v, want %+v", got, want)
	}
	if entry.Device != member.Node || entry.Port != member.Port {
		t.Errorf("entry endpoint = %s/%s, want %s/%s", entry.Device, entry.Port, member.Node, member.Port)
	}
	scope := analysis.PortScope(member.Node, member.Port)
	issues := journey.Metadata.IssuesFor(scope)
	if got := countIssueScope(issues, fabric.IssueQueueBufferUnstated, scope); got != 1 {
		t.Errorf("member scoped queue issues = %d, want one", got)
	}
	if len(entry.Step.Evidence) != 1 {
		t.Fatalf("step evidence = %v, want one reference", entry.Step.Evidence)
	}
	var memberIssue *analysis.Issue
	for i := range issues {
		if issues[i].Code == fabric.IssueQueueBufferUnstated && issues[i].Scope == scope {
			memberIssue = &issues[i]
			break
		}
	}
	if memberIssue == nil || len(memberIssue.Evidence) != 1 {
		t.Fatalf("member queue issue = %+v, want one evidence reference", memberIssue)
	}
	if got, want := entry.Step.Evidence[0], memberIssue.Evidence[0]; got != want {
		t.Errorf("step evidence ref %q != member issue ref %q", got, want)
	}
}

// jumboEgressFrame is one 9000-octet-payload frame, 9014 encoded octets.
func jumboEgressFrame() ethernet.Frame {
	return ethernet.Frame{Src: netaddr.MAC{0x02}, Dst: netaddr.MAC{0x03}, Payload: make([]byte, 9000)}
}

// TestEgressQueueDepthAndPeak covers the octet accounting: ten 1014-octet
// frames make a depth and peak of ten frames' octets, the pop returns the
// depth to zero, and the peak survives the drain.
func TestEgressQueueDepthAndPeak(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := accountingEgressFrame()
	journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	for range 10 {
		fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, journey, 0, "")
	}

	q := (*fabric.ExportFabricEgress(fab))[ep]
	if got, want := fabric.ExportEgressQueueDepth(q)[0], uint64(10*1014); got != want {
		t.Fatalf("depth after ten enqueues = %d, want %d", got, want)
	}
	if got, want := fabric.ExportEgressQueuePeak(q)[0], uint64(10*1014); got != want {
		t.Fatalf("peak after ten enqueues = %d, want %d", got, want)
	}
	(*fabric.ExportFabricBusyUntil(fab))[ep] = time.Time{}
	fabric.ExportFabricRemoveDequeue(fab, ep)
	fabric.ExportFabricServe(fab, fabric.ExportFabricClock(fab).Add(2*time.Hour), ep)
	if got := fabric.ExportEgressQueueDepth(q)[0]; got != 0 {
		t.Errorf("depth after the queue drained = %d, want 0", got)
	}
	if got, want := fabric.ExportEgressQueuePeak(q)[0], uint64(10*1014); got != want {
		t.Errorf("peak after the queue drained = %d, want it unchanged at %d", got, want)
	}
}

// TestEgressStatedBufferBoundary covers the admit line: a stated buffer of
// 2028 admits a second 1014-octet frame because depth+octets equals it, while
// 2027 refuses it because depth+octets exceeds it. A wire-octet count would
// refuse at 2028, since two frames are 2076 wire octets together.
func TestEgressStatedBufferBoundary(t *testing.T) {
	for _, tc := range []struct {
		buffer uint64
		admits int
	}{
		{buffer: 2028, admits: 2},
		{buffer: 2027, admits: 1},
	} {
		fab, ep := bufferAccountingFabric(t, &tc.buffer)
		frame := accountingEgressFrame()
		journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
		for range 2 {
			fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, journey, 0, "")
		}

		if got, want := fabric.ExportEgressQueueDepth((*fabric.ExportFabricEgress(fab))[ep])[0], uint64(tc.admits*1014); got != want {
			t.Errorf("buffer %d: depth = %d, want %d", tc.buffer, got, want)
		}
		if got, want := len(journey.Entries), 2-tc.admits; got != want {
			t.Errorf("buffer %d: drop entries = %d, want %d", tc.buffer, got, want)
		}
		var outDiscards, queueFull uint64
		if c := (*fabric.ExportFabricCounters(fab))[ep]; c != nil {
			outDiscards = c.OutDiscards
			queueFull = c.Discards[traffic.ReasonQueueFull]
		}
		if got, want := outDiscards, uint64(2-tc.admits); got != want {
			t.Errorf("buffer %d: OutDiscards = %d, want %d", tc.buffer, got, want)
		}
		if got, want := queueFull, uint64(2-tc.admits); got != want {
			t.Errorf("buffer %d: Discards[queue-full] = %d, want %d", tc.buffer, got, want)
		}
	}
}

// TestEgressStatedBufferNeverMarksUnstated covers a stated queue that backs up
// past one maximum-size frame: it keeps enforcing only its stated buffer and
// never marks the endpoint as an unstated-buffer queue.
func TestEgressStatedBufferNeverMarksUnstated(t *testing.T) {
	buffer := uint64(4000)
	fab, ep := bufferAccountingFabric(t, &buffer)
	frame := accountingEgressFrame()
	for range 2 {
		fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{}), 0, "")
	}

	if got, want := fabric.ExportEgressQueueDepth((*fabric.ExportFabricEgress(fab))[ep])[0], uint64(2*1014); got != want {
		t.Fatalf("depth = %d, want %d: the 4000-octet buffer holds both frames", got, want)
	}
	if len((*fabric.ExportFabricUnstatedBacked(fab))) != 0 {
		t.Fatalf("a stated-buffer queue marked endpoints %v, want none", (*fabric.ExportFabricUnstatedBacked(fab)))
	}
	scope := analysis.PortScope(ep.Node, ep.Port)
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 0 {
		t.Errorf("Metadata holds %d queue-buffer-unstated issues, want none on a stated queue", got)
	}
}

// TestEgressUnstatedThresholdUsesPortMTU covers the threshold of an
// unstated-buffer queue on a port that states its own MTU: a 9000-octet MTU
// makes the threshold 9018 octets, so two 1014-octet frames stay below it and
// only a depth past 9018 marks the endpoint. A fixed 1518-octet threshold would
// mark at 2028.
func TestEgressUnstatedThresholdUsesPortMTU(t *testing.T) {
	fab, ep := mtuAccountingFabric(t, 9000, nil)
	frame := accountingEgressFrame()
	journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})

	for range 2 {
		fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, journey, 0, "")
	}
	if len((*fabric.ExportFabricUnstatedBacked(fab))) != 0 {
		t.Fatalf("depth 2028 marked %v, want none below the 9018-octet threshold", (*fabric.ExportFabricUnstatedBacked(fab)))
	}

	for range 7 {
		fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, journey, 0, "")
	}
	if _, marked := (*fabric.ExportFabricUnstatedBacked(fab))[ep]; !marked {
		t.Fatalf("depth 9126 left %v unmarked, want a mark past the 9018-octet threshold", (*fabric.ExportFabricUnstatedBacked(fab)))
	}
}

// TestEgressLagBufferEnforcedOnMember covers a buffer stated on a LAG name: it
// is read from the LAG and enforced on the selected member's own egress queue,
// so the occupancy and the drop live on the member endpoint, not the LAG name.
func TestEgressLagBufferEnforcedOnMember(t *testing.T) {
	buffer := uint64(2000)
	fab, eps := lagAccountingFabric(t, 0, &buffer)
	member := eps[0]
	frame := accountingEgressFrame()
	journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	for range 2 {
		fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), member, "lag1", frame, 1, 1, journey, 0, "")
	}

	if got := fabric.ExportEgressQueueDepth((*fabric.ExportFabricEgress(fab))[member])[0]; got != 1014 {
		t.Errorf("member depth = %d, want 1014: the 2000-octet buffer refuses the second frame", got)
	}
	if _, keyed := (*fabric.ExportFabricEgress(fab))[fabric.Endpoint{Node: "sw1", Port: "lag1"}]; keyed {
		t.Errorf("the LAG name holds an egress queue; a LAG buffer is enforced per member")
	}
	if got, want := len(journey.Entries), 1; got != want {
		t.Errorf("drop entries = %d, want %d", got, want)
	}
	if got := fab.Snapshot().EgressDepths[member][0].Depth; got != 1014 {
		t.Errorf("EgressDepths[%v][0].Depth = %d, want 1014, keyed by the member endpoint", member, got)
	}
	scope := analysis.PortScope(member.Node, member.Port)
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 0 {
		t.Errorf("a stated LAG buffer raised %d queue-buffer-unstated issues, want none", got)
	}
}

// TestEgressMemberNamedProtocolFrameReadsLagBuffer covers a protocol frame
// sent on a member under the member's own name: the LAG's stated buffer
// answers it, so the member queue tail-drops and raises no
// queue-buffer-unstated issue.
func TestEgressMemberNamedProtocolFrameReadsLagBuffer(t *testing.T) {
	buffer := uint64(2000)
	fab, eps := lagAccountingFabric(t, 0, &buffer)
	member := eps[0]
	frame := accountingEgressFrame()
	journey := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	for range 2 {
		fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), member, member.Port, frame, 1, 1, journey, 0, "")
	}

	if got := fabric.ExportEgressQueueDepth((*fabric.ExportFabricEgress(fab))[member])[0]; got != 1014 {
		t.Errorf("member depth = %d, want 1014: the LAG's 2000-octet buffer refuses the second frame", got)
	}
	scope := analysis.PortScope(member.Node, member.Port)
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), fabric.IssueQueueBufferUnstated, scope); got != 0 {
		t.Errorf("a protocol frame on a member of a LAG with a stated buffer raised %d queue-buffer-unstated issues, want none", got)
	}
}

// TestEgressLagThresholdUsesLagMTU covers the threshold of an unstated-buffer
// member queue behind a LAG that states an MTU: the threshold comes from the
// LAG name, not the member, so one 9014-octet frame on a member with no MTU of
// its own stays below 9018 and does not mark. Reading the member's unset MTU
// would fall back to 1518 and mark.
func TestEgressLagThresholdUsesLagMTU(t *testing.T) {
	fab, eps := lagAccountingFabric(t, 9000, nil)
	member := eps[0]
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), member, "lag1", jumboEgressFrame(), 1, 1, fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{}), 0, "")

	if len((*fabric.ExportFabricUnstatedBacked(fab))) != 0 {
		t.Fatalf("one 9014-octet frame marked %v, want none below the LAG's 9018-octet threshold", (*fabric.ExportFabricUnstatedBacked(fab)))
	}
}

// TestEgressForkKeepsOwnPeak covers the fork: a later enqueue on the fork
// raises the fork's peak without moving the source's.
func TestEgressForkKeepsOwnPeak(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := accountingEgressFrame()
	fabric.ExportFabricEnqueueEgress(fab, (*fabric.ExportFabricClock(fab)), ep, ep.Port, frame, 1, 1, fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{}), 0, "")

	fork := fab.Fork()
	forkJourney := fabric.MakeTestJourneyPointer(fabric.ExportJourneyFields{})
	for range 3 {
		fabric.ExportFabricEnqueueEgress(fork, (*fabric.ExportFabricClock(fork)), ep, ep.Port, frame, 1, 1, forkJourney, 0, "")
	}

	if got := fabric.ExportEgressQueuePeak((*fabric.ExportFabricEgress(fab))[ep])[0]; got != 1014 {
		t.Errorf("source peak = %d, want 1014", got)
	}
	if got, want := fabric.ExportEgressQueuePeak((*fabric.ExportFabricEgress(fork))[ep])[0], uint64(4*1014); got != want {
		t.Errorf("fork peak = %d, want %d", got, want)
	}
}
