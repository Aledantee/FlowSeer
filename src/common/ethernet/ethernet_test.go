package ethernet_test

import (
	"bytes"
	"testing"

	"go.aledante.io/FlowSeer/src/common/ethernet"
	"go.aledante.io/FlowSeer/src/common/netaddr"
)

func TestCodecRequirement1(t *testing.T) {
	// Requirement 1: decoding 01 00 5e 00 00 fb  00 11 22 33 44 55  81 00  a0 64  08 00
	// plus payload yields one tag with PCP 5, DEI false, VID 100, EtherType 0x0800,
	// and encoding reproduces the bytes.
	prefix := []byte{
		0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb, // Dst MAC
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, // Src MAC
		0x81, 0x00, // Dot1Q C-tag TPID
		0xa0, 0x64, // TCI: PCP 5 (0b101), DEI false (0), VID 100 (0x0064)
		0x08, 0x00, // EtherType IPv4
	}
	payload := []byte("requirement 1 payload")
	raw := append(slicesClone(prefix), payload...)

	frame, err := ethernet.Decode(raw)
	if err != nil {
		t.Fatalf("Decode failed: %v", err)
	}

	wantDst := netaddr.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}
	if frame.Dst != wantDst {
		t.Errorf("frame.Dst = %v, want %v", frame.Dst, wantDst)
	}

	wantSrc := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	if frame.Src != wantSrc {
		t.Errorf("frame.Src = %v, want %v", frame.Src, wantSrc)
	}

	if len(frame.Tags) != 1 {
		t.Fatalf("len(frame.Tags) = %d, want 1", len(frame.Tags))
	}

	tag := frame.Tags[0]
	if tag.TPID != 0x8100 {
		t.Errorf("tag.TPID = 0x%04x, want 0x8100", tag.TPID)
	}
	if tag.PCP != 5 {
		t.Errorf("tag.PCP = %d, want 5", tag.PCP)
	}
	if tag.DEI != false {
		t.Errorf("tag.DEI = %v, want false", tag.DEI)
	}
	if tag.VID != 100 {
		t.Errorf("tag.VID = %d, want 100", tag.VID)
	}

	if frame.EtherType != ethernet.EtherTypeIPv4 {
		t.Errorf("frame.EtherType = 0x%04x, want 0x0800", frame.EtherType)
	}

	if !bytes.Equal(frame.Payload, payload) {
		t.Errorf("frame.Payload = %q, want %q", frame.Payload, payload)
	}

	encoded, err := frame.Encode()
	if err != nil {
		t.Fatalf("frame.Encode() failed: %v", err)
	}

	if !bytes.Equal(encoded, raw) {
		t.Fatalf("encoded bytes mismatch:\ngot:  %x\nwant: %x", encoded, raw)
	}
}

func TestTwoTagStack(t *testing.T) {
	// Two-tag stack (QinQ): outer S-tag (0x88a8), inner C-tag (0x8100).
	// Outer tag: PCP 3, DEI false, VID 200 -> TCI: (3 << 13) | 200 = 0x60c8
	// Inner tag: PCP 5, DEI true, VID 100  -> TCI: (5 << 13) | (1 << 12) | 100 = 0xb064
	raw := []byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, // Dst
		0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, // Src
		0x88, 0xa8, // S-tag TPID
		0x60, 0xc8, // TCI outer
		0x81, 0x00, // C-tag TPID
		0xb0, 0x64, // TCI inner
		0x08, 0x00, // IPv4 EtherType
		0x01, 0x02, 0x03, 0x04, // Payload
	}

	frame, err := ethernet.Decode(raw)
	if err != nil {
		t.Fatalf("Decode two-tag frame failed: %v", err)
	}

	if len(frame.Tags) != 2 {
		t.Fatalf("len(frame.Tags) = %d, want 2", len(frame.Tags))
	}

	outer := frame.Tags[0]
	if outer.TPID != uint16(ethernet.EtherTypeProviderBridging) || outer.PCP != 3 || outer.DEI != false || outer.VID != 200 {
		t.Errorf("outer tag = %+v, want TPID 0x88a8, PCP 3, DEI false, VID 200", outer)
	}

	inner := frame.Tags[1]
	if inner.TPID != uint16(ethernet.EtherTypeDot1Q) || inner.PCP != 5 || inner.DEI != true || inner.VID != 100 {
		t.Errorf("inner tag = %+v, want TPID 0x8100, PCP 5, DEI true, VID 100", inner)
	}

	if frame.EtherType != ethernet.EtherTypeIPv4 {
		t.Errorf("frame.EtherType = 0x%04x, want 0x0800", frame.EtherType)
	}

	encoded, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode failed: %v", err)
	}
	if !bytes.Equal(encoded, raw) {
		t.Fatalf("two-tag round-trip mismatch:\ngot:  %x\nwant: %x", encoded, raw)
	}
}

func TestUntagged(t *testing.T) {
	raw := []byte{
		0xff, 0xff, 0xff, 0xff, 0xff, 0xff, // Dst Broadcast
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55, // Src
		0x08, 0x06, // EtherType ARP
		0x00, 0x01, 0x08, 0x00, // ARP payload
	}

	frame, err := ethernet.Decode(raw)
	if err != nil {
		t.Fatalf("Decode untagged frame failed: %v", err)
	}

	if len(frame.Tags) != 0 {
		t.Fatalf("len(frame.Tags) = %d, want 0", len(frame.Tags))
	}
	if frame.EtherType != ethernet.EtherTypeARP {
		t.Errorf("frame.EtherType = 0x%04x, want 0x0806", frame.EtherType)
	}
	if !bytes.Equal(frame.Payload, raw[14:]) {
		t.Errorf("frame.Payload = %x, want %x", frame.Payload, raw[14:])
	}

	encoded, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode untagged failed: %v", err)
	}
	if !bytes.Equal(encoded, raw) {
		t.Fatalf("untagged round-trip mismatch:\ngot:  %x\nwant: %x", encoded, raw)
	}
}

func TestTruncatedAndShortFrames(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{
			name: "frame shorter than 14 bytes",
			raw:  []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x08},
		},
		{
			name: "tag TPID with no following bytes",
			raw: []byte{
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee,
				0x81, 0x00,
			},
		},
		{
			name: "tag TPID with only 1 byte following",
			raw: []byte{
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee,
				0x81, 0x00,
				0xa0,
			},
		},
		{
			name: "tag TPID with 2 bytes TCI but no subsequent EtherType",
			raw: []byte{
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee,
				0x81, 0x00,
				0xa0, 0x64,
			},
		},
		{
			name: "tag TPID with 2 bytes TCI and only 1 byte of EtherType",
			raw: []byte{
				0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee,
				0x81, 0x00,
				0xa0, 0x64,
				0x08,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ethernet.Decode(tc.raw)
			if err == nil {
				t.Errorf("Decode(%x) succeeded, want error", tc.raw)
			}
		})
	}
}

func TestReservedRangeEdges(t *testing.T) {
	cases := []struct {
		name string
		mac  netaddr.MAC
		want bool
	}{
		{
			name: "lower edge 01:80:c2:00:00:00",
			mac:  netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
			want: true,
		},
		{
			name: "upper edge 01:80:c2:00:00:0f",
			mac:  netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x0f},
			want: true,
		},
		{
			name: "interior reserved 01:80:c2:00:00:03",
			mac:  netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x03},
			want: true,
		},
		{
			name: "first address outside 01:80:c2:00:00:10",
			mac:  netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x10},
			want: false,
		},
		{
			name: "different fourth byte 01:80:c2:01:00:00",
			mac:  netaddr.MAC{0x01, 0x80, 0xc2, 0x01, 0x00, 0x00},
			want: false,
		},
		{
			name: "different first byte 00:80:c2:00:00:00",
			mac:  netaddr.MAC{0x00, 0x80, 0xc2, 0x00, 0x00, 0x00},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ethernet.IsReserved(tc.mac); got != tc.want {
				t.Errorf("IsReserved(%s) = %v, want %v", tc.mac, got, tc.want)
			}
		})
	}
}

func slicesClone(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)

	return out
}
