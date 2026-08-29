// Tests for netpen's owned L2 decoders. Each protocol is pinned against pcap
// fixtures harvested from the Python tool's byte construction:
// byte-for-byte where deterministic, field-set where randomized. Malformed
// and adversarial frames carry provenance notes in the corpus, never deleted.

package layers

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
)

// testdataDir is the fixture directory, resolved relative to this test file.
var testdataDir string

func init() {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	testdataDir = filepath.Join(wd, "testdata")
}

// readPcap reads all packets from a pcap file in testdata/.
func readPcap(t *testing.T, name string) [][]byte {
	t.Helper()
	f, err := os.Open(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer func() { _ = f.Close() }()

	r, err := pcapgo.NewReader(f)
	if err != nil {
		t.Fatalf("new pcap reader %s: %v", name, err)
	}

	var pkts [][]byte
	for {
		data, _, err := r.ReadPacketData()
		if err != nil {
			break
		}
		pkts = append(pkts, data)
	}
	if len(pkts) == 0 {
		t.Fatalf("no packets in %s", name)
	}
	return pkts
}

// readGolden reads a JSON golden file from testdata/.
func readGolden(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataDir, name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal golden %s: %v", name, err)
	}
	return m
}

// decodePacket runs gopacket's full packet decoder and returns the packet.
func decodePacket(t *testing.T, raw []byte) gopacket.Packet {
	t.Helper()
	p := gopacket.NewPacket(raw, layers.LayerTypeEthernet, gopacket.Default)
	if p == nil {
		t.Fatal("NewPacket returned nil")
	}
	return p
}

// mustGetLayer extracts a layer from a decoded packet, failing the test if
// absent.
func mustGetLayer[T any](t *testing.T, p gopacket.Packet, lt gopacket.LayerType) T {
	t.Helper()
	l := p.Layer(lt)
	if l == nil {
		t.Fatalf("layer %s not found in packet; layers: %v", lt, p.Layers())
	}
	got, ok := l.(T)
	if !ok {
		t.Fatalf("layer %s is %T, not %T", lt, l, got)
	}
	return got
}

// hardwareAddrEqual compares two hardware addresses by value.
func hardwareAddrEqual(a, b net.HardwareAddr) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDTPDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "dtp.pcap")
	golden := readGolden(t, "dtp.json")

	frames := golden["frames"].([]any)
	if len(pkts) != len(frames) {
		t.Fatalf("dtp.pcap has %d packets, golden has %d", len(pkts), len(frames))
	}

	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d", i), func(t *testing.T) {
			p := decodePacket(t, raw)

			// Verify the dispatch chain: Ethernet → LLC → SNAP → DTP.
			dtp := mustGetLayer[*DTP](t, p, LayerTypeDTP)

			frame := frames[i].(map[string]any)

			// Byte-for-byte round-trip: serialize the DTP layer and compare
			// against the original payload (after SNAP header).
			buf := gopacket.NewSerializeBuffer()
			if err := dtp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}

			// The serialized bytes should match the DTP payload from the fixture.
			expectedPayload := extractProtoPayload(t, raw, "dtp")
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}

			// Typed field pins.
			if dtp.Version != uint8(frame["version"].(float64)) {
				t.Errorf("version: got %d, want %d", dtp.Version, uint8(frame["version"].(float64)))
			}
			wantStatus := uint8(frame["trunk_status"].(float64))
			if dtp.TrunkStatus != wantStatus {
				t.Errorf("trunk_status: got 0x%02x, want 0x%02x", dtp.TrunkStatus, wantStatus)
			}
			wantType := uint8(frame["trunk_type"].(float64))
			if dtp.TrunkType != wantType {
				t.Errorf("trunk_type: got 0x%02x, want 0x%02x", dtp.TrunkType, wantType)
			}
			wantNeighbor, _ := net.ParseMAC(frame["neighbor"].(string))
			if !hardwareAddrEqual(dtp.Neighbor, wantNeighbor) {
				t.Errorf("neighbor: got %x, want %x", dtp.Neighbor, wantNeighbor)
			}
		})
	}
}

func TestDTPNeighborState(t *testing.T) {
	pkts := readPcap(t, "dtp.pcap")

	// Frame 0: desirable (0x03)
	p0 := decodePacket(t, pkts[0])
	dtp0 := mustGetLayer[*DTP](t, p0, LayerTypeDTP)
	if dtp0.NeighborState() != "desirable" {
		t.Errorf("frame 0 state: got %q, want %q", dtp0.NeighborState(), "desirable")
	}

	// Frame 1: trunk (0x81)
	p1 := decodePacket(t, pkts[1])
	dtp1 := mustGetLayer[*DTP](t, p1, LayerTypeDTP)
	if dtp1.NeighborState() != "trunk" {
		t.Errorf("frame 1 state: got %q, want %q", dtp1.NeighborState(), "trunk")
	}
}

func TestDTPUnknownTLVPassThrough(t *testing.T) {
	// Craft a DTP body with a known TLV, an unknown TLV (type 0xFFFF), and
	// another known TLV. The decode must succeed and surface all three.
	body := []byte{0x01} // version
	body = append(body, makeDTPtlv(0x0001, []byte{0x00})...)
	body = append(body, makeDTPtlv(0xFFFF, []byte("unknown"))...)
	body = append(body, makeDTPtlv(0x0002, []byte{0x03})...)

	d := &DTP{}
	if err := d.DecodeFromBytes(body, &testDecodeFeedback{}); err != nil {
		t.Fatalf("DecodeFromBytes with unknown TLV: %v", err)
	}
	if len(d.TLVs) != 3 {
		t.Fatalf("TLV count: got %d, want 3", len(d.TLVs))
	}
	if d.TLVs[1].Type != 0xFFFF {
		t.Errorf("unknown TLV type: got 0x%04x, want 0xFFFF", d.TLVs[1].Type)
	}
}

func TestDTPTruncatedMidTLV(t *testing.T) {
	// A DTP body with a TLV header that claims more data than present.
	body := []byte{0x01}                        // version
	body = append(body, 0x00, 0x01, 0x00, 0xFF) // type=1, length=255 (far beyond available)

	d := &DTP{}
	err := d.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "DTP") {
		t.Errorf("error should name protocol DTP: %v", err)
	}
	if !strings.Contains(err.Error(), "offset") {
		t.Errorf("error should name offset: %v", err)
	}
}

func makeDTPtlv(t uint16, v []byte) []byte {
	out := make([]byte, 4+len(v))
	binary.BigEndian.PutUint16(out[0:2], t)
	binary.BigEndian.PutUint16(out[2:4], uint16(4+len(v)))
	copy(out[4:], v)
	return out
}

func TestVTPDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "vtp.pcap")
	golden := readGolden(t, "vtp.json")

	frames := golden["frames"].([]any)
	if len(pkts) != len(frames) {
		t.Fatalf("vtp.pcap has %d packets, golden has %d", len(pkts), len(frames))
	}

	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			vtp := mustGetLayer[*VTP](t, p, LayerTypeVTP)

			frame := frames[i].(map[string]any)

			// Typed field pins.
			wantDomain := golden["domain"].(string)
			if vtp.Domain != wantDomain {
				t.Errorf("domain: got %q, want %q", vtp.Domain, wantDomain)
			}
			// Revision is only carried in summary and subset advertisements.
			if vtp.Code == VTPCodeSummary || vtp.Code == VTPCodeSubset {
				wantRev := uint32(golden["revision"].(float64))
				if vtp.Revision != wantRev {
					t.Errorf("revision: got %d, want %d", vtp.Revision, wantRev)
				}
			}
			wantCode := uint8(frame["code"].(float64))
			if uint8(vtp.Code) != wantCode {
				t.Errorf("code: got 0x%02x, want 0x%02x", vtp.Code, wantCode)
			}

			// Round-trip: serialize and compare against fixture payload.
			buf := gopacket.NewSerializeBuffer()
			if err := vtp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			expectedPayload := extractProtoPayload(t, raw, "vtp")
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}
		})
	}
}

func TestVTPSubsetVLANs(t *testing.T) {
	pkts := readPcap(t, "vtp.pcap")
	// Frame 1 is the subset advertisement.
	p := decodePacket(t, pkts[1])
	vtp := mustGetLayer[*VTP](t, p, LayerTypeVTP)

	if vtp.Code != VTPCodeSubset {
		t.Fatalf("code: got 0x%02x, want 0x%02x (subset)", vtp.Code, VTPCodeSubset)
	}
	if len(vtp.VLANs) != 2 {
		t.Fatalf("VLAN count: got %d, want 2", len(vtp.VLANs))
	}
	wantVLANs := []struct {
		id   uint16
		name string
	}{{10, "data"}, {20, "voice"}}
	for i, want := range wantVLANs {
		if vtp.VLANs[i].VLANID != want.id {
			t.Errorf("VLAN[%d] id: got %d, want %d", i, vtp.VLANs[i].VLANID, want.id)
		}
		if vtp.VLANs[i].Name != want.name {
			t.Errorf("VLAN[%d] name: got %q, want %q", i, vtp.VLANs[i].Name, want.name)
		}
	}
}

func TestVTPTruncated(t *testing.T) {
	// A VTP summary body truncated before the revision field.
	body := []byte{0x01, 0x01, 0x00, 0x09}     // version, code, followers, domainLen=9
	body = append(body, []byte("LABDOMAI")...) // 8 bytes, but domainLen says 9

	v := &VTP{}
	err := v.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "VTP") {
		t.Errorf("error should name protocol VTP: %v", err)
	}
}

func TestVTPTruncatedUpdater(t *testing.T) {
	// A 42-byte summary body: revision present, updater field cut short.
	// Decode must return a structured error instead of panicking.
	body := make([]byte, 0, 42)
	body = append(body, 0x01, 0x01, 0x00, 0x00) // version, code, seq, domainLen
	body = append(body, make([]byte, 32)...)    // zero-padded domain
	body = append(body, 0x00, 0x00, 0x00, 0x2A) // revision
	body = append(body, 0x0A, 0x00)             // updater cut at 2 of 4 bytes

	v := &VTP{}
	err := v.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "updater") {
		t.Errorf("error should name the truncated field: %v", err)
	}
}

func TestMVRPDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "mvrp.pcap")
	golden := readGolden(t, "mvrp.json")

	frames := golden["frames"].([]any)
	if len(pkts) != len(frames) {
		t.Fatalf("mvrp.pcap has %d packets, golden has %d", len(pkts), len(frames))
	}

	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			mvrp := mustGetLayer[*MVRP](t, p, LayerTypeMVRP)

			frame := frames[i].(map[string]any)
			messages := frame["messages"].([]any)
			msg := messages[0].(map[string]any)

			if mvrp.Version != uint8(frame["version"].(float64)) {
				t.Errorf("version: got %d, want %d", mvrp.Version, uint8(frame["version"].(float64)))
			}
			if len(mvrp.Messages) != 1 {
				t.Fatalf("message count: got %d, want 1", len(mvrp.Messages))
			}
			m := mvrp.Messages[0]
			wantVID := uint16(msg["first_value"].(float64))
			if m.FirstValue != wantVID {
				t.Errorf("first_value (VID): got %d, want %d", m.FirstValue, wantVID)
			}
			wantEvent := msg["event"].(string)
			if m.Events[0].String() != wantEvent {
				t.Errorf("event: got %q, want %q", m.Events[0].String(), wantEvent)
			}

			// Round-trip.
			buf := gopacket.NewSerializeBuffer()
			if err := mvrp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			// MVRP payload is after the 14-byte Ethernet header.
			expectedPayload := raw[14:]
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}
		})
	}
}

func TestMVRPTruncated(t *testing.T) {
	body := []byte{0x00, 0x01, 0x04} // version, attr_type, attr_len — missing vector header
	m := &MVRP{}
	err := m.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "MVRP") {
		t.Errorf("error should name protocol MVRP: %v", err)
	}
}

func TestPAgPDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "pagp.pcap")
	golden := readGolden(t, "pagp.json")

	frames := golden["frames"].([]any)
	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			pagp := mustGetLayer[*PAgP](t, p, LayerTypePAgP)

			frame := frames[i].(map[string]any)

			if pagp.Version != uint8(frame["version"].(float64)) {
				t.Errorf("version: got %d, want %d", pagp.Version, uint8(frame["version"].(float64)))
			}
			if pagp.Command != PAgPCommand(frame["command"].(float64)) {
				t.Errorf("command: got %d, want %d", pagp.Command, PAgPCommand(frame["command"].(float64)))
			}
			wantLocal, _ := net.ParseMAC(frame["local_device_id"].(string))
			if !hardwareAddrEqual(pagp.LocalDeviceID, wantLocal) {
				t.Errorf("local_device_id: got %x, want %x", pagp.LocalDeviceID, wantLocal)
			}
			wantPartner, _ := net.ParseMAC(frame["partner_device_id"].(string))
			if !hardwareAddrEqual(pagp.PartnerDeviceID, wantPartner) {
				t.Errorf("partner_device_id: got %x, want %x", pagp.PartnerDeviceID, wantPartner)
			}

			// Round-trip.
			buf := gopacket.NewSerializeBuffer()
			if err := pagp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			expectedPayload := extractProtoPayload(t, raw, "pagp")
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}
		})
	}
}

func TestPAgPTruncated(t *testing.T) {
	body := []byte{0x01, 0x01, 0x01, 0x01, 0x00, 0x11} // too short
	p := &PAgP{}
	err := p.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "PAgP") {
		t.Errorf("error should name protocol PAgP: %v", err)
	}
}

func TestLACPDecodeAndRoundTrip(t *testing.T) {
	pkts := readPcap(t, "lacp.pcap")
	golden := readGolden(t, "lacp.json")

	frames := golden["frames"].([]any)
	for i, raw := range pkts {
		t.Run(fmt.Sprintf("frame_%d_%s", i, frames[i].(map[string]any)["name"]), func(t *testing.T) {
			p := decodePacket(t, raw)
			lacp := mustGetLayer[*LACP](t, p, LayerTypeLACP)

			frame := frames[i].(map[string]any)

			if lacp.Subtype != uint8(golden["subtype"].(float64)) {
				t.Errorf("subtype: got %d, want %d", lacp.Subtype, uint8(golden["subtype"].(float64)))
			}
			if lacp.Version != uint8(golden["version"].(float64)) {
				t.Errorf("version: got %d, want %d", lacp.Version, uint8(golden["version"].(float64)))
			}

			actor := frame["actor"].(map[string]any)
			wantActorSys, _ := net.ParseMAC(actor["system"].(string))
			if !hardwareAddrEqual(lacp.Actor.System, wantActorSys) {
				t.Errorf("actor system: got %x, want %x", lacp.Actor.System, wantActorSys)
			}
			wantActorKey := uint16(actor["key"].(float64))
			if lacp.Actor.Key != wantActorKey {
				t.Errorf("actor key: got %d, want %d", lacp.Actor.Key, wantActorKey)
			}
			wantActorPort := uint16(actor["port"].(float64))
			if lacp.Actor.Port != wantActorPort {
				t.Errorf("actor port: got %d, want %d", lacp.Actor.Port, wantActorPort)
			}
			wantActorState := LACPActorState(actor["state"].(float64))
			if lacp.Actor.State != wantActorState {
				t.Errorf("actor state: got 0x%02x, want 0x%02x", lacp.Actor.State, wantActorState)
			}

			partner := frame["partner"].(map[string]any)
			wantPartnerSys, _ := net.ParseMAC(partner["system"].(string))
			if !hardwareAddrEqual(lacp.Partner.System, wantPartnerSys) {
				t.Errorf("partner system: got %x, want %x", lacp.Partner.System, wantPartnerSys)
			}
			wantPartnerKey := uint16(partner["key"].(float64))
			if lacp.Partner.Key != wantPartnerKey {
				t.Errorf("partner key: got %d, want %d", lacp.Partner.Key, wantPartnerKey)
			}

			wantMaxDelay := uint16(frame["collector_max_delay"].(float64))
			if lacp.CollectorMaxDelay != wantMaxDelay {
				t.Errorf("collector_max_delay: got %d, want %d", lacp.CollectorMaxDelay, wantMaxDelay)
			}

			// Round-trip.
			buf := gopacket.NewSerializeBuffer()
			if err := lacp.SerializeTo(buf, gopacket.SerializeOptions{}); err != nil {
				t.Fatalf("SerializeTo: %v", err)
			}
			expectedPayload := raw[14:] // after Ethernet header
			if hex.EncodeToString(buf.Bytes()) != hex.EncodeToString(expectedPayload) {
				t.Errorf("round-trip mismatch:\n  got  %x\n  want %x", buf.Bytes(), expectedPayload)
			}
		})
	}
}

func TestLACPTruncated(t *testing.T) {
	body := []byte{0x01, 0x01} // subtype + version only, missing actor TLV
	l := &LACP{}
	err := l.DecodeFromBytes(body, &testDecodeFeedback{})
	if err == nil {
		t.Fatal("expected truncated error, got nil")
	}
	if !strings.Contains(err.Error(), "LACP") {
		t.Errorf("error should name protocol LACP: %v", err)
	}
}

func TestEndToEndDispatch(t *testing.T) {
	// Verify the full dispatch chain on each protocol's fixture produces the
	// expected typed values (matching the Python reference dump in the goldens).
	t.Run("DTP_desirable", func(t *testing.T) {
		pkts := readPcap(t, "dtp.pcap")
		p := decodePacket(t, pkts[0])
		dtp := mustGetLayer[*DTP](t, p, LayerTypeDTP)

		// Verify dispatch chain layers are present.
		if p.Layer(layers.LayerTypeEthernet) == nil {
			t.Error("Ethernet layer missing")
		}
		if p.Layer(layers.LayerTypeLLC) == nil {
			t.Error("LLC layer missing")
		}
		if p.Layer(layers.LayerTypeSNAP) == nil {
			t.Error("SNAP layer missing")
		}

		if dtp.TrunkStatus != DTPStatusDesirable {
			t.Errorf("trunk_status: got 0x%02x, want 0x%02x", dtp.TrunkStatus, DTPStatusDesirable)
		}
		if dtp.NeighborState() != "desirable" {
			t.Errorf("neighbor state: got %q, want %q", dtp.NeighborState(), "desirable")
		}
	})

	t.Run("VTP_summary", func(t *testing.T) {
		pkts := readPcap(t, "vtp.pcap")
		p := decodePacket(t, pkts[0])
		vtp := mustGetLayer[*VTP](t, p, LayerTypeVTP)

		if vtp.Code != VTPCodeSummary {
			t.Errorf("code: got 0x%02x, want 0x%02x", vtp.Code, VTPCodeSummary)
		}
		if vtp.Domain != "LABDOMAIN" {
			t.Errorf("domain: got %q, want %q", vtp.Domain, "LABDOMAIN")
		}
		if vtp.Revision != 42 {
			t.Errorf("revision: got %d, want %d", vtp.Revision, 42)
		}
	})

	t.Run("MVRP_join", func(t *testing.T) {
		pkts := readPcap(t, "mvrp.pcap")
		p := decodePacket(t, pkts[0])
		mvrp := mustGetLayer[*MVRP](t, p, LayerTypeMVRP)

		if p.Layer(layers.LayerTypeEthernet) == nil {
			t.Error("Ethernet layer missing")
		}
		if len(mvrp.Messages) != 1 {
			t.Fatalf("messages: got %d, want 1", len(mvrp.Messages))
		}
		if mvrp.Messages[0].FirstValue != 10 {
			t.Errorf("VID: got %d, want %d", mvrp.Messages[0].FirstValue, 10)
		}
	})

	t.Run("LACP_lacpdu", func(t *testing.T) {
		pkts := readPcap(t, "lacp.pcap")
		p := decodePacket(t, pkts[0])
		lacp := mustGetLayer[*LACP](t, p, LayerTypeLACP)

		if p.Layer(layers.LayerTypeEthernet) == nil {
			t.Error("Ethernet layer missing")
		}
		if lacp.Subtype != 1 {
			t.Errorf("subtype: got %d, want 1", lacp.Subtype)
		}
	})

	t.Run("PAgP_hello", func(t *testing.T) {
		pkts := readPcap(t, "pagp.pcap")
		p := decodePacket(t, pkts[0])
		pagp := mustGetLayer[*PAgP](t, p, LayerTypePAgP)

		if p.Layer(layers.LayerTypeLLC) == nil {
			t.Error("LLC layer missing")
		}
		if pagp.Command != PAGPCmdHello {
			t.Errorf("command: got %d, want %d", pagp.Command, PAGPCmdHello)
		}
	})
}

// Malformed/adversarial corpus fixtures are accreted with provenance, never deleted.

func TestCorpusDTPMalformedTLV(t *testing.T) {
	pkts := readPcap(t, "dtp_malformed.pcap")
	p := decodePacket(t, pkts[0])
	errLayer := p.ErrorLayer()
	if errLayer == nil {
		t.Fatal("expected decoding error on malformed DTP frame")
	}
	if !strings.Contains(errLayer.Error().Error(), "DTP") {
		t.Errorf("error should name protocol DTP: %v", errLayer.Error())
	}
}

func TestCorpusDTPUnknownTLVFromPcap(t *testing.T) {
	pkts := readPcap(t, "dtp_unknown_tlv.pcap")
	p := decodePacket(t, pkts[0])
	dtp := mustGetLayer[*DTP](t, p, LayerTypeDTP)
	if len(dtp.TLVs) != 3 {
		t.Fatalf("TLV count: got %d, want 3", len(dtp.TLVs))
	}
	if dtp.TLVs[1].Type != 0xFFFE {
		t.Errorf("unknown TLV type: got 0x%04x, want 0xFFFE", dtp.TLVs[1].Type)
	}
	if dtp.TrunkStatus != 0x03 {
		t.Errorf("trunk_status after unknown TLV: got 0x%02x, want 0x03", dtp.TrunkStatus)
	}
}

func TestCorpusVTPMalformedSummary(t *testing.T) {
	pkts := readPcap(t, "vtp_malformed.pcap")
	p := decodePacket(t, pkts[0])
	errLayer := p.ErrorLayer()
	if errLayer == nil {
		t.Fatal("expected decoding error on malformed VTP frame")
	}
	if !strings.Contains(errLayer.Error().Error(), "VTP") {
		t.Errorf("error should name protocol VTP: %v", errLayer.Error())
	}
}

func TestCorpusLACPMalformed(t *testing.T) {
	pkts := readPcap(t, "lacp_malformed.pcap")
	p := decodePacket(t, pkts[0])
	errLayer := p.ErrorLayer()
	if errLayer == nil {
		t.Fatal("expected decoding error on malformed LACP frame")
	}
	if !strings.Contains(errLayer.Error().Error(), "LACP") {
		t.Errorf("error should name protocol LACP: %v", errLayer.Error())
	}
}

// Compile-time interface assertions.

var (
	_ gopacket.DecodingLayer = (*DTP)(nil)
	_ gopacket.DecodingLayer = (*VTP)(nil)
	_ gopacket.DecodingLayer = (*MVRP)(nil)
	_ gopacket.DecodingLayer = (*PAgP)(nil)
	_ gopacket.DecodingLayer = (*LACP)(nil)

	_ gopacket.SerializableLayer = (*DTP)(nil)
	_ gopacket.SerializableLayer = (*VTP)(nil)
	_ gopacket.SerializableLayer = (*MVRP)(nil)
	_ gopacket.SerializableLayer = (*PAgP)(nil)
	_ gopacket.SerializableLayer = (*LACP)(nil)
)

// extractProtoPayload extracts the protocol payload bytes from a raw frame:
// for LLC-encapsulated protocols (DTP, VTP, PAgP), it strips Ethernet (14) +
// LLC (3 or 4) + SNAP (5); for EtherType protocols (LACP, MVRP), it strips
// Ethernet (14).
func extractProtoPayload(t *testing.T, raw []byte, proto string) []byte {
	t.Helper()
	if len(raw) < 14 {
		t.Fatalf("frame too short: %d bytes", len(raw))
	}

	etherType := binary.BigEndian.Uint16(raw[12:14])
	switch proto {
	case "dtp", "vtp", "pagp":
		// 802.3/LLC/SNAP: length field ≤ 1500, LLC=3 (ctrl=3, 1 byte), SNAP=5
		if etherType > 1500 {
			t.Fatalf("expected 802.3 frame for %s, got EtherType 0x%04x", proto, etherType)
		}
		offset := 14 + 3 + 5 // Ethernet(14) + LLC(3) + SNAP(5)
		if offset > len(raw) {
			t.Fatalf("frame too short for %s payload: %d bytes", proto, len(raw))
		}
		return raw[offset:]
	case "lacp", "mvrp":
		return raw[14:]
	default:
		t.Fatalf("unknown protocol %s", proto)
		return nil
	}
}

type testDecodeFeedback struct{}

func (t *testDecodeFeedback) SetTruncated()    {}
func (t *testDecodeFeedback) SetError(_ error) {}
