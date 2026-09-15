package stp_test

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/net/netaddr"
	"go.aledante.io/FlowSeer/src/common/netsim/vswitch/stp"
)

func TestBPDUCodecRoundTrip(t *testing.T) {
	t.Parallel()

	mac, err := netaddr.Parse("00:11:22:33:44:55")
	if err != nil {
		t.Fatalf("Parse MAC: %v", err)
	}

	bridgeID := stp.BridgeID{
		Priority: 4096,
		Address:  mac,
	}

	b := stp.BPDU{
		RootID:       bridgeID,
		RootPathCost: 0,
		BridgeID:     bridgeID,
		PortID:       (128 << 8) | 1,
		MessageAge:   0,
		MaxAge:       stp.DefaultMaxAge,
		HelloTime:    stp.DefaultHelloTime,
		ForwardDelay: stp.DefaultForwardDelay,
	}
	b.SetRole(stp.RoleDesignated)
	b.SetProposal(true)

	frame := stp.Encode(b, mac)
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

	decodedBPDU, err := stp.Decode(decodedFrame)
	if err != nil {
		t.Fatalf("stp.Decode: %v", err)
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
	if decodedBPDU.Role() != stp.RoleDesignated {
		t.Errorf("Role() = %v, want %v", decodedBPDU.Role(), stp.RoleDesignated)
	}

	// Re-encode and verify identical wire bytes.
	reEncodedFrame := stp.Encode(decodedBPDU, mac)
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
	validBPDU := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	validFrame := stp.Encode(validBPDU, mac)

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
			name: "type 0 refused",
			modify: func(f *ethernet.Frame) {
				f.Payload[6] = 0
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

			_, err := stp.Decode(f)
			if err == nil {
				t.Fatal("Decode unexpectedly succeeded")
			}

			attrs := errs.Attributes(err)
			reason, ok := attrs["reason"]
			if !ok || reason != stp.ReasonUnsupportedBPDU {
				t.Errorf("reason = %v, want %v", reason, stp.ReasonUnsupportedBPDU)
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
		b := stp.BPDU{HelloTime: stp.DefaultHelloTime}
		b.SetTopologyChange(true)
		if !b.TopologyChange() {
			t.Error("TopologyChange() = false, want true")
		}
		if b.Flags&0x01 == 0 {
			t.Errorf("bit 0 not set: Flags=0x%02x", b.Flags)
		}
		f := stp.Encode(b, mac)
		dec, err := stp.Decode(f)
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
		b := stp.BPDU{HelloTime: stp.DefaultHelloTime}
		b.SetProposal(true)
		if !b.Proposal() {
			t.Error("Proposal() = false, want true")
		}
		if b.Flags&0x02 == 0 {
			t.Errorf("bit 1 not set: Flags=0x%02x", b.Flags)
		}
		f := stp.Encode(b, mac)
		dec, err := stp.Decode(f)
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
			role     stp.Role
			wantBits uint8
		}{
			{role: stp.RoleRoot, wantBits: 2 << 2},
			{role: stp.RoleDesignated, wantBits: 3 << 2},
			{role: stp.RoleAlternate, wantBits: 1 << 2},
			{role: stp.RoleBackup, wantBits: 1 << 2},
			{role: stp.RoleDisabled, wantBits: 0},
		}

		for _, c := range cases {
			b := stp.BPDU{HelloTime: stp.DefaultHelloTime}
			b.SetRole(c.role)
			if (b.Flags & (3 << 2)) != c.wantBits {
				t.Errorf("role %v: Flags bits 2-3 = 0x%02x, want 0x%02x", c.role, b.Flags&(3<<2), c.wantBits)
			}
			f := stp.Encode(b, mac)
			dec, err := stp.Decode(f)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			expectedDecodedRole := c.role
			if c.role == stp.RoleBackup {
				expectedDecodedRole = stp.RoleAlternate
			}
			if dec.Role() != expectedDecodedRole {
				t.Errorf("decoded Role() = %v, want %v", dec.Role(), expectedDecodedRole)
			}
		}
	})

	t.Run("learning", func(t *testing.T) {
		t.Parallel()
		b := stp.BPDU{HelloTime: stp.DefaultHelloTime}
		b.SetLearning(true)
		if !b.Learning() {
			t.Error("Learning() = false, want true")
		}
		if b.Flags&0x10 == 0 {
			t.Errorf("bit 4 not set: Flags=0x%02x", b.Flags)
		}
		f := stp.Encode(b, mac)
		dec, err := stp.Decode(f)
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
		b := stp.BPDU{HelloTime: stp.DefaultHelloTime}
		b.SetForwarding(true)
		if !b.Forwarding() {
			t.Error("Forwarding() = false, want true")
		}
		if b.Flags&0x20 == 0 {
			t.Errorf("bit 5 not set: Flags=0x%02x", b.Flags)
		}
		f := stp.Encode(b, mac)
		dec, err := stp.Decode(f)
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
		b := stp.BPDU{HelloTime: stp.DefaultHelloTime}
		b.SetAgreement(true)
		if !b.Agreement() {
			t.Error("Agreement() = false, want true")
		}
		if b.Flags&0x40 == 0 {
			t.Errorf("bit 6 not set: Flags=0x%02x", b.Flags)
		}
		f := stp.Encode(b, mac)
		dec, err := stp.Decode(f)
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
		b := stp.BPDU{HelloTime: stp.DefaultHelloTime}
		b.SetTopologyChangeAck(true)
		if !b.TopologyChangeAck() {
			t.Error("TopologyChangeAck() = false, want true")
		}
		if b.Flags&0x80 == 0 {
			t.Errorf("bit 7 not set: Flags=0x%02x", b.Flags)
		}
		f := stp.Encode(b, mac)
		dec, err := stp.Decode(f)
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

// TestDecodeRefusesZeroHelloTime guards the aging arithmetic: a BPDU whose
// hello time is zero would otherwise expire the instant it arrived.
func TestDecodeRefusesZeroHelloTime(t *testing.T) {
	t.Parallel()

	root := stp.BridgeID{Priority: 4096, Address: netaddr.MAC{0, 0x11, 0x22, 0x33, 0x44, 1}}
	frame := stp.Encode(stp.BPDU{RootID: root, BridgeID: root, MaxAge: 20 * time.Second}, root.Address)
	if _, err := stp.Decode(frame); err == nil {
		t.Fatal("Decode accepted a BPDU with hello time 0")
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

	decoded, err := stp.Decode(frame)
	if err != nil {
		t.Fatalf("stp.Decode: %v", err)
	}

	if decoded.Version != 0 {
		t.Errorf("Version = %d, want 0", decoded.Version)
	}
	if decoded.Type != stp.BPDUTypeConfiguration {
		t.Errorf("Type = %v, want %v", decoded.Type, stp.BPDUTypeConfiguration)
	}
	if !decoded.TopologyChange() {
		t.Error("TopologyChange() = false, want true")
	}
	if decoded.Role() != stp.RoleDesignated {
		t.Errorf("Role() = %v, want %v", decoded.Role(), stp.RoleDesignated)
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

	decoded, err := stp.Decode(frame)
	if err != nil {
		t.Fatalf("stp.Decode: %v", err)
	}

	if decoded.Type != stp.BPDUTypeTopologyChangeNotification {
		t.Errorf("Type = %v, want %v", decoded.Type, stp.BPDUTypeTopologyChangeNotification)
	}
	if decoded.Version != 0 {
		t.Errorf("Version = %d, want 0", decoded.Version)
	}
}

func TestEncodeConfigurationBPDURoundTrip(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c}
	bridgeID := stp.BridgeID{Priority: 61440, Address: mac}

	b := stp.BPDU{
		Type:         stp.BPDUTypeConfiguration,
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
	b.SetRole(stp.RoleRoot)

	frame := stp.Encode(b, mac)
	if frame.EtherType != ethernet.EtherType(38) {
		t.Errorf("EtherType = %d, want 38", frame.EtherType)
	}
	if len(frame.Payload) != 46 {
		t.Fatalf("payload len = %d, want 46", len(frame.Payload))
	}
	if frame.Payload[7] != 0x00 {
		t.Errorf("flags octet = 0x%02x, want 0x00", frame.Payload[7])
	}

	decoded, err := stp.Decode(frame)
	if err != nil {
		t.Fatalf("stp.Decode: %v", err)
	}

	if decoded.Type != stp.BPDUTypeConfiguration {
		t.Errorf("Type = %v, want %v", decoded.Type, stp.BPDUTypeConfiguration)
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
	if decoded.Role() != stp.RoleDesignated {
		t.Errorf("Role() = %v, want %v", decoded.Role(), stp.RoleDesignated)
	}
	if decoded.Proposal() {
		t.Error("Proposal() = true, want false")
	}
}

func TestEncodeEmptyBPDURapid(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	frame := stp.Encode(stp.BPDU{}, mac)

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

	frameWithHello := stp.Encode(stp.BPDU{HelloTime: 2 * time.Second}, mac)
	dec, err := stp.Decode(frameWithHello)
	if err != nil {
		t.Fatalf("stp.Decode: %v", err)
	}
	if dec.Version != 2 {
		t.Errorf("Version = %d, want 2", dec.Version)
	}
	if dec.Type != stp.BPDUTypeRapid {
		t.Errorf("Type = %v, want %v", dec.Type, stp.BPDUTypeRapid)
	}
}

func TestBPDUVersionAndTypeCombinations(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	valid := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}

	t.Run("version 2 type 0 refused", func(t *testing.T) {
		t.Parallel()
		f := stp.Encode(valid, mac)
		f.Payload[5] = 2
		f.Payload[6] = 0
		_, err := stp.Decode(f)
		if err == nil {
			t.Fatal("Decode unexpectedly succeeded for version 2 type 0")
		}
		attrs := errs.Attributes(err)
		if attrs["reason"] != stp.ReasonUnsupportedBPDU {
			t.Errorf("reason = %v, want %v", attrs["reason"], stp.ReasonUnsupportedBPDU)
		}
	})

	t.Run("version 0 type 2 refused", func(t *testing.T) {
		t.Parallel()
		f := stp.Encode(valid, mac)
		f.Payload[5] = 0
		f.Payload[6] = 2
		_, err := stp.Decode(f)
		if err == nil {
			t.Fatal("Decode unexpectedly succeeded for version 0 type 2")
		}
		attrs := errs.Attributes(err)
		if attrs["reason"] != stp.ReasonUnsupportedBPDU {
			t.Errorf("reason = %v, want %v", attrs["reason"], stp.ReasonUnsupportedBPDU)
		}
	})

	t.Run("version 3 RST body decodes with Version 3", func(t *testing.T) {
		t.Parallel()
		f := stp.Encode(valid, mac)
		f.Payload[5] = 3
		f.Payload[6] = 2
		dec, err := stp.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.Version != 3 {
			t.Errorf("Version = %d, want 3", dec.Version)
		}
		if dec.Type != stp.BPDUTypeRapid {
			t.Errorf("Type = %v, want %v", dec.Type, stp.BPDUTypeRapid)
		}
	})
}

func TestMSTBPDUCodecRoundTrip(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	regionalRoot := netaddr.MAC{0x02, 0x00, 0x00, 0x00, 0x00, 0x0c}
	root := stp.BridgeID{Priority: 4096, Address: mac}

	configID := &stp.ConfigID{
		Selector: 0,
		Name:     "region-a",
		Revision: 3,
		Digest:   [16]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
	}

	tests := []struct {
		name  string
		mstis []stp.MSTIRecord
	}{
		{name: "zero records", mstis: nil},
		{
			name: "one record",
			mstis: []stp.MSTIRecord{
				{
					MSTID:                1,
					Flags:                0x01,
					RegionalRootID:       stp.BridgeID{Priority: 8192 + 1, Address: regionalRoot},
					InternalRootPathCost: 100,
					BridgePriority:       0x20,
					PortPriority:         0x80,
					RemainingHops:        19,
				},
			},
		},
		{
			name: "two records",
			mstis: []stp.MSTIRecord{
				{
					MSTID:                1,
					Flags:                0x01,
					RegionalRootID:       stp.BridgeID{Priority: 8192 + 1, Address: regionalRoot},
					InternalRootPathCost: 100,
					BridgePriority:       0x20,
					PortPriority:         0x80,
					RemainingHops:        19,
				},
				{
					MSTID:                2,
					Flags:                0x02,
					RegionalRootID:       stp.BridgeID{Priority: 4096 + 2, Address: mac},
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

			b := stp.BPDU{
				RootID:               root,
				BridgeID:             root,
				PortID:               0x8001,
				HelloTime:            2 * time.Second,
				MaxAge:               20 * time.Second,
				ForwardDelay:         15 * time.Second,
				ConfigID:             configID,
				RegionalRootID:       stp.BridgeID{Priority: 32768, Address: regionalRoot},
				InternalRootPathCost: 50,
				RemainingHops:        20,
				MSTIs:                tc.mstis,
			}

			frame := stp.Encode(b, mac)

			wantPayloadLen := 3 + 102 + 16*len(tc.mstis)
			if len(frame.Payload) != wantPayloadLen {
				t.Fatalf("payload len = %d, want %d", len(frame.Payload), wantPayloadLen)
			}
			if frame.EtherType != ethernet.EtherType(wantPayloadLen) {
				t.Errorf("EtherType = %v, want %d", frame.EtherType, wantPayloadLen)
			}

			decoded, err := stp.Decode(frame)
			if err != nil {
				t.Fatalf("stp.Decode: %v", err)
			}

			if decoded.Version != 3 {
				t.Errorf("Version = %d, want 3", decoded.Version)
			}
			if decoded.Type != stp.BPDUTypeRapid {
				t.Errorf("Type = %v, want %v", decoded.Type, stp.BPDUTypeRapid)
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
			reEncodedFrame := stp.Encode(decoded, mac)
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
	b := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &stp.ConfigID{Name: "region-a"},
		MSTIs: []stp.MSTIRecord{
			{MSTID: 1, RegionalRootID: stp.BridgeID{Priority: 8192, Address: mac}},
		},
	}
	frame := stp.Encode(b, mac)

	// Truncate the single MSTI record so the payload no longer holds a whole
	// one, while the version 3 length field still claims it does.
	frame.Payload = frame.Payload[:len(frame.Payload)-1]

	_, err := stp.Decode(frame)
	if err == nil {
		t.Fatal("Decode unexpectedly succeeded on a partial trailing MSTI record")
	}

	attrs := errs.Attributes(err)
	if attrs["reason"] != stp.ReasonUnsupportedBPDU {
		t.Errorf("reason = %v, want %v", attrs["reason"], stp.ReasonUnsupportedBPDU)
	}
}

func TestMSTBPDUDecodeTruncatedBodyFallsBackToRST(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	b := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
	}
	frame := stp.Encode(b, mac)
	frame.Payload[5] = 3 // Mark the RST BPDU as version 3 without an MST body.

	if len(frame.Payload) >= 105 {
		t.Fatalf("test setup: payload len = %d, want < 105 to exercise the fallback", len(frame.Payload))
	}

	decoded, err := stp.Decode(frame)
	if err != nil {
		t.Fatalf("stp.Decode: %v", err)
	}

	if decoded.Version != 3 {
		t.Errorf("Version = %d, want 3", decoded.Version)
	}
	if decoded.Type != stp.BPDUTypeRapid {
		t.Errorf("Type = %v, want %v", decoded.Type, stp.BPDUTypeRapid)
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
	bridgeID := stp.BridgeID{
		Priority: 0x9005,
		Address:  netaddr.MAC{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff},
	}
	regionalRootID := stp.BridgeID{
		Priority: 0x1234,
		Address:  netaddr.MAC{0x11, 0x22, 0x33, 0x44, 0x55, 0x66},
	}

	b := stp.BPDU{
		RootID:         stp.BridgeID{Priority: 4096, Address: mac},
		BridgeID:       bridgeID,
		PortID:         0x8001,
		HelloTime:      2 * time.Second,
		MaxAge:         20 * time.Second,
		ForwardDelay:   15 * time.Second,
		ConfigID:       &stp.ConfigID{Name: "region-a"},
		RegionalRootID: regionalRootID,
		RemainingHops:  20,
	}

	frame := stp.Encode(b, mac)

	wantCISTRegionalRootOctets := []byte{0x12, 0x34, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66}
	if got := frame.Payload[20:28]; !bytes.Equal(got, wantCISTRegionalRootOctets) {
		t.Errorf("payload[20:28] = % x, want % x (CIST regional root identifier)", got, wantCISTRegionalRootOctets)
	}

	wantCISTBridgeOctets := []byte{0x90, 0x05, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	if got := frame.Payload[96:104]; !bytes.Equal(got, wantCISTBridgeOctets) {
		t.Errorf("payload[96:104] = % x, want % x (CIST bridge identifier)", got, wantCISTBridgeOctets)
	}

	decoded, err := stp.Decode(frame)
	if err != nil {
		t.Fatalf("stp.Decode: %v", err)
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
	b := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &stp.ConfigID{Name: "region-a"},
	}
	frame := stp.Encode(b, mac)

	// Append a whole trailing MSTI record's worth of octets without updating
	// the version 3 length field, as ten records appended past the length
	// the field names would look on the wire.
	frame.Payload = append(frame.Payload, make([]byte, 16)...)

	_, err := stp.Decode(frame)
	if err == nil {
		t.Fatal("Decode unexpectedly succeeded on a payload longer than its version 3 length names")
	}

	attrs := errs.Attributes(err)
	if attrs["reason"] != stp.ReasonUnsupportedBPDU {
		t.Errorf("reason = %v, want %v", attrs["reason"], stp.ReasonUnsupportedBPDU)
	}
}

// TestMSTBPDUEncodeRecordCountBoundary guards the version 3 length field
// against wrapping a uint16: at 4092 MSTI records the naive computation
// (64 + 16*n) wraps to 0, and Encode must refuse before that point rather
// than emit a length that decodes wrong.
func TestMSTBPDUEncodeRecordCountBoundary(t *testing.T) {
	t.Parallel()

	mac := netaddr.MAC{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	baseBPDU := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &stp.ConfigID{Name: "region-a"},
	}

	makeMSTIs := func(n int) []stp.MSTIRecord {
		mstis := make([]stp.MSTIRecord, n)
		for i := range mstis {
			mstis[i] = stp.MSTIRecord{
				MSTID:          stp.MSTID(1 + i%4094),
				RegionalRootID: stp.BridgeID{Priority: 32768, Address: mac},
			}
		}
		return mstis
	}

	const maxRecords = (65535 - 64) / 16 // mirrors stp's own maxMSTIRecords bound

	t.Run("at the maximum record count", func(t *testing.T) {
		t.Parallel()

		b := baseBPDU
		b.MSTIs = makeMSTIs(maxRecords)

		frame := stp.Encode(b, mac)
		if len(frame.Payload) == 0 {
			t.Fatal("Encode returned a zero Frame at the maximum record count")
		}

		decoded, err := stp.Decode(frame)
		if err != nil {
			t.Fatalf("stp.Decode: %v", err)
		}
		if len(decoded.MSTIs) != maxRecords {
			t.Errorf("len(MSTIs) = %d, want %d", len(decoded.MSTIs), maxRecords)
		}
	})

	t.Run("one past the maximum record count", func(t *testing.T) {
		t.Parallel()

		b := baseBPDU
		b.MSTIs = makeMSTIs(maxRecords + 1)

		frame := stp.Encode(b, mac)
		if len(frame.Payload) != 0 {
			t.Errorf("payload len = %d, want 0 (Encode should refuse to encode)", len(frame.Payload))
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
	b := stp.BPDU{
		RootID:       stp.BridgeID{Priority: 4096, Address: mac},
		BridgeID:     stp.BridgeID{Priority: 4096, Address: mac},
		PortID:       0x8001,
		HelloTime:    2 * time.Second,
		MaxAge:       20 * time.Second,
		ForwardDelay: 15 * time.Second,
		ConfigID:     &stp.ConfigID{Name: "region-a"},
		MSTIs: []stp.MSTIRecord{
			{MSTID: 7, RegionalRootID: stp.BridgeID{Priority: 0x8005, Address: mac}},
		},
	}

	frame := stp.Encode(b, mac)
	decoded, err := stp.Decode(frame)
	if err != nil {
		t.Fatalf("stp.Decode: %v", err)
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

	t.Run("configuration body with zero hello time refused", func(t *testing.T) {
		t.Parallel()
		b := stp.BPDU{
			Type:         stp.BPDUTypeConfiguration,
			RootID:       stp.BridgeID{Priority: 4096, Address: mac},
			BridgeID:     stp.BridgeID{Priority: 4096, Address: mac},
			PortID:       0x8001,
			MaxAge:       20 * time.Second,
			ForwardDelay: 15 * time.Second,
			HelloTime:    0,
		}
		f := stp.Encode(b, mac)
		_, err := stp.Decode(f)
		if err == nil {
			t.Fatal("Decode unexpectedly succeeded for Configuration BPDU with zero hello time")
		}
		attrs := errs.Attributes(err)
		if attrs["reason"] != stp.ReasonUnsupportedBPDU {
			t.Errorf("reason = %v, want %v", attrs["reason"], stp.ReasonUnsupportedBPDU)
		}
	})

	t.Run("TCN body is not checked for hello time", func(t *testing.T) {
		t.Parallel()
		b := stp.BPDU{
			Type: stp.BPDUTypeTopologyChangeNotification,
		}
		f := stp.Encode(b, mac)
		dec, err := stp.Decode(f)
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if dec.Type != stp.BPDUTypeTopologyChangeNotification {
			t.Errorf("Type = %v, want %v", dec.Type, stp.BPDUTypeTopologyChangeNotification)
		}
	})
}
