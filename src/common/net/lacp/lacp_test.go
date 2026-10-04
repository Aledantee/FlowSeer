package lacp_test

import (
	"bytes"
	"errors"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

var capLACPFixture = []byte{
	0x01, 0x80, 0xc2, 0x00, 0x00, 0x02, 0x00, 0x04, 0x96, 0x1f, 0x50, 0x6a, 0x88, 0x09, 0x01, 0x01,
	0x01, 0x14, 0x91, 0xf4, 0x00, 0x04, 0x96, 0x1f, 0x50, 0x6a, 0x80, 0x00, 0x00, 0x00, 0x00, 0x12,
	0x47, 0x00, 0x00, 0x00, 0x02, 0x14, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x3b, 0x00, 0x00, 0x00, 0x03, 0x10, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
	0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
}

var secondLACPFixture = []byte{
	0x01, 0x80, 0xc2, 0x00, 0x00, 0x02, 0x00, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x88, 0x09, 0x01, 0x01,
	0x01, 0x14, 0x12, 0x34, 0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0x11, 0x11, 0x22, 0x22, 0x33, 0x33,
	0x44, 0x01, 0x02, 0x03, 0x02, 0x14, 0xab, 0xcd, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x45, 0x67,
	0x56, 0x78, 0x67, 0x89, 0x9a, 0x0a, 0x0b, 0x0c, 0x03, 0x10, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12,
	0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x04, 0x06, 0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5,
	0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf, 0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5,
	0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf, 0xc0, 0xc1, 0xc2, 0xc3, 0xc4, 0xc5,
	0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xcb, 0xcc, 0xcd, 0xce, 0xcf, 0xd0, 0xd1,
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	actor := lacp.Info{
		SystemPriority: 32768,
		SystemID:       netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a},
		Key:            1,
		PortPriority:   32768,
		PortID:         1,
		State:          lacp.StateActive | lacp.StateShortTimeout | lacp.StateAggregation,
	}
	partner := lacp.Info{
		State: lacp.StateDefaulted,
	}
	pdu := lacp.PDU{
		Actor:             actor,
		Partner:           partner,
		CollectorMaxDelay: 0,
	}
	src := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01}

	frame := lacp.Encode(pdu, src)

	wantDst := netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x02}
	if frame.Dst != wantDst {
		t.Errorf("frame.Dst = %s, want %s", frame.Dst, wantDst)
	}
	if frame.Src != src {
		t.Errorf("frame.Src = %s, want %s", frame.Src, src)
	}
	if frame.EtherType != ethernet.EtherTypeSlowProtocols {
		t.Errorf("frame.EtherType = 0x%04x, want 0x8809", frame.EtherType)
	}
	if len(frame.Tags) != 0 {
		t.Errorf("len(frame.Tags) = %d, want 0", len(frame.Tags))
	}
	if len(frame.Payload) != 110 {
		t.Fatalf("len(frame.Payload) = %d, want 110", len(frame.Payload))
	}

	if frame.Payload[0] != 1 {
		t.Errorf("frame.Payload[0] (subtype) = %d, want 1", frame.Payload[0])
	}
	if frame.Payload[1] != 1 {
		t.Errorf("frame.Payload[1] (version) = %d, want 1", frame.Payload[1])
	}
	if frame.Payload[2] != 1 {
		t.Errorf("frame.Payload[2] (actor TLV type) = %d, want 1", frame.Payload[2])
	}
	if frame.Payload[3] != 20 {
		t.Errorf("frame.Payload[3] (actor TLV length) = %d, want 20", frame.Payload[3])
	}
	if frame.Payload[4] != 0x80 || frame.Payload[5] != 0x00 {
		t.Errorf("frame.Payload[4..5] = 0x%02x%02x, want 0x8000", frame.Payload[4], frame.Payload[5])
	}
	if actorTLV := frame.Payload[2:22]; actorTLV[16] != 0x07 {
		t.Errorf("actor TLV octet 16 (state) = 0x%02x, want 0x07", actorTLV[16])
	}
	if frame.Payload[18] != 0x07 {
		t.Errorf("frame.Payload[18] (state) = 0x%02x, want 0x07", frame.Payload[18])
	}

	decoded, err := lacp.Decode(frame)
	if err != nil {
		t.Fatalf("Decode(frame) failed: %v", err)
	}
	if decoded != pdu {
		t.Errorf("Decode(frame) = %+v, want %+v", decoded, pdu)
	}

	raw, err := frame.Encode()
	if err != nil {
		t.Fatalf("frame.Encode() failed: %v", err)
	}
	ethFrame, err := ethernet.Decode(raw)
	if err != nil {
		t.Fatalf("ethernet.Decode() failed: %v", err)
	}
	if !bytes.Equal(ethFrame.Payload, frame.Payload) {
		t.Fatalf("decoded Ethernet frame payload mismatch:\ngot:  %x\nwant: %x", ethFrame.Payload, frame.Payload)
	}
	ethDecoded, err := lacp.Decode(ethFrame)
	if err != nil {
		t.Fatalf("Decode(ethFrame) failed: %v", err)
	}
	if ethDecoded != pdu {
		t.Errorf("Decode(ethFrame) = %+v, want %+v", ethDecoded, pdu)
	}
}

func TestDecodeCAPFixtureAndEncodeOffsets(t *testing.T) {
	frame := decodeFixture(t, capLACPFixture)
	want := lacp.PDU{
		Actor: lacp.Info{
			SystemPriority: 0x91f4,
			SystemID:       netaddr.MAC{0x00, 0x04, 0x96, 0x1f, 0x50, 0x6a},
			Key:            0x8000,
			PortID:         0x0012,
			State:          0x47,
		},
		Partner:           lacp.Info{State: 0x3b},
		CollectorMaxDelay: 2,
	}

	decoded, err := lacp.Decode(frame)
	if err != nil {
		t.Fatalf("Decode(CAP) failed: %v", err)
	}
	if decoded != want {
		t.Fatalf("Decode(CAP) = %+v, want %+v", decoded, want)
	}

	encoded := lacp.Encode(decoded, frame.Src)
	if !bytes.Equal(encoded.Payload, capLACPFixture[14:]) {
		t.Fatalf("Encode(CAP) payload = %x, want fixture payload %x", encoded.Payload, capLACPFixture[14:])
	}
	assertEncodedFields(t, encoded.Payload, capLACPFixture[14:])
}

func TestDecodeSecondFixtureAndEncodeOffsets(t *testing.T) {
	frame := decodeFixture(t, secondLACPFixture)
	want := lacp.PDU{
		Actor: lacp.Info{
			SystemPriority: 0x1234,
			SystemID:       netaddr.MAC{0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
			Key:            0x1111,
			PortPriority:   0x2222,
			PortID:         0x3333,
			State:          0x44,
		},
		Partner: lacp.Info{
			SystemPriority: 0xabcd,
			SystemID:       netaddr.MAC{0x10, 0x20, 0x30, 0x40, 0x50, 0x60},
			Key:            0x4567,
			PortPriority:   0x5678,
			PortID:         0x6789,
			State:          0x9a,
		},
		CollectorMaxDelay: 0x0d0e,
	}

	decoded, err := lacp.Decode(frame)
	if err != nil {
		t.Fatalf("Decode(second fixture) failed: %v", err)
	}
	if decoded != want {
		t.Fatalf("Decode(second fixture) = %+v, want %+v", decoded, want)
	}

	encoded := lacp.Encode(decoded, frame.Src)
	assertEncodedFields(t, encoded.Payload, secondLACPFixture[14:])
}

func TestDecodeAcceptsVersionTwoAndAdditionalTLV(t *testing.T) {
	wire := bytes.Clone(secondLACPFixture)
	wire[15] = 2
	wire[16] = 0x07
	wire[72] = 0x04
	wire[73] = 0x06

	frame := decodeFixture(t, wire)
	got, err := lacp.Decode(frame)
	if err != nil {
		t.Fatalf("Decode(future LACPDU) failed: %v", err)
	}

	want, err := lacp.Decode(decodeFixture(t, secondLACPFixture))
	if err != nil {
		t.Fatalf("Decode(second fixture) failed: %v", err)
	}
	if got != want {
		t.Fatalf("Decode(future LACPDU) = %+v, want %+v", got, want)
	}
}

func TestDecodeRefusals(t *testing.T) {
	validFrame := decodeFixture(t, capLACPFixture)

	tests := []struct {
		name   string
		modify func(f ethernet.Frame) ethernet.Frame
	}{
		{
			name: "EtherType 0x0800",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.EtherType = ethernet.EtherTypeIPv4
				return f
			},
		},
		{
			name: "109-octet payload",
			modify: func(f ethernet.Frame) ethernet.Frame {
				f.Payload = make([]byte, 109)
				copy(f.Payload, validFrame.Payload)
				return f
			},
		},
		{
			name: "subtype 0x02",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[0] = lacp.SubtypeMarker
				f.Payload = p
				return f
			},
		},
		{
			name: "actor TLV length 19",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[3] = 19
				f.Payload = p
				return f
			},
		},
		{
			name: "partner TLV length 19",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[23] = 19
				f.Payload = p
				return f
			},
		},
		{
			name: "collector TLV length 15",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[43] = 15
				f.Payload = p
				return f
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.modify(validFrame)
			_, err := lacp.Decode(f)
			if err == nil {
				t.Fatalf("Decode() succeeded, want error wrapping ErrUnsupported")
			}
			if !errors.Is(err, lacp.ErrUnsupported) {
				t.Errorf("Decode() error = %v, want errors.Is(err, ErrUnsupported)", err)
			}
		})
	}
}

func decodeFixture(t *testing.T, wire []byte) ethernet.Frame {
	t.Helper()

	frame, err := ethernet.Decode(bytes.Clone(wire))
	if err != nil {
		t.Fatalf("ethernet.Decode(fixture) failed: %v", err)
	}
	return frame
}

func assertEncodedFields(t *testing.T, got, want []byte) {
	t.Helper()

	fields := []struct {
		name   string
		offset int
		length int
	}{
		{name: "actor system priority", offset: 4, length: 2},
		{name: "actor system", offset: 6, length: 6},
		{name: "actor key", offset: 12, length: 2},
		{name: "actor port priority", offset: 14, length: 2},
		{name: "actor port", offset: 16, length: 2},
		{name: "actor state", offset: 18, length: 1},
		{name: "partner system priority", offset: 24, length: 2},
		{name: "partner system", offset: 26, length: 6},
		{name: "partner key", offset: 32, length: 2},
		{name: "partner port priority", offset: 34, length: 2},
		{name: "partner port", offset: 36, length: 2},
		{name: "partner state", offset: 38, length: 1},
		{name: "collector max delay", offset: 44, length: 2},
	}
	for _, field := range fields {
		if !bytes.Equal(got[field.offset:field.offset+field.length], want[field.offset:field.offset+field.length]) {
			t.Errorf("encoded %s at payload[%d:%d] = %x, want fixture %x", field.name, field.offset, field.offset+field.length, got[field.offset:field.offset+field.length], want[field.offset:field.offset+field.length])
		}
	}
}
