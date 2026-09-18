package fabric

import (
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/analysis"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
)

// FrameID uniquely identifies an injected frame and its copies throughout the fabric simulation.
type FrameID uint64

// Delivery records a frame a destination host accepted, as it arrived.
type Delivery struct {
	Host  string
	At    time.Time
	Frame ethernet.Frame
}

// EntryKind identifies the event or transition recorded by a journey entry.
type EntryKind string

const (
	// EntryInjection records the initial introduction of a frame into the fabric.
	EntryInjection EntryKind = "Injection"

	// EntryHop records frame processing by a virtual switch forwarding engine.
	EntryHop EntryKind = "Hop"

	// EntryCrossing records frame transmission across a physical cable toward a destination port.
	EntryCrossing EntryKind = "Crossing"

	// EntryLoss records frame discard caused by a cable loss fault during transmission.
	EntryLoss EntryKind = "Loss"

	// EntryArrival records a frame reaching a host, before the host decides whether to accept it.
	EntryArrival EntryKind = "Arrival"

	// EntryDelivery records a host accepting an arrived frame.
	EntryDelivery EntryKind = "Delivery"

	// EntryReflection records a reflector accepting an arrived frame for
	// reflection. A reflector is not a host and delivers nothing, so this is
	// its own kind rather than EntryDelivery: originating copies is not a
	// host taking delivery of a frame.
	EntryReflection EntryKind = "Reflection"

	// EntryRejection records a host or a reflector refusing an arrived frame; its Reason names the refusing check.
	EntryRejection EntryKind = "Rejection"

	// EntryDrop records whole-frame discard by a switch, due to corrupted arrival, or at a host whose link is Down.
	EntryDrop EntryKind = "Drop"

	// EntryUnresolved records a frame whose fate cannot be decided: the link it needs is Unknown, or a host or a
	// reflector cannot read the IP or UDP header its acceptance rests on. Its Reason names the link or the header.
	EntryUnresolved EntryKind = "Unresolved"

	// EntryLoop records frame re-entry at a device port already visited by the same frame.
	EntryLoop EntryKind = "Loop"

	// EntryWake records a scheduled timer wake-up advancing a virtual switch.
	EntryWake EntryKind = "Wake"

	// EntryDequeue records an endpoint selecting its next egress frame.
	EntryDequeue EntryKind = "Dequeue"
)

// Entry records a single discrete event or hop in a frame's traversal of the network fabric.
//
// Device and Port name the endpoint the entry happened at; a host's Port is empty. Cable is the cable the
// entry rests on: the one crossed, lost on, or arrived over, and for an injection the origin's cable. Step is
// set on a host's Delivery, Rejection, or Unresolved entry after an Arrival, on a reflector's Reflection,
// Rejection, or Unresolved entry, which carries no preceding Arrival, and on the Drop entry a
// [vswitch.NeighborDrop] produces for a held frame that left its hold queue and reached no wire, where
// Port and Reason likewise come from the drop rather than from reading the step back.
type Entry struct {
	At            time.Time
	Kind          EntryKind
	Device        string
	Port          string
	Cable         *Cable
	Latency       time.Duration
	Serialization time.Duration
	Wait          time.Duration
	PCP           vlan.PCP
	Result        *vswitch.ForwardResult
	Step          *trace.Step
	Reason        trace.Reason
}

// JourneyState identifies the terminal or pending outcome of a frame's traversal across the network fabric.
type JourneyState string

const (
	// JourneyPending indicates the frame has work remaining queued, in an egress buffer, or held for neighbor resolution.
	JourneyPending JourneyState = "Pending"

	// JourneyDelivered indicates at least one host or reflector accepted the frame.
	JourneyDelivered JourneyState = "Delivered"

	// JourneyRejected indicates an arrived frame was refused by destination host or reflector policy.
	JourneyRejected JourneyState = "Rejected"

	// JourneyDropped indicates the frame was discarded by a switch forwarding engine, cable loss fault, or down interface.
	JourneyDropped JourneyState = "Dropped"

	// JourneyLooped indicates the frame re-entered a device port its traversal history already visited.
	JourneyLooped JourneyState = "Looped"

	// JourneyTruncated indicates the frame's wire payload was truncated upon capture prior to injection.
	JourneyTruncated JourneyState = "Truncated"

	// JourneyUnresolved indicates the frame reached an unknown link state or undecodable header at host or reflector.
	JourneyUnresolved JourneyState = "Unresolved"

	// JourneyReleased indicates a frame held in a neighbor hold queue was released after address resolution.
	JourneyReleased JourneyState = "Released"
)

// JourneyOriginKind identifies how a frame journey entered the simulation.
type JourneyOriginKind string

const (
	// OriginInjection indicates a frame introduced directly by a caller or switch protocol emission.
	OriginInjection JourneyOriginKind = "Injection"

	// OriginMirror indicates a frame produced as a port mirror or reflector copy of another frame.
	OriginMirror JourneyOriginKind = "Mirror"

	// OriginRelease indicates a frame released from a neighbor hold queue after address resolution.
	OriginRelease JourneyOriginKind = "Release"
)

// JourneyOrigin records how a journey began and its relationship to ancestor frames.
type JourneyOrigin struct {
	Kind   JourneyOriginKind
	Of     FrameID
	Mirror string
}

// Validate verifies that Of and Mirror fields are consistent with Kind.
func (o JourneyOrigin) Validate() error {
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
	case OriginRelease:
		if o.Of == 0 {
			return errs.New().Attr("kind", o.Kind).Msg("release origin must specify holding frame ID")
		}
		if o.Mirror != "" {
			return errs.New().Attr("kind", o.Kind).Attr("mirror", o.Mirror).Msg("release origin must not specify mirror name")
		}
	default:
		return errs.New().Attr("kind", o.Kind).Msg("unknown journey origin kind")
	}

	return nil
}

const (
	// IssueTruncatedRecord indicates an injected frame was truncated during capture.
	IssueTruncatedRecord analysis.IssueCode = "truncated-record"

	// ReasonTruncatedRecord indicates an injected frame was truncated during capture.
	ReasonTruncatedRecord trace.Reason = "truncated-record"
)

// journeyStatePrecedence declares the evaluation order for journey states, highest first.
var journeyStatePrecedence = []JourneyState{
	JourneyPending,
	JourneyUnresolved,
	JourneyLooped,
	JourneyTruncated,
	JourneyReleased,
	JourneyDelivered,
	JourneyRejected,
	JourneyDropped,
}

// Journey records the complete traversal history and deliveries of an injected frame across the fabric.
// Deliveries holds only frames a host accepted.
//
// Metadata is evaluated over the whole analysis and holds only what the journey depended on, captured as each
// entry was recorded. It keeps the issues and assumptions of every hop result, and the [Fabric.Metadata] issues
// and assumptions whose scope overlaps an entry's endpoint or cable, a hop's consulted ports or scopes, or the
// link of any such port. A switch entry naming no port contributes no endpoint scope at all: the only scope
// available to it is the whole switch's node scope, which would pull in every other port's issues along with
// it, so it depends on none. An acceptance a host could not decide adds an issue on the journey's scope. A
// later [Fabric.SetFault] does not change a journey already recorded.
type Journey struct {
	FrameID    FrameID
	Protocol   bool
	Origin     JourneyOrigin
	Injection  Injection
	Entries    []Entry
	Deliveries []Delivery
	Metadata   analysis.Metadata
	State      JourneyState
}

// Report returns independent copies of all recorded journeys sorted in ascending order of frame ID.
func (f *Fabric) Report() []Journey {
	if len(f.journeys) == 0 {
		return nil
	}

	pendingFrames := make(map[FrameID]bool)
	for _, arr := range f.queue {
		if arr.Kind == ArrivalFrame {
			pendingFrames[arr.FrameID] = true
		}
	}
	for _, q := range f.egress {
		for pcp := 0; pcp < 8; pcp++ {
			for _, item := range q.pending[pcp] {
				pendingFrames[item.fid] = true
			}
		}
	}

	releasedFrames := make(map[FrameID]bool)
	for _, j := range f.journeys {
		if j.Origin.Kind == OriginRelease && j.Origin.Of != 0 {
			releasedFrames[j.Origin.Of] = true
		}
	}

	for fid, j := range f.journeys {
		if isJourneyHeld(j) && !releasedFrames[fid] {
			pendingFrames[fid] = true
		}
	}

	out := make([]Journey, 0, len(f.journeys))
	for _, j := range f.journeys {
		cp := j.clone()
		cp.State = assignJourneyState(&cp, pendingFrames[j.FrameID], releasedFrames[j.FrameID])
		out = append(out, cp)
	}
	slices.SortFunc(out, func(a, b Journey) int {
		return cmp.Compare(a.FrameID, b.FrameID)
	})

	return out
}

func assignJourneyState(j *Journey, pending, released bool) JourneyState {
	for _, state := range journeyStatePrecedence {
		switch state {
		case JourneyPending:
			if pending {
				return JourneyPending
			}
		case JourneyUnresolved:
			if hasEntryKind(j, EntryUnresolved) {
				return JourneyUnresolved
			}
		case JourneyLooped:
			if hasEntryKind(j, EntryLoop) {
				return JourneyLooped
			}
		case JourneyTruncated:
			if isJourneyTruncated(j) {
				return JourneyTruncated
			}
		case JourneyReleased:
			if released {
				return JourneyReleased
			}
		case JourneyDelivered:
			if len(j.Deliveries) > 0 || hasEntryKind(j, EntryDelivery) || hasEntryKind(j, EntryReflection) || isJourneyConsumed(j) {
				return JourneyDelivered
			}
		case JourneyRejected:
			if hasEntryKind(j, EntryRejection) {
				return JourneyRejected
			}
		case JourneyDropped:
			if hasEntryKind(j, EntryDrop) || hasEntryKind(j, EntryLoss) {
				return JourneyDropped
			}
		}
	}

	return JourneyPending
}

func isJourneyConsumed(j *Journey) bool {
	if len(j.Entries) == 0 {
		return false
	}
	last := j.Entries[len(j.Entries)-1]
	return last.Kind == EntryHop && last.Result != nil && last.Result.Outcome == trace.Consumed
}

func hasEntryKind(j *Journey, kind EntryKind) bool {
	return slices.ContainsFunc(j.Entries, func(e Entry) bool {
		return e.Kind == kind
	})
}

func isJourneyHeld(j *Journey) bool {
	if len(j.Entries) == 0 {
		return false
	}
	last := j.Entries[len(j.Entries)-1]
	return last.Result != nil && last.Result.Outcome == trace.Held
}

func isJourneyTruncated(j *Journey) bool {
	if j.State == JourneyTruncated {
		return true
	}
	if slices.ContainsFunc(j.Metadata.Issues(), func(iss analysis.Issue) bool {
		return iss.Code == IssueTruncatedRecord
	}) {
		return true
	}
	return slices.ContainsFunc(j.Entries, func(e Entry) bool {
		return e.Reason == ReasonTruncatedRecord
	})
}

func (j Journey) clone() Journey {
	cp := j
	cp.Injection = j.Injection.clone()
	if len(j.Entries) > 0 {
		cp.Entries = make([]Entry, len(j.Entries))
		for i, e := range j.Entries {
			cp.Entries[i] = e.clone()
		}
	}
	if len(j.Deliveries) > 0 {
		cp.Deliveries = make([]Delivery, len(j.Deliveries))
		for i, d := range j.Deliveries {
			cp.Deliveries[i] = Delivery{
				Host:  d.Host,
				At:    d.At,
				Frame: cloneFrame(d.Frame),
			}
		}
	}

	return cp
}

// shallowClone returns a copy of the journey that shares ethernet.Frame values
// across forks while cloning mutable traversal history and deliveries.
func (j *Journey) shallowClone() *Journey {
	if j == nil {
		return nil
	}
	cp := *j
	if j.Injection.Packet != nil {
		packet := *j.Injection.Packet
		packet.Payload = slices.Clone(j.Injection.Packet.Payload)
		cp.Injection.Packet = &packet
	}
	if len(j.Entries) > 0 {
		cp.Entries = make([]Entry, len(j.Entries))
		for i, e := range j.Entries {
			cp.Entries[i] = e.clone()
		}
	}
	if len(j.Deliveries) > 0 {
		cp.Deliveries = slices.Clone(j.Deliveries)
	}

	return &cp
}

func (e Entry) clone() Entry {
	cp := e
	if e.Cable != nil {
		c := e.Cable.Clone()
		cp.Cable = &c
	}
	if e.Result != nil {
		cp.Result = cloneResult(*e.Result)
	}
	if e.Step != nil {
		step := cloneStep(*e.Step)
		cp.Step = &step
	}

	return cp
}

// record appends e to the journey and folds into the journey's metadata the
// issues and assumptions e depends on as the fabric stands now, together with
// issues the caller raised about the journey itself.
func (f *Fabric) record(j *Journey, e Entry, raised ...analysis.Issue) {
	j.Entries = append(j.Entries, e)

	issues := j.Metadata.Issues()
	assumptions := j.Metadata.Assumptions()
	catalog := j.Metadata.Evidence()
	changed := false
	cite := func(refs []trace.EvidenceRef, source analysis.EvidenceCatalog) {
		for _, ref := range refs {
			if evidence, ok := source.Lookup(ref); ok {
				catalog, _ = catalog.Add(evidence)
			}
		}
	}
	keepIssue := func(issue analysis.Issue, source analysis.EvidenceCatalog) {
		if slices.ContainsFunc(issues, func(kept analysis.Issue) bool { return sameIssue(kept, issue) }) {
			return
		}
		issues = append(issues, issue)
		cite(issue.Evidence, source)
		changed = true
	}
	keepAssumption := func(assumption analysis.Assumption, source analysis.EvidenceCatalog) {
		if slices.ContainsFunc(assumptions, func(kept analysis.Assumption) bool { return sameAssumption(kept, assumption) }) {
			return
		}
		assumptions = append(assumptions, assumption)
		cite(assumption.Evidence, source)
		changed = true
	}

	if e.Result != nil {
		for _, issue := range e.Result.Metadata.Issues() {
			keepIssue(issue, e.Result.Metadata.Evidence())
		}
		for _, assumption := range e.Result.Metadata.Assumptions() {
			keepAssumption(assumption, e.Result.Metadata.Evidence())
		}
	}
	if dependencies := f.dependencies(e); len(dependencies) > 0 {
		fabric := f.Metadata()
		for _, issue := range fabric.Issues() {
			if slices.ContainsFunc(dependencies, issue.Scope.Overlaps) {
				keepIssue(issue, fabric.Evidence())
			}
		}
		for _, assumption := range fabric.Assumptions() {
			if slices.ContainsFunc(dependencies, assumption.Scope.Overlaps) {
				keepAssumption(assumption, fabric.Evidence())
			}
		}
	}
	for _, issue := range raised {
		keepIssue(issue, f.evidence)
	}

	if changed {
		j.Metadata = analysis.NewMetadata(analysis.WholeScope(), issues, catalog, assumptions)
	}
}

// dependencies returns the scopes whose fabric issues could change e: its
// endpoint and cable, and for a hop the ports and scopes its result consulted,
// with each port's link. A switch endpoint naming no port contributes no
// endpoint scope: a host legitimately has one unnamed port, so an empty Port
// there names that port's scope, but for a switch the same empty Port has no
// scope narrower than the whole node, and inheriting every one of the
// switch's other ports' issues is worse than inheriting none.
func (f *Fabric) dependencies(e Entry) []analysis.Scope {
	var scopes []analysis.Scope
	endpoint := func(ep Endpoint) {
		if _, isHost := f.cfg.Hosts[ep.Node]; ep.Port != "" || isHost {
			scopes = append(scopes, endpointScope(ep))
		}
		if ref, ok := f.byEnd[ep]; ok {
			scopes = append(scopes, cableScope(ref.link.Cable))
		}
	}

	if e.Device != "" {
		endpoint(Endpoint{Node: e.Device, Port: e.Port})
	}
	if e.Cable != nil {
		scopes = append(scopes, cableScope(*e.Cable))
	}
	if e.Result != nil {
		for _, p := range e.Result.ConsultedPorts() {
			endpoint(Endpoint{Node: e.Device, Port: p.Name})
		}
		scopes = append(scopes, e.Result.ConsultedScopes()...)
	}

	return scopes
}

func sameIssue(a, b analysis.Issue) bool {
	a, b = a.Canonical(), b.Canonical()

	return a.Code == b.Code && a.Status == b.Status && a.Scope.Compare(b.Scope) == 0 && a.Message == b.Message &&
		slices.Equal(a.Evidence, b.Evidence)
}

func sameAssumption(a, b analysis.Assumption) bool {
	a, b = a.Canonical(), b.Canonical()

	return a.Scope.Compare(b.Scope) == 0 && a.Statement == b.Statement && slices.Equal(a.Evidence, b.Evidence)
}

func cloneResult(r vswitch.ForwardResult) *vswitch.ForwardResult {
	cp := r
	if len(r.Steps) > 0 {
		cp.Steps = make([]trace.Step, len(r.Steps))
		for i, step := range r.Steps {
			cp.Steps[i] = cloneStep(step)
		}
	}
	if len(r.Egress) > 0 {
		cp.Egress = make([]bridge.Egress, len(r.Egress))
		for i, egress := range r.Egress {
			cp.Egress[i] = egress
			cp.Egress[i].Frame = cloneFrame(egress.Frame)
		}
	}

	return &cp
}

func (i Injection) clone() Injection {
	cp := i
	cp.Frame = cloneFrame(i.Frame)
	if i.Packet != nil {
		packet := *i.Packet
		packet.Payload = slices.Clone(i.Packet.Payload)
		cp.Packet = &packet
	}

	return cp
}

func cloneStep(step trace.Step) trace.Step {
	cp := step
	cp.Inputs = slices.Clone(step.Inputs)
	cp.Outputs = slices.Clone(step.Outputs)
	cp.Evidence = slices.Clone(step.Evidence)

	return cp
}

func cloneFrame(f ethernet.Frame) ethernet.Frame {
	cp := f
	if len(f.Tags) > 0 {
		cp.Tags = slices.Clone(f.Tags)
	}
	if len(f.Payload) > 0 {
		cp.Payload = slices.Clone(f.Payload)
	}

	return cp
}
