// Package catalog supplies netpen's dispatch metadata. A host assembles the
// shipped behaviors into a [Catalog] with [New], which validates every
// entry's cost fields and sorts the rows the generator commits. The
// registration table in registrations.go, expanded by [Entries] and committed
// as [GeneratedEntries], is the durability-class view the runner still reads.
package catalog

import (
	"slices"
	"sort"
	"sync"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
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

// Legs is the interface requirement a behavior declares; see [check.Legs].
type Legs = check.Legs

const (
	// AttackOnly means the behavior uses the attack leg only.
	AttackOnly = check.AttackOnly
	// WatchOptional means the behavior uses the watch leg when present.
	WatchOptional = check.WatchOptional
	// WatchRequired means the behavior fails fast without a watch leg
	// (e.g. ghost).
	WatchRequired = check.WatchRequired
)

// registrationMode is a flag-gated class/teardown override for a behavior. The base
// (mode-less) behavior and each mode each emit their own catalog row, so
// portsteal's --relay and dtp's keep-trunk mode appear as distinct
// (behavior, mode) pairs. The zero value is invalid for registration. Callers
// must synchronize concurrent mutation of a shared registrationMode.
type registrationMode struct {
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

// registration is metadata for one registered behavior. registrations.go passes
// these values to [Register], and the catalog generator emits a row per
// (behavior, mode) pair. Run functions live in the attack packages, which
// expose runner behavior maps without registering catalog metadata. The zero
// value is invalid for registration. Callers must synchronize concurrent
// mutation of a shared registration or its slices.
type registration struct {
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
	Modes []registrationMode
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
	mu         sync.Mutex // guards behaviors and registered
	behaviors  []registration
	registered bool
)

// Register adds behavior metadata to the catalog. Register copies b and its
// slices. It returns an error for missing names or help text, unknown classes
// or legs, and modes without flags or help text, and after the first call to
// registrations or [Entries], because generation requires a fixed registration
// set. Register is safe for concurrent calls; callers must not mutate b's
// slices during the call. [MustRegister] is the init-time companion.
func Register(b registration) error {
	mu.Lock()
	defer mu.Unlock()
	if registered {
		return errs.Msgf("catalog: Register(%q) called after the catalog was read; registrations must precede generation", b.Name)
	}
	if err := validate(b); err != nil {
		return errs.Wrapf(err, "catalog: invalid registration %q", b.Name)
	}
	behaviors = append(behaviors, cloneBehavior(b))
	return nil
}

// MustRegister adds behavior metadata to the catalog and panics if [Register]
// returns an error. registrations.go calls it during package initialization
// over compile-time-constant behavior literals, which is the panic's init-time
// warrant: a bad registration fails on the first import of this package, in
// every build, before any behavior runs.
func MustRegister(b registration) {
	if err := Register(b); err != nil {
		panic(err)
	}
}

func cloneBehavior(b registration) registration {
	b.Protocols = slices.Clone(b.Protocols)
	b.Preconditions = slices.Clone(b.Preconditions)
	b.Modes = slices.Clone(b.Modes)
	return b
}

// validate enforces the registration contract: every behavior carries help
// text, the name is non-empty, the class is a known family, and each mode
// declares a flag and help text.
func validate(b registration) error {
	if b.Name == "" {
		return errs.Msg("name is required")
	}
	if b.Help == "" {
		return errs.Msg("help text is required")
	}
	if err := validateClass(b.Class); err != nil {
		return err
	}
	if err := validateLegs(b.Legs); err != nil {
		return err
	}
	for i, m := range b.Modes {
		if m.Flag == "" {
			return errs.Msgf("mode[%d]: flag is required", i)
		}
		if m.Help == "" {
			return errs.Msgf("mode[%d]: help text is required", i)
		}
		if err := validateClass(m.Class); err != nil {
			return errs.Wrapf(err, "mode %q", m.Flag)
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
		return errs.Msgf("unknown durability class %q", c)
	}
}

// validateLegs rejects an unrecognized legs value.
func validateLegs(l Legs) error {
	switch l {
	case AttackOnly, WatchOptional, WatchRequired:
		return nil
	default:
		return errs.Msgf("unknown legs %q", l)
	}
}

// registrations returns a name-sorted copy of the registered behaviors, including
// their slices. It is safe for concurrent calls. Calling
// it latches the catalog as registered (further [Register] calls panic),
// which is the contract the generator relies on for byte stability.
func registrations() []registration {
	mu.Lock()
	defer mu.Unlock()
	registered = true
	out := make([]registration, len(behaviors))
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
	bs := registrations()
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

// Mode is one flag-gated variant of a [Behavior]. It emits its own catalog
// row with its own cost fields, since a mode such as a persistent variant
// costs the network more than the base behavior. The zero value is invalid.
// Callers must synchronize concurrent mutation of a shared Mode.
type Mode struct {
	// Flag is the mode's flag name without the leading dashes.
	Flag string
	// Help is the one-line mode help text.
	Help string
	// Impact is the worst state change this mode leaves.
	Impact check.Impact
	// Restore is how that state goes away.
	Restore check.Restore
	// Affects is whose state changes.
	Affects check.Affects
}

// Behavior is one shipped check: the base [check.Entry], the [check.Run]
// that performs it, and its modes. The embedded Entry's Mode is empty; each
// mode row is derived from Modes. Run is nil only for an Orchestration
// entry. The zero value is invalid. Callers must synchronize concurrent
// mutation of a shared Behavior or its slices.
type Behavior struct {
	check.Entry
	Run   check.Run
	Modes []Mode
}

// Catalog is the validated, sorted set of behaviors a host assembles with
// [New] and hands to the runner. A Catalog is immutable after New and safe
// for concurrent use; the slices its methods return are owned by the
// caller.
type Catalog struct {
	entries   []check.Entry
	behaviors map[string]Behavior
}

// New validates behaviors and builds a [Catalog]. It returns an error for a
// missing name or help text, a non-empty base Mode, an unknown legs,
// impact, restore kind, or affects, a restore bound that is negative or
// set on a kind other than decay, a nil Run on an entry not flagged
// Orchestration, a mode without a flag or help text, and a duplicate
// (name, mode) pair. New copies every slice it keeps.
func New(behaviors ...Behavior) (Catalog, error) {
	c := Catalog{behaviors: make(map[string]Behavior, len(behaviors))}
	seen := make(map[string]bool)
	for _, b := range behaviors {
		if err := validateBehavior(b); err != nil {
			return Catalog{}, errs.Wrapf(err, "catalog: invalid behavior %q", b.Name)
		}
		if _, dup := c.behaviors[b.Name]; dup {
			return Catalog{}, errs.Msgf("catalog: duplicate behavior %q", b.Name)
		}
		b.Protocols = slices.Clone(b.Protocols)
		b.Modes = slices.Clone(b.Modes)
		c.behaviors[b.Name] = b
		for _, e := range rows(b) {
			key := e.Name + "\x00" + e.Mode
			if seen[key] {
				return Catalog{}, errs.Msgf("catalog: duplicate entry %q mode %q", e.Name, e.Mode)
			}
			seen[key] = true
			c.entries = append(c.entries, e)
		}
	}
	sort.Slice(c.entries, func(i, j int) bool {
		if c.entries[i].Name != c.entries[j].Name {
			return c.entries[i].Name < c.entries[j].Name
		}
		return c.entries[i].Mode < c.entries[j].Mode
	})
	return c, nil
}

// rows expands a behavior into its base row and one row per mode. Each row
// owns its Protocols slice so a caller holding an Entry cannot reach the
// behavior's.
func rows(b Behavior) []check.Entry {
	out := make([]check.Entry, 0, 1+len(b.Modes))
	base := b.Entry
	base.Protocols = slices.Clone(b.Protocols)
	out = append(out, base)
	for _, m := range b.Modes {
		row := base
		row.Protocols = slices.Clone(b.Protocols)
		row.Mode = m.Flag
		row.Help = m.Help
		row.Impact = m.Impact
		row.Restore = m.Restore
		row.Affects = m.Affects
		out = append(out, row)
	}
	return out
}

// Entries returns every (behavior, mode) row sorted by (Name, Mode), the
// order the generator commits. The result is owned by the caller.
func (c Catalog) Entries() []check.Entry {
	out := make([]check.Entry, len(c.entries))
	for i, e := range c.entries {
		e.Protocols = slices.Clone(e.Protocols)
		out[i] = e
	}
	return out
}

// Lookup resolves one (name, mode) pair to its row and the behavior's Run.
// ok is false when the catalog has no such pair.
func (c Catalog) Lookup(name, mode string) (entry check.Entry, run check.Run, ok bool) {
	b, ok := c.behaviors[name]
	if !ok {
		return check.Entry{}, nil, false
	}
	for _, e := range c.entries {
		if e.Name == name && e.Mode == mode {
			e.Protocols = slices.Clone(e.Protocols)
			return e, b.Run, true
		}
	}
	return check.Entry{}, nil, false
}

func validateBehavior(b Behavior) error {
	if b.Name == "" {
		return errs.Msg("name is required")
	}
	if b.Help == "" {
		return errs.Msg("help text is required")
	}
	if b.Mode != "" {
		return errs.Msgf("base entry carries mode %q; modes go in Modes", b.Mode)
	}
	if err := validateLegs(b.Legs); err != nil {
		return err
	}
	if err := validateCost(b.Impact, b.Restore, b.Affects); err != nil {
		return err
	}
	if b.Run == nil && !b.Orchestration {
		return errs.Msg("run function is required on a non-orchestration entry")
	}
	for i, m := range b.Modes {
		if m.Flag == "" {
			return errs.Msgf("mode[%d]: flag is required", i)
		}
		if m.Help == "" {
			return errs.Msgf("mode[%d]: help text is required", i)
		}
		if err := validateCost(m.Impact, m.Restore, m.Affects); err != nil {
			return errs.Wrapf(err, "mode %q", m.Flag)
		}
	}
	return nil
}

// validateCost rejects an unknown impact, restore kind, or affects so an
// unknown value cannot vanish from the ceiling gate, and a restore bound
// that no decay could announce.
func validateCost(impact check.Impact, restore check.Restore, affects check.Affects) error {
	switch impact {
	case check.ImpactNone, check.ImpactTemporary, check.ImpactPermanent:
	default:
		return errs.Msgf("unknown impact %q", impact)
	}
	switch restore.Kind {
	case check.RestoreNone, check.RestoreDecay, check.RestoreActive:
	default:
		return errs.Msgf("unknown restore kind %q", restore.Kind)
	}
	if restore.Bound < 0 {
		return errs.Msgf("restore bound %v is negative", restore.Bound)
	}
	if restore.Bound != 0 && restore.Kind != check.RestoreDecay {
		return errs.Msgf("restore bound %v on kind %q; only decay carries a bound", restore.Bound, restore.Kind)
	}
	switch affects {
	case check.AffectsNone, check.AffectsDevice, check.AffectsClients, check.AffectsFabric:
	default:
		return errs.Msgf("unknown affects %q", affects)
	}
	return nil
}
