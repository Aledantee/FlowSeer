package snmp

import (
	"bytes"
	"context"
	"errors"
	"iter"
	"strconv"
	"sync/atomic"
	"time"

	"go.aledante.io/ae"
)

// defaultWatchEventBuffer is the buffer size [NewWatcher] uses for its
// embedded pump's data channel. Sized large enough to absorb a single
// indicator-advance burst against a mid-sized table (e.g., a 500-row
// ifTable on chassis reload) without forcing the tick goroutine to
// block on a slow consumer; small enough that a stalled consumer does
// not pin large amounts of memory.
const defaultWatchEventBuffer = 64

// watcherDefaultMaxOIDs is the per-PDU OID cap the Watcher uses when
// chunking the per-row two-phase tick's multi-OID Get. Matches
// gosnmp's default. A future revision may surface a per-Session
// effective value through the [Session] interface; until then the
// constant is the single source of truth.
const watcherDefaultMaxOIDs = 60

// Watcher is a long-lived, indicator-gated per-table change stream
// over a [Session]. It is the streaming counterpart to [Walker]:
// where Walker emits one-shot (OID, VarBind) pairs from a single
// subtree walk, Watcher emits typed [WatchEvent] values whenever a
// watched table's rows change.
//
// The lifecycle mirrors Walker's: a constructor spawns the producer
// goroutine, a range-over-func iterator surfaces events, [Watcher.Err]
// reports the terminal cause after iteration ends, and [Watcher.Close]
// terminates the producer idempotently.
//
// The zero value is not usable; construct a Watcher via [NewWatcher],
// or use a generated per-table Watch method.
//
// # Operational contract
//
// On construction the Watcher walks the watched table once (cold
// start) and emits one [ChangeKindAdded] event per row. From that
// point the tick goroutine probes the configured [ChangeIndicator]
// at the State-tier cadence and emits events only when a row's value
// has changed. Quiet ticks do not produce events.
//
// # Error semantics
//
// Terminal errors ([ErrSessionClosed], context cancellation) latch
// via the embedded pump and surface through [Watcher.Err]. Transient
// per-tick errors (a single Get times out, a single VarBind fails to
// decode) do NOT terminate the Watcher; they are scratch-recorded
// via [Watcher.LastTickErr] for callers who want to observe them.
// The event stream is not polluted with synthetic error-bearing
// events — every yielded [WatchEvent] represents a real row change.
//
// # Synchronization model
//
// Watcher embeds a generic [*pump] of [WatchEvent[Row]] which owns
// the cross-cutting channel pump (data channel, stop signal,
// send/close RWMutex, derived context, terminal-error latch).
// Watcher-specific state — the row snapshot, the cached effective
// tier map, the fallback flag, the last-tick error — lives on
// Watcher itself.
type Watcher[Row any] struct {
	*pump[WatchEvent[Row]]

	// cfg captures the caller-supplied options after validation. The
	// scheduler reads cadence bounds and tier overrides from this;
	// the fallback path reads the probe window and logger.
	cfg *WatchConfig

	// sess is the SNMP session the tick goroutine drives. The
	// session is not closed by the Watcher; the caller owns its
	// lifecycle.
	sess Session

	// indicator describes which OID(s) the tick goroutine probes
	// each tick. See [ChangeIndicator].
	indicator ChangeIndicator

	// tableRoot is the table the Watcher walks on cold start and
	// on forced full walks. For per-row indicators this is the
	// indicator's tableRoot; for scalar indicators with a single
	// coverage table this is that table; for scalar indicators with
	// multiple coverage tables the caller must select one via cols.
	tableRoot OID

	// cols is the caller-supplied selection of columns to report on
	// each event. Stored for tier-driven scheduling; the cold-start
	// path walks the entire tableRoot and the decode function picks
	// out the relevant VarBinds.
	cols []AnyColumn

	// decode produces a Row value from a row's VarBinds. Supplied
	// by the caller (or by the generated per-table Watch wrapper).
	// Invoked once per detected row change with the row's index OID
	// and the VarBinds collected for that row during the most-recent
	// walk or targeted Get.
	decode func(OID, []VarBind) (Row, error)

	// equal compares two Row values for the diff logic that drives
	// ChangeKindModified. Required because Row is `any` (not
	// `comparable`) and generated row structs contain slice fields
	// where `==` does not compile.
	equal func(a, b Row) bool

	// merge updates an existing Row's fields with the values decoded
	// from a partial-column fetch (Counter-tier, Static-tier). Fields
	// whose columns are not present in the supplied VarBinds are left
	// at their previous value, preserving cold-start's full row across
	// partial-fetch ticks. Required because the snapshot is the only
	// source of truth for full-row state on every event, including
	// events produced by a tier that only fetches a subset of the
	// table's columns.
	//
	// Generated Watch wrappers supply a per-table merge function
	// emitted by mibgen (mergeIfTableRow, etc.). Callers using
	// NewWatcher directly may delegate to their decode function
	// (re-decode + field copy) when partial-fetch semantics do not
	// matter — see the test helpers in this package for the coarse
	// shape.
	merge func(dst *Row, vbs []VarBind)

	// snapshot is the current-state-only row map keyed by
	// OID.String() of the row index: the comparable key is the
	// dotted-string form, not the OID value itself.
	// Owned by the tick goroutine; not concurrent-safe (the
	// goroutine is its single mutator).
	//
	// Each entry carries the decoded Row plus the row's last-observed
	// indicator VarBind (for per-row indicators). The indicator field
	// is unused on scalar-indicator Watchers; the watcher-level
	// scalarIndicatorVB tracks that case instead.
	snapshot map[string]snapshotEntry[Row]

	// scalarIndicatorVB tracks the last-observed scalar indicator
	// value for scalar-indicator Watchers. Set by the first
	// scalar-tick observation (or by cold-start's trailing scalar
	// Get) and updated whenever the scalar advances. Unused for
	// per-row Watchers.
	scalarIndicatorVB VarBind

	// stateTierCols, counterTierCols, staticTierCols partition w.cols
	// by their resolved tier — package-supplied with
	// cfg.tierOverride applied on top. Resolved once at
	// NewWatcher and cached so per-tick work does not re-resolve.
	// Indicator-tier columns are excluded from all three partitions
	// (the indicator path handles them directly).
	//
	// The cached partitions are stable for the Watcher's lifetime.
	stateTierCols   []AnyColumn
	counterTierCols []AnyColumn
	staticTierCols  []AnyColumn

	// lastForcedWalk tracks the wall-clock time of the last forced
	// full-walk tick (the row-removal backstop). Set after
	// cold-start completes; reset after each forced walk.
	lastForcedWalk time.Time

	// stateInterval is the current State-tier adaptive cadence value,
	// stored as nanoseconds in an atomic.Int64 so internal
	// tests can read it without racing the tick goroutine's writes.
	// Starts at cfg.CadenceMin, resets to CadenceMin on every
	// indicator advance, and steps toward CadenceMax on quiet ticks
	// per cfg.CadenceStepFactor and cfg.CadenceStepCeiling.
	stateInterval atomic.Int64

	// nextStateTickAt is the wall-clock time the next State-tier
	// (indicator-gated) tick should fire. Updated after each
	// State-tier tick using stateInterval.
	nextStateTickAt time.Time

	// counterSchedules carries per-Counter-tier-column timer state.
	// Each entry's nextDueAt advances by the column's configured
	// interval each time the column is fetched. Independent of
	// State-tier and Static-tier schedules.
	counterSchedules []counterSchedule

	// nextStaticTickAt is the wall-clock time of the next
	// Static-tier batch fetch. Static columns share a single
	// library-wide cadence of 8 × CadenceMax.
	nextStaticTickAt time.Time

	// quietWithMovementCount tracks consecutive State-tier ticks
	// where the indicator did NOT advance AND at least one
	// Counter-tier emit was observed since the previous tick (the
	// stuck-zero probe). When the counter reaches cfg.ProbeWindow,
	// the Watcher transitions to fallback mode. Owned by the tick
	// goroutine; not concurrent-safe.
	quietWithMovementCount int

	// counterEventsSinceLastState counts Counter-tier emits since
	// the last State-tier tick. Incremented by the Counter-tier
	// path; the State-tier tick reads then resets it. Atomic so
	// the read/reset paired uniformly with the rest of the
	// transient-state fields and so any future move of Counter-tier
	// work to a separate goroutine stays race-free.
	counterEventsSinceLastState atomic.Int64

	// fallback reports whether the Watcher has transitioned to
	// fallback mode. Monotonic in first ship: once true, stays true
	// until Close. The indicator-exception probe and the stuck-zero
	// probe are the two transition triggers.
	fallback atomic.Bool

	// lastTickErr captures the most-recent transient per-tick
	// error. Reset to nil at each tick boundary.
	lastTickErr atomic.Pointer[error]
}

// NewWatcher constructs a Watcher tied to ctx and sess that observes
// the table identified by indicator. cols selects the columns whose
// values are reported in each emitted event. decode produces a Row
// value from a row's VarBinds; equal compares two Rows for the diff
// logic that drives [ChangeKindModified] events; merge updates an
// existing Row's fields with the values decoded from a partial-column
// fetch (Counter-tier, Static-tier) without destroying fields from
// columns not present in that tier's fetch. opts override cadence
// bounds, per-column tier classifications, fallback behavior, and
// other policy.
//
// On success NewWatcher spawns the tick goroutine before returning.
// On validation failure NewWatcher returns a non-nil Watcher with the
// error pre-latched via the embedded pump's terminal-error path AND
// returns the same error as the second value. No goroutine is
// spawned; the returned Watcher's [Watcher.Iter] terminates
// immediately and [Watcher.Err] surfaces the failure. Generated
// per-table Watch wrappers intentionally discard the error and
// rely on the latched-Err path so the caller's range loop terminates
// cleanly without a nil-check.
//
// Validation:
//
//   - cfg.CadenceBoundsSet must be true ([WithCadenceBounds] called).
//   - indicator must be a real value built via [NewPerRowIndicator]
//     or [NewScalarIndicator].
//   - decode, equal, and merge must be non-nil.
//   - cols may be empty (cold-start still walks the table; events
//     carry whatever Row the decode function produces).
//   - For scalar indicators covering multiple tables, the Watcher
//     must be able to derive a single table root from cols. The
//     caller — or the generated per-table Watch wrapper — supplies
//     cols belonging to exactly one of the indicator's coverage
//     tables.
//
// The caller's ctx is not stored on the Session; the Watcher's
// derived ctx (owned by the embedded pump) is the cancellation
// boundary for all in-flight wire IO.
func NewWatcher[Row any](
	ctx context.Context,
	sess Session,
	indicator ChangeIndicator,
	cols []AnyColumn,
	decode func(OID, []VarBind) (Row, error),
	equal func(a, b Row) bool,
	merge func(dst *Row, vbs []VarBind),
	opts ...WatchOption,
) (*Watcher[Row], error) {
	// Pre-allocate a Watcher so every error path can return a
	// non-nil value with the error latched on the pump.
	failedWatcher := func(err error) (*Watcher[Row], error) {
		w := &Watcher[Row]{
			pump: newPump[WatchEvent[Row]](ctx, defaultWatchEventBuffer),
		}
		w.fail(err)
		return w, err
	}

	if sess == nil {
		return failedWatcher(ae.Msg("session is nil"))
	}
	if indicator.isZero() {
		return failedWatcher(ae.Msg("indicator is the zero value; " +
			"use NewPerRowIndicator or NewScalarIndicator"))
	}
	if decode == nil {
		return failedWatcher(ae.Msg("decode is nil"))
	}
	if equal == nil {
		return failedWatcher(ae.Msg("equal is nil"))
	}
	if merge == nil {
		return failedWatcher(ae.Msg("merge is nil"))
	}

	cfg := ApplyWatchOptions(opts...)
	if err := cfg.ValidationError(); err != nil {
		return failedWatcher(err)
	}
	if !cfg.CadenceBoundsSet {
		return failedWatcher(ae.Msg("WithCadenceBounds is required " +
			"(cadence has no library default)"))
	}

	// Apply step-policy defaults when the caller did not set them
	// explicitly, using conservative starting values.
	if !cfg.CadenceStepPolicySet {
		cfg.CadenceStepFactor = 2.0
		cfg.CadenceStepCeiling = 16
	}
	// Conservative probe-window default.
	if !cfg.ProbeWindowSet {
		cfg.ProbeWindow = 5
	}
	// Forced-walk default: 4 × CadenceMax once bounds are known.
	if !cfg.ForcedWalkIntervalSet {
		cfg.ForcedWalkInterval = 4 * cfg.CadenceMax
	}
	// BulkWalk fallback threshold default.
	if !cfg.BulkWalkFallbackThresholdSet {
		cfg.BulkWalkFallbackThreshold = 4
	}

	tableRoot, err := deriveTableRoot(indicator, cols)
	if err != nil {
		return failedWatcher(err)
	}

	// Reject overrides whose target column is the indicator column —
	// the indicator is structurally Tier-Indicator and cannot be
	// re-routed (would double-fetch via both the indicator probe and
	// the State-tier targeted Get).
	if indicator.isPerRow() {
		indicatorKey := indicator.columnOID.String()
		if _, set := cfg.TierOverrides[indicatorKey]; set {
			return failedWatcher(ae.New().Attr("column_oid", indicatorKey).
				Msg("cannot override tier of indicator column; " +
					"the indicator is structurally Tier-Indicator and " +
					"cannot be re-routed"))
		}
	}

	w := &Watcher[Row]{
		pump:      newPump[WatchEvent[Row]](ctx, defaultWatchEventBuffer),
		cfg:       cfg,
		sess:      sess,
		indicator: indicator,
		tableRoot: tableRoot,
		cols:      append([]AnyColumn(nil), cols...),
		decode:    decode,
		equal:     equal,
		merge:     merge,
		snapshot:  make(map[string]snapshotEntry[Row]),
	}
	w.partitionCols()

	go w.run()
	return w, nil
}

// snapshotEntry pairs a row's decoded Row value with the last-observed
// indicator VarBind for that row (per-row indicator path). The
// indicator field is the only piece of internal state per row beyond
// the Row itself; the snapshot is current-state-only, so there is
// no history queue.
type snapshotEntry[Row any] struct {
	row         Row
	indicatorVB VarBind // only populated for per-row indicators
}

// counterSchedule tracks a single Counter-tier column's independent
// schedule. The interval is taken from cfg.CounterCadences (via
// [WithCounterCadence]) or a wire-kind default if the caller did not
// override.
type counterSchedule struct {
	col       AnyColumn
	interval  time.Duration
	nextDueAt time.Time
}

// counter32DefaultCadence is the library default Counter32 cadence
// when the caller did not pass [WithCounterCadence] for the column.
// Sized for the 1 Gbps-wraps-at-~34s case in RFC 2233's deprecation
// rationale: 25 s leaves headroom for one re-read across a wrap on
// 1 Gbps-class interfaces, while keeping wire cost modest.
const counter32DefaultCadence = 25 * time.Second

// counter64DefaultCadence is the library default Counter64 cadence.
// Counter64 effectively never wraps at any realistic line rate
// (~5 800 years at 100 Gbps), so the value is set conservatively for
// data freshness rather than wrap-safety.
const counter64DefaultCadence = 1 * time.Hour

// staticTierCadenceMultiplier multiplies cfg.CadenceMax to produce
// the library-wide Static-tier cadence. Static columns are
// almost-never-changing; 8 × CadenceMax means a Static column is
// observed at roughly an order of magnitude less often than a
// quiet State-tier column at its slowest cadence.
const staticTierCadenceMultiplier = 8

// partitionCols splits w.cols into stateTierCols, counterTierCols,
// and staticTierCols by calling [Watcher.classifyCol] on each
// column. Resolution order (see classifyCol): caller override via
// [WithColumnTier], then the package-supplied [WithTierLookup]
// callback (generated Watch methods wire mibgen's per-package
// ColumnTier function here), then the wire-Kind heuristic
// (Counter32 / Counter64 → TierCounter), then TierState as the
// default.
//
// partitionCols also builds the per-Counter-tier-column schedule
// entries (counterSchedules). Counter-tier cadence is resolved from
// cfg.CounterCadences first, then a wire-kind default.
func (w *Watcher[Row]) partitionCols() {
	for _, c := range w.cols {
		if c == nil {
			continue
		}
		key := c.OID().String()
		// Drop the indicator column from the partitions — the
		// indicator path probes it directly each tick; including it
		// here would double-fetch via the State-tier targeted Get.
		if w.indicator.isPerRow() &&
			w.indicator.columnOID.String() == key {
			continue
		}
		tier := w.classifyCol(c)
		switch tier {
		case TierCounter:
			w.counterTierCols = append(w.counterTierCols, c)
			interval, ok := w.cfg.counterCadence(c)
			if !ok {
				interval = w.counterDefaultCadence(c)
			}
			w.counterSchedules = append(w.counterSchedules,
				counterSchedule{col: c, interval: interval})
		case TierStatic:
			w.staticTierCols = append(w.staticTierCols, c)
		default:
			// TierState / TierUnknown / TierIndicator (latter is
			// already filtered out above) → State.
			w.stateTierCols = append(w.stateTierCols, c)
		}
	}
}

// counterDefaultCadence returns the wire-kind-driven default cadence
// for a Counter-tier column when the caller did not pass an explicit
// [WithCounterCadence] override.
func (w *Watcher[Row]) counterDefaultCadence(c AnyColumn) time.Duration {
	switch c.Kind() {
	case KindCounter64:
		return counter64DefaultCadence
	default:
		return counter32DefaultCadence
	}
}

// classifyCol resolves the effective tier for c. Resolution order:
//
//  1. Caller override via [WithColumnTier]. Always wins.
//  2. Package-supplied lookup installed via [WithTierLookup] (the
//     generated Watch method wires the per-package ColumnTier
//     function here). A TierUnknown return defers to the next step.
//  3. Wire-Kind heuristic: Counter32 / Counter64 → TierCounter.
//  4. Default: TierState.
func (w *Watcher[Row]) classifyCol(c AnyColumn) Tier {
	if t, ok := w.cfg.tierOverride(c); ok {
		return t
	}
	if w.cfg.TierLookup != nil {
		if t := w.cfg.TierLookup(c); t != TierUnknown {
			return t
		}
	}
	switch c.Kind() {
	case KindCounter32, KindCounter64:
		return TierCounter
	}
	return TierState
}

// deriveTableRoot picks the single table OID the Watcher operates
// over.
//
//   - Per-row indicator: the indicator's tableRoot (unambiguous).
//   - Scalar indicator covering exactly one table: that table.
//   - Scalar indicator covering multiple tables: the caller's cols
//     must all share a common table-root prefix that matches one of
//     the coverage tables. This handles the ENTITY-MIB case where
//     entLastChangeTime covers five tables but a single Watcher
//     watches just one.
func deriveTableRoot(indicator ChangeIndicator, cols []AnyColumn) (OID, error) {
	if indicator.isPerRow() {
		return indicator.tableRoot.Clone(), nil
	}
	cov := indicator.tableRoots
	if len(cov) == 1 {
		return cov[0].Clone(), nil
	}
	// Scalar covering multiple tables. Find the (single) coverage
	// table all cols agree on.
	if len(cols) == 0 {
		return OID{}, ae.Msg("scalar indicator covers " +
			"multiple tables; cols must be supplied so the Watcher " +
			"can derive a single table root")
	}
	var match OID
	for _, c := range cols {
		if c == nil {
			return OID{}, ae.Msg("cols contains a nil entry")
		}
		colOID := c.OID()
		var hit OID
		for _, root := range cov {
			if colOID.HasPrefix(root) {
				hit = root
				break
			}
		}
		if hit.Len() == 0 {
			return OID{}, ae.New().Attr("column_oid", colOID).
				Msg("column is not under any coverage table of the indicator")
		}
		if match.Len() == 0 {
			match = hit
		} else if !match.Equal(hit) {
			return OID{}, ae.New().Attr("table_a", match).Attr("table_b", hit).
				Msg("cols span multiple coverage tables; a single Watcher must operate on one table")
		}
	}
	return match.Clone(), nil
}

// Iter returns the range-over-func view of the Watcher's event
// stream. The returned [iter.Seq2] yields (rowIndex, event) pairs
// until iteration ends; the caller should check [Watcher.Err] after
// the loop to distinguish natural completion from a terminal error.
//
// Breaking out of the loop tells the producer to terminate; Iter
// drains any in-flight buffered events so the producer goroutine
// does not block on a full channel. The producer exits within a
// bounded time (one PDU round-trip after the next session call
// observes cancellation).
//
// The yielded OID is identical to [WatchEvent.Index]; surfacing it
// as the first key of the Seq2 mirrors Walker.Iter and keeps the
// common "skip the OID, use the event" call site short.
func (w *Watcher[Row]) Iter() iter.Seq2[OID, WatchEvent[Row]] {
	return func(yield func(OID, WatchEvent[Row]) bool) {
		for ev := range w.ch {
			if !yield(ev.Index, ev) {
				w.signalStop()
				for range w.ch {
				}
				return
			}
		}
	}
}

// Close signals the producer goroutine to terminate early and
// returns nil. After Close, the iterator drains any buffered events
// and then exits; [Watcher.Err] reports whichever terminal cause
// arrives first (nil if Close races the natural completion path).
//
// Close does NOT block waiting for the goroutine to exit — the
// producer shuts down asynchronously within a bounded time (one PDU
// round-trip after the next session call observes cancellation).
//
// Close is idempotent. The Watcher's snapshot, cached tier map, and
// fallback state are released when the Watcher value is
// garbage-collected; in-process restart requires a new Watcher.
func (w *Watcher[Row]) Close() error {
	w.signalStop()
	w.cancel()
	return nil
}

// Fallback reports whether the Watcher has transitioned to fallback
// mode — either because the indicator returned an exception variant
// on the first tick or because the indicator stayed unchanged
// across the probe window despite observable column movement.
//
// Fallback is monotonic in first ship: once true, the Watcher stays
// in fallback mode until Close. Real-device recovery from indicator
// outages requires a new Watcher.
func (w *Watcher[Row]) Fallback() bool {
	return w.fallback.Load()
}

// LastTickErr returns the most-recent transient per-tick error
// observed by the Watcher's tick goroutine, or nil if the latest
// tick completed without a transient error. The accessor is
// non-latching: the value resets to nil at the start of each tick.
//
// Use this method to surface degraded-but-running state to a
// supervising consumer who wants to log per-tick failures without
// being forced to defensively type-check every event in the
// [Watcher.Iter] stream. Terminal errors (session closed, context
// canceled) surface via [Watcher.Err] instead.
func (w *Watcher[Row]) LastTickErr() error {
	p := w.lastTickErr.Load()
	if p == nil {
		return nil
	}
	return *p
}

// TableRoot returns the OID of the table this Watcher operates over.
// Useful for diagnostics and for callers who supplied cols spanning
// multiple coverage tables and want to confirm which one was chosen.
func (w *Watcher[Row]) TableRoot() OID {
	return w.tableRoot.Clone()
}

// run is the tick goroutine's entry point. It performs the
// cold-start full walk, then enters the steady-state
// indicator-gated scheduler loop.
func (w *Watcher[Row]) run() {
	// closeData on goroutine exit guarantees the event channel is
	// closed exactly once, even if the cold-start path bails early
	// or the steady-state loop panics. The fail path in pump.fail
	// also closes the channel under the same chOnce guard, so this
	// is safe-by-construction.
	defer w.closeData()
	// Latch any panic as a terminal error so a misbehaving
	// session/decode path cannot tear down the host process (#5).
	defer func() {
		if r := recover(); r != nil {
			var err error
			if e, ok := r.(error); ok {
				err = ae.Wrap("Watcher.run panicked", e)
			} else {
				err = ae.New().Attr("panic", r).Msg("Watcher.run panicked")
			}
			w.fail(err)
		}
	}()

	if !w.coldStart() {
		// coldStart returned false → terminal error already latched
		// via pump.fail, or context was canceled. Either way, exit.
		return
	}

	w.steadyState()
}

// coldStart performs the first-tick full walk over tableRoot, decodes
// each row, captures the row's indicator VarBind (per-row case) or
// the scalar's value (scalar case), emits a ChangeKindAdded event per
// row, and builds the initial snapshot. Returns true on success,
// false on terminal error or cancellation (in which case the failure
// has already been latched via pump.fail and the caller should
// return).
func (w *Watcher[Row]) coldStart() bool {
	walker := w.sess.BulkWalk(w.ctx, w.tableRoot)
	// Always close the inner walker before returning so its pump
	// goroutine exits within one PDU round-trip — no goroutine
	// accumulation across long-running Watchers (the load-bearing
	// invariant for the nested-Walker lifecycle).
	defer func() { _ = walker.Close() }()

	groups, order, ok := w.collectTableWalk(walker)
	if !ok {
		return false
	}
	if err := walker.Err(); err != nil {
		w.fail(err)
		return false
	}

	// Decode each row and emit an Added event. For the per-row
	// indicator path, also extract the row's indicator VarBind from
	// the grouped VarBinds.
	pending := make([]WatchEvent[Row], 0, len(order))
	for _, key := range order {
		acc := groups[key]
		row, err := w.decode(acc.idx, acc.vbs)
		if err != nil {
			w.recordTickErr(err)
			continue
		}
		entry := snapshotEntry[Row]{row: row}
		if w.indicator.isPerRow() {
			entry.indicatorVB = w.extractIndicatorVB(acc.vbs)
		}
		w.snapshot[key] = entry
		pending = append(pending, WatchEvent[Row]{
			Index: acc.idx,
			Kind:  ChangeKindAdded,
			Row:   row,
		})
	}

	// Indicator probe at cold-start. If the indicator returns
	// an exception variant on the first tick the Watcher transitions
	// to fallback mode and unconditional full walks become the
	// per-tick action. For scalar indicators the probe is the
	// trailing Get; for per-row indicators we infer it from the
	// table-walk content (no row carried an indicator VB despite
	// rows being present).
	if !w.indicator.isPerRow() {
		vbs, err := w.sess.Get(w.ctx, []OID{w.indicator.scalarOID})
		if err != nil {
			// Get failure on the initial scalar probe is terminal
			// during cold-start (network down, session closed —
			// neither is the "indicator is broken on the agent"
			// case fallback is designed for).
			w.fail(err)
			return false
		}
		if len(vbs) > 0 {
			if IsException(vbs[0]) {
				w.enterFallback("scalar indicator returned " +
					vbs[0].GetHeader().Kind.String() +
					" on first probe")
				// Leave w.scalarIndicatorVB nil — in fallback
				// mode the scalar is not consulted.
			} else {
				w.scalarIndicatorVB = vbs[0]
			}
		}
	} else if len(w.snapshot) > 0 {
		anyIndicator := false
		for _, e := range w.snapshot {
			if e.indicatorVB != nil {
				anyIndicator = true
				break
			}
		}
		if !anyIndicator {
			w.enterFallback("per-row indicator column not present " +
				"on any cold-start row — falling back to full walks")
		}
	}

	for _, ev := range pending {
		if !w.send(ev) {
			return false
		}
	}

	// Initialize all schedules from the cold-start completion time
	// so subsequent ticks are evenly spaced from a single t0.
	now := time.Now()
	w.lastForcedWalk = now
	w.stateInterval.Store(int64(w.cfg.CadenceMin))
	w.nextStateTickAt = now.Add(w.cfg.CadenceMin)
	w.nextStaticTickAt = now.Add(time.Duration(staticTierCadenceMultiplier) * w.cfg.CadenceMax)
	for i := range w.counterSchedules {
		w.counterSchedules[i].nextDueAt = now.Add(w.counterSchedules[i].interval)
	}
	return true
}

// collectTableWalk drains walker into a per-row-index map. Each row's
// VarBinds preserve their order of arrival (BulkWalk's column-major
// shape). The boolean return is false when the watcher's stop or
// context fires mid-walk; in that case the caller should return
// without emitting.
func (w *Watcher[Row]) collectTableWalk(walker *Walker) (
	map[string]*rowAccum, []string, bool,
) {
	groups := make(map[string]*rowAccum)
	var order []string

	for idx, vb := range walker.Iter() {
		select {
		case <-w.stop:
			return nil, nil, false
		case <-w.ctx.Done():
			return nil, nil, false
		default:
		}

		rowIdx := rowIndex(idx, w.tableRoot)
		key := rowIdx.String()
		acc, ok := groups[key]
		if !ok {
			acc = &rowAccum{idx: rowIdx}
			groups[key] = acc
			order = append(order, key)
		}
		acc.vbs = append(acc.vbs, vb)
	}
	return groups, order, true
}

// rowAccum buffers VarBinds for a single row during a full-table
// walk. Used by [Watcher.collectTableWalk].
type rowAccum struct {
	idx OID
	vbs []VarBind
}

// extractIndicatorVB returns the indicator VarBind from a row's
// VarBinds, identified by matching the indicator column's OID against
// each VarBind's OID prefix (after stripping the row index). Returns
// nil when the indicator column is not present in the row's VarBinds
// (e.g., the agent omitted it) or when the only candidate is an SNMPv2
// exception variant (NoSuchInstance / EndOfMibView / NoSuchObject) —
// a missing real value cannot be used as a future advance baseline,
// so we treat it as absent and let the mid-life path enter fallback
// rather than silently mis-classifying the row as unchanged.
func (w *Watcher[Row]) extractIndicatorVB(vbs []VarBind) VarBind {
	for _, vb := range vbs {
		if IsException(vb) {
			continue
		}
		colOID := columnOIDFrom(vb.GetHeader().OID, w.tableRoot)
		if colOID.Equal(w.indicator.columnOID) {
			return vb
		}
	}
	return nil
}

// columnOIDFrom returns the column OID portion of a row-instance OID
// under tableRoot. For `<tableRoot>.<entry>.<col>.<index...>`, the
// column OID is `<tableRoot>.<entry>.<col>`.
func columnOIDFrom(full, tableRoot OID) OID {
	if !full.HasPrefix(tableRoot) {
		return OID{}
	}
	colEnd := tableRoot.Len() + 2 // <entry>.<col>
	if full.Len() < colEnd {
		return OID{}
	}
	prefix := make([]uint32, colEnd)
	for i := 0; i < colEnd; i++ {
		prefix[i] = full.At(i)
	}
	return OID{subs: prefix}
}

// recordTickErr stores err as the most-recent transient tick error,
// observable via [Watcher.LastTickErr]. Stored copy avoids capturing
// the loop variable when iterating over a slice of errors.
func (w *Watcher[Row]) recordTickErr(err error) {
	if err == nil {
		return
	}
	errCopy := err
	w.lastTickErr.Store(&errCopy)
}

// steadyState is the multi-schedule tick loop that runs after
// cold-start completes. Each iteration computes the nearest deadline
// across State-tier (indicator-gated), Counter-tier (per-column
// fixed cadence), and Static-tier (library-wide long cadence)
// schedules, sleeps until then, and fires whichever schedules are
// due.
//
// The State-tier interval adapts: starts at CadenceMin,
// resets to CadenceMin on every indicator advance, doubles (per
// cfg.CadenceStepFactor) toward min(CadenceMax, CadenceMin × Ceiling)
// on every quiet tick.
//
// Counter-tier and Static-tier schedules are independent of indicator
// state.
func (w *Watcher[Row]) steadyState() {
	for {
		next := w.computeNextDeadline()
		sleepFor := time.Until(next)
		if sleepFor <= 0 {
			sleepFor = time.Nanosecond
		}

		t := time.NewTimer(sleepFor)
		select {
		case <-w.stop:
			t.Stop()
			return
		case <-w.ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}

		// Reset the transient-error scratch at every tick boundary
		// per the non-latching contract of LastTickErr.
		w.lastTickErr.Store(nil)

		now := time.Now()

		// A forced full walk takes precedence over every other
		// schedule when the configured interval has elapsed.
		if !w.lastForcedWalk.IsZero() &&
			now.Sub(w.lastForcedWalk) >= w.cfg.ForcedWalkInterval {
			if !w.forcedFullWalk() {
				return
			}
			w.lastForcedWalk = time.Now()
			// Skip the rest of this tick — the forced walk already
			// observed every row's current state.
			continue
		}

		// State-tier (indicator-gated, or fallback full walk) tick —
		// fires when due.
		if !w.nextStateTickAt.IsZero() && !now.Before(w.nextStateTickAt) {
			prevInterval := time.Duration(w.stateInterval.Load())

			var advanced, ok bool
			if w.fallback.Load() {
				advanced, ok = w.fallbackTick()
			} else {
				advanced, ok = w.runStateTick()
			}
			if !ok {
				return
			}

			// Stuck-zero probe. Count consecutive ticks where
			// the indicator did NOT advance but Counter-tier emits
			// were observed since the previous State-tier tick (a
			// liveness signal that the device is alive but the
			// indicator is silent). Cross cfg.ProbeWindow → fallback.
			if !w.fallback.Load() {
				counterEmits := w.counterEventsSinceLastState.Swap(0)
				if advanced {
					w.quietWithMovementCount = 0
				} else if counterEmits > 0 {
					w.quietWithMovementCount++
					if w.cfg.ProbeWindow > 0 &&
						w.quietWithMovementCount >= w.cfg.ProbeWindow {
						w.enterFallback("indicator stayed unchanged " +
							"for probe window despite observable " +
							"counter movement")
					}
				}
			} else {
				// In fallback, drain the counter-emit signal so it
				// doesn't accumulate forever.
				w.counterEventsSinceLastState.Store(0)
			}

			w.updateStateInterval(advanced)
			newInterval := time.Duration(w.stateInterval.Load())
			if newInterval != prevInterval {
				// The probe window resets whenever the
				// State-tier cadence steps (any direction). Once the
				// cadence saturates at the ceiling
				// (min(CadenceMax, CadenceMin*CadenceStepCeiling))
				// further quiet ticks do not step and therefore do
				// not reset; worst-case outage-detection latency is
				// bounded by ProbeWindow ×
				// min(CadenceMax, CadenceMin*CadenceStepCeiling)
				// rather than ProbeWindow × CadenceMax × 2^N.
				w.quietWithMovementCount = 0
			}
			w.nextStateTickAt = time.Now().Add(newInterval)
		}

		// Counter-tier tick(s) — fire for every column whose
		// nextDueAt has elapsed.
		if len(w.counterSchedules) > 0 {
			if !w.runCounterTicksDue(time.Now()) {
				return
			}
		}

		// Static-tier tick — fires when due.
		if len(w.staticTierCols) > 0 &&
			!w.nextStaticTickAt.IsZero() &&
			!time.Now().Before(w.nextStaticTickAt) {
			if !w.runStaticTick() {
				return
			}
			w.nextStaticTickAt = time.Now().Add(
				time.Duration(staticTierCadenceMultiplier) * w.cfg.CadenceMax)
		}
	}
}

// computeNextDeadline returns the earliest wall-clock time at which
// any of the Watcher's schedules (State, Counter, Static, forced
// full walk) is due. Used to size the steady-state loop's sleep.
func (w *Watcher[Row]) computeNextDeadline() time.Time {
	candidates := []time.Time{w.nextStateTickAt}
	if !w.lastForcedWalk.IsZero() && w.cfg.ForcedWalkInterval > 0 {
		candidates = append(candidates, w.lastForcedWalk.Add(w.cfg.ForcedWalkInterval))
	}
	for _, c := range w.counterSchedules {
		if !c.nextDueAt.IsZero() {
			candidates = append(candidates, c.nextDueAt)
		}
	}
	if len(w.staticTierCols) > 0 && !w.nextStaticTickAt.IsZero() {
		candidates = append(candidates, w.nextStaticTickAt)
	}

	var earliest time.Time
	for _, c := range candidates {
		if c.IsZero() {
			continue
		}
		if earliest.IsZero() || c.Before(earliest) {
			earliest = c
		}
	}
	if earliest.IsZero() {
		// No schedule is set — degenerate; sleep for CadenceMin to
		// avoid a busy loop. Should not happen post-cold-start.
		return time.Now().Add(w.cfg.CadenceMin)
	}
	return earliest
}

// runStateTick fires the indicator-gated path. Returns
// (advanced, ok). advanced reports whether the indicator advance
// produced any new or changed row this tick — used by
// updateStateInterval to decide whether to reset or step the
// State-tier interval. ok is false on terminal error.
//
// The advance signal comes directly from the tick method (not from
// observing channel buffer growth) so a fast consumer that drains
// the buffer between emit and post-check cannot misclassify an
// advance as quiet.
func (w *Watcher[Row]) runStateTick() (advanced bool, ok bool) {
	if w.indicator.isPerRow() {
		return w.perRowTick()
	}
	return w.scalarTick()
}

// enterFallback transitions the Watcher to fallback mode and emits
// one log line (via cfg.Logger if set). Idempotent: the second and
// subsequent calls are no-ops and do not re-log. Safe to call from
// any context.
//
// Fallback is a sticky-bool process-state property in
// first ship: once true, stays true until Close.
func (w *Watcher[Row]) enterFallback(reason string) {
	if !w.fallback.CompareAndSwap(false, true) {
		return
	}
	if w.cfg.Logger != nil {
		w.cfg.Logger.Warn(
			"snmp.Watcher entering fallback mode: %s", reason)
	}
}

// fallbackTick is the per-tick action while the Watcher is in
// fallback mode: an unconditional full walk of the table with a
// diff emit. Mirrors fullWalkAndDiff (false /* emitRemoved */) —
// row removal stays the forced-walk path's responsibility.
//
// Returns (advanced, ok) so the State-tier cadence still adapts
// based on whether the walk surfaced any change. advanced is
// derived from the walk's emitted-event count (counted before
// send) so a fast consumer cannot mask the signal (#1).
func (w *Watcher[Row]) fallbackTick() (bool, bool) {
	return w.fullWalkAndDiff(false /* emitRemoved */)
}

// updateStateInterval adapts the State-tier cadence: indicator
// advance resets to
// CadenceMin, quiet tick multiplies by CadenceStepFactor capped at
// min(CadenceMax, CadenceMin × CadenceStepCeiling).
func (w *Watcher[Row]) updateStateInterval(advanced bool) {
	if advanced {
		w.stateInterval.Store(int64(w.cfg.CadenceMin))
		return
	}
	if w.cfg.CadenceStepFactor <= 1.0 {
		// Degenerate policy: never step. Honor it.
		return
	}
	current := time.Duration(w.stateInterval.Load())
	next := time.Duration(float64(current) * w.cfg.CadenceStepFactor)
	ceilingByMultiplier := w.cfg.CadenceMin *
		time.Duration(w.cfg.CadenceStepCeiling)
	ceiling := w.cfg.CadenceMax
	if ceilingByMultiplier < ceiling {
		ceiling = ceilingByMultiplier
	}
	if next > ceiling {
		next = ceiling
	}
	if next < w.cfg.CadenceMin {
		next = w.cfg.CadenceMin
	}
	w.stateInterval.Store(int64(next))
}

// runCounterTicksDue fires every Counter-tier column whose nextDueAt
// has elapsed by now. Each column's nextDueAt is advanced by its own
// configured interval after the fetch completes.
//
// Counter-tier fetches: a single multi-OID Get for (dueCols × all
// snapshot indices). Decode + diff via cfg.equal; emit
// ChangeKindModified for changed rows. New rows (index not in
// snapshot) cannot appear via the Counter-tier path because counters
// belong to existing rows; the State-tier or forced-walk path
// surfaces new indices.
//
// Returns false on terminal error.
func (w *Watcher[Row]) runCounterTicksDue(now time.Time) bool {
	dueCols := make([]AnyColumn, 0, len(w.counterSchedules))
	for i := range w.counterSchedules {
		if !w.counterSchedules[i].nextDueAt.IsZero() &&
			!now.Before(w.counterSchedules[i].nextDueAt) {
			dueCols = append(dueCols, w.counterSchedules[i].col)
		}
	}
	if len(dueCols) == 0 {
		return true
	}

	if len(w.snapshot) == 0 {
		// Nothing to fetch yet — empty table or pre-cold-start.
		w.advanceCounterDeadlines(dueCols, now)
		return true
	}

	indices := w.snapshotIndices()

	rowsByIdx, err := w.targetedRowFetchFor(dueCols, indices)
	if err != nil {
		w.recordTickErr(err)
		// Still advance deadlines so the failure does not
		// re-trigger on every loop pass.
		w.advanceCounterDeadlines(dueCols, now)
		return true
	}

	// Counter-tier fetches return ONLY the counter columns; the
	// decode function expects all columns. For first ship we pass
	// just the counter VBs and rely on the decode function to
	// populate only those fields, leaving others as their zero
	// value. The diff via cfg.equal then compares against the
	// snapshot row whose other fields were already zero — so a
	// Counter-only Modified event arrives with only the counter
	// fields populated. Callers who want Counter+State row
	// coalescing should use a per-row tick (indicator advance)
	// which fetches every requested column.
	events := w.diffRowsByIdx(rowsByIdx)

	w.advanceCounterDeadlines(dueCols, now)

	if len(events) > 0 {
		w.counterEventsSinceLastState.Add(int64(len(events)))
	}
	for _, ev := range events {
		if !w.send(ev) {
			return false
		}
	}
	return true
}

// diffRowsByIdx merges each row's freshly fetched VarBinds into the
// snapshot copy via cfg.merge, diffs against the prior snapshot via
// cfg.equal, and returns the Modified events to emit. Indices missing
// from the snapshot are skipped (they were evicted between the
// snapshot read and the targeted fetch). Index parse errors are
// recorded on w.lastTickErr and the offending row is skipped.
// Snapshot entries for changed rows are updated in place, retaining
// the previously observed indicatorVB.
//
// Merge — not decode-and-replace — is the load-bearing primitive
// here. Counter-tier and Static-tier paths fetch a subset of the
// table's columns; replacing the snapshot row with a freshly decoded
// row would zero every field outside the fetched column set. Merge
// preserves cold-start's full row by mutating only the fields whose
// columns are present in vbs.
//
// Callers must hold the tick-goroutine ownership invariant on the
// snapshot (this function is called from runCounterTicksDue and
// runStaticTick, both running on the tick goroutine).
func (w *Watcher[Row]) diffRowsByIdx(rowsByIdx map[string][]VarBind) []WatchEvent[Row] {
	events := make([]WatchEvent[Row], 0)
	for key, vbs := range rowsByIdx {
		prev, exists := w.snapshot[key]
		if !exists {
			// Index disappeared between snapshot and the fetch;
			// nothing to diff against.
			continue
		}
		idx, perr := parseRowIndexKey(key)
		if perr != nil {
			w.recordTickErr(perr)
			continue
		}
		merged := prev.row
		w.merge(&merged, vbs)
		if w.equal(prev.row, merged) {
			continue
		}
		ev := WatchEvent[Row]{
			Index: idx,
			Kind:  ChangeKindModified,
			Row:   merged,
		}
		if w.cfg.PrevRow {
			prevCopy := prev.row
			ev.Prev = &prevCopy
		}
		w.snapshot[key] = snapshotEntry[Row]{
			row:         merged,
			indicatorVB: prev.indicatorVB,
		}
		events = append(events, ev)
	}
	return events
}

// advanceCounterDeadlines pushes each due column's nextDueAt forward
// by its own interval from now. Called both after a successful
// fetch and after a failure so a transient error does not pin the
// schedule.
func (w *Watcher[Row]) advanceCounterDeadlines(due []AnyColumn, now time.Time) {
	dueSet := make(map[string]struct{}, len(due))
	for _, c := range due {
		dueSet[c.OID().String()] = struct{}{}
	}
	for i := range w.counterSchedules {
		if _, ok := dueSet[w.counterSchedules[i].col.OID().String()]; !ok {
			continue
		}
		w.counterSchedules[i].nextDueAt = now.Add(w.counterSchedules[i].interval)
	}
}

// runStaticTick fires the Static-tier batch fetch. Issues a chunked
// multi-OID Get for (staticTierCols × all snapshot indices), decodes
// each row, diffs via cfg.equal, emits ChangeKindModified for
// changed rows. Returns false on terminal error.
func (w *Watcher[Row]) runStaticTick() bool {
	indices := w.snapshotIndices()
	if len(indices) == 0 {
		return true
	}
	rowsByIdx, err := w.targetedRowFetchFor(w.staticTierCols, indices)
	if err != nil {
		w.recordTickErr(err)
		return true
	}

	events := w.diffRowsByIdx(rowsByIdx)

	for _, ev := range events {
		if !w.send(ev) {
			return false
		}
	}
	return true
}

// snapshotIndices returns every row index currently in the snapshot,
// in arbitrary order. Used by Counter-tier and Static-tier paths to
// build their targeted-fetch OID list.
func (w *Watcher[Row]) snapshotIndices() []OID {
	out := make([]OID, 0, len(w.snapshot))
	for key := range w.snapshot {
		idx, err := parseRowIndexKey(key)
		if err != nil {
			continue
		}
		out = append(out, idx)
	}
	return out
}

// targetedRowFetchFor is the multi-column / multi-index targeted
// fetch primitive shared by the per-row, Counter-tier, and
// Static-tier paths. Same chunking logic as [Watcher.targetedRowFetch]
// (which is a wrapper specialising on w.stateTierCols).
func (w *Watcher[Row]) targetedRowFetchFor(cols []AnyColumn, indices []OID) (map[string][]VarBind, error) {
	if len(cols) == 0 || len(indices) == 0 {
		return map[string][]VarBind{}, nil
	}

	maxOIDs := watcherDefaultMaxOIDs
	totalOIDs := len(indices) * len(cols)
	pduCount := (totalOIDs + maxOIDs - 1) / maxOIDs

	if pduCount > w.cfg.BulkWalkFallbackThreshold {
		return w.bulkWalkFilteredFor(cols, indices)
	}

	fullOIDs := make([]OID, 0, totalOIDs)
	for _, idx := range indices {
		for _, col := range cols {
			fullOIDs = append(fullOIDs, col.OID().Append(oidSubs(idx)...))
		}
	}

	result := make(map[string][]VarBind, len(indices))
	for off := 0; off < len(fullOIDs); off += maxOIDs {
		end := off + maxOIDs
		if end > len(fullOIDs) {
			end = len(fullOIDs)
		}
		chunk := fullOIDs[off:end]
		vbs, err := w.sess.Get(w.ctx, chunk)
		if err != nil {
			return nil, err
		}
		for _, vb := range vbs {
			rowIdx := w.rowIndexForVBIn(vb, cols)
			if rowIdx.Len() == 0 {
				continue
			}
			key := rowIdx.String()
			result[key] = append(result[key], vb)
		}
	}
	return result, nil
}

// bulkWalkFilteredFor is the BulkWalk fallback shared by Counter-tier
// and Static-tier paths. Walks the table, filters the response to
// (cols × indices).
func (w *Watcher[Row]) bulkWalkFilteredFor(cols []AnyColumn, indices []OID) (map[string][]VarBind, error) {
	walker := w.sess.BulkWalk(w.ctx, w.tableRoot)
	defer func() { _ = walker.Close() }()

	wantIdx := make(map[string]struct{}, len(indices))
	for _, idx := range indices {
		wantIdx[idx.String()] = struct{}{}
	}
	wantCol := make(map[string]struct{}, len(cols))
	for _, c := range cols {
		wantCol[c.OID().String()] = struct{}{}
	}

	result := make(map[string][]VarBind)
	for full, vb := range walker.Iter() {
		select {
		case <-w.stop:
			return nil, w.ctx.Err()
		case <-w.ctx.Done():
			return nil, w.ctx.Err()
		default:
		}
		colOID := columnOIDFrom(full, w.tableRoot)
		if _, ok := wantCol[colOID.String()]; !ok {
			continue
		}
		rowIdx := rowIndex(full, w.tableRoot)
		if _, ok := wantIdx[rowIdx.String()]; !ok {
			continue
		}
		result[rowIdx.String()] = append(result[rowIdx.String()], vb)
	}
	if err := walker.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// rowIndexForVBIn returns the row-index OID for a VarBind whose OID
// is `<col>.<idx...>` for one of the supplied cols. Generalisation of
// [Watcher.rowIndexForVB] to a caller-supplied column set.
func (w *Watcher[Row]) rowIndexForVBIn(vb VarBind, cols []AnyColumn) OID {
	full := vb.GetHeader().OID
	for _, col := range cols {
		colOID := col.OID()
		if full.HasPrefix(colOID) && full.Len() > colOID.Len() {
			return stripColumnPrefix(full, colOID)
		}
	}
	return OID{}
}

// perRowTick implements the per-row indicator path. It walks
// just the indicator column across the table, diffs each row's
// indicator VarBind against the snapshot, and — for rows whose
// indicator advanced — dispatches a targeted multi-OID Get (chunked,
// with BulkWalk fallback above threshold) for the State-tier columns
// of just the advanced rows. Decoded rows are diffed via cfg.equal
// to compute ChangeKindModified; new rows surface as
// ChangeKindAdded.
//
// Returns (advanced, ok): advanced is true when the indicator-column
// walk detected any new row or any row's indicator value change;
// ok is false on terminal error.
func (w *Watcher[Row]) perRowTick() (bool, bool) {
	walker := w.sess.BulkWalk(w.ctx, w.indicator.columnOID)
	defer func() { _ = walker.Close() }()

	type seen struct {
		idx OID
		vb  VarBind
	}
	advanced := []seen(nil)
	added := []seen(nil)
	sawException := false

	for full, vb := range walker.Iter() {
		select {
		case <-w.stop:
			return false, false
		case <-w.ctx.Done():
			return false, false
		default:
		}

		// Mid-life indicator exception: an indicator column that
		// returns NoSuchInstance
		// or any other exception variant during steady-state probing
		// breaks the equality invariant (a missing value cannot serve
		// as a future advance baseline). Latch the observation and
		// transition to fallback after the walk completes (#6).
		if IsException(vb) {
			sawException = true
			continue
		}

		// Compute the row index by stripping the indicator column
		// OID. full has shape `<indicator.columnOID>.<idx...>`; the
		// indicator OID already includes <tableRoot>.<entry>.<col>.
		rowIdx := stripColumnPrefix(full, w.indicator.columnOID)
		if rowIdx.Len() == 0 {
			continue
		}
		key := rowIdx.String()
		prev, exists := w.snapshot[key]
		switch {
		case !exists:
			added = append(added, seen{idx: rowIdx, vb: vb})
		case !indicatorVBEqual(prev.indicatorVB, vb):
			advanced = append(advanced, seen{idx: rowIdx, vb: vb})
		}
	}
	if err := walker.Err(); err != nil {
		w.fail(err)
		return false, false
	}
	if sawException && !w.fallback.Load() {
		w.enterFallback("per-row indicator returned exception variant during steady-state tick")
		return false, true
	}

	if len(advanced) == 0 && len(added) == 0 {
		// Quiet tick. Return advanced=false so the caller steps the
		// State-tier cadence toward CadenceMax.
		return false, true
	}

	// Fetch State-tier cols for the union of added + advanced rows.
	// New rows need a fresh fetch since their state-tier values were
	// never observed; advanced rows need a refresh.
	allChanged := make([]OID, 0, len(added)+len(advanced))
	for _, s := range added {
		allChanged = append(allChanged, s.idx)
	}
	for _, s := range advanced {
		allChanged = append(allChanged, s.idx)
	}

	rowsByIdx, err := w.targetedRowFetchFor(w.stateTierCols, allChanged)
	if err != nil {
		// Transient: surface via LastTickErr, do not emit anything
		// for this tick. Terminal-vs-transient classification runs
		// above this — any error reaching here is treated as
		// transient.
		// Report advanced=true so the caller resets the State-tier
		// cadence (indicator did advance; only the state fetch
		// failed).
		w.recordTickErr(err)
		return true, true
	}

	events := make([]WatchEvent[Row], 0, len(added)+len(advanced))

	for _, s := range added {
		key := s.idx.String()
		vbs, ok := rowsByIdx[key]
		if !ok {
			// Row vanished between the indicator walk and the
			// targeted Get. Note as transient and move on.
			w.recordTickErr(ae.New().Attr("index", s.idx).
				Msg("targeted Get returned no rows for index"))
			continue
		}
		// New row: no prior snapshot to merge against. decode is the
		// authoritative full(-for-state-tier) row.
		row, derr := w.decode(s.idx, vbs)
		if derr != nil {
			w.recordTickErr(derr)
			continue
		}
		w.snapshot[key] = snapshotEntry[Row]{
			row:         row,
			indicatorVB: s.vb,
		}
		events = append(events, WatchEvent[Row]{
			Index: s.idx,
			Kind:  ChangeKindAdded,
			Row:   row,
		})
	}

	for _, s := range advanced {
		key := s.idx.String()
		vbs, ok := rowsByIdx[key]
		if !ok {
			w.recordTickErr(ae.New().Attr("index", s.idx).
				Msg("targeted Get returned no rows for index"))
			continue
		}
		prev := w.snapshot[key]
		// Merge State-tier VBs onto the prior row instead of decoding
		// + replacing — replacing would zero Counter-tier and
		// Static-tier fields that were populated by cold-start /
		// previous tier fetches.
		merged := prev.row
		w.merge(&merged, vbs)
		ev := WatchEvent[Row]{
			Index: s.idx,
			Kind:  ChangeKindModified,
			Row:   merged,
		}
		if w.cfg.PrevRow {
			prevCopy := prev.row
			ev.Prev = &prevCopy
		}
		// Update snapshot before emit so a concurrent consumer
		// observing snapshot state via a later tick sees the new
		// value.
		w.snapshot[key] = snapshotEntry[Row]{
			row:         merged,
			indicatorVB: s.vb,
		}
		// If merged row is equal to prev row by cfg.equal, the
		// indicator advanced but no observable State-tier field
		// changed. Skip the Modified emit (one event per detected
		// row change); still update the snapshot's
		// indicator VB so we don't re-fetch the same row next tick.
		if w.equal(prev.row, merged) {
			continue
		}
		events = append(events, ev)
	}

	// At this point at least one new or advanced row was detected
	// and (best-effort) fetched. Report advanced=true to the caller
	// so the State-tier cadence resets to CadenceMin.
	for _, ev := range events {
		if !w.send(ev) {
			return true, false
		}
	}
	return true, true
}

// scalarTick implements the scalar indicator path. It Gets the
// scalar OID, compares the returned VarBind against the latched one,
// and — on advance — performs a full BulkWalk of the table to diff
// every row.
//
// Returns (advanced, ok): advanced is true when the scalar's value
// changed since the last observation; ok is false on terminal error.
func (w *Watcher[Row]) scalarTick() (bool, bool) {
	vbs, err := w.sess.Get(w.ctx, []OID{w.indicator.scalarOID})
	if err != nil {
		if errors.Is(err, ErrSessionClosed) ||
			errors.Is(err, context.Canceled) ||
			errors.Is(err, context.DeadlineExceeded) {
			w.fail(err)
			return false, false
		}
		w.recordTickErr(err)
		return false, true
	}
	if len(vbs) == 0 {
		w.recordTickErr(ae.Msg("Get returned no VarBinds"))
		return false, true
	}
	current := vbs[0]
	if indicatorVBEqual(w.scalarIndicatorVB, current) {
		return false, true
	}
	w.scalarIndicatorVB = current
	_, ok := w.fullWalkAndDiff(false /* emitRemoved */)
	return true, ok
}

// forcedFullWalk performs an unconditional full table walk
// regardless of indicator state, used as the row-removal backstop.
// Emits ChangeKindRemoved for indices present in the snapshot but
// missing from the walk, alongside Added/Modified events derived
// from the walk's contents.
//
// Returns ok = false on terminal error; the advanced bit from the
// underlying walk is discarded because the State-tier cadence does
// not apply on a forced-walk cycle.
func (w *Watcher[Row]) forcedFullWalk() bool {
	_, ok := w.fullWalkAndDiff(true /* emitRemoved */)
	return ok
}

// fullWalkAndDiff does a BulkWalk(tableRoot), groups VarBinds by row,
// decodes, and diffs against the snapshot. New rows emit Added,
// modified rows (per cfg.equal) emit Modified, and — if emitRemoved
// is set — rows in the snapshot but missing from the walk emit
// Removed and are evicted.
//
// Per-row indicator VBs in the walk update each row's snapshot entry
// so the next perRowTick has a current baseline.
//
// Returns (advanced, ok): advanced is len(events) > 0 counted before
// any send, so a fast consumer draining the channel between the send
// and the caller cannot misclassify an emitting walk as quiet (#1).
// ok is false on terminal error (already latched via w.fail).
func (w *Watcher[Row]) fullWalkAndDiff(emitRemoved bool) (bool, bool) {
	walker := w.sess.BulkWalk(w.ctx, w.tableRoot)
	defer func() { _ = walker.Close() }()

	groups, order, walkOK := w.collectTableWalk(walker)
	if !walkOK {
		return false, false
	}
	if err := walker.Err(); err != nil {
		w.fail(err)
		return false, false
	}

	seenKeys := make(map[string]struct{}, len(order))
	events := make([]WatchEvent[Row], 0, len(order))

	for _, key := range order {
		seenKeys[key] = struct{}{}
		acc := groups[key]
		row, err := w.decode(acc.idx, acc.vbs)
		if err != nil {
			w.recordTickErr(err)
			continue
		}
		entry := snapshotEntry[Row]{row: row}
		if w.indicator.isPerRow() {
			entry.indicatorVB = w.extractIndicatorVB(acc.vbs)
		}

		prev, existed := w.snapshot[key]
		w.snapshot[key] = entry
		if !existed {
			events = append(events, WatchEvent[Row]{
				Index: acc.idx,
				Kind:  ChangeKindAdded,
				Row:   row,
			})
			continue
		}
		if w.equal(prev.row, row) {
			continue
		}
		ev := WatchEvent[Row]{
			Index: acc.idx,
			Kind:  ChangeKindModified,
			Row:   row,
		}
		if w.cfg.PrevRow {
			prevCopy := prev.row
			ev.Prev = &prevCopy
		}
		events = append(events, ev)
	}

	// Collect (idx, prevRow) pairs for rows that disappeared. We
	// intentionally do NOT evict from w.snapshot here: if Close
	// races between the send loop and the snapshot eviction, the
	// consumer would observe a mutated snapshot without seeing the
	// corresponding Removed event (#11). Eviction happens only after
	// each Removed event is successfully sent.
	type removed struct {
		idx     OID
		prevRow Row
		key     string
	}
	var removals []removed
	if emitRemoved {
		for key, prev := range w.snapshot {
			if _, ok := seenKeys[key]; ok {
				continue
			}
			idx, perr := parseRowIndexKey(key)
			if perr != nil {
				w.recordTickErr(perr)
				continue
			}
			removals = append(removals, removed{
				idx: idx, prevRow: prev.row, key: key,
			})
		}
	}

	// advanced is computed before any send so a consumer draining
	// the channel mid-loop cannot mask the advance signal.
	advanced := len(events) > 0 || len(removals) > 0

	for _, ev := range events {
		if !w.send(ev) {
			return advanced, false
		}
	}
	for _, r := range removals {
		if !w.send(WatchEvent[Row]{
			Index: r.idx,
			Kind:  ChangeKindRemoved,
			Row:   r.prevRow,
		}) {
			return advanced, false
		}
		delete(w.snapshot, r.key)
	}
	return advanced, true
}

// stripColumnPrefix returns the row-index portion of full when full
// is `<colOID>.<idx...>`. Returns the empty OID when the prefix does
// not match or full has no index portion.
func stripColumnPrefix(full, colOID OID) OID {
	if !full.HasPrefix(colOID) {
		return OID{}
	}
	if full.Len() <= colOID.Len() {
		return OID{}
	}
	tail := make([]uint32, full.Len()-colOID.Len())
	for i := range tail {
		tail[i] = full.At(colOID.Len() + i)
	}
	return OID{subs: tail}
}

// oidSubs returns the sub-identifier slice underlying o. Used only
// for building targeted-Get OIDs where the caller wants to append a
// row index to a column OID via OID.Append's variadic surface.
func oidSubs(o OID) []uint32 {
	out := make([]uint32, o.Len())
	for i := range out {
		out[i] = o.At(i)
	}
	return out
}

// parseRowIndexKey parses a dotted-decimal row-index key back into an
// OID, bypassing the SMIv2 root validation that NewOID applies. Row
// indices legitimately violate the 0|1|2 first-sub-id rule (e.g.,
// `5` for ifIndex=5).
func parseRowIndexKey(key string) (OID, error) {
	if key == "" {
		return OID{}, nil
	}
	subs := make([]uint32, 0, 4)
	start := 0
	for i := 0; i <= len(key); i++ {
		if i == len(key) || key[i] == '.' {
			if i == start {
				return OID{}, ae.New().Attr("key", key).
					Msg("empty sub-identifier in row index key")
			}
			sub := key[start:i]
			v64, err := strconv.ParseUint(sub, 10, 32)
			if err != nil {
				return OID{}, ae.Wrapf(
					"parseRowIndexKey: sub-identifier %q overflow",
					err, sub)
			}
			subs = append(subs, uint32(v64))
			start = i + 1
		}
	}
	return OID{subs: subs}, nil
}

// indicatorVBEqual reports whether two indicator VarBind observations
// are equal per the raw-bytes equality rule. With raw payload bytes not
// surfaced through the [VarBind] interface, the implementation
// compares the decoded value within each variant — equivalent to a
// byte comparison for the indicator-eligible types (TimeTicks,
// Unsigned32, OctetString-encoded DateAndTime).
//
// Both nil values compare equal; a nil vs. non-nil pair compares
// unequal so the first-observation case (snapshot entry constructed
// without an indicator VB) is correctly classified as "advanced" on
// the next observation.
func indicatorVBEqual(a, b VarBind) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	}
	if a.GetHeader().Kind != b.GetHeader().Kind {
		return false
	}
	switch x := a.(type) {
	case TimeTicksVar:
		y, ok := b.(TimeTicksVar)
		return ok && x.Value == y.Value
	case Uinteger32Var:
		y, ok := b.(Uinteger32Var)
		return ok && x.Value == y.Value
	case Counter32Var:
		y, ok := b.(Counter32Var)
		return ok && x.Value == y.Value
	case Counter64Var:
		y, ok := b.(Counter64Var)
		return ok && x.Value == y.Value
	case Gauge32Var:
		y, ok := b.(Gauge32Var)
		return ok && x.Value == y.Value
	case Integer32Var:
		y, ok := b.(Integer32Var)
		return ok && x.Value == y.Value
	case OctetStringVar:
		y, ok := b.(OctetStringVar)
		return ok && bytes.Equal(x.Value, y.Value)
	case ObjectIDVar:
		y, ok := b.(ObjectIDVar)
		return ok && x.Value.Equal(y.Value)
	}
	// Exception variants and other types are never legitimate
	// indicator values. Treat them as unequal so the comparison
	// falls through to advance/fallback handling.
	return false
}

// rowIndex extracts a row's index OID from a VarBind OID under the
// given tableRoot. The SMIv2 convention is
// tableRoot.<entry>.<columnNum>.<index...> with entry always 1
// under the table object, so the index is oid.subs[tableRoot.Len()+2:].
//
// For OIDs that do not follow the convention (oid not under
// tableRoot, or fewer than 2 sub-ids past the root), rowIndex returns
// the empty OID. The caller will group these under the "" key and
// the decode function — if it accepts them — produces an empty-index
// Row.
func rowIndex(full, tableRoot OID) OID {
	if !full.HasPrefix(tableRoot) {
		return OID{}
	}
	skip := tableRoot.Len() + 2 // skip <entry> and <columnNum>
	if full.Len() <= skip {
		return OID{}
	}
	tail := make([]uint32, full.Len()-skip)
	for i := range tail {
		tail[i] = full.At(skip + i)
	}
	return OID{subs: tail}
}
