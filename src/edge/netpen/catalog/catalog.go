// Package catalog supplies netpen's dispatch metadata. A host assembles the
// shipped behaviors into a [Catalog] with [New], which validates every
// entry's cost fields and sorts the rows the generator commits as
// [GeneratedEntries]. The runner gates dispatch on the rows and resolves
// each (name, mode) pair to its behavior through [Catalog.Lookup].
package catalog

import (
	"slices"
	"sort"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/check"
)

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
// that performs it, and its modes. The embedded Entry's Mode is empty. Each
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
// for concurrent use. The slices its methods return belong to the caller.
// The zero value is an empty catalog.
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

// MustNew builds a [Catalog] from behaviors and panics when [New] rejects
// them. It is for a catalog assembled from literals the caller controls,
// where a rejected entry is a programming error every test and build
// reproduces.
func MustNew(behaviors ...Behavior) Catalog {
	c, err := New(behaviors...)
	if err != nil {
		panic(err)
	}
	return c
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
	switch b.Legs {
	case check.AttackOnly, check.WatchOptional, check.WatchRequired:
	default:
		return errs.Msgf("unknown legs %q", b.Legs)
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
