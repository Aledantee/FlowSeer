//go:build netpen_t1

package integration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/test/integration/testenv"
)

// TestAE5_StaticBinaryFullCompletion checks successful completion and a complete
// JSONL stream from the binary installed in FRR r1. Static linking of the local
// release artifact is checked separately; this test does not verify that the
// installed binary is that artifact or that the container is air-gapped.
func TestAE5_StaticBinaryFullCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("live t1 lab disabled in short mode")
	}
	target := testenv.Target()
	if target == "" {
		t.Skip("no t1 target; lab did not start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "exec", "netpen-t1-frr-r1",
		"netpen", "full", "--json=true", "-i", "eth0", "--duration", "3s")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("netpen full: %v\nstderr: %s\nstdout: %s", err, &stderr, out)
	}

	class, err := parseFindingsClass(string(out))
	if err != nil {
		t.Fatalf("netpen full output: %v\n%s", err, out)
	}
	t.Logf("AE5: netpen full completed (class=%s)", class)
}

// TestAE5_ReleaseBinaryStatic checks that file identifies the local amd64
// release artifact as statically linked. It does not execute that binary.
func TestAE5_ReleaseBinaryStatic(t *testing.T) {
	binary := findReleaseBinary(t)
	if binary == "" {
		t.Skip("no release binary in dist/; run `task release` first")
	}
	cmd := exec.Command("file", binary)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("file %s: %v", binary, err)
	}
	text := string(out)
	if !strings.Contains(text, "statically linked") {
		t.Errorf("release binary is not statically linked: %s", text)
	}
	t.Logf("AE5 file check: %s", strings.TrimSpace(text))
}

func findReleaseBinary(t *testing.T) string {
	t.Helper()
	candidate := filepath.Join("..", "..", "dist", "netpen-linux-amd64")
	info, err := os.Stat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatalf("stat release binary: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("release binary %s is not a regular file", candidate)
	}
	return candidate
}
