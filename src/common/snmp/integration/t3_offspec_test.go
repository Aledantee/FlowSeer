//go:build snmp_integration_t3

package integration

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
)

// TestT3_Replay_OffSpecWrongTypes_FusedFallback drives the generated
// fused ifTable walker against the offspec/wrong-types capture,
// where every requested column's value tag deliberately diverges from
// the IF-MIB declaration — the misbehavior class users report against
// other stacks (telegraf #14598, snmp_exporter #338). Every fused arm
// must decline and the generic coercion fallback must produce the
// exact expected values: a divergence here means the fast path changed
// decode semantics for off-spec devices.
//
// Deliberate tag swaps in the capture: ifIndex/ifType/ifOperStatus
// (Integer32-declared) served as Gauge32; ifSpeed (Gauge32-declared)
// served as Counter32; ifLastChange (TimeTicks-declared) served as
// INTEGER; ifInOctets (Counter32-declared) served as Gauge32.
//
// Covers conformance matrix row: raw-wrong-typed-column (end-to-end
// depth; the in-package pin in conformance_rawpath_test.go is the
// gate-cited coverage).
func TestT3_Replay_OffSpecWrongTypes_FusedFallback(t *testing.T) {
	var entry *ManifestEntry
	for i := range t3Manifest.Entries {
		if t3Manifest.Entries[i].Vendor == "offspec" && t3Manifest.Entries[i].Device == "wrong-types" {
			entry = &t3Manifest.Entries[i]
			break
		}
	}
	if entry == nil {
		t.Fatal("manifest has no offspec/wrong-types entry")
	}

	sess := t3DialReplay(t, *entry)
	tw := ifmib.IfTable.Walk(context.Background(), sess,
		ifmib.IfIndex, ifmib.IfSpeed, ifmib.IfOperStatus, ifmib.IfLastChange, ifmib.IfInOctets)

	type want struct {
		index      int32
		speed      uint32
		operStatus ifmib.IfOperStatusValue
		lastChange uint32
		inOctets   uint32
	}
	wants := map[int32]want{
		1: {index: 1, speed: 10000000, operStatus: ifmib.IfOperStatusValueUp, lastChange: 777, inOctets: 111111},
		2: {index: 2, speed: 1000000000, operStatus: ifmib.IfOperStatusValueUp, lastChange: 888, inOctets: 222222},
	}

	rows := 0
	for _, row := range tw.Iter() {
		rows++
		w, ok := wants[row.IfIndex]
		if !ok {
			t.Fatalf("unexpected row IfIndex=%d", row.IfIndex)
		}
		if row.IfSpeed != w.speed || row.IfOperStatus != w.operStatus ||
			row.IfLastChange != w.lastChange || row.IfInOctets != w.inOctets {
			t.Fatalf("row %d decoded %+v, want %+v — the fused-decode fallback changed coercion semantics", row.IfIndex, row, w)
		}
	}
	if err := tw.Err(); err != nil {
		t.Fatalf("walk over wrong-typed capture failed: %v — off-spec value tags must coerce, not abort", err)
	}
	if rows != len(wants) {
		t.Fatalf("walk yielded %d rows, want %d", rows, len(wants))
	}
}
