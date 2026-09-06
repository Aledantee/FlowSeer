package snmp

import (
	"context"
	"testing"

	"go.aledante.io/FlowSeer/src/common/secret"
)

// v3_fuzz_test.go is the cross-cutting hardening harness. The no-panic /
// typed-drop property is asserted per-layer by the unit tests;
// this file stress-proves it across the whole v3 receive surface — the
// envelope decode, the double-BER security params, and the post-decrypt
// scoped-PDU BER — closing the gosnmp #194/#274/#344 panic classes.

// fuzzSeedCorpus returns representative valid/edge v3 datagrams to seed every
// fuzz target from realistic structure rather than random discovery.
func fuzzSeedCorpus() [][]byte {
	var seeds [][]byte
	add := func(b []byte, err error) {
		if err == nil {
			seeds = append(seeds, b)
		}
	}
	// A full authPriv message and a plaintext authNoPriv message.
	add(encodeV3Message(sampleV3(true, true)))
	add(encodeV3Message(sampleV3(true, false)))
	add(encodeV3Message(sampleV3(false, false)))
	// A discovery probe.
	add(buildDiscoveryProbe(7))
	// A Report.
	add(encodeV3Message(&v3Message{
		msgID: 1, msgMaxSize: v3MaxMessageSize, securityModel: securityModelUSM,
		sec:    usmSecurityParameters{engineID: mustHex("8000000001"), engineBoots: 1, engineTime: 1},
		scoped: &scopedPDU{contextEngineID: mustHex("8000000001"), pdu: pdu{typ: pduReport, varbinds: []VarBind{usmStatsVB(4)}}},
	}))
	// Forged / oversized msgSecurityParameters: an oversized
	// engineID with a wrong-length authParams window, and an empty engineID
	// with the auth flag set carrying a non-usmStats Report varbind. These must
	// decode to a typed error / classify cleanly, never panic at the gate.
	add(encodeV3Message(&v3Message{
		msgID: 2, msgMaxSize: v3MaxMessageSize, securityModel: securityModelUSM,
		flags:  msgFlags{auth: true},
		sec:    usmSecurityParameters{engineID: make([]byte, 64), engineBoots: 1, engineTime: 1, userName: "x", authParams: make([]byte, 5)},
		scoped: &scopedPDU{contextEngineID: make([]byte, 64), pdu: pdu{typ: pduReport, varbinds: []VarBind{usmStatsVB(99)}}},
	}))
	add(encodeV3Message(&v3Message{
		msgID: 3, msgMaxSize: v3MaxMessageSize, securityModel: securityModelUSM,
		flags:  msgFlags{auth: true},
		sec:    usmSecurityParameters{engineID: nil, engineBoots: 0, engineTime: 0, authParams: make([]byte, 12)},
		scoped: &scopedPDU{pdu: pdu{typ: pduReport, varbinds: []VarBind{octet(MustOID(1, 3, 6, 1), "not-usmstats")}}},
	}))
	return seeds
}

// FuzzDecodeV3Message proves the v3 envelope + double-BER security-params
// decode never panics: any input is a clean decode or a typed error
// (#194/#344 classes).
func FuzzDecodeV3Message(f *testing.F) {
	for _, s := range fuzzSeedCorpus() {
		f.Add(s)
	}
	f.Add([]byte{0x30, 0x00})                   // empty SEQUENCE
	f.Add([]byte{0x30, 0x03, 0x02, 0x01, 0x03}) // version-only
	f.Fuzz(func(_ *testing.T, data []byte) {
		// Both the direct v3 decoder and the version dispatcher must be
		// panic-free on arbitrary input.
		_, _ = decodeV3Message(data)
		_, _, _ = decodeAnyMessage(data)
	})
}

// FuzzDecodeScopedPDU directly fuzzes the post-decrypt BER path that
// FuzzVerifyInbound cannot reach (HMAC rejects all fuzz input before
// decrypt). A wrong-key authPriv decrypt yields arbitrary bytes that are fed
// straight to decodeScopedPDU; this proves garbage-after-decrypt is a typed
// error, never a panic (the #194 class).
func FuzzDecodeScopedPDU(f *testing.F) {
	good, _ := encodeScopedPDU(sampleScoped())
	f.Add(good)
	if len(good) > 4 {
		f.Add(good[:len(good)/2])
	}
	f.Add([]byte{0x30, 0x00})
	f.Add([]byte{})
	f.Fuzz(func(_ *testing.T, data []byte) {
		_, _ = decodeScopedPDU(data)
	})
}

// FuzzVerifyInbound proves the post-decode crypto gate (HMAC verify,
// decrypt) never panics on a malformed/forged datagram. NOTE: HMAC
// verification rejects essentially all fuzz-generated input before the
// decrypt/BER-parse step, so the post-decrypt BER path is covered by
// FuzzDecodeScopedPDU above, not here.
func FuzzVerifyInbound(f *testing.F) {
	for _, s := range fuzzSeedCorpus() {
		f.Add(s)
	}
	cfg := USMConfig{
		Username: "alice", AuthProtocol: AuthSHA256, AuthPassphrase: secret.NewString("fuzz-auth-passphrase"),
		PrivProtocol: PrivAES, PrivPassphrase: secret.NewString("fuzz-priv-passphrase"), EngineID: mustHex("8000000001020304"),
	}
	u, err := newUSMContext(context.Background(), cfg)
	if err != nil {
		f.Fatalf("usm: %v", err)
	}
	f.Fuzz(func(_ *testing.T, data []byte) {
		dec, derr := decodeV3Message(data)
		if derr != nil || dec == nil {
			return
		}
		_, _ = u.verifyInbound(dec)
		_, _, _ = u.inboundGate(dec)
	})
}

// FuzzListenerHandlePacket proves the whole listener receive path — v1/v2c
// and v3, including the dual-role router, the engine-table lookup, and the
// authoritative responder — never panics and never wedges. A v3 datagram at
// a listener with zero registered engines must drop cleanly (#274 class).
func FuzzListenerHandlePacket(f *testing.F) {
	for _, s := range fuzzSeedCorpus() {
		f.Add(s)
	}
	f.Add(buildV2cTrap2Seed())

	// A listener with one registered engine and one with none, to exercise
	// both the resolved and the zero-engine drop paths.
	regCfg := USMConfig{
		Username: "alice", AuthProtocol: AuthSHA256, AuthPassphrase: secret.NewString("fuzz-auth-passphrase"),
		PrivProtocol: PrivAES, PrivPassphrase: secret.NewString("fuzz-priv-passphrase"), EngineID: mustHex("8000000001020304"),
	}
	withEngine := newFuzzListener(f, mustHex("80001f8800ffffffff000001"), []USMConfig{regCfg})
	zeroEngine := newFuzzListener(f, mustHex("80001f8800ffffffff000002"), nil)

	f.Fuzz(func(_ *testing.T, data []byte) {
		// remote is nil-safe; handlePacket must tolerate it.
		withEngine.handlePacket(data, nil)
		zeroEngine.handlePacket(data, nil)
	})
}

// newFuzzListener builds a listener wired to a discarding TrapStream, with no
// real socket (handlePacket only reads/sends via conn for v3 acks; a nil-safe
// sendTo guards the unset socket on the fuzz path).
func newFuzzListener(f *testing.F, ownEngineID []byte, table []USMConfig) *listener {
	f.Helper()
	ts := NewTrapStream(context.Background(), 64)
	l := &listener{
		ts:          ts,
		matcher:     nil,
		limiter:     nil,
		engines:     newEngineTable(context.Background()),
		ownEngineID: ownEngineID,
		logCtx:      context.Background(),
	}
	// start=Unix epoch -> snmpEngineTime is far past the 150s quarantine, so
	// the authoritative responder runs its full accept path under fuzz (the
	// quarantine-reject branch is covered by TestV3Inform_PostRestartQuarantine).
	l.startedNanos.Store(0)
	if err := l.engines.seed(table); err != nil {
		f.Fatalf("seed: %v", err)
	}
	return l
}

// buildV2cTrap2Seed returns a minimal valid v2c trap datagram for the corpus.
func buildV2cTrap2Seed() []byte {
	raw, _ := encodeMessage(&message{
		version:   V2c,
		community: "public",
		pdu:       pdu{typ: pduV2Trap, requestID: 1},
	})
	return raw
}
