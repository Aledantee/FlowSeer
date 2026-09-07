package edgeapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"strings"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// The setup key string is fse1_ followed by a 26-character identifier and a
// 52-character secret, both lowercase base32 without padding, which is 128 and
// 256 random bits. The identifier is the only half that may be logged or
// stored in the clear.
const (
	setupKeyPrefix      = "fse1_"
	setupKeyIDBytes     = 16
	setupKeySecretBytes = 32
)

// setupKeyEncoding is the lowercase a-z2-7 alphabet the key string is written
// in. base32 encodes uppercase, so every encoded segment is lowered.
var setupKeyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// generateSetupKey draws a fresh setup key from the operating system's
// cryptographic source and returns the whole key string with its identifier.
// The string is shown to the operator once and never stored; only its digest
// and its identifier are.
func generateSetupKey() (key, id string, err error) {
	idBytes := make([]byte, setupKeyIDBytes)
	if _, err := rand.Read(idBytes); err != nil {
		return "", "", errs.From(err).Code(ErrCodeRandom).Msg("draw setup key identifier")
	}
	secret := make([]byte, setupKeySecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", "", errs.From(err).Code(ErrCodeRandom).Msg("draw setup key secret")
	}

	id = encodeSetupKeySegment(idBytes)
	return setupKeyPrefix + id + "_" + encodeSetupKeySegment(secret), id, nil
}

func encodeSetupKeySegment(b []byte) string {
	return strings.ToLower(setupKeyEncoding.EncodeToString(b))
}

// hashSetupKey is the digest central stores in place of the key: SHA-256 over
// the whole key string, so a breach of the edge bucket yields no key that
// enrolls.
func hashSetupKey(key string) []byte {
	sum := sha256.Sum256([]byte(key))
	return sum[:]
}
