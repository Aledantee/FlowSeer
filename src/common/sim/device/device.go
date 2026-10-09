// Package device defines the layer-free contract a fabric uses to drive devices.
package device

import (
	"slices"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/analysis"
	"go.aledante.io/FlowSeer/src/common/sim/port"
	"go.aledante.io/FlowSeer/src/common/sim/trace"
)

// Device processes frames and timers under a caller-owned clock. Calls require
// serial access. Fork returns an independent device at the same runtime state.
type Device interface {
	Receive(time.Time, string, ethernet.Frame) Output
	ReceiveMirrored(time.Time, string, ethernet.Frame) Output
	Advance(time.Time) Output
	NextWake() (time.Time, bool)
	LinkChange(time.Time, string, port.LinkState, PointToPoint, uint64) (Output, error)
	Fork() Device
	Ports() port.Table
}

// Output holds the result and all frames and held-frame drops produced by a call.
// Timer and link calls may leave Result zero. Output is safe for concurrent reads.
type Output struct {
	Result
	Hold      HoldToken
	Emissions []Emission
	Copies    []Copy
	Drops     []Drop
}

// Result records forwarding and the facts its trust metadata depends on.
// A receive has one of the five trace outcomes, including when Egress is empty.
// Result is safe for concurrent reads, but not concurrent mutation.
type Result struct {
	trace.Trace
	Ingress           string
	FID               vlan.ID
	Egress            []Egress
	Metadata          analysis.Metadata
	PortDependencies  []port.Port
	ScopeDependencies []analysis.Scope
}

// ConsultedPorts returns independent snapshots in first-consulted order.
func (r Result) ConsultedPorts() []port.Port { return slices.Clone(r.PortDependencies) }

// ConsultedScopes returns independent scopes in canonical order.
func (r Result) ConsultedScopes() []analysis.Scope { return slices.Clone(r.ScopeDependencies) }

// Egress records a transmission or per-port drop. PCP survives untagged egress.
// Egress is safe for concurrent reads.
type Egress struct {
	Port    string
	Member  string
	Frame   ethernet.Frame
	PCP     vlan.PCP
	Dropped trace.Reason
}

// HoldToken identifies a queued frame within one device. Zero means no hold.
// HoldToken is safe for concurrent use.
type HoldToken uint64

// Emission is a frame sent by a device. Hold and Ingress identify a released
// frame. Protocol frames carry a zero hold token. PCP is the queue priority,
// including on an untagged frame. Emission is safe for concurrent reads.
type Emission struct {
	Port     string
	Member   string
	Frame    ethernet.Frame
	Hold     HoldToken
	Ingress  string
	PCP      vlan.PCP
	Protocol bool
}

// Drop reports a held frame that reached no wire. Reason and Step name the
// refusing stage. Steps holds its additional trace. Port is empty when no single
// egress port owns the drop. Drop is safe for concurrent reads.
type Drop struct {
	Step    trace.Step
	Steps   []trace.Step
	Port    string
	Reason  trace.Reason
	Hold    HoldToken
	Ingress string
}

// Copy records a mirrored frame. Mirror names its configuration, VLAN its
// logical output (zero for a direct port), and Member its selected LAG member
// (empty for a non-LAG output). Copy is safe for concurrent reads.
type Copy struct {
	Mirror string
	Port   string
	Member string
	VLAN   vlan.ID
	Frame  ethernet.Frame
}

// PointToPoint reports the operational duplex status of a link.
// PointToPoint is safe for concurrent use. An empty value means unknown.
type PointToPoint string

const (
	// PointToPointUnknown reports an unobserved link duplex.
	PointToPointUnknown PointToPoint = "Unknown"
	// PointToPointTrue reports a point-to-point link.
	PointToPointTrue PointToPoint = "True"
	// PointToPointFalse reports shared media.
	PointToPointFalse PointToPoint = "False"
)
