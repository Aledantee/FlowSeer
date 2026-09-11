package lacp_test

import (
	"bytes"
	"errors"
	"testing"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/lacp"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
)

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

func TestDecodeRefusals(t *testing.T) {
	validFrame := lacp.Encode(lacp.PDU{
		Actor: lacp.Info{
			SystemPriority: 32768,
			SystemID:       netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0a},
			Key:            1,
			PortPriority:   32768,
			PortID:         1,
			State:          lacp.StateActive | lacp.StateShortTimeout | lacp.StateAggregation,
		},
		Partner: lacp.Info{
			State: lacp.StateDefaulted,
		},
	}, netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01})

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
			name: "subtype 2",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[0] = 2
				f.Payload = p
				return f
			},
		},
		{
			name: "version 2",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[1] = 2
				f.Payload = p
				return f
			},
		},
		{
			name: "actor TLV type 2",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[2] = 2
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
			name: "partner TLV type 1",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[22] = 1
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
			name: "collector TLV type 4",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[42] = 4
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
		{
			name: "terminator TLV type 1",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[58] = 1
				f.Payload = p
				return f
			},
		},
		{
			name: "terminator TLV length 1",
			modify: func(f ethernet.Frame) ethernet.Frame {
				p := bytes.Clone(f.Payload)
				p[59] = 1
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
