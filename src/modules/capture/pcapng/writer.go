package pcapng

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
)

// Block types (draft-ietf-opsawg-pcapng-05).
const (
	blockTypeSHB = 0x0A0D0D0A
	blockTypeIDB = 0x00000001
	blockTypeISB = 0x00000005
	blockTypeEPB = 0x00000006

	byteOrderMagic = 0x1A2B3C4D
)

// Option codes this writer emits. optEndOfOpt terminates an options list;
// the ISB codes are the draft's own field names.
const (
	optEndOfOpt        = 0
	optISBIfRecv       = 4
	optISBIfDrop       = 5
	optISBFilterAccept = 6
)

// Writer streams one pcapng file: a Section Header Block and an Interface
// Description Block on construction, one Enhanced Packet Block per
// WriteRecord call, and an Interface Statistics Block on Close. It is not
// safe for concurrent use; a capture has one record-producing goroutine.
//
// Once a write fails, every later call returns the same error without
// touching the underlying io.Writer again.
type Writer struct {
	w        io.Writer
	linkType capturev1.LinkType
	snapLen  uint32
	err      error
}

// NewWriter writes the Section Header Block and one Interface Description
// Block naming linkType and snapLen, then returns a Writer ready for
// WriteRecord calls.
func NewWriter(w io.Writer, linkType capturev1.LinkType, snapLen uint32) (*Writer, error) {
	wr := &Writer{w: w, linkType: linkType, snapLen: snapLen}
	if err := wr.writeSHB(); err != nil {
		return nil, err
	}
	if err := wr.writeIDB(); err != nil {
		return nil, err
	}
	return wr, nil
}

// WriteRecord appends one Enhanced Packet Block for rec.
func (wr *Writer) WriteRecord(rec *capturev1.PacketRecord) error {
	if wr.err != nil {
		return wr.err
	}

	data := rec.GetData()
	// The if_tsresol option is not written on the Interface Description
	// Block, so draft-ietf-opsawg-pcapng-05 defines the timestamp's
	// resolution as 10^-6 (microseconds) by default.
	micros := uint64(rec.GetCapturedAt().AsTime().UnixMicro())

	body := make([]byte, 20, 20+len(data))
	binary.LittleEndian.PutUint32(body[0:4], 0) // interface id: the one IDB written
	binary.LittleEndian.PutUint32(body[4:8], uint32(micros>>32))
	binary.LittleEndian.PutUint32(body[8:12], uint32(micros))
	binary.LittleEndian.PutUint32(body[12:16], uint32(len(data)))
	binary.LittleEndian.PutUint32(body[16:20], rec.GetOriginalLength())
	body = append(body, data...)

	if err := wr.writeBlock(blockTypeEPB, body); err != nil {
		wr.err = err
		return err
	}
	return nil
}

// errClosed is Close's sticky error once it has already succeeded: a
// second Close, or a WriteRecord after one, would otherwise append another
// block past what the Interface Statistics Block summarized.
var errClosed = errors.New("pcapng: writer is already closed")

// Close appends one Interface Statistics Block carrying counters. Only the
// three CaptureCounters fields the pcapng draft's own ISB options name have
// a home there (isb_ifrecv, isb_ifdrop, isb_filteraccept); dropped_by_budget
// and dropped_by_transport are FlowSeer accounting concepts the draft's
// vocabulary has no option for, and are not forced into one that would
// misstate their meaning. A counter CaptureCounters leaves absent is
// omitted from the block rather than written as a reported zero, matching
// the schema's own "absent means the stage does not report one — never a
// zero" rule.
func (wr *Writer) Close(counters *capturev1.CaptureCounters) error {
	if wr.err != nil {
		return wr.err
	}

	micros := uint64(time.Now().UnixMicro())
	body := make([]byte, 12)
	binary.LittleEndian.PutUint32(body[0:4], 0)
	binary.LittleEndian.PutUint32(body[4:8], uint32(micros>>32))
	binary.LittleEndian.PutUint32(body[8:12], uint32(micros))

	if counters.HasReceived() {
		body = appendOption(body, optISBIfRecv, encodeU64(counters.GetReceived()))
	}
	if counters.HasDroppedByInterface() {
		body = appendOption(body, optISBIfDrop, encodeU64(counters.GetDroppedByInterface()))
	}
	if counters.HasAccepted() {
		body = appendOption(body, optISBFilterAccept, encodeU64(counters.GetAccepted()))
	}
	body = appendOption(body, optEndOfOpt, nil)

	if err := wr.writeBlock(blockTypeISB, body); err != nil {
		wr.err = err
		return err
	}
	wr.err = errClosed
	return nil
}

func (wr *Writer) writeSHB() error {
	body := make([]byte, 0, 16)
	body = binary.LittleEndian.AppendUint32(body, byteOrderMagic)
	body = binary.LittleEndian.AppendUint16(body, 1)              // major version
	body = binary.LittleEndian.AppendUint16(body, 0)              // minor version
	body = binary.LittleEndian.AppendUint64(body, math.MaxUint64) // section length: -1, unspecified
	return wr.writeBlock(blockTypeSHB, body)
}

func (wr *Writer) writeIDB() error {
	body := make([]byte, 8)
	binary.LittleEndian.PutUint16(body[0:2], uint16(wr.linkType))
	// bytes 2:4 are the reserved field, left zero.
	binary.LittleEndian.PutUint32(body[4:8], wr.snapLen)
	return wr.writeBlock(blockTypeIDB, body)
}

// writeBlock frames body in the generic block structure: type, total
// length, the body padded to a 32-bit boundary, and the trailing total
// length every block repeats.
func (wr *Writer) writeBlock(blockType uint32, body []byte) error {
	pad := (4 - len(body)%4) % 4
	total := 12 + len(body) + pad

	buf := make([]byte, 0, total)
	buf = binary.LittleEndian.AppendUint32(buf, blockType)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(total))
	buf = append(buf, body...)
	buf = append(buf, make([]byte, pad)...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(total))

	_, err := wr.w.Write(buf)
	return err
}

func appendOption(body []byte, code uint16, value []byte) []byte {
	body = binary.LittleEndian.AppendUint16(body, code)
	body = binary.LittleEndian.AppendUint16(body, uint16(len(value)))
	body = append(body, value...)
	pad := (4 - len(value)%4) % 4
	return append(body, make([]byte, pad)...)
}

func encodeU64(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}
