//go:build snmp_integration_t4

package integration

import (
	"strings"
	"testing"
)

func TestT4TargetAddress(t *testing.T) {
	for _, tt := range []struct {
		name      string
		input     string
		address   string
		community string
	}{
		{name: "IPv4", input: "192.0.2.1@public", address: "192.0.2.1:161", community: "public"},
		{name: "IPv6", input: "[2001:db8::1]:1161@public", address: "[2001:db8::1]:1161", community: "public"},
		{name: "community separator", input: "router@fixture@secret", address: "router:161", community: "fixture@secret"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			targets, err := parseT4Targets(tt.input)
			if err != nil || len(targets) != 1 {
				t.Fatalf("got %d targets and error %v, want one target", len(targets), err)
			}
			if got := targets[0].Address(); got != tt.address {
				t.Errorf("Address() = %q, want %q", got, tt.address)
			}
			if targets[0].Community != tt.community {
				t.Error("parsed community differs from input")
			}
		})
	}
}

func TestT4TargetErrorOmitsCommunity(t *testing.T) {
	_, err := parseT4Targets("router:bad@fixture-secret@tail")
	if err == nil {
		t.Fatal("invalid port accepted")
	}
	if strings.Contains(err.Error(), "fixture-secret") || strings.Contains(err.Error(), "tail") {
		t.Fatal("parser error includes community content")
	}
}
