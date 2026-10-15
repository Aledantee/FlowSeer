// Package check is the contract between netpen's runner and its behaviors.
// A behavior is a [Run] over [Deps] that returns a [Verdict]. The catalog
// describes each behavior and mode as an [Entry] whose [Impact], [Restore],
// and [Affects] state what the check costs the network. The runner gates
// dispatch on those fields. [Target.Need] checks the form of what a behavior
// reads before its first frame, and [First] and [Collect] bound what it
// observes by the run's window. The package imports nothing under netpen
// beyond findings and link, so the catalog, the runner, and every behavior
// package can share it.
package check

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/link"
)

// Impact is the worst state change a check leaves on the network. The runner
// refuses an entry whose impact exceeds the run's ceiling. The zero value is
// invalid. Impact values are safe for concurrent reads.
type Impact string

const (
	// ImpactNone changes no device, client, or fabric state.
	ImpactNone Impact = "none"
	// ImpactTemporary changes state that decays or is restored by the run.
	ImpactTemporary Impact = "temporary"
	// ImpactPermanent changes state that outlives the run.
	ImpactPermanent Impact = "permanent"
)

// Level orders impacts for the ceiling comparison: none below temporary
// below permanent. An unknown impact orders above permanent so it can never
// pass a ceiling.
func (i Impact) Level() int {
	switch i {
	case ImpactNone:
		return 0
	case ImpactTemporary:
		return 1
	case ImpactPermanent:
		return 2
	}
	return 3
}

// RestoreKind names how the state a check changes goes away. The zero value
// is invalid. RestoreKind values are safe for concurrent reads.
type RestoreKind string

const (
	// RestoreNone means the check leaves nothing to restore or expire.
	RestoreNone RestoreKind = "none"
	// RestoreDecay means the state expires on its own. The run announces
	// the bound when one is known.
	RestoreDecay RestoreKind = "decay"
	// RestoreActive means the run restores the state itself through the
	// teardown steps the behavior arms.
	RestoreActive RestoreKind = "active"
)

// Restore describes the mechanism by which a check's state goes away. Bound
// is the decay lifetime the probe itself announces, or zero when no source
// states one. Bound only means something with [RestoreDecay]. Restore values
// are safe for concurrent reads.
type Restore struct {
	Kind  RestoreKind
	Bound time.Duration
}

// Affects names whose state a check changes. The zero value is invalid.
// Affects values are safe for concurrent reads.
type Affects string

const (
	// AffectsNone changes nobody's state.
	AffectsNone Affects = "none"
	// AffectsDevice changes state on the switch or router under test.
	AffectsDevice Affects = "device"
	// AffectsClients changes state on hosts attached to the segment.
	AffectsClients Affects = "clients"
	// AffectsFabric changes state across the switching or routing domain.
	AffectsFabric Affects = "fabric"
)

// Legs is the interface requirement a behavior declares: whether the watch
// leg is unused, optional, or required. The zero value is invalid. Legs
// values are safe for concurrent reads.
type Legs string

const (
	// AttackOnly means the behavior uses the attack leg only.
	AttackOnly Legs = "attack-only"
	// WatchOptional means the behavior uses the watch leg when present.
	WatchOptional Legs = "watch-optional"
	// WatchRequired means the behavior fails fast without a watch leg.
	WatchRequired Legs = "watch-required"
)

// Entry is one row of the catalog: one (behavior, mode) pair with its
// dispatch metadata and cost fields. Mode is empty for the base row. An
// Orchestration entry scripts other behaviors and has no Run of its own.
// The zero value describes no behavior. Callers must synchronize concurrent
// mutation of a shared Entry or its slices.
type Entry struct {
	// Name is the behavior's command name, the dispatch key.
	Name string
	// Mode is the mode flag without the leading dashes. The base row has an
	// empty Mode.
	Mode string
	// Protocols lists the protocol families the behavior exercises.
	Protocols []string
	// Legs is the interface requirement.
	Legs Legs
	// Impact is the worst state change this pair leaves.
	Impact Impact
	// Restore is how that state goes away.
	Restore Restore
	// Affects is whose state changes.
	Affects Affects
	// Help is the one-line help text.
	Help string
	// Orchestration marks an entry that scripts other behaviors and
	// carries no Run of its own.
	Orchestration bool
}

// Verdict is what a [Run] returns: the kind, a detail a person can read,
// and how long the behavior observed before deciding. The runner emits it
// as the attack's [findings.KindVerdict] record. Verdict values are safe
// for concurrent reads.
type Verdict struct {
	Kind     findings.VerdictKind
	Detail   string
	Observed time.Duration
}

// Pending is the verdict of a check with no observable yet. detail names
// the observable it lacks.
func Pending(detail string) Verdict {
	return Verdict{Kind: findings.VerdictPending, Detail: detail}
}

// Confirmed is the verdict of a check whose observable showed the attack
// taking effect within observed.
func Confirmed(detail string, observed time.Duration) Verdict {
	return Verdict{Kind: findings.VerdictConfirmed, Detail: detail, Observed: observed}
}

// Resisted is the verdict of a check whose observable stayed quiet for the
// whole of observed.
func Resisted(detail string, observed time.Duration) Verdict {
	return Verdict{Kind: findings.VerdictResisted, Detail: detail, Observed: observed}
}

// Emitter is the typed sink a behavior writes records through. The runner
// supplies one per attack that stamps the attack and mode. An Emitter
// supports one goroutine, the behavior's own. A behavior that spawns
// workers serializes its calls itself.
type Emitter interface {
	// Emit delivers one record and reports false when the run is
	// terminating so the behavior can return promptly.
	Emit(r findings.Record) bool
	// Finding emits a [findings.KindFinding] record for module with the
	// given detail payload, which the caller has already redacted.
	Finding(module string, detail json.RawMessage) bool
	// Progress emits a [findings.KindProgress] record for phase.
	Progress(phase, detail string) bool
}

// Step is one restore action a behavior arms. It runs under a context
// bounded by the run's teardown budget and returns promptly on
// cancellation.
type Step func(ctx context.Context) error

// Teardown is the arming handle a behavior with [RestoreActive] registers
// its restore steps against before its first frame. The runner runs the
// armed steps on completion or interrupt. Implementations are safe for
// concurrent calls.
type Teardown interface {
	// Arm registers one named restore step. Steps run in arm order,
	// least-dependent state first, each in its own error scope.
	Arm(name string, fn Step)
}

// Deps supplies a [Run] with its resolved entry, target, legs, and handles.
// Treat the fields as immutable. Each handle documents its own concurrency
// contract.
type Deps struct {
	// AttackLeg is the leg the behavior sends and receives on, never nil.
	// The runner paces its sends to the run's rate.
	AttackLeg link.Leg
	// WatchLeg is the passive observation leg, nil when the run has none.
	// A [WatchRequired] behavior is never invoked without one.
	WatchLeg link.Leg
	// Target holds the addresses and identities the behavior reads. The
	// behavior calls [Target.Need] for the fields it cannot run without.
	Target Target
	// Entry is the resolved catalog row for this (behavior, mode) pair.
	Entry Entry
	// Observe is the window a behavior with an observable waits for the
	// effect before returning [Resisted].
	Observe time.Duration
	// Emitter is the typed sink for findings and progress, never nil.
	Emitter Emitter
	// Teardown is the arming handle, non-nil only when Entry.Restore.Kind
	// is [RestoreActive].
	Teardown Teardown
	// Listen opens a listening socket for a behavior that serves clients;
	// the runner defaults it to net.Listen and a test passes an in-memory
	// listener.
	Listen func(ctx context.Context, network, address string) (net.Listener, error)
}

// Run is one check. It reads its target from deps, sends through the legs,
// and returns the verdict. A non-nil error means the behavior could not
// decide. The runner then records a [findings.VerdictPending] verdict
// carrying the error text and continues with the next attack. The context
// is derived from the run's context, so a behavior returns promptly on
// ctx.Done().
type Run func(ctx context.Context, deps Deps) (Verdict, error)
