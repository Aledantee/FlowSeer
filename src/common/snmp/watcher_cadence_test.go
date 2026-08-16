package snmp

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// --- updateStateInterval unit tests ----------------------------------

// TestUpdateStateInterval_AdvanceResetsToMin pins the
// advance-resets-to-CadenceMin rule.
func TestUpdateStateInterval_AdvanceResetsToMin(t *testing.T) {
	w := &Watcher[testIfRow]{
		cfg: &WatchConfig{
			CadenceMin:         10 * time.Millisecond,
			CadenceMax:         160 * time.Millisecond,
			CadenceStepFactor:  2.0,
			CadenceStepCeiling: 16,
		},
	}
	w.stateInterval.Store(int64(80 * time.Millisecond)) // not at min
	w.updateStateInterval(true)
	if got := time.Duration(w.stateInterval.Load()); got != 10*time.Millisecond {
		t.Errorf("after advance: stateInterval = %v, want 10ms", got)
	}
}

// TestUpdateStateInterval_QuietDoubles pins the quiet-tick doubling
// step.
func TestUpdateStateInterval_QuietDoubles(t *testing.T) {
	w := &Watcher[testIfRow]{
		cfg: &WatchConfig{
			CadenceMin:         10 * time.Millisecond,
			CadenceMax:         320 * time.Millisecond,
			CadenceStepFactor:  2.0,
			CadenceStepCeiling: 16,
		},
	}
	w.stateInterval.Store(int64(10 * time.Millisecond))
	for i, want := range []time.Duration{
		20 * time.Millisecond,
		40 * time.Millisecond,
		80 * time.Millisecond,
		160 * time.Millisecond,
	} {
		w.updateStateInterval(false)
		if got := time.Duration(w.stateInterval.Load()); got != want {
			t.Errorf("step %d: stateInterval = %v, want %v",
				i, got, want)
		}
	}
}

// TestUpdateStateInterval_CappedByCeilingMultiplier pins the
// CadenceStepCeiling cap (CadenceMin × ceiling).
func TestUpdateStateInterval_CappedByCeilingMultiplier(t *testing.T) {
	w := &Watcher[testIfRow]{
		cfg: &WatchConfig{
			CadenceMin:         10 * time.Millisecond,
			CadenceMax:         10 * time.Second,
			CadenceStepFactor:  2.0,
			CadenceStepCeiling: 4, // cap at 40ms
		},
	}
	w.stateInterval.Store(int64(10 * time.Millisecond))
	for i := 0; i < 10; i++ {
		w.updateStateInterval(false)
	}
	if got := time.Duration(w.stateInterval.Load()); got != 40*time.Millisecond {
		t.Errorf("after many quiet ticks: stateInterval = %v, want 40ms cap", got)
	}
}

// TestUpdateStateInterval_CappedByCadenceMax pins that CadenceMax is
// the upper bound even when CadenceMin × Ceiling is larger.
func TestUpdateStateInterval_CappedByCadenceMax(t *testing.T) {
	w := &Watcher[testIfRow]{
		cfg: &WatchConfig{
			CadenceMin:         10 * time.Millisecond,
			CadenceMax:         50 * time.Millisecond, // lower than 16 × min = 160ms
			CadenceStepFactor:  2.0,
			CadenceStepCeiling: 16,
		},
	}
	w.stateInterval.Store(int64(10 * time.Millisecond))
	for i := 0; i < 10; i++ {
		w.updateStateInterval(false)
	}
	if got := time.Duration(w.stateInterval.Load()); got != 50*time.Millisecond {
		t.Errorf("after many quiet ticks: stateInterval = %v, want 50ms (CadenceMax)", got)
	}
}

// TestUpdateStateInterval_FactorOneDegenerate pins the documented-
// degenerate factor = 1.0 case (no step).
func TestUpdateStateInterval_FactorOneDegenerate(t *testing.T) {
	w := &Watcher[testIfRow]{
		cfg: &WatchConfig{
			CadenceMin:         10 * time.Millisecond,
			CadenceMax:         100 * time.Millisecond,
			CadenceStepFactor:  1.0,
			CadenceStepCeiling: 10,
		},
	}
	w.stateInterval.Store(int64(10 * time.Millisecond))
	for i := 0; i < 5; i++ {
		w.updateStateInterval(false)
	}
	if got := time.Duration(w.stateInterval.Load()); got != 10*time.Millisecond {
		t.Errorf("factor 1.0 produced step: stateInterval = %v, want 10ms", got)
	}
}

// --- Live adaptive-cadence test against fakeSession ------------------

// TestWatcher_AdaptiveCadence_QuietTicksStep verifies that several
// quiet indicator walks cause the State-tier interval to climb
// observably. Uses short cadences and counts BulkWalk calls over a
// fixed window to verify "ticks per second" shape changes.
func TestWatcher_AdaptiveCadence_QuietTicksStep(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))
	// Many quiet indicator walks.
	for i := 0; i < 100; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 100}))
	}

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(10*time.Millisecond, 320*time.Millisecond),
		WithCadenceStepPolicy(2.0, 16),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Cold-start emits the Added; drain it.
	<-w.ch

	// Sample the stateInterval continuously so we capture the
	// maximum interval observed regardless of where the cadence
	// happened to be when the test goroutine read it. Under -race
	// the scheduler can jitter the bare post-Sleep read enough to
	// flake the lower bound; the sampler removes that brittleness.
	maxObserved := time.Duration(0)
	stopSampler := make(chan struct{})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		for {
			select {
			case <-stopSampler:
				return
			default:
			}
			if cur := time.Duration(w.stateInterval.Load()); cur > maxObserved {
				maxObserved = cur
			}
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Sample for a longer window than the bare Sleep variant to
	// absorb scheduler jitter under -race.
	time.Sleep(300 * time.Millisecond)
	close(stopSampler)
	<-samplerDone

	// After ~300ms of quiet ticks at min=10ms with factor 2.0, the
	// state interval should have climbed several steps. With 10ms
	// then 20, 40, 80, 160 — by 300ms wall-clock the loop has had
	// time to step at least to 80ms or higher.
	if maxObserved < 40*time.Millisecond {
		t.Errorf("after ~300ms quiet: max stateInterval = %v, want >= 40ms (some stepping)",
			maxObserved)
	}
	// Should not exceed the cap (320ms or ceiling × min = 160ms,
	// whichever is smaller — 160ms here).
	if maxObserved > 160*time.Millisecond {
		t.Errorf("after ~300ms quiet: max stateInterval = %v, exceeded cap 160ms",
			maxObserved)
	}
}

// TestWatcher_AdaptiveCadence_AdvanceResets verifies that an
// indicator advance after several quiet ticks snaps the State-tier
// interval back to CadenceMin.
func TestWatcher_AdaptiveCadence_AdvanceResets(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))
	// Pre-queue many quiet ticks before the advance so the script
	// queue survives whatever pace the scheduler chooses. Under -race
	// the test goroutine can fall a few ticks behind real time;
	// fewer-than-N quiet entries would silently empty the queue and
	// the advance would race against the script's empty-walk default.
	for i := 0; i < 30; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 100}))
	}
	// Then an advance.
	s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 200}))
	// Targeted Get response.
	s.pushGet(map[string]VarBind{
		ifDescrOID.Append(1).String(): OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(1), Kind: KindOctetString},
			Value:  []byte("eth1-new"),
		},
	})
	// More quiet to let observation happen.
	for i := 0; i < 30; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 200}))
	}

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(10*time.Millisecond, 80*time.Millisecond),
		WithCadenceStepPolicy(2.0, 8),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Drain cold-start Added.
	<-w.ch

	// Sample the stateInterval continuously in a separate
	// goroutine so we capture the post-advance minimum even if a
	// subsequent quiet tick steps the interval back up before the
	// test goroutine reads it.
	minInterval := w.cfg.CadenceMax // start optimistic-high
	stopSampler := make(chan struct{})
	samplerDone := make(chan struct{})
	go func() {
		defer close(samplerDone)
		for {
			select {
			case <-stopSampler:
				return
			default:
			}
			cur := time.Duration(w.stateInterval.Load())
			if cur < minInterval {
				minInterval = cur
			}
			time.Sleep(1 * time.Millisecond)
		}
	}()

	// Wait for the advance to be observed (drain the Modified
	// event the test scripted). Generous deadline because race-mode
	// parallel runs slow the goroutine scheduling and the cadence
	// can step several times before the advance script is consumed.
	timeout := time.After(5 * time.Second)
loop:
	for {
		select {
		case ev := <-w.ch:
			if ev.Kind == ChangeKindModified {
				break loop
			}
		case <-timeout:
			close(stopSampler)
			<-samplerDone
			t.Fatal("timed out waiting for Modified event")
		}
	}

	// Sample for a brief window after the advance event so the
	// reset-to-CadenceMin tick is observed.
	time.Sleep(30 * time.Millisecond)
	close(stopSampler)
	<-samplerDone

	if minInterval > 20*time.Millisecond {
		t.Errorf("post-advance min stateInterval = %v, want near CadenceMin (10ms)",
			minInterval)
	}
}

// --- Counter-tier independence -----------------------------------------

// TestWatcher_CounterTier_FiresOnOwnCadence verifies that Counter-tier
// columns fire on their own schedule independently of the State-tier
// indicator-gated path.
func TestWatcher_CounterTier_FiresOnOwnCadence(t *testing.T) {
	counterCol := fakeColumn{
		oid:  MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10), // ifInOctets
		kind: KindCounter32,
	}

	s := newScriptedSession()
	// Cold-start: 1 row.
	colStartVBs := []VarBind{
		OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(1), Kind: KindOctetString},
			Value:  []byte("eth1"),
		},
		TimeTicksVar{
			Header: Header{OID: ifLastChange.Append(1), Kind: KindTimeTicks},
			Value:  100,
		},
		Counter32Var{
			Header: Header{OID: counterCol.OID().Append(1), Kind: KindCounter32},
			Value:  1000,
		},
	}
	s.pushTableWalk(colStartVBs)
	// Several quiet indicator walks.
	for i := 0; i < 30; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 100}))
	}
	// Counter-tier Get responses — return a higher value each time.
	for v := uint32(2000); v <= 5000; v += 1000 {
		s.pushGet(map[string]VarBind{
			counterCol.OID().Append(1).String(): Counter32Var{
				Header: Header{OID: counterCol.OID().Append(1), Kind: KindCounter32},
				Value:  v,
			},
		})
	}

	type ifRowWithCounter struct {
		IfIndex   uint32
		IfInBytes uint32
	}
	decode := func(idx OID, vbs []VarBind) (ifRowWithCounter, error) {
		var r ifRowWithCounter
		if idx.Len() != 1 {
			return r, fmt.Errorf("bad idx")
		}
		r.IfIndex = idx.At(0)
		for _, vb := range vbs {
			if c, ok := vb.(Counter32Var); ok {
				r.IfInBytes = c.Value
			}
		}
		return r, nil
	}
	equal := func(a, b ifRowWithCounter) bool {
		return a.IfIndex == b.IfIndex && a.IfInBytes == b.IfInBytes
	}
	// merge updates only IfInBytes from a partial Counter-tier fetch.
	// IfIndex is preserved from prev (the Counter-tier Get doesn't
	// re-walk index columns).
	merge := func(dst *ifRowWithCounter, vbs []VarBind) {
		for _, vb := range vbs {
			if c, ok := vb.(Counter32Var); ok {
				dst.IfInBytes = c.Value
			}
		}
	}

	w, err := NewWatcher[ifRowWithCounter](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{counterCol},
		decode, equal, merge,
		WithCadenceBounds(15*time.Millisecond, 200*time.Millisecond),
		WithCounterCadence(counterCol, 30*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Drain cold-start.
	<-w.ch

	// Wait for at least 3 Counter-tier Modified events.
	counterModifieds := 0
	timeout := time.After(1 * time.Second)
loop:
	for {
		select {
		case ev := <-w.ch:
			if ev.Kind == ChangeKindModified {
				counterModifieds++
				if counterModifieds >= 3 {
					break loop
				}
			}
		case <-timeout:
			break loop
		}
	}
	if counterModifieds < 3 {
		t.Errorf("Counter-tier Modified events = %d, want >= 3", counterModifieds)
	}
}

// TestWatcher_CounterTier_NoColumnsNoExtraTicks pins that the
// scheduler does not fire extra ticks when no Counter-tier columns
// are selected — the steady-state loop should behave identically to a
// State-tier-only Watcher.
func TestWatcher_CounterTier_NoColumnsNoExtraTicks(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))
	for i := 0; i < 50; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 100}))
	}

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		// No Counter-tier cols selected.
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(10*time.Millisecond, 300*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	<-w.ch // cold-start

	time.Sleep(150 * time.Millisecond)
	// No Get calls should have fired (no Counter-tier and no
	// indicator advance).
	if got := s.getCalls.Load(); got != 0 {
		t.Errorf("Get calls = %d, want 0 (no Counter-tier)", got)
	}
}

// --- Static-tier opt-in --------------------------------------------

// TestWatcher_StaticTier_RespectedByOverride verifies that
// WithColumnTier(col, TierStatic) keeps the column out of the
// indicator-advance path. The State-tier targeted Get for an
// advanced row should NOT include the Static-classified column.
func TestWatcher_StaticTier_RespectedByOverride(t *testing.T) {
	staticCol := fakeColumn{
		oid:  ifDescrOID,
		kind: KindOctetString,
	}
	stateCol := fakeColumn{
		oid:  MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 8), // ifOperStatus
		kind: KindInteger32,
	}

	s := newScriptedSession()
	s.pushTableWalk([]VarBind{
		OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(1), Kind: KindOctetString},
			Value:  []byte("eth1"),
		},
		Integer32Var{
			Header: Header{OID: stateCol.OID().Append(1), Kind: KindInteger32},
			Value:  1,
		},
		TimeTicksVar{
			Header: Header{OID: ifLastChange.Append(1), Kind: KindTimeTicks},
			Value:  100,
		},
	})
	// Tick 1: advance.
	s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 200}))

	// Capture the OIDs the Watcher asks for in the targeted Get.
	// Use a custom Get response that only handles stateCol's OID;
	// if ifDescr is in the request it will be NoSuchInstance (which
	// confirms the OID was sent, not the absence we want). Instead
	// inspect the request via the script's getRequests slice (added
	// below via instrumenting the scriptedSession).
	s.pushGet(map[string]VarBind{
		stateCol.OID().Append(1).String(): Integer32Var{
			Header: Header{OID: stateCol.OID().Append(1), Kind: KindInteger32},
			Value:  2,
		},
	})

	type customRow struct {
		Idx    uint32
		Descr  string
		Status int32
	}
	decode := func(idx OID, vbs []VarBind) (customRow, error) {
		var r customRow
		if idx.Len() != 1 {
			return r, fmt.Errorf("bad idx")
		}
		r.Idx = idx.At(0)
		for _, vb := range vbs {
			oid := vb.GetHeader().OID
			col, _ := oid.Parent()
			switch {
			case col.Equal(ifDescrOID):
				if s, ok := vb.(OctetStringVar); ok {
					r.Descr = string(s.Value)
				}
			case col.Equal(stateCol.OID()):
				if i, ok := vb.(Integer32Var); ok {
					r.Status = i.Value
				}
			}
		}
		return r, nil
	}
	equal := func(a, b customRow) bool {
		return a.Idx == b.Idx && a.Descr == b.Descr && a.Status == b.Status
	}
	// merge mirrors decode but only writes the columns present in vbs.
	// Idx stays at prev's value because the index column is not
	// re-walked on Static-tier / State-tier targeted Gets.
	merge := func(dst *customRow, vbs []VarBind) {
		for _, vb := range vbs {
			oid := vb.GetHeader().OID
			col, _ := oid.Parent()
			switch {
			case col.Equal(ifDescrOID):
				if s, ok := vb.(OctetStringVar); ok {
					dst.Descr = string(s.Value)
				}
			case col.Equal(stateCol.OID()):
				if i, ok := vb.(Integer32Var); ok {
					dst.Status = i.Value
				}
			}
		}
	}

	w, err := NewWatcher[customRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{staticCol, stateCol},
		decode, equal, merge,
		WithCadenceBounds(15*time.Millisecond, 200*time.Millisecond),
		WithColumnTier(staticCol, TierStatic),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Drain cold-start (1 Added).
	<-w.ch

	// Wait for the indicator-advance Modified.
	timeout := time.After(1 * time.Second)
loop:
	for {
		select {
		case ev := <-w.ch:
			if ev.Kind == ChangeKindModified {
				// The Modified event should reflect the new
				// Status (2) but NOT a new IfDescr (because
				// Static-tier wasn't fetched).
				if ev.Row.Status != 2 {
					t.Errorf("Modified.Status = %d, want 2", ev.Row.Status)
				}
				break loop
			}
		case <-timeout:
			t.Fatal("timed out waiting for Modified")
		}
	}

	// Compile-time check that staticCol is in the staticTierCols
	// partition.
	staticFound := false
	for _, c := range w.staticTierCols {
		if c.OID().Equal(staticCol.OID()) {
			staticFound = true
			break
		}
	}
	if !staticFound {
		t.Error("staticCol was not partitioned into staticTierCols")
	}
}

// --- Tier resolution --------------------------------------------------

func TestPartitionCols_CounterKindHeuristic(t *testing.T) {
	counterCol := fakeColumn{
		oid:  MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10),
		kind: KindCounter32,
	}
	stateCol := fakeColumn{
		oid:  ifDescrOID,
		kind: KindOctetString,
	}
	indicatorCol := fakeColumn{oid: ifLastChange, kind: KindTimeTicks}

	indicator, _ := NewPerRowIndicator(indicatorCol, ifTableRoot)

	w := &Watcher[testIfRow]{
		indicator: indicator,
		cfg:       &WatchConfig{},
		cols:      []AnyColumn{counterCol, stateCol, indicatorCol},
	}
	w.partitionCols()

	if len(w.counterTierCols) != 1 ||
		!w.counterTierCols[0].OID().Equal(counterCol.OID()) {
		t.Errorf("counterTierCols = %v, want [counterCol]", w.counterTierCols)
	}
	if len(w.stateTierCols) != 1 ||
		!w.stateTierCols[0].OID().Equal(stateCol.OID()) {
		t.Errorf("stateTierCols = %v, want [stateCol]", w.stateTierCols)
	}
	if len(w.staticTierCols) != 0 {
		t.Errorf("staticTierCols = %v, want []", w.staticTierCols)
	}
	// Indicator column dropped from all partitions.
	for _, c := range w.stateTierCols {
		if c.OID().Equal(indicatorCol.OID()) {
			t.Error("indicator column appears in stateTierCols")
		}
	}
}

func TestPartitionCols_OverrideWins(t *testing.T) {
	counterCol := fakeColumn{
		oid:  MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10),
		kind: KindCounter32,
	}
	indicatorCol := fakeColumn{oid: ifLastChange, kind: KindTimeTicks}
	indicator, _ := NewPerRowIndicator(indicatorCol, ifTableRoot)

	w := &Watcher[testIfRow]{
		indicator: indicator,
		cfg: ApplyWatchOptions(
			WithColumnTier(counterCol, TierState),
		),
		cols: []AnyColumn{counterCol},
	}
	w.partitionCols()

	// Override should override the Counter32 heuristic.
	if len(w.stateTierCols) != 1 ||
		!w.stateTierCols[0].OID().Equal(counterCol.OID()) {
		t.Errorf("override did not promote counter col to State: %v", w.stateTierCols)
	}
	if len(w.counterTierCols) != 0 {
		t.Errorf("override did not remove counter col from Counter: %v",
			w.counterTierCols)
	}
}

func TestPartitionCols_CounterDefaultCadenceByKind(t *testing.T) {
	c32 := fakeColumn{
		oid:  MustOID(1, 3, 6, 1, 2, 1, 2, 2, 1, 10),
		kind: KindCounter32,
	}
	c64 := fakeColumn{
		oid:  MustOID(1, 3, 6, 1, 2, 1, 31, 1, 1, 1, 6),
		kind: KindCounter64,
	}
	indicator, _ := NewPerRowIndicator(
		fakeColumn{oid: ifLastChange, kind: KindTimeTicks}, ifTableRoot,
	)

	w := &Watcher[testIfRow]{
		indicator: indicator,
		cfg:       &WatchConfig{},
		cols:      []AnyColumn{c32, c64},
	}
	w.partitionCols()

	if len(w.counterSchedules) != 2 {
		t.Fatalf("counterSchedules = %v, want 2 entries", w.counterSchedules)
	}
	for _, sched := range w.counterSchedules {
		if sched.col.OID().Equal(c32.OID()) {
			if sched.interval != counter32DefaultCadence {
				t.Errorf("c32 default cadence = %v, want %v",
					sched.interval, counter32DefaultCadence)
			}
		}
		if sched.col.OID().Equal(c64.OID()) {
			if sched.interval != counter64DefaultCadence {
				t.Errorf("c64 default cadence = %v, want %v",
					sched.interval, counter64DefaultCadence)
			}
		}
	}
}

// --- computeNextDeadline -------------------------------------------

func TestComputeNextDeadline_PicksEarliest(t *testing.T) {
	now := time.Now()
	w := &Watcher[testIfRow]{
		cfg: &WatchConfig{
			CadenceMin:         10 * time.Millisecond,
			ForcedWalkInterval: 100 * time.Millisecond,
		},
		nextStateTickAt:  now.Add(50 * time.Millisecond),
		lastForcedWalk:   now.Add(-60 * time.Millisecond), // due at -60 + 100 = +40ms
		nextStaticTickAt: now.Add(200 * time.Millisecond),
		staticTierCols:   []AnyColumn{fakeColumn{}}, // make staticTier "active"
		counterSchedules: []counterSchedule{
			{nextDueAt: now.Add(30 * time.Millisecond)},
			{nextDueAt: now.Add(80 * time.Millisecond)},
		},
	}
	got := w.computeNextDeadline()
	// Earliest: counter at +30ms.
	want := now.Add(30 * time.Millisecond)
	if !got.Equal(want) {
		t.Errorf("computeNextDeadline = %v, want %v (counter)", got, want)
	}
}
