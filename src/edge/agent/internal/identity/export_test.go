package identity

import (
	"encoding/base64"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/model/edge/v1"
)

// SameKeyForTest reports whether two identities hold the same private key.
// The field is unexported because it is a private key; a test that has to
// prove enrollment reused the key on disk still needs to ask.
func SameKeyForTest(a, b *Identity) bool { return a.key.Equal(b.key) }

// SetNonceForTest fixes the nonce a signer draws, so a header can be compared
// against a published vector. Production draws from crypto/rand.
func SetNonceForTest(s *Signer, nonce []byte) {
	s.nonce = func() ([]byte, error) { return nonce, nil }
}

// DecodeForTest reads back the assertion inside a header, so a test can
// assert what was signed rather than only that two headers differ.
func DecodeForTest(t *testing.T, header string) *edgev1.EdgeAssertion {
	t.Helper()
	_, raw, ok := strings.Cut(header, " ")
	if !ok {
		t.Fatalf("header %q carries no scheme", header)
	}
	wire, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	signed := &edgev1.SignedEdgeAssertion{}
	if err := proto.Unmarshal(wire, signed); err != nil {
		t.Fatalf("parse signed assertion: %v", err)
	}
	assertion := &edgev1.EdgeAssertion{}
	if err := proto.Unmarshal(signed.GetPayload(), assertion); err != nil {
		t.Fatalf("parse assertion: %v", err)
	}
	return assertion
}

// unmarshalProofForTest reads a KeyProof's payload, so a test's fake central
// can check the key it was presented with.
func UnmarshalProofForTest(proof *edgev1.KeyProof, into *edgev1.KeyProofPayload) error {
	return proto.Unmarshal(proof.GetPayload(), into)
}
