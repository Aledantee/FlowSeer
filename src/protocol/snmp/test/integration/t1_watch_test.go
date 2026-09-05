//go:build snmp_integration_t1

package integration

import (
	"context"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/src/protocol/snmp"
)

// Real-wire Watch behavior against the snmpd harness.
//
// Shippable subset (cold-start, quiet device, fallback):
//
//   - cold-start: first ticks emit Added events for every
//     interface snmpd is configured with.
//   - quiet device (partial): with no scripted changes, no
//     additional events arrive after cold-start within a bounded
//     window.
//   - fallback: pointing the Watcher at an indicator OID
//     known not to exist on the agent's MIB view triggers the
//     non-terminal fallback transition; Watcher.Fallback() reads
//     true; Err() stays nil.
//
// Deferred (single-row state change, row removal): both
// require harness extensions that mutate snmpd state mid-run (writable
// MIB mode or a sidecar that rewrites the config and signals reload).
// Tracked as t.Skip placeholders so the test surface is in place when
// the extension lands.

// TestT1_Watch_ColdStartEmitsAddedForEveryInterface covers cold-start
// against the real snmpd. The snmpd manifest is expected to expose at
// least one interface row; the test asserts every emitted Added event
// carries a populated IfDescr and that the Watcher's Err() stays nil
// after cold-start.
func TestT1_Watch_ColdStartEmitsAddedForEveryInterface(t *testing.T) {
	sess := t1DialV2c(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tw := ifmib.IfTable.Watch(ctx, sess,
		[]snmp.AnyColumn{ifmib.IfDescr, ifmib.IfOperStatus},
		snmp.WithCadenceBounds(500*time.Millisecond, 5*time.Second),
		snmp.WithForcedWalkInterval(1*time.Hour),
	)
	t.Cleanup(func() {
		if err := tw.Close(); err != nil {
			t.Errorf("close watcher: %v", err)
		}
	})

	events := drainColdStart(ctx, t, tw, 8*time.Second)
	if len(events) == 0 {
		t.Fatalf("cold-start produced 0 events; expected >= 1 from snmpd")
	}
	for _, ev := range events {
		if ev.Kind != snmp.ChangeKindAdded {
			t.Errorf("event kind = %v, want Added during cold-start", ev.Kind)
		}
		if ev.Row.IfDescr == "" {
			t.Errorf("event for index %s has empty IfDescr", ev.Index)
		}
	}

	if err := tw.Err(); err != nil {
		t.Errorf("Err() = %v, want nil", err)
	}
	if tw.Fallback() {
		t.Logf("note: Fallback() = true; snmpd may lack ifLastChange — non-blocking")
	}
}

// TestT1_Watch_QuietDeviceNoEventsAfterColdStart covers the
// quiet-device no-emit portion: after the cold-start drain, no
// additional events arrive within a bounded window because snmpd is
// idle.
//
// Wire-cost observation (data for follow-up cadence calibration):
// the State-tier tick fires at CadenceMin every interval; over the
// window we expect O(window / cadenceMin) ifLastChange walks plus
// the cold-start full walk. The test does not assert PDU counts —
// snmp.NewSession does not surface wire stats — but
// records the inter-tick gap as a t.Logf for future tuning.
func TestT1_Watch_QuietDeviceNoEventsAfterColdStart(t *testing.T) {
	sess := t1DialV2c(t)

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	tw := ifmib.IfTable.Watch(ctx, sess,
		[]snmp.AnyColumn{ifmib.IfDescr},
		snmp.WithCadenceBounds(300*time.Millisecond, 2*time.Second),
		snmp.WithForcedWalkInterval(1*time.Hour),
	)
	t.Cleanup(func() {
		if err := tw.Close(); err != nil {
			t.Errorf("close watcher: %v", err)
		}
	})

	coldStart := drainColdStart(ctx, t, tw, 4*time.Second)
	if len(coldStart) == 0 {
		t.Fatal("no cold-start events from snmpd; expected at least loopback")
	}

	events := forwardIfTableEvents(tw)
	deadline := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break loop
			}
			t.Errorf("unexpected post-cold-start event during quiet window: %+v", ev)
		case <-deadline:
			break loop
		}
	}

	if err := tw.Err(); err != nil {
		t.Errorf("Err() = %v, want nil after quiet window", err)
	}
}

// TestT1_Watch_FallbackOnMissingIndicator covers fallback: point the
// Watcher at an indicator OID known not to exist on the snmpd MIB
// view. The cold-start probe returns NoSuchObject; the Watcher
// transitions to fallback mode; Err() stays nil (fallback is
// degraded-but-operational, not terminal).
//
// Construction uses snmp.NewWatcher directly with a hand-crafted
// scalar indicator pointing at an unallocated OID under the agent's
// MIB tree. The generated ifmib.IfTable.Watch wires the real
// IfTableIndicator (per-row), which snmpd actually serves, so it
// would not exercise the fallback path here.
func TestT1_Watch_FallbackOnMissingIndicator(t *testing.T) {
	sess := t1DialV2c(t)

	// Pick an OID under the IF-MIB subtree that snmpd does not
	// implement: a private/experimental object under
	// enterprises.99999 (1.3.6.1.4.1.99999) — uncommonly registered
	// vendor space.
	missingScalar := snmp.MustOID(1, 3, 6, 1, 4, 1, 99999, 1, 0)
	ifTableRoot := snmp.MustOID(1, 3, 6, 1, 2, 1, 2, 2)

	indicator, err := snmp.NewScalarIndicator(
		missingScalar, snmp.KindTimeTicks, []snmp.OID{ifTableRoot},
	)
	if err != nil {
		t.Fatalf("NewScalarIndicator: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The decode/equal pair is the same shape the generated wrapper
	// would emit; we inline a minimal version for the test row.
	type row struct {
		Index   snmp.OID
		IfDescr string
	}
	decode := func(idx snmp.OID, vbs []snmp.VarBind) (row, error) {
		r := row{Index: idx}
		for _, vb := range vbs {
			if s, ok := vb.(snmp.OctetStringVar); ok {
				r.IfDescr = string(s.Value)
			}
		}
		return r, nil
	}
	equal := func(a, b row) bool {
		return a.Index.Equal(b.Index) && a.IfDescr == b.IfDescr
	}
	merge := func(dst *row, vbs []snmp.VarBind) {
		for _, vb := range vbs {
			if s, ok := vb.(snmp.OctetStringVar); ok {
				dst.IfDescr = string(s.Value)
			}
		}
	}

	w, err := snmp.NewWatcher[row](
		ctx, sess, indicator,
		[]snmp.AnyColumn{ifmib.IfDescr},
		decode, equal, merge,
		snmp.WithCadenceBounds(500*time.Millisecond, 5*time.Second),
		snmp.WithForcedWalkInterval(1*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("close watcher: %v", err)
		}
	})

	// Wait for fallback to engage. The scalar probe runs at
	// cold-start, so within a second or two the transition should
	// be observable.
	deadline := time.After(5 * time.Second)
	for !w.Fallback() {
		select {
		case <-deadline:
			t.Fatal("Fallback never engaged within 5s")
		case <-time.After(50 * time.Millisecond):
		}
	}

	if w.Err() != nil {
		t.Errorf("Err() = %v, want nil (fallback is not terminal)", w.Err())
	}
	// Range over events for a bounded window — fallback mode still
	// runs full walks at State-tier cadence, so we should see Added
	// events from the cold-start walk.
	added := 0
	events := forwardEvents(w)
	endWindow := time.After(2 * time.Second)
streamLoop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break streamLoop
			}
			if ev.Kind == snmp.ChangeKindAdded {
				added++
			}
		case <-endWindow:
			break streamLoop
		}
	}
	t.Logf("fallback-mode events observed: %d Added", added)
}

// TestT1_Watch_SingleRowStateChange is a placeholder. The
// scenario requires snmpd to publish a state change mid-test —
// achievable by switching to writable-MIB mode or running a sidecar
// that rewrites snmpd.conf and signals SIGHUP between ticks. Neither
// is committed today.
func TestT1_Watch_SingleRowStateChange(t *testing.T) {
	t.Skip("requires snmpd writable-MIB harness extension — tracked in follow-up")
}

// TestT1_Watch_RowRemoval is a placeholder. Same harness-
// extension requirement.
func TestT1_Watch_RowRemoval(t *testing.T) {
	t.Skip("requires snmpd writable-MIB harness extension — tracked in follow-up")
}

// drainColdStart consumes events from the Watcher's iterator until
// the channel quiets (no event within idleWindow) or the parent ctx
// fires. Returns the events collected.
func drainColdStart(ctx context.Context, t *testing.T, tw *ifmib.IfTableWatcher, idleWindow time.Duration) []snmp.WatchEvent[ifmib.IfTableRow] {
	t.Helper()
	events := forwardIfTableEvents(tw)
	var got []snmp.WatchEvent[ifmib.IfTableRow]
	idle := time.NewTimer(idleWindow)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return got
		case ev, ok := <-events:
			if !ok {
				return got
			}
			got = append(got, ev)
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(idleWindow)
		case <-idle.C:
			return got
		}
	}
}

// forwardIfTableEvents pumps the Watcher's typed Iter into a buffered
// channel via a single consumer goroutine. The channel closes when
// the iterator terminates.
func forwardIfTableEvents(tw *ifmib.IfTableWatcher) <-chan snmp.WatchEvent[ifmib.IfTableRow] {
	out := make(chan snmp.WatchEvent[ifmib.IfTableRow], 32)
	go func() {
		defer close(out)
		for _, ev := range tw.Iter() {
			out <- ev
		}
	}()
	return out
}

// forwardEvents is the generic version for callers using
// snmp.NewWatcher directly (no typed wrapper).
func forwardEvents[Row any](w *snmp.Watcher[Row]) <-chan snmp.WatchEvent[Row] {
	out := make(chan snmp.WatchEvent[Row], 32)
	go func() {
		defer close(out)
		for _, ev := range w.Iter() {
			out <- ev
		}
	}()
	return out
}
