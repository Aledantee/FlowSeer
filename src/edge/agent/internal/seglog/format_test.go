package seglog

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

var literalSegment = []byte{
	0x46, 0x53, 0x53, 0x4c, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x80, 0xeb, 0xb0, 0xb1,
	0x03, 0x00, 0x00, 0x00, 0xb7, 0x3f, 0x4b, 0x36,
	0x61, 0x62, 0x63,
}

func TestEncodeSegmentLiteral(t *testing.T) {
	buf := make([]byte, 1, 64)
	buf[0] = 0xff
	got, err := appendRecord(appendSegmentHeader(buf, time.Unix(0, 1)), []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte{0xff}, literalSegment...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
	if &got[0] != &buf[0] {
		t.Fatal("encoding did not use the caller's buffer")
	}
}

func TestCRC32CVector(t *testing.T) {
	// RFC 3720 appendix B.4: https://www.rfc-editor.org/rfc/rfc3720#appendix-B.4
	got, err := appendRecord(nil, make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if want := []byte{0xaa, 0x36, 0x91, 0x8a}; !bytes.Equal(got[4:8], want) {
		t.Fatalf("got % x, want % x", got[4:8], want)
	}
}

func TestAppendRecordEmpty(t *testing.T) {
	buf := []byte{0xab}
	got, err := appendRecord(buf, nil)
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeRecord {
		t.Fatalf("got %v, want record error", err)
	}
	if !bytes.Equal(got, buf) {
		t.Fatalf("got % x, want % x", got, buf)
	}
}

func TestScanSegmentOrder(t *testing.T) {
	created := time.Unix(0, -1)
	buf := appendSegmentHeader(nil, created)
	want := [][]byte{[]byte("c"), []byte("a"), []byte("b")}
	for _, record := range want {
		var err error
		buf, err = appendRecord(buf, record)
		if err != nil {
			t.Fatal(err)
		}
	}
	got := scanSegment(buf)
	if !got.created.Equal(created) || got.offset != len(buf) || got.reason != "" {
		t.Fatalf("got time %v offset %d reason %q, want %v %d empty reason", got.created, got.offset, got.reason, created, len(buf))
	}
	if len(got.records) != len(want) {
		t.Fatalf("got %d records, want %d", len(got.records), len(want))
	}
	for i := range want {
		if !bytes.Equal(got.records[i], want[i]) {
			t.Errorf("record %d: got %q, want %q", i, got.records[i], want[i])
		}
	}
	empty := scanSegment(buf[:20])
	if len(empty.records) != 0 || empty.offset != 20 || empty.reason != "" || !empty.created.Equal(created) {
		t.Fatalf("got %+v, want valid empty segment", empty)
	}
}

func TestScanSegmentDamage(t *testing.T) {
	tests := []struct {
		name   string
		body   []byte
		reason scanReason
		offset int
		prefix bool
	}{
		{"short header", bytes.Clone(literalSegment[:19]), scanShortHeader, 0, false},
		{"bad magic", bytes.Clone(literalSegment), scanBadMagic, 0, false},
		{"unknown version", bytes.Clone(literalSegment), scanUnknownVersion, 0, false},
		{"header checksum", bytes.Clone(literalSegment), scanHeaderChecksum, 0, false},
		{"short record header", append(bytes.Clone(literalSegment), 1, 0, 0), scanShortHeader, 31, true},
		{"zero length", append(bytes.Clone(literalSegment), make([]byte, 8)...), scanZeroLength, 31, true},
		{"length past end", append(bytes.Clone(literalSegment), 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0), scanLengthPastEnd, 31, true},
		{"payload checksum", append(bytes.Clone(literalSegment), literalSegment[20:]...), scanPayloadChecksum, 31, true},
	}
	tests[1].body[0] = 'X'
	tests[2].body[4] = 2
	tests[3].body[8] ^= 1
	tests[7].body[39] ^= 1
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanSegment(tt.body)
			if got.reason != tt.reason || got.offset != tt.offset {
				t.Fatalf("got reason %q offset %d, want %q %d", got.reason, got.offset, tt.reason, tt.offset)
			}
			if tt.prefix {
				if !got.created.Equal(time.Unix(0, 1)) || len(got.records) != 1 || !bytes.Equal(got.records[0], []byte("abc")) {
					t.Fatalf("got %+v, want creation time 1 and prefix [abc]", got)
				}
			} else if len(got.records) != 0 || !got.created.IsZero() {
				t.Fatalf("got %+v, want no accepted header or records", got)
			}
		})
	}
}

func TestParseSegmentName(t *testing.T) {
	tests := []struct {
		name   string
		seq    uint64
		sealed bool
		ok     bool
	}{
		{"00000000000000af.seg", 175, true, true},
		{"00000000000000af.open", 175, false, true},
		{"ffffffffffffffff.seg", 0xffffffffffffffff, true, true},
		{"0000000000000000.open", 0, false, true},
		{"00000000000000a.seg", 0, false, false},
		{"00000000000000AF.seg", 0, false, false},
		{"hub.creds", 0, false, false},
		{"00000000000000af.tmp", 0, false, false},
		{"00000000000000ag.seg", 0, false, false},
		{"+0000000000000af.seg", 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seq, sealed, ok := parseSegmentName(tt.name)
			if seq != tt.seq || sealed != tt.sealed || ok != tt.ok {
				t.Fatalf("got (%d, %t, %t), want (%d, %t, %t)", seq, sealed, ok, tt.seq, tt.sealed, tt.ok)
			}
		})
	}
	if openSuffix != ".open" || sealedSuffix != ".seg" {
		t.Fatalf("got suffixes %q %q, want .open .seg", openSuffix, sealedSuffix)
	}
}

func TestErrorCodes(t *testing.T) {
	if ErrCodeConfig.String() != "agent/seglog-config" || ErrCodeIO.String() != "agent/seglog-io" || ErrCodeRecord.String() != "agent/seglog-record" || ErrCodeClosed.String() != "agent/seglog-closed" {
		t.Fatal("segment log error codes differ from their contract")
	}
}

func TestHeaderSignedTime(t *testing.T) {
	got := appendSegmentHeader(nil, time.Unix(0, -1))
	if want := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}; !bytes.Equal(got[8:16], want) {
		t.Fatalf("got % x, want % x", got[8:16], want)
	}
	if binary.LittleEndian.Uint16(got[6:8]) != 0 {
		t.Fatalf("got reserved bytes % x, want 00 00", got[6:8])
	}
}
