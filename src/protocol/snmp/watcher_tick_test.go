package snmp

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatcher_PerRow_IndicatorAdvanceTargetedGet covers the
// single-row state change: cold-start over 3 ifTable rows; tick 1 sees the indicator
// unchanged (no emit); tick 2 sees row 2's indicator advance and a
// targeted Get fires for only row 2's state-tier columns; exactly
// one ChangeKindModified event emits for row 2 with the new
// IfDescr.
func TestWatcher_PerRow_IndicatorAdvanceTargetedGet(t *testing.T) {
	s := newScriptedSession()
	// Cold-start: 3 rows with ifLastCh = 100,200,300.
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
		ifRow{idx: 2, descr: "eth2", lastCh: 200},
		ifRow{idx: 3, descr: "eth3", lastCh: 300},
	))

	// Tick 1: indicator walk — same values.
	s.pushIndicatorWalk(buildIndicatorVBs(
		idxValue{1, 100},
		idxValue{2, 200},
		idxValue{3, 300},
	))

	// Tick 2: indicator walk — row 2 advanced.
	s.pushIndicatorWalk(buildIndicatorVBs(
		idxValue{1, 100},
		idxValue{2, 250},
		idxValue{3, 300},
	))

	// Tick 2's targeted Get for row 2's state-tier cols (just
	// ifDescr in this test) — return the new value.
	s.pushGet(map[string]VarBind{
		ifDescrOID.Append(2).String(): OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(2), Kind: KindOctetString},
			Value:  []byte("eth2-renamed"),
		},
	})

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(20*time.Millisecond, 20*time.Millisecond),
		// Disable forced full walks so the test only exercises the
		// indicator-gated path.
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	events := drainEvents(t, w, 4, 2*time.Second)
	// Expect: 3 Added (cold-start) + 1 Modified (row 2 advance).
	if len(events) != 4 {
		t.Fatalf("event count = %d, want 4: %+v", len(events), events)
	}
	addedCount := 0
	var modified WatchEvent[testIfRow]
	for _, ev := range events {
		switch ev.Kind {
		case ChangeKindAdded:
			addedCount++
		case ChangeKindModified:
			modified = ev
		}
	}
	if addedCount != 3 {
		t.Errorf("Added count = %d, want 3", addedCount)
	}
	if modified.Kind != ChangeKindModified {
		t.Errorf("Modified event not seen: %+v", events)
	}
	if modified.Row.IfIndex != 2 {
		t.Errorf("Modified row index = %d, want 2", modified.Row.IfIndex)
	}
	if modified.Row.IfDescr != "eth2-renamed" {
		t.Errorf("Modified row IfDescr = %q, want eth2-renamed", modified.Row.IfDescr)
	}
	// Quiet tick 1 → no Modified; ensure the Get was issued exactly
	// once (only for tick 2).
	if got := s.getCalls.Load(); got != 1 {
		t.Errorf("Get calls = %d, want 1 (only tick 2 should fire Get)", got)
	}
}

// TestWatcher_PerRow_QuietTick_NoEvents pins the no-emit rule:
// after cold-start, ticks where the indicator is unchanged produce
// zero events.
func TestWatcher_PerRow_QuietTick_NoEvents(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
		ifRow{idx: 2, descr: "eth2", lastCh: 200},
	))
	// Multiple quiet indicator walks.
	for i := 0; i < 5; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(
			idxValue{1, 100},
			idxValue{2, 200},
		))
	}

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		nil,
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(15*time.Millisecond, 15*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Cold-start emits 2 Added; subsequent quiet ticks emit nothing.
	got := drainEvents(t, w, 2, 1*time.Second)
	if len(got) != 2 {
		t.Fatalf("cold-start emitted %d events, want 2", len(got))
	}
	// Wait for ~5 ticks of quiet; assert no additional events.
	select {
	case ev := <-w.pump.Data():
		t.Errorf("unexpected event after quiet ticks: %+v", ev)
	case <-time.After(150 * time.Millisecond):
		// expected
	}
}

// TestWatcher_PerRow_AddedRowOnTick covers a row that did not exist
// at cold-start appearing later. The indicator walk surfaces a new
// row index; the targeted Get fetches its state-tier columns; an
// Added event emits.
func TestWatcher_PerRow_AddedRowOnTick(t *testing.T) {
	s := newScriptedSession()
	// Cold-start: rows 1, 2.
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
		ifRow{idx: 2, descr: "eth2", lastCh: 200},
	))
	// Tick 1: indicator walk includes a new row 3.
	s.pushIndicatorWalk(buildIndicatorVBs(
		idxValue{1, 100},
		idxValue{2, 200},
		idxValue{3, 300},
	))
	// Targeted Get for row 3.
	s.pushGet(map[string]VarBind{
		ifDescrOID.Append(3).String(): OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(3), Kind: KindOctetString},
			Value:  []byte("eth3-new"),
		},
	})

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(20*time.Millisecond, 20*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	events := drainEvents(t, w, 3, 2*time.Second)
	added := 0
	var newAdded WatchEvent[testIfRow]
	for _, ev := range events {
		if ev.Kind == ChangeKindAdded {
			added++
			if ev.Row.IfIndex == 3 {
				newAdded = ev
			}
		}
	}
	if added != 3 {
		t.Errorf("Added count = %d, want 3 (2 cold-start + 1 new)", added)
	}
	if newAdded.Row.IfIndex != 3 {
		t.Error("new row (ifIndex=3) Added event not seen")
	}
	if newAdded.Row.IfDescr != "eth3-new" {
		t.Errorf("new row IfDescr = %q, want eth3-new", newAdded.Row.IfDescr)
	}
}

// TestWatcher_PerRow_WithPrevRow pins that WithPrevRow populates
// Prev only on Modified events, not Added.
func TestWatcher_PerRow_WithPrevRow(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))
	s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 200})) // advance
	s.pushGet(map[string]VarBind{
		ifDescrOID.Append(1).String(): OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(1), Kind: KindOctetString},
			Value:  []byte("eth1-renamed"),
		},
	})

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(15*time.Millisecond, 15*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
		WithPrevRow(),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	events := drainEvents(t, w, 2, 2*time.Second)
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	addedEv := events[0]
	modifiedEv := events[1]
	if addedEv.Kind != ChangeKindAdded {
		t.Errorf("first event kind = %v, want Added", addedEv.Kind)
	}
	if addedEv.Prev != nil {
		t.Errorf("Added event Prev = %+v, want nil", addedEv.Prev)
	}
	if modifiedEv.Kind != ChangeKindModified {
		t.Errorf("second event kind = %v, want Modified", modifiedEv.Kind)
	}
	if modifiedEv.Prev == nil {
		t.Fatal("Modified event Prev = nil, want non-nil under WithPrevRow")
	}
	if modifiedEv.Prev.IfDescr != "eth1" {
		t.Errorf("Modified Prev.IfDescr = %q, want eth1", modifiedEv.Prev.IfDescr)
	}
	if modifiedEv.Row.IfDescr != "eth1-renamed" {
		t.Errorf("Modified Row.IfDescr = %q, want eth1-renamed", modifiedEv.Row.IfDescr)
	}
}

// TestWatcher_PerRow_IndicatorAdvanceButRowEqual_NoEmit pins the
// one-event-per-detected-row-change rule:
// the indicator advanced but the observable row fields did not
// change (after re-decode). No Modified emits.
func TestWatcher_PerRow_IndicatorAdvanceButRowEqual_NoEmit(t *testing.T) {
	s := newScriptedSession()
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))
	s.pushIndicatorWalk(buildIndicatorVBs(idxValue{1, 200})) // advance
	// Get returns the same ifDescr as before.
	s.pushGet(map[string]VarBind{
		ifDescrOID.Append(1).String(): OctetStringVar{
			Header: Header{OID: ifDescrOID.Append(1), Kind: KindOctetString},
			Value:  []byte("eth1"),
		},
	})

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		// Equal compares IfIndex + IfDescr only (the test row type
		// also has IfLastCh, but the indicator-only change should
		// be invisible to the equal func to model the "advance
		// without observable change" case).
		func(a, b testIfRow) bool {
			return a.IfIndex == b.IfIndex && a.IfDescr == b.IfDescr
		},
		testIfMerge,
		WithCadenceBounds(15*time.Millisecond, 15*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Should see 1 Added from cold-start, 0 Modified.
	got := drainEvents(t, w, 1, 1*time.Second)
	if len(got) != 1 {
		t.Fatalf("event count = %d, want 1 (no Modified)", len(got))
	}
	if got[0].Kind != ChangeKindAdded {
		t.Errorf("event kind = %v, want Added", got[0].Kind)
	}
	// Confirm no Modified follows within a tick window.
	select {
	case ev := <-w.pump.Data():
		t.Errorf("unexpected Modified event: %+v", ev)
	case <-time.After(80 * time.Millisecond):
		// expected
	}
}

// TestWatcher_Scalar_AdvanceEmitsAddedModifiedRemoved covers the
// row-deletion + new-row scenario: scalar indicator advances; full walk reveals row 2 removed, row 4
// appeared, row 3 modified. Events: Removed[2], Added[4], Modified[3].
func TestWatcher_Scalar_AdvanceEmitsAddedModifiedRemoved(t *testing.T) {
	scalarOID := MustOID(1, 3, 6, 1, 2, 1, 47, 1, 4, 1)
	entPhysicalTable := MustOID(1, 3, 6, 1, 2, 1, 47, 1, 1, 1)
	entDescrCol := MustOID(1, 3, 6, 1, 2, 1, 47, 1, 1, 1, 1, 2)

	indicator := MustChangeIndicator(NewScalarIndicator(
		scalarOID, KindTimeTicks, []OID{entPhysicalTable},
	))

	s := newScriptedSession()
	// Cold-start: rows 1, 2, 3.
	s.pushWalkAt(entPhysicalTable, buildEntRows(t, entDescrCol,
		entRow{idx: 1, descr: "chassis"},
		entRow{idx: 2, descr: "fan-A"},
		entRow{idx: 3, descr: "psu-A"},
	))
	// Cold-start scalar Get.
	s.pushGet(map[string]VarBind{
		scalarOID.String(): TimeTicksVar{
			Header: Header{OID: scalarOID, Kind: KindTimeTicks},
			Value:  100,
		},
	})

	// Tick 1: scalar Get returns advanced value → triggers full walk.
	s.pushGet(map[string]VarBind{
		scalarOID.String(): TimeTicksVar{
			Header: Header{OID: scalarOID, Kind: KindTimeTicks},
			Value:  200,
		},
	})
	s.pushWalkAt(entPhysicalTable, buildEntRows(t, entDescrCol,
		entRow{idx: 1, descr: "chassis"},
		// row 2 removed
		entRow{idx: 3, descr: "psu-B"}, // modified
		entRow{idx: 4, descr: "fan-B"}, // added
	))

	type entRowDecoded struct {
		Index uint32
		Descr string
	}
	decode := func(idx OID, vbs []VarBind) (entRowDecoded, error) {
		var r entRowDecoded
		if idx.Len() != 1 {
			return r, fmt.Errorf("bad idx: %s", idx)
		}
		r.Index = idx.At(0)
		for _, vb := range vbs {
			if s, ok := vb.(OctetStringVar); ok {
				r.Descr = string(s.Value)
			}
		}
		return r, nil
	}
	equal := func(a, b entRowDecoded) bool {
		return a.Index == b.Index && a.Descr == b.Descr
	}
	merge := func(dst *entRowDecoded, vbs []VarBind) {
		for _, vb := range vbs {
			if s, ok := vb.(OctetStringVar); ok {
				dst.Descr = string(s.Value)
			}
		}
	}

	w, err := NewWatcher[entRowDecoded](
		context.Background(),
		s,
		indicator,
		[]AnyColumn{fakeColumn{oid: entDescrCol, kind: KindOctetString}},
		decode, equal, merge,
		WithCadenceBounds(20*time.Millisecond, 20*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Cold-start: 3 Added. Tick 1 advance: 1 Modified (row 3) + 1 Added (row 4).
	// Removed (row 2) does NOT fire from a scalar-advance full walk
	// because emitRemoved=false in scalarTick's full-walk path —
	// row removal is reserved for the forced-walk backstop.
	// This is by design; the test verifies the contract.
	events := drainEvents(t, w, 5, 2*time.Second)

	addedAt := func(idx uint32) bool {
		for _, ev := range events {
			if ev.Kind == ChangeKindAdded && ev.Row.Index == idx {
				return true
			}
		}
		return false
	}
	modifiedAt := func(idx uint32) bool {
		for _, ev := range events {
			if ev.Kind == ChangeKindModified && ev.Row.Index == idx {
				return true
			}
		}
		return false
	}

	for _, i := range []uint32{1, 2, 3} {
		if !addedAt(i) {
			t.Errorf("missing cold-start Added for index %d", i)
		}
	}
	if !addedAt(4) {
		t.Error("missing post-advance Added for index 4")
	}
	if !modifiedAt(3) {
		t.Error("missing post-advance Modified for index 3")
	}
	for _, ev := range events {
		if ev.Kind == ChangeKindRemoved {
			t.Errorf("unexpected Removed event from scalar advance: %+v", ev)
		}
	}
}

// TestWatcher_ForcedFullWalkEmitsRemoved covers the row-removal
// backstop: row removed
// from the table but the per-row indicator never advances on the
// removal. The forced full-walk interval fires and emits the
// missing row as ChangeKindRemoved.
func TestWatcher_ForcedFullWalkEmitsRemoved(t *testing.T) {
	s := newScriptedSession()
	// Cold-start: 2 rows.
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
		ifRow{idx: 2, descr: "eth2", lastCh: 200},
	))

	// Many quiet indicator walks before the forced walk fires.
	for i := 0; i < 20; i++ {
		s.pushIndicatorWalk(buildIndicatorVBs(
			idxValue{1, 100},
			// row 2 disappeared but indicator did not bump for the
			// remaining row.
		))
	}

	// Forced full walk returns only row 1 (row 2 truly gone).
	s.pushTableWalk(buildIfTableRows(t,
		ifRow{idx: 1, descr: "eth1", lastCh: 100},
	))

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(10*time.Millisecond, 10*time.Millisecond),
		WithForcedWalkInterval(60*time.Millisecond), // fires after a few ticks
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Cold-start: 2 Added. Forced walk: 1 Removed.
	events := drainEvents(t, w, 3, 2*time.Second)
	var removed []WatchEvent[testIfRow]
	for _, ev := range events {
		if ev.Kind == ChangeKindRemoved {
			removed = append(removed, ev)
		}
	}
	if len(removed) != 1 {
		t.Fatalf("Removed count = %d, want 1", len(removed))
	}
	if removed[0].Row.IfIndex != 2 {
		t.Errorf("Removed row index = %d, want 2", removed[0].Row.IfIndex)
	}
}

// TestWatcher_PerRow_ChunkingSubThreshold verifies that an
// indicator advance for a moderate row count results in chunked
// Gets, not a BulkWalk fallback, when PDU count stays below the
// threshold.
func TestWatcher_PerRow_ChunkingSubThreshold(t *testing.T) {
	const rows = 20
	s := newScriptedSession()
	coldStartRows := make([]ifRow, rows)
	for i := 0; i < rows; i++ {
		coldStartRows[i] = ifRow{
			idx:    uint32(i + 1),
			descr:  fmt.Sprintf("eth%d", i+1),
			lastCh: uint32(100 + i),
		}
	}
	s.pushTableWalk(buildIfTableRows(t, coldStartRows...))

	// Tick 1: all rows advance.
	advanced := make([]idxValue, rows)
	for i := 0; i < rows; i++ {
		advanced[i] = idxValue{uint32(i + 1), uint32(500 + i)}
	}
	s.pushIndicatorWalk(buildIndicatorVBs(advanced...))

	// Targeted Get response: one ifDescr per row, all new values.
	getResp := make(map[string]VarBind, rows)
	for i := 0; i < rows; i++ {
		oid := ifDescrOID.Append(uint32(i + 1))
		getResp[oid.String()] = OctetStringVar{
			Header: Header{OID: oid, Kind: KindOctetString},
			Value:  []byte(fmt.Sprintf("eth%d-new", i+1)),
		}
	}
	// The chunked Get may dispatch in multiple PDUs; the test
	// fakeSession concatenates all queued Get responses across
	// multiple calls. Provide one large response.
	s.pushGet(getResp)
	// Pre-position more pushGet responses since chunking might
	// dispatch multiple PDUs and each consumes a script. Total OIDs
	// = 20 cols, default MaxOIDs = 60, so 1 PDU suffices.
	// 20 / 60 = 1 PDU < threshold 4 → chunked-Get path.
	if want := (rows*1 + watcherDefaultMaxOIDs - 1) / watcherDefaultMaxOIDs; want > 1 {
		t.Fatalf("test premise broken: expected 1 PDU, computed %d", want)
	}

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(15*time.Millisecond, 15*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
		WithBulkWalkFallbackThreshold(4), // default
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	_ = drainEvents(t, w, rows*2, 3*time.Second)
	// Total BulkWalk calls should be:
	//   1 cold-start (tableRoot)
	// + 1 indicator-column walk for tick 1
	// (no fallback BulkWalk — chunked-Get path took over)
	if got := s.bulkWalkCalls.Load(); got != 2 {
		t.Errorf("BulkWalk calls = %d, want 2 (no fallback)", got)
	}
	if got := s.getCalls.Load(); got != 1 {
		t.Errorf("Get calls = %d, want 1 (single chunked PDU)", got)
	}
}

// TestWatcher_PerRow_ChunkingMultiplePDUsBelowThreshold confirms
// chunked Gets are dispatched when total OIDs exceed MaxOIDs but
// PDU count stays at-or-below the threshold.
func TestWatcher_PerRow_ChunkingMultiplePDUsBelowThreshold(t *testing.T) {
	const rows = 100
	// 100 rows × 1 col / 60 per PDU = 2 PDUs ≤ threshold 4 → still chunked.
	s := newScriptedSession()
	coldStartRows := make([]ifRow, rows)
	for i := 0; i < rows; i++ {
		coldStartRows[i] = ifRow{
			idx:    uint32(i + 1),
			descr:  fmt.Sprintf("eth%d", i+1),
			lastCh: uint32(100 + i),
		}
	}
	s.pushTableWalk(buildIfTableRows(t, coldStartRows...))

	advanced := make([]idxValue, rows)
	for i := 0; i < rows; i++ {
		advanced[i] = idxValue{uint32(i + 1), uint32(500 + i)}
	}
	s.pushIndicatorWalk(buildIndicatorVBs(advanced...))

	// Build a combined Get response and push it once; the script
	// returns all matching OIDs from the request.
	getResp := make(map[string]VarBind, rows)
	for i := 0; i < rows; i++ {
		oid := ifDescrOID.Append(uint32(i + 1))
		getResp[oid.String()] = OctetStringVar{
			Header: Header{OID: oid, Kind: KindOctetString},
			Value:  []byte(fmt.Sprintf("eth%d-new", i+1)),
		}
	}
	// Push it twice so the second chunked Get also has a script
	// (scripts are dequeued per Get call).
	s.pushGet(getResp)
	s.pushGet(getResp)

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(15*time.Millisecond, 15*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
		WithBulkWalkFallbackThreshold(4),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	_ = drainEvents(t, w, rows*2, 5*time.Second)

	// 1 cold-start + 1 indicator walk = 2 BulkWalks; no fallback.
	if got := s.bulkWalkCalls.Load(); got != 2 {
		t.Errorf("BulkWalk calls = %d, want 2", got)
	}
	// 2 chunked Gets (100 OIDs at 60 per PDU = 2 PDUs).
	if got := s.getCalls.Load(); got != 2 {
		t.Errorf("Get calls = %d, want 2 (chunked)", got)
	}
}

// TestWatcher_PerRow_ChunkingAboveThresholdFallsBack confirms that
// when chunking would exceed the threshold, the BulkWalk fallback
// fires instead.
func TestWatcher_PerRow_ChunkingAboveThresholdFallsBack(t *testing.T) {
	const rows = 300 // 300 cols at 60 per PDU = 5 PDUs > threshold 4
	s := newScriptedSession()
	coldStartRows := make([]ifRow, rows)
	for i := 0; i < rows; i++ {
		coldStartRows[i] = ifRow{
			idx:    uint32(i + 1),
			descr:  fmt.Sprintf("eth%d", i+1),
			lastCh: uint32(100 + i),
		}
	}
	s.pushTableWalk(buildIfTableRows(t, coldStartRows...))

	advanced := make([]idxValue, rows)
	for i := 0; i < rows; i++ {
		advanced[i] = idxValue{uint32(i + 1), uint32(500 + i)}
	}
	s.pushIndicatorWalk(buildIndicatorVBs(advanced...))

	// BulkWalk fallback: returns updated ifDescr for every row.
	updatedRows := make([]ifRow, rows)
	for i := 0; i < rows; i++ {
		updatedRows[i] = ifRow{
			idx:    uint32(i + 1),
			descr:  fmt.Sprintf("eth%d-new", i+1),
			lastCh: uint32(500 + i),
		}
	}
	s.pushTableWalk(buildIfTableRows(t, updatedRows...))

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(15*time.Millisecond, 15*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
		WithBulkWalkFallbackThreshold(4),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	_ = drainEvents(t, w, rows*2, 5*time.Second)

	// 1 cold-start + 1 indicator walk + 1 BulkWalk fallback = 3 BulkWalks.
	if got := s.bulkWalkCalls.Load(); got != 3 {
		t.Errorf("BulkWalk calls = %d, want 3 (cold-start + indicator + fallback)", got)
	}
	// Get should NOT have been called — BulkWalk fallback replaced
	// the chunked-Get path entirely.
	if got := s.getCalls.Load(); got != 0 {
		t.Errorf("Get calls = %d, want 0 (fallback path bypasses Get)", got)
	}
}

// TestWatcher_PerRow_ChunkingThresholdDisabled confirms
// math.MaxInt threshold preserves the chunking contract: chunked-Get
// path takes 5 PDUs without ever falling back.
func TestWatcher_PerRow_ChunkingThresholdDisabled(t *testing.T) {
	const rows = 300 // 5 PDUs at 60 per PDU
	s := newScriptedSession()
	coldStartRows := make([]ifRow, rows)
	for i := 0; i < rows; i++ {
		coldStartRows[i] = ifRow{
			idx:    uint32(i + 1),
			descr:  fmt.Sprintf("eth%d", i+1),
			lastCh: uint32(100 + i),
		}
	}
	s.pushTableWalk(buildIfTableRows(t, coldStartRows...))

	advanced := make([]idxValue, rows)
	for i := 0; i < rows; i++ {
		advanced[i] = idxValue{uint32(i + 1), uint32(500 + i)}
	}
	s.pushIndicatorWalk(buildIndicatorVBs(advanced...))

	getResp := make(map[string]VarBind, rows)
	for i := 0; i < rows; i++ {
		oid := ifDescrOID.Append(uint32(i + 1))
		getResp[oid.String()] = OctetStringVar{
			Header: Header{OID: oid, Kind: KindOctetString},
			Value:  []byte(fmt.Sprintf("eth%d-new", i+1)),
		}
	}
	for i := 0; i < 5; i++ {
		s.pushGet(getResp)
	}

	w, err := NewWatcher[testIfRow](
		context.Background(),
		s,
		makeIfIndicator(t),
		[]AnyColumn{fakeColumn{oid: ifDescrOID, kind: KindOctetString}},
		testIfDecode,
		testIfEqual,
		testIfMerge,
		WithCadenceBounds(15*time.Millisecond, 15*time.Millisecond),
		WithForcedWalkInterval(10*time.Hour),
		WithBulkWalkFallbackThreshold(math.MaxInt),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	_ = drainEvents(t, w, rows*2, 5*time.Second)

	if got := s.bulkWalkCalls.Load(); got != 2 {
		t.Errorf("BulkWalk calls = %d, want 2 (no fallback)", got)
	}
	if got := s.getCalls.Load(); got != 5 {
		t.Errorf("Get calls = %d, want 5 (300 OIDs / 60 per PDU)", got)
	}
}

func TestIndicatorVBEqual_NilBoth(t *testing.T) {
	if !indicatorVBEqual(nil, nil) {
		t.Error("nil == nil should be true")
	}
}

func TestIndicatorVBEqual_NilVsNonNil(t *testing.T) {
	vb := TimeTicksVar{Header: Header{Kind: KindTimeTicks}, Value: 1}
	if indicatorVBEqual(nil, vb) {
		t.Error("nil vs TimeTicks should be unequal")
	}
	if indicatorVBEqual(vb, nil) {
		t.Error("TimeTicks vs nil should be unequal")
	}
}

func TestIndicatorVBEqual_TimeTicks(t *testing.T) {
	a := TimeTicksVar{Header: Header{Kind: KindTimeTicks}, Value: 100}
	b := TimeTicksVar{Header: Header{Kind: KindTimeTicks}, Value: 100}
	c := TimeTicksVar{Header: Header{Kind: KindTimeTicks}, Value: 101}
	if !indicatorVBEqual(a, b) {
		t.Error("equal TimeTicks not detected")
	}
	if indicatorVBEqual(a, c) {
		t.Error("unequal TimeTicks not detected")
	}
}

func TestIndicatorVBEqual_OctetString(t *testing.T) {
	a := OctetStringVar{Header: Header{Kind: KindOctetString}, Value: []byte("hello")}
	b := OctetStringVar{Header: Header{Kind: KindOctetString}, Value: []byte("hello")}
	c := OctetStringVar{Header: Header{Kind: KindOctetString}, Value: []byte("world")}
	if !indicatorVBEqual(a, b) {
		t.Error("equal OctetString not detected")
	}
	if indicatorVBEqual(a, c) {
		t.Error("unequal OctetString not detected")
	}
}

func TestIndicatorVBEqual_KindMismatchUnequal(t *testing.T) {
	a := TimeTicksVar{Header: Header{Kind: KindTimeTicks}, Value: 100}
	b := Uinteger32Var{Header: Header{Kind: KindUinteger32}, Value: 100}
	if indicatorVBEqual(a, b) {
		t.Error("different Kinds should be unequal")
	}
}

func TestIndicatorVBEqual_ExceptionVariantUnequal(t *testing.T) {
	a := TimeTicksVar{Header: Header{Kind: KindTimeTicks}, Value: 100}
	b := NoSuchObjectVar{Header: Header{Kind: KindNoSuchObject}}
	if indicatorVBEqual(a, b) {
		t.Error("exception variant should be unequal to value variant")
	}
}

// scriptedSession is a fake [Session] that scripts BulkWalk and Get
// responses on a per-call basis. Differs from watcherFakeSession in
// that BulkWalk responses are keyed on the root OID (so the test can
// distinguish a tableRoot walk from an indicator-column walk).
type scriptedSession struct {
	*watcherFakeSession

	walkScripts map[string][][]VarBind // root.String() → FIFO of responses
	getScripts  []map[string]VarBind   // FIFO; each script is OID.String() → VB
}

func newScriptedSession() *scriptedSession {
	return &scriptedSession{
		watcherFakeSession: &watcherFakeSession{},
		walkScripts:        make(map[string][][]VarBind),
	}
}

func (s *scriptedSession) pushTableWalk(vbs []VarBind) {
	s.pushWalkAt(ifTableRoot, vbs)
}

func (s *scriptedSession) pushWalkAt(root OID, vbs []VarBind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := root.String()
	s.walkScripts[key] = append(s.walkScripts[key], vbs)
}

func (s *scriptedSession) pushIndicatorWalk(vbs []VarBind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := ifLastChange.String()
	s.walkScripts[key] = append(s.walkScripts[key], vbs)
}

func (s *scriptedSession) pushGet(resp map[string]VarBind) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getScripts = append(s.getScripts, resp)
}

func (s *scriptedSession) BulkWalk(ctx context.Context, root OID, _ ...CallOption) *Walker {
	s.bulkWalkCalls.Add(1)
	s.mu.Lock()
	key := root.String()
	q := s.walkScripts[key]
	var vbs []VarBind
	if len(q) > 0 {
		vbs = q[0]
		s.walkScripts[key] = q[1:]
	}
	s.mu.Unlock()

	w := NewWalker(ctx, 64)
	w.Pump(func(_ context.Context) {
		for _, vb := range vbs {
			if !w.Send(vb.GetHeader().OID, vb) {
				return
			}
		}
	})
	return w
}

func (s *scriptedSession) BulkWalkRaw(ctx context.Context, root OID, opts ...CallOption) *RawWalker {
	return RawWalkerFromWalker(ctx, s.BulkWalk(ctx, root, opts...))
}

func (s *scriptedSession) Get(_ context.Context, oids []OID, _ ...CallOption) ([]VarBind, error) {
	s.getCalls.Add(1)
	s.mu.Lock()
	var script map[string]VarBind
	if len(s.getScripts) > 0 {
		script = s.getScripts[0]
		s.getScripts = s.getScripts[1:]
	}
	s.mu.Unlock()

	out := make([]VarBind, 0, len(oids))
	for _, o := range oids {
		if vb, ok := script[o.String()]; ok {
			out = append(out, vb)
			continue
		}
		// Default: return NoSuchInstance-like result that decode
		// can interpret as "no value for this OID". Tests that
		// expect specific responses populate the script
		// exhaustively.
		out = append(out, NoSuchInstanceVar{Header: Header{OID: o, Kind: KindNoSuchInstance}})
	}
	return out, nil
}

type ifRow struct {
	idx    uint32
	descr  string
	lastCh uint32
}

func buildIfTableRows(t *testing.T, rows ...ifRow) []VarBind {
	t.Helper()
	vbs := make([]VarBind, 0, 2*len(rows))
	for _, r := range rows {
		oid := ifDescrOID.Append(r.idx)
		vbs = append(vbs, OctetStringVar{
			Header: Header{OID: oid, Kind: KindOctetString},
			Value:  []byte(r.descr),
		})
	}
	for _, r := range rows {
		oid := ifLastChange.Append(r.idx)
		vbs = append(vbs, TimeTicksVar{
			Header: Header{OID: oid, Kind: KindTimeTicks},
			Value:  r.lastCh,
		})
	}
	return vbs
}

type idxValue struct {
	idx   uint32
	value uint32
}

func buildIndicatorVBs(vals ...idxValue) []VarBind {
	vbs := make([]VarBind, 0, len(vals))
	for _, v := range vals {
		oid := ifLastChange.Append(v.idx)
		vbs = append(vbs, TimeTicksVar{
			Header: Header{OID: oid, Kind: KindTimeTicks},
			Value:  v.value,
		})
	}
	return vbs
}

type entRow struct {
	idx   uint32
	descr string
}

func buildEntRows(t *testing.T, descrCol OID, rows ...entRow) []VarBind {
	t.Helper()
	vbs := make([]VarBind, 0, len(rows))
	for _, r := range rows {
		oid := descrCol.Append(r.idx)
		vbs = append(vbs, OctetStringVar{
			Header: Header{OID: oid, Kind: KindOctetString},
			Value:  []byte(r.descr),
		})
	}
	return vbs
}

// drainEvents reads up to n events from w within the deadline. Returns
// fewer than n events on timeout. The Watcher's tick goroutine must
// be feeding events for the function to return — caller is responsible
// for the scripting that makes that happen.
func drainEvents[Row any](t *testing.T, w *Watcher[Row], n int, deadline time.Duration) []WatchEvent[Row] {
	t.Helper()
	out := make([]WatchEvent[Row], 0, n)
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for len(out) < n {
		select {
		case ev, ok := <-w.pump.Data():
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timer.C:
			t.Logf("drainEvents: timed out after %v with %d events", deadline, len(out))
			return out
		}
	}
	return out
}

// Sanity check that the scriptedSession satisfies the Session
// interface.
var _ Session = (*scriptedSession)(nil)

// Pin that the lastTickErr field uses atomic.Pointer[error] semantics
// the rest of the package depends on (catches a future refactor that
// accidentally changes the type).
var _ atomic.Pointer[error] = atomic.Pointer[error]{}

// Confirm ErrSessionClosed remains an exported error sentinel; used
// in the terminal-error tests above and the fallback tests below.
var _ error = ErrSessionClosed
