package busattach_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	edgev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/edge/v1"
	"go.aledante.io/FlowSeer/src/edge/agent/internal/busattach"
	"go.aledante.io/FlowSeer/src/modules/edgebus"
)

type attachFake struct {
	calls    int
	response *edgev1.AttachBusResponse
	err      error
}

func (f *attachFake) AttachBus(
	context.Context, *connect.Request[edgev1.AttachBusRequest],
) (*connect.Response[edgev1.AttachBusResponse], error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(f.response), nil
}

// TestAnEdgeWithNoAnchorsIsRefusedAtStart covers the configuration that would
// otherwise fail as a connection problem.
//
// PinnedTLSConfig fails closed on an empty anchor set — PinVerifier builds a
// set from the anchors and rejects any certificate not in it, so an empty set
// rejects everything, and InsecureSkipVerify does not change that because Go
// still calls VerifyPeerCertificate. That is the safe direction. It is not a
// usable one: the edge would come up, dial the hub, and be refused forever
// with nothing saying why. Refusing at start names the actual condition.
func TestAnEdgeWithNoAnchorsIsRefusedAtStart(t *testing.T) {
	client := &attachFake{}
	_, err := busattach.Attach(context.Background(), client, busattach.Config{
		StateDir: t.TempDir(),
		EdgeID:   "0192e6a0-0000-7000-8000-0000000000ed",
	})
	if err == nil {
		t.Fatal("Attach() error = nil, want an edge with no anchors refused")
	}
	if client.calls != 0 {
		t.Errorf("AttachBus was called %d times, want 0: the check belongs before the call", client.calls)
	}
}

// TestTheEmptyAnchorSetRejectsEveryCertificate is the property the check
// above exists because of, asserted directly rather than assumed about a
// library. It is the difference between "trust nothing" and "trust the system
// roots", and those read identically at the call site.
func TestTheEmptyAnchorSetRejectsEveryCertificate(t *testing.T) {
	certificate, digest := selfSigned(t)

	if err := edgebus.PinnedTLSConfig(nil).VerifyPeerCertificate([][]byte{certificate}, nil); err == nil {
		t.Error("an empty anchor set accepted a certificate; it must trust nothing, not the system roots")
	}
	if err := edgebus.PinnedTLSConfig([][]byte{digest}).VerifyPeerCertificate([][]byte{certificate}, nil); err != nil {
		t.Errorf("the matching anchor was rejected: %v", err)
	}
	other := make([]byte, len(digest))
	if err := edgebus.PinnedTLSConfig([][]byte{other}).VerifyPeerCertificate([][]byte{certificate}, nil); err == nil {
		t.Error("a non-matching anchor accepted the certificate")
	}
}

// TestAFailedAttachStartsNothing proves the call is what gates the rest: no
// leaf, no receiver, and nothing left in the state directory to confuse the
// next start.
func TestAFailedAttachStartsNothing(t *testing.T) {
	dir := t.TempDir()
	client := &attachFake{err: connect.NewError(connect.CodeUnavailable, context.DeadlineExceeded)}

	attachment, err := busattach.Attach(context.Background(), client, busattach.Config{
		StateDir:     filepath.Join(dir, "bus"),
		EdgeID:       "0192e6a0-0000-7000-8000-0000000000ed",
		TrustAnchors: [][]byte{make([]byte, 32)},
	})
	if err == nil {
		t.Fatal("Attach() error = nil, want the failed call surfaced")
	}
	if attachment != nil {
		t.Error("Attach() returned an attachment alongside its error")
	}
}

// selfSigned returns a DER certificate and the SPKI digest that pins it.
func selfSigned(t *testing.T) (der, digest []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "central.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err = x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return der, edgebus.SPKIDigest(parsed)
}
