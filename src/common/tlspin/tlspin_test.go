package tlspin_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/common/tlspin"
)

func TestPinVerifierRejectsAnUnanchoredCertificate(t *testing.T) {
	anchored := selfSigned(t)
	other := selfSigned(t)
	verify := tlspin.PinVerifier([][]byte{tlspin.SPKIDigest(anchored)})
	if err := verify([][]byte{anchored.Raw}, nil); err != nil {
		t.Fatalf("anchored certificate rejected: %v", err)
	}
	err := verify([][]byte{other.Raw}, nil)
	if code, ok := errs.CodeOf(err); !ok || code != tlspin.ErrCodePin {
		t.Fatalf("unanchored certificate: err=%v code=%q", err, code)
	}
	if err := verify(nil, nil); err == nil {
		t.Fatal("empty chain accepted")
	}
}

func TestPinVerifierRejectsAnUnparsableCertificate(t *testing.T) {
	verify := tlspin.PinVerifier(nil)
	err := verify([][]byte{[]byte("not DER")}, nil)
	if code, ok := errs.CodeOf(err); !ok || code != tlspin.ErrCodePin {
		t.Fatalf("unparsable certificate: err=%v code=%q, want ErrCodePin", err, code)
	}
}

func selfSigned(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "flowseer-central"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return cert
}

func TestTheEmptyAnchorSetRejectsEveryCertificate(t *testing.T) {
	certificate := selfSigned(t)
	digest := tlspin.SPKIDigest(certificate)

	err := tlspin.PinnedTLSConfig(nil).VerifyPeerCertificate([][]byte{certificate.Raw}, nil)
	if code, ok := errs.CodeOf(err); !ok || code != tlspin.ErrCodePin {
		t.Errorf("empty anchors: err=%v code=%q, want ErrCodePin", err, code)
	}
	if err := tlspin.PinnedTLSConfig([][]byte{digest}).VerifyPeerCertificate([][]byte{certificate.Raw}, nil); err != nil {
		t.Errorf("the matching anchor was rejected: %v", err)
	}
	other := make([]byte, len(digest))
	err = tlspin.PinnedTLSConfig([][]byte{other}).VerifyPeerCertificate([][]byte{certificate.Raw}, nil)
	if code, ok := errs.CodeOf(err); !ok || code != tlspin.ErrCodePin {
		t.Errorf("non-matching anchor: err=%v code=%q, want ErrCodePin", err, code)
	}
}
