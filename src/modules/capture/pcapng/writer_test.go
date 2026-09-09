// Package pcapng_test's structural walk is the gate: it needs no external
// tool. Manual verification, not run here since neither ships as a
// repository dependency: write a *Writer's output to a file and run
// `capinfos <file>` or `tshark -r <file>` from a Wireshark install; either
// should report the packet count, LINK_TYPE_ETHERNET, and the configured
// snap length without complaint.
package pcapng_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/pcapng"
)

// block is one parsed pcapng block: its type and body (the length and
// trailer are checked and discarded).
type block struct {
	typ  uint32
	body []byte
}

// splitBlocks walks buf's generic block structure by hand: type, total
// length, body padded to 32 bits, and a repeated total length trailer that
// must match the leading one.
func splitBlocks(t *testing.T, buf []byte) []block {
	t.Helper()
	var blocks []block
	for len(buf) > 0 {
		if len(buf) < 12 {
			t.Fatalf("trailing %d bytes are too short for a block header/trailer", len(buf))
		}
		typ := binary.LittleEndian.Uint32(buf[0:4])
		total := binary.LittleEndian.Uint32(buf[4:8])
		if total%4 != 0 {
			t.Fatalf("block total length %d is not a multiple of 4", total)
		}
		if uint32(len(buf)) < total {
			t.Fatalf("block claims total length %d, only %d bytes remain", total, len(buf))
		}
		trailer := binary.LittleEndian.Uint32(buf[total-4 : total])
		if trailer != total {
			t.Fatalf("block trailer length %d does not match leading length %d", trailer, total)
		}
		blocks = append(blocks, block{typ: typ, body: buf[8 : total-4]})
		buf = buf[total:]
	}
	return blocks
}

const (
	blockTypeSHB = 0x0A0D0D0A
	blockTypeIDB = 0x00000001
	blockTypeISB = 0x00000005
	blockTypeEPB = 0x00000006
)

func newRecord(t *testing.T, seq uint64, data []byte) *capturev1.PacketRecord {
	t.Helper()
	r := &capturev1.PacketRecord{}
	r.SetSequence(seq)
	r.SetCapturedAt(timestamppb.New(time.Unix(1_700_000_000, 0)))
	r.SetOriginalLength(uint32(len(data)))
	r.SetData(data)
	return r
}

func TestWriter_StructuralWalk(t *testing.T) {
	const numRecords = 100

	var buf bytes.Buffer
	w, err := pcapng.NewWriter(&buf, capturev1.LinkType_LINK_TYPE_ETHERNET, 128)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for i := range numRecords {
		data := bytes.Repeat([]byte{byte(i)}, 14)
		if err := w.WriteRecord(newRecord(t, uint64(i), data)); err != nil {
			t.Fatalf("WriteRecord(%d): %v", i, err)
		}
	}
	counters := &capturev1.CaptureCounters{}
	counters.SetReceived(numRecords)
	counters.SetAccepted(numRecords)
	counters.SetDroppedByInterface(3)
	if err := w.Close(counters); err != nil {
		t.Fatalf("Close: %v", err)
	}

	blocks := splitBlocks(t, buf.Bytes())
	if len(blocks) != 2+numRecords+1 {
		t.Fatalf("got %d blocks, want %d (SHB + IDB + %d EPBs + ISB)", len(blocks), 2+numRecords+1, numRecords)
	}

	shb := blocks[0]
	if shb.typ != blockTypeSHB {
		t.Fatalf("block 0 type = %#x, want SHB %#x", shb.typ, blockTypeSHB)
	}
	if magic := binary.LittleEndian.Uint32(shb.body[0:4]); magic != 0x1A2B3C4D {
		t.Errorf("SHB byte-order magic = %#x, want 0x1a2b3c4d", magic)
	}

	idb := blocks[1]
	if idb.typ != blockTypeIDB {
		t.Fatalf("block 1 type = %#x, want IDB %#x", idb.typ, blockTypeIDB)
	}
	if linkType := binary.LittleEndian.Uint16(idb.body[0:2]); linkType != uint16(capturev1.LinkType_LINK_TYPE_ETHERNET) {
		t.Errorf("IDB LinkType = %d, want %d", linkType, capturev1.LinkType_LINK_TYPE_ETHERNET)
	}
	if snapLen := binary.LittleEndian.Uint32(idb.body[4:8]); snapLen != 128 {
		t.Errorf("IDB SnapLen = %d, want 128", snapLen)
	}

	for i := range numRecords {
		epb := blocks[2+i]
		if epb.typ != blockTypeEPB {
			t.Fatalf("block %d type = %#x, want EPB %#x", 2+i, epb.typ, blockTypeEPB)
		}
		capturedLen := binary.LittleEndian.Uint32(epb.body[12:16])
		originalLen := binary.LittleEndian.Uint32(epb.body[16:20])
		if capturedLen != 14 || originalLen != 14 {
			t.Errorf("EPB %d captured/original length = %d/%d, want 14/14", i, capturedLen, originalLen)
		}
		data := epb.body[20 : 20+capturedLen]
		want := bytes.Repeat([]byte{byte(i)}, 14)
		if !bytes.Equal(data, want) {
			t.Errorf("EPB %d data = %x, want %x", i, data, want)
		}
	}

	isb := blocks[len(blocks)-1]
	if isb.typ != blockTypeISB {
		t.Fatalf("last block type = %#x, want ISB %#x", isb.typ, blockTypeISB)
	}
	opts := parseOptions(t, isb.body[12:])
	if got := binary.LittleEndian.Uint64(opts[4]); got != numRecords {
		t.Errorf("ISB isb_ifrecv = %d, want %d", got, numRecords)
	}
	if got := binary.LittleEndian.Uint64(opts[5]); got != 3 {
		t.Errorf("ISB isb_ifdrop = %d, want 3", got)
	}
	if got := binary.LittleEndian.Uint64(opts[6]); got != numRecords {
		t.Errorf("ISB isb_filteraccept = %d, want %d", got, numRecords)
	}
}

// parseOptions walks a pcapng options list by hand: option code (2 bytes),
// option length (2 bytes), value padded to 32 bits, terminated by
// opt_endofopt (code 0, length 0).
func parseOptions(t *testing.T, buf []byte) map[uint16][]byte {
	t.Helper()
	opts := map[uint16][]byte{}
	for len(buf) > 0 {
		if len(buf) < 4 {
			t.Fatalf("trailing %d bytes are too short for an option header", len(buf))
		}
		code := binary.LittleEndian.Uint16(buf[0:2])
		length := binary.LittleEndian.Uint16(buf[2:4])
		if code == 0 {
			return opts
		}
		pad := (4 - int(length)%4) % 4
		opts[code] = buf[4 : 4+length]
		buf = buf[4+int(length)+pad:]
	}
	t.Fatalf("options list has no opt_endofopt terminator")
	return nil
}

// TestWriter_StickyError proves a write failure is remembered: once
// WriteRecord fails, a later call returns the same error without writing to
// the underlying io.Writer again.
func TestWriter_StickyError(t *testing.T) {
	fw := &failAfterWriter{failAfter: 2} // SHB, then IDB, then fail
	w, err := pcapng.NewWriter(fw, capturev1.LinkType_LINK_TYPE_ETHERNET, 128)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	rec := newRecord(t, 0, []byte{1, 2, 3})
	err1 := w.WriteRecord(rec)
	if err1 == nil {
		t.Fatalf("WriteRecord: want an error from the third write, got nil")
	}
	callsAfterFirstError := fw.calls

	err2 := w.WriteRecord(rec)
	if err2 != err1 {
		t.Errorf("WriteRecord after a failure returned a different error: %v, want %v", err2, err1)
	}
	if fw.calls != callsAfterFirstError {
		t.Errorf("WriteRecord after a failure wrote to the underlying io.Writer again (%d calls, want %d)", fw.calls, callsAfterFirstError)
	}

	if err3 := w.Close(&capturev1.CaptureCounters{}); err3 != err1 {
		t.Errorf("Close after a failure returned %v, want the sticky error %v", err3, err1)
	}
	if fw.calls != callsAfterFirstError {
		t.Errorf("Close after a failure wrote to the underlying io.Writer again (%d calls, want %d)", fw.calls, callsAfterFirstError)
	}
}

// TestWriter_AbsentCountersOmitted proves a CaptureCounters field the
// engine never set is left out of the Interface Statistics Block's options
// rather than written as a reported zero.
func TestWriter_AbsentCountersOmitted(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcapng.NewWriter(&buf, capturev1.LinkType_LINK_TYPE_ETHERNET, 128)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	counters := &capturev1.CaptureCounters{}
	counters.SetAccepted(5) // received and dropped_by_interface stay unset.
	if err := w.Close(counters); err != nil {
		t.Fatalf("Close: %v", err)
	}

	blocks := splitBlocks(t, buf.Bytes())
	isb := blocks[len(blocks)-1]
	opts := parseOptions(t, isb.body[12:])
	if _, ok := opts[4]; ok {
		t.Errorf("ISB carries isb_ifrecv though received was never set")
	}
	if _, ok := opts[5]; ok {
		t.Errorf("ISB carries isb_ifdrop though dropped_by_interface was never set")
	}
	if got, ok := opts[6]; !ok || binary.LittleEndian.Uint64(got) != 5 {
		t.Errorf("ISB isb_filteraccept = %v (ok=%v), want 5", got, ok)
	}
}

// TestWriter_CloseThenWriteRecordFails proves a write after a successful
// Close is rejected, rather than appending an Enhanced Packet Block after
// the Interface Statistics Block that was meant to summarize the section.
func TestWriter_CloseThenWriteRecordFails(t *testing.T) {
	var buf bytes.Buffer
	w, err := pcapng.NewWriter(&buf, capturev1.LinkType_LINK_TYPE_ETHERNET, 128)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.Close(&capturev1.CaptureCounters{}); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.WriteRecord(newRecord(t, 0, []byte{1, 2, 3})); err == nil {
		t.Error("WriteRecord after Close: want an error, got nil")
	}
	if err := w.Close(&capturev1.CaptureCounters{}); err == nil {
		t.Error("second Close: want an error, got nil")
	}
}

// failAfterWriter succeeds for its first failAfter calls, then fails every
// call after that.
type failAfterWriter struct {
	calls     int
	failAfter int
}

func (f *failAfterWriter) Write(p []byte) (int, error) {
	f.calls++
	if f.calls > f.failAfter {
		return 0, bytes.ErrTooLarge
	}
	return len(p), nil
}
