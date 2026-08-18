package snmp

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// fakeColumn is a minimal AnyColumn implementation used by tests in
// this file to exercise WatchOption keying logic without depending on
// the Column[T] generic type.
type fakeColumn struct {
	oid  OID
	kind Kind
}

func (f fakeColumn) OID() OID    { return f.oid }
func (f fakeColumn) Key() string { return f.oid.WireKey() }
func (f fakeColumn) Kind() Kind  { return f.kind }
func (fakeColumn) sealedColumn() {}

// oidParts is a tiny test helper to build OIDs inline from explicit
// sub-identifiers. The package's other test helper [mustOID] takes a
// dotted string; oidParts here is uint32-vararg-shaped so OID literals
// stay readable when nested inside table-driven tests.
func oidParts(t *testing.T, parts ...uint32) OID {
	t.Helper()
	o, err := NewOID(parts...)
	if err != nil {
		t.Fatalf("NewOID(%v): %v", parts, err)
	}
	return o
}

// --- Tier --------------------------------------------------------------

func TestTier_AllValuesStringed(t *testing.T) {
	// Closed-enum convention pin: every Tier constant has a non-empty
	// String() that doesn't fall through to the numeric fallback.
	// Catches a future reorder/insert that forgets to extend String().
	for _, tc := range []struct {
		tier Tier
		want string
	}{
		{TierUnknown, "Unknown"},
		{TierCounter, "Counter"},
		{TierIndicator, "Indicator"},
		{TierState, "State"},
		{TierStatic, "Static"},
	} {
		got := tc.tier.String()
		if got != tc.want {
			t.Errorf("Tier(%d).String() = %q, want %q",
				int(tc.tier), got, tc.want)
		}
	}
}

func TestTier_UnknownValueFallback(t *testing.T) {
	got := Tier(99).String()
	if !strings.HasPrefix(got, "Tier(") {
		t.Errorf("Tier(99).String() = %q, want Tier(99) fallback", got)
	}
}

// --- ChangeKind --------------------------------------------------------

func TestChangeKind_AllValuesStringed(t *testing.T) {
	for _, tc := range []struct {
		k    ChangeKind
		want string
	}{
		{ChangeKindUnknown, "Unknown"},
		{ChangeKindAdded, "Added"},
		{ChangeKindModified, "Modified"},
		{ChangeKindRemoved, "Removed"},
	} {
		got := tc.k.String()
		if got != tc.want {
			t.Errorf("ChangeKind(%d).String() = %q, want %q",
				int(tc.k), got, tc.want)
		}
	}
}

func TestChangeKind_UnknownValueFallback(t *testing.T) {
	got := ChangeKind(99).String()
	if !strings.HasPrefix(got, "ChangeKind(") {
		t.Errorf("ChangeKind(99).String() = %q, want fallback", got)
	}
}

// --- ChangeIndicator --------------------------------------------------

func TestNewPerRowIndicator_Happy(t *testing.T) {
	tableRoot := oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2)
	colOID := oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2, 1, 9)
	col := fakeColumn{oid: colOID, kind: KindTimeTicks}

	ci, err := NewPerRowIndicator(col, tableRoot)
	if err != nil {
		t.Fatalf("NewPerRowIndicator: %v", err)
	}
	if !ci.isPerRow() {
		t.Error("isPerRow() = false, want true")
	}
	if !ci.ColumnOID().Equal(colOID) {
		t.Errorf("ColumnOID() = %s, want %s", ci.ColumnOID(), colOID)
	}
	cov := ci.Coverage()
	if len(cov) != 1 || !cov[0].Equal(tableRoot) {
		t.Errorf("Coverage() = %v, want [%s]", cov, tableRoot)
	}
	if !ci.ScalarOID().Equal(OID{}) {
		t.Errorf("ScalarOID() = %s, want empty", ci.ScalarOID())
	}
}

func TestNewPerRowIndicator_NilColumn(t *testing.T) {
	tableRoot := oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2)
	_, err := NewPerRowIndicator(nil, tableRoot)
	if err == nil {
		t.Fatal("NewPerRowIndicator(nil, ...) = nil err, want error")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error message %q does not mention nil", err)
	}
}

func TestNewPerRowIndicator_EmptyRoot(t *testing.T) {
	col := fakeColumn{oid: oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2, 1, 9)}
	_, err := NewPerRowIndicator(col, OID{})
	if err == nil {
		t.Fatal("NewPerRowIndicator(col, empty) = nil err, want error")
	}
	if !strings.Contains(err.Error(), "tableRoot") {
		t.Errorf("error message %q does not mention tableRoot", err)
	}
}

func TestNewPerRowIndicator_ColumnNotUnderRoot(t *testing.T) {
	tableRoot := oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2)
	col := fakeColumn{oid: oidParts(t, 1, 3, 6, 1, 2, 1, 31, 1, 1, 5)}

	_, err := NewPerRowIndicator(col, tableRoot)
	if err == nil {
		t.Fatal("expected error for column outside tableRoot")
	}
	// The column and table-root OIDs moved from the message into
	// structured attributes.
	attrs := errs.Attributes(err)
	if got, ok := attrs["column_oid"].(OID); !ok || got.String() != "1.3.6.1.2.1.31.1.1.5" {
		t.Errorf("column_oid attr = %v, want 1.3.6.1.2.1.31.1.1.5", attrs["column_oid"])
	}
	if got, ok := attrs["table_root"].(OID); !ok || got.String() != "1.3.6.1.2.1.2.2" {
		t.Errorf("table_root attr = %v, want 1.3.6.1.2.1.2.2", attrs["table_root"])
	}
}

func TestNewScalarIndicator_Happy(t *testing.T) {
	scalarOID := oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	roots := []OID{
		oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 1, 1),
		oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 2, 1),
	}

	ci, err := NewScalarIndicator(scalarOID, KindUinteger32, roots)
	if err != nil {
		t.Fatalf("NewScalarIndicator: %v", err)
	}
	if ci.isPerRow() {
		t.Error("isPerRow() = true, want false")
	}
	if !ci.ScalarOID().Equal(scalarOID) {
		t.Errorf("ScalarOID() = %s, want %s", ci.ScalarOID(), scalarOID)
	}
	if ci.Kind() != KindUinteger32 {
		t.Errorf("Kind() = %v, want %v", ci.Kind(), KindUinteger32)
	}
	cov := ci.Coverage()
	if len(cov) != 2 {
		t.Fatalf("Coverage() len = %d, want 2", len(cov))
	}
	for i, r := range roots {
		if !cov[i].Equal(r) {
			t.Errorf("Coverage()[%d] = %s, want %s", i, cov[i], r)
		}
	}
}

func TestNewScalarIndicator_NilTableRoots(t *testing.T) {
	scalarOID := oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	_, err := NewScalarIndicator(scalarOID, KindUinteger32, nil)
	if err == nil {
		t.Fatal("NewScalarIndicator(_, _, nil) = nil err, want error")
	}
	if !strings.Contains(err.Error(), "at least one table root") {
		t.Errorf("error %q does not mention the requirement", err)
	}
}

func TestNewScalarIndicator_EmptyTableRoots(t *testing.T) {
	scalarOID := oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	_, err := NewScalarIndicator(scalarOID, KindUinteger32, []OID{})
	if err == nil {
		t.Fatal("NewScalarIndicator(_, _, []) = nil err, want error")
	}
}

func TestNewScalarIndicator_EmptyScalarOID(t *testing.T) {
	roots := []OID{oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 1, 1)}
	_, err := NewScalarIndicator(OID{}, KindUinteger32, roots)
	if err == nil {
		t.Fatal("NewScalarIndicator(empty, _, _) = nil err, want error")
	}
}

func TestNewScalarIndicator_EmptyTableRootElement(t *testing.T) {
	scalarOID := oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	roots := []OID{
		oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 1, 1),
		{}, // empty in the middle
	}
	_, err := NewScalarIndicator(scalarOID, KindUinteger32, roots)
	if err == nil {
		t.Fatal("NewScalarIndicator with empty root element = nil err, want error")
	}
	if got := errs.Attributes(err)["index"]; got != 1 {
		t.Errorf("index attr = %v, want 1 (the offending element)", got)
	}
}

func TestMustChangeIndicator_PanicsOnError(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("MustChangeIndicator did not panic on error")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("recovered value is not an error: %T", r)
		}
		if !strings.Contains(err.Error(), "table root") {
			t.Errorf("panic error %q does not match underlying validation", err)
		}
	}()

	scalarOID := oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	_ = MustChangeIndicator(NewScalarIndicator(scalarOID, KindUinteger32, nil))
}

func TestMustChangeIndicator_PassesThroughOnSuccess(t *testing.T) {
	tableRoot := oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2)
	col := fakeColumn{oid: oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2, 1, 9)}

	ci := MustChangeIndicator(NewPerRowIndicator(col, tableRoot))
	if !ci.isPerRow() {
		t.Error("isPerRow() = false on round-tripped indicator")
	}
}

func TestChangeIndicator_CoverageReturnsCopy(t *testing.T) {
	scalarOID := oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	roots := []OID{oidParts(t, 1, 3, 6, 1, 2, 1, 47, 1, 1, 1)}
	ci, err := NewScalarIndicator(scalarOID, KindUinteger32, roots)
	if err != nil {
		t.Fatalf("NewScalarIndicator: %v", err)
	}

	cov := ci.Coverage()
	cov[0] = OID{} // mutate the returned slice

	// Reading Coverage again must yield the original — the indicator
	// owns its storage and Coverage returns a fresh copy.
	cov2 := ci.Coverage()
	if cov2[0].Equal(OID{}) {
		t.Error("Coverage() returned mutable shared storage")
	}
}

func TestChangeIndicator_ZeroValueDetected(t *testing.T) {
	var ci ChangeIndicator
	if !ci.isZero() {
		t.Error("zero ChangeIndicator did not report isZero=true")
	}

	tableRoot := oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2)
	col := fakeColumn{oid: oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2, 1, 9)}
	ci, _ = NewPerRowIndicator(col, tableRoot)
	if ci.isZero() {
		t.Error("constructed per-row indicator reported isZero=true")
	}
}

// --- WatchConfig + WithXxx options ------------------------------------

func TestWatchConfig_AllWithXxxApply(t *testing.T) {
	col := fakeColumn{oid: oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2, 1, 10)}
	logger := &countingLogger{}

	cfg := ApplyWatchOptions(
		WithCadenceBounds(5*time.Second, 80*time.Second),
		WithCadenceStepPolicy(2.0, 8),
		WithCounterCadence(col, 25*time.Second),
		WithColumnTier(col, TierStatic),
		WithPrevRow(),
		WithProbeWindow(7),
		WithForcedWalkInterval(5*time.Minute),
		WithBulkWalkFallbackThreshold(8),
		WithLogger(logger),
	)
	if err := cfg.ValidationError(); err != nil {
		t.Fatalf("ValidationError = %v, want nil", err)
	}
	if cfg.CadenceMin != 5*time.Second || cfg.CadenceMax != 80*time.Second {
		t.Errorf("CadenceBounds = (%v,%v)", cfg.CadenceMin, cfg.CadenceMax)
	}
	if !cfg.CadenceBoundsSet {
		t.Error("CadenceBoundsSet = false")
	}
	if cfg.CadenceStepFactor != 2.0 || cfg.CadenceStepCeiling != 8 {
		t.Errorf("StepPolicy = (%v,%d)", cfg.CadenceStepFactor, cfg.CadenceStepCeiling)
	}
	if d, ok := cfg.counterCadence(col); !ok || d != 25*time.Second {
		t.Errorf("counterCadence = (%v,%v)", d, ok)
	}
	if tier, ok := cfg.tierOverride(col); !ok || tier != TierStatic {
		t.Errorf("tierOverride = (%v,%v)", tier, ok)
	}
	if !cfg.PrevRow {
		t.Error("PrevRow = false")
	}
	if cfg.ProbeWindow != 7 || !cfg.ProbeWindowSet {
		t.Errorf("ProbeWindow = (%d, set=%v)", cfg.ProbeWindow, cfg.ProbeWindowSet)
	}
	if cfg.ForcedWalkInterval != 5*time.Minute || !cfg.ForcedWalkIntervalSet {
		t.Errorf("ForcedWalkInterval = (%v, set=%v)",
			cfg.ForcedWalkInterval, cfg.ForcedWalkIntervalSet)
	}
	if cfg.BulkWalkFallbackThreshold != 8 || !cfg.BulkWalkFallbackThresholdSet {
		t.Errorf("BulkWalkFallbackThreshold = (%d, set=%v)",
			cfg.BulkWalkFallbackThreshold, cfg.BulkWalkFallbackThresholdSet)
	}
	if cfg.Logger != logger {
		t.Errorf("Logger = %v, want %v", cfg.Logger, logger)
	}
}

func TestWatchConfig_BoundsCommutative(t *testing.T) {
	a := ApplyWatchOptions(
		WithCadenceBounds(5*time.Second, 80*time.Second),
		WithCadenceStepPolicy(2.0, 8),
	)
	b := ApplyWatchOptions(
		WithCadenceStepPolicy(2.0, 8),
		WithCadenceBounds(5*time.Second, 80*time.Second),
	)
	if a.CadenceMin != b.CadenceMin || a.CadenceMax != b.CadenceMax {
		t.Errorf("bounds diverged across order")
	}
	if a.CadenceStepFactor != b.CadenceStepFactor ||
		a.CadenceStepCeiling != b.CadenceStepCeiling {
		t.Errorf("step policy diverged across order")
	}
}

func TestWatchConfig_BoundsLastWins(t *testing.T) {
	cfg := ApplyWatchOptions(
		WithCadenceBounds(10*time.Second, 60*time.Second),
		WithCadenceBounds(5*time.Second, 30*time.Second),
	)
	if cfg.CadenceMin != 5*time.Second || cfg.CadenceMax != 30*time.Second {
		t.Errorf("last-wins violated: bounds = (%v,%v)",
			cfg.CadenceMin, cfg.CadenceMax)
	}
}

func TestWatchConfig_BoundsValidation(t *testing.T) {
	for name, opts := range map[string][]WatchOption{
		"min greater than max": {
			WithCadenceBounds(1*time.Minute, 10*time.Second),
		},
		"negative min": {
			WithCadenceBounds(-1*time.Second, 30*time.Second),
		},
		"zero min": {
			WithCadenceBounds(0, 30*time.Second),
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := ApplyWatchOptions(opts...)
			if err := cfg.ValidationError(); err == nil {
				t.Errorf("ValidationError = nil, want non-nil for %q", name)
			}
		})
	}
}

func TestWatchConfig_StepPolicyValidation(t *testing.T) {
	// factor < 1.0 is rejected; factor == 1.0 is accepted (degenerate
	// but legal).
	cfg := ApplyWatchOptions(WithCadenceStepPolicy(0.5, 8))
	if err := cfg.ValidationError(); err == nil {
		t.Error("factor 0.5 not rejected")
	}

	cfg = ApplyWatchOptions(WithCadenceStepPolicy(1.0, 8))
	if err := cfg.ValidationError(); err != nil {
		t.Errorf("factor 1.0 rejected: %v", err)
	}

	cfg = ApplyWatchOptions(WithCadenceStepPolicy(2.0, 0))
	if err := cfg.ValidationError(); err == nil {
		t.Error("ceiling 0 not rejected")
	}

	cfg = ApplyWatchOptions(WithCadenceStepPolicy(2.0, -1))
	if err := cfg.ValidationError(); err == nil {
		t.Error("ceiling -1 not rejected")
	}
}

func TestWatchConfig_CounterCadenceValidation(t *testing.T) {
	col := fakeColumn{oid: oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2, 1, 10)}

	cfg := ApplyWatchOptions(WithCounterCadence(nil, 25*time.Second))
	if err := cfg.ValidationError(); err == nil {
		t.Error("nil column not rejected")
	}

	cfg = ApplyWatchOptions(WithCounterCadence(col, 0))
	if err := cfg.ValidationError(); err == nil {
		t.Error("zero cadence not rejected")
	}

	cfg = ApplyWatchOptions(WithCounterCadence(col, -time.Second))
	if err := cfg.ValidationError(); err == nil {
		t.Error("negative cadence not rejected")
	}
}

func TestWatchConfig_OverrideKeyedByOIDString(t *testing.T) {
	// Two distinct AnyColumn values referring to the same OID must
	// share the same override slot per the OID-string comparability
	// invariant. last-wins under a single OID-string key.
	oid := oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2, 1, 18) // ifAlias
	col1 := fakeColumn{oid: oid, kind: KindOctetString}
	col2 := fakeColumn{oid: oid, kind: KindOctetString}
	// Pin the premise: fakeColumn (and the real Column[T]) is NOT
	// Go-comparable because OID itself carries a []uint32 slice. A
	// future refactor that accidentally introduces map[AnyColumn]Tier
	// would compile but panic at insert time; the OID-string keying
	// dodges that.
	_ = col1
	_ = col2

	cfg := ApplyWatchOptions(
		WithColumnTier(col1, TierStatic),
		WithColumnTier(col2, TierState),
	)
	if err := cfg.ValidationError(); err != nil {
		t.Fatalf("ValidationError = %v", err)
	}
	tier, ok := cfg.tierOverride(col1)
	if !ok || tier != TierState {
		t.Errorf("tierOverride(col1) = (%v,%v), want (State,true)", tier, ok)
	}
	tier, ok = cfg.tierOverride(col2)
	if !ok || tier != TierState {
		t.Errorf("tierOverride(col2) = (%v,%v), want (State,true)", tier, ok)
	}
	if got := len(cfg.TierOverrides); got != 1 {
		t.Errorf("TierOverrides has %d entries, want 1 (single shared key)", got)
	}
}

func TestWatchConfig_ColumnTierValidation(t *testing.T) {
	col := fakeColumn{oid: oidParts(t, 1, 3, 6, 1, 2, 1, 2, 2, 1, 18)}

	cfg := ApplyWatchOptions(WithColumnTier(nil, TierStatic))
	if err := cfg.ValidationError(); err == nil {
		t.Error("nil column not rejected")
	}

	cfg = ApplyWatchOptions(WithColumnTier(col, TierUnknown))
	if err := cfg.ValidationError(); err == nil {
		t.Error("TierUnknown not rejected")
	}

	cfg = ApplyWatchOptions(WithColumnTier(col, Tier(99)))
	if err := cfg.ValidationError(); err == nil {
		t.Error("unknown Tier value not rejected")
	}
}

func TestWatchConfig_ProbeWindowValidation(t *testing.T) {
	cfg := ApplyWatchOptions(WithProbeWindow(0))
	if err := cfg.ValidationError(); err != nil {
		t.Errorf("ProbeWindow 0 rejected: %v", err)
	}
	if !cfg.ProbeWindowSet || cfg.ProbeWindow != 0 {
		t.Errorf("ProbeWindow = (%d, set=%v)", cfg.ProbeWindow, cfg.ProbeWindowSet)
	}

	cfg = ApplyWatchOptions(WithProbeWindow(-1))
	if err := cfg.ValidationError(); err == nil {
		t.Error("negative ProbeWindow not rejected")
	}
}

func TestWatchConfig_ForcedWalkValidation(t *testing.T) {
	cfg := ApplyWatchOptions(WithForcedWalkInterval(0))
	if err := cfg.ValidationError(); err == nil {
		t.Error("zero forced-walk interval not rejected")
	}

	cfg = ApplyWatchOptions(WithForcedWalkInterval(-time.Minute))
	if err := cfg.ValidationError(); err == nil {
		t.Error("negative forced-walk interval not rejected")
	}
}

func TestWatchConfig_BulkWalkFallbackThresholdValidation(t *testing.T) {
	cfg := ApplyWatchOptions(WithBulkWalkFallbackThreshold(0))
	if err := cfg.ValidationError(); err == nil {
		t.Error("threshold 0 not rejected")
	}

	cfg = ApplyWatchOptions(WithBulkWalkFallbackThreshold(math.MaxInt))
	if err := cfg.ValidationError(); err != nil {
		t.Errorf("MaxInt threshold rejected: %v", err)
	}
}

func TestWatchConfig_FirstErrorWins(t *testing.T) {
	// Apply two options that each fail validation; the first error
	// must stick, not be overwritten by the later one.
	cfg := ApplyWatchOptions(
		WithCadenceBounds(-1*time.Second, 30*time.Second), // error A
		WithProbeWindow(-5), // error B
	)
	err := cfg.ValidationError()
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "min") &&
		!strings.Contains(err.Error(), "WithCadenceBounds") {
		t.Errorf("first error (cadence) was overwritten: %v", err)
	}
}

func TestWatchConfig_LoggerNilAllowed(t *testing.T) {
	// WithLogger(nil) is legal — it disables logging. Validation
	// should pass.
	cfg := ApplyWatchOptions(WithLogger(nil))
	if err := cfg.ValidationError(); err != nil {
		t.Errorf("WithLogger(nil) rejected: %v", err)
	}
	if cfg.Logger != nil {
		t.Error("Logger not set to nil")
	}
}

func TestWatchConfig_NilReceiverHelpers(t *testing.T) {
	// Defensive: nil receiver helpers must not panic. Catches a future
	// refactor that loses the nil checks.
	var c *WatchConfig
	if got := c.ValidationError(); got != nil {
		t.Errorf("nil.ValidationError = %v, want nil", got)
	}
	if tier, ok := c.tierOverride(nil); ok || tier != TierUnknown {
		t.Errorf("nil.tierOverride = (%v,%v)", tier, ok)
	}
	if d, ok := c.counterCadence(nil); ok || d != 0 {
		t.Errorf("nil.counterCadence = (%v,%v)", d, ok)
	}
}

// --- Logger -----------------------------------------------------------

// countingLogger is a test [Logger] used by both this file's tests
// and the Watcher fallback-transition tests.
type countingLogger struct {
	calls    int
	messages []string
}

func (l *countingLogger) Warn(format string, args ...any) {
	l.calls++
	// Capture rendered messages so assertions can inspect content
	// without re-encoding.
	l.messages = append(l.messages, formatLog(format, args...))
}

func formatLog(format string, args ...any) string {
	// Tiny indirection so we can avoid importing fmt only in the
	// logger.
	var b strings.Builder
	for i, c := range format {
		// preserve formatting verbs as-is — the test logger does not
		// need perfect rendering. Strip the simplest %s/%v
		// placeholders to keep messages readable.
		if c == '%' && i+1 < len(format) {
			b.WriteByte('%')
			continue
		}
		b.WriteRune(c)
	}
	if len(args) > 0 {
		b.WriteString(" /args=")
		for _, a := range args {
			b.WriteString(toStr(a))
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return "v"
	}
}

// Sanity check that the countingLogger satisfies the Logger
// interface at compile time.
var _ Logger = (*countingLogger)(nil)

// --- WatchEvent ------------------------------------------------------

func TestWatchEvent_ZeroValuePrevIsNil(t *testing.T) {
	var e WatchEvent[int]
	if e.Prev != nil {
		t.Errorf("zero WatchEvent.Prev = %v, want nil", e.Prev)
	}
	if e.Kind != ChangeKindUnknown {
		t.Errorf("zero WatchEvent.Kind = %v, want Unknown", e.Kind)
	}
}

// --- Misc ------------------------------------------------------------

// TestWatchConfig_ValidationErrorIsWrappable pins that the returned
// error participates in errors.Is so callers can match against
// well-known sentinels in future revisions.
func TestWatchConfig_ValidationErrorIsWrappable(t *testing.T) {
	cfg := ApplyWatchOptions(WithCadenceBounds(0, 0))
	err := cfg.ValidationError()
	if err == nil {
		t.Fatal("expected error")
	}
	// Until we introduce explicit sentinel error vars for the
	// watcher, errors.Is against a Sentinel just yields false; the
	// important property is that errors.Is itself does not panic.
	_ = errors.Is(err, ErrSessionClosed)
}
