package syslog

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"testing"
	"time"
)

func TestTLSAlertWriteDeadline(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	cfg := &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12, SessionTicketsDisabled: true}
	limits, err := (Limits{IdleTimeout: 50 * time.Millisecond, FrameTimeout: 50 * time.Millisecond}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	admission, err := newAdmission(limits, 0)
	if err != nil {
		t.Fatal(err)
	}
	receiver := &Receiver{limits: limits, admission: admission, queue: make(chan receivedFrame, 1), handshakes: make(chan struct{}, 1)}
	left, right := net.Pipe()
	done := make(chan struct{})
	t.Cleanup(func() {
		_ = left.Close()
		_ = right.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("receiver did not stop")
		}
	})
	go func() {
		defer close(done)
		receiver.receiveStream(context.Background(), boundListener{config: ListenConfig{Transport: TLS, Framing: OctetCounting, TLSConfig: cfg}}, left)
	}()
	client := tls.Client(right, &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.HandshakeContext(ctx); err != nil {
		t.Fatal(err)
	}
	// A bad encrypted record triggers an alert write to a peer that no longer reads.
	if _, err := right.Write([]byte{23, 3, 3, 0, 1, 0}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("TLS alert write exceeded receive deadline")
	}
	if got := receiver.Stats(); got.ReservedFrames != 0 || got.ReservedBytes != 0 || got.FramingErrors != 1 {
		t.Fatalf("got %+v", got)
	}
}
