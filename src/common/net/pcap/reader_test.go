package pcap_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/pcap"
)

var testFrame = append([]byte{
	0x02, 0, 0, 0, 0, 2, 0x02, 0, 0, 0, 0, 1, 0x08, 0,
}, make([]byte, 46)...)

func TestReaderClassicFixture(t *testing.T) {
	wire, err := os.ReadFile("testdata/classic_micro_le.pcap")
	if err != nil {
		t.Fatal(err)
	}
	if got := wire[:8]; !bytes.Equal(got, []byte{0xd4, 0xc3, 0xb2, 0xa1, 2, 0, 4, 0}) {
		t.Fatalf("header = % x, want little-endian microsecond pcap", got)
	}
	if got := wire[16:24]; !bytes.Equal(got, []byte{0xff, 0xff, 0, 0, 1, 0, 0, 0}) {
		t.Fatalf("snaplen and link type = % x, want literal header fields", got)
	}
	if got := wire[24:40]; !bytes.Equal(got, []byte{
		0, 0xf1, 0x53, 0x65, 0xfa, 0, 0, 0, 0x3c, 0, 0, 0, 0x3c, 0, 0, 0,
	}) {
		t.Fatalf("record header = % x, want literal timestamp and lengths", got)
	}
	if got := wire[40:54]; !bytes.Equal(got, testFrame[:14]) {
		t.Fatalf("Ethernet prefix = % x, want % x", got, testFrame[:14])
	}

	r, err := pcap.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	var firstData []byte
	for i, micros := range []int64{250, 500} {
		rec, err := r.Next()
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		wantAt := time.Unix(1_700_000_000, micros*1_000)
		if !rec.At.Equal(wantAt) || rec.OrigLen != 60 || rec.LinkType != 1 || rec.HasFCS {
			t.Errorf("record %d metadata = %+v, want %s, length 60, Ethernet, no declared FCS", i, rec, wantAt)
		}
		if !bytes.Equal(rec.Data, testFrame) {
			t.Errorf("record %d data = % x, want % x", i, rec.Data, testFrame)
		}
		if i == 0 {
			firstData = rec.Data
			firstData[0] = 0xff
		} else if firstData[0] != 0xff {
			t.Errorf("first record data changed after Next: % x", firstData[:14])
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Errorf("end = %v, want io.EOF", err)
	}
}

func TestReaderClassicVariants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order binary.ByteOrder
		nanos bool
	}{
		{"little microseconds", binary.LittleEndian, false},
		{"big microseconds", binary.BigEndian, false},
		{"little nanoseconds", binary.LittleEndian, true},
		{"big nanoseconds", binary.BigEndian, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fraction := uint32(250)
			wantNano := int64(250_000)
			if tc.nanos {
				fraction = 250_000
			}
			r, err := pcap.NewReader(bytes.NewReader(classicWire(tc.order, tc.nanos, fraction, 60, 60, 0)))
			if err != nil {
				t.Fatal(err)
			}
			rec, err := r.Next()
			if err != nil {
				t.Fatal(err)
			}
			if want := time.Unix(1_700_000_000, wantNano); !rec.At.Equal(want) {
				t.Errorf("At = %s, want %s", rec.At, want)
			}
		})
	}
}

func TestReaderClassicRefusals(t *testing.T) {
	valid := classicWire(binary.LittleEndian, false, 250, 60, 60, 0)
	for _, tc := range []struct {
		name  string
		wire  []byte
		atNew bool
	}{
		{"unknown magic", append([]byte{0, 0, 0, 0}, valid[4:]...), true},
		{"truncated file header", valid[:12], true},
		{"wrong version", replaceBytes(valid, 4, []byte{3, 0}), true},
		{"truncated record header", valid[:30], false},
		{"record header without packet", valid[:40], false},
		{"truncated packet", valid[:len(valid)-1], false},
		{"fraction outside second", classicWire(binary.LittleEndian, false, 1_000_000, 60, 60, 0), false},
		{"packet above limit", classicWire(binary.LittleEndian, false, 250, 1<<20+1, 1<<20+1, 0), false},
		{"packet above snaplen", classicWire(binary.LittleEndian, false, 250, 60, 60, 32), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := pcap.NewReader(bytes.NewReader(tc.wire))
			if tc.atNew {
				if err == nil || errors.Is(err, io.EOF) {
					t.Fatalf("NewReader error = %v, want structural error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}
			if _, err := r.Next(); err == nil || errors.Is(err, io.EOF) {
				t.Fatalf("Next error = %v, want structural error", err)
			}
		})
	}
}

func TestReaderClassicFCSFlags(t *testing.T) {
	const (
		fcsPresentBit  = uint32(1 << 26)
		fcsLengthShift = 28
	)
	for fieldBits := uint32(0); fieldBits < 1<<6; fieldBits++ {
		linkWord := uint32(1) | fieldBits<<26
		wantFCS := linkWord&fcsPresentBit != 0 && linkWord>>fcsLengthShift != 0
		t.Run(fmt.Sprintf("link word %#08x", linkWord), func(t *testing.T) {
			r, err := pcap.NewReader(bytes.NewReader(classicWire(binary.LittleEndian, false, 0, 60, 60, linkWord)))
			if err != nil {
				t.Fatal(err)
			}
			rec, err := r.Next()
			if err != nil {
				t.Fatal(err)
			}
			if rec.HasFCS != wantFCS {
				t.Errorf("HasFCS = %t, want %t for link word %#x", rec.HasFCS, wantFCS, linkWord)
			}
		})
	}
}

func TestReaderStructuredTruncations(t *testing.T) {
	order := binary.LittleEndian
	classic := classicWire(order, false, 250, 60, 60, 0)
	section := ngSection(order)
	idb := ngIDB(order, 1, 0, nil)
	epb := ngEPB(order, 0, 0, testFrame, nil)
	packetPrefix := append(append([]byte(nil), section...), idb...)
	optionIDB := ngIDB(order, 1, 0, ngOption(order, 9, []byte{9}))
	optionSection := ngBlock(order, 0x0a0d0d0a, append(append([]byte(nil), section[8:24]...), 0, 0, 0, 0))
	unknown := ngBlock(order, 5, []byte{0, 0, 0, 0})
	paddedEPB := ngEPB(order, 0, 0, testFrame[:59], nil)

	for _, tc := range []struct {
		name    string
		wire    []byte
		atNew   bool
		context string
	}{
		{"file magic", nil, true, "file header"},
		{"classic file header", classic[:4], true, "file header"},
		{"classic record data", classic[:40], false, "packet data"},
		{"pcapng section length", section[:4], true, "section header"},
		{"pcapng section byte order", section[:8], true, "section byte order"},
		{"pcapng section fields", section[:12], true, "section fields"},
		{"pcapng section options header", optionSection[:24], true, "section options"},
		{"pcapng section trailer", section[:24], true, "block trailer"},
		{"pcapng interface fields", append(section, idb[:8]...), false, "interface fields"},
		{"pcapng interface options header", append(section, optionIDB[:16]...), false, "interface options"},
		{"pcapng interface options value", append(section, optionIDB[:20]...), false, "interface options"},
		{"pcapng interface options padding", append(section, optionIDB[:21]...), false, "interface options"},
		{"pcapng packet fields", append(packetPrefix, epb[:8]...), false, "packet fields"},
		{"pcapng packet data", append(packetPrefix, epb[:28]...), false, "packet data"},
		{"pcapng packet padding", append(packetPrefix, paddedEPB[:87]...), false, "packet padding"},
		{"pcapng unknown block body", append(section, unknown[:8]...), false, "block type 5"},
		{"pcapng packet trailer", append(packetPrefix, epb[:len(epb)-4]...), false, "block trailer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := pcap.NewReader(bytes.NewReader(tc.wire))
			if !tc.atNew && err == nil {
				_, err = r.Next()
			}
			if err == nil || errors.Is(err, io.EOF) || !strings.Contains(err.Error(), tc.context) {
				t.Fatalf("error = %v, want structural %s error", err, tc.context)
			}
		})
	}
}

func TestReaderPcapngFixture(t *testing.T) {
	wire, err := os.ReadFile("testdata/ng_nano_le.pcapng")
	if err != nil {
		t.Fatal(err)
	}
	if got := wire[72:80]; !bytes.Equal(got, []byte{0xfe, 0x9c, 0x97, 0x17, 0, 0, 0x2a, 0x36}) {
		t.Fatalf("EPB timestamp halves = % x, want literal 1.7e18 ticks", got)
	}
	if got := wire[44:52]; !bytes.Equal(got, []byte{9, 0, 1, 0, 9, 0, 0, 0}) {
		t.Fatalf("IDB resolution option = % x, want decimal nanoseconds", got)
	}
	r, err := pcap.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(1_700_000_000, 0); !rec.At.Equal(want) {
		t.Errorf("At = %s, want %s", rec.At, want)
	}
	if rec.LinkType != 1 || rec.OrigLen != 60 || rec.HasFCS || !bytes.Equal(rec.Data, testFrame) {
		t.Errorf("record = %+v, want Ethernet frame without FCS", rec)
	}
	if _, err := r.Next(); err != io.EOF {
		t.Errorf("end = %v, want io.EOF", err)
	}
}

func TestReaderPcapngInterfacesAndSections(t *testing.T) {
	little := binary.LittleEndian
	big := binary.BigEndian
	first := append(ngSection(little), ngIDB(little, 1, 0, nil)...)
	first = append(first, ngBlock(little, 0xdeadbeef, []byte{1, 2, 3, 4})...)
	first = append(first, ngEPB(little, 0, 1_700_000_000_000_000, testFrame, nil)...)
	first = append(first, ngIDB(little, 276, 0, ngOption(little, 9, []byte{0x8a}))...)
	first = append(first, ngEPB(little, 1, 1_025, testFrame, nil)...)
	second := append(ngSection(big), ngIDB(big, 1, 0, ngOption(big, 9, []byte{9}))...)
	second = append(second, ngEPB(big, 0, 1_700_000_000_000_000_000, testFrame, nil)...)
	r, err := pcap.NewReader(bytes.NewReader(append(first, second...)))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []struct {
		at       time.Time
		linkType uint16
	}{
		{time.Unix(1_700_000_000, 0), 1},
		{time.Unix(1, 976_562), 276},
		{time.Unix(1_700_000_000, 0), 1},
	} {
		rec, err := r.Next()
		if err != nil {
			t.Fatalf("record %d: %v", i, err)
		}
		if !rec.At.Equal(want.at) || rec.LinkType != want.linkType {
			t.Errorf("record %d = %s link %d, want %s link %d", i, rec.At, rec.LinkType, want.at, want.linkType)
		}
	}
	if _, err := r.Next(); err != io.EOF {
		t.Errorf("end = %v, want io.EOF", err)
	}
}

func TestReaderPcapngSectionVersions(t *testing.T) {
	order := binary.LittleEndian
	for _, tc := range []struct {
		name  string
		minor uint16
		ok    bool
	}{
		{"version 1.2", 2, true},
		{"version 1.1", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			section := ngSection(order)
			order.PutUint16(section[14:16], tc.minor)
			wire := append(append(section, ngIDB(order, 1, 0, nil)...), ngEPB(order, 0, 0, testFrame, nil)...)
			r, err := pcap.NewReader(bytes.NewReader(wire))
			if !tc.ok {
				if err == nil || !strings.Contains(err.Error(), "unsupported version 1.1") {
					t.Fatalf("NewReader error = %v, want unsupported version 1.1", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			rec, err := r.Next()
			if err != nil || !bytes.Equal(rec.Data, testFrame) {
				t.Fatalf("Next = %x, %v, want EPB data", rec.Data, err)
			}
		})
	}
}

func TestReaderPcapngMisalignedSection(t *testing.T) {
	order := binary.LittleEndian
	sectionBody := append([]byte(nil), ngSection(order)[8:24]...)
	section := ngBlock(order, 0x0a0d0d0a, append(sectionBody, 0, 0))
	_, err := pcap.NewReader(bytes.NewReader(section))
	if err == nil || !strings.Contains(err.Error(), "invalid section length 30") {
		t.Fatalf("NewReader error = %v, want invalid section length 30", err)
	}
}

func TestReaderPcapngOffsetAndFCS(t *testing.T) {
	order := binary.LittleEndian
	idbOptions := append(ngOption(order, 14, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}), ngOption(order, 13, []byte{4})...)
	wire := append(ngSection(order), ngIDB(order, 1, 0, idbOptions)...)
	wire = append(wire, ngEPB(order, 0, 2_000_000, testFrame, nil)...)
	r, err := pcap.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(1, 0); !rec.At.Equal(want) || !rec.HasFCS {
		t.Errorf("At/FCS = %s/%t, want %s/true", rec.At, rec.HasFCS, want)
	}

	flags := make([]byte, 4)
	order.PutUint32(flags, 4<<5)
	wire = append(ngSection(order), ngIDB(order, 1, 0, nil)...)
	wire = append(wire, ngEPB(order, 0, 0, testFrame, ngOption(order, 2, flags))...)
	r, err = pcap.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	rec, err = r.Next()
	if err != nil || !rec.HasFCS {
		t.Errorf("EPB FCS = %t, error %v, want true", rec.HasFCS, err)
	}

	classic, err := pcap.NewReader(bytes.NewReader(classicWire(order, false, 0, 60, 60, 0x24000001)))
	if err != nil {
		t.Fatal(err)
	}
	rec, err = classic.Next()
	if err != nil || !rec.HasFCS {
		t.Errorf("classic FCS = %t, error %v, want true", rec.HasFCS, err)
	}
}

func TestReaderPcapngSubNanosecondResolution(t *testing.T) {
	order := binary.LittleEndian
	wire := append(ngSection(order), ngIDB(order, 1, 0, ngOption(order, 9, []byte{20}))...)
	wire = append(wire, ngEPB(order, 0, 1_700_000_000_000_000_001, testFrame, nil)...)
	r, err := pcap.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := r.Next()
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Unix(0, 17_000_000); !rec.At.Equal(want) {
		t.Errorf("At = %s, want %s", rec.At, want)
	}
}

func TestReaderPcapngTimestampTimeRange(t *testing.T) {
	const maxUnixSeconds = int64(^uint64(0)>>1) - 62_135_596_800
	order := binary.LittleEndian
	for _, tc := range []struct {
		name   string
		offset int64
	}{
		{"no offset", 0},
		{"positive offset", 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := ngOption(order, 9, []byte{0})
			if tc.offset > 0 {
				offset := make([]byte, 8)
				order.PutUint64(offset, uint64(tc.offset))
				options = append(options, ngOption(order, 14, offset)...)
			}
			for _, boundary := range []struct {
				name    string
				ticks   uint64
				wantErr bool
			}{
				{"last valid second", uint64(maxUnixSeconds - tc.offset), false},
				{"first invalid second", uint64(maxUnixSeconds - tc.offset + 1), true},
			} {
				t.Run(boundary.name, func(t *testing.T) {
					wire := append(ngSection(order), ngIDB(order, 1, 0, options)...)
					wire = append(wire, ngEPB(order, 0, boundary.ticks, testFrame, nil)...)
					r, err := pcap.NewReader(bytes.NewReader(wire))
					if err != nil {
						t.Fatal(err)
					}
					rec, err := r.Next()
					if boundary.wantErr {
						if err == nil || !strings.Contains(err.Error(), "timestamp overflows time.Time") {
							t.Fatalf("Next error = %v, want timestamp overflow", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if got := rec.At.Unix(); got != maxUnixSeconds {
						t.Errorf("At.Unix() = %d, want %d", got, maxUnixSeconds)
					}
				})
			}
		})
	}
}

func TestReaderPcapngRefusals(t *testing.T) {
	order := binary.LittleEndian
	good := append(ngSection(order), ngIDB(order, 1, 0, nil)...)
	epbStart := len(good)
	good = append(good, ngEPB(order, 0, 0, testFrame, nil)...)
	badOptionPadding := ngOption(order, 9, []byte{9})
	badOptionPadding[len(badOptionPadding)-1] = 1
	badPaddingData := append([]byte(nil), testFrame[:59]...)
	badPadding := ngEPB(order, 0, 0, badPaddingData, nil)
	badPadding[len(badPadding)-5] = 1
	obsoleteBody := append(make([]byte, 20), testFrame...)
	order.PutUint32(obsoleteBody[12:16], 60)
	order.PutUint32(obsoleteBody[16:20], 60)
	offset := make([]byte, 8)
	order.PutUint64(offset, 1<<62-1)
	offsetOptions := append(ngOption(order, 9, []byte{0}), ngOption(order, 14, offset)...)
	for _, tc := range []struct {
		name string
		wire []byte
	}{
		{"bad trailer", replaceBytes(good, len(good)-4, []byte{0, 0, 0, 0})},
		{"truncated block", good[:len(good)-1]},
		{"truncated packet data", good[:len(good)-10]},
		{"packet block header without fields", good[:epbStart+8]},
		{"unknown block header without body", append(ngSection(order), ngBlock(order, 5, []byte{0, 0, 0, 0})[:8]...)},
		{"packet fields without data", good[:epbStart+28]},
		{"misaligned block", append(ngSection(order), ngBlock(order, 5, []byte{0, 0})...)},
		{"bad option length", append(append(ngSection(order), ngIDB(order, 1, 0, ngOption(order, 9, []byte{9, 9}))...), ngEPB(order, 0, 0, testFrame, nil)...)},
		{"bad option padding", append(append(ngSection(order), ngIDB(order, 1, 0, badOptionPadding)...), ngEPB(order, 0, 0, testFrame, nil)...)},
		{"invalid interface", append(append(ngSection(order), ngIDB(order, 1, 0, nil)...), ngEPB(order, 1, 0, testFrame, nil)...)},
		{"packet above limit", ngWithLength(order, 1<<20+1, 0)},
		{"packet above snaplen", append(append(ngSection(order), ngIDB(order, 1, 32, nil)...), ngEPB(order, 0, 0, testFrame, nil)...)},
		{"bad packet padding", append(append(ngSection(order), ngIDB(order, 1, 0, nil)...), badPadding...)},
		{"simple packet", append(append(ngSection(order), ngIDB(order, 1, 0, nil)...), ngBlock(order, 3, append([]byte{60, 0, 0, 0}, testFrame...))...)},
		{"obsolete packet", append(append(ngSection(order), ngIDB(order, 1, 0, nil)...), ngBlock(order, 2, obsoleteBody)...)},
		{"timestamp overflow", append(append(ngSection(order), ngIDB(order, 1, 0, ngOption(order, 9, []byte{0}))...), ngEPB(order, 0, ^uint64(0), testFrame, nil)...)},
		{"timestamp time range overflow", append(append(ngSection(order), ngIDB(order, 1, 0, ngOption(order, 9, []byte{0}))...), ngEPB(order, 0, 1<<63-1, testFrame, nil)...)},
		{"timestamp offset time range overflow", append(append(ngSection(order), ngIDB(order, 1, 0, offsetOptions)...), ngEPB(order, 0, 1<<62, testFrame, nil)...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := pcap.NewReader(bytes.NewReader(tc.wire))
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}
			if _, err := r.Next(); err == nil || errors.Is(err, io.EOF) {
				t.Fatalf("Next error = %v, want structural error", err)
			}
		})
	}
	if !strings.Contains(readError(t, ngWithLength(order, 1<<20+1, 0)), "1048577") {
		t.Error("oversized packet error omits captured length")
	}
}

func readError(t *testing.T, wire []byte) string {
	t.Helper()
	r, err := pcap.NewReader(bytes.NewReader(wire))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Next()
	if err == nil {
		t.Fatal("Next succeeded, want error")
	}
	return err.Error()
}

func classicWire(order binary.ByteOrder, nanos bool, fraction, captured, original, snapOrLink uint32) []byte {
	wire := make([]byte, 24+16)
	magic := uint32(0xa1b2c3d4)
	if nanos {
		magic = 0xa1b23c4d
	}
	order.PutUint32(wire[:4], magic)
	order.PutUint16(wire[4:6], 2)
	order.PutUint16(wire[6:8], 4)
	snap := uint32(65_535)
	if captured > snap {
		snap = captured
	}
	link := uint32(1)
	if snapOrLink == 32 {
		snap = 32
	} else if snapOrLink != 0 {
		link = snapOrLink
	}
	order.PutUint32(wire[16:20], snap)
	order.PutUint32(wire[20:24], link)
	order.PutUint32(wire[24:28], 1_700_000_000)
	order.PutUint32(wire[28:32], fraction)
	order.PutUint32(wire[32:36], captured)
	order.PutUint32(wire[36:40], original)
	if captured > uint32(len(testFrame)) {
		data := make([]byte, captured)
		copy(data, testFrame)
		return append(wire, data...)
	}
	return append(wire, testFrame...)
}

func replaceBytes(wire []byte, offset int, value []byte) []byte {
	out := append([]byte(nil), wire...)
	copy(out[offset:], value)
	return out
}

func ngSection(order binary.ByteOrder) []byte {
	body := make([]byte, 16)
	order.PutUint32(body[:4], 0x1a2b3c4d)
	order.PutUint16(body[4:6], 1)
	for i := 8; i < 16; i++ {
		body[i] = 0xff
	}
	return ngBlock(order, 0x0a0d0d0a, body)
}

func ngIDB(order binary.ByteOrder, link uint16, snap uint32, options []byte) []byte {
	body := make([]byte, 8)
	order.PutUint16(body[:2], link)
	order.PutUint32(body[4:8], snap)
	return ngBlock(order, 1, append(body, options...))
}

func ngEPB(order binary.ByteOrder, iface uint32, ticks uint64, data []byte, options []byte) []byte {
	body := make([]byte, 20)
	order.PutUint32(body[:4], iface)
	order.PutUint32(body[4:8], uint32(ticks>>32))
	order.PutUint32(body[8:12], uint32(ticks))
	order.PutUint32(body[12:16], uint32(len(data)))
	order.PutUint32(body[16:20], uint32(len(data)))
	body = append(body, data...)
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	return ngBlock(order, 6, append(body, options...))
}

func ngOption(order binary.ByteOrder, code uint16, value []byte) []byte {
	option := make([]byte, 4)
	order.PutUint16(option[:2], code)
	order.PutUint16(option[2:4], uint16(len(value)))
	option = append(option, value...)
	for len(option)%4 != 0 {
		option = append(option, 0)
	}
	return option
}

func ngBlock(order binary.ByteOrder, kind uint32, body []byte) []byte {
	wire := make([]byte, len(body)+12)
	order.PutUint32(wire[:4], kind)
	order.PutUint32(wire[4:8], uint32(len(wire)))
	copy(wire[8:], body)
	order.PutUint32(wire[len(wire)-4:], uint32(len(wire)))
	return wire
}

func ngWithLength(order binary.ByteOrder, captured, snap uint32) []byte {
	data := make([]byte, captured)
	copy(data, testFrame)
	epb := ngEPB(order, 0, 0, data, nil)
	return append(append(ngSection(order), ngIDB(order, 1, snap, nil)...), epb...)
}
