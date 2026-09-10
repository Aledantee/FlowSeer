// Package trace provides a common vocabulary for frame processing steps, outcomes, and configuration diffs.
package trace

// Layer identifies an architectural or protocol layer emitting a trace step or owning a diff subject.
// The zero value is an empty layer identifier. Specific layer constants are declared by the packages
// that implement those layers.
// Instances are immutable value types safe for concurrent use.
type Layer string

// Op represents the operation performed at a trace step.
// The zero value is an empty operation.
// Instances are immutable value types safe for concurrent use.
type Op string

// Operation is an alias for [Op].
type Operation = Op

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
// Instances are value types safe for concurrent use.
type Step struct {
	Layer  Layer
	Op     Op
	Detail string
}

// Operation returns s.Op.
func (s Step) Operation() Op {
	return s.Op
}

// Outcome represents the final disposition of a frame in a network simulation trace.
// The zero value is an empty outcome.
// Instances are immutable value types safe for concurrent use.
type Outcome string

const (
	// OutcomeForwarded indicates the frame was forwarded to a specific destination.
	OutcomeForwarded Outcome = "Forwarded"

	// OutcomeFlooded indicates the frame was replicated across all eligible ports.
	OutcomeFlooded Outcome = "Flooded"

	// OutcomeDropped indicates the frame was discarded without transmission.
	OutcomeDropped Outcome = "Dropped"

	// Forwarded is an alias for [OutcomeForwarded].
	Forwarded = OutcomeForwarded

	// Flooded is an alias for [OutcomeFlooded].
	Flooded = OutcomeFlooded

	// Dropped is an alias for [OutcomeDropped].
	Dropped = OutcomeDropped
)

// Reason explains why a frame met its outcome, such as why it was dropped or flooded.
// The zero value is an empty reason, typical when a frame is forwarded normally.
// Reason constants are declared by the packages that emit them.
// Instances are immutable value types safe for concurrent use.
type Reason string

// Trace records the sequence of processing steps, final outcome, and outcome reason for a frame.
// The zero value is an empty trace.
// Instances are not safe for concurrent modification.
type Trace struct {
	Steps   []Step
	Outcome Outcome
	Reason  Reason
}

// Subject identifies an entity affected by a configuration change.
// The zero value is an empty subject.
// Instances are value types safe for concurrent use.
type Subject struct {
	Kind string
	Key  string
}

// Change records a transition in one configuration field of a subject between two configurations.
// The zero value is an empty change.
// Instances are not safe for concurrent modification if From or To hold mutable values.
type Change struct {
	Layer   Layer
	Subject Subject
	Field   string
	From    any
	To      any
}
