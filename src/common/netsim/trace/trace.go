// Package trace provides a common vocabulary for frame processing steps, outcomes, and configuration diffs.
package trace

// Layer identifies an architectural or protocol layer emitting a trace step or owning a diff subject.
// The zero value is an empty layer identifier. Specific layer constants are declared by the packages
// that implement those layers.
type Layer string

// Op represents the operation performed at a trace step.
// The zero value is an empty operation.
type Op string

const (
	// OpClassify classifies ingress traffic or identifies a filtering database.
	OpClassify Op = "classify"

	// OpFilter applies admission or filtering policies.
	OpFilter Op = "filter"

	// OpLearn learns a source address into the forwarding table.
	OpLearn Op = "learn"

	// OpLookup queries the forwarding database for destination addresses.
	OpLookup Op = "lookup"

	// OpReplicate replicates a frame across multiple destination ports.
	OpReplicate Op = "replicate"

	// OpRewrite modifies frame headers on ingress or egress.
	OpRewrite Op = "rewrite"

	// OpTransmit delivers a frame to an egress port or medium.
	OpTransmit Op = "transmit"

	// OpDrop discards a frame due to policy, unknown state, or resource exhaustion.
	OpDrop Op = "drop"
)

// Step records one operation performed by a layer while processing a frame.
// The zero value is an empty step.
type Step struct {
	Layer  Layer
	Op     Op
	Detail string
}

// Outcome represents the final disposition of a frame in a network simulation trace.
// The zero value is an empty outcome.
type Outcome string

const (
	// Forwarded is a frame sent out one port.
	Forwarded Outcome = "Forwarded"

	// Flooded is a frame replicated to every eligible port.
	Flooded Outcome = "Flooded"

	// Dropped is a frame discarded without transmission.
	Dropped Outcome = "Dropped"
)

// Reason explains why a frame met its outcome, such as why it was dropped or flooded.
// The zero value is an empty reason, typical when a frame is forwarded normally.
// Reason constants are declared by the packages that emit them.
type Reason string

// Trace records the sequence of processing steps, final outcome, and outcome reason for a frame.
// The zero value is an empty trace.
type Trace struct {
	Steps   []Step
	Outcome Outcome
	Reason  Reason
}

// Subject identifies an entity affected by a configuration change.
// The zero value is an empty subject.
type Subject struct {
	Kind string
	Key  string
}

// Change records a transition in one configuration field of a subject between two configurations.
// The zero value is an empty change.
type Change struct {
	Layer   Layer
	Subject Subject
	Field   string
	From    any
	To      any
}
