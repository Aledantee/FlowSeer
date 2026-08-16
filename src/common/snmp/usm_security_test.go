package snmp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const (
	secTestAuthPass = "auth-supersecret-passphrase"
	secTestPrivPass = "priv-supersecret-passphrase"
)

var secTestEngine = mustHex("8000000001020304050607")

func sampleResponsePDU() pdu {
	return pdu{
		typ:       pduGetResponse,
		requestID: 12345,
		varbinds: []VarBind{
			OctetStringVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 1, 0), Kind: KindOctetString}, Value: []byte("a device")},
			TimeTicksVar{Header: Header{OID: MustOID(1, 3, 6, 1, 2, 1, 1, 3, 0), Kind: KindTimeTicks}, Value: 999},
		},
	}
}

func mustContext(t *testing.T, auth AuthProtocol, priv PrivProtocol) *usmContext {
	t.Helper()
	cfg := USMConfig{Username: "alice", AuthProtocol: auth, PrivProtocol: priv, EngineID: secTestEngine}
	if auth != AuthProtocolNone {
		cfg.AuthPassphrase = secTestAuthPass
	}
	if priv != PrivProtocolNone {
		cfg.PrivPassphrase = secTestPrivPass
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("invalid cfg: %v", err)
	}
	u, err := newUSMContext(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newUSMContext: %v", err)
	}
	return u
}

// TestUSM_RoundTripMatrix builds and then verifies+decrypts across the full
// auth×priv matrix; the scoped PDU survives the round-trip.
func TestUSM_RoundTripMatrix(t *testing.T) {
	auths := []AuthProtocol{AuthMD5, AuthSHA, AuthSHA224, AuthSHA256, AuthSHA384, AuthSHA512}
	privs := []PrivProtocol{PrivDES, Priv3DES, PrivAES, PrivAES192, PrivAES256, PrivAES192C, PrivAES256C}

	type cell struct {
		auth AuthProtocol
		priv PrivProtocol
	}
	var cells []cell
	cells = append(cells, cell{AuthProtocolNone, PrivProtocolNone}) // noAuthNoPriv
	for _, a := range auths {
		cells = append(cells, cell{a, PrivProtocolNone}) // authNoPriv
		for _, p := range privs {
			cells = append(cells, cell{a, p}) // authPriv
		}
	}

	for _, c := range cells {
		name := c.auth.String() + "+" + c.priv.String()
		t.Run(name, func(t *testing.T) {
			u := mustContext(t, c.auth, c.priv)
			raw, err := u.buildOutbound(7, sampleResponsePDU(), 3, 600, true)
			if err != nil {
				t.Fatalf("buildOutbound: %v", err)
			}
			dec, err := decodeV3Message(raw)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			sp, err := u.verifyInbound(dec)
			if err != nil {
				t.Fatalf("verifyInbound: %v", err)
			}
			if sp.pdu.requestID != 12345 || len(sp.pdu.varbinds) != 2 {
				t.Fatalf("scoped pdu mismatch: %+v", sp.pdu)
			}
			if sp.pdu.varbinds[0].GetHeader().OID.String() != "1.3.6.1.2.1.1.1.0" {
				t.Fatalf("varbind OID mismatch")
			}
		})
	}
}

// TestUSM_DowngradePlaintext rejects an unencrypted reply at an authPriv
// context.
func TestUSM_DowngradePlaintext(t *testing.T) {
	send := mustContext(t, AuthSHA256, PrivProtocolNone) // authNoPriv sender
	recv := mustContext(t, AuthSHA256, PrivAES256)       // authPriv expected
	// send and recv share the same auth passphrase + engineID, so their auth
	// keys are identical and the HMAC passes — only the downgrade check fires.
	raw, err := send.buildOutbound(7, sampleResponsePDU(), 3, 600, false)
	if err != nil {
		t.Fatalf("buildOutbound: %v", err)
	}
	dec, _ := decodeV3Message(raw)
	if _, err := recv.verifyInbound(dec); !errors.Is(err, ErrUSMDowngrade) {
		t.Fatalf("expected downgrade error, got %v", err)
	}
}

// TestUSM_DowngradeUnauthenticated covers the first downgrade branch: a
// noAuthNoPriv reply arriving at an authNoPriv context must be rejected
// before any HMAC work (the message carries no auth flag at all).
func TestUSM_DowngradeUnauthenticated(t *testing.T) {
	send := mustContext(t, AuthProtocolNone, PrivProtocolNone) // noAuthNoPriv
	recv := mustContext(t, AuthSHA256, PrivProtocolNone)       // authNoPriv expected
	raw, err := send.buildOutbound(7, sampleResponsePDU(), 3, 600, false)
	if err != nil {
		t.Fatalf("buildOutbound: %v", err)
	}
	dec, _ := decodeV3Message(raw)
	if _, err := recv.verifyInbound(dec); !errors.Is(err, ErrUSMDowngrade) {
		t.Fatalf("expected downgrade error for unauthenticated reply, got %v", err)
	}
}

// TestUSM_TamperedDigest rejects a reply whose MAC was tampered
// (usmStatsWrongDigests).
func TestUSM_TamperedDigest(t *testing.T) {
	u := mustContext(t, AuthSHA256, PrivProtocolNone)
	raw, err := u.buildOutbound(7, sampleResponsePDU(), 3, 600, false)
	if err != nil {
		t.Fatalf("buildOutbound: %v", err)
	}
	// Flip a byte inside the auth params region: decode, find params, corrupt
	// in the raw datagram.
	dec, _ := decodeV3Message(raw)
	idx := indexOf(raw, dec.msg.sec.authParams)
	if idx < 0 {
		t.Fatalf("auth params not located")
	}
	raw[idx] ^= 0x80
	dec2, err := decodeV3Message(raw)
	if err != nil {
		t.Fatalf("decode after tamper: %v", err)
	}
	if _, err := u.verifyInbound(dec2); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("expected auth failure, got %v", err)
	}
}

// TestUSM_WrongPrivKeyBERFails confirms a wrong priv key yields a typed
// DecryptionErrors (BER parse failure), never a panic.
func TestUSM_WrongPrivKeyBERFails(t *testing.T) {
	send := mustContext(t, AuthSHA256, PrivAES256)
	raw, err := send.buildOutbound(7, sampleResponsePDU(), 3, 600, false)
	if err != nil {
		t.Fatalf("buildOutbound: %v", err)
	}
	dec, _ := decodeV3Message(raw)
	// Receiver with a *different* priv passphrase (same auth so HMAC passes,
	// only the decrypt produces garbage).
	cfg := USMConfig{
		Username: "alice", AuthProtocol: AuthSHA256, AuthPassphrase: secTestAuthPass,
		PrivProtocol: PrivAES256, PrivPassphrase: "a-totally-different-priv-passphrase", EngineID: secTestEngine,
	}
	recv, err := newUSMContext(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newUSMContext: %v", err)
	}
	if _, err := recv.verifyInbound(dec); !errors.Is(err, ErrPrivDecrypt) {
		t.Fatalf("expected decryption error, got %v", err)
	}
}

// TestUSM_NoKeysBeforeDiscovery confirms buildOutbound errors before
// setEngine.
func TestUSM_NoKeysBeforeDiscovery(t *testing.T) {
	cfg := USMConfig{Username: "bob", AuthProtocol: AuthSHA, AuthPassphrase: secTestAuthPass}
	u, err := newUSMContext(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newUSMContext: %v", err)
	}
	if u.hasEngine() {
		t.Fatalf("should have no engine yet")
	}
	if _, err := u.buildOutbound(1, sampleResponsePDU(), 0, 0, true); !errors.Is(err, ErrUSMNoKeys) {
		t.Fatalf("expected ErrUSMNoKeys, got %v", err)
	}
	if err := u.setEngine(secTestEngine); err != nil {
		t.Fatalf("setEngine: %v", err)
	}
	if _, err := u.buildOutbound(1, sampleResponsePDU(), 0, 0, true); err != nil {
		t.Fatalf("after discovery: %v", err)
	}
}

// TestUSM_NoSecretLeak asserts no passphrase appears in any returned error
// (passphrase redaction).
func TestUSM_NoSecretLeak(t *testing.T) {
	recv := mustContext(t, AuthSHA256, PrivAES256)
	send := mustContext(t, AuthSHA256, PrivProtocolNone)
	raw, _ := send.buildOutbound(7, sampleResponsePDU(), 3, 600, false)
	dec, _ := decodeV3Message(raw)
	_, err := recv.verifyInbound(dec)
	if err == nil {
		t.Fatalf("expected an error to inspect")
	}
	for _, secret := range []string{secTestAuthPass, secTestPrivPass} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaked a passphrase: %v", err)
		}
	}
}

// indexOf finds sub in b (first occurrence), or -1.
func indexOf(b, sub []byte) int {
	return strings.Index(string(b), string(sub))
}
