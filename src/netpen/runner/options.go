// Package runner is netpen's embeddable run engine: an options-struct-plus-
// callback library surface (KTD6) that resolves behaviors through the
// [catalog] and drives each as a thin function over the protocol toolkit,
// delivering typed [findings.Record] values through a bounded stream whose
// lifecycle follows the Collection Primitives contract.
//
// The package follows naabu's embeddable shape (Options + Runner + Run) and
// zgrab2's per-attack context cancellation (a behavior runs under a context
// derived from Run's ctx). The CLI is one host; a future FlowSeer edge agent
// (A3) is another — it sets [Options.SuppressOutput] and consumes the stream
// programmatically, so a host embeds logic, never UI code (R7).
package runner

import (
	"context"
	"encoding/json"
	"time"

	"go.aledante.io/FlowSeer/src/netpen/catalog"
	"go.aledante.io/FlowSeer/src/netpen/findings"
	"go.aledante.io/FlowSeer/src/netpen/link"
)

// Options configures a [Runner]. It is a plain struct, not a functional-option
// chain, following the stdlib idiom (http.Server, tls.Config) the project's
// code-style mandates: a struct is self-documenting at the call site and free
// of per-option closure ceremony.
//
// Required fields: [Options.AttackLeg]. A zero [Options] is invalid by design;
// [NewRunner] rejects it rather than silently defaulting, so a missing leg is
// visible in review.
type Options struct {
	// AttackLeg is the leg attacks send and receive on. Required for any
	// run with a non-empty Attacks list; a nil AttackLeg causes Run to
	// return a coded error before dispatch.
	AttackLeg link.Leg

	// WatchLeg is the optional passive observation leg. A behavior
	// declaring [catalog.WatchRequired] fails fast when WatchLeg is nil
	// (R2): the runtime enforces the requirement uniformly so no
	// behavior re-checks it.
	WatchLeg link.Leg

	// Attacks is the ordered list of (name, mode) pairs to run, resolved
	// through the catalog. Each name must have a catalog entry and, if
	// [Options.Behaviors] is non-empty, a behavior function. The order is
	// preserved: behaviors run in the order listed.
	Attacks []AttackRef

	// Rate limits packet emission, in packets per second. Zero means
	// unlimited. Behaviors read the effective rate from [Deps.Rate] and
	// are responsible for honoring it; the runner does not enforce it.
	Rate int

	// Timeout bounds the whole run. Zero means no timeout. The runner
	// derives a context with this deadline from Run's ctx; a behavior
	// that exceeds it observes ctx.Err() (context.DeadlineExceeded).
	Timeout time.Duration

	// Behaviors maps an attack name to its run function. U8-U11 populate
	// this map from their attack packages. A name in Attacks with no
	// entry here (and no catalog registration) is a dispatch-level
	// failure; a name with a catalog entry but no behavior function is
	// also a dispatch-level failure, so a half-registered attack fails
	// fast rather than silently no-op.
	Behaviors map[string]Behavior

	// SuppressOutput is the A3 embed seam: when true the host consumes
	// the stream programmatically (via [Runner.Stream]) and CLI output
	// is suppressed. The runner itself does not write to any io.Writer;
	// the flag is a contract signal a host reads to decide whether to
	// wire its output mode. The U7 output modes consult the flag; the
	// runner merely carries it.
	SuppressOutput bool
}

// AttackRef is one entry in [Options.Attacks]: a (behavior, mode) pair. Mode
// is empty for the base (mode-less) behavior; a non-empty Mode selects a
// flag-gated variant (e.g. portsteal's "relay").
type AttackRef struct {
	// Name is the behavior's command name, matching the catalog entry
	// and the CLI dispatch table (e.g. "arpspoof", "portsteal").
	Name string

	// Mode is the mode flag without the leading dashes (e.g. "relay",
	// "wipe", "persist"). Empty selects the base behavior.
	Mode string
}

// Behavior is the run function a behavior package registers. It is invoked as
// run(ctx, deps) and emits findings through [Deps.Emitter]. A behavior error
// is surfaced as a typed [findings.ErrorRecord] through the stream and the run
// of remaining behaviors continues; only a dispatch-level failure or context
// cancellation aborts the whole run.
//
// The context is derived from [Runner.Run]'s ctx (zgrab2's per-attack
// cancellation pattern): a behavior should respect ctx.Done() and return
// promptly on cancellation. Run returns ctx.Err() unwrapped on cancellation
// (code-style: context cancellation surfaces as the unwrapped ctx.Err()).
type Behavior func(ctx context.Context, deps Deps) error

// Deps is the per-attack execution context handed to a [Behavior]. It is the
// once-per-project seam: U6 (durability gating) wraps the dispatch loop around
// this shape, and U8-U11 (behavior implementations) build on it without
// reworking signatures. Keep it minimal — add nothing speculative.
type Deps struct {
	// AttackLeg is the leg the behavior sends and receives on. Always
	// non-nil (Run rejects a nil AttackLeg before dispatch).
	AttackLeg link.Leg

	// WatchLeg is the passive observation leg, or nil when the run has no
	// watch leg. Behaviors declaring [catalog.WatchRequired] are
	// guaranteed a non-nil WatchLeg: the runtime fails fast before
	// invocation when it is absent.
	WatchLeg link.Leg

	// Rate is the effective packet-rate limit (packets per second) from
	// [Options.Rate]. Zero means unlimited. The behavior is responsible
	// for honoring it; the runner does not enforce it.
	Rate int

	// Entry is the resolved catalog row for this (behavior, mode) pair:
	// the durability class, legs requirement, preconditions, and help
	// text. U6 reads Entry.Class and Entry.Legs to gate dispatch.
	Entry catalog.Entry

	// Emitter is the typed sink the behavior writes findings through. It
	// is safe for concurrent use from one goroutine (the behavior's own);
	// a behavior that spawns goroutines must coordinate access itself.
	Emitter *Emitter
}

// Emitter is the typed sink a [Behavior] writes findings through. It wraps
// the run's [Stream] channel so a behavior emits typed [findings.Record]
// values without holding a reference to the stream itself.
//
// An Emitter is safe for use from a single goroutine (the behavior's run). A
// behavior that spawns concurrent workers must serialize Emit calls itself,
// or use one of the typed convenience methods under its own coordination.
type Emitter struct {
	stream *Stream
}

// Emit delivers one findings record to the stream. It blocks until the
// stream's bounded channel has room or the run's context is canceled, so a
// stalled consumer cannot force the producer to grow unbounded memory
// (backpressure: bounded channel + producer block respecting ctx). Returns
// false when the stream is terminating so the behavior can return promptly.
func (e *Emitter) Emit(r findings.Record) bool {
	return e.stream.send(r)
}

// Finding emits a [findings.KindFinding] record for the given module with the
// given detail payload. It is a convenience wrapper around [Emitter.Emit] so
// the common case reads cleanly. detail is serialized as-is into the
// finding's Detail field.
func (e *Emitter) Finding(module string, detail json.RawMessage) bool {
	r := findings.NewRecord(findings.KindFinding)
	r.Attack = e.stream.attackName()
	r.Mode = e.stream.attackMode()
	r.Finding = &findings.Finding{Module: module, Detail: detail}
	return e.Emit(r)
}

// Progress emits a [findings.KindProgress] record for the given phase. It is
// a convenience wrapper around [Emitter.Emit].
func (e *Emitter) Progress(phase, detail string) bool {
	r := findings.NewRecord(findings.KindProgress)
	r.Attack = e.stream.attackName()
	r.Mode = e.stream.attackMode()
	r.Progress = &findings.Progress{Phase: phase, Detail: detail}
	return e.Emit(r)
}
