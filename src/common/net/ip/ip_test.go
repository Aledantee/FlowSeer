package ip_test

import (
	"bytes"
	"encoding/binary"
	"net/netip"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ip"
)

func TestIPv4DecodeEncode(t *testing.T) {
	t.Parallel()

	// Header bytes:
	// 0x45, 0x00: version 4, IHL 5, TOS 0
	// 0x00, 0x14: total length 20
	// 0x00, 0x00: identification 0
	// 0x00, 0x00: flags 0, fragment offset 0
	// 0x40, 0x11: TTL 64 (0x40), protocol 17 (0x11)
	// 0x48, 0xcc: header checksum (one's complement sum below)
	// 0x0a, 0x00, 0x0a, 0x07: src 10.0.10.7
	// 0x0a, 0x00, 0x14, 0x07: dst 10.0.20.7
	//
	// Hand-summed one's complement (16-bit words):
	//   0x4500 + 0x0014 = 0x4514
	//   0x4514 + 0x0000 = 0x4514
	//   0x4514 + 0x0000 = 0x4514
	//   0x4514 + 0x4011 = 0x8525
	//   0x8525 + 0x0a00 = 0x8f25
	//   0x8f25 + 0x0a07 = 0x992c
	//   0x992c + 0x0a00 = 0xa32c
	//   0xa32c + 0x1407 = 0xb733
	//   ^0xb733 = 0x48cc
	raw := []byte{
		0x45, 0x00, 0x00, 0x14,
		0x00, 0x00, 0x00, 0x00,
		0x40, 0x11, 0x48, 0xcc,
		0x0a, 0x00, 0x0a, 0x07,
		0x0a, 0x00, 0x14, 0x07,
	}

	hdr, payload, err := ip.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	wantSrc := netip.MustParseAddr("10.0.10.7")
	if hdr.Src != wantSrc {
		t.Errorf("hdr.Src = %v, want %v", hdr.Src, wantSrc)
	}

	wantDst := netip.MustParseAddr("10.0.20.7")
	if hdr.Dst != wantDst {
		t.Errorf("hdr.Dst = %v, want %v", hdr.Dst, wantDst)
	}

	if hdr.HopLimit != 64 {
		t.Errorf("hdr.HopLimit = %d, want 64", hdr.HopLimit)
	}
	if hdr.Protocol != 17 {
		t.Errorf("hdr.Protocol = %d, want 17", hdr.Protocol)
	}
	if hdr.TrafficClass != 0 {
		t.Errorf("hdr.TrafficClass = %d, want 0", hdr.TrafficClass)
	}
	if hdr.V4 == nil {
		t.Fatal("hdr.V4 is nil, want non-nil")
	}
	if hdr.V6 != nil {
		t.Errorf("hdr.V6 = %v, want nil", hdr.V6)
	}
	if got := hdr.Version(); got != 4 {
		t.Errorf("hdr.Version() = %d, want 4", got)
	}
	if len(payload) != 0 {
		t.Errorf("len(payload) = %d, want 0", len(payload))
	}

	encoded, err := hdr.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(encoded, raw) {
		t.Errorf("encoded bytes mismatch:\ngot  %x\nwant %x", encoded, raw)
	}
}

func TestIPv4OptionsRoundTrip(t *testing.T) {
	t.Parallel()

	options := []byte{0x94, 0x04, 0x00, 0x00}
	payload := []byte("payload-data")

	hdr := ip.Header{
		Src:          netip.MustParseAddr("192.0.2.1"),
		Dst:          netip.MustParseAddr("198.51.100.1"),
		HopLimit:     128,
		Protocol:     6,
		TrafficClass: 0x10,
		V4: &ip.V4{
			ID:             0x1234,
			Flags:          0x02,
			FragmentOffset: 0,
			Options:        options,
		},
	}

	encoded, err := hdr.Encode(payload)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	wantHeaderLen := ip.V4HeaderLen + len(options)
	if len(encoded) != wantHeaderLen+len(payload) {
		t.Fatalf("len(encoded) = %d, want %d", len(encoded), wantHeaderLen+len(payload))
	}

	decodedHdr, decodedPayload, err := ip.Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if decodedHdr.Src != hdr.Src {
		t.Errorf("Src = %v, want %v", decodedHdr.Src, hdr.Src)
	}
	if decodedHdr.Dst != hdr.Dst {
		t.Errorf("Dst = %v, want %v", decodedHdr.Dst, hdr.Dst)
	}
	if decodedHdr.HopLimit != hdr.HopLimit {
		t.Errorf("HopLimit = %d, want %d", decodedHdr.HopLimit, hdr.HopLimit)
	}
	if decodedHdr.Protocol != hdr.Protocol {
		t.Errorf("Protocol = %d, want %d", decodedHdr.Protocol, hdr.Protocol)
	}
	if decodedHdr.TrafficClass != hdr.TrafficClass {
		t.Errorf("TrafficClass = %d, want %d", decodedHdr.TrafficClass, hdr.TrafficClass)
	}
	if decodedHdr.V4 == nil {
		t.Fatal("decodedHdr.V4 is nil")
	}
	if decodedHdr.V4.ID != hdr.V4.ID {
		t.Errorf("V4.ID = 0x%04x, want 0x%04x", decodedHdr.V4.ID, hdr.V4.ID)
	}
	if decodedHdr.V4.Flags != hdr.V4.Flags {
		t.Errorf("V4.Flags = %d, want %d", decodedHdr.V4.Flags, hdr.V4.Flags)
	}
	if !bytes.Equal(decodedHdr.V4.Options, options) {
		t.Errorf("V4.Options = %x, want %x", decodedHdr.V4.Options, options)
	}
	if !bytes.Equal(decodedPayload, payload) {
		t.Errorf("decodedPayload = %s, want %s", decodedPayload, payload)
	}

	reencoded, err := decodedHdr.Encode(decodedPayload)
	if err != nil {
		t.Fatalf("re-Encode: %v", err)
	}
	if !bytes.Equal(reencoded, encoded) {
		t.Errorf("re-encoded bytes mismatch:\ngot  %x\nwant %x", reencoded, encoded)
	}
}

func TestIPv6DecodeEncode(t *testing.T) {
	t.Parallel()

	raw := make([]byte, ip.V6HeaderLen)
	// Version 6, TrafficClass 0x40, FlowLabel 0x12345.
	firstWord := (uint32(6) << 28) | (uint32(0x40) << 20) | (uint32(0x12345) & 0x000fffff)
	binary.BigEndian.PutUint32(raw[0:4], firstWord)
	binary.BigEndian.PutUint16(raw[4:6], 0)
	raw[6] = 17
	raw[7] = 64

	src := netip.MustParseAddr("2001:db8:10::7")
	dst := netip.MustParseAddr("2001:db8:20::7")
	srcBytes := src.As16()
	dstBytes := dst.As16()
	copy(raw[8:24], srcBytes[:])
	copy(raw[24:40], dstBytes[:])

	hdr, payload, err := ip.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if hdr.Src != src {
		t.Errorf("hdr.Src = %v, want %v", hdr.Src, src)
	}
	if hdr.Dst != dst {
		t.Errorf("hdr.Dst = %v, want %v", hdr.Dst, dst)
	}
	if hdr.HopLimit != 64 {
		t.Errorf("hdr.HopLimit = %d, want 64", hdr.HopLimit)
	}
	if hdr.Protocol != 17 {
		t.Errorf("hdr.Protocol = %d, want 17", hdr.Protocol)
	}
	if hdr.TrafficClass != 0x40 {
		t.Errorf("hdr.TrafficClass = 0x%02x, want 0x40", hdr.TrafficClass)
	}
	if hdr.V6 == nil {
		t.Fatal("hdr.V6 is nil, want non-nil")
	}
	if hdr.V4 != nil {
		t.Errorf("hdr.V4 = %v, want nil", hdr.V4)
	}
	if hdr.V6.FlowLabel != 0x12345 {
		t.Errorf("hdr.V6.FlowLabel = 0x%x, want 0x12345", hdr.V6.FlowLabel)
	}
	if got := hdr.Version(); got != 6 {
		t.Errorf("hdr.Version() = %d, want 6", got)
	}
	if len(payload) != 0 {
		t.Errorf("len(payload) = %d, want 0", len(payload))
	}

	encoded, err := hdr.Encode(nil)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(encoded, raw) {
		t.Errorf("encoded bytes mismatch:\ngot  %x\nwant %x", encoded, raw)
	}
}

func TestRefusals(t *testing.T) {
	t.Parallel()

	validV4Raw := []byte{
		0x45, 0x00, 0x00, 0x14,
		0x00, 0x00, 0x00, 0x00,
		0x40, 0x11, 0x48, 0xcc,
		0x0a, 0x00, 0x0a, 0x07,
		0x0a, 0x00, 0x14, 0x07,
	}

	validV6Raw := make([]byte, ip.V6HeaderLen)
	binary.BigEndian.PutUint32(validV6Raw[0:4], uint32(6)<<28)
	src6 := netip.MustParseAddr("2001:db8:10::7").As16()
	dst6 := netip.MustParseAddr("2001:db8:20::7").As16()
	copy(validV6Raw[8:24], src6[:])
	copy(validV6Raw[24:40], dst6[:])

	tests := []struct {
		name      string
		run       func() error
		wantField string
	}{
		{
			name: "version 5",
			run: func() error {
				raw := append([]byte(nil), validV4Raw...)
				raw[0] = (5 << 4) | (raw[0] & 0x0f)
				_, _, err := ip.Decode(raw)
				return err
			},
			wantField: "version",
		},
		{
			name: "IHL 4",
			run: func() error {
				raw := append([]byte(nil), validV4Raw...)
				raw[0] = (4 << 4) | 4
				_, _, err := ip.Decode(raw)
				return err
			},
			wantField: "ihl",
		},
		{
			name: "IHL past buffer",
			run: func() error {
				raw := append([]byte(nil), validV4Raw...)
				raw[0] = (4 << 4) | 6
				_, _, err := ip.Decode(raw)
				return err
			},
			wantField: "ihl",
		},
		{
			name: "total length past buffer",
			run: func() error {
				raw := append([]byte(nil), validV4Raw...)
				binary.BigEndian.PutUint16(raw[2:4], 30)
				_, _, err := ip.Decode(raw)
				return err
			},
			wantField: "total_length",
		},
		{
			name: "checksum off by one",
			run: func() error {
				raw := append([]byte(nil), validV4Raw...)
				raw[11] ^= 1
				_, _, err := ip.Decode(raw)
				return err
			},
			wantField: "checksum",
		},
		{
			name: "IPv6 payload length past buffer",
			run: func() error {
				raw := append([]byte(nil), validV6Raw...)
				binary.BigEndian.PutUint16(raw[4:6], 10)
				_, _, err := ip.Decode(raw)
				return err
			},
			wantField: "payload_length",
		},
		{
			name: "both parts",
			run: func() error {
				hdr := ip.Header{
					Src: netip.MustParseAddr("10.0.10.7"),
					Dst: netip.MustParseAddr("10.0.20.7"),
					V4:  &ip.V4{},
					V6:  &ip.V6{},
				}
				_, err := hdr.Encode(nil)
				return err
			},
			wantField: "parts",
		},
		{
			name: "neither part",
			run: func() error {
				hdr := ip.Header{
					Src: netip.MustParseAddr("10.0.10.7"),
					Dst: netip.MustParseAddr("10.0.20.7"),
				}
				_, err := hdr.Encode(nil)
				return err
			},
			wantField: "parts",
		},
		{
			name: "part and addresses disagree IPv4 part with IPv6 src",
			run: func() error {
				hdr := ip.Header{
					Src: netip.MustParseAddr("2001:db8::1"),
					Dst: netip.MustParseAddr("10.0.20.7"),
					V4:  &ip.V4{},
				}
				_, err := hdr.Encode(nil)
				return err
			},
			wantField: "address",
		},
		{
			name: "part and addresses disagree IPv4 part with IPv6 dst",
			run: func() error {
				hdr := ip.Header{
					Src: netip.MustParseAddr("10.0.10.7"),
					Dst: netip.MustParseAddr("2001:db8::2"),
					V4:  &ip.V4{},
				}
				_, err := hdr.Encode(nil)
				return err
			},
			wantField: "address",
		},
		{
			name: "part and addresses disagree IPv6 part with IPv4 src",
			run: func() error {
				hdr := ip.Header{
					Src: netip.MustParseAddr("10.0.10.7"),
					Dst: netip.MustParseAddr("2001:db8::2"),
					V6:  &ip.V6{},
				}
				_, err := hdr.Encode(nil)
				return err
			},
			wantField: "address",
		},
		{
			name: "part and addresses disagree IPv6 part with IPv4 dst",
			run: func() error {
				hdr := ip.Header{
					Src: netip.MustParseAddr("2001:db8::1"),
					Dst: netip.MustParseAddr("10.0.20.7"),
					V6:  &ip.V6{},
				}
				_, err := hdr.Encode(nil)
				return err
			},
			wantField: "address",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.run()
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			attrs := errs.Attributes(err)
			gotField, ok := attrs["field"]
			if !ok {
				t.Fatalf("error attributes missing field attribute: %v", attrs)
			}
			if gotField != tc.wantField {
				t.Errorf("field attribute = %q, want %q", gotField, tc.wantField)
			}
		})
	}
}

func TestPayloadBounding(t *testing.T) {
	t.Parallel()

	t.Run("IPv4 length bounding", func(t *testing.T) {
		t.Parallel()

		raw := []byte{
			0x45, 0x00, 0x00, 0x19, // total length 25 (20 header + 5 payload)
			0x00, 0x00, 0x00, 0x00,
			0x40, 0x11, 0x00, 0x00, // checksum zeroed for computation
			0x0a, 0x00, 0x0a, 0x07,
			0x0a, 0x00, 0x14, 0x07,
		}
		// Compute valid checksum.
		// 0x4500 + 0x0019 + 0x4011 + 0x0a00 + 0x0a07 + 0x0a00 + 0x1407 = 0xb738.
		// ^0xb738 = 0x48c7.
		binary.BigEndian.PutUint16(raw[10:12], 0x48c7)

		payload := []byte("hello")
		trailingGarbage := []byte("extra-trailing-bytes")
		buf := make([]byte, 0, len(raw)+len(payload)+len(trailingGarbage))
		buf = append(buf, raw...)
		buf = append(buf, payload...)
		buf = append(buf, trailingGarbage...)

		_, gotPayload, err := ip.Decode(buf)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !bytes.Equal(gotPayload, payload) {
			t.Errorf("gotPayload = %q, want %q", gotPayload, payload)
		}
	})

	t.Run("IPv6 length bounding", func(t *testing.T) {
		t.Parallel()

		raw := make([]byte, ip.V6HeaderLen)
		binary.BigEndian.PutUint32(raw[0:4], uint32(6)<<28)
		binary.BigEndian.PutUint16(raw[4:6], 5) // payload length 5
		src := netip.MustParseAddr("2001:db8:10::7").As16()
		dst := netip.MustParseAddr("2001:db8:20::7").As16()
		copy(raw[8:24], src[:])
		copy(raw[24:40], dst[:])

		payload := []byte("world")
		trailingGarbage := []byte("extra-trailing-bytes")
		buf := make([]byte, 0, len(raw)+len(payload)+len(trailingGarbage))
		buf = append(buf, raw...)
		buf = append(buf, payload...)
		buf = append(buf, trailingGarbage...)

		_, gotPayload, err := ip.Decode(buf)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !bytes.Equal(gotPayload, payload) {
			t.Errorf("gotPayload = %q, want %q", gotPayload, payload)
		}
	})
}

func TestVersionMethod(t *testing.T) {
	t.Parallel()

	if got := (ip.Header{}).Version(); got != 0 {
		t.Errorf("empty Header.Version() = %d, want 0", got)
	}
	if got := (ip.Header{V4: &ip.V4{}, V6: &ip.V6{}}).Version(); got != 0 {
		t.Errorf("both parts Header.Version() = %d, want 0", got)
	}
}

func TestIPv4OptionsValidation(t *testing.T) {
	t.Parallel()

	hdr := ip.Header{
		Src: netip.MustParseAddr("10.0.10.7"),
		Dst: netip.MustParseAddr("10.0.20.7"),
		V4: &ip.V4{
			Options: []byte{1, 2, 3}, // not a multiple of 4
		},
	}
	_, err := hdr.Encode(nil)
	if err == nil {
		t.Fatal("expected error for non-multiple of 4 options, got nil")
	}
	if attrs := errs.Attributes(err); attrs["field"] != "options" {
		t.Errorf("field attribute = %v, want options", attrs["field"])
	}

	hdr.V4.Options = make([]byte, 44) // exceeds 40 octets
	_, err = hdr.Encode(nil)
	if err == nil {
		t.Fatal("expected error for >40 bytes options, got nil")
	}
	if attrs := errs.Attributes(err); attrs["field"] != "options" {
		t.Errorf("field attribute = %v, want options", attrs["field"])
	}
}
