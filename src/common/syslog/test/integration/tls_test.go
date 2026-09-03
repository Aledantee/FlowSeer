package integration_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/syslog"
)

func certificates(t testing.TB, padding int) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "syslog test"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, IsCA: true}
	if padding > 0 {
		template.ExtraExtensions = []pkix.Extension{{Id: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 32473, 1}, Value: make([]byte, padding)}}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	return &tls.Config{Certificates: []tls.Certificate{cert}, ClientCAs: pool, MinVersion: tls.VersionTLS12}, &tls.Config{RootCAs: pool, ServerName: "localhost", Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

func closeChecked(t testing.TB, c interface{ Close() error }) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Error(err)
	}
}

func TestTransportsAndTLSVerification(t *testing.T) {
	server, client := certificates(t, 0)
	server.ClientAuth = tls.RequireAndVerifyClientCert
	for _, transport := range []syslog.Transport{syslog.UDP, syslog.TCP, syslog.TLS} {
		t.Run(string(transport), func(t *testing.T) {
			r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: transport, Address: "127.0.0.1:0", TLSConfig: server}}, syslog.ReceiverOptions{Parse: syslog.ParseOptions{CaptureRaw: true}})
			if err != nil {
				t.Fatal(err)
			}
			defer closeChecked(t, r)
			s, err := syslog.NewSender(r.Addresses()[0].Address, transport, syslog.SenderOptions{TLSConfig: client})
			if err != nil {
				t.Fatal(err)
			}
			defer closeChecked(t, s)
			record := syslog.Record{Priority: syslog.Text("13"), Content: []byte("owned\x00binary\nbody")}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			report, err := s.Send(ctx, record, syslog.EncodeOptions{Format: syslog.RFC5424})
			if err != nil || report.Delivery != syslog.Written {
				t.Fatal(report, err)
			}
			got, err := r.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if string(got.Content) != string(record.Content) || got.Observation.Transport != transport || got.Raw == nil || !got.Observation.Peer.IsValid() {
				t.Fatal(got)
			}
			if transport == syslog.TLS && !got.Observation.Authenticated {
				t.Fatal("missing client verification")
			}
		})
	}
	r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.TLS, Address: "127.0.0.1:0", TLSConfig: server}}, syslog.ReceiverOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer closeChecked(t, r)
	for _, config := range []*tls.Config{{ServerName: "wrong.example", RootCAs: client.RootCAs}, {ServerName: "localhost"}} {
		s, err := syslog.NewSender(r.Addresses()[0].Address, syslog.TLS, syslog.SenderOptions{TLSConfig: config})
		if err != nil {
			t.Fatal(err)
		}
		report, err := s.Send(context.Background(), syslog.Record{Priority: syslog.Text("13")}, syslog.EncodeOptions{Format: syslog.RFC5424})
		closeChecked(t, s)
		if err == nil || report.Delivery != syslog.NotSent {
			t.Fatal("unverified TLS accepted", report, err)
		}
	}
}

func TestTLSHeavyCertificateAndStalledHandshake(t *testing.T) {
	server, client := certificates(t, 120<<10)
	server.ClientAuth = tls.RequireAndVerifyClientCert
	r, err := syslog.Listen(context.Background(), []syslog.ListenConfig{{Transport: syslog.TLS, Address: "127.0.0.1:0", TLSConfig: server}}, syslog.ReceiverOptions{Limits: syslog.Limits{MaxConnections: 4, MaxHandshakes: 2, HandshakeTimeout: 100 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	defer closeChecked(t, r)
	s, err := syslog.NewSender(r.Addresses()[0].Address, syslog.TLS, syslog.SenderOptions{TLSConfig: client})
	if err != nil {
		t.Fatal(err)
	}
	defer closeChecked(t, s)
	if _, err := s.Send(context.Background(), syslog.Record{Priority: syslog.Text("13")}, syslog.EncodeOptions{Format: syslog.RFC5424}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := r.Next(ctx); err != nil {
		t.Fatal(err)
	}
	stalled, err := net.Dial("tcp", r.Addresses()[0].Address)
	if err != nil {
		t.Fatal(err)
	}
	defer closeChecked(t, stalled)
	time.Sleep(150 * time.Millisecond)
	if r.Stats().ActiveHandshakes != 0 || r.Stats().HandshakeErrors == 0 {
		t.Fatal(r.Stats())
	}
	start := time.Now()
	closeChecked(t, r)
	if time.Since(start) > time.Second || r.Stats().ReservedBytes != 0 {
		t.Fatal("shutdown bound", r.Stats())
	}
}
