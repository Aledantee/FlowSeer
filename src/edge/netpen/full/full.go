package full

// full.go implements the four-phase evidence-gated `full` orchestration.
// The orchestrator is a thin coordinator over the runner: it
// runs recon, arms the burst from the evidence, selects follow-ups from
// the evidence, and emits a summary. Each phase drives its own runner
// instance, since a runner runs once. The orchestrator merges the
// findings into one stream the CLI consumes.
//
// Phase contract:
//
//  1. recon: ARP sweep + passive listen → typed Evidence.
//  2. burst: the baseline's bounded attack set, --duration bound, shared
//     leg. The unconditional core (stproot, camflood, dhcpstarve, gratarp,
//     dtp, roguera, llmnr) always fires. daddos, arpspoof, and vrrp are
//     armed by recon evidence or flags. All run under Orchestrated=true so
//     no permanent mode can dispatch.
//  3. follow-ups: sequential, selected ONLY by recon evidence (no
//     blind sequences). Each gated follow-up runs as its own runner
//     invocation with Orchestrated=true.
//  4. report: a summary record closes the run, including the sweep-net
//     fallback chain and one count per verdict kind, read from the
//     verdict records each phase's runner emitted.
//
// Interrupted run in ANY phase emits partial findings: the command's
// signal handler engages the runner's teardown and the stream flushes
// before the process exits.
//
// A named-but-absent watch leg FAILS FAST before recon — a
// deliberate deviation from the baseline's silent single-leg
// degradation, asserted as intentional.

import (
	"context"
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/spawn"
	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
	"go.aledante.io/FlowSeer/src/edge/netpen/runner"
)

// ErrCodeFull is the wire identity for an orchestration-level failure
// (e.g. a named-but-absent watch leg).
var ErrCodeFull = errs.NewCode("netpen/full")

// Config configures a [Full] run. Its maps and callback state must not be
// mutated concurrently with Run.
type Config struct {
	// AttackLeg is the attack interface. Required.
	AttackLeg link.Leg
	// WatchLeg is the optional watch leg. Nil means a single-leg run with
	// pending traversal verdicts.
	WatchLeg link.Leg
	// WatchLegNamed is the watch interface name the operator passed
	// (-w). When non-empty, the orchestrator fails fast if WatchLeg is
	// nil — a named-but-absent watch leg is an error, not a silent
	// single-leg degradation.
	WatchLegNamed string
	// AttackLegName is the attack interface name (for the meta record).
	AttackLegName string
	// Duration bounds the burst phase. Non-positive values use 30 seconds.
	Duration time.Duration
	// ScanTime bounds the recon passive listen. Non-positive values use
	// 15 seconds.
	ScanTime time.Duration
	// NoSpoof skips ARP poisoning of discovered hosts (disarms arpspoof
	// and portsteal).
	NoSpoof bool
	// Rate limits packet emission (pps, zero = unlimited).
	Rate int
	// Catalog resolves every attack the phases dispatch. The command
	// passes the shipped catalog. Tests substitute recording stubs.
	Catalog catalog.Catalog
	// Target is handed to every phase's runner as the hosts, addresses,
	// and identities the behaviors act on. A burst behavior whose fields
	// are empty is a skipped verdict the summary counts.
	Target check.Target
	// MaxImpact is the impact ceiling every phase's runner enforces.
	MaxImpact check.Impact
	// Observe is the observation window every phase's runner hands its
	// behaviors. Non-positive values use the runner's default.
	Observe time.Duration
	// TeardownBudget scales the teardown budget for tests.
	TeardownBudget time.Duration
	// ReconFn is the recon function. Tests substitute a stub that
	// returns canned Evidence. When nil, the orchestrator uses the
	// default recon (passive listen + ARP sweep).
	ReconFn func(ctx context.Context, cfg Config) (Evidence, error)
	// SweepNet is the configured network for the ARP sweep. Empty means
	// 172.16.0.0/24.
	SweepNet string
}

// Full coordinates recon, a bounded burst, sequential follow-ups, and a
// summary. Construct it with [NewFull] and call Run once. The zero value is
// unusable. Records may be read concurrently with Run. Drain the record
// channel during Run, since a full buffer blocks progress.
type Full struct {
	cfg Config
	recorder
}

// NewFull constructs the orchestrator from the config. A live record
// channel is created here so the CLI can consume records as they arrive
// while Run executes. It retains the config's catalog and callbacks.
func NewFull(cfg Config) *Full {
	return &Full{cfg: cfg, recorder: newRecorder()}
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

	// Phase 4: report. The summary counts the verdict records each
	// phase's runner emitted, one per attack.
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

	opts := runner.Options{
		Catalog:        f.cfg.Catalog,
		AttackLeg:      f.cfg.AttackLeg,
		WatchLeg:       f.cfg.WatchLeg,
		Attacks:        refs,
		Target:         f.cfg.Target,
		MaxImpact:      f.cfg.MaxImpact,
		Observe:        f.cfg.Observe,
		Rate:           f.cfg.Rate,
		Orchestrated:   true,
		TeardownBudget: f.cfg.TeardownBudget,
	}
	if timeout > 0 {
		opts.Timeout = timeout
	}

	r := runner.NewRunner(opts)

	var recs []findings.Record
	done := make(chan struct{})
	// close(done) is fn's own deferred call, so it runs during a panic
	// unwind too: the caller's <-done below never hangs on a phase that
	// panics collecting records.
	spawn.Go(ctx, "netpen full.runPhase collect", func() {
		defer close(done)
		for rec := range r.Stream().Iter() {
			recs = append(recs, rec)
		}
	})

	runErr := r.Run(ctx)
	r.Wait()
	<-done
	return recs, runErr
}

// summaryRecord builds the closing summary record from the phases' records,
// including the sweep-net fallback chain recorded by recon and one count
// per verdict kind.
func (f *Full) summaryRecord(ev Evidence, dispatchedRefs []runner.AttackRef, recs []findings.Record) findings.Record {
	sweep, _ := ev["sweep-net"].(string)
	s := countByKind(recs)
	s.Attacks = len(dispatchedRefs)
	s.SweepNet = sweep

	r := findings.NewRecord(findings.KindSummary)
	r.Summary = &s
	return r
}

// countByKind tallies the findings, verdicts, and errors of one phase's
// records into a summary with no attack count or sweep net.
func countByKind(recs []findings.Record) findings.Summary {
	var s findings.Summary
	for _, r := range recs {
		switch r.Kind {
		case findings.KindFinding:
			s.Findings++
		case findings.KindVerdict:
			if r.Verdict != nil {
				s.Count(r.Verdict.Kind)
			}
		case findings.KindError:
			s.Errors++
		}
	}
	return s
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
