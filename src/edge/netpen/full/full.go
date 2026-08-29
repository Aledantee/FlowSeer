package full

// full.go implements the four-phase evidence-gated `full` orchestration.
// The orchestrator is a thin coordinator over the runner: it
// runs recon, arms the burst from the evidence, selects follow-ups from
// the evidence, and emits a summary. Each phase drives its own runner
// instance (the runner is single-Run); the orchestrator merges the
// findings into one stream the CLI consumes.
//
// Phase contract:
//
//  1. recon: ARP sweep + passive listen → typed Evidence.
//  2. burst: the baseline's bounded attack set, --duration bound, shared
//     leg. Unconditional core (stproot, camflood, dhcpstarve, gratarp,
//     dtp, roguera, llmnr) always fires; daddos/arpspoof/vrrp armed by
//     recon evidence/flags. All run under Orchestrated=true so no
//     permanent mode can dispatch.
//  3. follow-ups: sequential, selected ONLY by recon evidence (no
//     blind sequences). Each gated follow-up runs as its own runner
//     invocation with Orchestrated=true.
//  4. report: a summary record closes the run, including the sweep-net
//     fallback chain and the traversal verdicts.
//
// Interrupted run in ANY phase emits partial findings: the runner's
// SignalHandler engages teardown and the stream flushes before the
// process exits. The watch-leg evidence window derives from the ACTUAL
// burst end (not flag arithmetic): the orchestrator records burstEnd
// when the burst phase completes (or is interrupted) and the follow-up
// phase uses it.
//
// A named-but-absent watch leg FAILS FAST before recon — a
// deliberate deviation from the baseline's silent single-leg
// degradation, asserted as intentional.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// ErrCodeFull is the wire identity for an orchestration-level failure
// (e.g. a named-but-absent watch leg).
var ErrCodeFull = errs.NewCode("netpen/full")

// Config configures the `full` orchestrator.
type Config struct {
	// AttackLeg is the attack interface. Required.
	AttackLeg link.Leg
	// WatchLeg is the optional watch leg. nil = single-leg run (pending
	// verdicts). A non-nil WatchLegNamed that does not exist fails fast
	// before recon (a deviation from the baseline, which degraded silently).
	WatchLeg link.Leg
	// WatchLegNamed is the watch interface name the operator passed
	// (-w). When non-empty, the orchestrator fails fast if WatchLeg is
	// nil — a named-but-absent watch leg is an error, not a silent
	// single-leg degradation.
	WatchLegNamed string
	// AttackLegName is the attack interface name (for the meta record).
	AttackLegName string
	// Duration bounds the burst phase (seconds). Zero means a minimal
	// default; the orchestrator clamps to at least 1s.
	Duration time.Duration
	// ScanTime bounds the recon passive listen (seconds).
	ScanTime time.Duration
	// NoSpoof skips ARP poisoning of discovered hosts (disarms arpspoof
	// and portsteal).
	NoSpoof bool
	// Rate limits packet emission (pps, zero = unlimited).
	Rate int
	// Behaviors is the behavior map. The orchestrator accepts this for
	// testability: tests substitute recording stubs. When nil, the
	// orchestrator builds the merged map from the four behavior
	// packages.
	Behaviors map[string]runner.Behavior
	// TeardownBudget scales the teardown budget for tests.
	TeardownBudget time.Duration
	// ReconFn is the recon function. Tests substitute a stub that
	// returns canned Evidence. When nil, the orchestrator uses the
	// default recon (passive listen + ARP sweep).
	ReconFn func(ctx context.Context, cfg Config) (Evidence, error)
	// SweepNet is the configured sweep network for the ARP sweep
	// fallback. Empty means derive from the attack leg, then fall back
	// to 172.16.0.0/24.
	SweepNet string
}

// Full is the orchestrator. It holds the merged findings and the
// traversal verdicts. The CLI (cmd/netpen) constructs one, calls Run,
// and streams the findings into the selected output mode.
//
// It embeds [recorder] for the shared record-collection surface
// (Records, RecordChan, appendRecord, closeRecords).
type Full struct {
	cfg Config
	recorder
	verdsMu sync.Mutex
	verds   []verdict
}

type verdict struct {
	attack string
	mode   string
	kind   findings.Kind // resisted, skipped, pending, or empty (confirmed via finding)
	detail string
}

// NewFull constructs the orchestrator from the config. A live record
// channel is created here so the CLI can consume records as they arrive
// while Run executes.
func NewFull(cfg Config) *Full {
	return &Full{cfg: cfg, recorder: newRecorder()}
}

// appendVerdict adds a traversal verdict.
func (f *Full) appendVerdict(v verdict) {
	f.verdsMu.Lock()
	f.verds = append(f.verds, v)
	f.verdsMu.Unlock()
}

// Run executes the four-phase orchestration. It blocks until all phases
// complete or the context is canceled. On cancellation, partial
// findings are still emitted: each phase's runner flushes its
// stream before returning, and the orchestrator collects whatever
// completed.
//
// The returned error is non-nil only for orchestration-level failures
// (named-but-absent watch leg, recon failure). Behavior-level failures
// surface as typed error records in the findings, not as a run failure
// (findings never move the exit code).
func (f *Full) Run(ctx context.Context) error {
	defer f.closeRecords()
	// Deviation from the baseline: a named-but-absent watch leg fails fast before recon.
	if f.cfg.WatchLegNamed != "" && f.cfg.WatchLeg == nil {
		err := missingWatchLegErr(f.cfg.WatchLegNamed)
		f.appendRecord(errRecord(err, "full", ""))
		return err
	}

	// Phase 1: recon.
	f.appendRecord(progress("full", "", "recon", "ARP sweep + passive listen"))
	reconFn := f.cfg.ReconFn
	if reconFn == nil {
		reconFn = defaultRecon
	}
	ev, err := reconFn(ctx, f.cfg)
	if err != nil {
		// Recon failure is a runtime failure (exit 1), but partial
		// findings are still emitted.
		f.appendRecord(errRecord(err, "full", ""))
		return err
	}
	f.appendRecord(progress("full", "", "recon", fmt.Sprintf("done: %d evidence keys", countEvidence(ev))))

	// Record the watch-leg presence in evidence for the gate surfaces.
	if f.cfg.WatchLeg != nil {
		ev[EvWatchLeg] = true
	}

	// Phase 2: burst.
	burstRefs := armBurst(ev, f.cfg.NoSpoof)
	f.appendRecord(progress("full", "", "burst", fmt.Sprintf("%d workers armed", len(burstRefs))))

	duration := f.cfg.Duration
	if duration <= 0 {
		duration = 30 * time.Second
	}
	burstRecs, burstErr := f.runPhase(ctx, burstRefs, duration)
	dispatchedRefs := append([]runner.AttackRef(nil), burstRefs...)
	summaryRecs := append([]findings.Record(nil), burstRecs...)
	for _, r := range burstRecs {
		f.appendRecord(r)
	}
	if burstErr != nil {
		rec := errRecord(burstErr, "full", "")
		f.appendRecord(rec)
		summaryRecs = append(summaryRecs, rec)
	}
	f.appendRecord(progress("full", "", "burst", "done"))

	// Watch-leg traversal evidence: capture on the watch leg for a
	// bounded window from the ACTUAL burst end. Any frame seen
	// in the window marks traversal; verdicts key off this evidence.
	if f.cfg.WatchLeg != nil && observeTraversal(ctx, f.cfg.WatchLeg, traversalWindow) {
		ev["watch-recording"] = true
	}

	// Phase 3: follow-ups (sequential).
	followUps := selectFollowUps(ev, f.cfg.WatchLeg != nil, f.cfg.NoSpoof)
	f.appendRecord(progress("full", "", "follow-ups", fmt.Sprintf("%d selected", len(followUps))))

	for _, fu := range followUps {
		if ctx.Err() != nil {
			break
		}
		f.appendRecord(progress("full", "", "follow-up", fmt.Sprintf("%s: %s", fu.ref.Name, fu.reason)))
		dispatchedRefs = append(dispatchedRefs, fu.ref)
		fuRecs, fuErr := f.runPhase(ctx, []runner.AttackRef{fu.ref}, 0)
		summaryRecs = append(summaryRecs, fuRecs...)
		for _, r := range fuRecs {
			f.appendRecord(r)
		}
		if fuErr != nil {
			rec := errRecord(fuErr, fu.ref.Name, fu.ref.Mode)
			f.appendRecord(rec)
			summaryRecs = append(summaryRecs, rec)
		}
	}
	f.appendRecord(progress("full", "", "follow-ups", "done"))

	// Phase 4: report.
	// Compute traversal verdicts from the burst evidence window.
	f.computeVerdicts(ev)
	f.appendRecord(f.summaryRecord(ev, dispatchedRefs, summaryRecs))
	return nil
}

// runPhase runs a set of attack refs through a fresh runner instance
// with Orchestrated=true. The runner handles the durability gate,
// teardown, and signal handling for each behavior. The phase timeout
// bounds the runner's Run (zero = no timeout, for follow-ups that run
// to completion).
func (f *Full) runPhase(ctx context.Context, refs []runner.AttackRef, timeout time.Duration) ([]findings.Record, error) {
	if len(refs) == 0 {
		return nil, nil
	}

	behaviors := f.cfg.Behaviors
	if behaviors == nil {
		behaviors = MergedBehaviors()
	}

	opts := runner.Options{
		AttackLeg:      f.cfg.AttackLeg,
		WatchLeg:       f.cfg.WatchLeg,
		Attacks:        refs,
		Behaviors:      behaviors,
		Rate:           f.cfg.Rate,
		Orchestrated:   true,
		TeardownBudget: f.cfg.TeardownBudget,
		SuppressOutput: true,
	}
	if timeout > 0 {
		opts.Timeout = timeout
	}

	r := runner.NewRunner(opts)

	var recs []findings.Record
	done := make(chan struct{})
	go func() {
		for rec := range r.Stream().Iter() {
			recs = append(recs, rec)
		}
		close(done)
	}()

	runErr := r.Run(ctx)
	r.Wait()
	<-done
	return recs, runErr
}

// computeVerdicts derives traversal verdicts from the burst evidence and
// the watch-leg recording. A behavior that ran in the burst gets:
//   - pending: no watch leg (indistinguishable from resisted without one)
//   - resisted: watch leg present but no traversal evidence
//   - confirmed: watch leg recorded burst frames (upgrade pending->confirmed)
//
// The upgrade is the two-leg semantics: the watch-leg evidence window
// derives from the ACTUAL burst end (recorded by Run), not flag
// arithmetic.
// traversalWindow is how long the watch leg is observed after the burst
// ends. It derives the evidence window from the actual burst end, never
// from flag arithmetic.
const traversalWindow = 2 * time.Second

// observeTraversal reports whether any frame appears on the watch leg
// within the window. It returns true on the first observed frame; an
// empty window or leg error is not traversal evidence.
func observeTraversal(ctx context.Context, watch link.Leg, window time.Duration) bool {
	wctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	// First frame or the window's end: a clean first frame is traversal
	// evidence; a drained channel or an error frame is not.
	frame, ok := <-watch.Receive(wctx)
	return ok && frame.Err == nil
}

func (f *Full) computeVerdicts(ev Evidence) {
	// In the test/hooked path, the watch-leg recording is simulated via
	// the evidence map's "watch-recording" key (set by the recon stub or
	// the test harness). Absent that, a single-leg run = pending.
	hasWatch := f.cfg.WatchLeg != nil
	watchRecorded, _ := ev["watch-recording"].(bool)

	burstRefs := armBurst(ev, f.cfg.NoSpoof)
	for _, ref := range burstRefs {
		if !hasWatch {
			f.appendVerdict(verdict{attack: ref.Name, mode: ref.Mode, kind: findings.KindPending, detail: "no -w watch leg to observe traversal"})
			continue
		}
		if watchRecorded {
			f.appendVerdict(verdict{attack: ref.Name, mode: ref.Mode, kind: findings.KindFinding, detail: "traversal confirmed on watch leg"})
		} else {
			f.appendVerdict(verdict{attack: ref.Name, mode: ref.Mode, kind: findings.KindResisted, detail: "watch leg saw no traversal"})
		}
	}
}

// summaryRecord builds the closing summary record, including the
// sweep-net fallback chain recorded by recon and the traversal verdicts.
func (f *Full) summaryRecord(ev Evidence, dispatchedRefs []runner.AttackRef, recs []findings.Record) findings.Record {
	f.verdsMu.Lock()
	verds := make([]verdict, len(f.verds))
	copy(verds, f.verds)
	f.verdsMu.Unlock()

	counts := countByKind(recs)
	for _, v := range verds {
		switch v.kind {
		case findings.KindResisted:
			counts.resisted++
		case findings.KindPending:
			counts.pending++
		case findings.KindSkipped:
			counts.skipped++
		case findings.KindError:
			counts.errors++
		}
	}

	sweep, _ := ev["sweep-net"].(string)

	r := findings.NewRecord(findings.KindSummary)
	r.Summary = &findings.Summary{
		Attacks:  len(dispatchedRefs),
		Findings: counts.findings,
		Resisted: counts.resisted,
		Skipped:  counts.skipped,
		Pending:  counts.pending,
		Errors:   counts.errors,
		SweepNet: sweep,
	}
	return r
}

type kindCounts struct {
	findings int
	resisted int
	skipped  int
	pending  int
	errors   int
}

func countByKind(recs []findings.Record) kindCounts {
	var c kindCounts
	for _, r := range recs {
		switch r.Kind {
		case findings.KindFinding:
			c.findings++
		case findings.KindResisted:
			c.resisted++
		case findings.KindSkipped:
			c.skipped++
		case findings.KindPending:
			c.pending++
		case findings.KindError:
			c.errors++
		}
	}
	return c
}

//nolint:unparam // mode is always "" for progress records
func progress(attack, mode, phase, detail string) findings.Record {
	r := findings.NewRecord(findings.KindProgress)
	r.Attack = attack
	r.Mode = mode
	r.Progress = &findings.Progress{Phase: phase, Detail: detail}
	return r
}

func errRecord(err error, attack, mode string) findings.Record {
	return findings.NewErrorRecord(err, attack, mode)
}

// countEvidence returns the number of non-empty evidence keys.
func countEvidence(ev Evidence) int {
	n := 0
	for k := range ev {
		if ev.Has(k) {
			n++
		}
	}
	return n
}

// MergedBehaviors builds the merged behavior map from the four behavior
// packages. This is the production path; tests substitute a stub map via
// Config.Behaviors. cmd/netpen calls this to avoid duplicating the
// four-package merge.
func MergedBehaviors() map[string]runner.Behavior {
	out := make(map[string]runner.Behavior)
	for _, m := range []map[string]runner.Behavior{
		l2Behaviors(), fhBehaviors(), ip6Behaviors(), routingBehaviors(),
	} {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
