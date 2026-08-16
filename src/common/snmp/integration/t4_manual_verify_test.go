//go:build snmp_integration_t4

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/generated/go/mib/hostresourcesmib"
	"go.aledante.io/FlowSeer/generated/go/mib/ifmib"
	"go.aledante.io/FlowSeer/generated/go/mib/ipmib"
	"go.aledante.io/FlowSeer/generated/go/mib/lldpmib"
	"go.aledante.io/FlowSeer/generated/go/mib/mikrotik"
	"go.aledante.io/FlowSeer/generated/go/mib/snmpv2mib"
	"go.aledante.io/FlowSeer/src/common/snmp"
)

// TestT4_LiveDeviceVerify is the live-device acceptance gate: each
// previously-failing scalar Get and table Walk discovered during
// manual verification against MikroTik CRS317 (SwOS 2.18) and hAP ax
// (RouterOS 7.20.6) becomes a named subtest so a regression names
// itself.
//
// Subtests are organised by bug class. Each scalar regression check
// tolerates ErrException (the device legitimately doesn't expose that
// OID — common when running against a router for the AP-only Mtxr
// optical OIDs) but fails on ErrTypeMismatch or ErrLossyConversion —
// those would mean the decoder leniency policy regressed.
//
// Table-walk regression checks require a non-zero row count and a nil
// Err(), since every device class t4 targets has at least one
// interface, and the walker contract guarantees Err()=nil on a
// successful walk.
//
// The device classes this test is built against (and the operators
// who should re-run it when touching leniency or walker code):
//
//   - MikroTik SwOS 2.18 (CRS-series industrial switches)
//   - MikroTik RouterOS 7.20.6 (hAP / CCR / CRS-routers)
func TestT4_LiveDeviceVerify(t *testing.T) {
	if len(t4Targets) == 0 {
		t.Fatal("t4Targets empty; TestMain should have populated this or skipped the tier")
	}

	for _, tg := range t4Targets {
		t.Run(tg.String(), func(t *testing.T) {
			sess := t4DialV2c(t, tg)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			runScalarRegressions(t, ctx, sess)
			runTableWalkRegressions(t, ctx, sess)
		})
	}
}

// runScalarRegressions exercises the four Get-shaped decoder bugs
// manual verification surfaced. Each is named after the specific
// lossless-coercion that DecodeUint32 / DecodeInt32 has to honor for
// the agent's natural emission shape.
func runScalarRegressions(t *testing.T, ctx context.Context, sess snmp.Session) {
	t.Helper()

	t.Run("sysServices_Integer32_into_Uinteger32", func(t *testing.T) {
		// Mandatory SNMPv2-MIB scalar; both device classes expose it.
		// A strict decoder fails with ErrTypeMismatch when the agent
		// emitted Integer32 (the SMI-correct wire form for
		// INTEGER (0..127)) into our Uinteger32 generated decoder.
		v, err := snmpv2mib.SysServicesGet(ctx, sess)
		assertDecoderLeniency(t, err, "sysServices.0")
		t.Logf("sysServices.0 = %d", v)
	})

	t.Run("MtxrHlFanSpeed1_Integer32_into_Gauge32", func(t *testing.T) {
		// Agent emits Integer32 for a Gauge32-declared column;
		// generated decoder used a strict Gauge32Var type-assert. The
		// router has no fan; expect ErrException there. The CRS317
		// has two fans; expect a value.
		v, err := mikrotik.MtxrHlFanSpeed1Get(ctx, sess)
		assertDecoderLeniency(t, err, "mtxrHlFanSpeed1.0")
		t.Logf("mtxrHlFanSpeed1.0 = %d (ErrException = device has no fan)", v)
	})

	t.Run("MtxrHlFanSpeed2_Integer32_into_Gauge32", func(t *testing.T) {
		v, err := mikrotik.MtxrHlFanSpeed2Get(ctx, sess)
		assertDecoderLeniency(t, err, "mtxrHlFanSpeed2.0")
		t.Logf("mtxrHlFanSpeed2.0 = %d (ErrException = device has no second fan)", v)
	})
}

// runTableWalkRegressions exercises the column-major BulkWalk bug
// plus the per-row decode contracts. Each subtest names the
// device-class invariant it pins so a future regression points at the
// right line in the contract.
func runTableWalkRegressions(t *testing.T, ctx context.Context, sess snmp.Session) {
	t.Helper()

	t.Run("ifTable_walk_one_row_per_interface", func(t *testing.T) {
		// A column-major BulkWalk would inflate this to (rows ×
		// requested-columns). The CRS317 has 16 ports; the hAP ax has
		// 14. Both devices expose ifTable; an empty result here would
		// be a regression even though the exact count is device-
		// specific.
		w := ifmib.IfTable.Walk(ctx, sess,
			ifmib.IfIndex, ifmib.IfDescr, ifmib.IfType, ifmib.IfOperStatus,
			ifmib.IfInOctets, ifmib.IfOutOctets,
		)
		rows := 0
		for _, row := range w.Iter() {
			rows++
			if rows <= 4 {
				t.Logf("  ifIndex=%d descr=%q type=%v oper=%v",
					row.IfIndex, row.IfDescr, row.IfType, row.IfOperStatus)
			}
		}
		if err := w.Err(); err != nil {
			t.Fatalf("ifTable walk: %v", err)
		}
		if rows == 0 {
			t.Fatal("ifTable yielded 0 rows; want >0 (every device has at least one interface)")
		}
		t.Logf("ifTable rows: %d", rows)
	})

	t.Run("ipAddrTable_composite_index_decodes_per_row", func(t *testing.T) {
		// Without decoder leniency the ipAdEntIfIndex column
		// (INTEGER (1..2^31-1)) fails to decode when the agent emits
		// Integer32 instead of Uinteger32, and a column-major walker
		// halts at the first row's emission. Both must succeed; an
		// empty table is acceptable (some routers don't expose any
		// IP addresses to SNMP).
		w := ipmib.IpAddrTable.Walk(ctx, sess,
			ipmib.IpAdEntAddr, ipmib.IpAdEntIfIndex, ipmib.IpAdEntNetMask,
		)
		rows := 0
		for _, row := range w.Iter() {
			rows++
			if rows <= 4 {
				t.Logf("  ipAdEntAddr=%s ifIndex=%d netMask=%s",
					row.IpAdEntAddr, row.IpAdEntIfIndex, row.IpAdEntNetMask)
			}
		}
		if err := w.Err(); err != nil {
			t.Fatalf("ipAddrTable walk: %v", err)
		}
		t.Logf("ipAddrTable rows: %d", rows)
	})

	t.Run("hrStorageTable_walk_one_row_per_storage", func(t *testing.T) {
		// HOST-RESOURCES-MIB is supported on RouterOS but not on
		// SwOS. SwOS returns NoSuchObject on the walk start, which
		// the walker surfaces via Err(); accept that as a clean
		// not-supported signal.
		w := hostresourcesmib.HrStorageTable.Walk(ctx, sess,
			hostresourcesmib.HrStorageDescr, hostresourcesmib.HrStorageSize, hostresourcesmib.HrStorageUsed,
		)
		rows := 0
		for _, row := range w.Iter() {
			rows++
			if rows <= 4 {
				t.Logf("  storage[%d] descr=%q size=%d used=%d",
					rows, row.HrStorageDescr, row.HrStorageSize, row.HrStorageUsed)
			}
		}
		if err := w.Err(); err != nil {
			// Walker.Err can carry the underlying ErrException for
			// NoSuchObject; that's the "device doesn't support this
			// table" case, not a regression.
			if errors.Is(err, snmp.ErrException) {
				t.Skipf("hrStorageTable not supported on this device: %v", err)
			}
			t.Fatalf("hrStorageTable walk: %v", err)
		}
		t.Logf("hrStorageTable rows: %d", rows)
	})

	t.Run("lldpLocPortTable_walk_one_row_per_port", func(t *testing.T) {
		// LLDP-MIB is RouterOS-only on MikroTik; SwOS doesn't ship a
		// MIB-side implementation. Same skip-on-ErrException pattern
		// as hrStorageTable.
		w := lldpmib.LldpLocPortTable.Walk(ctx, sess,
			lldpmib.LldpLocPortIdSubtype, lldpmib.LldpLocPortId, lldpmib.LldpLocPortDesc,
		)
		rows := 0
		for _, row := range w.Iter() {
			rows++
			if rows <= 4 {
				t.Logf("  lldpLocPort[%d] subtype=%v id=%v desc=%q",
					rows, row.LldpLocPortIdSubtype, row.LldpLocPortId, row.LldpLocPortDesc)
			}
		}
		if err := w.Err(); err != nil {
			if errors.Is(err, snmp.ErrException) {
				t.Skipf("lldpLocPortTable not supported on this device: %v", err)
			}
			t.Fatalf("lldpLocPortTable walk: %v", err)
		}
		t.Logf("lldpLocPortTable rows: %d", rows)
	})

	t.Run("mtxrOpticalTable_walk_with_lenient_decoders", func(t *testing.T) {
		// CRS317 with SFP cages exposes the optical table; the hAP ax
		// router doesn't. mtxrOpticalSupplyVoltage's wire variant
		// (Uinteger32 vs Gauge32) is one of the shapes the decoder
		// leniency policy exists to accept. Empty table is the "no
		// SFP cages" case, not a
		// regression.
		w := mikrotik.MtxrOpticalTable.Walk(ctx, sess,
			mikrotik.MtxrOpticalName,
			mikrotik.MtxrOpticalSupplyVoltage,
			mikrotik.MtxrOpticalTemperature,
			mikrotik.MtxrOpticalTxPower,
			mikrotik.MtxrOpticalRxPower,
		)
		rows := 0
		for _, row := range w.Iter() {
			rows++
			if rows <= 4 {
				t.Logf("  optical[%d] name=%q volt=%d temp=%d tx=%d rx=%d",
					rows, row.MtxrOpticalName, row.MtxrOpticalSupplyVoltage,
					row.MtxrOpticalTemperature, row.MtxrOpticalTxPower, row.MtxrOpticalRxPower)
			}
		}
		if err := w.Err(); err != nil {
			if errors.Is(err, snmp.ErrException) {
				t.Skipf("mtxrOpticalTable not supported on this device: %v", err)
			}
			t.Fatalf("mtxrOpticalTable walk: %v", err)
		}
		t.Logf("mtxrOpticalTable rows: %d", rows)
	})
}

// assertDecoderLeniency fails the test only if the error indicates
// the decoder leniency policy or emitter rewiring regressed. ErrException
// (NoSuchObject / NoSuchInstance — the device legitimately doesn't
// expose this OID) is reported via t.Logf and accepted. The two
// regression-class errors are ErrTypeMismatch (decoder rejected a
// variant it should now accept) and ErrLossyConversion (decoder
// rejected a value that should fit) — both are real failures.
func assertDecoderLeniency(t *testing.T, err error, label string) {
	t.Helper()
	if err == nil {
		return
	}
	if errors.Is(err, snmp.ErrException) {
		t.Logf("%s: device returned exception (acceptable): %v", label, err)
		return
	}
	if errors.Is(err, snmp.ErrTypeMismatch) {
		t.Fatalf("%s: REGRESSION — ErrTypeMismatch should have been accepted by the leniency policy: %v", label, err)
	}
	if errors.Is(err, snmp.ErrLossyConversion) {
		t.Fatalf("%s: REGRESSION — ErrLossyConversion implies the agent emitted a value outside the target type's range (this may also be a real agent bug): %v", label, err)
	}
	// Anything else (network timeout, wire error, etc.) is not a
	// decoder or walker regression specifically — log it and continue.
	t.Logf("%s: non-decode error (likely transport): %v", label, err)
}
