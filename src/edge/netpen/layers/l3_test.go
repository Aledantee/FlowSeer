// Tests for netpen's owned L3/name-resolution decoders. Each protocol
// is pinned against pcap fixtures harvested from the Python tool's byte
// construction: byte-for-byte where deterministic, field-set where
// randomized. Malformed and adversarial frames carry provenance notes in
// the corpus, never deleted.

package layers

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
)

func TestHSRPDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "hsrp.pcap")
	golden := readGolden(t, "hsrp.json")

	frames := golden["frames"].([]any)
	if len(pkts) != len(frames) {
		t.Fatalf("hsrp.pcap has %d packets, golden has %d", len(pkts), len(frames))
	}

	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			hsrp := mustGetLayer[*HSRP](t, p, LayerTypeHSRP)

			frame := frames[i].(map[string]any)

			if hsrp.Version != uint8(frame["version"].(float64)) {
				t.Errorf("version: got %d, want %d", hsrp.Version, uint8(frame["version"].(float64)))
			}
			if hsrp.Opcode != HSRPOpcode(frame["opcode"].(float64)) {
				t.Errorf("opcode: got %d, want %d", hsrp.Opcode, HSRPOpcode(frame["opcode"].(float64)))
			}
			if hsrp.State != HSRPState(frame["state"].(float64)) {
				t.Errorf("state: got %d, want %d", hsrp.State, HSRPState(frame["state"].(float64)))
			}
			if hsrp.Hellotime != uint8(frame["hellotime"].(float64)) {
				t.Errorf("hellotime: got %d, want %d", hsrp.Hellotime, uint8(frame["hellotime"].(float64)))
			}
			if hsrp.Holdtime != uint8(frame["holdtime"].(float64)) {
				t.Errorf("holdtime: got %d, want %d", hsrp.Holdtime, uint8(frame["holdtime"].(float64)))
			}
			if hsrp.Priority != uint8(frame["priority"].(float64)) {
				t.Errorf("priority: got %d, want %d", hsrp.Priority, uint8(frame["priority"].(float64)))
			}
			if hsrp.Group != uint8(frame["group"].(float64)) {
				t.Errorf("group: got %d, want %d", hsrp.Group, uint8(frame["group"].(float64)))
			}
			wantVIP := net.ParseIP(frame["vip"].(string))
			if !hsrp.VirtualIP.Equal(wantVIP) {
				t.Errorf("virtualIP: got %s, want %s", hsrp.VirtualIP, wantVIP)
			}
			wantAuth := frame["auth"].(string)
			if string(hsrp.Auth[:len(wantAuth)]) != wantAuth {
				t.Errorf("auth: got %q, want %q", string(hsrp.Auth[:len(wantAuth)]), wantAuth)
			}

			// Round-trip.
			buf := gopacket.NewSerializeBuffer()
			if err := hsrp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			expectedPayload := extractL3Payload(t, raw, "hsrp")
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}
		})
	}
}

func TestHSRPTruncated(t *testing.T) {
	body := []byte{0x00, 0x01, 0x10, 0x03, 0x0a, 0xff, 0x01, 0x00} // 8 bytes, need 20
	h := &HSRP{}
	err := h.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "HSRP") {
		t.Errorf("error should name protocol HSRP: %v", err)
	}
	if !strings.Contains(err.Error(), "offset") {
		t.Errorf("error should name offset: %v", err)
	}
}

func TestGLBPDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "glbp.pcap")
	golden := readGolden(t, "glbp.json")

	frames := golden["frames"].([]any)
	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			glbp := mustGetLayer[*GLBP](t, p, LayerTypeGLBP)

			frame := frames[i].(map[string]any)

			if glbp.Version != uint8(frame["version"].(float64)) {
				t.Errorf("version: got %d, want %d", glbp.Version, uint8(frame["version"].(float64)))
			}
			if glbp.Group != uint16(frame["group"].(float64)) {
				t.Errorf("group: got %d, want %d", glbp.Group, uint16(frame["group"].(float64)))
			}
			wantOwnerMAC, _ := net.ParseMAC(frame["owner_mac"].(string))
			if !hardwareAddrEqual(glbp.OwnerMAC, wantOwnerMAC) {
				t.Errorf("owner_mac: got %x, want %x", glbp.OwnerMAC, wantOwnerMAC)
			}

			if len(glbp.TLVs) != 1 {
				t.Fatalf("TLV count: got %d, want 1", len(glbp.TLVs))
			}
			hello := glbp.TLVs[0].Hello
			if hello == nil {
				t.Fatal("Hello TLV: got nil")
			}
			if hello.HelloTime != uint32(frame["hello_time"].(float64)) {
				t.Errorf("hello_time: got %d, want %d", hello.HelloTime, uint32(frame["hello_time"].(float64)))
			}
			if hello.HoldTime != uint32(frame["hold_time"].(float64)) {
				t.Errorf("hold_time: got %d, want %d", hello.HoldTime, uint32(frame["hold_time"].(float64)))
			}
			if hello.Priority != uint8(frame["priority"].(float64)) {
				t.Errorf("priority: got %d, want %d", hello.Priority, uint8(frame["priority"].(float64)))
			}
			if hello.State != GLBPState(frame["state"].(float64)) {
				t.Errorf("state: got %d, want %d", hello.State, GLBPState(frame["state"].(float64)))
			}
			wantAddress := net.ParseIP(frame["virtual_address"].(string))
			if !net.IP(hello.VirtualAddress).Equal(wantAddress) {
				t.Errorf("virtual_address: got %s, want %s", net.IP(hello.VirtualAddress), wantAddress)
			}

			// Round-trip.
			buf := gopacket.NewSerializeBuffer()
			if err := glbp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			expectedPayload := extractL3Payload(t, raw, "glbp")
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}
		})
	}
}

func TestGLBPTruncated(t *testing.T) {
	body := []byte{0x01, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x11, 0x22, 0x33, 0x44} // 11 bytes, need 12
	g := &GLBP{}
	err := g.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "GLBP") {
		t.Errorf("error should name protocol GLBP: %v", err)
	}
}

func TestGLBPKnownBytesWiresharkLayout(t *testing.T) {
	// packet-glbp.c at 1dbb8baf9c5bb2e9501b15cce98cea6a3c0f41a3,
	// lines 137-218 and 289-365, is independent of this codec.
	known := []byte{
		0x01, 0x00, 0x00, 0x01, 0x00, 0x00,
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
		0x01, 0x1c, 0x00, 0x20, 0x00, 0xff, 0x00, 0x00,
		0x00, 0x00, 0x0b, 0xb8, 0x00, 0x00, 0x27, 0x10,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x04,
		0x0a, 0x00, 0x00, 0x01,
	}

	glbp := &GLBP{}
	if err := glbp.DecodeFromBytes(known, &testDecodeFeedback{}); err != nil {
		t.Fatalf("DecodeFromBytes: %v", err)
	}
	if glbp.Version != 1 {
		t.Errorf("version: got %d, want 1", glbp.Version)
	}
	if glbp.Unknown1 != 0 {
		t.Errorf("unknown1: got %d, want 0", glbp.Unknown1)
	}
	if glbp.Group != 1 {
		t.Errorf("group: got %d, want 1", glbp.Group)
	}
	if glbp.Unknown2 != 0 {
		t.Errorf("unknown2: got %d, want 0", glbp.Unknown2)
	}
	wantOwnerMAC := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	if !bytes.Equal(glbp.OwnerMAC, wantOwnerMAC) {
		t.Errorf("owner MAC: got %x, want %x", glbp.OwnerMAC, wantOwnerMAC)
	}
	if len(glbp.TLVs) != 1 {
		t.Fatalf("TLV count: got %d, want 1", len(glbp.TLVs))
	}
	tlv := glbp.TLVs[0]
	if tlv.Type != 1 {
		t.Errorf("TLV type: got %d, want 1", tlv.Type)
	}
	if tlv.Length != 28 {
		t.Errorf("TLV length: got %d, want 28", tlv.Length)
	}
	if tlv.Hello == nil {
		t.Fatal("Hello TLV: got nil")
	}
	hello := tlv.Hello
	if hello.Unknown10 != 0 {
		t.Errorf("Hello unknown10: got %d, want 0", hello.Unknown10)
	}
	if hello.State != GLBPStateActive {
		t.Errorf("Hello state: got %d, want %d", hello.State, GLBPStateActive)
	}
	if hello.Unknown11 != 0 {
		t.Errorf("Hello unknown11: got %d, want 0", hello.Unknown11)
	}
	if hello.Priority != 255 {
		t.Errorf("Hello priority: got %d, want 255", hello.Priority)
	}
	if hello.Unknown12 != 0 {
		t.Errorf("Hello unknown12: got %d, want 0", hello.Unknown12)
	}
	if hello.HelloTime != 3000 {
		t.Errorf("Hello interval: got %d, want 3000", hello.HelloTime)
	}
	if hello.HoldTime != 10000 {
		t.Errorf("Hold interval: got %d, want 10000", hello.HoldTime)
	}
	if hello.RedirectTime != 0 {
		t.Errorf("Redirect time: got %d, want 0", hello.RedirectTime)
	}
	if hello.Timeout != 0 {
		t.Errorf("Timeout: got %d, want 0", hello.Timeout)
	}
	if hello.Unknown13 != 0 {
		t.Errorf("Hello unknown13: got %d, want 0", hello.Unknown13)
	}
	if hello.AddressType != 1 {
		t.Errorf("Address type: got %d, want 1", hello.AddressType)
	}
	if hello.AddressLength != 4 {
		t.Errorf("Address length: got %d, want 4", hello.AddressLength)
	}
	wantAddress := []byte{10, 0, 0, 1}
	if !bytes.Equal(hello.VirtualAddress, wantAddress) {
		t.Errorf("Virtual address: got %v, want %v", hello.VirtualAddress, wantAddress)
	}

	buf := gopacket.NewSerializeBuffer()
	if err := glbp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
		t.Fatalf("SerializeTo: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), known) {
		t.Errorf("encoded bytes:\n  got  %x\n  want %x", buf.Bytes(), known)
	}
}

func TestGLBPRejectsTLVLengthBelowHeader(t *testing.T) {
	body := []byte{
		0x01, 0x00, 0x00, 0x01, 0x00, 0x00,
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
		0x01, 0x01,
	}

	glbp := &GLBP{}
	err := glbp.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("DecodeFromBytes: got nil, want malformed TLV length error")
	}
	if !strings.Contains(err.Error(), "length 1 < 2") {
		t.Errorf("DecodeFromBytes error: got %q, want TLV length below 2", err)
	}
}

func TestGLBPUnknownTLVPassThrough(t *testing.T) {
	body := []byte{
		0x01, 0x00, 0x00, 0x01, 0x00, 0x00,
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
		0xff, 0x04, 0xaa, 0x55,
	}

	g := &GLBP{}
	if err := g.DecodeFromBytes(body, &testDecodeFeedback{}); err != nil {
		t.Fatalf("DecodeFromBytes with unknown TLV: %v", err)
	}
	if len(g.TLVs) != 1 {
		t.Fatalf("TLV count: got %d, want 1", len(g.TLVs))
	}
	if g.TLVs[0].Type != 0xff {
		t.Errorf("unknown TLV type: got 0x%02x, want 0xff", g.TLVs[0].Type)
	}
	if !bytes.Equal(g.TLVs[0].Value, []byte{0xaa, 0x55}) {
		t.Errorf("unknown TLV value: got %x, want aa55", g.TLVs[0].Value)
	}
}

func TestEIGRPDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "eigrp.pcap")
	golden := readGolden(t, "eigrp.json")

	frames := golden["frames"].([]any)
	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			eigrp := mustGetLayer[*EIGRP](t, p, LayerTypeEIGRP)

			frame := frames[i].(map[string]any)

			if eigrp.Version != uint8(frame["version"].(float64)) {
				t.Errorf("version: got %d, want %d", eigrp.Version, uint8(frame["version"].(float64)))
			}
			if eigrp.Opcode != EIGRPOpcode(frame["opcode"].(float64)) {
				t.Errorf("opcode: got %d, want %d", eigrp.Opcode, EIGRPOpcode(frame["opcode"].(float64)))
			}
			if eigrp.Flags != uint32(frame["flags"].(float64)) {
				t.Errorf("flags: got %d, want %d", eigrp.Flags, uint32(frame["flags"].(float64)))
			}
			if eigrp.SequenceNumber != uint32(frame["seq"].(float64)) {
				t.Errorf("seq: got %d, want %d", eigrp.SequenceNumber, uint32(frame["seq"].(float64)))
			}
			if eigrp.AckNumber != uint32(frame["ack"].(float64)) {
				t.Errorf("ack: got %d, want %d", eigrp.AckNumber, uint32(frame["ack"].(float64)))
			}
			if eigrp.VirtualRouterID != uint16(frame["vrid"].(float64)) {
				t.Errorf("vrid: got %d, want %d", eigrp.VirtualRouterID, uint16(frame["vrid"].(float64)))
			}
			if eigrp.AutonomousSystem != uint16(frame["as"].(float64)) {
				t.Errorf("as: got %d, want %d", eigrp.AutonomousSystem, uint16(frame["as"].(float64)))
			}

			// Verify TLVs.
			tlvs := frame["tlvs"].([]any)
			if len(eigrp.TLVs) != len(tlvs) {
				t.Fatalf("TLV count: got %d, want %d", len(eigrp.TLVs), len(tlvs))
			}
			for j, wantTLV := range tlvs {
				wt := wantTLV.(map[string]any)
				if eigrp.TLVs[j].Type != uint16(wt["type"].(float64)) {
					t.Errorf("TLV[%d] type: got %d, want %d", j, eigrp.TLVs[j].Type, uint16(wt["type"].(float64)))
				}
				if eigrp.TLVs[j].Length != uint16(wt["length"].(float64)) {
					t.Errorf("TLV[%d] length: got %d, want %d", j, eigrp.TLVs[j].Length, uint16(wt["length"].(float64)))
				}
			}

			// Round-trip.
			buf := gopacket.NewSerializeBuffer()
			if err := eigrp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			expectedPayload := extractL3Payload(t, raw, "eigrp")
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}
		})
	}
}

func TestEIGRPMissingTLVTailDecline(t *testing.T) {
	// A header-only EIGRP packet (no TLV tail) must decode successfully —
	// the header fields are populated and the TLV slice stays empty.
	pkts := readPcap(t, "eigrp_no_tlv.pcap")
	p := decodePacket(t, pkts[0])
	eigrp := mustGetLayer[*EIGRP](t, p, LayerTypeEIGRP)

	if eigrp.Version != 2 {
		t.Errorf("version: got %d, want 2", eigrp.Version)
	}
	if eigrp.Opcode != EIGRPOpcodeHello {
		t.Errorf("opcode: got %d, want %d (hello)", eigrp.Opcode, EIGRPOpcodeHello)
	}
	if eigrp.AutonomousSystem != 1 {
		t.Errorf("as: got %d, want 1", eigrp.AutonomousSystem)
	}
	if len(eigrp.TLVs) != 0 {
		t.Errorf("TLV count: got %d, want 0 (header-only packet)", len(eigrp.TLVs))
	}
}

func TestEIGRPTruncated(t *testing.T) {
	body := []byte{0x02, 0x05, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // 10 bytes, need 20
	e := &EIGRP{}
	err := e.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "EIGRP") {
		t.Errorf("error should name protocol EIGRP: %v", err)
	}
}

func TestLLMNRDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "llmnr.pcap")
	golden := readGolden(t, "llmnr.json")

	frames := golden["frames"].([]any)
	if len(pkts) != len(frames) {
		t.Fatalf("llmnr.pcap has %d packets, golden has %d", len(pkts), len(frames))
	}

	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			llmnr := mustGetLayer[*LLMNR](t, p, LayerTypeLLMNR)

			frame := frames[i].(map[string]any)

			if llmnr.ID != uint16(frame["id"].(float64)) {
				t.Errorf("id: got %d, want %d", llmnr.ID, uint16(frame["id"].(float64)))
			}
			wantQR := uint16(frame["qr"].(float64))
			if llmnr.QR() != (wantQR == 1) {
				t.Errorf("qr: got %v, want %v", llmnr.QR(), wantQR == 1)
			}
			if llmnr.QDCount != uint16(frame["qdcount"].(float64)) {
				t.Errorf("qdcount: got %d, want %d", llmnr.QDCount, uint16(frame["qdcount"].(float64)))
			}

			// Questions.
			questions := frame["questions"].([]any)
			if len(llmnr.Questions) != len(questions) {
				t.Fatalf("question count: got %d, want %d", len(llmnr.Questions), len(questions))
			}
			for j, q := range questions {
				qm := q.(map[string]any)
				if llmnr.Questions[j].Name != qm["name"].(string) {
					t.Errorf("question[%d] name: got %q, want %q", j, llmnr.Questions[j].Name, qm["name"].(string))
				}
				if llmnr.Questions[j].QType != uint16(qm["qtype"].(float64)) {
					t.Errorf("question[%d] qtype: got %d, want %d", j, llmnr.Questions[j].QType, uint16(qm["qtype"].(float64)))
				}
			}

			// Answers (response frames only).
			if answers, ok := frame["answers"].([]any); ok {
				if len(llmnr.Answers) != len(answers) {
					t.Fatalf("answer count: got %d, want %d", len(llmnr.Answers), len(answers))
				}
				for j, a := range answers {
					am := a.(map[string]any)
					if llmnr.Answers[j].Name != am["name"].(string) {
						t.Errorf("answer[%d] name: got %q, want %q", j, llmnr.Answers[j].Name, am["name"].(string))
					}
					if llmnr.Answers[j].Type != uint16(am["type"].(float64)) {
						t.Errorf("answer[%d] type: got %d, want %d", j, llmnr.Answers[j].Type, uint16(am["type"].(float64)))
					}
					if llmnr.Answers[j].TTL != uint32(am["ttl"].(float64)) {
						t.Errorf("answer[%d] ttl: got %d, want %d", j, llmnr.Answers[j].TTL, uint32(am["ttl"].(float64)))
					}
					wantRdata := net.ParseIP(am["rdata"].(string))
					if !llmnr.Answers[j].DataEqual(wantRdata) {
						t.Errorf("answer[%d] rdata: got %v, want %v", j, llmnr.Answers[j].Data, wantRdata)
					}
				}
			}

			// Round-trip: queries (no compression) are byte-for-byte;
			// responses with compression pointers are re-serialized
			// with literal names (the serializer does not compress), so
			// we verify the re-serialized frame decodes to the same
			// typed fields instead of comparing bytes.
			buf := gopacket.NewSerializeBuffer()
			if err := llmnr.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			frameName := frame["name"].(string)
			if frameName == "query" {
				expectedPayload := extractL3Payload(t, raw, "llmnr")
				if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
					t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
				}
			} else {
				// Re-decode the serialized bytes and verify field equivalence.
				reDecoded := &LLMNR{}
				if err := reDecoded.DecodeFromBytes(buf.Bytes(), &testDecodeFeedback{}); err != nil {
					t.Fatalf("re-decode serialized LLMNR: %v", err)
				}
				if reDecoded.ID != llmnr.ID {
					t.Errorf("re-decoded id: got %d, want %d", reDecoded.ID, llmnr.ID)
				}
				if reDecoded.QDCount != llmnr.QDCount {
					t.Errorf("re-decoded qdcount: got %d, want %d", reDecoded.QDCount, llmnr.QDCount)
				}
				if len(reDecoded.Questions) != len(llmnr.Questions) {
					t.Fatalf("re-decoded questions: got %d, want %d", len(reDecoded.Questions), len(llmnr.Questions))
				}
				for j := range reDecoded.Questions {
					if reDecoded.Questions[j].Name != llmnr.Questions[j].Name {
						t.Errorf("re-decoded question[%d] name: got %q, want %q",
							j, reDecoded.Questions[j].Name, llmnr.Questions[j].Name)
					}
				}
				if len(reDecoded.Answers) != len(llmnr.Answers) {
					t.Fatalf("re-decoded answers: got %d, want %d", len(reDecoded.Answers), len(llmnr.Answers))
				}
				for j := range reDecoded.Answers {
					if reDecoded.Answers[j].Name != llmnr.Answers[j].Name {
						t.Errorf("re-decoded answer[%d] name: got %q, want %q",
							j, reDecoded.Answers[j].Name, llmnr.Answers[j].Name)
					}
					if reDecoded.Answers[j].TTL != llmnr.Answers[j].TTL {
						t.Errorf("re-decoded answer[%d] ttl: got %d, want %d",
							j, reDecoded.Answers[j].TTL, llmnr.Answers[j].TTL)
					}
				}
			}
		})
	}
}

func TestLLMNRTruncated(t *testing.T) {
	body := []byte{0x12, 0x34, 0x00, 0x00, 0x00, 0x01} // 6 bytes, need 12
	l := &LLMNR{}
	err := l.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "LLMNR") {
		t.Errorf("error should name protocol LLMNR: %v", err)
	}
}

func TestNBTNSDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "nbns.pcap")
	golden := readGolden(t, "nbns.json")

	frames := golden["frames"].([]any)
	if len(pkts) != len(frames) {
		t.Fatalf("nbns.pcap has %d packets, golden has %d", len(pkts), len(frames))
	}

	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			nbns := mustGetLayer[*NBNS](t, p, LayerTypeNBTNS)

			frame := frames[i].(map[string]any)

			if nbns.ID != uint16(frame["id"].(float64)) {
				t.Errorf("id: got %d, want %d", nbns.ID, uint16(frame["id"].(float64)))
			}
			if nbns.Flags != uint16(frame["flags"].(float64)) {
				t.Errorf("flags: got 0x%04x, want 0x%04x", nbns.Flags, uint16(frame["flags"].(float64)))
			}

			// Questions.
			if questions, ok := frame["questions"].([]any); ok {
				if len(nbns.Questions) != len(questions) {
					t.Fatalf("question count: got %d, want %d", len(nbns.Questions), len(questions))
				}
				for j, q := range questions {
					qm := q.(map[string]any)
					if nbns.Questions[j].Name != qm["name"].(string) {
						t.Errorf("question[%d] name: got %q, want %q", j, nbns.Questions[j].Name, qm["name"].(string))
					}
					if nbns.Questions[j].QType != uint16(qm["qtype"].(float64)) {
						t.Errorf("question[%d] qtype: got %d, want %d", j, nbns.Questions[j].QType, uint16(qm["qtype"].(float64)))
					}
				}
			}

			// Answers.
			if answers, ok := frame["answers"].([]any); ok {
				if len(nbns.Answers) != len(answers) {
					t.Fatalf("answer count: got %d, want %d", len(nbns.Answers), len(answers))
				}
				for j, a := range answers {
					am := a.(map[string]any)
					if nbns.Answers[j].Name != am["name"].(string) {
						t.Errorf("answer[%d] name: got %q, want %q", j, nbns.Answers[j].Name, am["name"].(string))
					}
					if nbns.Answers[j].Type != uint16(am["type"].(float64)) {
						t.Errorf("answer[%d] type: got %d, want %d", j, nbns.Answers[j].Type, uint16(am["type"].(float64)))
					}
					if nbns.Answers[j].TTL != uint32(am["ttl"].(float64)) {
						t.Errorf("answer[%d] ttl: got %d, want %d", j, nbns.Answers[j].TTL, uint32(am["ttl"].(float64)))
					}
				}
			}

			// Round-trip.
			buf := gopacket.NewSerializeBuffer()
			if err := nbns.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			expectedPayload := extractL3Payload(t, raw, "nbns")
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}
		})
	}
}

func TestNBTNSTruncated(t *testing.T) {
	body := []byte{0x56, 0x78, 0x00, 0x10, 0x00, 0x01} // 6 bytes, need 12
	n := &NBNS{}
	err := n.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "NBT-NS") {
		t.Errorf("error should name protocol NBT-NS: %v", err)
	}
}

// Compression-loop adversarial corpus fixtures are accreted with provenance.

func TestCorpusLLMNRCompressionLoop(t *testing.T) {
	pkts := readPcap(t, "llmnr_loop.pcap")
	p := decodePacket(t, pkts[0])
	errLayer := p.ErrorLayer()
	if errLayer == nil {
		t.Fatal("expected decoding error on LLMNR compression-loop frame")
	}
	if !strings.Contains(errLayer.Error().Error(), "LLMNR") {
		t.Errorf("error should name protocol LLMNR: %v", errLayer.Error())
	}
	if !strings.Contains(errLayer.Error().Error(), "loop") {
		t.Errorf("error should report compression loop: %v", errLayer.Error())
	}
}

func TestCorpusNBTNSCompressionLoop(t *testing.T) {
	pkts := readPcap(t, "nbns_loop.pcap")
	p := decodePacket(t, pkts[0])
	errLayer := p.ErrorLayer()
	if errLayer == nil {
		t.Fatal("expected decoding error on NBT-NS compression-loop frame")
	}
	if !strings.Contains(errLayer.Error().Error(), "NBT-NS") {
		t.Errorf("error should name protocol NBT-NS: %v", errLayer.Error())
	}
	if !strings.Contains(errLayer.Error().Error(), "loop") {
		t.Errorf("error should report compression loop: %v", errLayer.Error())
	}
}

func TestL3EndToEndDispatch(t *testing.T) {
	t.Run("HSRP_hello", func(t *testing.T) {
		pkts := readPcap(t, "hsrp.pcap")
		p := decodePacket(t, pkts[0])
		hsrp := mustGetLayer[*HSRP](t, p, LayerTypeHSRP)

		if p.Layer(layers.LayerTypeEthernet) == nil {
			t.Error("Ethernet layer missing")
		}
		if p.Layer(layers.LayerTypeIPv4) == nil {
			t.Error("IPv4 layer missing")
		}
		if p.Layer(layers.LayerTypeUDP) == nil {
			t.Error("UDP layer missing")
		}
		if hsrp.Opcode != HSRPOpcodeHello {
			t.Errorf("opcode: got %d, want %d (hello)", hsrp.Opcode, HSRPOpcodeHello)
		}
		if hsrp.Priority != 255 {
			t.Errorf("priority: got %d, want 255", hsrp.Priority)
		}
		if hsrp.Group != 1 {
			t.Errorf("group: got %d, want 1", hsrp.Group)
		}
	})

	t.Run("HSRP_coup", func(t *testing.T) {
		pkts := readPcap(t, "hsrp.pcap")
		p := decodePacket(t, pkts[1])
		hsrp := mustGetLayer[*HSRP](t, p, LayerTypeHSRP)

		if hsrp.Opcode != HSRPOpcodeCoup {
			t.Errorf("opcode: got %d, want %d (coup)", hsrp.Opcode, HSRPOpcodeCoup)
		}
	})

	t.Run("GLBP_hello", func(t *testing.T) {
		pkts := readPcap(t, "glbp.pcap")
		p := decodePacket(t, pkts[0])
		glbp := mustGetLayer[*GLBP](t, p, LayerTypeGLBP)

		if p.Layer(layers.LayerTypeEthernet) == nil {
			t.Error("Ethernet layer missing")
		}
		if p.Layer(layers.LayerTypeIPv4) == nil {
			t.Error("IPv4 layer missing")
		}
		if p.Layer(layers.LayerTypeUDP) == nil {
			t.Error("UDP layer missing")
		}
		if len(glbp.TLVs) != 1 || glbp.TLVs[0].Hello == nil {
			t.Fatalf("Hello TLV: got %#v", glbp.TLVs)
		}
		if glbp.TLVs[0].Hello.State != GLBPStateActive {
			t.Errorf("state: got %d, want %d", glbp.TLVs[0].Hello.State, GLBPStateActive)
		}
	})

	t.Run("EIGRP_hello", func(t *testing.T) {
		pkts := readPcap(t, "eigrp.pcap")
		p := decodePacket(t, pkts[0])
		eigrp := mustGetLayer[*EIGRP](t, p, LayerTypeEIGRP)

		if p.Layer(layers.LayerTypeEthernet) == nil {
			t.Error("Ethernet layer missing")
		}
		if p.Layer(layers.LayerTypeIPv4) == nil {
			t.Error("IPv4 layer missing")
		}
		if eigrp.Opcode != EIGRPOpcodeHello {
			t.Errorf("opcode: got %d, want %d (hello)", eigrp.Opcode, EIGRPOpcodeHello)
		}
		if eigrp.AutonomousSystem != 1 {
			t.Errorf("as: got %d, want 1", eigrp.AutonomousSystem)
		}
	})

	t.Run("LLMNR_query", func(t *testing.T) {
		pkts := readPcap(t, "llmnr.pcap")
		p := decodePacket(t, pkts[0])
		llmnr := mustGetLayer[*LLMNR](t, p, LayerTypeLLMNR)

		if p.Layer(layers.LayerTypeUDP) == nil {
			t.Error("UDP layer missing")
		}
		if llmnr.QR() {
			t.Error("query should have QR=false")
		}
		if len(llmnr.Questions) != 1 {
			t.Fatalf("questions: got %d, want 1", len(llmnr.Questions))
		}
		if llmnr.Questions[0].Name != "host.lab" {
			t.Errorf("question name: got %q, want %q", llmnr.Questions[0].Name, "host.lab")
		}
	})

	t.Run("NBTNS_query", func(t *testing.T) {
		pkts := readPcap(t, "nbns.pcap")
		p := decodePacket(t, pkts[0])
		nbns := mustGetLayer[*NBNS](t, p, LayerTypeNBTNS)

		if p.Layer(layers.LayerTypeUDP) == nil {
			t.Error("UDP layer missing")
		}
		if len(nbns.Questions) != 1 {
			t.Fatalf("questions: got %d, want 1", len(nbns.Questions))
		}
		if nbns.Questions[0].Name != "WORKSTATION" {
			t.Errorf("question name: got %q, want %q", nbns.Questions[0].Name, "WORKSTATION")
		}
	})
}

// Compile-time interface assertions.

var (
	_ gopacket.DecodingLayer = (*HSRP)(nil)
	_ gopacket.DecodingLayer = (*GLBP)(nil)
	_ gopacket.DecodingLayer = (*EIGRP)(nil)
	_ gopacket.DecodingLayer = (*LLMNR)(nil)
	_ gopacket.DecodingLayer = (*NBNS)(nil)

	_ gopacket.SerializableLayer = (*HSRP)(nil)
	_ gopacket.SerializableLayer = (*GLBP)(nil)
	_ gopacket.SerializableLayer = (*EIGRP)(nil)
	_ gopacket.SerializableLayer = (*LLMNR)(nil)
	_ gopacket.SerializableLayer = (*NBNS)(nil)
)

// extractL3Payload extracts the protocol payload bytes from a raw L3 frame:
// for UDP-encapsulated protocols (HSRP, GLBP, LLMNR, NBT-NS), it strips
// Ethernet(14) + IPv4(20) + UDP(8); for IP-protocol protocols (EIGRP), it
// strips Ethernet(14) + IPv4(20).
func extractL3Payload(t *testing.T, raw []byte, proto string) []byte {
	t.Helper()
	if len(raw) < 14 {
		t.Fatalf("frame too short: %d bytes", len(raw))
	}

	switch proto {
	case "hsrp", "glbp", "llmnr", "nbns":
		// Ethernet(14) + IPv4(20) + UDP(8) = 42
		offset := 42
		if offset > len(raw) {
			t.Fatalf("frame too short for %s payload: %d bytes", proto, len(raw))
		}
		return raw[offset:]
	case "eigrp":
		// Ethernet(14) + IPv4(20) = 34
		offset := 34
		if offset > len(raw) {
			t.Fatalf("frame too short for %s payload: %d bytes", proto, len(raw))
		}
		return raw[offset:]
	default:
		t.Fatalf("unknown protocol %s", proto)
		return nil
	}
}

// DataEqual compares the answer rdata against a parsed IP. This is a helper
// used by LLMNR tests to compare rdata bytes to expected IP addresses.
func (r *LLMNRResourceRecord) DataEqual(ip net.IP) bool {
	if len(r.Data) != 4 {
		return false
	}
	return net.IP(r.Data).Equal(ip)
}
