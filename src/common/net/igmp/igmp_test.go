package igmp_test

import (
	"encoding/binary"
	"errors"
	"math"
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/igmp"
)

func TestV2ReportRoundTrip(t *testing.T) {
	want := igmp.Message{
		Type:  igmp.ReportV2,
		Group: netip.MustParseAddr("239.1.1.1"),
	}

	wire, err := igmp.Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(wire) != 8 {
		t.Fatalf("len(Encode()) = %d, want 8", len(wire))
	}
	if wire[0] != 0x16 {
		t.Errorf("Encode()[0] = %#x, want 0x16", wire[0])
	}

	got, err := igmp.Decode(wire)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.Type != want.Type {
		t.Errorf("Decode().Type = %#x, want %#x", got.Type, want.Type)
	}
	if got.Group != want.Group {
		t.Errorf("Decode().Group = %v, want %v", got.Group, want.Group)
	}
}

func TestQueriesRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		want igmp.Message
		size int
	}{
		{
			name: "v2",
			want: igmp.Message{
				Version: igmp.V2,
				Type:    igmp.Query,
			},
			size: 8,
		},
		{
			name: "v3 with two sources",
			want: igmp.Message{
				Version:  igmp.V3,
				Type:     igmp.Query,
				MaxResp:  12800 * time.Millisecond,
				Group:    netip.MustParseAddr("239.1.1.1"),
				Sources:  []netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")},
				Suppress: true,
				QRV:      5,
				QQIC:     0x81,
			},
			size: 20,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := igmp.Encode(tc.want)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if len(wire) != tc.size {
				t.Fatalf("len(Encode()) = %d, want %d", len(wire), tc.size)
			}

			got, err := igmp.Decode(wire)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			assertMessage(t, got, tc.want)
		})
	}
}

func TestV3TimerCodeBoundaries(t *testing.T) {
	for _, tc := range []struct {
		code uint8
		want time.Duration
	}{
		{code: 127, want: 12700 * time.Millisecond},
		{code: 128, want: 12800 * time.Millisecond},
	} {
		m := igmp.Message{Version: igmp.V3, Type: igmp.Query, MaxResp: tc.want}
		wire, err := igmp.Encode(m)
		if err != nil {
			t.Fatalf("Encode(%d) error = %v", tc.code, err)
		}
		if wire[1] != tc.code {
			t.Errorf("Encode(%d) code = %d, want %d", tc.code, wire[1], tc.code)
		}

		got, err := igmp.Decode(wire)
		if err != nil {
			t.Fatalf("Decode(%d) error = %v", tc.code, err)
		}
		if got.MaxResp != tc.want {
			t.Errorf("Decode(%d).MaxResp = %v, want %v", tc.code, got.MaxResp, tc.want)
		}
	}
}

func TestV3ReportRoundTrip(t *testing.T) {
	want := igmp.Message{
		Type: igmp.ReportV3,
		Records: []igmp.GroupRecord{
			{
				Type:    igmp.ModeIsExclude,
				Group:   netip.MustParseAddr("239.1.1.1"),
				Sources: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
			},
		},
	}

	wire, err := igmp.Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(wire) != 20 {
		t.Fatalf("len(Encode()) = %d, want 20", len(wire))
	}

	got, err := igmp.Decode(wire)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	assertMessage(t, got, want)
}

func TestDecodeErrors(t *testing.T) {
	report, err := igmp.Encode(igmp.Message{
		Type:  igmp.ReportV2,
		Group: netip.MustParseAddr("239.1.1.1"),
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	badChecksum := append([]byte(nil), report...)
	badChecksum[7] ^= 0xff
	if _, err := igmp.Decode(badChecksum); !errors.Is(err, igmp.ErrMalformed) {
		t.Errorf("Decode(bad checksum) error = %v, want ErrMalformed", err)
	}

	unknown := append([]byte(nil), report...)
	unknown[0] = 0x30
	binary.BigEndian.PutUint16(unknown[2:4], 0)
	binary.BigEndian.PutUint16(unknown[2:4], internetChecksum(unknown))
	if _, err := igmp.Decode(unknown); !errors.Is(err, igmp.ErrUnsupported) {
		t.Errorf("Decode(unknown type) error = %v, want ErrUnsupported", err)
	}

	if _, err := igmp.Decode(report[:7]); !errors.Is(err, igmp.ErrMalformed) {
		t.Errorf("Decode(short message) error = %v, want ErrMalformed", err)
	}

	inconsistent, err := igmp.Encode(igmp.Message{
		Version: igmp.V3,
		Type:    igmp.Query,
		Group:   netip.MustParseAddr("239.1.1.1"),
		Sources: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	inconsistent = inconsistent[:12]
	binary.BigEndian.PutUint16(inconsistent[2:4], 0)
	binary.BigEndian.PutUint16(inconsistent[2:4], internetChecksum(inconsistent))
	if _, err := igmp.Decode(inconsistent); !errors.Is(err, igmp.ErrMalformed) {
		t.Errorf("Decode(inconsistent query) error = %v, want ErrMalformed", err)
	}
}

func TestEncodeValidation(t *testing.T) {
	v4Source := netip.MustParseAddr("192.0.2.1")

	tests := []struct {
		name string
		m    igmp.Message
		want error
	}{
		{name: "wrong group family", m: igmp.Message{Type: igmp.ReportV2, Group: netip.MustParseAddr("ff05::1")}, want: igmp.ErrMalformed},
		{name: "unicast group", m: igmp.Message{Type: igmp.ReportV2, Group: netip.MustParseAddr("192.0.2.1")}, want: igmp.ErrMalformed},
		{name: "unrepresentable v2 timer", m: igmp.Message{Version: igmp.V2, Type: igmp.Query, MaxResp: 150 * time.Millisecond}, want: igmp.ErrMalformed},
		{name: "unrepresentable v3 timer", m: igmp.Message{Version: igmp.V3, Type: igmp.Query, MaxResp: 12900 * time.Millisecond}, want: igmp.ErrMalformed},
		{name: "source count overflow", m: igmp.Message{Version: igmp.V3, Type: igmp.Query, Sources: make([]netip.Addr, math.MaxUint16+1)}, want: igmp.ErrMalformed},
		{name: "wire length overflow", m: igmp.Message{Version: igmp.V3, Type: igmp.Query, Sources: make([]netip.Addr, 16381)}, want: igmp.ErrMalformed},
		{
			name: "wrong source family",
			m: igmp.Message{
				Version: igmp.V3,
				Type:    igmp.Query,
				Group:   netip.MustParseAddr("239.1.1.1"),
				Sources: []netip.Addr{netip.MustParseAddr("2001:db8::1")},
			},
			want: igmp.ErrMalformed,
		},
		{name: "record count overflow", m: igmp.Message{Type: igmp.ReportV3, Records: make([]igmp.GroupRecord, math.MaxUint16+1)}, want: igmp.ErrMalformed},
		{name: "unknown message type", m: igmp.Message{Type: 0x30}, want: igmp.ErrUnsupported},
		{name: "unknown query version", m: igmp.Message{Version: 1, Type: igmp.Query}, want: igmp.ErrUnsupported},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "wire length overflow" {
				for i := range tc.m.Sources {
					tc.m.Sources[i] = v4Source
				}
			}
			_, err := igmp.Encode(tc.m)
			if !errors.Is(err, tc.want) {
				t.Errorf("Encode() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func assertMessage(t *testing.T, got, want igmp.Message) {
	t.Helper()

	if got.Version != want.Version || got.Type != want.Type || got.MaxResp != want.MaxResp ||
		got.Group != want.Group || got.Suppress != want.Suppress || got.QRV != want.QRV || got.QQIC != want.QQIC {
		t.Errorf("Decode() = %+v, want %+v", got, want)
	}
	if len(got.Sources) != len(want.Sources) {
		t.Fatalf("len(Decode().Sources) = %d, want %d", len(got.Sources), len(want.Sources))
	}
	for i := range want.Sources {
		if got.Sources[i] != want.Sources[i] {
			t.Errorf("Decode().Sources[%d] = %v, want %v", i, got.Sources[i], want.Sources[i])
		}
	}
	if len(got.Records) != len(want.Records) {
		t.Fatalf("len(Decode().Records) = %d, want %d", len(got.Records), len(want.Records))
	}
	for i := range want.Records {
		if got.Records[i].Type != want.Records[i].Type || got.Records[i].Group != want.Records[i].Group {
			t.Errorf("Decode().Records[%d] = %+v, want %+v", i, got.Records[i], want.Records[i])
		}
		if len(got.Records[i].Sources) != len(want.Records[i].Sources) {
			t.Fatalf("len(Decode().Records[%d].Sources) = %d, want %d", i, len(got.Records[i].Sources), len(want.Records[i].Sources))
		}
		for j := range want.Records[i].Sources {
			if got.Records[i].Sources[j] != want.Records[i].Sources[j] {
				t.Errorf("Decode().Records[%d].Sources[%d] = %v, want %v", i, j, got.Records[i].Sources[j], want.Records[i].Sources[j])
			}
		}
	}
}

func internetChecksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b[:2]))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum > math.MaxUint16 {
		sum = sum&math.MaxUint16 + sum>>16
	}
	return ^uint16(sum)
}
