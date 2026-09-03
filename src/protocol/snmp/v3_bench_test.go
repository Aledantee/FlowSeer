package snmp

import (
	"context"
	"testing"
)

// BenchmarkV3DecodeVerify measures the inbound v3 decode+HMAC-verify path (the
// loopback micro suite is v2c-only, so this fills the v3 gap). It is the
// regression benchmark for the streaming HMAC: with the streaming verify it
// reports 22 allocs/op; the earlier per-datagram full-buffer copy added one
// allocation plus len(datagram) bytes (23 allocs/op, +144 B/op on this small
// authNoPriv datagram, scaling with response size). authNoPriv keeps the timed
// work to decode + HMAC (no decrypt).
func BenchmarkV3DecodeVerify(b *testing.B) {
	cfg := USMConfig{
		Username:       "alice",
		AuthProtocol:   AuthSHA,
		PrivProtocol:   PrivProtocolNone,
		EngineID:       mustHex("8000000001020304050607"),
		AuthPassphrase: "auth-supersecret-passphrase",
	}
	if err := cfg.Validate(); err != nil {
		b.Fatalf("cfg: %v", err)
	}
	u, err := newUSMContext(context.Background(), cfg)
	if err != nil {
		b.Fatalf("newUSMContext: %v", err)
	}
	raw, err := u.buildOutbound(7, sampleResponsePDU(), 3, 600, true)
	if err != nil {
		b.Fatalf("buildOutbound: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dec, err := decodeV3Message(raw)
		if err != nil {
			b.Fatalf("decode: %v", err)
		}
		if _, err := u.verifyInbound(dec); err != nil {
			b.Fatalf("verify: %v", err)
		}
	}
}
