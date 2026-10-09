package fabric

import (
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/device"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// ExportArrivalFields exposes internal state to external package tests.
type ExportArrivalFields struct {
	At      time.Time
	Kind    ArrivalKind
	Seq     uint64
	Device  string
	Port    string
	FrameID FrameID
	Frame   ethernet.Frame
	Corrupt bool
	Index   int
}

// ExportAssignJourneyState exposes internal state to external package tests.
var ExportAssignJourneyState = assignJourneyState

// ExportAttachedSource exposes internal state to external package tests.
type ExportAttachedSource = attachedSource

// ExportAttachedSourceFields exposes internal state to external package tests.
type ExportAttachedSourceFields struct {
	Attachment StreamAttachment
	At         time.Time
	Frame      ethernet.Frame
	Peeked     bool
	Ended      bool
}

// ExportCompareArrival exposes internal state to external package tests.
var ExportCompareArrival = compareArrival

// ExportEgressQueue exposes internal state to external package tests.
type ExportEgressQueue = egressQueue

// ExportEgressQueueFields exposes internal state to external package tests.
type ExportEgressQueueFields struct {
	Pending        [8][]queued
	DequeueAt      time.Time
	DequeuePending bool
	RateClock      [8]time.Time
	Depth          [8]uint64
	Peak           [8]uint64
}

// ExportFabricAttachments exposes internal state to external package tests.
func ExportFabricAttachments(x *Fabric) *[]attachedSource { return &x.attachments }

// ExportFabricBusyUntil exposes internal state to external package tests.
func ExportFabricBusyUntil(x *Fabric) *map[Endpoint]time.Time { return &x.busyUntil }

// ExportFabricByEnd exposes internal state to external package tests.
func ExportFabricByEnd(x *Fabric) *map[Endpoint]linkEndRef { return &x.byEnd }

// ExportFabricCableCrossings exposes internal state to external package tests.
func ExportFabricCableCrossings(x *Fabric) *map[Endpoint]uint { return &x.cableCrossings }

// ExportFabricClock exposes internal state to external package tests.
func ExportFabricClock(x *Fabric) *time.Time { return &x.clock }

// ExportFabricCounters exposes internal state to external package tests.
func ExportFabricCounters(x *Fabric) *map[Endpoint]*Counters { return &x.counters }

// ExportFabricDequeueItems exposes internal state to external package tests.
func ExportFabricDequeueItems(x *Fabric) *map[Endpoint]int { return &x.dequeueItems }

// ExportFabricEgress exposes internal state to external package tests.
func ExportFabricEgress(x *Fabric) *map[Endpoint]*egressQueue { return &x.egress }

// ExportFabricEnqueue exposes internal state to external package tests.
func ExportFabricEnqueue(x *Fabric, a0 Arrival) { x.enqueue(a0) }

// ExportFabricEnqueueEgress exposes internal state to external package tests.
func ExportFabricEnqueueEgress(x *Fabric, a0 time.Time, a1 Endpoint, a2 string, a3 ethernet.Frame, a4 uint64, a5 FrameID, a6 *journey, a7 vlan.PCP, a8 string) bool {
	return x.enqueueEgress(a0, a1, a2, a3, a4, a5, a6, a7, a8)
}

// ExportFabricEntered exposes internal state to external package tests.
func ExportFabricEntered(x *Fabric) *map[FrameID]map[Endpoint]bool { return &x.entered }

// ExportHoldKey exposes device-scoped hold identity to external tests.
type ExportHoldKey = holdKey

// MakeTestHoldKey returns a hold key for a device token.
func MakeTestHoldKey(node string, token device.HoldToken) holdKey {
	return holdKey{node: node, token: token}
}

// ExportFabricFields exposes internal state to external package tests.
type ExportFabricFields struct {
	Topology        topology
	Evidence        analysis.EvidenceCatalog
	RuntimeEvidence analysis.EvidenceCatalog
	Links           []Link
	LinkTrust       []linkTrust
	Uncabled        map[Endpoint]Uncabled
	ByEnd           map[Endpoint]linkEndRef
	Clock           time.Time
	Stepped         bool
	Queue           []Arrival
	Attachments     []attachedSource
	Wakes           map[string]time.Time
	DequeueItems    map[Endpoint]int
	WakeItems       map[string]int
	NextFrameID     FrameID
	NextSeq         uint64
	Journeys        map[FrameID]*journey
	Entered         map[FrameID]map[Endpoint]bool
	CableCrossings  map[Endpoint]uint
	BusyUntil       map[Endpoint]time.Time
	Egress          map[Endpoint]*egressQueue
	Counters        map[Endpoint]*Counters
	UnstatedBacked  map[Endpoint]trace.EvidenceRef
	MetadataCache   *analysis.Metadata
	Inflight        map[FrameID]int
	Held            map[holdKey]FrameID
	Flows           map[FlowID]*FlowStats
	Touched         []FrameID
	Err             error
}

// ExportFabricFlows exposes internal state to external package tests.
func ExportFabricFlows(x *Fabric) *map[FlowID]*FlowStats { return &x.flows }

// ExportFabricHasPendingJourneys exposes internal state to external package tests.
func ExportFabricHasPendingJourneys(x *Fabric) bool { return x.hasPendingJourneys() }

// ExportFabricHeld exposes internal state to external package tests.
func ExportFabricHeld(x *Fabric) *map[holdKey]FrameID { return &x.held }

// ExportFabricInflight exposes internal state to external package tests.
func ExportFabricInflight(x *Fabric) *map[FrameID]int { return &x.inflight }

// ExportFabricJourneys exposes internal state to external package tests.
func ExportFabricJourneys(x *Fabric) *map[FrameID]*journey { return &x.journeys }

// ExportFabricLinkTrust exposes internal state to external package tests.
func ExportFabricLinkTrust(x *Fabric) *[]linkTrust { return &x.linkTrust }

// ExportFabricLinks exposes internal state to external package tests.
func ExportFabricLinks(x *Fabric) *[]Link { return &x.links }

// ExportFabricMetadataCache exposes internal state to external package tests.
func ExportFabricMetadataCache(x *Fabric) **analysis.Metadata { return &x.metadataCache }

// ExportFabricNextFrameID exposes internal state to external package tests.
func ExportFabricNextFrameID(x *Fabric) *FrameID { return &x.nextFrameID }

// ExportFabricNextSeq exposes internal state to external package tests.
func ExportFabricNextSeq(x *Fabric) *uint64 { return &x.nextSeq }

// ExportFabricNodeDevice exposes internal state to external package tests.
func ExportFabricNodeDevice(x *Fabric, a0 string) device.Device { return x.nodeDevice(a0) }

// ExportFabricPendingWork exposes internal state to external package tests.
func ExportFabricPendingWork(x *Fabric) PendingWork { return x.pendingWork() }

// ExportFabricPopArrival exposes internal state to external package tests.
func ExportFabricPopArrival(x *Fabric) Arrival { return x.popArrival() }

// ExportFabricQueue exposes internal state to external package tests.
func ExportFabricQueue(x *Fabric) *[]Arrival { return &x.queue }

// ExportFabricRemoveDequeue exposes internal state to external package tests.
func ExportFabricRemoveDequeue(x *Fabric, a0 Endpoint) { x.removeDequeue(a0) }

// ExportFabricRemoveDequeueArrival exposes internal state to external package tests.
func ExportFabricRemoveDequeueArrival(x *Fabric, a0 Endpoint) { x.removeDequeueArrival(a0) }

// ExportFabricRemoveWakeArrival exposes internal state to external package tests.
func ExportFabricRemoveWakeArrival(x *Fabric, a0 string) { x.removeWakeArrival(a0) }

// ExportFabricRun exposes internal state to external package tests.
func ExportFabricRun(x *Fabric, a0 int, a1 int) RunResult { return x.run(a0, a1) }

// ExportFabricRuntimeEvidence exposes internal state to external package tests.
func ExportFabricRuntimeEvidence(x *Fabric) *analysis.EvidenceCatalog { return &x.runtimeEvidence }

// ExportFabricScheduleDequeue exposes internal state to external package tests.
func ExportFabricScheduleDequeue(x *Fabric, a0 Endpoint, a1 time.Time) { x.scheduleDequeue(a0, a1) }

// ExportFabricServe exposes internal state to external package tests.
func ExportFabricServe(x *Fabric, a0 time.Time, a1 Endpoint) { x.serve(a0, a1) }

// ExportFabricSettle exposes internal state to external package tests.
func ExportFabricSettle(x *Fabric, a0 FrameID) { x.settle(a0) }

// ExportFabricStepped exposes internal state to external package tests.
func ExportFabricStepped(x *Fabric) *bool { return &x.stepped }

// ExportFabricTopology exposes internal state to external package tests.
func ExportFabricTopology(x *Fabric) *topology { return &x.topology }

// ExportFabricUncabled exposes internal state to external package tests.
func ExportFabricUncabled(x *Fabric) *map[Endpoint]Uncabled { return &x.uncabled }

// ExportFabricUnlinked exposes internal state to external package tests.
func ExportFabricUnlinked(x *Fabric, a0 string) []LinkEnd { return x.unlinked(a0) }

// ExportFabricUnstatedBacked exposes internal state to external package tests.
func ExportFabricUnstatedBacked(x *Fabric) *map[Endpoint]trace.EvidenceRef { return &x.unstatedBacked }

// ExportFabricWakeItems exposes internal state to external package tests.
func ExportFabricWakeItems(x *Fabric) *map[string]int { return &x.wakeItems }

// ExportFabricWakes exposes internal state to external package tests.
func ExportFabricWakes(x *Fabric) *map[string]time.Time { return &x.wakes }

// ExportIsJourneyHeld exposes internal state to external package tests.
var ExportIsJourneyHeld = isJourneyHeld

// ExportJourneyFields exposes internal state to external package tests.
type ExportJourneyFields struct {
	FrameID    FrameID
	Protocol   bool
	Origin     JourneyOrigin
	Injection  Injection
	Entries    []Entry
	Deliveries []Delivery
	Metadata   analysis.Metadata
	State      JourneyState
	Settled    bool
}

// ExportJourneyOriginValidate exposes internal state to external package tests.
func ExportJourneyOriginValidate(x JourneyOrigin) error { return x.validate() }

// ExportLinkTrust exposes internal state to external package tests.
type ExportLinkTrust = linkTrust

// ExportLinkTrustFields exposes internal state to external package tests.
type ExportLinkTrustFields struct {
	Issues     []analysis.Issue
	Assumption *analysis.Assumption
}

// ExportQueued exposes internal state to external package tests.
type ExportQueued = queued

// ExportQueuedFields exposes internal state to external package tests.
type ExportQueuedFields struct {
	Frame      ethernet.Frame
	Octets     uint64
	Seq        uint64
	Fid        FrameID
	Journey    *journey
	Pcp        vlan.PCP
	EgressPort string
	Enqueued   time.Time
	Mirror     string
}

// ExportReasonTruncatedRecord exposes internal state to external package tests.
const ExportReasonTruncatedRecord = reasonTruncatedRecord

// ExportReplaySpecFields exposes internal state to external package tests.
type ExportReplaySpecFields struct {
	Checkpoint *Fabric
	Scenario   Scenario
}

// ExportAttachedSourceAttachment exposes internal state to external package tests.
func ExportAttachedSourceAttachment(x *attachedSource) *StreamAttachment { return &x.attachment }

// ExportAttachedSourceFrame exposes internal state to external package tests.
func ExportAttachedSourceFrame(x *attachedSource) *ethernet.Frame { return &x.frame }

// ExportEgressQueueDepth exposes internal state to external package tests.
func ExportEgressQueueDepth(x *egressQueue) *[8]uint64 { return &x.depth }

// ExportEgressQueuePeak exposes internal state to external package tests.
func ExportEgressQueuePeak(x *egressQueue) *[8]uint64 { return &x.peak }

// ExportEgressQueuePending exposes internal state to external package tests.
func ExportEgressQueuePending(x *egressQueue) *[8][]queued { return &x.pending }

// ExportLinkEndRefEnd exposes internal state to external package tests.
func ExportLinkEndRefEnd(x *linkEndRef) **LinkEnd { return &x.end }

// ExportLinkEndRefLink exposes internal state to external package tests.
func ExportLinkEndRefLink(x *linkEndRef) **Link { return &x.link }

// ExportLinkTrustIssues exposes internal state to external package tests.
func ExportLinkTrustIssues(x *linkTrust) *[]analysis.Issue { return &x.issues }

// ExportQueuedFid exposes internal state to external package tests.
func ExportQueuedFid(x *queued) *FrameID { return &x.fid }

// ExportQueuedSeq exposes internal state to external package tests.
func ExportQueuedSeq(x *queued) *uint64 { return &x.seq }

// ExportTopologyStart exposes internal state to external package tests.
func ExportTopologyStart(x *topology) *time.Time { return &x.start }

// MakeTestArrival exposes internal state to external package tests.
func MakeTestArrival(x ExportArrivalFields) Arrival {
	return Arrival{At: x.At, Kind: x.Kind, Seq: x.Seq, Device: x.Device, Port: x.Port, FrameID: x.FrameID, Frame: x.Frame, Corrupt: x.Corrupt, index: x.Index}
}

// MakeTestAttachedSource exposes internal state to external package tests.
func MakeTestAttachedSource(x ExportAttachedSourceFields) attachedSource {
	return attachedSource{attachment: x.Attachment, at: x.At, frame: x.Frame, peeked: x.Peeked, ended: x.Ended}
}

// MakeTestEgressQueue exposes internal state to external package tests.
func MakeTestEgressQueue(x ExportEgressQueueFields) egressQueue {
	return egressQueue{pending: x.Pending, dequeueAt: x.DequeueAt, dequeuePending: x.DequeuePending, rateClock: x.RateClock, depth: x.Depth, peak: x.Peak}
}

// MakeTestEgressQueuePointer exposes internal state to external package tests.
func MakeTestEgressQueuePointer(x ExportEgressQueueFields) *egressQueue {
	v := MakeTestEgressQueue(x)
	return &v
}

// MakeTestJourney exposes internal state to external package tests.
func MakeTestJourney(x ExportJourneyFields) Journey {
	return Journey{FrameID: x.FrameID, Protocol: x.Protocol, Origin: x.Origin, Injection: x.Injection, Entries: x.Entries, Deliveries: x.Deliveries, Metadata: x.Metadata, State: x.State}
}

// MakeTestJourneyPointer exposes internal state to external package tests.
func MakeTestJourneyPointer(x ExportJourneyFields) *journey {
	return &journey{
		FrameID: x.FrameID, Protocol: x.Protocol, Origin: x.Origin,
		Injection: x.Injection, Entries: x.Entries, Deliveries: x.Deliveries, Metadata: x.Metadata, settled: x.Settled,
	}
}

// ExportJourney exposes the execution record to package tests.
type ExportJourney = journey

// MakeTestLinkTrust exposes internal state to external package tests.
func MakeTestLinkTrust(x ExportLinkTrustFields) linkTrust {
	return linkTrust{issues: x.Issues, assumption: x.Assumption}
}

// MakeTestQueued exposes internal state to external package tests.
func MakeTestQueued(x ExportQueuedFields) queued {
	return queued{frame: x.Frame, octets: x.Octets, seq: x.Seq, fid: x.Fid, journey: x.Journey, pcp: x.Pcp, egressPort: x.EgressPort, enqueued: x.Enqueued, mirror: x.Mirror}
}

// MakeTestReplaySpec exposes internal state to external package tests.
func MakeTestReplaySpec(x ExportReplaySpecFields) ReplaySpec {
	return ReplaySpec{checkpoint: x.Checkpoint, Scenario: x.Scenario}
}

// validate verifies that Of and Mirror fields are consistent with Kind.
func (o JourneyOrigin) validate() error {
	switch o.Kind {
	case OriginInjection:
		if o.Of != 0 {
			return errs.New().Attr("kind", o.Kind).Attr("of", o.Of).Msg("injection origin must not specify parent frame ID")
		}
		if o.Mirror != "" {
			return errs.New().Attr("kind", o.Kind).Attr("mirror", o.Mirror).Msg("injection origin must not specify mirror name")
		}
	case OriginMirror:
		if o.Of == 0 {
			return errs.New().Attr("kind", o.Kind).Msg("mirror origin must specify parent frame ID")
		}
	case OriginReflection:
		if o.Of == 0 {
			return errs.New().Attr("kind", o.Kind).Msg("reflection origin must specify parent frame ID")
		}
		if o.Mirror != "" {
			return errs.New().Attr("kind", o.Kind).Attr("mirror", o.Mirror).Msg("reflection origin must not specify mirror name")
		}
	default:
		return errs.New().Attr("kind", o.Kind).Msg("unknown journey origin kind")
	}

	return nil
}
