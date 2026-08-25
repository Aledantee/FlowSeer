// Package catalog is netpen's generated attack catalog: the single source
// for dispatch, help text, legs, preconditions, and durability classes.
// The registration table in registrations.go registers behavior metadata
// via [Register]; a generator ([gen.go]) emits the committed catalog as Go source
// (zz_generated_catalog.go), and the cross-check, family-guard, oracle,
// validator, and CLI-reconciliation tests pin the seams so the catalog
// cannot drift from registrations or from the CLI dispatch table.
//
// Metadata registration is centralized in registrations.go. Attack packages
// expose runner behavior maps and do not call [Register]; the generated catalog
// is the data view of the centralized metadata table.
package catalog

import (
	"fmt"
	"sort"
	"sync"
)

// Durability is the four-class safety taxonomy every (attack, mode) pair
// carries. The class lives in the catalog and gates dispatch.
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
// watch leg is forbidden, optional, or required.
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
// (behavior, mode) pairs.
type Mode struct {
	// Flag is the mode's flag name without the leading dashes (e.g.
	// "relay", "wipe", "persist", "keep-trunk").
	Flag string
	// Class overrides the behavior's default durability class for this
	// mode.
	Class Durability
	// Teardown overrides the behavior's default teardown descriptor for
	// this mode. Empty means the class's default (no teardown for
	// transient/non-destructive classes).
	Teardown string
	// Help is the one-line mode help text.
	Help string
}

// Behavior is metadata for one registered behavior. registrations.go passes
// these values to [Register], and the catalog generator emits a row per
// (behavior, mode) pair. Run functions live in the attack packages, which
// expose runner behavior maps without registering catalog metadata.
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
	// Teardown is the default teardown descriptor. Empty for classes
	// that carry no teardown (non-destructive, transient-decay).
	Teardown string
	// Help is the one-line help text shown in CLI usage and the catalog.
	Help string
	// Modes are the flag-gated class/teardown overrides. Each mode emits
	// its own catalog row. Empty for behaviors with no modes.
	Modes []Mode
}

// Entry is one row of the generated catalog: one (behavior, mode) pair.
// The base (mode-less) row has an empty Mode. A mode-bearing behavior
// emits a base row plus one row per mode.
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

// Register adds behavior metadata to the catalog. registrations.go calls it
// during package initialization; attack packages expose runner behavior maps
// and do not register catalog metadata. Register validates the registration:
// a behavior missing help text or preconditions (where the class requires
// them) is rejected with a panic, so a bad registration fails at init rather
// than silently producing a malformed catalog row.
// Register must not be called after [Behaviors] or [Entries] has been read;
// the catalog is append-only until generation.
func Register(b Behavior) {
	mu.Lock()
	defer mu.Unlock()
	if registered {
		panic(fmt.Sprintf("catalog: Register(%q) called after the catalog was read; registrations must precede generation", b.Name))
	}
	if err := validate(b); err != nil {
		panic(fmt.Sprintf("catalog: invalid registration %q: %v", b.Name, err))
	}
	behaviors = append(behaviors, b)
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

// Behaviors returns a sorted copy of the registered behaviors. Calling
// it latches the catalog as registered (further [Register] calls panic),
// which is the contract the generator relies on for byte stability.
func Behaviors() []Behavior {
	mu.Lock()
	defer mu.Unlock()
	registered = true
	out := make([]Behavior, len(behaviors))
	copy(out, behaviors)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Entries expands the registered behaviors into one [Entry] per
// (behavior, mode) pair: the base row (empty Mode) plus one row per mode.
// The result is sorted by (Name, Mode) so generation is byte-stable.
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
