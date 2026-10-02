// Package trace provides a common vocabulary for frame processing steps, outcomes, and configuration diffs.
package trace

import (
	"cmp"
	"slices"
	"strings"
)

// Layer identifies an architectural or protocol layer emitting a trace step or owning a diff subject.
// Specific layer constants are declared by the packages that implement those layers.
// The zero value is an empty layer identifier. Layer is safe for concurrent use.
type Layer string

// Op represents the operation performed at a trace step.
// The zero value is an empty operation. Op is safe for concurrent use.
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

	// OpQueue observes an egress queue as it admits a frame.
	OpQueue Op = "queue"

	// OpDrop discards a frame due to policy, unknown state, or resource exhaustion.
	OpDrop Op = "drop"
)

// RuleID identifies a producer-owned decision, classification, or filtering rule.
// Producers define their own rule IDs without central enumeration; unknown IDs are accepted as-is.
// The zero value represents an unspecified rule. RuleID is safe for concurrent use.
type RuleID string

// String returns the string representation of the rule identifier.
func (r RuleID) String() string {
	return string(r)
}

// Subject identifies an entity affected by an operation or configuration change.
// The zero value is an empty subject. Subject is safe for concurrent use.
type Subject struct {
	Kind string
	Key  string
}

// Compare returns an integer comparing two subjects in canonical order (Kind, then Key).
func (s Subject) Compare(other Subject) int {
	if c := strings.Compare(s.Kind, other.Kind); c != 0 {
		return c
	}
	return strings.Compare(s.Key, other.Key)
}

// String returns the canonical string representation of the subject (for example, "port:1/1/1").
// If both Kind and Key are empty, String returns an empty string.
func (s Subject) String() string {
	if s.Kind == "" && s.Key == "" {
		return ""
	}
	if s.Key == "" {
		return s.Kind
	}
	return s.Kind + ":" + s.Key
}

// EvidenceRef is an opaque identifier referencing supporting evidence in an evidence catalog.
// The zero value represents an empty reference. EvidenceRef is safe for concurrent use.
type EvidenceRef string

// Fact is an open contract for immutable, capability-owned semantic facts.
// Each fact exposes a stable type ID and a canonical representation for equality and ordering.
// Implementations must be immutable value types safe for concurrent use.
type Fact interface {
	// TypeID returns the stable identifier for the fact type.
	TypeID() string

	// Canonical returns the deterministic canonical representation used for equality and ordering.
	Canonical() string
}

// CompareFact returns an integer comparing two facts in canonical order.
// A nil fact orders before any non-nil fact.
// Non-nil facts are ordered first by TypeID, then by Canonical representation.
func CompareFact(a, b Fact) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	if c := strings.Compare(a.TypeID(), b.TypeID()); c != 0 {
		return c
	}
	return strings.Compare(a.Canonical(), b.Canonical())
}

// EqualFact reports whether two facts are semantically equal.
func EqualFact(a, b Fact) bool {
	return CompareFact(a, b) == 0
}

// SortFacts sorts a slice of facts in-place by their canonical ordering.
func SortFacts(facts []Fact) {
	slices.SortFunc(facts, CompareFact)
}

// Step records one semantic operation performed by a layer while processing a frame.
// The zero value is an empty, unspecified step. Step is safe for concurrent read access.
type Step struct {
	Layer    Layer
	Op       Op
	RuleID   RuleID
	Subject  Subject
	Inputs   []Fact
	Outputs  []Fact
	Evidence []EvidenceRef
}

// Canonical returns a copy of the step with inputs, outputs, and evidence in canonical order.
// Evidence references are sorted and deduplicated.
func (s Step) Canonical() Step {
	out := Step{
		Layer:   s.Layer,
		Op:      s.Op,
		RuleID:  s.RuleID,
		Subject: s.Subject,
	}

	if len(s.Inputs) > 0 {
		out.Inputs = make([]Fact, len(s.Inputs))
		copy(out.Inputs, s.Inputs)
		SortFacts(out.Inputs)
	}

	if len(s.Outputs) > 0 {
		out.Outputs = make([]Fact, len(s.Outputs))
		copy(out.Outputs, s.Outputs)
		SortFacts(out.Outputs)
	}

	if len(s.Evidence) > 0 {
		ev := make([]EvidenceRef, len(s.Evidence))
		copy(ev, s.Evidence)
		slices.Sort(ev)
		out.Evidence = slices.Compact(ev)
	}

	return out
}

// Equal reports whether two steps are semantically equal, comparing canonical representations.
func (s Step) Equal(other Step) bool {
	return EqualStep(s, other)
}

// EqualStep reports whether two steps are semantically equal.
func EqualStep(a, b Step) bool {
	return CompareStep(a, b) == 0
}

// CompareStep compares two steps canonically.
func CompareStep(a, b Step) int {
	ca := a.Canonical()
	cb := b.Canonical()

	if c := strings.Compare(string(ca.Layer), string(cb.Layer)); c != 0 {
		return c
	}
	if c := strings.Compare(string(ca.Op), string(cb.Op)); c != 0 {
		return c
	}
	if c := strings.Compare(string(ca.RuleID), string(cb.RuleID)); c != 0 {
		return c
	}
	if c := ca.Subject.Compare(cb.Subject); c != 0 {
		return c
	}

	if c := compareFactSlices(ca.Inputs, cb.Inputs); c != 0 {
		return c
	}
	if c := compareFactSlices(ca.Outputs, cb.Outputs); c != 0 {
		return c
	}
	return slices.Compare(ca.Evidence, cb.Evidence)
}

func compareFactSlices(a, b []Fact) int {
	if len(a) != len(b) {
		return cmp.Compare(len(a), len(b))
	}
	for i := range a {
		if c := CompareFact(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}

// String returns the deterministic human-readable representation of the step.
func (s Step) String() string {
	return RenderStep(s)
}

// Outcome represents the final disposition of a frame in a network simulation trace.
// The zero value is an empty outcome. Outcome is safe for concurrent use.
type Outcome string

const (
	// Forwarded is a frame sent out one port.
	Forwarded Outcome = "Forwarded"

	// Flooded is a frame replicated to every eligible port.
	Flooded Outcome = "Flooded"

	// Dropped is a frame discarded without transmission.
	Dropped Outcome = "Dropped"

	// Consumed is a frame received and taken by the device for itself.
	Consumed Outcome = "Consumed"

	// Held is a frame that neither arrived nor failed: it is queued on an
	// Incomplete neighbor entry, waiting on address resolution that may still
	// release it or time it out. See the routing package's neighbor lifecycle.
	Held Outcome = "Held"
)

// Reason explains why a frame met its outcome, such as why it was dropped or flooded.
// The zero value is an empty reason, typical when a frame is forwarded normally.
// Reason constants are declared by the packages that emit them. Reason is safe for concurrent use.
type Reason string

// Trace records the sequence of processing steps, final outcome, and outcome reason for a frame.
// The zero value is an empty trace. Trace is safe for concurrent read access.
type Trace struct {
	Steps   []Step
	Outcome Outcome
	Reason  Reason
}

// Canonical returns a copy of the trace with every step canonicalized.
func (t Trace) Canonical() Trace {
	out := Trace{
		Outcome: t.Outcome,
		Reason:  t.Reason,
	}
	if len(t.Steps) > 0 {
		out.Steps = make([]Step, len(t.Steps))
		for i, s := range t.Steps {
			out.Steps[i] = s.Canonical()
		}
	}
	return out
}

// Equal reports whether two traces are semantically equal.
func (t Trace) Equal(other Trace) bool {
	return Equal(t, other)
}

// Equal reports whether two traces are semantically equal.
func Equal(a, b Trace) bool {
	if a.Outcome != b.Outcome || a.Reason != b.Reason {
		return false
	}
	if len(a.Steps) != len(b.Steps) {
		return false
	}
	for i := range a.Steps {
		if !EqualStep(a.Steps[i], b.Steps[i]) {
			return false
		}
	}
	return true
}

// String returns the deterministic human-readable representation of the trace.
func (t Trace) String() string {
	return Render(t)
}

// Change records a transition in one configuration field of a subject between two configurations.
// From and To hold typed semantic facts or nil if unset.
// The zero value is an empty change. Change is safe for concurrent read access.
type Change struct {
	Layer    Layer
	Subject  Subject
	Field    string
	From     Fact
	To       Fact
	Evidence []EvidenceRef
}

// Canonical returns a copy of the change with evidence sorted and deduplicated.
func (c Change) Canonical() Change {
	out := Change{
		Layer:   c.Layer,
		Subject: c.Subject,
		Field:   c.Field,
		From:    c.From,
		To:      c.To,
	}
	if len(c.Evidence) > 0 {
		ev := make([]EvidenceRef, len(c.Evidence))
		copy(ev, c.Evidence)
		slices.Sort(ev)
		out.Evidence = slices.Compact(ev)
	}
	return out
}

// Equal reports whether two changes are semantically equal.
func (c Change) Equal(other Change) bool {
	return EqualChange(c, other)
}

// EqualChange reports whether two changes are semantically equal.
func EqualChange(a, b Change) bool {
	return CompareChange(a, b) == 0
}

// CompareChange compares two changes in canonical order.
// The order evaluates Layer, Subject (Kind then Key), Field, From fact, To fact, and Evidence.
func CompareChange(a, b Change) int {
	ca := a.Canonical()
	cb := b.Canonical()

	if c := strings.Compare(string(ca.Layer), string(cb.Layer)); c != 0 {
		return c
	}
	if c := ca.Subject.Compare(cb.Subject); c != 0 {
		return c
	}
	if c := strings.Compare(ca.Field, cb.Field); c != 0 {
		return c
	}
	if c := CompareFact(ca.From, cb.From); c != 0 {
		return c
	}
	if c := CompareFact(ca.To, cb.To); c != 0 {
		return c
	}
	return slices.Compare(ca.Evidence, cb.Evidence)
}

// SortChanges sorts a slice of changes in-place by their canonical ordering.
func SortChanges(changes []Change) {
	slices.SortFunc(changes, CompareChange)
}

// CanonicalChanges returns a sorted copy of changes with each change canonicalized.
func CanonicalChanges(changes []Change) []Change {
	if changes == nil {
		return nil
	}
	out := make([]Change, len(changes))
	for i, c := range changes {
		out[i] = c.Canonical()
	}
	SortChanges(out)
	return out
}

// String returns the deterministic human-readable representation of the change.
func (c Change) String() string {
	return RenderChange(c)
}
