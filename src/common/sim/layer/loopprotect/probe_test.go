package loopprotect_test

import (
	"bytes"
	"strings"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/sim/layer/loopprotect"
)

// fixtureWire is the probe frame the README gives as the format's example,
// written out octet by octet: destination 03:46:53:4c:50:00, source
// aa:bb:cc:dd:ee:ff, EtherType 0x88b5, version 1, origin 02:11:22:33:44:55,
// VID 0x0141, sequence 0xa1b2c3d4, name length 8, name "et-0/0/7".
var fixtureWire = []byte{
	0x03, 0x46, 0x53, 0x4c, 0x50, 0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x88, 0xb5, 0x01, 0x02,
	0x11, 0x22, 0x33, 0x44, 0x55, 0x01, 0x41, 0xa1, 0xb2, 0xc3, 0xd4, 0x08, 0x65, 0x74, 0x2d, 0x30,
	0x2f, 0x30, 0x2f, 0x37,
}

var (
	fixtureSrc   = netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	fixtureProbe = loopprotect.Probe{
		OriginMAC: netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		VID:       0x0141,
		Sequence:  0xa1b2c3d4,
		Port:      "et-0/0/7",
	}
)

// fixtureFrame builds the fixture's frame from its literal fields, so a
// refusal row changes one of them and nothing else.
func fixtureFrame() ethernet.Frame {
	return ethernet.Frame{
		Dst:       netaddr.MAC{0x03, 0x46, 0x53, 0x4c, 0x50, 0x00},
		Src:       fixtureSrc,
		EtherType: 0x88b5,
		Payload: []byte{
			0x01,
			0x02, 0x11, 0x22, 0x33, 0x44, 0x55,
			0x01, 0x41,
			0xa1, 0xb2, 0xc3, 0xd4,
			0x08,
			'e', 't', '-', '0', '/', '0', '/', '7',
		},
	}
}

func TestEncodeMatchesFixture(t *testing.T) {
	t.Parallel()

	f, err := loopprotect.Encode(fixtureProbe, fixtureSrc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := f.Encode()
	if err != nil {
		t.Fatalf("ethernet.Frame.Encode: %v", err)
	}
	if !bytes.Equal(got, fixtureWire) {
		t.Errorf("serialized frame = % x, want % x", got, fixtureWire)
	}
}

func TestDecodeFixture(t *testing.T) {
	t.Parallel()

	got, err := loopprotect.Decode(fixtureFrame())
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != fixtureProbe {
		t.Errorf("Decode() = %+v, want %+v", got, fixtureProbe)
	}
	if got.VID != vlan.ID(0x0141) {
		t.Errorf("VID = %#x, want 0x0141", uint16(got.VID))
	}
}

func TestDecodeRoundTripsEncode(t *testing.T) {
	t.Parallel()

	f, err := loopprotect.Encode(fixtureProbe, fixtureSrc)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := loopprotect.Decode(f)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != fixtureProbe {
		t.Errorf("Decode(Encode(p)) = %+v, want %+v", got, fixtureProbe)
	}
}

func TestEncodeRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		port string
	}{
		{name: "empty name", port: ""},
		{name: "256 octet name", port: strings.Repeat("x", 256)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := fixtureProbe
			p.Port = tc.port
			if _, err := loopprotect.Encode(p, fixtureSrc); err == nil {
				t.Errorf("Encode(%d-octet name) = nil error, want an error", len(tc.port))
			}
		})
	}

	// The longest name the length octet holds still encodes and decodes.
	p := fixtureProbe
	p.Port = strings.Repeat("x", 255)
	f, err := loopprotect.Encode(p, fixtureSrc)
	if err != nil {
		t.Fatalf("Encode(255-octet name): %v", err)
	}
	got, err := loopprotect.Decode(f)
	if err != nil {
		t.Fatalf("Decode(255-octet name): %v", err)
	}
	if got != p {
		t.Errorf("Decode(Encode(p)) = %+v, want %+v", got, p)
	}
}

// TestDecodeRefusals builds each row from the fixture with one field
// changed, so only the guard the row names can refuse it.
func TestDecodeRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(f *ethernet.Frame)
	}{
		{
			name:   "destination is not the probe group",
			mutate: func(f *ethernet.Frame) { f.Dst = netaddr.MAC{0x03, 0x46, 0x53, 0x4c, 0x50, 0x01} },
		},
		{
			name:   "EtherType is not 0x88b5",
			mutate: func(f *ethernet.Frame) { f.EtherType = 0x0800 },
		},
		{
			name:   "wrong version",
			mutate: func(f *ethernet.Frame) { f.Payload[0] = 2 },
		},
		{
			name:   "payload shorter than 14 octets",
			mutate: func(f *ethernet.Frame) { f.Payload = f.Payload[:13] },
		},
		{
			name: "zero name length",
			mutate: func(f *ethernet.Frame) {
				f.Payload[13] = 0
				f.Payload = f.Payload[:14]
			},
		},
		{
			name:   "name running past the payload",
			mutate: func(f *ethernet.Frame) { f.Payload[13] = 9 },
		},
		{
			name:   "trailing octets after the name",
			mutate: func(f *ethernet.Frame) { f.Payload = append(f.Payload, 0xff) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := fixtureFrame()
			tc.mutate(&f)

			if _, err := loopprotect.Decode(f); err == nil {
				t.Fatalf("Decode(%s) = nil error, want an error", tc.name)
			}
		})
	}
}

func TestDecodeRefusesEnvelopeBeforePayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		frame ethernet.Frame
		attr  string
		want  any
	}{
		{
			name:  "destination",
			frame: ethernet.Frame{Dst: netaddr.MAC{0x03, 0x46, 0x53, 0x4c, 0x50, 0x01}, EtherType: 0x88b5},
			attr:  "dst",
			want:  "03:46:53:4c:50:01",
		},
		{
			name:  "EtherType",
			frame: ethernet.Frame{Dst: loopprotect.GroupAddress(), EtherType: 0x0800},
			attr:  "ether_type",
			want:  uint16(0x0800),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := loopprotect.Decode(tc.frame)
			if err == nil {
				t.Fatal("Decode = nil error, want envelope refusal")
			}
			if got := errs.Attributes(err)[tc.attr]; got != tc.want {
				t.Errorf("refusal attribute %q = %v, want %v", tc.attr, got, tc.want)
			}
		})
	}
}

func TestSetProbeVID(t *testing.T) {
	t.Parallel()

	p := loopprotect.Probe{
		OriginMAC: netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		VID:       0,
		Sequence:  42,
		Port:      "1/1/1",
	}
	f, err := loopprotect.Encode(p, netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	patched := loopprotect.SetProbeVID(f, 100)
	if before, err := loopprotect.Decode(f); err != nil || before.VID != 0 {
		t.Errorf("input frame after SetProbeVID decodes to VID %d, error %v, want VID 0 unchanged", before.VID, err)
	}
	decoded, err := loopprotect.Decode(patched)
	if err != nil {
		t.Fatalf("Decode(patched): %v", err)
	}
	if decoded.VID != 100 {
		t.Errorf("decoded.VID = %d, want 100", decoded.VID)
	}
	if decoded.Sequence != 42 || decoded.Port != "1/1/1" {
		t.Errorf("decoded non-VID fields corrupted: %+v", decoded)
	}

	// Short frame is returned unmodified
	short := ethernet.Frame{Payload: []byte{1, 2, 3}}
	if got := loopprotect.SetProbeVID(short, 100); len(got.Payload) != 3 {
		t.Errorf("short payload modified: len = %d", len(got.Payload))
	}
}
