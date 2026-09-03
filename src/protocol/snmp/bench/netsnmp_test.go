//go:build snmp_bench_macro

package bench

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestNetSNMPCommandArguments(t *testing.T) {
	cases := []struct {
		name      string
		community string
	}{
		{name: "plain", community: "public"},
		{name: "spaces_and_quote", community: "read only ' community"},
		{name: "shell_expansion", community: "$(printf substituted); printf injected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, walk := range []bool{false, true} {
				command := snmpgetCmd("127.0.0.1:1161", tc.community, defaultScalarOID)
				want := []string{"-v", "2c", "-c", tc.community, "-t", "1", "-r", "1", "-On"}
				if walk {
					command = snmpbulkwalkCmd("127.0.0.1:1161", tc.community, defaultScalarOID)
					want = append(want, "-Cr50")
				}
				want = append(want, "127.0.0.1:1161", defaultScalarOID)
				// Shell functions capture argv without starting a client or opening a socket.
				script := `snmpget() { printf '%s\000' "$@"; }; snmpbulkwalk() { printf '%s\000' "$@"; }; ` + command
				out, err := exec.CommandContext(t.Context(), "sh", "-c", script).Output()
				if err != nil {
					t.Errorf("walk=%t: shell command: %v", walk, err)
					continue
				}
				got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
				if !reflect.DeepEqual(got, want) {
					t.Errorf("walk=%t: got argv %q, want %q", walk, got, want)
				}
			}
		})
	}
}
