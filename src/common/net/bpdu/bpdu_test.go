package bpdu_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/bpdu"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/net/vlan"
)

const (
	defaultHelloTime    = 2 * time.Second
	defaultMaxAge       = 20 * time.Second
	defaultForwardDelay = 15 * time.Second
)

func mustEncode(t *testing.T, b bpdu.BPDU, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := bpdu.Encode(b, src)
	if err != nil {
		t.Fatalf("bpdu.Encode: %v", err)
	}
	return frame
}

func TestBPDUCodecRoundTrip(t *testing.T) {
	t.Parallel()

	mac, err := netaddr.Parse("00:11:22:33:44:55")
	if err != nil {
		t.Fatalf("Parse MAC: %v", err)
	}

	bridgeID := bpdu.BridgeID{
		Priority: 4096,
		Address:  mac,
	}

	b := bpdu.BPDU{
		RootID:       bridgeID,
		RootPathCost: 0,
		BridgeID:     bridgeID,
		PortID:       (128 << 8) | 1,
		MessageAge:   0,
		MaxAge:       defaultMaxAge,
		HelloTime:    defaultHelloTime,
		ForwardDelay: defaultForwardDelay,
	}
	b.SetRole(bpdu.RoleDesignated)
	b.SetProposal(true)

	frame := mustEncode(t, b, mac)
	if len(frame.Payload) != 46 {
		t.Fatalf("payload len = %d, want 46", len(frame.Payload))
	}
	if frame.EtherType != ethernet.EtherType(39) {
		t.Errorf("EtherType = %v, want 39", frame.EtherType)
	}

	wire, err := frame.Encode()
	if err != nil {
		t.Fatalf("frame.Encode: %v", err)
	}
	if len(wire) != 60 {
		t.Fatalf("wire length = %d, want 60", len(wire))
	}

	decodedFrame, err := ethernet.Decode(wire)
	if err != nil {
		t.Fatalf("ethernet.Decode: %v", err)
	}

	decodedBPDU, err := bpdu.Decode(decodedFrame)
	if err != nil {
		t.Fatalf("bpdu.Decode: %v", err)
	}

	if decodedBPDU.RootID != b.RootID {
		t.Errorf("RootID = %v, want %v", decodedBPDU.RootID, b.RootID)
	}
	if decodedBPDU.RootPathCost != 0 {
		t.Errorf("RootPathCost = %d, want 0", decodedBPDU.RootPathCost)
	}
	if decodedBPDU.BridgeID != b.BridgeID {
		t.Errorf("BridgeID = %v, want %v", decodedBPDU.BridgeID, b.BridgeID)
	}
	if decodedBPDU.PortID != b.PortID {
		t.Errorf("PortID = 0x%04x, want 0x%04x", decodedBPDU.PortID, b.PortID)
	}
	if decodedBPDU.MessageAge != 0 {
		t.Errorf("MessageAge = %v, want 0", decodedBPDU.MessageAge)
	}
	if decodedBPDU.MaxAge != 20*time.Second {
		t.Errorf("MaxAge = %v, want 20s", decodedBPDU.MaxAge)
	}
	if decodedBPDU.HelloTime != 2*time.Second {
		t.Errorf("HelloTime = %v, want 2s", decodedBPDU.HelloTime)
	}
	if decodedBPDU.ForwardDelay != 15*time.Second {
		t.Errorf("ForwardDelay = %v, want 15s", decodedBPDU.ForwardDelay)
	}
	if !decodedBPDU.Proposal() {
		t.Error("Proposal() = false, want true")
	}
	if decodedBPDU.Role() != bpdu.RoleDesignated {
		t.Errorf("Role() = %v, want %v", decodedBPDU.Role(), bpdu.RoleDesignated)
	}

	// Re-encode and verify identical wire bytes.
	reEncodedFrame := mustEncode(t, decodedBPDU, mac)
	reEncodedWire, err := reEncodedFrame.Encode()
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	if string(reEncodedWire) != string(wire) {
		t.Error("re-encoded wire bytes do not match original")
	}
}

func TestBPDUDecodeRefusals(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	validBPDU := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	validFrame := mustEncode(t, validBPDU, mac)

	tests := []struct {
		name      string
		modify    func(f *ethernet.Frame)
		wantField string
	}{
		{
			name: "payload too short",
			modify: func(f *ethernet.Frame) {
				f.Payload = f.Payload[:38]
			},
			wantField: "length",
		},
		{
			name: "wrong LLC DSAP",
			modify: func(f *ethernet.Frame) {
				f.Payload[0] = 0x00
			},
			wantField: "DSAP",
		},
		{
			name: "wrong LLC SSAP",
			modify: func(f *ethernet.Frame) {
				f.Payload[1] = 0xaa
			},
			wantField: "SSAP",
		},
		{
			name: "wrong LLC control",
			modify: func(f *ethernet.Frame) {
				f.Payload[2] = 0x00
			},
			wantField: "control",
		},
		{
			name: "wrong protocol ID",
			modify: func(f *ethernet.Frame) {
				binary.BigEndian.PutUint16(f.Payload[3:5], 0x0001)
			},
			wantField: "protocol identifier",
		},
		{
			name: "version 0 refused",
			modify: func(f *ethernet.Frame) {
				f.Payload[5] = 0
			},
			wantField: "version",
		},
		{
			name: "version 1 refused",
			modify: func(f *ethernet.Frame) {
				f.Payload[5] = 1
			},
			wantField: "version",
		},
		{
			name: "type 1 refused",
			modify: func(f *ethernet.Frame) {
				f.Payload[6] = 1
			},
			wantField: "type",
		},
		{
			name: "type 3 refused",
			modify: func(f *ethernet.Frame) {
				f.Payload[6] = 3
			},
			wantField: "type",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := ethernet.Frame{
				Dst:       validFrame.Dst,
				Src:       validFrame.Src,
				EtherType: validFrame.EtherType,
				Payload:   append([]byte(nil), validFrame.Payload...),
			}
			tc.modify(&f)

			_, err := bpdu.Decode(f)
			if err == nil {
				t.Fatal("Decode unexpectedly succeeded")
			}

			if !errors.Is(err, bpdu.ErrUnsupported) {
				t.Errorf("Decode error = %v, want ErrUnsupported", err)
			}

			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantField)) {
				t.Errorf("error %q does not name field %q", err.Error(), tc.wantField)
			}
		})
	}
}

func TestBPDUFlagBits(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	t.Run("topology change", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{HelloTime: defaultHelloTime}
		b.SetTopologyChange(true)
		if !b.TopologyChange() {
			t.Error("TopologyChange() = false, want true")
		}
		if b.Flags&0x01 == 0 {
			t.Errorf("bit 0 not set: Flags=0x%02x", b.Flags)
		}
		f := mustEncode(t, b, mac)
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !dec.TopologyChange() {
			t.Error("decoded TopologyChange() = false, want true")
		}

		b.SetTopologyChange(false)
		if b.TopologyChange() {
			t.Error("TopologyChange() = true after clear, want false")
		}
	})

	t.Run("proposal", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{HelloTime: defaultHelloTime}
		b.SetProposal(true)
		if !b.Proposal() {
			t.Error("Proposal() = false, want true")
		}
		if b.Flags&0x02 == 0 {
			t.Errorf("bit 1 not set: Flags=0x%02x", b.Flags)
		}
		f := mustEncode(t, b, mac)
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !dec.Proposal() {
			t.Error("decoded Proposal() = false, want true")
		}

		b.SetProposal(false)
		if b.Proposal() {
			t.Error("Proposal() = true after clear, want false")
		}
	})

	t.Run("roles", func(t *testing.T) {
		t.Parallel()
		cases := []struct {
			role     bpdu.Role
			wantBits uint8
		}{
			{role: bpdu.RoleRoot, wantBits: 2 << 2},
			{role: bpdu.RoleDesignated, wantBits: 3 << 2},
			{role: bpdu.RoleAlternate, wantBits: 1 << 2},
			{role: bpdu.RoleBackup, wantBits: 1 << 2},
			{role: bpdu.RoleDisabled, wantBits: 0},
		}

		for _, c := range cases {
			b := bpdu.BPDU{HelloTime: defaultHelloTime}
			b.SetRole(c.role)
			if (b.Flags & (3 << 2)) != c.wantBits {
				t.Errorf("role %v: Flags bits 2-3 = 0x%02x, want 0x%02x", c.role, b.Flags&(3<<2), c.wantBits)
			}
			f := mustEncode(t, b, mac)
			dec, err := bpdu.Decode(f)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			expectedDecodedRole := c.role
			if c.role == bpdu.RoleBackup {
				expectedDecodedRole = bpdu.RoleAlternate
			}
			if dec.Role() != expectedDecodedRole {
				t.Errorf("decoded Role() = %v, want %v", dec.Role(), expectedDecodedRole)
			}
		}
	})

	t.Run("learning", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{HelloTime: defaultHelloTime}
		b.SetLearning(true)
		if !b.Learning() {
			t.Error("Learning() = false, want true")
		}
		if b.Flags&0x10 == 0 {
			t.Errorf("bit 4 not set: Flags=0x%02x", b.Flags)
		}
		f := mustEncode(t, b, mac)
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !dec.Learning() {
			t.Error("decoded Learning() = false, want true")
		}

		b.SetLearning(false)
		if b.Learning() {
			t.Error("Learning() = true after clear, want false")
		}
	})

	t.Run("forwarding", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{HelloTime: defaultHelloTime}
		b.SetForwarding(true)
		if !b.Forwarding() {
			t.Error("Forwarding() = false, want true")
		}
		if b.Flags&0x20 == 0 {
			t.Errorf("bit 5 not set: Flags=0x%02x", b.Flags)
		}
		f := mustEncode(t, b, mac)
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !dec.Forwarding() {
			t.Error("decoded Forwarding() = false, want true")
		}

		b.SetForwarding(false)
		if b.Forwarding() {
			t.Error("Forwarding() = true after clear, want false")
		}
	})

	t.Run("agreement", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{HelloTime: defaultHelloTime}
		b.SetAgreement(true)
		if !b.Agreement() {
			t.Error("Agreement() = false, want true")
		}
		if b.Flags&0x40 == 0 {
			t.Errorf("bit 6 not set: Flags=0x%02x", b.Flags)
		}
		f := mustEncode(t, b, mac)
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !dec.Agreement() {
			t.Error("decoded Agreement() = false, want true")
		}

		b.SetAgreement(false)
		if b.Agreement() {
			t.Error("Agreement() = true after clear, want false")
		}
	})

	t.Run("topology change ack", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{Type: bpdu.TypeConfiguration, HelloTime: defaultHelloTime}
		b.SetTopologyChangeAck(true)
		if !b.TopologyChangeAck() {
			t.Error("TopologyChangeAck() = false, want true")
		}
		if b.Flags&0x80 == 0 {
			t.Errorf("bit 7 not set: Flags=0x%02x", b.Flags)
		}
		f := mustEncode(t, b, mac)
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !dec.TopologyChangeAck() {
			t.Error("decoded TopologyChangeAck() = false, want true")
		}

		b.SetTopologyChangeAck(false)
		if b.TopologyChangeAck() {
			t.Error("TopologyChangeAck() = true after clear, want false")
		}
	})
}

func TestRapidAndMSTIgnoreTopologyChangeAck(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	t.Run("IEEE RST", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{Type: bpdu.TypeRapid, Flags: 0xff, HelloTime: defaultHelloTime}
		frame := mustEncode(t, b, mac)
		if frame.Payload[7] != 0x7f {
			t.Errorf("encoded flags = 0x%02x, want 0x7f", frame.Payload[7])
		}
		frame.Payload[7] = 0xff
		decoded, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if decoded.Flags != 0x7f || decoded.TopologyChangeAck() {
			t.Errorf("decoded flags = 0x%02x, want 0x7f", decoded.Flags)
		}
	})

	t.Run("IEEE MST", func(t *testing.T) {
		t.Parallel()
		frame := ethernet.Frame{
			Dst:       netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
			Src:       mac,
			EtherType: ethernet.EtherType(len(mstBPDUWireFixture()) - 3),
			Payload:   append([]byte(nil), mstBPDUWireFixture()...),
		}
		frame.Payload[7] = 0xff
		decoded, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if decoded.ConfigID == nil || decoded.Flags != 0x7f || decoded.TopologyChangeAck() {
			t.Errorf("decoded MST = %+v, want MST with flags 0x7f", decoded)
		}
		decoded.Flags = 0xff
		encoded := mustEncode(t, decoded, mac)
		if encoded.Payload[7] != 0x7f {
			t.Errorf("encoded flags = 0x%02x, want 0x7f", encoded.Payload[7])
		}
	})

	t.Run("SSTP RST", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{Type: bpdu.TypeRapid, Flags: 0xff, HelloTime: defaultHelloTime}
		frame := mustEncodeSSTP(t, b, 10, mac)
		if frame.Payload[12] != 0x7f {
			t.Errorf("encoded flags = 0x%02x, want 0x7f", frame.Payload[12])
		}
		frame.Payload[12] = 0xff
		decoded, _, err := bpdu.DecodeSSTP(frame)
		if err != nil {
			t.Fatalf("DecodeSSTP: %v", err)
		}
		if decoded.Flags != 0x7f || decoded.TopologyChangeAck() {
			t.Errorf("decoded flags = 0x%02x, want 0x7f", decoded.Flags)
		}
	})
}

// TestDecodeAcceptsZeroHelloTime guards IEEE 802.1Q-2003 clause 14.4 compliance:
// a BPDU whose hello time is zero is accepted on decode.
func TestDecodeAcceptsZeroHelloTime(t *testing.T) {
	t.Parallel()

	root := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 1}}
	frame := mustEncode(t, bpdu.BPDU{RootID: root, BridgeID: root, MaxAge: 20 * time.Second}, root.Address)
	b, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("Decode rejected a BPDU with hello time 0: %v", err)
	}
	if b.HelloTime != 0 {
		t.Errorf("HelloTime = %v, want 0", b.HelloTime)
	}
}

func TestDecodeConfigurationBPDU(t *testing.T) {
	t.Parallel()

	payload := make([]byte, 46)
	payload[0] = 0x42
	payload[1] = 0x42
	payload[2] = 0x03
	payload[5] = 0
	payload[6] = 0
	payload[7] = 0x01
	binary.BigEndian.PutUint16(payload[8:10], 61440)
	copy(payload[10:16], []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c})
	binary.BigEndian.PutUint32(payload[16:20], 0)
	binary.BigEndian.PutUint16(payload[20:22], 61440)
	copy(payload[22:28], []byte{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c})
	binary.BigEndian.PutUint16(payload[28:30], 0x8001)
	binary.BigEndian.PutUint16(payload[30:32], 0)
	binary.BigEndian.PutUint16(payload[32:34], uint16((20*time.Second*256)/time.Second))
	binary.BigEndian.PutUint16(payload[34:36], uint16((2*time.Second*256)/time.Second))
	binary.BigEndian.PutUint16(payload[36:38], uint16((15*time.Second*256)/time.Second))

	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
		Src:       netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c},
		EtherType: ethernet.EtherType(38),
		Payload:   payload,
	}

	decoded, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("bpdu.Decode: %v", err)
	}

	if decoded.Version != 0 {
		t.Errorf("Version = %d, want 0", decoded.Version)
	}
	if decoded.Type != bpdu.TypeConfiguration {
		t.Errorf("Type = %v, want %v", decoded.Type, bpdu.TypeConfiguration)
	}
	if !decoded.TopologyChange() {
		t.Error("TopologyChange() = false, want true")
	}
	if decoded.Role() != bpdu.RoleDesignated {
		t.Errorf("Role() = %v, want %v", decoded.Role(), bpdu.RoleDesignated)
	}
	if decoded.Proposal() {
		t.Error("Proposal() = true, want false")
	}
}

func TestDecodeTCNBPDU(t *testing.T) {
	t.Parallel()

	// 4-octet TCN body after 3-octet LLC header.
	payload := []byte{0x42, 0x42, 0x03, 0x00, 0x00, 0x00, 0x80}
	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
		Src:       netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x01},
		EtherType: ethernet.EtherType(7),
		Payload:   payload,
	}

	decoded, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("bpdu.Decode: %v", err)
	}

	if decoded.Type != bpdu.TypeTopologyChangeNotification {
		t.Errorf("Type = %v, want %v", decoded.Type, bpdu.TypeTopologyChangeNotification)
	}
	if decoded.Version != 0 {
		t.Errorf("Version = %d, want 0", decoded.Version)
	}
}

func TestEncodeConfigurationBPDURoundTrip(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c}
	bridgeID := bpdu.BridgeID{Priority: 61440, Address: mac}

	b := bpdu.BPDU{
		Type:         bpdu.TypeConfiguration,
		RootID:       bridgeID,
		RootPathCost: 100,
		BridgeID:     bridgeID,
		PortID:       0x8001,
		MessageAge:   1 * time.Second,
		MaxAge:       20 * time.Second,
		HelloTime:    2 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	b.SetProposal(true)
	b.SetRole(bpdu.RoleRoot)

	frame := mustEncode(t, b, mac)
	if frame.EtherType != ethernet.EtherType(38) {
		t.Errorf("EtherType = %d, want 38", frame.EtherType)
	}
	if len(frame.Payload) != 46 {
		t.Fatalf("payload len = %d, want 46", len(frame.Payload))
	}
	if frame.Payload[7] != 0x00 {
		t.Errorf("flags octet = 0x%02x, want 0x00", frame.Payload[7])
	}

	decoded, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("bpdu.Decode: %v", err)
	}

	if decoded.Type != bpdu.TypeConfiguration {
		t.Errorf("Type = %v, want %v", decoded.Type, bpdu.TypeConfiguration)
	}
	if decoded.RootID != b.RootID {
		t.Errorf("RootID = %v, want %v", decoded.RootID, b.RootID)
	}
	if decoded.BridgeID != b.BridgeID {
		t.Errorf("BridgeID = %v, want %v", decoded.BridgeID, b.BridgeID)
	}
	if decoded.PortID != b.PortID {
		t.Errorf("PortID = 0x%04x, want 0x%04x", decoded.PortID, b.PortID)
	}
	if decoded.RootPathCost != b.RootPathCost {
		t.Errorf("RootPathCost = %d, want %d", decoded.RootPathCost, b.RootPathCost)
	}
	if decoded.MessageAge != b.MessageAge {
		t.Errorf("MessageAge = %v, want %v", decoded.MessageAge, b.MessageAge)
	}
	if decoded.MaxAge != b.MaxAge {
		t.Errorf("MaxAge = %v, want %v", decoded.MaxAge, b.MaxAge)
	}
	if decoded.HelloTime != b.HelloTime {
		t.Errorf("HelloTime = %v, want %v", decoded.HelloTime, b.HelloTime)
	}
	if decoded.ForwardDelay != b.ForwardDelay {
		t.Errorf("ForwardDelay = %v, want %v", decoded.ForwardDelay, b.ForwardDelay)
	}
	if decoded.Role() != bpdu.RoleDesignated {
		t.Errorf("Role() = %v, want %v", decoded.Role(), bpdu.RoleDesignated)
	}
	if decoded.Proposal() {
		t.Error("Proposal() = true, want false")
	}
}

func TestEncodeEmptyBPDURapid(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	frame := mustEncode(t, bpdu.BPDU{}, mac)

	if frame.EtherType != ethernet.EtherType(39) {
		t.Errorf("EtherType = %d, want 39", frame.EtherType)
	}
	if len(frame.Payload) != 46 {
		t.Fatalf("payload len = %d, want 46", len(frame.Payload))
	}
	if frame.Payload[0] != 0x42 || frame.Payload[1] != 0x42 || frame.Payload[2] != 0x03 {
		t.Errorf("LLC header = %x %x %x, want 42 42 03", frame.Payload[0], frame.Payload[1], frame.Payload[2])
	}
	if frame.Payload[5] != 2 {
		t.Errorf("version = %d, want 2", frame.Payload[5])
	}
	if frame.Payload[6] != 2 {
		t.Errorf("type = %d, want 2", frame.Payload[6])
	}

	expectedPayload := make([]byte, 46)
	expectedPayload[0] = 0x42
	expectedPayload[1] = 0x42
	expectedPayload[2] = 0x03
	expectedPayload[5] = 2
	expectedPayload[6] = 2
	if !bytes.Equal(frame.Payload, expectedPayload) {
		t.Errorf("payload = %x, want %x", frame.Payload, expectedPayload)
	}

	frameWithHello := mustEncode(t, bpdu.BPDU{HelloTime: 2 * time.Second}, mac)
	dec, err := bpdu.Decode(frameWithHello)
	if err != nil {
		t.Fatalf("bpdu.Decode: %v", err)
	}
	if dec.Version != 2 {
		t.Errorf("Version = %d, want 2", dec.Version)
	}
	if dec.Type != bpdu.TypeRapid {
		t.Errorf("Type = %v, want %v", dec.Type, bpdu.TypeRapid)
	}
}

func TestBPDUVersionAndTypeCombinations(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	valid := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}

	t.Run("version 2 type 0 accepted as Configuration BPDU", func(t *testing.T) {
		t.Parallel()
		f := mustEncode(t, valid, mac)
		f.Payload[5] = 2
		f.Payload[6] = 0
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.Type != bpdu.TypeConfiguration {
			t.Errorf("Type = %v, want TypeConfiguration", dec.Type)
		}
	})

	t.Run("version 0 type 2 refused", func(t *testing.T) {
		t.Parallel()
		f := mustEncode(t, valid, mac)
		f.Payload[5] = 0
		f.Payload[6] = 2
		_, err := bpdu.Decode(f)
		if err == nil {
			t.Fatal("Decode unexpectedly succeeded for version 0 type 2")
		}
		if !errors.Is(err, bpdu.ErrUnsupported) {
			t.Errorf("error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("version 3 RST body decodes with Version 3", func(t *testing.T) {
		t.Parallel()
		f := mustEncode(t, valid, mac)
		f.Payload[5] = 3
		f.Payload[6] = 2
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.Version != 3 {
			t.Errorf("Version = %d, want 3", dec.Version)
		}
		if dec.Type != bpdu.TypeRapid {
			t.Errorf("Type = %v, want %v", dec.Type, bpdu.TypeRapid)
		}
	})
}

func TestMSTBPDUCodecRoundTrip(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	regionalRoot := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c}
	root := bpdu.BridgeID{Priority: 4096, Address: mac}

	configID := &bpdu.ConfigID{
		Selector: 0,
		Name:     "region-a",
		Revision: 3,
		Digest:   [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
	}

	tests := []struct {
		name  string
		mstis []bpdu.MSTIRecord
	}{
		{name: "zero records", mstis: nil},
		{
			name: "one record",
			mstis: []bpdu.MSTIRecord{
				{
					MSTID:                1,
					Flags:                0x01,
					RegionalRootID:       bpdu.BridgeID{Priority: 8192 + 1, Address: regionalRoot},
					InternalRootPathCost: 100,
					BridgePriority:       0x20,
					PortPriority:         0x80,
					RemainingHops:        19,
				},
			},
		},
		{
			name: "two records",
			mstis: []bpdu.MSTIRecord{
				{
					MSTID:                1,
					Flags:                0x01,
					RegionalRootID:       bpdu.BridgeID{Priority: 8192 + 1, Address: regionalRoot},
					InternalRootPathCost: 100,
					BridgePriority:       0x20,
					PortPriority:         0x80,
					RemainingHops:        19,
				},
				{
					MSTID:                2,
					Flags:                0x02,
					RegionalRootID:       bpdu.BridgeID{Priority: 4096 + 2, Address: mac},
					InternalRootPathCost: 200,
					BridgePriority:       0x40,
					PortPriority:         0x90,
					RemainingHops:        18,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			b := bpdu.BPDU{
				RootID:               root,
				BridgeID:             root,
				PortID:               0x8001,
				HelloTime:            2 * time.Second,
				MaxAge:               20 * time.Second,
				ForwardDelay:         15 * time.Second,
				ConfigID:             configID,
				RegionalRootID:       bpdu.BridgeID{Priority: 32768, Address: regionalRoot},
				InternalRootPathCost: 50,
				RemainingHops:        20,
				MSTIs:                tc.mstis,
			}

			frame := mustEncode(t, b, mac)

			wantPayloadLen := 3 + 102 + 16*len(tc.mstis)
			if len(frame.Payload) != wantPayloadLen {
				t.Fatalf("payload len = %d, want %d", len(frame.Payload), wantPayloadLen)
			}
			if frame.EtherType != ethernet.EtherType(wantPayloadLen) {
				t.Errorf("EtherType = %v, want %d", frame.EtherType, wantPayloadLen)
			}

			decoded, err := bpdu.Decode(frame)
			if err != nil {
				t.Fatalf("bpdu.Decode: %v", err)
			}

			if decoded.Version != 3 {
				t.Errorf("Version = %d, want 3", decoded.Version)
			}
			if decoded.Type != bpdu.TypeRapid {
				t.Errorf("Type = %v, want %v", decoded.Type, bpdu.TypeRapid)
			}
			if decoded.ConfigID == nil {
				t.Fatal("ConfigID = nil, want non-nil")
			}
			if *decoded.ConfigID != *configID {
				t.Errorf("ConfigID = %+v, want %+v", *decoded.ConfigID, *configID)
			}
			if decoded.RegionalRootID != b.RegionalRootID {
				t.Errorf("RegionalRootID = %v, want %v", decoded.RegionalRootID, b.RegionalRootID)
			}
			if decoded.InternalRootPathCost != b.InternalRootPathCost {
				t.Errorf("InternalRootPathCost = %d, want %d", decoded.InternalRootPathCost, b.InternalRootPathCost)
			}
			if decoded.RemainingHops != b.RemainingHops {
				t.Errorf("RemainingHops = %d, want %d", decoded.RemainingHops, b.RemainingHops)
			}
			if len(decoded.MSTIs) != len(tc.mstis) {
				t.Fatalf("len(MSTIs) = %d, want %d", len(decoded.MSTIs), len(tc.mstis))
			}
			for i, want := range tc.mstis {
				if decoded.MSTIs[i] != want {
					t.Errorf("MSTIs[%d] = %+v, want %+v", i, decoded.MSTIs[i], want)
				}
			}

			// Re-encode and verify identical wire bytes.
			wire, err := frame.Encode()
			if err != nil {
				t.Fatalf("frame.Encode: %v", err)
			}
			reEncodedFrame := mustEncode(t, decoded, mac)
			reEncodedWire, err := reEncodedFrame.Encode()
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			if string(reEncodedWire) != string(wire) {
				t.Error("re-encoded wire bytes do not match original")
			}
		})
	}
}

func TestMSTBPDUDecodeRefusesPartialRecord(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &bpdu.ConfigID{Name: "region-a"},
		MSTIs: []bpdu.MSTIRecord{
			{MSTID: 1, RegionalRootID: bpdu.BridgeID{Priority: 8192, Address: mac}},
		},
	}
	frame := mustEncode(t, b, mac)

	// Truncate the single MSTI record so the payload no longer holds a whole
	// one, while the version 3 length field still claims it does.
	frame.Payload = frame.Payload[:len(frame.Payload)-1]

	_, err := bpdu.Decode(frame)
	if err == nil {
		t.Fatal("Decode unexpectedly succeeded on a partial trailing MSTI record")
	}

	if !errors.Is(err, bpdu.ErrUnsupported) {
		t.Errorf("error = %v, want ErrUnsupported", err)
	}
}

func TestMSTBPDUDecodeTruncatedBodyFallsBackToRST(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	frame := mustEncode(t, b, mac)
	frame.Payload[5] = 3 // Mark the RST BPDU as version 3 without an MST body.

	if len(frame.Payload) >= 105 {
		t.Fatalf("test setup: payload len = %d, want < 105 to exercise the fallback", len(frame.Payload))
	}

	decoded, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("bpdu.Decode: %v", err)
	}

	if decoded.Version != 3 {
		t.Errorf("Version = %d, want 3", decoded.Version)
	}
	if decoded.Type != bpdu.TypeRapid {
		t.Errorf("Type = %v, want %v", decoded.Type, bpdu.TypeRapid)
	}
	if decoded.ConfigID != nil {
		t.Errorf("ConfigID = %+v, want nil", decoded.ConfigID)
	}
	if len(decoded.MSTIs) != 0 {
		t.Errorf("len(MSTIs) = %d, want 0", len(decoded.MSTIs))
	}
}

// TestMSTBPDUEncodePlacesBridgeAndRegionalRootSeparately is a golden test for
// the MST body layout: the CIST bridge identifier belongs at payload octets
// [96:104] and the CIST regional root identifier at [20:28] (the slot the
// RST shape uses for its bridge identifier). A uniform swap of the two
// fields would still pass every round-trip and re-encode assertion, so this
// test pins each field's octets against literal expected bytes instead of
// deriving them from the same encoder logic under test.
func TestMSTBPDUEncodePlacesBridgeAndRegionalRootSeparately(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	bridgeID := bpdu.BridgeID{
		Priority: 0x9005,
		Address:  netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
	}
	regionalRootID := bpdu.BridgeID{
		Priority: 0x1234,
		Address:  netaddr.MAC{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
	}

	b := bpdu.BPDU{
		RootID:         bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:       bridgeID,
		PortID:         0x8001,
		HelloTime:      2 * time.Second,
		MaxAge:         20 * time.Second,
		ForwardDelay:   15 * time.Second,
		ConfigID:       &bpdu.ConfigID{Name: "region-a"},
		RegionalRootID: regionalRootID,
		RemainingHops:  20,
	}

	frame := mustEncode(t, b, mac)

	wantCISTRegionalRootOctets := []byte{0x12, 0x34, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	if got := frame.Payload[20:28]; !bytes.Equal(got, wantCISTRegionalRootOctets) {
		t.Errorf("payload[20:28] = % x, want % x (CIST regional root identifier)", got, wantCISTRegionalRootOctets)
	}

	wantCISTBridgeOctets := []byte{0x90, 0x05, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	if got := frame.Payload[96:104]; !bytes.Equal(got, wantCISTBridgeOctets) {
		t.Errorf("payload[96:104] = % x, want % x (CIST bridge identifier)", got, wantCISTBridgeOctets)
	}

	decoded, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("bpdu.Decode: %v", err)
	}
	if decoded.BridgeID != bridgeID {
		t.Errorf("decoded BridgeID = %v, want %v", decoded.BridgeID, bridgeID)
	}
	if decoded.RegionalRootID != regionalRootID {
		t.Errorf("decoded RegionalRootID = %v, want %v", decoded.RegionalRootID, regionalRootID)
	}
}

// TestMSTBPDUDecodeIgnoresOctetsAfterRecords guards the 14.4 e) reading for
// a payload carrying more octets than its Version 3 Length names.
func TestMSTBPDUDecodeIgnoresOctetsAfterRecords(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &bpdu.ConfigID{Name: "region-a"},
	}
	frame := mustEncode(t, b, mac)

	// Append a whole trailing MSTI record's worth of octets without updating
	// the version 3 length field.
	frame.Payload = append(frame.Payload, make([]byte, 16)...)

	decoded, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if decoded.ConfigID == nil {
		t.Fatal("ConfigID = nil, want MST configuration")
	}
	if len(decoded.MSTIs) != 0 {
		t.Errorf("len(MSTIs) = %d, want 0", len(decoded.MSTIs))
	}
}

// TestMSTBPDUEncodeRecordCountBoundary guards the version 3 length field
// against wrapping a uint16: at 4092 MSTI records the naive computation
// (64 + 16*n) wraps to 0, and Encode must refuse before that point rather
// than emit a length that decodes wrong.
func TestMSTBPDUEncodeRecordCountBoundary(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	baseBPDU := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &bpdu.ConfigID{Name: "region-a"},
	}

	makeMSTIs := func(n int) []bpdu.MSTIRecord {
		mstis := make([]bpdu.MSTIRecord, n)
		for i := range mstis {
			mstis[i] = bpdu.MSTIRecord{
				MSTID:          bpdu.MSTID(1 + i%4094),
				RegionalRootID: bpdu.BridgeID{Priority: 32768, Address: mac},
			}
		}
		return mstis
	}

	const maxRecords = bpdu.MaxMSTIRecords

	t.Run("at the maximum record count", func(t *testing.T) {
		t.Parallel()

		b := baseBPDU
		b.MSTIs = makeMSTIs(maxRecords)

		frame := mustEncode(t, b, mac)

		decoded, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("bpdu.Decode: %v", err)
		}
		if len(decoded.MSTIs) != maxRecords {
			t.Errorf("len(MSTIs) = %d, want %d", len(decoded.MSTIs), maxRecords)
		}
	})

	t.Run("one past the maximum record count", func(t *testing.T) {
		t.Parallel()

		b := baseBPDU
		b.MSTIs = makeMSTIs(maxRecords + 1)

		_, err := bpdu.Encode(b, mac)
		if err == nil {
			t.Fatal("bpdu.Encode: got nil error, want an error for a record count past the maximum")
		}
		if errors.Is(err, bpdu.ErrUnsupported) {
			t.Errorf("bpdu.Encode error unexpectedly wrapped ErrUnsupported: %v", err)
		}
	})
}

// TestMSTIRecordPriorityLowBitsFollowMSTID pins the documented relationship
// between MSTIRecord.MSTID and RegionalRootID.Priority: Encode replaces
// Priority's low 12 bits with MSTID, and Decode re-derives both from those
// same low 12 bits, so the pair round-trips only on Priority's top 4 bits.
func TestMSTIRecordPriorityLowBitsFollowMSTID(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &bpdu.ConfigID{Name: "region-a"},
		MSTIs: []bpdu.MSTIRecord{
			{MSTID: 7, RegionalRootID: bpdu.BridgeID{Priority: 0x8005, Address: mac}},
		},
	}

	frame := mustEncode(t, b, mac)
	decoded, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("bpdu.Decode: %v", err)
	}

	if len(decoded.MSTIs) != 1 {
		t.Fatalf("len(MSTIs) = %d, want 1", len(decoded.MSTIs))
	}
	const wantPriority = 0x8007 // top 4 bits (0x8) kept, low 12 bits replaced by MSTID 7
	if got := decoded.MSTIs[0].RegionalRootID.Priority; got != wantPriority {
		t.Errorf("decoded RegionalRootID.Priority = 0x%04x, want 0x%04x", got, wantPriority)
	}
	if decoded.MSTIs[0].MSTID != 7 {
		t.Errorf("decoded MSTID = %d, want 7", decoded.MSTIs[0].MSTID)
	}
}

func TestHelloTimeValidationPerType(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	t.Run("configuration body with zero hello time accepted", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{
			Type:         bpdu.TypeConfiguration,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
			PortID:       0x8001,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
			HelloTime:    0,
		}
		f := mustEncode(t, b, mac)
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.HelloTime != 0 {
			t.Errorf("HelloTime = %v, want 0", dec.HelloTime)
		}
	})

	t.Run("TCN body is not checked for hello time", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{
			Type: bpdu.TypeTopologyChangeNotification,
		}
		f := mustEncode(t, b, mac)
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.Type != bpdu.TypeTopologyChangeNotification {
			t.Errorf("Type = %v, want %v", dec.Type, bpdu.TypeTopologyChangeNotification)
		}
	})

	t.Run("SSTP configuration body with zero hello time accepted", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{
			Type:         bpdu.TypeConfiguration,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
			PortID:       0x8001,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
			HelloTime:    0,
		}
		f := mustEncodeSSTP(t, b, 10, mac)
		dec, _, err := bpdu.DecodeSSTP(f)
		if err != nil {
			t.Fatalf("DecodeSSTP: %v", err)
		}
		if dec.HelloTime != 0 {
			t.Errorf("HelloTime = %v, want 0", dec.HelloTime)
		}
	})
}

func mustEncodeSSTP(t *testing.T, b bpdu.BPDU, vid vlan.ID, src netaddr.MAC) ethernet.Frame {
	t.Helper()

	frame, err := bpdu.EncodeSSTP(b, vid, src)
	if err != nil {
		t.Fatalf("bpdu.EncodeSSTP: %v", err)
	}
	return frame
}

func TestSSTPCodecRoundTrip(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	root := bpdu.BridgeID{Priority: 0x8000, Address: netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}}
	bridge := bpdu.BridgeID{Priority: 0x9000, Address: mac}

	tests := []struct {
		name string
		b    bpdu.BPDU
		vid  vlan.ID
	}{
		{
			name: "zero flags",
			b: bpdu.BPDU{
				RootID:       root,
				RootPathCost: 4,
				BridgeID:     bridge,
				PortID:       0x8002,
				HelloTime:    defaultHelloTime,
				MaxAge:       defaultMaxAge,
				ForwardDelay: defaultForwardDelay,
				Flags:        0x00,
			},
			vid: 1,
		},
		{
			name: "every RST flag bit set",
			b: bpdu.BPDU{
				RootID:       root,
				RootPathCost: 0,
				BridgeID:     bridge,
				PortID:       0x8001,
				HelloTime:    defaultHelloTime,
				MaxAge:       defaultMaxAge,
				ForwardDelay: defaultForwardDelay,
				Flags:        0x7F,
			},
			vid: 100,
		},
		{
			name: "every flag bit cleared",
			b: bpdu.BPDU{
				RootID:       bridge,
				RootPathCost: 19,
				BridgeID:     bridge,
				PortID:       0x8003,
				HelloTime:    defaultHelloTime,
				MaxAge:       defaultMaxAge,
				ForwardDelay: defaultForwardDelay,
				Flags:        0x00,
			},
			vid: 4094,
		},
		{
			name: "message age and version carried through",
			b: bpdu.BPDU{
				RootID:       root,
				RootPathCost: 200000,
				BridgeID:     bridge,
				PortID:       0x8010,
				MessageAge:   1 * time.Second,
				HelloTime:    defaultHelloTime,
				MaxAge:       defaultMaxAge,
				ForwardDelay: defaultForwardDelay,
				Flags:        0x3D,
				Version:      2,
			},
			vid: 20,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			frame := mustEncodeSSTP(t, tc.b, tc.vid, mac)
			if len(frame.Payload) != 50 {
				t.Fatalf("payload len = %d, want 50", len(frame.Payload))
			}
			if frame.Dst != bpdu.GroupAddressSSTP() {
				t.Errorf("Dst = %v, want %v", frame.Dst, bpdu.GroupAddressSSTP())
			}
			if frame.EtherType != ethernet.EtherType(50) {
				t.Errorf("EtherType = %v, want 50", frame.EtherType)
			}

			decoded, vid, err := bpdu.DecodeSSTP(frame)
			if err != nil {
				t.Fatalf("bpdu.DecodeSSTP: %v", err)
			}

			if vid != tc.vid {
				t.Errorf("vid = %d, want %d", vid, tc.vid)
			}
			if decoded.RootID != tc.b.RootID {
				t.Errorf("RootID = %v, want %v", decoded.RootID, tc.b.RootID)
			}
			if decoded.RootPathCost != tc.b.RootPathCost {
				t.Errorf("RootPathCost = %d, want %d", decoded.RootPathCost, tc.b.RootPathCost)
			}
			if decoded.BridgeID != tc.b.BridgeID {
				t.Errorf("BridgeID = %v, want %v", decoded.BridgeID, tc.b.BridgeID)
			}
			if decoded.PortID != tc.b.PortID {
				t.Errorf("PortID = 0x%04x, want 0x%04x", decoded.PortID, tc.b.PortID)
			}
			if decoded.MessageAge != tc.b.MessageAge {
				t.Errorf("MessageAge = %v, want %v", decoded.MessageAge, tc.b.MessageAge)
			}
			if decoded.MaxAge != tc.b.MaxAge {
				t.Errorf("MaxAge = %v, want %v", decoded.MaxAge, tc.b.MaxAge)
			}
			if decoded.HelloTime != tc.b.HelloTime {
				t.Errorf("HelloTime = %v, want %v", decoded.HelloTime, tc.b.HelloTime)
			}
			if decoded.ForwardDelay != tc.b.ForwardDelay {
				t.Errorf("ForwardDelay = %v, want %v", decoded.ForwardDelay, tc.b.ForwardDelay)
			}
			if decoded.Flags != tc.b.Flags {
				t.Errorf("Flags = 0x%02x, want 0x%02x", decoded.Flags, tc.b.Flags)
			}
			if decoded.Type != bpdu.TypeRapid {
				t.Errorf("Type = %v, want %v", decoded.Type, bpdu.TypeRapid)
			}
			wantVersion := tc.b.Version
			if wantVersion < 2 {
				wantVersion = 2
			}
			if decoded.Version != wantVersion {
				t.Errorf("Version = %d, want %d", decoded.Version, wantVersion)
			}

			// Re-encode and verify identical wire bytes.
			reEncoded := mustEncodeSSTP(t, decoded, vid, mac)
			if !bytes.Equal(reEncoded.Payload, frame.Payload) {
				t.Error("re-encoded payload does not match original")
			}
		})
	}
}

func TestSSTPConfigurationGoldenPayload(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12}
	units := func(v uint16) time.Duration {
		return time.Duration(v) * time.Second / 256
	}
	b := bpdu.BPDU{
		Version: 1,
		Type:    bpdu.TypeConfiguration,
		Flags:   0xff,
		RootID: bpdu.BridgeID{
			Priority: 0x1234,
			Address:  netaddr.MAC{0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
		},
		RootPathCost: 0x01020304,
		BridgeID: bpdu.BridgeID{
			Priority: 0x2345,
			Address:  netaddr.MAC{0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c},
		},
		PortID:       0x3456,
		MessageAge:   units(0x0102),
		MaxAge:       units(0x0304),
		HelloTime:    units(0x0506),
		ForwardDelay: units(0x0708),
	}

	frame := mustEncodeSSTP(t, b, 10, mac)
	want := []byte{
		0xaa, 0xaa, 0x03, // WS payload offsets 0-2: LLC header.
		0x00, 0x00, 0x0c, // WS payload offsets 3-5: SNAP OUI.
		0x01, 0x0b, // WS payload offsets 6-7: SNAP PID.
		0x00, 0x00, // WS payload offsets 8-9: Protocol Identifier.
		0x00,       // WS payload offset 10: Version.
		0x00,       // WS payload offset 11: BPDU Type.
		0x81,       // WS payload offset 12: Configuration flags.
		0x12, 0x34, // WS payload offsets 13-14: Root Identifier priority.
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, // WS payload offsets 15-20: Root Identifier address.
		0x01, 0x02, 0x03, 0x04, // WS payload offsets 21-24: Root Path Cost.
		0x23, 0x45, // WS payload offsets 25-26: Bridge Identifier priority.
		0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, // WS payload offsets 27-32: Bridge Identifier address.
		0x34, 0x56, // WS payload offsets 33-34: Port Identifier.
		0x01, 0x02, // WS payload offsets 35-36: Message Age.
		0x03, 0x04, // WS payload offsets 37-38: Max Age.
		0x05, 0x06, // WS payload offsets 39-40: Hello Time.
		0x07, 0x08, // WS payload offsets 41-42: Forward Delay.
		0x00,       // WS payload offset 43: reserved octet before the TLV.
		0x00, 0x00, // WS payload offsets 44-45: originating-VLAN TLV type.
		0x00, 0x02, // WS payload offsets 46-47: originating-VLAN TLV length.
		0x00, 0x0a, // WS payload offsets 48-49: originating VLAN 10.
	}
	if !bytes.Equal(frame.Payload, want) {
		t.Fatalf("Configuration payload = % x, want % x", frame.Payload, want)
	}

	decoded, vid, err := bpdu.DecodeSSTP(frame)
	if err != nil {
		t.Fatalf("DecodeSSTP: %v", err)
	}
	if vid != 10 {
		t.Errorf("vid = %d, want 10", vid)
	}
	if decoded.Type != bpdu.TypeConfiguration {
		t.Errorf("Type = %v, want Configuration", decoded.Type)
	}
	if decoded.Version != 0 {
		t.Errorf("Version = %d, want 0", decoded.Version)
	}
	if decoded.Flags != 0x81 {
		t.Errorf("Flags = 0x%02x, want 0x81", decoded.Flags)
	}
	if decoded.RootID != b.RootID || decoded.RootPathCost != b.RootPathCost || decoded.BridgeID != b.BridgeID || decoded.PortID != b.PortID {
		t.Errorf("decoded identifiers/body = %+v, want root=%+v cost=%d bridge=%+v port=0x%04x", decoded, b.RootID, b.RootPathCost, b.BridgeID, b.PortID)
	}
	if decoded.MessageAge != b.MessageAge || decoded.MaxAge != b.MaxAge || decoded.HelloTime != b.HelloTime || decoded.ForwardDelay != b.ForwardDelay {
		t.Errorf("decoded timers = %v/%v/%v/%v, want %v/%v/%v/%v", decoded.MessageAge, decoded.MaxAge, decoded.HelloTime, decoded.ForwardDelay, b.MessageAge, b.MaxAge, b.HelloTime, b.ForwardDelay)
	}
}

func TestSSTPTopologyChangeNotificationGoldenPayload(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	frame := mustEncodeSSTP(t, bpdu.BPDU{Type: bpdu.TypeTopologyChangeNotification}, 10, mac)
	want := append([]byte{
		0xaa, 0xaa, 0x03, 0x00, 0x00, 0x0c, 0x01, 0x0b,
		0x00, 0x00, 0x00, 0x80,
	}, make([]byte, 34)...)
	if frame.EtherType != ethernet.EtherType(12) {
		t.Errorf("EtherType = %d, want 12", frame.EtherType)
	}
	if !bytes.Equal(frame.Payload, want) {
		t.Fatalf("TCN payload = % x, want % x", frame.Payload, want)
	}

	decoded, vid, err := bpdu.DecodeSSTP(frame)
	if err != nil {
		t.Fatalf("DecodeSSTP padded TCN: %v", err)
	}
	if decoded.Type != bpdu.TypeTopologyChangeNotification || decoded.Version != 0 || vid != 0 {
		t.Errorf("decoded TCN = type %v version %d vid %d, want TCN/0/0", decoded.Type, decoded.Version, vid)
	}

	short := frame
	short.Payload = short.Payload[:12]
	if _, vid, err := bpdu.DecodeSSTP(short); err != nil || vid != 0 {
		t.Errorf("DecodeSSTP 12-octet TCN = vid %d, err %v, want 0/nil", vid, err)
	}
	short.Payload = short.Payload[:11]
	if _, _, err := bpdu.DecodeSSTP(short); err == nil {
		t.Fatal("DecodeSSTP accepted an 11-octet TCN")
	}
}

// TestSSTPEncodeGoldenPayload pins the SSTP wire layout against literal
// expected bytes for VID 20, so the encoder's own field choices cannot drift
// the layout underneath a round-trip test that would not notice a uniform
// reordering.
func TestSSTPEncodeGoldenPayload(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 0x8000, Address: netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}},
		RootPathCost: 4,
		BridgeID:     bpdu.BridgeID{Priority: 0x9000, Address: mac},
		PortID:       0x8002,
		MessageAge:   0,
		MaxAge:       defaultMaxAge,
		HelloTime:    defaultHelloTime,
		ForwardDelay: defaultForwardDelay,
		Flags:        0x00,
	}

	frame := mustEncodeSSTP(t, b, vlan.ID(20), mac)

	want, err := hex.DecodeString(strings.ReplaceAll(
		"AA AA 03 00 00 0C 01 0B 00 00 02 02 00 80 00 AA BB CC DD EE FF "+
			"00 00 00 04 90 00 00 11 22 33 44 55 80 02 00 00 14 00 02 00 0F 00 "+
			"00 00 00 00 02 00 14",
		" ", ""))
	if err != nil {
		t.Fatalf("hex.DecodeString: %v", err)
	}
	if len(want) != 50 {
		t.Fatalf("golden vector len = %d, want 50", len(want))
	}

	if len(frame.Payload) != len(want) {
		t.Fatalf("payload len = %d, want %d", len(frame.Payload), len(want))
	}
	for i := range want {
		if frame.Payload[i] != want[i] {
			t.Errorf("payload[%d] = 0x%02x, want 0x%02x", i, frame.Payload[i], want[i])
		}
	}
}

func TestSSTPDecodeRefusals(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	validBPDU := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	validFrame := mustEncodeSSTP(t, validBPDU, 20, mac)

	tests := []struct {
		name      string
		modify    func(f *ethernet.Frame)
		wantField string
	}{
		{
			name: "payload too short",
			modify: func(f *ethernet.Frame) {
				f.Payload = f.Payload[:49]
			},
			wantField: "too short",
		},
		{
			name: "wrong LLC header",
			modify: func(f *ethernet.Frame) {
				f.Payload[0] = 0x00
			},
			wantField: "LLC header",
		},
		{
			name: "wrong SNAP OUI",
			modify: func(f *ethernet.Frame) {
				f.Payload[3] = 0x01
			},
			wantField: "OUI",
		},
		{
			name: "wrong SNAP PID",
			modify: func(f *ethernet.Frame) {
				binary.BigEndian.PutUint16(f.Payload[6:8], 0x0001)
			},
			wantField: "PID",
		},
		{
			name: "wrong protocol identifier",
			modify: func(f *ethernet.Frame) {
				binary.BigEndian.PutUint16(f.Payload[8:10], 0x0001)
			},
			wantField: "protocol identifier",
		},
		{
			name: "version below 2",
			modify: func(f *ethernet.Frame) {
				f.Payload[10] = 1
			},
			wantField: "version",
		},
		{
			name: "wire type not 0x02",
			modify: func(f *ethernet.Frame) {
				f.Payload[11] = 0x81
			},
			wantField: "type",
		},
		{
			name: "TLV type not 0",
			modify: func(f *ethernet.Frame) {
				binary.BigEndian.PutUint16(f.Payload[44:46], 0x0001)
			},
			wantField: "TLV type",
		},
		{
			name: "TLV length not 2",
			modify: func(f *ethernet.Frame) {
				binary.BigEndian.PutUint16(f.Payload[46:48], 0x0003)
			},
			wantField: "TLV length",
		},
	}

	config := validBPDU
	config.Type = bpdu.TypeConfiguration
	config.Version = 0
	configFrame := mustEncodeSSTP(t, config, 20, mac)
	if _, _, err := bpdu.DecodeSSTP(configFrame); err != nil {
		t.Fatalf("DecodeSSTP refused a version-0 Configuration BPDU: %v", err)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := ethernet.Frame{
				Dst:       validFrame.Dst,
				Src:       validFrame.Src,
				EtherType: validFrame.EtherType,
				Payload:   append([]byte(nil), validFrame.Payload...),
			}
			tc.modify(&f)

			_, _, err := bpdu.DecodeSSTP(f)
			if err == nil {
				t.Fatal("DecodeSSTP unexpectedly succeeded")
			}

			if !errors.Is(err, bpdu.ErrUnsupported) {
				t.Errorf("Decode error = %v, want ErrUnsupported", err)
			}

			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantField)) {
				t.Errorf("error %q does not name field %q", err.Error(), tc.wantField)
			}
		})
	}
}

func TestEncodeSSTPRefusesMSTConfigID(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	b := bpdu.BPDU{
		RootID:    bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:  bpdu.BridgeID{Priority: 4096, Address: mac},
		HelloTime: 2 * time.Second,
		ConfigID:  &bpdu.ConfigID{Name: "region-a"},
	}

	_, err := bpdu.EncodeSSTP(b, 1, mac)
	if err == nil {
		t.Fatal("EncodeSSTP unexpectedly succeeded for a BPDU with ConfigID set")
	}

	if !errors.Is(err, bpdu.ErrUnsupported) {
		t.Errorf("error = %v, want ErrUnsupported", err)
	}
}

func TestSSTPCrossesWithPlainBPDU(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	b := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}

	t.Run("Decode refuses an SSTP frame", func(t *testing.T) {
		t.Parallel()

		sstpFrame := mustEncodeSSTP(t, b, 20, mac)

		_, err := bpdu.Decode(sstpFrame)
		if err == nil {
			t.Fatal("Decode unexpectedly succeeded for an SSTP frame")
		}
		if !errors.Is(err, bpdu.ErrUnsupported) {
			t.Errorf("error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("DecodeSSTP refuses a plain LLC BPDU frame", func(t *testing.T) {
		t.Parallel()

		llcFrame := mustEncode(t, b, mac)

		_, _, err := bpdu.DecodeSSTP(llcFrame)
		if err == nil {
			t.Fatal("DecodeSSTP unexpectedly succeeded for a plain LLC BPDU frame")
		}
		if !errors.Is(err, bpdu.ErrUnsupported) {
			t.Errorf("error = %v, want ErrUnsupported", err)
		}
	})
}

func TestDecodeRefusesTruncatedPayload(t *testing.T) {
	t.Parallel()

	f := ethernet.Frame{
		Payload: []byte{0x42, 0x42, 0x03},
	}
	_, err := bpdu.Decode(f)
	if !errors.Is(err, bpdu.ErrUnsupported) {
		t.Fatalf("Decode(truncated) error = %v, want ErrUnsupported", err)
	}
}

func TestMaxMSTIRecordsRefuses65Where64Encode(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	baseBPDU := bpdu.BPDU{
		RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &bpdu.ConfigID{Name: "region-a"},
	}

	makeMSTIs := func(n int) []bpdu.MSTIRecord {
		mstis := make([]bpdu.MSTIRecord, n)
		for i := range mstis {
			mstis[i] = bpdu.MSTIRecord{
				MSTID:          bpdu.MSTID(1 + i),
				RegionalRootID: bpdu.BridgeID{Priority: 32768, Address: mac},
			}
		}
		return mstis
	}

	t.Run("64 records encode and decode", func(t *testing.T) {
		t.Parallel()
		b := baseBPDU
		b.MSTIs = makeMSTIs(64)
		frame, err := bpdu.Encode(b, mac)
		if err != nil {
			t.Fatalf("Encode(64 records): %v", err)
		}
		dec, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("Decode(64 records): %v", err)
		}
		if len(dec.MSTIs) != 64 {
			t.Errorf("decoded records = %d, want 64", len(dec.MSTIs))
		}
	})

	t.Run("65 records are refused by Encode", func(t *testing.T) {
		t.Parallel()
		b := baseBPDU
		b.MSTIs = makeMSTIs(65)
		_, err := bpdu.Encode(b, mac)
		if err == nil {
			t.Fatal("Encode(65 records) succeeded, want refusal")
		}
	})

	t.Run("complete version 3 body naming 65 records decodes as RST BPDU", func(t *testing.T) {
		t.Parallel()
		b := baseBPDU
		b.MSTIs = makeMSTIs(64)
		frame, err := bpdu.Encode(b, mac)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		extraRecord := append([]byte(nil), frame.Payload[len(frame.Payload)-16:]...)
		frame.Payload = append(frame.Payload, extraRecord...)
		frame.EtherType = ethernet.EtherType(len(frame.Payload) - 3)
		binary.BigEndian.PutUint16(frame.Payload[39:41], 1104)
		dec, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("Decode(v3 len naming 65): %v", err)
		}
		if dec.Type != bpdu.TypeRapid {
			t.Errorf("Type = %v, want TypeRapid", dec.Type)
		}
		if dec.ConfigID != nil {
			t.Errorf("ConfigID = %+v, want nil (decoded as RST)", dec.ConfigID)
		}
		if len(dec.MSTIs) != 0 {
			t.Errorf("len(MSTIs) = %d, want 0 (decoded as RST)", len(dec.MSTIs))
		}
	})
}

func TestBPDUDecodeQ2003Compliance(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}

	t.Run("version 2 Configuration BPDU is accepted", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{
			Version:      2,
			Type:         bpdu.TypeConfiguration,
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		frame, err := bpdu.Encode(b, mac)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		frame.Payload[5] = 2
		frame.Payload[6] = 0
		dec, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("Decode version 2 Configuration BPDU: %v", err)
		}
		if dec.Type != bpdu.TypeConfiguration {
			t.Errorf("Type = %v, want TypeConfiguration", dec.Type)
		}
		if dec.Version != 2 {
			t.Errorf("Version = %d, want 2", dec.Version)
		}
	})

	t.Run("nonzero Version 1 Length decodes as RST", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
			ConfigID:     &bpdu.ConfigID{Name: "region-a"},
		}
		frame, err := bpdu.Encode(b, mac)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		frame.Payload[38] = 1
		dec, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.ConfigID != nil {
			t.Errorf("ConfigID = %+v, want nil (decoded as RST)", dec.ConfigID)
		}
		if len(dec.MSTIs) != 0 {
			t.Errorf("len(MSTIs) = %d, want 0", len(dec.MSTIs))
		}
	})

	t.Run("version 4 with whole MST body decodes as MST", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
			ConfigID:     &bpdu.ConfigID{Name: "region-a"},
		}
		frame, err := bpdu.Encode(b, mac)
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		frame.Payload[5] = 4
		dec, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("Decode version 4 MST: %v", err)
		}
		if dec.ConfigID == nil || dec.ConfigID.Name != "region-a" {
			t.Errorf("ConfigID = %+v, want region-a (decoded as MST)", dec.ConfigID)
		}
		if dec.Version != 4 {
			t.Errorf("Version = %d, want 4", dec.Version)
		}
	})

	t.Run("version 2 rejects a 38-byte RST payload", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		frame := mustEncode(t, b, mac)
		frame.Payload = append([]byte(nil), frame.Payload[:38]...)
		frame.Payload[5] = 2

		_, err := bpdu.Decode(frame)
		if err == nil {
			t.Fatal("Decode accepted a 38-byte version 2 RST payload")
		}
		if !errors.Is(err, bpdu.ErrUnsupported) {
			t.Errorf("error = %v, want ErrUnsupported", err)
		}
	})

	t.Run("version 3 accepts a 38-byte RST payload", func(t *testing.T) {
		t.Parallel()
		b := bpdu.BPDU{
			RootID:       bpdu.BridgeID{Priority: 4096, Address: mac},
			BridgeID:     bpdu.BridgeID{Priority: 4096, Address: mac},
			PortID:       0x8001,
			HelloTime:    2 * time.Second,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
		}
		frame := mustEncode(t, b, mac)
		frame.Payload = append([]byte(nil), frame.Payload[:38]...)
		frame.Payload[5] = 3

		dec, err := bpdu.Decode(frame)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.Type != bpdu.TypeRapid {
			t.Errorf("Type = %v, want TypeRapid", dec.Type)
		}
		if dec.Version != 3 {
			t.Errorf("Version = %d, want 3", dec.Version)
		}
		if dec.ConfigID != nil {
			t.Errorf("ConfigID = %+v, want nil", dec.ConfigID)
		}
		if len(dec.MSTIs) != 0 {
			t.Errorf("len(MSTIs) = %d, want 0", len(dec.MSTIs))
		}
	})
}

func mstBPDUWireFixture() []byte {
	return []byte{
		// LLC header: DSAP, SSAP, Control
		0x42, 0x42, 0x03,

		// Figure 14-1 octets 1-2: Protocol Identifier (0x0000)
		0x00, 0x00,
		// Figure 14-1 octet 3: Protocol Version Identifier (3)
		0x03,
		// Figure 14-1 octet 4: BPDU Type (0x02, Rapid/MST BPDU)
		0x02,
		// Figure 14-1 octet 5: CIST Flags
		0x7d,
		// Figure 14-1 octets 6-13: CIST Root Identifier (Priority 0x1234, MAC 01:02:03:04:05:06)
		0x12, 0x34, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06,
		// Figure 14-1 octets 14-17: CIST External Root Path Cost (0x0708090a)
		0x07, 0x08, 0x09, 0x0a,
		// Figure 14-1 octets 18-25: CIST Regional Root Identifier (Priority 0x2345, MAC 0b:0c:0d:0e:0f:10)
		0x23, 0x45, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
		// Figure 14-1 octets 26-27: CIST Port Identifier (0x8123)
		0x81, 0x23,
		// Figure 14-1 octets 28-29: Message Age (0x0100 = 1s)
		0x01, 0x00,
		// Figure 14-1 octets 30-31: Max Age (0x1400 = 20s)
		0x14, 0x00,
		// Figure 14-1 octets 32-33: Hello Time (0x0200 = 2s)
		0x02, 0x00,
		// Figure 14-1 octets 34-35: Forward Delay (0x0f00 = 15s)
		0x0f, 0x00,
		// Figure 14-1 octet 36: Version 1 Length (0)
		0x00,
		// Figure 14-1 octets 37-38: Version 3 Length (96 = 64 + 2*16)
		0x00, 0x60,
		// Figure 14-1 octet 39: Configuration Identifier Format Selector (0)
		0x00,
		// Figure 14-1 octets 40-71: Configuration Name ("region-fixture", padded to 32 octets)
		'r', 'e', 'g', 'i', 'o', 'n', '-', 'f', 'i', 'x', 't', 'u', 'r', 'e', 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		// Figure 14-1 octets 72-73: Revision Level (0x0102)
		0x01, 0x02,
		// Figure 14-1 octets 74-89: Configuration Digest (16 octets)
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
		// Figure 14-1 octets 90-93: CIST Internal Root Path Cost (0x21222324)
		0x21, 0x22, 0x23, 0x24,
		// Figure 14-1 octets 94-101: CIST Bridge Identifier (Priority 0x3142, MAC 25:26:27:28:29:2a)
		0x31, 0x42, 0x25, 0x26, 0x27, 0x28, 0x29, 0x2a,
		// Figure 14-1 octet 102: CIST Remaining Hops (20)
		0x14,

		// Figure 14-2 MSTI Configuration Message 1:
		// Figure 14-2 octet 1: MSTI Flags
		0x01,
		// Figure 14-2 octets 2-9: MSTI Regional Root Identifier (Priority 0x4001, MAC 2b:2c:2d:2e:2f:30)
		0x40, 0x01, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f, 0x30,
		// Figure 14-2 octets 10-13: MSTI Internal Root Path Cost (0x31323334)
		0x31, 0x32, 0x33, 0x34,
		// Figure 14-2 octet 14: MSTI Bridge Priority (0x40)
		0x40,
		// Figure 14-2 octet 15: MSTI Port Priority (0x20)
		0x20,
		// Figure 14-2 octet 16: MSTI Remaining Hops (19)
		0x13,

		// Figure 14-2 MSTI Configuration Message 2:
		// Figure 14-2 octet 1: MSTI Flags
		0x02,
		// Figure 14-2 octets 2-9: MSTI Regional Root Identifier (Priority 0x1002, MAC 35:36:37:38:39:3a)
		0x10, 0x02, 0x35, 0x36, 0x37, 0x38, 0x39, 0x3a,
		// Figure 14-2 octets 10-13: MSTI Internal Root Path Cost (0x3b3c3d3e)
		0x3b, 0x3c, 0x3d, 0x3e,
		// Figure 14-2 octet 14: MSTI Bridge Priority (0x10)
		0x10,
		// Figure 14-2 octet 15: MSTI Port Priority (0xE0)
		0xe0,
		// Figure 14-2 octet 16: MSTI Remaining Hops (18)
		0x12,
	}
}

// TestMSTBPDUWireFixture asserts the exact wire bytes of an MST BPDU against
// a literal fixture commented per field with its octets in IEEE 802.1Q-2003
// Figure 14-1 and Figure 14-2, ensuring every multi-octet field has distinct
// bytes in each octet, Decode returns the fields, and Encode returns the bytes.
func TestMSTBPDUWireFixture(t *testing.T) {
	t.Parallel()

	srcMAC := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	wire := mstBPDUWireFixture()

	frame := ethernet.Frame{
		Dst:       netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
		Src:       srcMAC,
		EtherType: ethernet.EtherType(len(wire) - 3),
		Payload:   wire,
	}

	dec, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if dec.Version != 3 {
		t.Errorf("Version = %d, want 3", dec.Version)
	}
	if dec.Type != bpdu.TypeRapid {
		t.Errorf("Type = %v, want TypeRapid", dec.Type)
	}
	if dec.Flags != 0x7d {
		t.Errorf("Flags = 0x%02x, want 0x7d", dec.Flags)
	}
	if dec.RootID != (bpdu.BridgeID{Priority: 0x1234, Address: netaddr.MAC{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}}) {
		t.Errorf("RootID = %v, want Priority 0x1234", dec.RootID)
	}
	if dec.RootPathCost != 0x0708090a {
		t.Errorf("RootPathCost = 0x%08x, want 0x0708090a", dec.RootPathCost)
	}
	if dec.RegionalRootID != (bpdu.BridgeID{Priority: 0x2345, Address: netaddr.MAC{0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}}) {
		t.Errorf("RegionalRootID = %v, want Priority 0x2345", dec.RegionalRootID)
	}
	if dec.PortID != 0x8123 {
		t.Errorf("PortID = 0x%04x, want 0x8123", dec.PortID)
	}
	if dec.MessageAge != time.Second {
		t.Errorf("MessageAge = %v, want 1s", dec.MessageAge)
	}
	if dec.MaxAge != 20*time.Second {
		t.Errorf("MaxAge = %v, want 20s", dec.MaxAge)
	}
	if dec.HelloTime != 2*time.Second {
		t.Errorf("HelloTime = %v, want 2s", dec.HelloTime)
	}
	if dec.ForwardDelay != 15*time.Second {
		t.Errorf("ForwardDelay = %v, want 15s", dec.ForwardDelay)
	}
	if dec.InternalRootPathCost != 0x21222324 {
		t.Errorf("InternalRootPathCost = 0x%08x, want 0x21222324", dec.InternalRootPathCost)
	}
	if dec.BridgeID != (bpdu.BridgeID{Priority: 0x3142, Address: netaddr.MAC{0x25, 0x26, 0x27, 0x28, 0x29, 0x2a}}) {
		t.Errorf("BridgeID = %v, want Priority 0x3142", dec.BridgeID)
	}
	if dec.RemainingHops != 20 {
		t.Errorf("RemainingHops = %d, want 20", dec.RemainingHops)
	}

	wantConfigID := bpdu.ConfigID{
		Selector: 0,
		Name:     "region-fixture",
		Revision: 0x0102,
		Digest:   [16]byte{0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
	}
	if dec.ConfigID == nil || *dec.ConfigID != wantConfigID {
		t.Errorf("ConfigID = %+v, want %+v", dec.ConfigID, wantConfigID)
	}

	wantMSTIs := []bpdu.MSTIRecord{
		{
			MSTID:                1,
			Flags:                0x01,
			RegionalRootID:       bpdu.BridgeID{Priority: 0x4001, Address: netaddr.MAC{0x2b, 0x2c, 0x2d, 0x2e, 0x2f, 0x30}},
			InternalRootPathCost: 0x31323334,
			BridgePriority:       0x40,
			PortPriority:         0x20,
			RemainingHops:        19,
		},
		{
			MSTID:                2,
			Flags:                0x02,
			RegionalRootID:       bpdu.BridgeID{Priority: 0x1002, Address: netaddr.MAC{0x35, 0x36, 0x37, 0x38, 0x39, 0x3a}},
			InternalRootPathCost: 0x3b3c3d3e,
			BridgePriority:       0x10,
			PortPriority:         0xe0,
			RemainingHops:        18,
		},
	}
	if len(dec.MSTIs) != len(wantMSTIs) {
		t.Fatalf("len(MSTIs) = %d, want %d", len(dec.MSTIs), len(wantMSTIs))
	}
	for i, want := range wantMSTIs {
		if dec.MSTIs[i] != want {
			t.Errorf("MSTI %d record = %+v, want %+v", i+1, dec.MSTIs[i], want)
		}
	}

	wantBPDU := bpdu.BPDU{
		Version:              3,
		Type:                 bpdu.TypeRapid,
		Flags:                0x7d,
		RootID:               bpdu.BridgeID{Priority: 0x1234, Address: netaddr.MAC{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}},
		RootPathCost:         0x0708090a,
		BridgeID:             bpdu.BridgeID{Priority: 0x3142, Address: netaddr.MAC{0x25, 0x26, 0x27, 0x28, 0x29, 0x2a}},
		PortID:               0x8123,
		MessageAge:           time.Second,
		MaxAge:               20 * time.Second,
		HelloTime:            2 * time.Second,
		ForwardDelay:         15 * time.Second,
		ConfigID:             &wantConfigID,
		RegionalRootID:       bpdu.BridgeID{Priority: 0x2345, Address: netaddr.MAC{0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}},
		InternalRootPathCost: 0x21222324,
		RemainingHops:        20,
		MSTIs:                wantMSTIs,
	}
	enc, err := bpdu.Encode(wantBPDU, srcMAC)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !bytes.Equal(enc.Payload, wire) {
		t.Errorf("Encode payload does not match wire fixture bytes:\ngot:  % x\nwant: % x", enc.Payload, wire)
	}
}

func TestDecodeVersion3ByLength(t *testing.T) {
	t.Parallel()

	const (
		version3   = 3
		version4   = 4
		version255 = 255
	)

	tests := []struct {
		name            string
		octets          int
		version         uint8
		version1Len     byte
		version3Len     uint16
		wantMST         bool
		wantRecords     int
		wantUnsupported bool
	}{
		{name: "34 octets are refused", octets: 34, version: version3, wantUnsupported: true},
		{name: "35 octets are RST version 3", octets: 35, version: version3},
		{name: "35 octets are RST version 4", octets: 35, version: version4},
		{name: "35 octets are RST version 255", octets: 35, version: version255},
		{name: "60 octets are RST despite MST-shaped lengths", octets: 60, version: version3, version3Len: 80},
		{name: "101 octets are RST despite MST-shaped lengths", octets: 101, version: version3, version3Len: 64},
		{name: "102 octets are MST with no records", octets: 102, version: version3, version3Len: 64, wantMST: true},
		{name: "102 octets are RST with a longer MST length", octets: 102, version: version3, version3Len: 80},
		{name: "102 octets are RST with a nonzero Version 1 Length", octets: 102, version: version3, version1Len: 1, version3Len: 64},
		{name: "103 octets are MST with no records", octets: 103, version: version3, version3Len: 64, wantMST: true},
		{name: "117 octets refuse an absent record", octets: 117, version: version3, version3Len: 80, wantUnsupported: true},
		{name: "118 octets are MST with one record", octets: 118, version: version3, version3Len: 80, wantMST: true, wantRecords: 1},
		{name: "119 octets are MST with one record and trailing octets", octets: 119, version: version3, version3Len: 80, wantMST: true, wantRecords: 1},
		{name: "118 octets are RST with a nonzero Version 1 Length", octets: 118, version: version3, version1Len: 1, version3Len: 80},
		{name: "118 octets are RST with a partial record length", octets: 118, version: version3, version3Len: 81},
		{name: "118 octets are RST with too many records", octets: 118, version: version3, version3Len: 1104},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payload := append([]byte(nil), mstBPDUWireFixture()...)
			wantPayloadLen := tc.octets + 3
			if wantPayloadLen < len(payload) {
				payload = payload[:wantPayloadLen]
			} else {
				payload = append(payload, make([]byte, wantPayloadLen-len(payload))...)
			}
			payload[5] = tc.version
			if len(payload) >= 41 {
				payload[38] = tc.version1Len
				binary.BigEndian.PutUint16(payload[39:41], tc.version3Len)
			}

			frame := ethernet.Frame{
				Dst:       netaddr.MAC{0x01, 0x80, 0xc2, 0x00, 0x00, 0x00},
				Src:       netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
				EtherType: ethernet.EtherType(tc.octets),
				Payload:   payload,
			}

			decoded, err := bpdu.Decode(frame)
			if tc.wantUnsupported {
				if err == nil {
					t.Fatal("Decode succeeded, want ErrUnsupported")
				}
				if !errors.Is(err, bpdu.ErrUnsupported) {
					t.Fatalf("error = %v, want ErrUnsupported", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}

			if decoded.Version != tc.version {
				t.Errorf("Version = %d, want %d", decoded.Version, tc.version)
			}
			if decoded.Type != bpdu.TypeRapid {
				t.Errorf("Type = %v, want TypeRapid", decoded.Type)
			}
			if tc.wantMST {
				if decoded.ConfigID == nil {
					t.Fatal("ConfigID = nil, want MST configuration")
				}
				if len(decoded.MSTIs) != tc.wantRecords {
					t.Errorf("len(MSTIs) = %d, want %d", len(decoded.MSTIs), tc.wantRecords)
				}
				return
			}

			if decoded.ConfigID != nil {
				t.Errorf("ConfigID = %+v, want nil for RST", decoded.ConfigID)
			}
			if len(decoded.MSTIs) != 0 {
				t.Errorf("len(MSTIs) = %d, want 0 for RST", len(decoded.MSTIs))
			}
			wantRoot := bpdu.BridgeID{Priority: 0x1234, Address: netaddr.MAC{0x01, 0x02, 0x03, 0x04, 0x05, 0x06}}
			if decoded.RootID != wantRoot {
				t.Errorf("RootID = %v, want %v", decoded.RootID, wantRoot)
			}
			if decoded.RootPathCost != 0x0708090a {
				t.Errorf("RootPathCost = 0x%08x, want 0x0708090a", decoded.RootPathCost)
			}
			wantBridge := bpdu.BridgeID{Priority: 0x2345, Address: netaddr.MAC{0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10}}
			if decoded.BridgeID != wantBridge {
				t.Errorf("BridgeID = %v, want %v", decoded.BridgeID, wantBridge)
			}
			if decoded.PortID != 0x8123 {
				t.Errorf("PortID = 0x%04x, want 0x8123", decoded.PortID)
			}
		})
	}
}

func TestDecodeVersion3LengthsNeverPanic(t *testing.T) {
	t.Parallel()

	for octets := 0; octets <= 150; octets++ {
		octets := octets
		t.Run(fmt.Sprintf("%d octets", octets), func(t *testing.T) {
			t.Parallel()

			payload := append([]byte(nil), mstBPDUWireFixture()...)
			wantPayloadLen := octets + 3
			if wantPayloadLen < len(payload) {
				payload = payload[:wantPayloadLen]
			} else {
				payload = append(payload, make([]byte, wantPayloadLen-len(payload))...)
			}
			if len(payload) >= 6 {
				payload[5] = 3
			}
			if len(payload) >= 7 {
				payload[6] = 2
			}

			frame := ethernet.Frame{
				EtherType: ethernet.EtherType(octets),
				Payload:   payload,
			}
			_, err := bpdu.Decode(frame)
			if err != nil && !errors.Is(err, bpdu.ErrUnsupported) {
				t.Fatalf("Decode error = %v, want nil or ErrUnsupported", err)
			}
		})
	}
}
