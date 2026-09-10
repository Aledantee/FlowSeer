package mirror_test

import (
	"bytes"
	"encoding/hex"
	"net"
	"testing"

	capturev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/net/capture/v1"
	"go.aledante.io/FlowSeer/src/modules/capture/mirror"
)

// inner is a stand-in mirrored Ethernet frame carried by every fixture below
// that has one.
var inner = mustHex("001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")

var (
	srcIP = net.ParseIP("192.0.2.1")
	dstIP = net.ParseIP("192.0.2.2")
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// TestDecode_ErspanTypeII proves the decapsulating receiver unwraps a
// mirrored frame and records the outer wrapper as metadata: GRE protocol
// 0x88BE with the sequence-number bit set, ERSPAN Version 1, session id 7,
// truncation bit set.
func TestDecode_ErspanTypeII(t *testing.T) {
	payload := mustHex("100088be000000011064740700003039001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")
	env, gotInner, err := mirror.Decode(payload, srcIP, dstIP)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !env.HasErspanTypeIi() {
		t.Fatalf("envelope has no erspan_type_ii wrapper")
	}
	f := env.GetErspanTypeIi()
	if got := f.GetSessionId(); got != 7 {
		t.Errorf("session_id = %d, want 7", got)
	}
	if !f.GetTruncated() {
		t.Errorf("truncated = false, want true")
	}
	if !bytes.Equal(gotInner, inner) {
		t.Errorf("inner frame = %x, want %x", gotInner, inner)
	}
}

func TestDecode_ErspanTypeIII_Marker(t *testing.T) {
	// ethernet_frame (the draft's P bit) is false: a Type III header with no
	// mirrored frame behind it, whatever real device or condition produced
	// one, carries no inner frame.
	payload := mustHex("100022eb0000000220000009deadbeef00000000")
	env, gotInner, err := mirror.Decode(payload, srcIP, dstIP)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !env.HasErspanTypeIii() {
		t.Fatalf("envelope has no erspan_type_iii wrapper")
	}
	if env.GetErspanTypeIii().GetEthernetFrame() {
		t.Errorf("ethernet_frame = true, want false")
	}
	if gotInner != nil {
		t.Errorf("inner frame = %x, want nil for a marker", gotInner)
	}
}

func TestDecode_ErspanTypeIII_EthernetFrame(t *testing.T) {
	payload := mustHex("100022eb0000000220000009deadbeef0fff8d1c001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")
	env, gotInner, err := mirror.Decode(payload, srcIP, dstIP)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	f := env.GetErspanTypeIii()
	if !f.GetEthernetFrame() {
		t.Fatalf("ethernet_frame = false, want true")
	}
	if got := f.GetSessionId(); got != 9 {
		t.Errorf("session_id = %d, want 9", got)
	}
	if got := f.GetTimestamp(); got != 0xDEADBEEF {
		t.Errorf("timestamp = %#x, want 0xdeadbeef", got)
	}
	if got := f.GetSecurityGroupTag(); got != 4095 {
		t.Errorf("security_group_tag = %d, want 4095", got)
	}
	if got := f.GetFrameType(); got != 3 {
		t.Errorf("frame_type = %d, want 3", got)
	}
	if got := f.GetHardwareId(); got != 17 {
		t.Errorf("hardware_id = %d, want 17", got)
	}
	if !f.GetDirection() {
		t.Errorf("direction = false, want true")
	}
	if got := f.GetTimestampGranularity(); got != capturev1.ErspanTimestampGranularity(2) {
		t.Errorf("timestamp_granularity = %v, want 2", got)
	}
	if !bytes.Equal(gotInner, inner) {
		t.Errorf("inner frame = %x, want %x", gotInner, inner)
	}
}

func TestDecode_ErspanTypeI(t *testing.T) {
	// GRE protocol 0x88BE with no sequence number: draft-foschiano-erspan-03
	// describes Type I as GRE with no ERSPAN header at all.
	payload := mustHex("000088be001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")
	env, gotInner, err := mirror.Decode(payload, srcIP, dstIP)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !env.HasErspanTypeI() {
		t.Fatalf("envelope has no erspan_type_i wrapper")
	}
	if !bytes.Equal(gotInner, inner) {
		t.Errorf("inner frame = %x, want %x", gotInner, inner)
	}
}

func TestDecode_PlainGRE_Ethernet(t *testing.T) {
	payload := mustHex("00006558001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")
	env, gotInner, err := mirror.Decode(payload, srcIP, dstIP)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !env.HasGre() {
		t.Fatalf("envelope has no gre wrapper")
	}
	if got := env.GetGre().GetProtocolType(); got != 0x6558 {
		t.Errorf("protocol_type = %#x, want 0x6558", got)
	}
	if !bytes.Equal(gotInner, inner) {
		t.Errorf("inner frame = %x, want %x", gotInner, inner)
	}
}

func TestDecode_PlainGRE_NonEthernet(t *testing.T) {
	// protocol type 0x0800 (IPv4): the GRE payload is not an Ethernet frame,
	// so it is counted and decoded but produces no inner frame.
	payload := mustHex("00000800450000280000000040018000")
	env, gotInner, err := mirror.Decode(payload, srcIP, dstIP)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := env.GetGre().GetProtocolType(); got != 0x0800 {
		t.Errorf("protocol_type = %#x, want 0x0800", got)
	}
	if gotInner != nil {
		t.Errorf("inner frame = %x, want nil for a non-Ethernet GRE payload", gotInner)
	}
}

// TestDecode_HeaderOnlyPayloadYieldsNilInner proves a GRE keepalive (a
// header with nothing behind it) reports a nil inner frame, not a non-nil
// zero-length one: Go's own slicing of an exhausted buffer produces
// []byte{}, not nil, so this is not automatic.
func TestDecode_HeaderOnlyPayloadYieldsNilInner(t *testing.T) {
	payload := mustHex("00006558") // base GRE header, TEB protocol type, nothing after it
	_, gotInner, err := mirror.Decode(payload, srcIP, dstIP)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if gotInner != nil {
		t.Errorf("inner frame = %#v, want nil for a header-only payload", gotInner)
	}
}

func TestDecodeUDP_Tzsp_RejectsWrongVersionOrType(t *testing.T) {
	wrongVersion := mustHex("02000001000122334455") // version 2
	if _, _, err := mirror.DecodeUDP(wrongVersion, srcIP, dstIP, []capturev1.MirrorEncapsulation{
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_TZSP,
	}); err == nil {
		t.Error("DecodeUDP with TZSP version 2: want an error, got nil")
	}

	keepalive := mustHex("01040001deadbeef") // version 1, type 4 (KEEPALIVE)
	if _, _, err := mirror.DecodeUDP(keepalive, srcIP, dstIP, []capturev1.MirrorEncapsulation{
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_TZSP,
	}); err == nil {
		t.Error("DecodeUDP with TZSP type 4 (KEEPALIVE): want an error, got nil")
	}
}

func TestDecode_SourceDestination(t *testing.T) {
	payload := mustHex("00006558001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")
	env, _, err := mirror.Decode(payload, srcIP, dstIP)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := env.GetSource().GetV4().GetOctets(); !bytes.Equal(got, srcIP.To4()) {
		t.Errorf("source = %v, want %v", got, srcIP.To4())
	}
	if got := env.GetDestination().GetV4().GetOctets(); !bytes.Equal(got, dstIP.To4()) {
		t.Errorf("destination = %v, want %v", got, dstIP.To4())
	}
}

func TestDecodeUDP_Vxlan(t *testing.T) {
	payload := mustHex("0800000001ccdd00001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")
	candidates := []capturev1.MirrorEncapsulation{capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN}
	env, gotInner, err := mirror.DecodeUDP(payload, srcIP, dstIP, candidates)
	if err != nil {
		t.Fatalf("DecodeUDP: %v", err)
	}
	if !env.HasVxlan() {
		t.Fatalf("envelope has no vxlan wrapper")
	}
	if got := env.GetVxlan().GetVni(); got != 0x01ccdd {
		t.Errorf("vni = %#x, want 0x01ccdd", got)
	}
	if !bytes.Equal(gotInner, inner) {
		t.Errorf("inner frame = %x, want %x", gotInner, inner)
	}
}

func TestDecodeUDP_Tzsp(t *testing.T) {
	payload := mustHex("01000001000a02abcd01001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")
	candidates := []capturev1.MirrorEncapsulation{capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_TZSP}
	env, gotInner, err := mirror.DecodeUDP(payload, srcIP, dstIP, candidates)
	if err != nil {
		t.Fatalf("DecodeUDP: %v", err)
	}
	if !env.HasTzsp() {
		t.Fatalf("envelope has no tzsp wrapper")
	}
	if got := env.GetTzsp().GetEncapsulatedProtocol(); got != 1 {
		t.Errorf("encapsulated_protocol = %d, want 1", got)
	}
	if !bytes.Equal(gotInner, inner) {
		t.Errorf("inner frame = %x, want %x", gotInner, inner)
	}
}

// TestDecodeUDP_FixedOrder proves DecodeUDP tries VXLAN before TZSP
// regardless of candidates' own order. VXLAN's I flag (byte 0 bit 0x08) and
// TZSP's exact version byte (must be 1) cannot both hold for the same
// leading byte, so a payload cannot validate under both structurally; the
// fixed order is instead proven by a TZSP-only-valid payload still decoding
// as TZSP when candidates lists VXLAN first — VXLAN's own failed attempt
// must fall through rather than short-circuit the whole call.
func TestDecodeUDP_FixedOrder(t *testing.T) {
	payload := mustHex("01000001000a02abcd01001122334455aabbccddeeff0800494e4e45524652414d455041594c4f4144")

	vxlanFirst := []capturev1.MirrorEncapsulation{
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_VXLAN,
		capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_TZSP,
	}
	env, _, err := mirror.DecodeUDP(payload, srcIP, dstIP, vxlanFirst)
	if err != nil {
		t.Fatalf("DecodeUDP: %v", err)
	}
	if !env.HasTzsp() {
		t.Errorf("with VXLAN tried first and failing, decoded as %v, want tzsp (fallthrough to the next candidate)", env.WhichWrapper())
	}

	tzspOnly := []capturev1.MirrorEncapsulation{capturev1.MirrorEncapsulation_MIRROR_ENCAPSULATION_TZSP}
	env2, _, err := mirror.DecodeUDP(payload, srcIP, dstIP, tzspOnly)
	if err != nil {
		t.Fatalf("DecodeUDP: %v", err)
	}
	if !env2.HasTzsp() {
		t.Errorf("with only TZSP a candidate, decoded as %v, want tzsp", env2.WhichWrapper())
	}
}

func TestDecodeUDP_NoCandidateMatches(t *testing.T) {
	if _, _, err := mirror.DecodeUDP([]byte{0, 1, 2}, srcIP, dstIP, nil); err == nil {
		t.Fatal("DecodeUDP: want an error when no candidate matches, got nil")
	}
}
