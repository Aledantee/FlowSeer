package snmp

import (
	"fmt"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Tier classifies a column by how its value evolves on a watched table.
// The Watcher's scheduler consults the per-column Tier to decide when
// to fetch the column: indicator columns drive change detection,
// counters increment continuously and need their own cadence to stay
// wrap-safe, state columns change only when the indicator advances,
// and static columns barely change at all.
//
// The zero value [TierUnknown] is reserved so that an uninitialized
// classification is distinguishable from a deliberately-State column.
// It is not used by mibgen's emitted maps; codegen always emits one of
// the four known tiers.
//
// The set is closed: adding a value requires updating [Tier.String] and
// the convention test.
type Tier uint8

const (
	// TierUnknown is the zero value. mibgen never emits this; callers
	// constructing tier overrides should always select an explicit tier.
	TierUnknown Tier = iota
	// TierCounter classifies a column whose wire type is Counter32 /
	// Counter64. Counters are fetched on their own cadence, independent
	// of indicator advances, so that values are sampled often enough
	// for the underlying counter not to wrap between observations.
	TierCounter
	// TierIndicator classifies a column whose value advances when
	// another column in the same row (or another row in the same table)
	// changes meaningfully. The Watcher uses indicator columns as the
	// cheap "did anything change?" probe.
	TierIndicator
	// TierState classifies a column whose value changes when the
	// table's change indicator advances. State columns are fetched
	// targeted (per-row indicator path) or wholesale (table-scalar
	// indicator path) on indicator advance, and not re-fetched on
	// quiet ticks.
	TierState
	// TierStatic classifies a column whose value almost never changes
	// (interface descriptions, hardware serial numbers). Static columns
	// are fetched on a much longer library-wide cadence, regardless of
	// indicator state. Static is never auto-classified by mibgen; it
	// is an opt-in tier callers select via [WithColumnTier].
	TierStatic
)

// String returns the Tier's identifier in a form suitable for
// diagnostics. Closed enum: every constant has a non-empty mapping.
func (t Tier) String() string {
	switch t {
	case TierUnknown:
		return "Unknown"
	case TierCounter:
		return "Counter"
	case TierIndicator:
		return "Indicator"
	case TierState:
		return "State"
	case TierStatic:
		return "Static"
	}
	return fmt.Sprintf("Tier(%d)", int(t))
}

// ChangeKind classifies a row-level event emitted by a [Watcher].
// The Watcher detects changes by diffing each tick's freshly-decoded
// rows against an internal snapshot, then emits one event per detected
// row change. There is no "Unchanged" value — no-emit is the unchanged
// signal.
//
// The set is closed: adding a value requires updating
// [ChangeKind.String] and the convention test.
type ChangeKind uint8

const (
	// ChangeKindUnknown is the zero value, reserved so that an
	// uninitialized event is distinguishable from a deliberate Added.
	// Watcher-emitted events always carry one of the three real kinds.
	ChangeKindUnknown ChangeKind = iota
	// ChangeKindAdded indicates the row's index appeared in the
	// watched table for the first time since the Watcher's snapshot
	// was built (or since the row was last Removed).
	ChangeKindAdded
	// ChangeKindModified indicates the row's index was already in the
	// snapshot and at least one of the requested column values
	// changed.
	ChangeKindModified
	// ChangeKindRemoved indicates the row's index was in the snapshot
	// but is no longer in the table (detected via index-set comparison
	// on a full walk).
	ChangeKindRemoved
)

// String returns the ChangeKind's identifier in a form suitable for
// diagnostics.
func (k ChangeKind) String() string {
	switch k {
	case ChangeKindUnknown:
		return "Unknown"
	case ChangeKindAdded:
		return "Added"
	case ChangeKindModified:
		return "Modified"
	case ChangeKindRemoved:
		return "Removed"
	}
	return fmt.Sprintf("ChangeKind(%d)", int(k))
}

// ChangeIndicator describes the column or scalar a [Watcher] polls to
// detect that a watched table has changed. Two shapes are first-class:
//
//   - Per-row: an indicator column inside the watched table's row, one
//     value per row. Probed by a single-column walk of the indicator
//     across the table. Constructed via [NewPerRowIndicator].
//   - Scalar: a single scalar OID covering one or more tables. Probed
//     by one [Session.Get]. Constructed via [NewScalarIndicator].
//
// The shape distinction is an internal scheduler concern; callers do
// not need to branch on it. The Watcher's tick loop branches on
// [ChangeIndicator.isPerRow] (unexported).
//
// Equality across consecutive observations is performed on the raw
// VarBind payload bytes, not the decoded Go value. This is a uniform
// rule across TimeTicks-, TimeStamp-, and DateAndTime-typed
// indicators.
type ChangeIndicator struct {
	// perRow is true for [NewPerRowIndicator] outputs, false for
	// [NewScalarIndicator] outputs.
	perRow bool
	// columnOID is set for per-row indicators; the OID of the
	// indicator column within the row.
	columnOID OID
	// tableRoot is set for per-row indicators; the OID of the table
	// the indicator column belongs to.
	tableRoot OID
	// scalarOID is set for scalar indicators; the OID of the scalar
	// the Watcher polls each tick.
	scalarOID OID
	// kind is set for scalar indicators; the wire [Kind] of the scalar
	// (useful for backends that need to differentiate Get response
	// shapes).
	kind Kind
	// tableRoots is set for scalar indicators; the OIDs of the tables
	// the scalar covers. Always at least one entry. May be more than
	// one (ENTITY-MIB's entLastChangeTime covers five tables).
	tableRoots []OID
}

// NewPerRowIndicator constructs a per-row [ChangeIndicator]: a column
// inside a table's row, one value per row index. The indicator is
// probed by a single-column walk of col across the table.
//
// Validation:
//
//   - col must not be nil.
//   - col.OID() must have tableRoot as a prefix (the indicator column
//     lives inside the table's row).
//   - tableRoot must be non-empty.
//
// On failure the returned ChangeIndicator is the zero value.
func NewPerRowIndicator(col AnyColumn, tableRoot OID) (ChangeIndicator, error) {
	if col == nil {
		return ChangeIndicator{}, errs.Msg("column is nil")
	}
	if tableRoot.Len() == 0 {
		return ChangeIndicator{}, errs.Msg("tableRoot is empty")
	}
	colOID := col.OID()
	if !colOID.HasPrefix(tableRoot) || colOID.Len() <= tableRoot.Len() {
		return ChangeIndicator{}, errs.New().
			Attr("column_oid", colOID).Attr("table_root", tableRoot).
			Msg("column OID is not a strict descendant of tableRoot")
	}
	return ChangeIndicator{
		perRow:    true,
		columnOID: colOID.Clone(),
		tableRoot: tableRoot.Clone(),
	}, nil
}

// NewScalarIndicator constructs a scalar [ChangeIndicator]: a single
// scalar OID that the Watcher polls with one Get per tick. The scalar
// covers one or more tables.
//
// Validation:
//
//   - scalarOID must be non-empty.
//   - tableRoots must contain at least one entry; every entry must be
//     non-empty.
//
// On failure the returned ChangeIndicator is the zero value.
func NewScalarIndicator(scalarOID OID, kind Kind, tableRoots []OID) (ChangeIndicator, error) {
	if scalarOID.Len() == 0 {
		return ChangeIndicator{}, errs.Msg("scalarOID is empty")
	}
	if len(tableRoots) == 0 {
		return ChangeIndicator{}, errs.Msg(
			"at least one table root required")
	}
	for i, r := range tableRoots {
		if r.Len() == 0 {
			return ChangeIndicator{}, errs.New().Attr("index", i).
				Msg("tableRoots entry is empty")
		}
	}
	cloned := make([]OID, len(tableRoots))
	for i, r := range tableRoots {
		cloned[i] = r.Clone()
	}
	return ChangeIndicator{
		perRow:     false,
		scalarOID:  scalarOID.Clone(),
		kind:       kind,
		tableRoots: cloned,
	}, nil
}

// MustChangeIndicator is the panicking companion to [NewPerRowIndicator]
// and [NewScalarIndicator], intended for codegen output where the
// arguments are statically known to be valid. A validation failure
// panics with the underlying error — matching the [MustOID] and
// [regexp.MustCompile] pattern (learnings doc).
//
// Usage: pass the result of NewPerRowIndicator or NewScalarIndicator
// directly:
//
//	var IfTableIndicator = snmp.MustChangeIndicator(
//	    snmp.NewPerRowIndicator(IfLastChange, ifTableRoot))
func MustChangeIndicator(ci ChangeIndicator, err error) ChangeIndicator {
	if err != nil {
		panic(err)
	}
	return ci
}

// ColumnOID returns the indicator column's OID for a per-row indicator,
// or the empty OID for a scalar indicator. Callers that branch on the
// indicator's shape should consult both ColumnOID and ScalarOID rather
// than relying on either being non-empty as a shape signal.
func (ci ChangeIndicator) ColumnOID() OID { return ci.columnOID.Clone() }

// ScalarOID returns the indicator scalar's OID for a scalar indicator,
// or the empty OID for a per-row indicator.
func (ci ChangeIndicator) ScalarOID() OID { return ci.scalarOID.Clone() }

// Kind returns the indicator's wire [Kind] for a scalar indicator, or
// [KindUnknown] for a per-row indicator (whose kind is implicit in the
// column's own Kind).
func (ci ChangeIndicator) Kind() Kind { return ci.kind }

// Coverage returns the OIDs of the tables this indicator covers. For
// per-row indicators the slice has exactly one entry (the table the
// indicator column belongs to). For scalar indicators the slice has
// one or more entries depending on how many tables the scalar covers.
// The returned slice is a copy; callers may mutate it freely.
func (ci ChangeIndicator) Coverage() []OID {
	if ci.perRow {
		return []OID{ci.tableRoot.Clone()}
	}
	out := make([]OID, len(ci.tableRoots))
	for i, r := range ci.tableRoots {
		out[i] = r.Clone()
	}
	return out
}

// isPerRow reports whether the indicator is a per-row column shape.
// Unexported — the shape is an internal scheduler concern, not part
// of the public API.
func (ci ChangeIndicator) isPerRow() bool { return ci.perRow }

// isZero reports whether the indicator is the uninitialized zero
// value (no column or scalar set). Unexported helper used by
// [NewWatcher]'s eager validation.
func (ci ChangeIndicator) isZero() bool {
	return !ci.perRow && ci.scalarOID.Len() == 0 && ci.columnOID.Len() == 0
}

// WatchEvent is the value type a [Watcher] yields over its
// [Watcher.Iter] iterator. One WatchEvent corresponds to one detected
// row change.
//
// Prev is non-nil only when the caller set [WithPrevRow] and Kind is
// [ChangeKindModified]. Added events have nil Prev (no prior state to
// capture); Removed events have nil Prev as well — Row carries the
// last-known row state at the index of removal.
//
// Transient per-tick errors are NOT injected into the event stream;
// callers that want to observe them should poll [Watcher.LastTickErr].
// This means every WatchEvent the iterator yields represents a real
// row change with a well-formed Row value — consumers do not need to
// defensively type-check before reading Row fields.
type WatchEvent[Row any] struct {
	// Index is the row's index OID (the suffix under the table root
	// that distinguishes one row from another).
	Index OID
	// Kind is the kind of change detected; one of [ChangeKindAdded],
	// [ChangeKindModified], or [ChangeKindRemoved].
	Kind ChangeKind
	// Row is the row's current value (for Added and Modified) or its
	// last-known value before removal (for Removed).
	Row Row
	// Prev is the row's value before this change; only set when the
	// caller opted in via [WithPrevRow] and Kind is
	// [ChangeKindModified]. Nil otherwise.
	Prev *Row
}

// Logger is the minimal sink a [Watcher] uses for one-time fallback
// transition logs. A nil Logger is treated as a no-op; callers who
// want to capture fallback events should pass a [WithLogger] option
// carrying their own implementation.
//
// The interface is intentionally minimal — Warn is the only level the
// Watcher currently uses. Adding more levels later is additive; the
// existing single-method shape is forward-compatible because a richer
// logger type can simply not implement them.
type Logger interface {
	// Warn records a single warning. The format is fmt.Printf-style;
	// the Watcher invokes it at most once per fallback transition.
	Warn(format string, args ...any)
}

// WatchConfig is the accumulator type [WatchOption] values apply
// against. Callers should not construct a WatchConfig directly — use
// [ApplyWatchOptions] or pass options to [NewWatcher] (or a generated
// per-table Watch method, which forwards to NewWatcher).
//
// Zero values mean "use the library default" except for cadence
// bounds, where validation requires the caller to set them
// explicitly. See each field's documentation for the per-field rule.
//
// The *Set fields make explicit-zero distinguishable from
// no-override — the same shape used by [CallConfig]'s TimeoutSet and
// RetriesSet.
type WatchConfig struct {
	// CadenceMin is the lower bound of the State-tier adaptive
	// cadence. The Watcher's State-tier interval starts at this value
	// on cold-start, resets to it on every indicator advance, and
	// steps toward CadenceMax on every quiet tick.
	CadenceMin time.Duration
	// CadenceMax is the upper bound of the State-tier adaptive
	// cadence. The interval never exceeds CadenceMax / CadenceMin
	// times step ceiling.
	CadenceMax time.Duration
	// CadenceBoundsSet records whether CadenceMin/CadenceMax were set
	// via [WithCadenceBounds]. NewWatcher rejects a config where this
	// is false (cadence bounds are required, not defaulted).
	CadenceBoundsSet bool

	// CadenceStepFactor is the multiplicative step applied to the
	// State-tier interval on each quiet tick. Default 2.0 (doubling).
	// Values <= 1.0 are documented as degenerate (interval never
	// steps); factor < 1.0 is rejected at NewWatcher.
	CadenceStepFactor float64
	// CadenceStepCeiling caps the multiplier (interval never exceeds
	// CadenceMin * CadenceStepCeiling). Default 16. The cap also
	// bounds against CadenceMax — whichever bound is reached first
	// wins. Values <= 0 are rejected.
	CadenceStepCeiling int
	// CadenceStepPolicySet records whether the step policy was set
	// via [WithCadenceStepPolicy]; when false the defaults apply.
	CadenceStepPolicySet bool

	// CounterCadences carries per-column Counter-tier cadence
	// overrides set via [WithCounterCadence]. Keyed by
	// AnyColumn.OID().String() per the OID-string comparability
	// invariant.
	CounterCadences map[string]time.Duration

	// TierOverrides carries per-column Tier overrides set via
	// [WithColumnTier]. Keyed by AnyColumn.OID().String() per the
	// OID-string comparability invariant.
	TierOverrides map[string]Tier

	// TierLookup is the package-supplied per-column tier classifier
	// consulted between TierOverrides and the wire-Kind fallback.
	// Generated Watch methods (mibgen) install the per-package
	// ColumnTier function here; user-supplied [WithColumnTier]
	// entries still win because they are checked first. Returning
	// [TierUnknown] from the lookup yields to the wire-Kind
	// heuristic. Optional; nil is treated as "no package lookup".
	TierLookup func(AnyColumn) Tier

	// PrevRow toggles population of WatchEvent.Prev on Modified
	// events. Default false; the small allocation per Modified event
	// is opt-in.
	PrevRow bool

	// ProbeWindow is the stuck-zero detector threshold: number of
	// consecutive ticks of "indicator unchanged AND at least one
	// Counter column advanced" before the Watcher falls back to
	// unconditional full walks. Default 5. Zero disables the probe;
	// values < 0 are rejected.
	ProbeWindow int
	// ProbeWindowSet records whether ProbeWindow was set via
	// [WithProbeWindow]; when false the default (5) applies.
	ProbeWindowSet bool

	// ForcedWalkInterval is the cadence at which the Watcher
	// unconditionally walks the entire table to catch row removals
	// the indicator did not signal. Default: 4 * CadenceMax (set at
	// NewWatcher time once bounds are known). Zero means "use the
	// derived default"; explicit values must be > 0.
	ForcedWalkInterval    time.Duration
	ForcedWalkIntervalSet bool

	// BulkWalkFallbackThreshold caps the number of chunked Get PDUs
	// the per-row two-phase tick will dispatch before falling back to
	// a single BulkWalk of the State-tier columns. Default 4. Set to
	// [math.MaxInt] to disable the fallback (preserves the strict
	// per-row chunked-Get contract at the cost of more wire traffic on
	// chassis-reload events). Values < 1 are rejected.
	BulkWalkFallbackThreshold    int
	BulkWalkFallbackThresholdSet bool

	// Logger is the sink for one-time fallback transition logs. Nil
	// means "no log emitted". Callers who want logs should set this
	// via [WithLogger].
	Logger Logger

	// configErr captures the first validation error encountered
	// while applying options. The Watcher constructor surfaces this
	// error before any wire IO so invalid combinations are caught at
	// construction time. The field is unexported because it is a
	// transport mechanism, not a caller-settable field; read it via
	// [WatchConfig.ValidationError].
	configErr error
}

// ValidationError returns the first validation error captured while
// applying options to this WatchConfig, or nil if every option
// applied cleanly. [NewWatcher] consults this before any wire IO.
func (c *WatchConfig) ValidationError() error {
	if c == nil {
		return nil
	}
	return c.configErr
}

// recordErr stores err as the WatchConfig's first validation error.
// Subsequent calls do not clobber an earlier error.
func (c *WatchConfig) recordErr(err error) {
	if c.configErr == nil {
		c.configErr = err
	}
}

// tierOverride returns the caller-supplied tier for col, if any. The
// boolean is true when an override was registered. Keyed by
// col.OID().String() per the OID-string comparability invariant; two
// distinct Column values with the same OID resolve to the same
// override slot.
func (c *WatchConfig) tierOverride(col AnyColumn) (Tier, bool) {
	if c == nil || c.TierOverrides == nil || col == nil {
		return TierUnknown, false
	}
	t, ok := c.TierOverrides[col.OID().String()]
	return t, ok
}

// counterCadence returns the caller-supplied Counter-tier cadence for
// col, if any. The boolean is true when an explicit cadence was
// registered. Keyed by col.OID().String().
func (c *WatchConfig) counterCadence(col AnyColumn) (time.Duration, bool) {
	if c == nil || c.CounterCadences == nil || col == nil {
		return 0, false
	}
	d, ok := c.CounterCadences[col.OID().String()]
	return d, ok
}

// WatchOption configures a [Watcher] at construction time. Options
// follow the existing [Option] / [CallOption] functional-options
// pattern: applied in the order passed, last-wins per-field,
// commutative across distinct fields.
//
// Validation is eager: an option that detects an invalid argument
// records the error on the [WatchConfig] via an internal mechanism;
// [NewWatcher] surfaces it before any wire IO.
type WatchOption func(*WatchConfig)

// ApplyWatchOptions builds a [WatchConfig] from opts. It is the
// public counterpart used by code that needs to inspect the resolved
// config without immediately constructing a Watcher.
func ApplyWatchOptions(opts ...WatchOption) *WatchConfig {
	cfg := &WatchConfig{}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// WithCadenceBounds sets the State-tier adaptive cadence's lower and
// upper bounds. The interval starts at lower on cold-start, resets to
// lower on every indicator advance, and steps toward upper on quiet
// ticks per the configured step policy.
//
// Validation: lower > 0; upper >= lower. Bounds are required — the
// Watcher does not pick a default cadence because the right value
// depends on per-device behavior the library cannot infer.
func WithCadenceBounds(lower, upper time.Duration) WatchOption {
	return func(c *WatchConfig) {
		c.CadenceMin = lower
		c.CadenceMax = upper
		c.CadenceBoundsSet = true
		if lower <= 0 {
			c.recordErr(errs.New().Attr("got", lower).
				Msg("min must be > 0"))
			return
		}
		if upper < lower {
			c.recordErr(errs.New().Attr("max", upper).Attr("min", lower).
				Msg("max must be >= min"))
			return
		}
	}
}

// WithCadenceStepPolicy overrides the default State-tier step policy.
// factor is the multiplicative step applied on each quiet tick;
// ceiling is the cap as a multiple of CadenceMin (interval never
// exceeds min * ceiling, additionally bounded by CadenceMax).
//
// Defaults: factor = 2.0 (doubling), ceiling = 16.
//
// Validation: factor >= 1.0 (values < 1 would shrink the interval,
// which is reserved for indicator-advance reset); ceiling > 0.
// factor == 1.0 is accepted but documented as degenerate (the
// interval never steps).
func WithCadenceStepPolicy(factor float64, ceiling int) WatchOption {
	return func(c *WatchConfig) {
		c.CadenceStepFactor = factor
		c.CadenceStepCeiling = ceiling
		c.CadenceStepPolicySet = true
		if factor < 1.0 {
			c.recordErr(errs.New().Attr("factor", factor).
				Msg("factor must be >= 1.0"))
			return
		}
		if ceiling <= 0 {
			c.recordErr(errs.New().Attr("ceiling", ceiling).
				Msg("ceiling must be > 0"))
			return
		}
	}
}

// WithCounterCadence sets the per-Counter-column cadence at which the
// Watcher re-fetches col. Counter columns fire on their own schedule
// independent of indicator state, so the value is sampled often
// enough that the underlying counter does not wrap between
// observations.
//
// Validation: col must not be nil; interval must be > 0.
//
// The cadence is keyed by col.OID().String() — two distinct Column
// values referring to the same OID share the same override slot per
// the OID-string comparability invariant.
func WithCounterCadence(col AnyColumn, interval time.Duration) WatchOption {
	return func(c *WatchConfig) {
		if col == nil {
			c.recordErr(errs.Msg("column is nil"))
			return
		}
		if interval <= 0 {
			c.recordErr(errs.New().Attr("oid", col.OID()).Attr("got", interval).
				Msg("interval must be > 0"))
			return
		}
		if c.CounterCadences == nil {
			c.CounterCadences = make(map[string]time.Duration)
		}
		c.CounterCadences[col.OID().String()] = interval
	}
}

// WithColumnTier overrides the package-supplied [Tier] classification
// for col on this Watcher. The override applies on top of mibgen's
// per-package ColumnTiers map.
//
// Common uses:
//
//   - Pin a heavyweight State column (e.g. ifAlias) as Static so the
//     Watcher does not re-fetch it on every indicator advance.
//   - Treat a Counter column as State on a deployment where wraps are
//     known to be impossible (the column's value is interesting on
//     change, not on schedule).
//
// Validation: col must not be nil; tier must be one of the known
// non-Unknown values. The override is registered immediately; the
// constructor rejects an override whose target column is the table's
// indicator column (the indicator is structurally required to be
// Tier-Indicator and cannot be re-routed).
//
// Keyed by col.OID().String() per the OID-string comparability
// invariant —
// two distinct Column values referring to the same OID share the same
// override slot.
func WithColumnTier(col AnyColumn, tier Tier) WatchOption {
	return func(c *WatchConfig) {
		if col == nil {
			c.recordErr(errs.Msg("column is nil"))
			return
		}
		switch tier {
		case TierCounter, TierIndicator, TierState, TierStatic:
			// valid
		default:
			c.recordErr(errs.New().Attr("tier", tier).
				Msg("tier is not a known classification"))
			return
		}
		if c.TierOverrides == nil {
			c.TierOverrides = make(map[string]Tier)
		}
		c.TierOverrides[col.OID().String()] = tier
	}
}

// WithTierLookup installs a package-supplied per-column tier classifier
// consulted between [WithColumnTier] overrides and the wire-Kind
// fallback. Generated Watch methods (mibgen) use this option to wire
// the per-package ColumnTier function so a fresh Watcher already
// honors the MIB's structural tier classification without the caller
// having to enumerate every column via [WithColumnTier].
//
// The lookup may return [TierUnknown] to defer to the wire-Kind
// heuristic — this lets a generator-supplied table cover only the
// columns it has structural opinions about and leave the rest to the
// runtime default.
//
// User-supplied [WithColumnTier] entries still win because the
// override map is checked first. When multiple WithTierLookup options
// are applied to the same WatchConfig, the last value wins; generated
// code prepends its option to the caller's so caller-side
// WithTierLookup, if any, still overrides the generated lookup.
//
// Validation: a nil lookup is accepted and treated as "clear the
// lookup".
func WithTierLookup(lookup func(AnyColumn) Tier) WatchOption {
	return func(c *WatchConfig) {
		c.TierLookup = lookup
	}
}

// WithPrevRow toggles population of [WatchEvent.Prev] on
// [ChangeKindModified] events. When set, every Modified event
// carries the row's value before the change so consumers can compute
// per-field diffs without their own snapshot.
//
// Added and Removed events ignore the flag — Added has nil Prev (no
// prior state to capture), Removed carries the last-known value in
// Row already.
//
// Default off — the small allocation per Modified event is opt-in.
func WithPrevRow() WatchOption {
	return func(c *WatchConfig) { c.PrevRow = true }
}

// WithProbeWindow sets the stuck-zero detector threshold. When
// the indicator stays unchanged for ticks consecutive ticks despite
// observable Counter-tier movement (proof the device is alive), the
// Watcher transitions to fallback mode and emits one log line.
//
// The window counter resets to zero whenever the State-tier
// adaptive cadence steps in any direction. Once the cadence
// saturates at the ceiling (min(CadenceMax,
// CadenceMin*CadenceStepCeiling)), further "quiet" ticks do not
// trigger a step and therefore do not reset the counter, so the
// worst-case outage-detection latency is bounded by
// ticks * min(CadenceMax, CadenceMin*CadenceStepCeiling)
// wall-clock rather than ticks * CadenceMax * 2^N.
//
// Validation: ticks >= 0. Zero disables the probe (only the
// first-tick indicator exception triggers fallback). Default 5.
//
// The probe requires at least one Counter-tier column to be
// selected; otherwise there is no "observable column movement"
// signal and the probe never fires.
func WithProbeWindow(ticks int) WatchOption {
	return func(c *WatchConfig) {
		c.ProbeWindow = ticks
		c.ProbeWindowSet = true
		if ticks < 0 {
			c.recordErr(errs.New().Attr("ticks", ticks).
				Msg("ticks must be >= 0"))
			return
		}
	}
}

// WithForcedWalkInterval sets the cadence at which the Watcher
// unconditionally walks the entire table to catch row removals the
// indicator did not signal. Applies to both per-row and table-scalar
// indicator paths.
//
// Per-row indicator paths need this because removed rows have no
// indicator to advance (RFC-correct). Scalar paths need it because
// some vendor agents fail to bump the scalar on row deletion despite
// RFC 6933 mandating it.
//
// Default: 4 * CadenceMax (set at NewWatcher time once bounds are
// known).
//
// Validation: interval > 0.
func WithForcedWalkInterval(interval time.Duration) WatchOption {
	return func(c *WatchConfig) {
		c.ForcedWalkInterval = interval
		c.ForcedWalkIntervalSet = true
		if interval <= 0 {
			c.recordErr(errs.New().Attr("interval", interval).
				Msg("interval must be > 0"))
			return
		}
	}
}

// WithBulkWalkFallbackThreshold sets the per-row two-phase tick's
// chunking-vs-BulkWalk threshold. When an indicator advance
// triggers a targeted multi-OID Get that would exceed pdus PDUs
// (after chunking by Session.MaxOIDs), the Watcher abandons the
// chunked-Get path and performs a single BulkWalk of the table's
// State-tier columns instead.
//
// BulkWalk reads each row's columns wholesale, so when this fallback
// fires Counter-tier columns are pulled together with State-tier
// columns — a deliberate violation of the "Counter columns not
// re-fetched in the per-row path" guarantee on chassis-reload
// events. Callers who cannot tolerate Counter double-fetch can set
// this threshold to [math.MaxInt] to disable the fallback entirely.
//
// Default 4. Values < 1 are rejected.
func WithBulkWalkFallbackThreshold(pdus int) WatchOption {
	return func(c *WatchConfig) {
		c.BulkWalkFallbackThreshold = pdus
		c.BulkWalkFallbackThresholdSet = true
		if pdus < 1 {
			c.recordErr(errs.New().Attr("pdus", pdus).
				Msg("pdus must be >= 1"))
			return
		}
	}
}

// WithLogger sets the [Logger] the Watcher uses for one-time fallback
// transition logs. Nil disables logging (the default). Each
// fallback transition emits at most one Warn call; the log fires at
// the false-to-true edge of [Watcher.Fallback].
func WithLogger(logger Logger) WatchOption {
	return func(c *WatchConfig) { c.Logger = logger }
}
