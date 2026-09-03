package syslog

import (
	"crypto/tls"
	"testing"
)

func TestDynamicTLSConfigurationValidation(t *testing.T) {
	base := &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return &tls.Config{MinVersion: tls.VersionTLS10, Certificates: []tls.Certificate{{}}}, nil
	}}
	config, err := tlsConfiguration(base, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.GetConfigForClient(&tls.ClientHelloInfo{}); err == nil {
		t.Fatal("dynamic TLS configuration bypassed minimum version")
	}
	if base.MinVersion != 0 {
		t.Fatal("caller TLS configuration mutated")
	}
}
