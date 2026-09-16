// Package catalog supplies netpen's dispatch metadata and durability classes.
// Registrations in registrations.go are expanded into one row per behavior and
// mode by [Entries]. The generator commits the same rows as [GeneratedEntries].
// Register, Behaviors, and Entries synchronize access to the registry; callers
// own the metadata returned by Behaviors and Entries.
package catalog

import (
	"fmt"
	"slices"
	"sort"
	"sync"
)

// Durability is the four-class safety taxonomy every (attack, mode) pair
// carries. The class lives in the catalog and gates dispatch. Its zero value is
// invalid. Durability values are safe for concurrent reads.
type Durability string

const (
	// NonDestructive changes no device or neighbor state.
	NonDestructive Durability = "non-destructive"
	// TransientDecay is disruptive with no possible restore; the catalog
	// records the decay bound and the run announces it.
	TransientDecay Durability = "transient-decay"
	// TemporaryRestored is disruptive but reversible; the teardown/restore
	// path is armed before the first frame and runs on completion and
	// interrupt.
	TemporaryRestored Durability = "temporary-restored"
	// PermanentDestructive is state that outlives the run; it requires an
	// explicit per-run opt-in before a single frame is emitted.
	PermanentDestructive Durability = "permanent-destructive"
)

// Legs is the interface requirement a behavior declares: whether the
// watch leg is forbidden, optional, or required. Its zero value is invalid.
// Legs values are safe for concurrent reads.
type Legs string

const (
	// AttackOnly means the behavior uses the attack leg only.
	AttackOnly Legs = "attack-only"
	// WatchOptional means the behavior uses the watch leg when present.
	WatchOptional Legs = "watch-optional"
	// WatchRequired means the behavior fails fast without a watch leg
	// (e.g. ghost).
	WatchRequired Legs = "watch-required"
)

// Mode is a flag-gated class/teardown override for a behavior. The base
// (mode-less) behavior and each mode each emit their own catalog row, so
// portsteal's --relay and dtp's keep-trunk mode appear as distinct
// (behavior, mode) pairs. The zero value is invalid for registration. Callers
// must synchronize concurrent mutation of a shared Mode.
type Mode struct {
	// Flag is the mode's flag name without the leading dashes (e.g.
	// "relay", "wipe", "persist", "keep-trunk").
	Flag string
	// Class overrides the behavior's default durability class for this
	// mode.
	Class Durability
	// Teardown overrides the behavior's default teardown descriptor for
	// this mode. Empty means this mode declares no restoration or expiry
	// details; it does not inherit the behavior's descriptor.
	Teardown string
	// Help is the one-line mode help text.
	Help string
}

// Behavior is metadata for one registered behavior. registrations.go passes
// these values to [Register], and the catalog generator emits a row per
// (behavior, mode) pair. Run functions live in the attack packages, which
// expose runner behavior maps without registering catalog metadata. The zero
// value is invalid for registration. Callers must synchronize concurrent
// mutation of a shared Behavior or its slices.
type Behavior struct {
	// Name is the command name, matching the CLI dispatch table
	// (e.g. "arpspoof", "portsteal", "ospf"). It is the dispatch key.
	Name string
	// Protocols lists the protocol families the behavior exercises
	// (e.g. "arp", "dhcp", "ospf"). Used for catalog display.
	Protocols []string
	// Preconditions are the gate inputs the behavior declares:
	// recon evidence ("ra6", "vlans", "vtp-domain") or flag-gated
	// requirements. Empty for unconditional behaviors.
	Preconditions []string
	// Legs is the interface requirement.
	Legs Legs
	// Class is the default durability class for the mode-less (base)
	// behavior.
	Class Durability
	// Teardown describes active restoration or passive state expiry. Empty
	// means the behavior leaves no state to restore or expire.
	Teardown string
	// Help is the one-line help text shown in CLI usage and the catalog.
	Help string
	// Modes are the flag-gated class/teardown overrides. Each mode emits
	// its own catalog row. Empty for behaviors with no modes.
	Modes []Mode
}

// Entry is one row of the generated catalog: one (behavior, mode) pair.
// The base (mode-less) row has an empty Mode. A mode-bearing behavior
// emits a base row plus one row per mode. The zero value does not describe a
// registered behavior. Callers must synchronize concurrent mutation of a shared
// Entry or its slices.
type Entry struct {
	// Name is the behavior's command name.
	Name string
	// Mode is the mode flag (empty for the base row).
	Mode string
	// Protocols are the protocol families the behavior exercises.
	Protocols []string
	// Preconditions are the gate inputs the behavior declares.
	Preconditions []string
	// Legs is the interface requirement.
	Legs Legs
	// Class is the durability class for this (behavior, mode) pair.
	Class Durability
	// Teardown is the teardown descriptor for this pair.
	Teardown string
	// Help is the help text (the mode's help if Mode is non-empty).
	Help string
}

var (
	mu         sync.Mutex
	behaviors  []Behavior
	registered bool
)

// Register adds behavior metadata to the catalog. Register copies b and its
// slices. It returns an error for missing names or help text, unknown classes
// or legs, and modes without flags or help text, and after the first call to
// [Behaviors] or [Entries], because generation requires a fixed registration
// set. Register is safe for concurrent calls; callers must not mutate b's
// slices during the call. [MustRegister] is the init-time companion.
func Register(b Behavior) error {
	mu.Lock()
	defer mu.Unlock()
	if registered {
		return fmt.Errorf("catalog: Register(%q) called after the catalog was read; registrations must precede generation", b.Name)
	}
	if err := validate(b); err != nil {
		return fmt.Errorf("catalog: invalid registration %q: %w", b.Name, err)
	}
	behaviors = append(behaviors, cloneBehavior(b))
	return nil
}

// MustRegister adds behavior metadata to the catalog and panics if [Register]
// returns an error. registrations.go calls it during package initialization
// over compile-time-constant behavior literals, which is the panic's init-time
// warrant: a bad registration fails on the first import of this package, in
// every build, before any behavior runs.
func MustRegister(b Behavior) {
	if err := Register(b); err != nil {
		panic(err)
	}
}

func cloneBehavior(b Behavior) Behavior {
	b.Protocols = slices.Clone(b.Protocols)
	b.Preconditions = slices.Clone(b.Preconditions)
	b.Modes = slices.Clone(b.Modes)
	return b
}

// validate enforces the registration contract: every behavior carries help
// text, the name is non-empty, the class is a known family, and each mode
// declares a flag and help text.
func validate(b Behavior) error {
	if b.Name == "" {
		return fmt.Errorf("name is required")
	}
	if b.Help == "" {
		return fmt.Errorf("help text is required")
	}
	if err := validateClass(b.Class); err != nil {
		return err
	}
	if err := validateLegs(b.Legs); err != nil {
		return err
	}
	for i, m := range b.Modes {
		if m.Flag == "" {
			return fmt.Errorf("mode[%d]: flag is required", i)
		}
		if m.Help == "" {
			return fmt.Errorf("mode[%d]: help text is required", i)
		}
		if err := validateClass(m.Class); err != nil {
			return fmt.Errorf("mode %q: %w", m.Flag, err)
		}
	}
	return nil
}

// validateClass rejects an unrecognized durability class so an unknown
// class cannot silently vanish from listings and gates (the family-guard
// test's unit counterpart).
func validateClass(c Durability) error {
	switch c {
	case NonDestructive, TransientDecay, TemporaryRestored, PermanentDestructive:
		return nil
	default:
		return fmt.Errorf("unknown durability class %q", c)
	}
}

// validateLegs rejects an unrecognized legs value.
func validateLegs(l Legs) error {
	switch l {
	case AttackOnly, WatchOptional, WatchRequired:
		return nil
	default:
		return fmt.Errorf("unknown legs %q", l)
	}
}

// Behaviors returns a name-sorted copy of the registered behaviors, including
// their slices. It is safe for concurrent calls. Calling
// it latches the catalog as registered (further [Register] calls panic),
// which is the contract the generator relies on for byte stability.
func Behaviors() []Behavior {
	mu.Lock()
	defer mu.Unlock()
	registered = true
	out := make([]Behavior, len(behaviors))
	for i, b := range behaviors {
		out[i] = cloneBehavior(b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Entries expands the registered behaviors into one [Entry] per
// (behavior, mode) pair: the base row (empty Mode) plus one row per mode.
// The result is sorted by (Name, Mode) so generation is byte-stable.
// Entries is safe for concurrent calls and returns independently owned slices.
func Entries() []Entry {
	bs := Behaviors()
	var out []Entry
	for _, b := range bs {
		out = append(out, Entry{
			Name:          b.Name,
			Mode:          "",
			Protocols:     append([]string(nil), b.Protocols...),
			Preconditions: append([]string(nil), b.Preconditions...),
			Legs:          b.Legs,
			Class:         b.Class,
			Teardown:      b.Teardown,
			Help:          b.Help,
		})
		for _, m := range b.Modes {
			out = append(out, Entry{
				Name:          b.Name,
				Mode:          m.Flag,
				Protocols:     append([]string(nil), b.Protocols...),
				Preconditions: append([]string(nil), b.Preconditions...),
				Legs:          b.Legs,
				Class:         m.Class,
				Teardown:      m.Teardown,
				Help:          m.Help,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Mode < out[j].Mode
	})
	return out
}
