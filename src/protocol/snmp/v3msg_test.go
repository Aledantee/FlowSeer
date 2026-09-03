package snmp

import (
	"bytes"
	"testing"
)

func sampleScoped() *scopedPDU {
	return &scopedPDU{
		contextEngineID: mustHex("8000000001020304"),
		contextName:     []byte("ctx-a"),
		pdu: pdu{
			typ:       pduGetRequest,
			requestID: 42,
			varbinds: []VarBind{
				NullVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0), Kind: KindNull}},
			},
		},
	}
}

func sampleV3(auth, priv bool) *v3Message {
	m := &v3Message{
		msgID:         0x11223344,
		msgMaxSize:    v3MaxMessageSize,
		flags:         msgFlags{reportable: true, auth: auth, priv: priv},
		securityModel: securityModelUSM,
		sec: usmSecurityParameters{
			engineID:    mustHex("8000000001020304"),
			engineBoots: 5,
			engineTime:  100,
			userName:    "alice",
		},
	}
	if auth {
		m.sec.authParams = bytes.Repeat([]byte{0xAB}, 12)
	}
	if priv {
		m.sec.privParams = mustHex("0102030405060708")
		m.ciphertext = mustHex("deadbeefcafebabe1122334455667788")
	} else {
		m.scoped = sampleScoped()
	}
	return m
}

// TestV3Message_RoundTrip_Plain round-trips an authNoPriv (plaintext) v3
// message and checks every field decodes back equal.
func TestV3Message_RoundTrip_Plain(t *testing.T) {
	m := sampleV3(true, false)
	raw, err := encodeV3Message(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec, err := decodeV3Message(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := dec.msg
	if got.msgID != m.msgID || got.msgMaxSize != m.msgMaxSize || got.securityModel != securityModelUSM {
		t.Fatalf("header mismatch: %+v", got)
	}
	if got.flags != m.flags {
		t.Fatalf("flags = %+v, want %+v", got.flags, m.flags)
	}
	if !bytes.Equal(got.sec.engineID, m.sec.engineID) || got.sec.engineBoots != 5 || got.sec.engineTime != 100 || got.sec.userName != "alice" {
		t.Fatalf("sec params mismatch: %+v", got.sec)
	}
	if got.scoped == nil || got.scoped.pdu.requestID != 42 || len(got.scoped.pdu.varbinds) != 1 {
		t.Fatalf("scoped pdu mismatch: %+v", got.scoped)
	}
	if got.scoped.pdu.varbinds[0].GetHeader().OID.String() != "1.3.6.1.2.1.1.1.0" {
		t.Fatalf("varbind OID mismatch: %v", got.scoped.pdu.varbinds[0].GetHeader().OID)
	}
}

// TestV3Message_RoundTrip_Priv round-trips an authPriv message whose msgData
// is an encrypted OCTET STRING; the ciphertext survives verbatim.
func TestV3Message_RoundTrip_Priv(t *testing.T) {
	m := sampleV3(true, true)
	raw, err := encodeV3Message(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec, err := decodeV3Message(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !dec.encrypted {
		t.Fatalf("expected encrypted msgData")
	}
	if !bytes.Equal(dec.msg.ciphertext, m.ciphertext) {
		t.Fatalf("ciphertext mismatch:\n got %x\nwant %x", dec.msg.ciphertext, m.ciphertext)
	}
	if !bytes.Equal(dec.scopedRaw, m.ciphertext) {
		t.Fatalf("scopedRaw should equal ciphertext")
	}
	if !bytes.Equal(dec.msg.sec.privParams, m.sec.privParams) {
		t.Fatalf("privParams mismatch")
	}
}

// TestV3Message_AuthParamRange confirms the decoder reports the exact
// msgAuthenticationParameters byte range: wholeMsg aliases the raw datagram
// and [authStart:authEnd) is precisely the auth-param octets, so the streaming
// HMAC zero-treats exactly that window.
func TestV3Message_AuthParamRange(t *testing.T) {
	m := sampleV3(true, false)
	raw, err := encodeV3Message(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dec, err := decodeV3Message(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	// wholeMsg is the datagram itself (no copy).
	if !bytes.Equal(dec.wholeMsg, raw) {
		t.Fatalf("wholeMsg should equal the raw datagram")
	}
	// [authStart:authEnd) must be exactly the auth-param octets on the wire.
	idx := bytes.Index(raw, m.sec.authParams)
	if idx < 0 {
		t.Fatalf("auth params not found in datagram")
	}
	if dec.authStart != idx || dec.authEnd != idx+len(m.sec.authParams) {
		t.Fatalf("auth window [%d:%d), want [%d:%d)", dec.authStart, dec.authEnd, idx, idx+len(m.sec.authParams))
	}
	if !bytes.Equal(dec.wholeMsg[dec.authStart:dec.authEnd], m.sec.authParams) {
		t.Fatalf("auth window bytes do not match the auth params")
	}
	// And the decoded authParams equal the original value.
	if !bytes.Equal(dec.msg.sec.authParams, m.sec.authParams) {
		t.Fatalf("decoded authParams mismatch")
	}
}

// TestV3Message_VersionDispatch checks decodeAnyMessage routes v3 to the v3
// decoder and v1/v2c to decodeMessage, and that decodeMessage alone still
// rejects v3 (the M1 invariant).
func TestV3Message_VersionDispatch(t *testing.T) {
	v3raw, err := encodeV3Message(sampleV3(true, false))
	if err != nil {
		t.Fatalf("encode v3: %v", err)
	}
	v2raw, err := encodeMessage(&message{
		version:   V2c,
		community: "public",
		pdu:       pdu{typ: pduGetRequest, requestID: 7, varbinds: nil},
	})
	if err != nil {
		t.Fatalf("encode v2c: %v", err)
	}

	if v1v2, v3, err := decodeAnyMessage(v3raw); err != nil || v3 == nil || v1v2 != nil {
		t.Fatalf("v3 dispatch: v1v2=%v v3=%v err=%v", v1v2, v3, err)
	}
	if v1v2, v3, err := decodeAnyMessage(v2raw); err != nil || v1v2 == nil || v3 != nil {
		t.Fatalf("v2c dispatch: v1v2=%v v3=%v err=%v", v1v2, v3, err)
	}
	// decodeMessage alone rejects v3.
	if _, err := decodeMessage(v3raw); err == nil {
		t.Fatalf("decodeMessage should reject v3")
	}
}

// TestV3Message_PrivWithoutAuthRejected confirms the RFC-invalid flag combo
// is rejected.
func TestV3Message_PrivWithoutAuthRejected(t *testing.T) {
	m := sampleV3(false, false)
	m.flags = msgFlags{priv: true} // priv set, auth clear
	m.sec.privParams = mustHex("0102030405060708")
	m.ciphertext = mustHex("deadbeef")
	m.scoped = nil
	raw, err := encodeV3Message(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := decodeV3Message(raw); err == nil {
		t.Fatalf("priv-without-auth should be rejected")
	}
}

// TestV3Message_DecodeEdges confirms malformed envelopes are typed errors,
// never panics.
func TestV3Message_DecodeEdges(t *testing.T) {
	raw, _ := encodeV3Message(sampleV3(true, false))
	for _, n := range []int{0, 1, 5, 10, len(raw) / 2, len(raw) - 1} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic on truncated[:%d]: %v", n, r)
				}
			}()
			if _, err := decodeV3Message(raw[:n]); err == nil {
				t.Fatalf("truncated[:%d] should error", n)
			}
		}()
	}
}

// TestV3Message_ScopedReuse confirms a decoded scoped PDU feeds the existing
// pduError/varbind path identically to v1/v2c.
func TestV3Message_ScopedReuse(t *testing.T) {
	m := sampleV3(true, false)
	m.scoped.pdu = pdu{
		typ:         pduGetResponse,
		requestID:   9,
		errorStatus: NoSuchName,
		errorIndex:  1,
		varbinds:    []VarBind{NullVar{Header: Header{OID: MustOID(1, 3, 6, 1), Kind: KindNull}}},
	}
	raw, _ := encodeV3Message(m)
	dec, err := decodeV3Message(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	pe := pduError(&message{pdu: dec.msg.scoped.pdu})
	if pe == nil || pe.Status != NoSuchName {
		t.Fatalf("pduError mismatch: %+v", pe)
	}
}
