package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// DefaultTeardownBudget bounds the total time the teardown executor may
// spend running armed steps before forcing completion (roughly ten
// seconds). Tests scale this down by setting [Options.TeardownBudget]
// so the bounded-budget assertion runs in milliseconds.
const DefaultTeardownBudget = 10 * time.Second

// gateDecision is the outcome of the durability gate for one (attack,
// mode) pair. It carries everything Run needs to dispatch the behavior
// correctly: whether to proceed, the announcement to emit before the
// first frame, and the teardown handle to arm (or nil).
type gateDecision struct {
	// proceed is false when the gate refuses (permanent without ack, or
	// permanent under orchestration). A refused behavior receives a nil
	// AttackLeg and is never invoked.
	proceed bool

	// announce is a run-start announcement record emitted before the
	// behavior runs. Non-nil for transient-decay (the decay bound) and
	// acknowledged-permanent (the accepted mode + consequence); nil for
	// non-destructive and temporary-restored (no announcement).
	announce *findings.Record

	// teardown is the armed-teardown handle handed to a temporary-restored
	// behavior via [Deps.Teardown]. Non-nil for temporary-restored and
	// acknowledged-permanent entries (both arm teardown); nil otherwise.
	teardown *Teardown
}

// gate evaluates the durability class for one (attack, mode) pair and
// decides whether and how to dispatch. It consults
// the catalog entry BEFORE the leg's TX is ever handed to the behavior:
// a refused behavior literally never receives a send-capable leg, so zero
// frames are possible. The ack set is per (name, mode); the
// orchestration flag closes the permanent path entirely.
//
// On refusal the gate emits the typed refusal record through the stream
// and returns proceed=false; Run then returns the coded non-zero exit.
func gate(s *Stream, entry catalog.Entry, ack map[string]bool, orchestrated bool) gateDecision {
	switch entry.Class {
	case catalog.NonDestructive:
		return gateDecision{proceed: true}

	case catalog.TransientDecay:
		// Announce the catalog's decay bound as a run-start record
		// before executing; arm nothing (transient-decay has no
		// restore).
		rec := decayAnnouncement(entry)
		return gateDecision{proceed: true, announce: rec}

	case catalog.TemporaryRestored:
		// Teardown armed before the first frame; steps execute on
		// completion or interrupt. The handle is returned; the
		// behavior arms steps against it.
		return gateDecision{proceed: true, teardown: newTeardown(s, entry)}

	case catalog.PermanentDestructive:
		// Under orchestration the permanent path is closed regardless
		// of ack (full never dispatches a permanent mode).
		if orchestrated {
			rec := refusalRecord(entry, "permanent-destructive mode cannot dispatch under orchestration")
			_ = s.send(*rec)
			return gateDecision{proceed: false, announce: rec}
		}
		if !ack[entryKey(entry.Name, entry.Mode)] {
			opt := requiredOptIn(entry)
			rec := refusalRecord(entry, fmt.Sprintf(
				"permanent-destructive mode %q requires the per-run opt-in %s", entry.Mode, opt))
			_ = s.send(*rec)
			return gateDecision{proceed: false, announce: rec}
		}
		// Acknowledged permanent: announce the accepted mode + consequence
		// before the first frame, then treat like temporary-restored for
		// teardown arming.
		rec := acceptedAnnouncement(entry)
		return gateDecision{proceed: true, announce: rec, teardown: newTeardown(s, entry)}

	default:
		// An unknown class is a catalog/registration bug; the family-guard
		// test rejects these at registration, so this is defense in depth.
		rec := refusalRecord(entry, fmt.Sprintf("unknown durability class %q", entry.Class))
		_ = s.send(*rec)
		return gateDecision{proceed: false, announce: rec}
	}
}

// requiredOptIn returns the opt-in flag string the operator must supply to
// acknowledge a permanent-destructive mode (the acknowledgment names
// each permanent mode). It is the mode flag with the leading dashes the
// CLI uses, mirroring the baseline's --i-know gate shape.
func requiredOptIn(entry catalog.Entry) string {
	if entry.Mode == "" {
		return "--ack <name>"
	}
	return "--ack " + entry.Name + " --" + entry.Mode
}

// refusalRecord builds a typed [findings.KindRefusal] record naming the
// attack, the mode, and the reason the gate declined before any frame.
// The record's Reason is the stable machine-contract string a
// consumer matches on.
func refusalRecord(entry catalog.Entry, reason string) *findings.Record {
	r := findings.NewRecord(findings.KindRefusal)
	r.Attack = entry.Name
	r.Mode = entry.Mode
	r.Refusal = &findings.Refusal{Reason: reason}
	return &r
}

// decayAnnouncement builds the run-start announcement record for a
// transient-decay entry: the catalog's recorded decay bound (the decay
// bound is announced at run start). It rides as a progress record
// so both output modes surface it before the first finding.
func decayAnnouncement(entry catalog.Entry) *findings.Record {
	r := findings.NewRecord(findings.KindProgress)
	r.Attack = entry.Name
	r.Mode = entry.Mode
	bound := entry.Teardown
	if bound == "" {
		bound = "decay bound unrecorded"
	}
	r.Progress = &findings.Progress{
		Phase:  "decay-bound",
		Detail: bound,
	}
	return &r
}

// acceptedAnnouncement builds the run-start announcement record for an
// acknowledged permanent-destructive entry: it names the accepted mode and
// its consequence before the first frame.
func acceptedAnnouncement(entry catalog.Entry) *findings.Record {
	r := findings.NewRecord(findings.KindProgress)
	r.Attack = entry.Name
	r.Mode = entry.Mode
	r.Progress = &findings.Progress{
		Phase:  "accepted-permanent",
		Detail: fmt.Sprintf("mode %q accepted; %s", entry.Mode, entry.Teardown),
	}
	return &r
}

// Teardown is the arming handle a temporary-restored behavior registers
// its restore steps against. The runner constructs one
// before invoking the behavior and hands it through [Deps.Teardown]; the
// behavior arms each named restore step via [Teardown.Arm] before its
// first frame. After the behavior returns (or is interrupted), the
// runner executes the armed steps via [Teardown.Run].
//
// Steps execute in the order armed — which the behavior establishes as
// reverse-dependency order: least-dependent (host-local state, e.g.
// ip_forward) armed first, executed first, so host-local state restores
// ahead of neighbor-cache repairs. Each step runs in its own error scope;
// one failing step does not abandon later ones. Per-step failures collect
// into a named partial-failure record ([findings.KindError] carrying
// [catalog.ErrCodeTeardownPartial] and the failed step names) and set
// the non-zero exit.
//
// A Teardown is safe for concurrent use: the behavior arms steps from its
// own goroutine, and completion and interrupt may both call Run — the
// runOnce guard ensures the steps execute exactly once, and the mutex
// guards the step and failed lists.
type Teardown struct {
	stream *Stream
	entry  catalog.Entry

	// mu guards steps, failed, and completed. Arm holds the write lock
	// to append; execute snapshots steps under the lock then runs them
	// unlocked (step functions may block), publishing progress under
	// the lock after each step; the reporting helpers read under the
	// lock.
	mu sync.Mutex

	// steps is the ordered list of armed steps, in arm order.
	steps []teardownStep

	// failed collects the names of steps that failed during the last
	// [Teardown.Run], so the partial-failure record can name them.
	failed []string

	// completed collects the names of steps that finished (success or
	// failure) during [Teardown.Run], so the abandoned-steps report on
	// a forced exit can compute armed minus completed.
	completed []string

	// runOnce ensures Run executes the steps exactly once even when
	// completion and interrupt call it concurrently (completion
	// and interrupt share one entry point).
	runOnce sync.Once
}

// teardownStep is one armed restore step: a name (for the partial-failure
// record) and the function the behavior registered.
type teardownStep struct {
	name string
	fn   TeardownStep
}

// TeardownStep is the restore function a behavior registers against a
// [Teardown] via [Teardown.Arm]. It runs under a context bounded by the
// teardown budget; an error is collected into the partial-failure record
// and does not stop later steps.
type TeardownStep func(ctx context.Context) error

// newTeardown constructs a Teardown tied to the stream (for the
// partial-failure record) and the catalog entry (for naming).
func newTeardown(s *Stream, entry catalog.Entry) *Teardown {
	return &Teardown{stream: s, entry: entry}
}

// Arm registers one named restore step. The behavior calls this before
// emitting its first frame; the order of Arm calls is the execution order
// (reverse-dependency: least-dependent first). A step name identifies the
// step in the partial-failure record; names should be stable and
// descriptive (e.g. "ip-forward-restore", "neighbor-unicast-repair").
func (t *Teardown) Arm(name string, fn TeardownStep) {
	t.mu.Lock()
	t.steps = append(t.steps, teardownStep{name: name, fn: fn})
	t.mu.Unlock()
}

// armed reports whether at least one step was armed. The runner calls
// this after the behavior returns to enforce the loud-failure contract:
// a temporary-restored behavior that arms zero steps is a coded runtime
// failure (a required restore that restores nothing is a bug).
func (t *Teardown) armed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.steps) > 0
}

// partialRecord builds the named partial-failure record for a teardown
// that could not complete every step. It carries
// [catalog.ErrCodeTeardownPartial] and names the failed steps so the
// operator knows exactly what could not be restored.
func (t *Teardown) partialRecord() findings.Record {
	failed := t.failedSteps()
	r := findings.NewRecord(findings.KindError)
	r.Attack = t.entry.Name
	r.Mode = t.entry.Mode
	r.Error = &findings.ErrorRecord{
		Code:    catalog.ErrCodeTeardownPartial.String(),
		Message: "teardown partial failure: " + strings.Join(failed, ", "),
		Attack:  t.entry.Name,
	}
	return r
}
