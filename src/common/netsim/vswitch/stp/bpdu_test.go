package stp_test

import (
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
		var b stp.BPDU
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
		var b stp.BPDU
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
			var b stp.BPDU
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
		var b stp.BPDU
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
		var b stp.BPDU
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
		var b stp.BPDU
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
		var b stp.BPDU
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
