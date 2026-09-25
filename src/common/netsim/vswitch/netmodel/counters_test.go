package netmodel_test

import (
	"testing"
	"time"

	"buf.build/go/protovalidate"

	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/netmodel"
)

func TestInterfaceCountersExportDistinctValues(t *testing.T) {
	c := fabric.Counters{
		InBytes:      101,
		OutBytes:     102,
		InUnicast:    103,
		OutUnicast:   104,
		InMulticast:  105,
		OutMulticast: 106,
		InBroadcast:  107,
		OutBroadcast: 108,
		InErrors:     109,
		OutErrors:    110,
		InDiscards:   111,
		OutDiscards:  112,
	}

	exported := netmodel.InterfaceCounters(c, time.Time{})

	if got, want := exported.GetInBytes(), uint64(101); got != want {
		t.Errorf("GetInBytes() = %d, want %d", got, want)
	}
	if got, want := exported.GetOutBytes(), uint64(102); got != want {
		t.Errorf("GetOutBytes() = %d, want %d", got, want)
	}
	if got, want := exported.GetInUnicastPackets(), uint64(103); got != want {
		t.Errorf("GetInUnicastPackets() = %d, want %d", got, want)
	}
	if got, want := exported.GetOutUnicastPackets(), uint64(104); got != want {
		t.Errorf("GetOutUnicastPackets() = %d, want %d", got, want)
	}
	if got, want := exported.GetInMulticastPackets(), uint64(105); got != want {
		t.Errorf("GetInMulticastPackets() = %d, want %d", got, want)
	}
	if got, want := exported.GetOutMulticastPackets(), uint64(106); got != want {
		t.Errorf("GetOutMulticastPackets() = %d, want %d", got, want)
	}
	if got, want := exported.GetInBroadcastPackets(), uint64(107); got != want {
		t.Errorf("GetInBroadcastPackets() = %d, want %d", got, want)
	}
	if got, want := exported.GetOutBroadcastPackets(), uint64(108); got != want {
		t.Errorf("GetOutBroadcastPackets() = %d, want %d", got, want)
	}
	if got, want := exported.GetInErrors(), uint64(109); got != want {
		t.Errorf("GetInErrors() = %d, want %d", got, want)
	}
	if got, want := exported.GetOutErrors(), uint64(110); got != want {
		t.Errorf("GetOutErrors() = %d, want %d", got, want)
	}
	if got, want := exported.GetInDiscards(), uint64(111); got != want {
		t.Errorf("GetInDiscards() = %d, want %d", got, want)
	}
	if got, want := exported.GetOutDiscards(), uint64(112); got != want {
		t.Errorf("GetOutDiscards() = %d, want %d", got, want)
	}

	if err := protovalidate.Validate(exported); err != nil {
		t.Errorf("protovalidate.Validate failed: %v", err)
	}
}

func TestInterfaceCountersExportZeroValues(t *testing.T) {
	c := fabric.Counters{}

	exported := netmodel.InterfaceCounters(c, time.Time{})

	if !exported.HasInBytes() || exported.GetInBytes() != 0 {
		t.Errorf("HasInBytes/GetInBytes mismatch: has=%v val=%d", exported.HasInBytes(), exported.GetInBytes())
	}
	if !exported.HasOutBytes() || exported.GetOutBytes() != 0 {
		t.Errorf("HasOutBytes/GetOutBytes mismatch: has=%v val=%d", exported.HasOutBytes(), exported.GetOutBytes())
	}
	if !exported.HasInUnicastPackets() || exported.GetInUnicastPackets() != 0 {
		t.Errorf("HasInUnicastPackets/GetInUnicastPackets mismatch: has=%v val=%d", exported.HasInUnicastPackets(), exported.GetInUnicastPackets())
	}
	if !exported.HasOutUnicastPackets() || exported.GetOutUnicastPackets() != 0 {
		t.Errorf("HasOutUnicastPackets/GetOutUnicastPackets mismatch: has=%v val=%d", exported.HasOutUnicastPackets(), exported.GetOutUnicastPackets())
	}
	if !exported.HasInMulticastPackets() || exported.GetInMulticastPackets() != 0 {
		t.Errorf("HasInMulticastPackets/GetInMulticastPackets mismatch: has=%v val=%d", exported.HasInMulticastPackets(), exported.GetInMulticastPackets())
	}
	if !exported.HasOutMulticastPackets() || exported.GetOutMulticastPackets() != 0 {
		t.Errorf("HasOutMulticastPackets/GetOutMulticastPackets mismatch: has=%v val=%d", exported.HasOutMulticastPackets(), exported.GetOutMulticastPackets())
	}
	if !exported.HasInBroadcastPackets() || exported.GetInBroadcastPackets() != 0 {
		t.Errorf("HasInBroadcastPackets/GetInBroadcastPackets mismatch: has=%v val=%d", exported.HasInBroadcastPackets(), exported.GetInBroadcastPackets())
	}
	if !exported.HasOutBroadcastPackets() || exported.GetOutBroadcastPackets() != 0 {
		t.Errorf("HasOutBroadcastPackets/GetOutBroadcastPackets mismatch: has=%v val=%d", exported.HasOutBroadcastPackets(), exported.GetOutBroadcastPackets())
	}
	if !exported.HasInErrors() || exported.GetInErrors() != 0 {
		t.Errorf("HasInErrors/GetInErrors mismatch: has=%v val=%d", exported.HasInErrors(), exported.GetInErrors())
	}
	if !exported.HasOutErrors() || exported.GetOutErrors() != 0 {
		t.Errorf("HasOutErrors/GetOutErrors mismatch: has=%v val=%d", exported.HasOutErrors(), exported.GetOutErrors())
	}
	if !exported.HasInDiscards() || exported.GetInDiscards() != 0 {
		t.Errorf("HasInDiscards/GetInDiscards mismatch: has=%v val=%d", exported.HasInDiscards(), exported.GetInDiscards())
	}
	if !exported.HasOutDiscards() || exported.GetOutDiscards() != 0 {
		t.Errorf("HasOutDiscards/GetOutDiscards mismatch: has=%v val=%d", exported.HasOutDiscards(), exported.GetOutDiscards())
	}

	if err := protovalidate.Validate(exported); err != nil {
		t.Errorf("protovalidate.Validate failed: %v", err)
	}
}

func TestInterfaceCountersDiscontinuityFromRunStart(t *testing.T) {
	start := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)

	exported := netmodel.InterfaceCounters(fabric.Counters{}, start)

	if !exported.HasLastDiscontinuity() {
		t.Fatal("HasLastDiscontinuity() = false for a non-zero run start")
	}
	if got := exported.GetLastDiscontinuity().AsTime(); !got.Equal(start) {
		t.Errorf("GetLastDiscontinuity() = %v, want %v", got, start)
	}
	if err := protovalidate.Validate(exported); err != nil {
		t.Errorf("protovalidate.Validate failed: %v", err)
	}
}

func TestInterfaceCountersZeroStartLeavesDiscontinuityUnset(t *testing.T) {
	exported := netmodel.InterfaceCounters(fabric.Counters{}, time.Time{})

	if exported.HasLastDiscontinuity() {
		t.Errorf("HasLastDiscontinuity() = true for a zero run start, got %v", exported.GetLastDiscontinuity().AsTime())
	}
}
