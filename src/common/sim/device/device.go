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
// State samples independent state without advancing timers.
// AdmitEgress reads queue policy without changing occupancy. Its depth and frame
// size are encoded octets, excluding preamble and gap.
type Device interface {
	AdmitEgress(string, vlan.PCP, uint64, uint64) QueueDecision
	Receive(time.Time, string, ethernet.Frame) Output
	ReceiveMirrored(time.Time, string, ethernet.Frame) Output
	Advance(time.Time) Output
	NextWake() (time.Time, bool)
	LinkChange(time.Time, string, Link) (Output, error)
	Fork() Device
	Ports() port.Table
	State(time.Time) State
}

// Output holds the result and all frames and held-frame drops produced by a call.
// Timer and link calls may leave Result zero. Output is safe for concurrent reads.
type Output struct {
	Result
	// Policed reports an ingress policer refusal.
	Policed   bool
	Hold      HoldToken
	Emissions []Emission
	Copies    []Copy
	Drops     []Drop
}

// QueueDecision admits or refuses an encoded frame at the supplied queue depth.
// Step is a tail-drop or unstated-buffer threshold step, or nil. MaxRateBPS is
// the queue's maximum bit rate, with zero meaning unlimited. QueueDecision is
// safe for concurrent reads.
type QueueDecision struct {
	Admitted   bool
	Reason     trace.Reason
	Step       *trace.Step
	MaxRateBPS uint64
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
	// Hold identifies the released frame that produced this copy. Zero means
	// the copy belongs to the current receive.
	Hold HoldToken
}

// Link reports this end's operational state, speed and duplex. PeerRelays is
// true for a peer that terminates or relays frames rather than repeating them.
// Link is safe for concurrent use.
type Link struct {
	State      port.LinkState
	SpeedBPS   uint64
	Duplex     Duplex
	PeerRelays bool
}

// Duplex is this end's observed duplex. Empty means unknown.
// Duplex is safe for concurrent use.
type Duplex string

const (
	// DuplexUnknown reports an unobserved link duplex.
	DuplexUnknown Duplex = "Unknown"
	// DuplexFull reports simultaneous transmission in both directions.
	DuplexFull Duplex = "Full"
	// DuplexHalf reports transmission in one direction at a time.
	DuplexHalf Duplex = "Half"
)
