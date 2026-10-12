// Package runner is netpen's embeddable run engine: an options-struct-plus-
// callback library surface that resolves each attack through the host's
// [catalog.Catalog], gates it on the run's impact ceiling, acknowledgments,
// and legs, drives its [check.Run] with a paced leg and a typed target, and
// delivers typed [findings.Record] values, the attack's verdict among them,
// through a bounded stream whose lifecycle follows the Collection Primitives
// contract.
//
// The package follows naabu's embeddable shape (Options + Runner + Run) and
// zgrab2's per-attack context cancellation (a behavior runs under a context
// derived from Run's ctx). The CLI is one host; an embedding FlowSeer agent
// is another and consumes the stream programmatically. The runner owns no
// process: signal handling and exit live in the host.
package runner

import (
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/catalog"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// DefaultObserve is the observation window when [Options.Observe] is not
// positive: how long a behavior with an observable waits for the effect
// before returning resisted.
const DefaultObserve = 3 * time.Second

// Options configures a [Runner]. The zero value describes an empty run
// whose ceiling admits only entries with no impact. AttackLeg is required
// when Attacks is non-empty. Callers must not mutate referenced slices
// after passing Options to [NewRunner].
type Options struct {
	// Catalog resolves each attack to its entry and run function. A name
	// in Attacks the catalog lacks is a dispatch-level failure.
	Catalog catalog.Catalog

	// AttackLeg is the leg attacks send and receive on. Required for any
	// run with a non-empty Attacks list; a nil AttackLeg causes Run to
	// return a coded error before dispatch. The runner wraps it in a
	// pacer when Rate is positive.
	AttackLeg link.Leg

	// WatchLeg is the optional passive observation leg. A behavior
	// declaring [check.WatchRequired] fails fast when WatchLeg is nil:
	// the runtime enforces the requirement uniformly so no behavior
	// re-checks it.
	WatchLeg link.Leg

	// Attacks is the ordered list of (name, mode) pairs to run, resolved
	// through the catalog. The order is preserved: behaviors run in the
	// order listed.
	Attacks []AttackRef

	// Target holds the hosts, addresses, groups, and identities the
	// behaviors act on. A behavior whose required fields are empty is
	// recorded as a skipped verdict.
	Target check.Target

	// MaxImpact is the impact ceiling: an entry whose impact exceeds it
	// is refused with a refusal record and a skipped verdict, and the run
	// continues. The zero value admits only entries with no impact.
	MaxImpact check.Impact

	// Observe is the window a behavior with an observable waits for the
	// effect before returning resisted. Non-positive values use
	// [DefaultObserve].
	Observe time.Duration

	// Rate limits packet emission, in packets per second. Zero means
	// unlimited. The runner enforces it on the attack leg it hands to
	// every behavior.
	Rate int

	// Timeout bounds the whole run. Zero means no timeout. The runner
	// derives a context with this deadline from Run's ctx; a behavior
	// that exceeds it observes ctx.Err() (context.DeadlineExceeded).
	Timeout time.Duration

	// Acknowledged is the per-run opt-in set naming each permanent
	// (attack, mode) pair the operator explicitly accepted. A pair in
	// [Options.Attacks] whose impact is [check.ImpactPermanent] dispatches
	// only when the same (name, mode) appears here; without it the gate
	// refuses before any frame is emitted. The acknowledgment is per pair,
	// not global: accepting "vtp wipe" does not accept "vtp set".
	Acknowledged []AttackRef

	// Orchestrated is the `full`-command safety seam: when true, the
	// runner is running under orchestration, no permanent entry may
	// dispatch even when its pair appears in [Options.Acknowledged], and
	// a behavior with an incomplete target is a skipped verdict the run
	// continues past. A direct single-attack invocation leaves it false
	// and Run returns the target error so the command exits 2.
	Orchestrated bool

	// TeardownBudget is the time allowance for restoring each behavior.
	// Non-positive values use [DefaultTeardownBudget]. Steps must respect
	// their contexts for this allowance to bound completion time.
	TeardownBudget time.Duration
}

// AttackRef is one entry in [Options.Attacks]: a (behavior, mode) pair. Mode
// is empty for the base (mode-less) behavior; a non-empty Mode selects a
// flag-gated variant (e.g. portsteal's "relay").
// An AttackRef can be shared between goroutines if it is not modified.
type AttackRef struct {
	// Name is the behavior's command name, matching the catalog entry
	// and the CLI dispatch table (e.g. "arpspoof", "portsteal").
	Name string

	// Mode is the mode flag without the leading dashes (e.g. "relay",
	// "wipe", "persist"). Empty selects the base behavior.
	Mode string
}
