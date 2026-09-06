package integration

import (
	"strings"
	"testing"
)

func TestParseT4Targets(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		addr string
	}{
		{name: "hostname", raw: "router@operator:secret", addr: "router:830"},
		{name: "explicit_port", raw: "127.0.0.1:1830@operator:secret", addr: "127.0.0.1:1830"},
		{name: "ipv6", raw: "[::1]:830@operator:secret", addr: "[::1]:830"},
		{name: "missing_user", raw: "router@:secret"},
		{name: "missing_separator", raw: "router:operator:secret"},
		{name: "invalid_port", raw: "router:bad@operator:secret"},
		{name: "port_overflow", raw: "router:65536@operator:secret"},
		{name: "empty_host", raw: ":830@operator:secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseT4Targets(tc.raw)
			if tc.addr == "" {
				if err == nil {
					t.Fatal("parse returned nil error, want malformed-target error")
				}
				if strings.Contains(err.Error(), "secret") {
					t.Error("parse error exposes the password")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if len(got) != 1 || got[0].Addr != tc.addr || got[0].User != "operator" || !got[0].Password.EqualString("secret") {
				t.Error("parsed target differs from the requested address and credentials")
			}
		})
	}
}
