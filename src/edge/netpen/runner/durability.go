package runner

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// DefaultTeardownBudget is the cleanup allowance for each behavior when
// Options.TeardownBudget is non-positive. Steps must respect their contexts
// to complete within this allowance.
const DefaultTeardownBudget = 10 * time.Second

// gateDecision is the outcome of the dispatch gate for one (attack, mode)
// pair. It carries everything Run needs to dispatch the behavior
// correctly: whether to proceed and why not, the announcement to emit
// before the first frame, and the teardown handle to arm (or nil).
type gateDecision struct {
	// proceed is false when the gate refuses. A refused behavior receives
	// no leg and is never invoked.
	proceed bool

	// stop is set with proceed false when the refusal ends the run: a
	// permanent entry the operator named without its acknowledgment, or
	// under orchestration. A ceiling refusal leaves it false and the run
	// continues with the next attack.
	stop bool

	// announce is a run-start announcement record emitted before the
	// behavior runs: the decay bound of a decaying entry or the accepted
	// mode of an acknowledged permanent one; nil otherwise. On refusal it
	// is the refusal record the gate already sent.
	announce *findings.Record

	// teardown is the armed-teardown handle handed to a behavior whose
	// entry restores actively; nil otherwise.
	teardown *Teardown
}

// gate decides whether and how to dispatch one (attack, mode) pair, in the
// order ceiling, acknowledgment, legs. It consults the entry BEFORE the
// leg's TX is ever handed to the behavior: a refused behavior never
// receives a send-capable leg, so zero frames are possible. The ack set is
// per (name, mode); the orchestration flag closes the permanent path
// entirely.
//
// On refusal the gate emits the typed refusal record through the stream
// and returns proceed=false.
func gate(s *Stream, entry check.Entry, ceiling check.Impact, ack map[string]bool, orchestrated bool, watchLeg bool) gateDecision {
	if entry.Impact.Level() > ceiling.Level() {
		rec := refusalRecord(entry, fmt.Sprintf("impact %s exceeds ceiling %s", entry.Impact, ceiling))
		_ = s.send(*rec)
		return gateDecision{announce: rec}
	}

	var announce *findings.Record
	if entry.Impact == check.ImpactPermanent {
		// Under orchestration the permanent path is closed regardless
		// of ack (full never dispatches a permanent mode).
		if orchestrated {
			rec := refusalRecord(entry, "permanent mode cannot dispatch under orchestration")
			_ = s.send(*rec)
			return gateDecision{stop: true, announce: rec}
		}
		if !ack[entryKey(entry.Name, entry.Mode)] {
			rec := refusalRecord(entry, fmt.Sprintf(
				"permanent mode %q requires the per-run opt-in %s", entry.Mode, requiredOptIn(entry)))
			_ = s.send(*rec)
			return gateDecision{stop: true, announce: rec}
		}
		announce = acceptedAnnouncement(entry)
	}

	if entry.Legs == check.WatchRequired && !watchLeg {
		rec := refusalRecord(entry, "requires a watch leg; none attached")
		_ = s.send(*rec)
		return gateDecision{stop: true, announce: rec}
	}

	dec := gateDecision{proceed: true, announce: announce}
	if entry.Restore.Kind == check.RestoreDecay {
		dec.announce = decayAnnouncement(entry)
	}
	if entry.Restore.Kind == check.RestoreActive {
		dec.teardown = newTeardown(s, entry)
	}
	return dec
}

// requiredOptIn returns the opt-in flag string the operator must supply to
// acknowledge a permanent mode. It is the mode flag with the leading dashes
// the CLI uses, mirroring the baseline's --i-know gate shape.
func requiredOptIn(entry check.Entry) string {
	if entry.Mode == "" {
		return "--i-accept-permanent " + entry.Name + "="
	}
	return "--i-accept-permanent " + entry.Name + "=" + entry.Mode
}

// refusalRecord builds a typed [findings.KindRefusal] record naming the
// attack, the mode, and the reason the gate declined before any frame.
// The record's Reason is the stable machine-contract string a
// consumer matches on.
func refusalRecord(entry check.Entry, reason string) *findings.Record {
	r := findings.NewRecord(findings.KindRefusal)
	r.Attack = entry.Name
	r.Mode = entry.Mode
	r.Refusal = &findings.Refusal{Reason: reason}
	return &r
}

// decayAnnouncement builds the run-start announcement record for a
// decaying entry: the bound the probe itself announces, or a statement
// that none is known. It rides as a progress record so both output modes
// surface it before the first finding.
func decayAnnouncement(entry check.Entry) *findings.Record {
	r := findings.NewRecord(findings.KindProgress)
	r.Attack = entry.Name
	r.Mode = entry.Mode
	bound := "decay bound unknown"
	if entry.Restore.Bound > 0 {
		bound = "decays within " + entry.Restore.Bound.String()
	}
	r.Progress = &findings.Progress{
		Phase:  "decay-bound",
		Detail: bound,
	}
	return &r
}

// acceptedAnnouncement builds the run-start announcement record for an
// acknowledged permanent entry: it names the accepted mode and whose state
// it changes before the first frame.
func acceptedAnnouncement(entry check.Entry) *findings.Record {
	r := findings.NewRecord(findings.KindProgress)
	r.Attack = entry.Name
	r.Mode = entry.Mode
	r.Progress = &findings.Progress{
		Phase:  "accepted-permanent",
		Detail: fmt.Sprintf("mode %q accepted; permanent change to %s state", entry.Mode, entry.Affects),
	}
	return &r
}

// Teardown is the arming handle a behavior whose entry restores actively
// registers its restore steps against. The runner constructs one before
// invoking the behavior and hands it through [check.Deps.Teardown]; the
// behavior arms each named restore step via [Teardown.Arm] before its first
// frame. After the behavior returns (or is interrupted), the runner
// executes the armed steps via [Teardown.Run].
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
	entry  check.Entry

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
	fn   check.Step
}

// newTeardown constructs a Teardown tied to the stream (for the
// partial-failure record) and the catalog entry (for naming).
func newTeardown(s *Stream, entry check.Entry) *Teardown {
	return &Teardown{stream: s, entry: entry}
}

// Arm registers one named restore step. The behavior calls this before
// emitting its first frame; the order of Arm calls is the execution order
// (reverse-dependency: least-dependent first). A step name identifies the
// step in the partial-failure record; names should be stable and
// descriptive (e.g. "ip-forward-restore", "neighbor-unicast-repair").
func (t *Teardown) Arm(name string, fn check.Step) {
	t.mu.Lock()
	t.steps = append(t.steps, teardownStep{name: name, fn: fn})
	t.mu.Unlock()
}

// armed reports whether at least one step was armed. The runner calls
// this after the behavior returns to enforce the loud-failure contract:
// an actively restored behavior that arms zero steps is a coded runtime
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
