package bpfdev

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

type testRecord struct {
	hdrlen  uint16
	caplen  uint32
	datalen uint32
	sec     uint64
	usec    uint64
	data    []byte
}

// appendRecord appends r laid out as hdrLayout64 describes, padded to the
// next word boundary unless it ends the buffer.
func appendRecord(buf []byte, r testRecord, pad bool) []byte {
	hdr := make([]byte, r.hdrlen)
	binary.LittleEndian.PutUint64(hdr[0:], r.sec)
	binary.LittleEndian.PutUint64(hdr[8:], r.usec)
	binary.LittleEndian.PutUint32(hdr[16:], r.caplen)
	binary.LittleEndian.PutUint32(hdr[20:], r.datalen)
	binary.LittleEndian.PutUint16(hdr[24:], r.hdrlen)
	buf = append(buf, hdr...)
	buf = append(buf, r.data...)
	if pad {
		for len(buf)%8 != 0 {
			buf = append(buf, 0)
		}
	}
	return buf
}

func TestParseBufferTwoRecordsAligned(t *testing.T) {
	a := bytes.Repeat([]byte{0xaa}, 60)
	b := bytes.Repeat([]byte{0xbb}, 64)
	var buf []byte
	buf = appendRecord(buf, testRecord{hdrlen: 26, caplen: 60, datalen: 60, sec: 1700000000, usec: 5, data: a}, true)
	buf = appendRecord(buf, testRecord{hdrlen: 26, caplen: 64, datalen: 64, sec: 1700000001, usec: 7, data: b}, false)
	if len(buf) != 178 {
		t.Fatalf("fixture length = %d, want 178", len(buf))
	}

	recs, err := parseBuffer(buf, hdrLayout64)
	if err != nil {
		t.Fatalf("parseBuffer: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	if !bytes.Equal(recs[0].Data, a) {
		t.Errorf("record 0 data = %x, want %x", recs[0].Data, a)
	}
	if !bytes.Equal(recs[1].Data, b) {
		t.Errorf("record 1 data = %x, want %x", recs[1].Data, b)
	}
	if &recs[1].Data[0] != &buf[114] {
		t.Errorf("record 1 data does not start at buffer offset 114")
	}
	if want := time.Unix(1700000001, 7000); !recs[1].Timestamp.Equal(want) {
		t.Errorf("record 1 timestamp = %v, want %v", recs[1].Timestamp, want)
	}
}

func TestParseBufferWireLength(t *testing.T) {
	buf := appendRecord(nil, testRecord{hdrlen: 26, caplen: 64, datalen: 1514, data: make([]byte, 64)}, false)

	recs, err := parseBuffer(buf, hdrLayout64)
	if err != nil {
		t.Fatalf("parseBuffer: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].CapturedLength != 64 {
		t.Errorf("CapturedLength = %d, want 64", recs[0].CapturedLength)
	}
	if recs[0].WireLength != 1514 {
		t.Errorf("WireLength = %d, want 1514", recs[0].WireLength)
	}
}

func TestParseBufferTruncatedCaplen(t *testing.T) {
	buf := appendRecord(nil, testRecord{hdrlen: 26, caplen: 2000, datalen: 2000, data: make([]byte, 74)}, false)
	if len(buf) != 100 {
		t.Fatalf("fixture length = %d, want 100", len(buf))
	}

	recs, err := parseBuffer(buf, hdrLayout64)
	if !errors.Is(err, errs.New().Code(ErrCodeBuffer).Msg("")) {
		t.Errorf("error = %v, want code %q", err, ErrCodeBuffer)
	}
	if len(recs) != 0 {
		t.Errorf("got %d records, want none", len(recs))
	}
}

func TestParseBufferEmpty(t *testing.T) {
	recs, err := parseBuffer(nil, hdrLayout64)
	if err != nil {
		t.Errorf("error = %v, want nil", err)
	}
	if len(recs) != 0 {
		t.Errorf("got %d records, want none", len(recs))
	}
}

func TestParseBufferShortHdrlen(t *testing.T) {
	buf := appendRecord(nil, testRecord{hdrlen: 26, caplen: 4, datalen: 4, data: make([]byte, 4)}, false)
	binary.LittleEndian.PutUint16(buf[24:], 10)
	// Without the hdrlen check the walk would step 16 bytes and land on a
	// well-formed empty record here, so only that check can refuse the buffer.
	buf = append(buf, make([]byte, 12)...)
	binary.LittleEndian.PutUint32(buf[32:], 0)
	binary.LittleEndian.PutUint16(buf[40:], 26)

	recs, err := parseBuffer(buf, hdrLayout64)
	if !errors.Is(err, errs.New().Code(ErrCodeBuffer).Msg("")) {
		t.Errorf("error = %v, want code %q", err, ErrCodeBuffer)
	}
	if len(recs) != 0 {
		t.Errorf("got %d records, want none", len(recs))
	}
}

// pcapFrame is one record of a classic pcap file.
type pcapFrame struct {
	data    []byte
	origLen int
}

// readPcap reads the classic libpcap format that tcpdump -w writes: a 24-byte
// global header whose magic 0xa1b2c3d4 (microsecond timestamps) fixes the byte
// order, then records of a 16-byte header (ts_sec, ts_usec, incl_len,
// orig_len) and incl_len bytes.
func readPcap(t *testing.T, path string, raw []byte) []pcapFrame {
	t.Helper()
	if len(raw) < 24 {
		t.Fatalf("%s is %d bytes, shorter than the 24-byte global header", path, len(raw))
	}
	var order binary.ByteOrder
	switch {
	case binary.LittleEndian.Uint32(raw) == 0xa1b2c3d4:
		order = binary.LittleEndian
	case binary.BigEndian.Uint32(raw) == 0xa1b2c3d4:
		order = binary.BigEndian
	default:
		t.Fatalf("%s has magic %x, want 0xa1b2c3d4 in either byte order", path, raw[:4])
	}
	var frames []pcapFrame
	for off := 24; off < len(raw); {
		if len(raw)-off < 16 {
			t.Fatalf("%s ends inside a record header at offset %d", path, off)
		}
		inclLen := int(order.Uint32(raw[off+8:]))
		origLen := int(order.Uint32(raw[off+12:]))
		off += 16
		if inclLen > len(raw)-off {
			t.Fatalf("%s record at offset %d has incl_len %d past the end", path, off-16, inclLen)
		}
		frames = append(frames, pcapFrame{data: raw[off : off+inclLen], origLen: origLen})
		off += inclLen
	}
	return frames
}

// The fixtures are embedded so the test binary carries them into the FreeBSD
// guest, where the suite runs without the source tree.
var (
	//go:embed testdata/freebsd14-arm64-read.bin
	fixtureReadBuffer []byte
	//go:embed testdata/freebsd14-arm64.pcap
	fixturePcap []byte
)

// TestParseBufferMatchesTcpdump parses one read(2) buffer captured in a
// FreeBSD 14 guest and checks it against what tcpdump, which reads the same
// kernel through libpcap, wrote for the same three frames.
func TestParseBufferMatchesTcpdump(t *testing.T) {
	want := readPcap(t, "testdata/freebsd14-arm64.pcap", fixturePcap)
	if len(want) != 3 {
		t.Fatalf("pcap holds %d frames, want 3", len(want))
	}

	got, err := parseBuffer(fixtureReadBuffer, hdrLayout64)
	if err != nil {
		t.Fatalf("parseBuffer: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("parseBuffer returned %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i].Data, want[i].data) {
			t.Errorf("record %d data differs from the pcap frame (%d bytes parsed, %d in pcap)", i, len(got[i].Data), len(want[i].data))
		}
		if got[i].CapturedLength != len(want[i].data) {
			t.Errorf("record %d CapturedLength = %d, want %d", i, got[i].CapturedLength, len(want[i].data))
		}
		if got[i].WireLength != want[i].origLen {
			t.Errorf("record %d WireLength = %d, want %d (orig_len)", i, got[i].WireLength, want[i].origLen)
		}
	}
}

func TestParseBufferShortRemainderBeforeHeader(t *testing.T) {
	// Ten bytes cannot hold the smallest header, so the parser must refuse
	// them before it reads a header field.
	recs, err := parseBuffer(make([]byte, 10), hdrLayout64)
	if !errors.Is(err, errs.New().Code(ErrCodeBuffer).Msg("")) {
		t.Fatalf("error = %v, want code %q", err, ErrCodeBuffer)
	}
	if recs != nil {
		t.Errorf("got %d records with the error, want none", len(recs))
	}
}
