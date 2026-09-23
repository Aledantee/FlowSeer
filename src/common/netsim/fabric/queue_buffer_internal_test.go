package fabric

import (
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/port"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/traffic"
)

// queueBufferTopology builds one untagged switch joining h1, h2, and h3 at a
// gigabit, with an optional buffer stated on the port toward h3. A nil buffer
// leaves every queue unstated.
func queueBufferTopology(t *testing.T, buffer *uint64) (*Fabric, map[string]netaddr.MAC) {
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

	fab, err := New(Config{
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
		Hosts: map[string]Host{
			"h1": {Address: macs["h1"]},
			"h2": {Address: macs["h2"]},
			"h3": {Address: macs["h3"]},
		},
		Cables: []Cable{
			{A: Endpoint{Node: "h1"}, B: Endpoint{Node: "sw1", Port: "1/1/1"}},
			{A: Endpoint{Node: "h2"}, B: Endpoint{Node: "sw1", Port: "1/1/2"}},
			{A: Endpoint{Node: "sw1", Port: "1/1/3"}, B: Endpoint{Node: "h3"}},
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
func primeQueueBufferLearning(t *testing.T, fab *Fabric, macs map[string]netaddr.MAC) {
	t.Helper()
	if _, err := fab.Inject(Injection{
		At:     fab.Snapshot().Clock,
		Origin: Endpoint{Node: "h3"},
		Frame:  ethernet.Frame{Src: macs["h3"], Dst: macs["h1"]},
	}); err != nil {
		t.Fatalf("prime Inject: %v", err)
	}
	fab.Run(1000)
}

// unstatedBackedFabric returns a one-switch fabric with one port per name, all
// busy, so enqueueing two frames onto each marks every endpoint as an
// unstated-buffer queue that backed up.
func unstatedBackedFabric(t *testing.T, names []string) (*Fabric, []Endpoint) {
	t.Helper()

	builder := port.NewBuilder()
	for _, name := range names {
		builder.Add(port.Port{Name: name, Kind: port.Physical, AdminStatus: port.Up, OperStatus: port.Up})
	}
	ports, err := builder.Build()
	if err != nil {
		t.Fatalf("build ports: %v", err)
	}

	fab, err := New(Config{Switches: map[string]vswitch.Config{
		"sw1": {Ports: ports},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fab.initRunState()

	eps := make([]Endpoint, 0, len(names))
	for _, name := range names {
		ep := Endpoint{Node: "sw1", Port: name}
		fab.busyUntil[ep] = fab.clock.Add(time.Hour)
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

	scope := analysis.PortScope("sw1", "1/1/3")
	if got := fab.Metadata().IssuesFor(scope); len(got) != 0 {
		t.Fatalf("Metadata before the crossing holds %+v, want no issue", got)
	}

	// h1's and h2's frames arrive together, so the second appends at 2028
	// octets and crosses the 1518-octet threshold.
	at := fab.Snapshot().Clock
	for _, host := range []string{"h1", "h2"} {
		if _, err := fab.Inject(Injection{
			At:        at,
			Origin:    Endpoint{Node: host},
			Frame:     queueBufferFrame(macs[host], macs["h3"]),
			Retention: RetainAggregate,
			Flow:      1,
		}); err != nil {
			t.Fatalf("Inject %s: %v", host, err)
		}
	}
	fab.Run(1_000_000)

	issues := fab.Metadata().IssuesFor(scope)
	if len(issues) != 1 || issues[0].Code != IssueQueueBufferUnstated || issues[0].Status != analysis.Incomplete {
		t.Fatalf("Metadata issues on %v = %+v, want one queue-buffer-unstated at Incomplete", scope, issues)
	}

	flow := fab.Flows()[FlowID(1)]
	if !hasIssueScope(flow.Metadata.Issues(), IssueQueueBufferUnstated, scope) {
		t.Errorf("flow metadata issues = %+v, want queue-buffer-unstated on %v", flow.Metadata.Issues(), scope)
	}
}

// TestQueueBufferUnstatedThreshold asserts one 1014-octet frame below the
// 1518-octet threshold raises no issue, and the frame queued behind it at 2028
// octets raises one, because the crossing counts the enqueued frame.
func TestQueueBufferUnstatedThreshold(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := egressBufferFrame()

	fab.enqueueEgress(fab.clock, ep, ep.Port, frame, 1, 1, &Journey{}, 0, "")
	if len(fab.unstatedBacked) != 0 {
		t.Fatalf("one 1014-octet frame marked %v, want no mark below the 1518-octet threshold", fab.unstatedBacked)
	}

	fab.enqueueEgress(fab.clock, ep, ep.Port, frame, 1, 1, &Journey{}, 0, "")
	if _, marked := fab.unstatedBacked[ep]; !marked {
		t.Fatalf("the second frame left %v unmarked, want a mark at 2028 octets", fab.unstatedBacked)
	}
}

// TestQueueBufferUnstatedMetadataIsStable covers the issue set across reads:
// an earlier Metadata value is unchanged by a later crossing, and the read after
// the crossings carries exactly one queue-buffer-unstated issue per marked
// endpoint.
func TestQueueBufferUnstatedMetadataIsStable(t *testing.T) {
	fab, eps := unstatedBackedFabric(t, []string{"1/1/1", "1/1/2", "1/1/3"})
	before := fab.Metadata()

	frame := egressBufferFrame()
	for _, ep := range eps {
		for range 2 {
			fab.enqueueEgress(fab.clock, ep, ep.Port, frame, 1, 1, &Journey{}, 0, "")
		}
	}

	after := fab.Metadata()
	for _, ep := range eps {
		scope := analysis.PortScope(ep.Node, ep.Port)
		if got := countIssueScope(before.IssuesFor(scope), IssueQueueBufferUnstated, scope); got != 0 {
			t.Errorf("the earlier Metadata value gained %d queue-buffer-unstated issues on %v after the crossing", got, scope)
		}
		if got := countIssueScope(after.IssuesFor(scope), IssueQueueBufferUnstated, scope); got != 1 {
			t.Errorf("the read after the crossing holds %d queue-buffer-unstated issues on %v, want one", got, scope)
		}
	}
}

// TestQueueBufferUnstatedMarksOncePerEndpoint covers two PCPs crossing on one
// port: the endpoint is marked once and Metadata reports one issue.
func TestQueueBufferUnstatedMarksOncePerEndpoint(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	frame := egressBufferFrame()
	for _, pcp := range []vlan.PCP{0, 7} {
		for range 2 {
			fab.enqueueEgress(fab.clock, ep, ep.Port, frame, 1, 1, &Journey{}, pcp, "")
		}
	}

	if len(fab.unstatedBacked) != 1 {
		t.Fatalf("marked endpoints = %v, want one", fab.unstatedBacked)
	}
	scope := analysis.PortScope(ep.Node, ep.Port)
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), IssueQueueBufferUnstated, scope); got != 1 {
		t.Errorf("Metadata holds %d queue-buffer-unstated issues, want one for the marked endpoint", got)
	}
}

// TestQueueBufferUnstatedHostScope covers a host's cable end: with no switch
// queue to state a buffer, the mark scopes to the host's node.
func TestQueueBufferUnstatedHostScope(t *testing.T) {
	fab, _ := bufferAccountingFabric(t, nil)
	hostEnd := Endpoint{Node: "h1"}
	fab.busyUntil[hostEnd] = fab.clock.Add(time.Hour)
	frame := egressBufferFrame()
	for range 2 {
		fab.enqueueEgress(fab.clock, hostEnd, "", frame, 1, 1, &Journey{}, 0, "")
	}

	if _, marked := fab.unstatedBacked[hostEnd]; !marked {
		t.Fatalf("host end left unmarked: %v", fab.unstatedBacked)
	}
	scope := analysis.NodeScope("h1")
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), IssueQueueBufferUnstated, scope); got != 1 {
		t.Errorf("Metadata holds %d queue-buffer-unstated issues on the host node, want one", got)
	}
}

// TestQueueBufferUnstatedForkIsIndependent covers a fork: marking on the fork
// leaves the source's cached Metadata unchanged.
func TestQueueBufferUnstatedForkIsIndependent(t *testing.T) {
	fab, ep := bufferAccountingFabric(t, nil)
	scope := analysis.PortScope(ep.Node, ep.Port)
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), IssueQueueBufferUnstated, scope); got != 0 {
		t.Fatalf("source Metadata before the fork holds %d queue-buffer-unstated issues, want none", got)
	}

	fork := fab.Fork()
	frame := egressBufferFrame()
	for range 2 {
		fork.enqueueEgress(fork.clock, ep, ep.Port, frame, 1, 1, &Journey{}, 0, "")
	}

	if got := countIssueScope(fork.Metadata().IssuesFor(scope), IssueQueueBufferUnstated, scope); got != 1 {
		t.Errorf("fork Metadata holds %d queue-buffer-unstated issues, want one", got)
	}
	if got := countIssueScope(fab.Metadata().IssuesFor(scope), IssueQueueBufferUnstated, scope); got != 0 {
		t.Errorf("source Metadata holds %d queue-buffer-unstated issues after the fork marked, want none", got)
	}
}
