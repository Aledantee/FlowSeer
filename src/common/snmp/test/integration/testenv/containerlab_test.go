//go:build snmp_integration_t2

package testenv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSRLinuxReadinessCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForSRLinuxReady(ctx, "invalid:port"); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestSRLinuxPartialDeployCleanupError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	script := "#!/bin/sh\ncase \"$1\" in\n deploy) printf 'deploy-failed\\n' >&2; exit 1;;\n destroy) printf 'destroy-failed\\n' >&2; exit 2;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "containerlab"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, cleanup, err := StartSRLinux(context.Background(), "fake-topology.yml")
	if cleanup != nil {
		t.Error("failed startup returned a cleanup callback")
		if err := cleanup(); err != nil {
			t.Errorf("unexpected callback cleanup: %v", err)
		}
	}
	if err == nil || !strings.Contains(err.Error(), "deploy-failed") || !strings.Contains(err.Error(), "destroy-failed") {
		t.Errorf("got %v, want deployment and cleanup diagnostics", err)
	}
}
