package bpdu_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"reflect"
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
			name: "type 0x81 refused",
			modify: func(f *ethernet.Frame) {
				f.Payload[6] = 0x81
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
		b := bpdu.BPDU{HelloTime: defaultHelloTime}
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

// TestDecodeAcceptsZeroHelloTime pins that the codec reads a Hello Time of
// zero as zero. Q2003 14.4 sets no bound on it, so the caller decides what a
// BPDU that announces no hello interval means.
func TestDecodeAcceptsZeroHelloTime(t *testing.T) {
	t.Parallel()

	root := bpdu.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 1}}
	frame := mustEncode(t, bpdu.BPDU{RootID: root, BridgeID: root, MaxAge: 20 * time.Second}, root.Address)
	got, err := bpdu.Decode(frame)
	if err != nil {
		t.Fatalf("Decode refused a BPDU with hello time 0: %v", err)
	}
	if got.HelloTime != 0 {
		t.Errorf("HelloTime = %s, want 0", got.HelloTime)
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

	t.Run("version 2 type 0 decodes as a Configuration BPDU", func(t *testing.T) {
		t.Parallel()
		f := mustEncode(t, valid, mac)
		f.Payload[5] = 2
		f.Payload[6] = 0
		dec, err := bpdu.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.Type != bpdu.TypeConfiguration || dec.Version != 2 {
			t.Errorf("Type, Version = %v, %d, want Configuration, 2", dec.Type, dec.Version)
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

// TestMSTBPDUDecodeRefusesOverlongPayload guards against a payload carrying
// more octets than its own version 3 length names: silently accepting the
// extra octets would decode a longer capture as an MST BPDU with fewer (or
// no) records, aging out information the sender actually refreshed.
func TestMSTBPDUDecodeRefusesOverlongPayload(t *testing.T) {
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
	// the version 3 length field, as ten records appended past the length
	// the field names would look on the wire.
	frame.Payload = append(frame.Payload, make([]byte, 16)...)

	_, err := bpdu.Decode(frame)
	if err == nil {
		t.Fatal("Decode unexpectedly succeeded on a payload longer than its version 3 length names")
	}

	if !errors.Is(err, bpdu.ErrUnsupported) {
		t.Errorf("error = %v, want ErrUnsupported", err)
	}
}

// TestMSTBPDUEncodeRecordCountBoundary pins the limit of Q2003 13.14, 14.4
// d) 3), and 14.6 v): no more than 64 MSTI Configuration Messages in an MST
// BPDU. Encode refuses a 65th, and the 64 it accepts decode back.
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

	const maxRecords = 64 // Q2003 13.14

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

func TestHelloTimePerType(t *testing.T) {
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
			t.Fatalf("Decode refused a Configuration BPDU with zero hello time: %v", err)
		}
		if dec.HelloTime != 0 {
			t.Errorf("HelloTime = %s, want 0", dec.HelloTime)
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
			name: "every flag bit set",
			b: bpdu.BPDU{
				RootID:       root,
				RootPathCost: 0,
				BridgeID:     bridge,
				PortID:       0x8001,
				HelloTime:    defaultHelloTime,
				MaxAge:       defaultMaxAge,
				ForwardDelay: defaultForwardDelay,
				Flags:        0xFF,
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
				f.Payload[11] = 0x00
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

// mstFixture is an MST BPDU with two MSTI records, written octet by octet
// from Q2003 Figure 14-1 (BPDU octets 1 to 102) and Figure 14-2 (MSTI
// Configuration Message octets 1 to 16). The LLC header precedes BPDU octet
// 1, so BPDU octet n is payload[n+2]. Every multi-octet field differs in each
// of its octets, so a swapped or shifted octet shows. Wireshark's dissector
// (packet-bpdu.c) reads the same offsets. No device capture backs this
// fixture.
var mstFixture = []byte{
	0x42, 0x42, 0x03, // LLC header: DSAP, SSAP, control.
	0x00, 0x00, // Octets 1-2: Protocol Identifier.
	0x03,                                           // Octet 3: Protocol Version Identifier.
	0x02,                                           // Octet 4: BPDU Type, RST/MST.
	0x7e,                                           // Octet 5: CIST Flags, proposal, Designated role, learning, forwarding, agreement.
	0x30, 0x05, 0x00, 0x1b, 0x2c, 0x3d, 0x4e, 0x5f, // Octets 6-13: CIST Root Identifier.
	0x00, 0x01, 0x86, 0xa0, // Octets 14-17: CIST External Path Cost, 100000.
	0x50, 0x03, 0x06, 0xa1, 0xb2, 0xc3, 0xd4, 0xe5, // Octets 18-25: CIST Regional Root Identifier.
	0x81, 0x1d, // Octets 26-27: CIST Port Identifier.
	0x01, 0x80, // Octets 28-29: Message Age, 1.5 s in 1/256 s.
	0x14, 0x80, // Octets 30-31: Max Age, 20.5 s.
	0x02, 0x40, // Octets 32-33: Hello Time, 2.25 s.
	0x0f, 0x40, // Octets 34-35: Forward Delay, 15.25 s.
	0x00,       // Octet 36: Version 1 Length.
	0x00, 0x60, // Octets 37-38: Version 3 Length, 64 + 2*16.
	0x00,                                                                         // Octet 39: MST Configuration Identifier, Format Selector.
	'f', 'l', 'o', 'w', 's', 'e', 'e', 'r', '-', 'r', 'e', 'g', 'i', 'o', 'n', 0, // Octets 40-55: Configuration Name, first 16 octets.
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, // Octets 56-71: Configuration Name, null padding to 32 octets.
	0x0a, 0x0b, // Octets 72-73: Revision Level.
	0xd0, 0xd1, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xdb, 0xdc, 0xdd, 0xde, 0xdf, // Octets 74-89: Configuration Digest.
	0x00, 0x0f, 0x42, 0x40, // Octets 90-93: CIST Internal Root Path Cost, 1000000.
	0x60, 0x07, 0x0a, 0x1c, 0x2d, 0x3e, 0x4f, 0x50, // Octets 94-101: CIST Bridge Identifier.
	0x13, // Octet 102: CIST Remaining Hops.

	// First MSTI Configuration Message, octets 103-118.
	0x3c,                                           // Octet 1: MSTI Flags, Designated role, learning, forwarding.
	0x70, 0x01, 0x0c, 0x21, 0x32, 0x43, 0x54, 0x65, // Octets 2-9: MSTI Regional Root Identifier, MSTID 1 in the low 12 bits of octets 2-3.
	0x00, 0x01, 0xc3, 0x50, // Octets 10-13: MSTI Internal Root Path Cost, 115536.
	0x40, // Octet 14: MSTI Bridge Priority in bits 5-8.
	0x20, // Octet 15: MSTI Port Priority in bits 5-8.
	0x12, // Octet 16: MSTI Remaining Hops.

	// Second MSTI Configuration Message, octets 119-134.
	0x78,                                           // Octet 1: MSTI Flags, Root role, learning, forwarding, agreement.
	0x80, 0x02, 0x0e, 0x1f, 0x2a, 0x3b, 0x4c, 0x5d, // Octets 2-9: MSTI Regional Root Identifier, MSTID 2.
	0x00, 0x02, 0x4f, 0x6b, // Octets 10-13: MSTI Internal Root Path Cost, 151403.
	0x10, // Octet 14: MSTI Bridge Priority.
	0xe0, // Octet 15: MSTI Port Priority.
	0x11, // Octet 16: MSTI Remaining Hops.
}

func mstFixtureBPDU() bpdu.BPDU {
	rootMAC := netaddr.MAC{0x00, 0x1b, 0x2c, 0x3d, 0x4e, 0x5f}
	return bpdu.BPDU{
		Version:      3,
		Type:         bpdu.TypeRapid,
		Flags:        0x7e,
		RootID:       bpdu.BridgeID{Priority: 0x3005, Address: rootMAC},
		RootPathCost: 100000,
		BridgeID:     bpdu.BridgeID{Priority: 0x6007, Address: netaddr.MAC{0x0a, 0x1c, 0x2d, 0x3e, 0x4f, 0x50}},
		PortID:       0x811d,
		MessageAge:   1500 * time.Millisecond,
		MaxAge:       20*time.Second + 500*time.Millisecond,
		HelloTime:    2*time.Second + 250*time.Millisecond,
		ForwardDelay: 15*time.Second + 250*time.Millisecond,
		ConfigID: &bpdu.ConfigID{
			Name:     "flowseer-region",
			Revision: 0x0a0b,
			Digest:   [16]byte{0xd0, 0xd1, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xdb, 0xdc, 0xdd, 0xde, 0xdf},
		},
		RegionalRootID:       bpdu.BridgeID{Priority: 0x5003, Address: netaddr.MAC{0x06, 0xa1, 0xb2, 0xc3, 0xd4, 0xe5}},
		InternalRootPathCost: 1000000,
		RemainingHops:        0x13,
		MSTIs: []bpdu.MSTIRecord{
			{
				MSTID:                1,
				Flags:                0x3c,
				RegionalRootID:       bpdu.BridgeID{Priority: 0x7001, Address: netaddr.MAC{0x0c, 0x21, 0x32, 0x43, 0x54, 0x65}},
				InternalRootPathCost: 115536,
				BridgePriority:       0x40,
				PortPriority:         0x20,
				RemainingHops:        0x12,
			},
			{
				MSTID:                2,
				Flags:                0x78,
				RegionalRootID:       bpdu.BridgeID{Priority: 0x8002, Address: netaddr.MAC{0x0e, 0x1f, 0x2a, 0x3b, 0x4c, 0x5d}},
				InternalRootPathCost: 151403,
				BridgePriority:       0x10,
				PortPriority:         0xe0,
				RemainingHops:        0x11,
			},
		},
	}
}

func fixtureFrame(payload []byte) ethernet.Frame {
	return ethernet.Frame{Payload: append([]byte(nil), payload...)}
}

func TestMSTFixtureDecodes(t *testing.T) {
	t.Parallel()

	got, err := bpdu.Decode(fixtureFrame(mstFixture))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	want := mstFixtureBPDU()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Decode = %+v\nwant %+v", got, want)
	}
}

func TestMSTFixtureEncodes(t *testing.T) {
	t.Parallel()

	frame := mustEncode(t, mstFixtureBPDU(), netaddr.MAC{0x02, 0, 0, 0, 0, 1})
	if !bytes.Equal(frame.Payload, mstFixture) {
		t.Errorf("Encode payload =\n% x\nwant\n% x", frame.Payload, mstFixture)
	}
	if frame.EtherType != ethernet.EtherType(len(mstFixture)) {
		t.Errorf("EtherType = %d, want %d", frame.EtherType, len(mstFixture))
	}
}

// TestMSTIPriorityOctetsKeepTheirHighNibble pins Q2003 14.6.1 d) and e): bits
// 1 to 4 of the MSTI Bridge Priority and Port Priority octets are sent as 0
// and ignored on receipt.
func TestMSTIPriorityOctetsKeepTheirHighNibble(t *testing.T) {
	t.Parallel()

	const (
		bridgeOctet = 3 + 102 + 13
		portOctet   = bridgeOctet + 1
	)

	t.Run("encode sends the low nibbles as zero", func(t *testing.T) {
		t.Parallel()

		b := mstFixtureBPDU()
		b.MSTIs[0].BridgePriority = 0x4f
		b.MSTIs[0].PortPriority = 0x2f
		frame := mustEncode(t, b, netaddr.MAC{0x02, 0, 0, 0, 0, 1})
		if got := frame.Payload[bridgeOctet]; got != 0x40 {
			t.Errorf("bridge priority octet = 0x%02x, want 0x40", got)
		}
		if got := frame.Payload[portOctet]; got != 0x20 {
			t.Errorf("port priority octet = 0x%02x, want 0x20", got)
		}
	})

	t.Run("decode ignores the low nibbles", func(t *testing.T) {
		t.Parallel()

		payload := append([]byte(nil), mstFixture...)
		payload[bridgeOctet] = 0x4f
		payload[portOctet] = 0x2f
		got, err := bpdu.Decode(fixtureFrame(payload))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if got.MSTIs[0].BridgePriority != 0x40 || got.MSTIs[0].PortPriority != 0x20 {
			t.Errorf("priorities = 0x%02x, 0x%02x, want 0x40, 0x20", got.MSTIs[0].BridgePriority, got.MSTIs[0].PortPriority)
		}
	})
}

// TestDecodeClassifiesBPDUsPerQ2003 pins Q2003 14.4. Each case differs from
// the fixture, or from the case it names, in one property.
func TestDecodeClassifiesBPDUsPerQ2003(t *testing.T) {
	t.Parallel()

	type shape int
	const (
		refused shape = iota
		configuration
		tcn
		rst
		mst
	)

	// withRecords returns the fixture's MST body with n zero records, the
	// length field naming n.
	withRecords := func(n int) []byte {
		p := append([]byte(nil), mstFixture[:3+102]...)
		p = append(p, make([]byte, 16*n)...)
		binary.BigEndian.PutUint16(p[39:41], uint16(64+16*n))
		return p
	}
	// edit returns a copy of p with fn applied.
	edit := func(p []byte, fn func([]byte) []byte) []byte {
		return fn(append([]byte(nil), p...))
	}
	set := func(i int, v byte) func([]byte) []byte {
		return func(p []byte) []byte { p[i] = v; return p }
	}
	cut := func(n int) func([]byte) []byte {
		return func(p []byte) []byte { return p[:n] }
	}
	setV3Length := func(v uint16) func([]byte) []byte {
		return func(p []byte) []byte {
			binary.BigEndian.PutUint16(p[39:41], v)
			return p
		}
	}
	// retype sets the version and type octets and keeps n octets after the LLC header.
	retype := func(version, typ byte, n int) func([]byte) []byte {
		return func(p []byte) []byte {
			p[5], p[6] = version, typ
			return p[:3+n]
		}
	}

	tests := []struct {
		name    string
		payload []byte
		want    shape
		version uint8
	}{
		{"MST body with two records", mstFixture, mst, 3},
		{"MST body with 64 records", withRecords(64), mst, 3},
		{"65 records named by the length field", withRecords(65), rst, 3},
		{"Version 1 Length nonzero", edit(mstFixture, set(3+35, 1)), rst, 3},
		{"version 4 with a whole MST body", edit(mstFixture, set(5, 4)), mst, 4},
		{"version 255 with a whole MST body", edit(mstFixture, set(5, 255)), mst, 255},
		{"version 3 length not a whole number of records", edit(mstFixture, setV3Length(64+16*2+1)), rst, 3},
		{"version 3 length below 64", edit(mstFixture, setV3Length(63)), rst, 3},
		{"payload shorter than the length field claims", edit(mstFixture, cut(len(mstFixture)-1)), refused, 0},
		{"version 3 body of 102 octets with no records", withRecords(0), mst, 3},
		{"version 3 with 101 octets", edit(withRecords(0), cut(3+101)), rst, 3},
		{"version 3 with 35 octets", edit(mstFixture, cut(3+35)), rst, 3},
		{"version 3 with 34 octets", edit(mstFixture, cut(3+34)), refused, 0},
		{"version 2 RST BPDU with 36 octets", edit(mstFixture, retype(2, 2, 36)), rst, 2},
		{"version 2 RST BPDU with 35 octets", edit(mstFixture, retype(2, 2, 35)), refused, 0},
		{"version 0 type 2", edit(mstFixture, retype(0, 2, 36)), refused, 0},
		{"version 2 Configuration BPDU", edit(mstFixture, retype(2, 0, 35)), configuration, 2},
		{"version 3 Configuration BPDU", edit(mstFixture, retype(3, 0, 35)), configuration, 3},
		{"version 3 Configuration BPDU with 34 octets", edit(mstFixture, retype(3, 0, 34)), refused, 0},
		{"version 3 TCN", edit(mstFixture, retype(3, 0x80, 4)), tcn, 3},
		{"version 0 TCN", edit(mstFixture, retype(0, 0x80, 4)), tcn, 0},
		{"TCN with 3 octets", edit(mstFixture, retype(3, 0x80, 3)), refused, 0},
		{"version 3 unknown type", edit(mstFixture, set(6, 0x81)), refused, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := bpdu.Decode(fixtureFrame(tc.payload))
			if tc.want == refused {
				if !errors.Is(err, bpdu.ErrUnsupported) {
					t.Fatalf("Decode error = %v, want ErrUnsupported", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Version != tc.version {
				t.Errorf("Version = %d, want %d", got.Version, tc.version)
			}
			switch tc.want {
			case configuration:
				if got.Type != bpdu.TypeConfiguration || got.ConfigID != nil {
					t.Errorf("Type, ConfigID = %v, %v, want Configuration, nil", got.Type, got.ConfigID)
				}
			case tcn:
				if got.Type != bpdu.TypeTopologyChangeNotification {
					t.Errorf("Type = %v, want TCN", got.Type)
				}
			case rst:
				if got.Type != bpdu.TypeRapid || got.ConfigID != nil || len(got.MSTIs) != 0 {
					t.Errorf("Type, ConfigID, MSTIs = %v, %v, %d, want an RST BPDU", got.Type, got.ConfigID, len(got.MSTIs))
				}
			case mst:
				if got.Type != bpdu.TypeRapid || got.ConfigID == nil {
					t.Errorf("Type, ConfigID = %v, %v, want an MST BPDU", got.Type, got.ConfigID)
				}
			case refused:
			}
		})
	}
}

// TestDecodeNeverPanicsOnMalformedInput feeds Decode every prefix of the
// fixture under each version, type, Version 1 Length, and Version 3 Length
// that selects a different branch of Q2003 14.4.
func TestDecodeNeverPanicsOnMalformedInput(t *testing.T) {
	t.Parallel()

	for _, version := range []byte{0, 1, 2, 3, 4, 255} {
		for _, typ := range []byte{0, 2, 3, 0x80, 0xff} {
			for _, v1 := range []byte{0, 1} {
				for _, v3 := range []uint16{0, 63, 64, 80, 96, 1088, 1104, 65535} {
					full := append([]byte(nil), mstFixture...)
					full[5], full[6], full[38] = version, typ, v1
					binary.BigEndian.PutUint16(full[39:41], v3)
					for n := 0; n <= len(full); n++ {
						_, _ = bpdu.Decode(fixtureFrame(full[:n]))
					}
				}
			}
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(mstFixture)
	f.Add(mstFixture[:40])
	f.Add([]byte{0x42, 0x42, 0x03, 0, 0, 0, 0x80})
	f.Fuzz(func(_ *testing.T, payload []byte) {
		_, _ = bpdu.Decode(ethernet.Frame{Payload: payload})
	})
}
