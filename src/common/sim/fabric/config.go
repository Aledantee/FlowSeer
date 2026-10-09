package fabric

import (
	"cmp"
	"slices"
	"strings"

	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	cablemodel "go.aledante.io/FlowSeer/src/common/sim/medium/cable"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Layer is the architectural trace layer identifier for the network fabric.
const Layer trace.Layer = "fabric"

const (

	// ReasonNoCable records that a port is Down because [Config.Uncabled] states no cable reaches it;
	// unlinked ports carry it, since a port without a cable has no Link.
	ReasonNoCable trace.Reason = "no-cable"

	// ReasonAdjacencyUnresolved records that a switch port is Unknown because no cable names it and
	// [Config.Uncabled] does not list it, so nothing says what, if anything, is attached.
	ReasonAdjacencyUnresolved trace.Reason = "adjacency-unresolved"
)

const (
	// IssueOperStatusConflict marks a switch port whose configured operational status, Up or Down,
	// differs from the status its cable derives. The derived status is the one that executes.
	IssueOperStatusConflict analysis.IssueCode = "oper-status-conflict"

	// IssueQueueBufferUnstated marks an endpoint whose egress queue has backed
	// up past one maximum-size frame without a stated buffer, so its loss
	// figure is unknown.
	IssueQueueBufferUnstated analysis.IssueCode = "queue-buffer-unstated"
)

// Endpoint names a specific attachment point in the fabric, referencing a virtual switch port
// or a single-port host when Port is empty.
type Endpoint struct {
	Node string
	Port string
}

// TypeID returns the fact type identifier for Endpoint.
func (e Endpoint) TypeID() string {
	return "fabric.endpoint"
}

// Canonical returns the canonical representation of the endpoint.
func (e Endpoint) Canonical() string {
	return encodeEndpointPart(e.Node) + ":" + encodeEndpointPart(e.Port)
}

func encodeEndpointPart(value string) string {
	return strings.NewReplacer("%", "%25", ":", "%3A", "-", "%2D").Replace(value)
}

// Cable models a physical link connecting two endpoints: a length and a medium that give the propagation time and
// bound the negotiated speed, an optional Delay that replaces the propagation term, an optional top speed limit,
// and declared faults. A LengthMeters of 0 is a stated 0 m cable. Evidence references entries of
// the construction evidence catalog that support the cable; issues resting on the cable cite them. Evidence is not
// behavior, so configuration diffs does not report it.
type Cable struct {
	A Endpoint
	B Endpoint
	cablemodel.Cable
	Evidence []trace.EvidenceRef
}

// Clone copies physical facts and evidence references independently.
func (c Cable) Clone() Cable {
	cp := c
	cp.Cable = c.Cable.Clone()
	cp.Evidence = slices.Clone(c.Evidence)
	return cp
}

// Equal compares endpoints, physical facts, and ordered evidence references.
func (c Cable) Equal(other Cable) bool {
	return c.A == other.A && c.B == other.B && c.Cable.Equal(other.Cable) && slices.Equal(c.Evidence, other.Evidence)
}

// Uncabled states that a switch port has no cable, so the port is Down with reason no-cable. A non-LAG
// switch port that no cable names and no Uncabled entry lists is unresolved instead. Evidence references
// entries of the construction evidence catalog and, like a cable's, stays out of configuration diffs.
type Uncabled struct {
	Endpoint Endpoint
	Evidence []trace.EvidenceRef
}

// Clone returns an independent deep copy of the entry.
func (u Uncabled) Clone() Uncabled {
	return Uncabled{Endpoint: u.Endpoint, Evidence: slices.Clone(u.Evidence)}
}

// Equal reports whether two entries name the same port with the same evidence references in the same order.
func (u Uncabled) Equal(other Uncabled) bool {
	return u.Endpoint == other.Endpoint && slices.Equal(u.Evidence, other.Evidence)
}

func cloneUncabled(entries []Uncabled) []Uncabled {
	if entries == nil {
		return nil
	}
	cp := make([]Uncabled, len(entries))
	for i, entry := range entries {
		cp[i] = entry.Clone()
	}

	return cp
}

func canonicalCableOrientation(c Cable) Cable {
	if compareEndpoint(c.A, c.B) <= 0 {
		return c
	}

	c.A, c.B = c.B, c.A
	switch c.Fault.Kind {
	case cablemodel.FaultDeadAToB:
		c.Fault.Kind = cablemodel.FaultDeadBToA
	case cablemodel.FaultDeadBToA:
		c.Fault.Kind = cablemodel.FaultDeadAToB
	}

	return c
}

func compareEndpoint(a, b Endpoint) int {
	if order := cmp.Compare(a.Node, b.Node); order != 0 {
		return order
	}

	return cmp.Compare(a.Port, b.Port)
}

func cableEndpointsFor(c Cable) cableEndpoints {
	c = canonicalCableOrientation(c)
	return cableEndpoints{A: c.A, B: c.B}
}

func normalizedFault(f cablemodel.Fault) cablemodel.Fault {
	f = f.Clone()
	if f.Kind == "" {
		f.Kind = cablemodel.FaultNone
	}
	switch f.Kind {
	case cablemodel.FaultLoseEveryNth, cablemodel.FaultCorruptEveryNth:
		f.Sequence = nil
	case cablemodel.FaultLoseSequence:
		f.N = 0
		slices.Sort(f.Sequence)
		f.Sequence = slices.Compact(f.Sequence)
	default:
		f.N = 0
		f.Sequence = nil
	}

	return f
}

func sameFault(a, b cablemodel.Fault) bool {
	return a.Kind == b.Kind && a.N == b.N && slices.Equal(a.Sequence, b.Sequence)
}

type cableEndpoints struct {
	A Endpoint
	B Endpoint
}

func (e cableEndpoints) Canonical() string {
	return e.A.Canonical() + "-" + e.B.Canonical()
}
