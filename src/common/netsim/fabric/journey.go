package fabric

import (
	"cmp"
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/trace"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/bridge"
)

// FrameID uniquely identifies an injected frame and its copies throughout the fabric simulation.
type FrameID uint64

// Delivery records the arrival of a transmitted frame at a destination host.
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

	// EntryDelivery records successful frame arrival at a destination host.
	EntryDelivery EntryKind = "Delivery"

	// EntryDrop records whole-frame discard by a switch, due to corrupted arrival, or at a host whose link is Down.
	EntryDrop EntryKind = "Drop"

	// EntryUnresolved records a frame whose fate cannot be decided because the link it needs is Unknown; its
	// Reason is the link's.
	EntryUnresolved EntryKind = "Unresolved"

	// EntryLoop records frame re-entry at a device port already visited by the same frame.
	EntryLoop EntryKind = "Loop"

	// EntryWake records a scheduled timer wake-up advancing a virtual switch.
	EntryWake EntryKind = "Wake"

	// EntryDequeue records an endpoint selecting its next egress frame.
	EntryDequeue EntryKind = "Dequeue"
)

// Entry records a single discrete event or hop in a frame's traversal of the network fabric.
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
	Reason        trace.Reason
}

// Journey records the complete traversal history and deliveries of an injected frame across the fabric.
type Journey struct {
	FrameID    FrameID
	Protocol   bool
	Mirror     string
	Parent     FrameID
	Injection  Injection
	Entries    []Entry
	Deliveries []Delivery
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

	return cp
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
