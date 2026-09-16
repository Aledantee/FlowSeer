package loopprotect_test

import (
	"bytes"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/loopprotect"
)

func TestEncodeOffsets(t *testing.T) {
	t.Parallel()

	origin := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	src := netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	name := "et-0/0/7"

	p := loopprotect.Probe{
		OriginMAC: origin,
		VID:       0x0141,
		Sequence:  0xa1b2c3d4,
		Port:      name,
	}

	f := loopprotect.Encode(p, src)

	if f.Dst != loopprotect.GroupAddress {
		t.Errorf("Dst = %s, want %s", f.Dst, loopprotect.GroupAddress)
	}
	if f.Dst.String() != "03:46:53:4c:50:00" {
		t.Errorf("Dst = %s, want 03:46:53:4c:50:00", f.Dst)
	}
	if f.Src != src {
		t.Errorf("Src = %s, want %s", f.Src, src)
	}
	if f.EtherType != loopprotect.EtherType {
		t.Errorf("EtherType = 0x%04x, want 0x%04x", f.EtherType, loopprotect.EtherType)
	}
	if f.EtherType != 0x88b5 {
		t.Errorf("EtherType = 0x%04x, want 0x88b5", f.EtherType)
	}

	wantLen := 14 + len(name)
	if len(f.Payload) != wantLen {
		t.Fatalf("payload length = %d, want %d", len(f.Payload), wantLen)
	}

	if f.Payload[0] != 1 {
		t.Errorf("payload[0] (version) = %d, want 1", f.Payload[0])
	}

	wantMAC := []byte{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	if got := f.Payload[1:7]; !bytes.Equal(got, wantMAC) {
		t.Errorf("payload[1:7] (origin MAC) = % x, want % x", got, wantMAC)
	}

	wantVID := []byte{0x01, 0x41}
	if got := f.Payload[7:9]; !bytes.Equal(got, wantVID) {
		t.Errorf("payload[7:9] (VID) = % x, want % x", got, wantVID)
	}

	wantSeq := []byte{0xa1, 0xb2, 0xc3, 0xd4}
	if got := f.Payload[9:13]; !bytes.Equal(got, wantSeq) {
		t.Errorf("payload[9:13] (sequence) = % x, want % x", got, wantSeq)
	}

	if f.Payload[13] != byte(len(name)) {
		t.Errorf("payload[13] (name length) = %d, want %d", f.Payload[13], len(name))
	}

	if got := string(f.Payload[14:]); got != name {
		t.Errorf("payload[14:] (name) = %q, want %q", got, name)
	}

	wantPayload := []byte{
		0x01,
		0x02, 0x11, 0x22, 0x33, 0x44, 0x55,
		0x01, 0x41,
		0xa1, 0xb2, 0xc3, 0xd4,
		0x08,
		'e', 't', '-', '0', '/', '0', '/', '7',
	}
	if !bytes.Equal(f.Payload, wantPayload) {
		t.Errorf("payload = % x, want % x", f.Payload, wantPayload)
	}
}

func TestDecodeRoundTripsEncode(t *testing.T) {
	t.Parallel()

	origin := netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55}
	src := netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}

	want := loopprotect.Probe{
		OriginMAC: origin,
		VID:       0x0141,
		Sequence:  0xa1b2c3d4,
		Port:      "et-0/0/7",
	}

	got, err := loopprotect.Decode(loopprotect.Encode(want, src))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got != want {
		t.Errorf("Decode(Encode(p)) = %+v, want %+v", got, want)
	}
}

func TestDecodeRefusals(t *testing.T) {
	t.Parallel()

	validPayload := func() []byte {
		return []byte{
			0x01,
			0x02, 0x11, 0x22, 0x33, 0x44, 0x55,
			0x01, 0x41,
			0xa1, 0xb2, 0xc3, 0xd4,
			0x08,
			'e', 't', '-', '0', '/', '0', '/', '7',
		}
	}

	tests := []struct {
		name    string
		payload []byte
	}{
		{
			name: "wrong version",
			payload: func() []byte {
				p := validPayload()
				p[0] = 2
				return p
			}(),
		},
		{
			name:    "payload shorter than 14 octets",
			payload: validPayload()[:13],
		},
		{
			name: "zero name length",
			payload: []byte{
				0x01,
				0x02, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x01, 0x41,
				0xa1, 0xb2, 0xc3, 0xd4,
				0x00,
			},
		},
		{
			name: "name running past the payload",
			payload: []byte{
				0x01,
				0x02, 0x11, 0x22, 0x33, 0x44, 0x55,
				0x01, 0x41,
				0xa1, 0xb2, 0xc3, 0xd4,
				0x08,
				'e', 't', '-', '0',
			},
		},
		{
			name: "trailing octets after the name",
			payload: func() []byte {
				p := validPayload()
				return append(p, 0xff)
			}(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := ethernet.Frame{
				Dst:       loopprotect.GroupAddress,
				Src:       netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
				EtherType: loopprotect.EtherType,
				Payload:   tc.payload,
			}

			_, err := loopprotect.Decode(f)
			if err == nil {
				t.Fatalf("Decode(%s) = nil error, want an error", tc.name)
			}
		})
	}
}

func TestDecodeFieldsReadBack(t *testing.T) {
	t.Parallel()

	payload := []byte{
		0x01,
		0x02, 0x11, 0x22, 0x33, 0x44, 0x55,
		0x01, 0x41,
		0xa1, 0xb2, 0xc3, 0xd4,
		0x08,
		'e', 't', '-', '0', '/', '0', '/', '7',
	}

	f := ethernet.Frame{
		Dst:       loopprotect.GroupAddress,
		Src:       netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
		EtherType: loopprotect.EtherType,
		Payload:   payload,
	}

	got, err := loopprotect.Decode(f)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	want := loopprotect.Probe{
		OriginMAC: netaddr.MAC{0x02, 0x11, 0x22, 0x33, 0x44, 0x55},
		VID:       vlan.ID(0x0141),
		Sequence:  0xa1b2c3d4,
		Port:      "et-0/0/7",
	}
	if got != want {
		t.Errorf("Decode() = %+v, want %+v", got, want)
	}
}
