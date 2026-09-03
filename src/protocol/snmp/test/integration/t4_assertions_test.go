//go:build snmp_integration_t4

package integration

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDecoderLeniencyRejectsTransportFailure(t *testing.T) {
	if os.Getenv("SNMP_TEST_DECODER_FAILURE") == "1" {
		assertDecoderLeniency(t, context.DeadlineExceeded, "sysServices.0")
		return
	}
	cmd := exec.Command(os.Args[0], "-test.short", "-test.run=^TestDecoderLeniencyRejectsTransportFailure$")
	cmd.Env = append(os.Environ(), "SNMP_TEST_DECODER_FAILURE=1")
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("got subprocess error %v, want assertion failure (exit 1)\n%s", err, out)
	}
	if !strings.Contains(string(out), "sysServices.0") || !strings.Contains(string(out), "context deadline exceeded") {
		t.Errorf("failure lost operation or cause: %s", out)
	}
}
