package edgebus

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// ErrCodePin identifies a peer certificate whose SPKI digest is not among
// the anchors an edge was provisioned with.
var ErrCodePin = errs.NewCode("edgebus/pin")

// PinVerifier returns the tls.Config.VerifyPeerCertificate callback an edge
// installs on every dialer that reaches central, the Connect client and
// the leaf remote alike, so the two cannot drift apart. It accepts a chain
// whose leaf certificate's SubjectPublicKeyInfo SHA-256 digest is one of
// anchors, the 32-byte digests EdgeProvisioning and EnrollResponse carry.
// A caller pairs it with InsecureSkipVerify, since the anchor replaces the
// system roots rather than adding to them: a corporate interception
// certificate is exactly what the pin exists to refuse.
func PinVerifier(anchors [][]byte) func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	pinned := make(map[string]struct{}, len(anchors))
	for _, anchor := range anchors {
		pinned[hex.EncodeToString(anchor)] = struct{}{}
	}
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errs.New().Code(ErrCodePin).Msg("peer presented no certificate")
		}
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return errs.From(err).Code(ErrCodePin).Msg("parse peer certificate")
		}
		if _, ok := pinned[hex.EncodeToString(SPKIDigest(cert))]; !ok {
			return errs.New().Code(ErrCodePin).Msg("peer certificate matches no provisioned anchor")
		}
		return nil
	}
}

// PinnedTLSConfig is the client configuration both dialers use: no system
// roots, the pin verifier in their place.
func PinnedTLSConfig(anchors [][]byte) *tls.Config {
	return &tls.Config{
		MinVersion:            tls.VersionTLS12,
		InsecureSkipVerify:    true, //nolint:gosec // the pin verifier replaces chain verification by design
		VerifyPeerCertificate: PinVerifier(anchors),
	}
}

// SPKIDigest is the anchor a certificate is pinned by.
func SPKIDigest(cert *x509.Certificate) []byte {
	digest := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return digest[:]
}
