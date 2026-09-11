package mld_test

import (
	"encoding/binary"
	"errors"
	"math"
	"net/netip"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ip"
	"go.aledante.io/FlowSeer/src/common/net/mld"
)

func TestV1ReportRoundTrip(t *testing.T) {
	hdr := ipv6Header(58)
	want := mld.Message{Type: mld.ReportV1, Group: netip.MustParseAddr("ff05::1")}

	wire, err := mld.Encode(hdr, want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(wire) != 24 {
		t.Fatalf("len(Encode()) = %d, want 24", len(wire))
	}
	if wire[0] != 131 {
		t.Errorf("Encode()[0] = %d, want 131", wire[0])
	}

	got, err := mld.Decode(hdr, wire)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	assertMessage(t, got, want)
}

func TestQueriesRoundTrip(t *testing.T) {
	hdr := ipv6Header(58)
	tests := []struct {
		name string
		want mld.Message
		size int
	}{
		{name: "v1", want: mld.Message{Version: mld.V1, Type: mld.Query, MaxResp: 10 * time.Second}, size: 24},
		{
			name: "v2 with one source",
			want: mld.Message{
				Version:  mld.V2,
				Type:     mld.Query,
				MaxResp:  32768 * time.Millisecond,
				Group:    netip.MustParseAddr("ff05::1"),
				Sources:  []netip.Addr{netip.MustParseAddr("2001:db8::1")},
				Suppress: true,
				QRV:      7,
				QQIC:     0x81,
			},
			size: 44,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := mld.Encode(hdr, tc.want)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if len(wire) != tc.size {
				t.Fatalf("len(Encode()) = %d, want %d", len(wire), tc.size)
			}

			got, err := mld.Decode(hdr, wire)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			assertMessage(t, got, tc.want)
		})
	}
}

func TestV2TimerCodeBoundaries(t *testing.T) {
	hdr := ipv6Header(58)
	for _, tc := range []struct {
		code uint16
		want time.Duration
	}{
		{code: 32767, want: 32767 * time.Millisecond},
		{code: 32768, want: 32768 * time.Millisecond},
	} {
		m := mld.Message{Version: mld.V2, Type: mld.Query, MaxResp: tc.want}
		wire, err := mld.Encode(hdr, m)
		if err != nil {
			t.Fatalf("Encode(%d) error = %v", tc.code, err)
		}
		if got := binary.BigEndian.Uint16(wire[4:6]); got != tc.code {
			t.Errorf("Encode(%d) code = %d, want %d", tc.code, got, tc.code)
		}

		got, err := mld.Decode(hdr, wire)
		if err != nil {
			t.Fatalf("Decode(%d) error = %v", tc.code, err)
		}
		if got.MaxResp != tc.want {
			t.Errorf("Decode(%d).MaxResp = %v, want %v", tc.code, got.MaxResp, tc.want)
		}
	}
}

func TestV2ReportRoundTrip(t *testing.T) {
	hdr := ipv6Header(58)
	want := mld.Message{
		Type: mld.ReportV2,
		Records: []mld.AddressRecord{
			{Type: mld.ModeIsExclude, Group: netip.MustParseAddr("ff05::1"), Sources: []netip.Addr{netip.MustParseAddr("2001:db8::1")}},
		},
	}

	wire, err := mld.Encode(hdr, want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if len(wire) != 44 {
		t.Fatalf("len(Encode()) = %d, want 44", len(wire))
	}

	got, err := mld.Decode(hdr, wire)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	assertMessage(t, got, want)
}

func TestHopByHopHeader(t *testing.T) {
	want := mld.Message{Type: mld.ReportV1, Group: netip.MustParseAddr("ff05::1")}
	suffix, err := mld.Encode(ipv6Header(58), want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	payload := make([]byte, 16, 16+len(suffix))
	payload[0] = 58
	payload[1] = 1
	payload = append(payload, suffix...)

	got, err := mld.Decode(ipv6Header(0), payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	assertMessage(t, got, want)

	badNextHeader := append([]byte(nil), payload...)
	badNextHeader[0] = 17
	if _, err := mld.Decode(ipv6Header(0), badNextHeader); !errors.Is(err, mld.ErrMalformed) {
		t.Errorf("Decode(non-ICMPv6 suffix) error = %v, want ErrMalformed", err)
	}
	if _, err := mld.Decode(ipv6Header(0), payload[:8]); !errors.Is(err, mld.ErrMalformed) {
		t.Errorf("Decode(short Hop-by-Hop header) error = %v, want ErrMalformed", err)
	}
}

func TestDecodeErrors(t *testing.T) {
	hdr := ipv6Header(58)
	report, err := mld.Encode(hdr, mld.Message{Type: mld.ReportV1, Group: netip.MustParseAddr("ff05::1")})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	badChecksum := append([]byte(nil), report...)
	badChecksum[23] ^= 0xff
	if _, err := mld.Decode(hdr, badChecksum); !errors.Is(err, mld.ErrMalformed) {
		t.Errorf("Decode(bad checksum) error = %v, want ErrMalformed", err)
	}

	unknown := append([]byte(nil), report...)
	unknown[0] = 200
	binary.BigEndian.PutUint16(unknown[2:4], 0)
	binary.BigEndian.PutUint16(unknown[2:4], icmpv6Checksum(hdr, unknown))
	if _, err := mld.Decode(hdr, unknown); !errors.Is(err, mld.ErrUnsupported) {
		t.Errorf("Decode(unknown type) error = %v, want ErrUnsupported", err)
	}

	if _, err := mld.Decode(hdr, report[:23]); !errors.Is(err, mld.ErrMalformed) {
		t.Errorf("Decode(short message) error = %v, want ErrMalformed", err)
	}

	inconsistent, err := mld.Encode(hdr, mld.Message{
		Version: mld.V2,
		Type:    mld.Query,
		Group:   netip.MustParseAddr("ff05::1"),
		Sources: []netip.Addr{netip.MustParseAddr("2001:db8::1")},
	})
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	inconsistent = inconsistent[:28]
	binary.BigEndian.PutUint16(inconsistent[2:4], 0)
	binary.BigEndian.PutUint16(inconsistent[2:4], icmpv6Checksum(hdr, inconsistent))
	if _, err := mld.Decode(hdr, inconsistent); !errors.Is(err, mld.ErrMalformed) {
		t.Errorf("Decode(inconsistent query) error = %v, want ErrMalformed", err)
	}
}

func TestEncodeValidation(t *testing.T) {
	hdr := ipv6Header(58)
	v6Source := netip.MustParseAddr("2001:db8::1")
	tests := []struct {
		name string
		hdr  ip.Header
		m    mld.Message
		want error
	}{
		{
			name: "IPv4 header",
			hdr:  ip.Header{Src: netip.MustParseAddr("192.0.2.1"), Dst: netip.MustParseAddr("224.0.0.1"), Protocol: 58, V4: &ip.V4{}},
			m:    mld.Message{Type: mld.ReportV1, Group: netip.MustParseAddr("ff05::1")},
			want: mld.ErrMalformed,
		},
		{name: "wrong group family", hdr: hdr, m: mld.Message{Type: mld.ReportV1, Group: netip.MustParseAddr("239.1.1.1")}, want: mld.ErrMalformed},
		{name: "unicast group", hdr: hdr, m: mld.Message{Type: mld.ReportV1, Group: netip.MustParseAddr("2001:db8::1")}, want: mld.ErrMalformed},
		{name: "unrepresentable v1 timer", hdr: hdr, m: mld.Message{Version: mld.V1, Type: mld.Query, MaxResp: 1500 * time.Microsecond}, want: mld.ErrMalformed},
		{name: "unrepresentable v2 timer", hdr: hdr, m: mld.Message{Version: mld.V2, Type: mld.Query, MaxResp: 32769 * time.Millisecond}, want: mld.ErrMalformed},
		{name: "source count overflow", hdr: hdr, m: mld.Message{Version: mld.V2, Type: mld.Query, Sources: make([]netip.Addr, math.MaxUint16+1)}, want: mld.ErrMalformed},
		{name: "wire length overflow", hdr: hdr, m: mld.Message{Version: mld.V2, Type: mld.Query, Sources: make([]netip.Addr, 4095)}, want: mld.ErrMalformed},
		{
			name: "wrong source family",
			hdr:  hdr,
			m: mld.Message{
				Version: mld.V2,
				Type:    mld.Query,
				Group:   netip.MustParseAddr("ff05::1"),
				Sources: []netip.Addr{netip.MustParseAddr("192.0.2.1")},
			},
			want: mld.ErrMalformed,
		},
		{name: "record count overflow", hdr: hdr, m: mld.Message{Type: mld.ReportV2, Records: make([]mld.AddressRecord, math.MaxUint16+1)}, want: mld.ErrMalformed},
		{name: "unknown message type", hdr: hdr, m: mld.Message{Type: 200}, want: mld.ErrUnsupported},
		{name: "unknown query version", hdr: hdr, m: mld.Message{Version: 3, Type: mld.Query}, want: mld.ErrUnsupported},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "wire length overflow" {
				for i := range tc.m.Sources {
					tc.m.Sources[i] = v6Source
				}
			}
			_, err := mld.Encode(tc.hdr, tc.m)
			if !errors.Is(err, tc.want) {
				t.Errorf("Encode() error = %v, want %v", err, tc.want)
			}
		})
	}
}

func ipv6Header(protocol uint8) ip.Header {
	return ip.Header{
		Src:      netip.MustParseAddr("fe80::1"),
		Dst:      netip.MustParseAddr("ff02::1"),
		Protocol: protocol,
		V6:       &ip.V6{},
	}
}

func assertMessage(t *testing.T, got, want mld.Message) {
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

func icmpv6Checksum(hdr ip.Header, payload []byte) uint16 {
	pseudo := make([]byte, 40+len(payload))
	src := hdr.Src.As16()
	dst := hdr.Dst.As16()
	copy(pseudo[0:16], src[:])
	copy(pseudo[16:32], dst[:])
	binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(payload)))
	pseudo[39] = 58
	copy(pseudo[40:], payload)
	return internetChecksum(pseudo)
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
