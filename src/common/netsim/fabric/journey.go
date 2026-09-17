package fabric

import (
	"cmp"
	"slices"
	"time"

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

	// EntryRejection records a host refusing an arrived frame; its Reason names the refusing check.
	EntryRejection EntryKind = "Rejection"

	// EntryDrop records whole-frame discard by a switch, due to corrupted arrival, or at a host whose link is Down.
	EntryDrop EntryKind = "Drop"

	// EntryUnresolved records a frame whose fate cannot be decided: the link it needs is Unknown, and its Reason
	// is the link's, or a host cannot read the IP header its acceptance rests on.
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
// set on a host's acceptance decision, a Delivery, Rejection, or Unresolved entry after an Arrival.
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

// Journey records the complete traversal history and deliveries of an injected frame across the fabric.
// Deliveries holds only frames a host accepted.
//
// Metadata is evaluated over the whole analysis and holds only what the journey depended on, captured as each
// entry was recorded. It keeps the issues and assumptions of every hop result, and the [Fabric.Metadata] issues
// and assumptions whose scope overlaps an entry's endpoint or cable, a hop's consulted ports or scopes, or the
// link of any such port. An acceptance a host could not decide adds an issue on the journey's scope. A later
// [Fabric.SetFault] does not change a journey already recorded.
type Journey struct {
	FrameID    FrameID
	Protocol   bool
	Mirror     string
	Parent     FrameID
	Injection  Injection
	Entries    []Entry
	Deliveries []Delivery
	Metadata   analysis.Metadata
}

// Report returns independent copies of all recorded journeys sorted in ascending order of frame ID.
func (f *Fabric) Report() []Journey {
	if len(f.journeys) == 0 {
		return nil
	}
	out := make([]Journey, 0, len(f.journeys))
	for _, j := range f.journeys {
		out = append(out, j.clone())
	}
	slices.SortFunc(out, func(a, b Journey) int {
		return cmp.Compare(a.FrameID, b.FrameID)
	})

	return out
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
// with each port's link.
func (f *Fabric) dependencies(e Entry) []analysis.Scope {
	var scopes []analysis.Scope
	endpoint := func(ep Endpoint) {
		scopes = append(scopes, endpointScope(ep))
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
