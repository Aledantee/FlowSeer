package netsimload

import (
	"bytes"
	"encoding/hex"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/net/ethernet"
	"go.aledante.io/FlowSeer/src/common/netsim/fabric"
)

func TestSignPinsEverySignatureOffset(t *testing.T) {
	frame := ethernet.Frame{Payload: bytes.Repeat([]byte{0xaa}, SignatureSize+2)}
	original := append([]byte(nil), frame.Payload...)
	submitted := time.Unix(1700000000, 123456789)

	signed, err := Sign(frame, fabric.FlowID(0x01020304), 0x1112131415161718, submitted)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	want := "46534c44000000010102030400000000111213141516171817979cfe3d85cd15"
	if got := hex.EncodeToString(signed.Payload[:SignatureSize]); got != want {
		t.Fatalf("signature = %s, want %s", got, want)
	}
	if !bytes.Equal(frame.Payload, original) {
		t.Fatal("Sign mutated the source payload")
	}
}

func TestSignatureRoundTrip(t *testing.T) {
	submitted := time.Unix(1700000000, 0)
	frame, err := Sign(ethernet.Frame{Payload: make([]byte, SignatureSize)}, 7, 9, submitted)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	wire, err := frame.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := DecodeWireSignature(wire)
	if err != nil {
		t.Fatalf("DecodeWireSignature: %v", err)
	}
	if got.FlowID != 7 || got.Sequence != 9 || !got.SubmittedAt.Equal(submitted) {
		t.Fatalf("signature = %+v, want flow 7 sequence 9 at %v", got, submitted)
	}
}

func TestDecodeSignatureRejectsBadLayout(t *testing.T) {
	valid, err := hex.DecodeString("46534c44000000010102030400000000111213141516171817979cfe3d85cd15")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"magic", func(p []byte) []byte { p[0] = 'N'; return p }},
		{"version", func(p []byte) []byte { p[7] = 2; return p }},
		{"reserved word", func(p []byte) []byte { p[15] = 1; return p }},
		{"short payload", func(p []byte) []byte { return p[:SignatureSize-1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := tc.mutate(bytes.Clone(valid))
			if _, err := DecodeSignature(payload); err == nil {
				t.Fatal("DecodeSignature accepted invalid signature")
			}
		})
	}
}

func TestDecodeSignatureLiteralVector(t *testing.T) {
	payload, err := hex.DecodeString("46534c44000000010102030400000000111213141516171817979cfe3d85cd15")
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeSignature(payload)
	if err != nil {
		t.Fatalf("DecodeSignature: %v", err)
	}
	if got.FlowID != 0x01020304 || got.Sequence != 0x1112131415161718 || !got.SubmittedAt.Equal(time.Unix(1700000000, 123456789)) {
		t.Fatalf("literal signature = %+v", got)
	}
}

func TestSignRejectsZeroFlowAndShortPayload(t *testing.T) {
	if _, err := Sign(ethernet.Frame{Payload: make([]byte, SignatureSize)}, 0, 0, time.Unix(0, 0)); err == nil {
		t.Fatal("Sign accepted zero flow ID")
	}
	if _, err := Sign(ethernet.Frame{Payload: make([]byte, SignatureSize-1)}, 1, 0, time.Unix(0, 0)); err == nil {
		t.Fatal("Sign accepted a short payload")
	}
}
