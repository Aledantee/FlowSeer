// Package testenv holds the shared environment plumbing for the netpen
// integration test suite: Docker / containerlab availability gates and
// the per-tier target address plumbing.
//
// Tiers reach the wire through the real link.Open + runner path; there is
// no backend-swap seam. The test environment provides the Docker
// orchestration (containerlab topology or plain docker compose) and the
// target address the tests connect to.
package testenv

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// target carries the lab target address established by a tier's TestMain.
var target atomic.Value // string

// SetTarget records the lab target a tier's TestMain has brought online.
func SetTarget(addr string) {
	target.Store(addr)
}

// Target returns the lab target previously set by SetTarget, or "".
func Target() string {
	if v, ok := target.Load().(string); ok {
		return v
	}
	return ""
}

// HasDocker reports whether the Docker CLI is on PATH.
func HasDocker() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// HasContainerlab reports whether containerlab is on PATH.
func HasContainerlab() bool {
	_, err := exec.LookPath("containerlab")
	return err == nil
}

// DockerDaemonRunning reports whether the Docker daemon is responsive.
// HasDocker only checks the CLI binary; this probes the actual socket.
func DockerDaemonRunning() bool {
	if !HasDocker() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	out, err := cmd.Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// SkipIfNoDocker calls t.Skip when Docker is not available.
func SkipIfNoDocker(t testing.TB) {
	t.Helper()
	if !HasDocker() {
		t.Skip("docker not found on PATH; install Docker to run this tier")
	}
}

// DockerComposeVersion returns the `docker compose version` output, or
// empty if docker compose is unavailable.
func DockerComposeVersion() string {
	if !HasDocker() {
		return ""
	}
	out, err := exec.Command("docker", "compose", "version", "--format", "{{.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ErrDockerUnavailable is returned by tier start functions when the
// Docker daemon is not running, so TestMain can distinguish "skip" from
// "fail".
var ErrDockerUnavailable = fmt.Errorf("docker daemon not running")

// IsDockerUnavailable reports whether err is ErrDockerUnavailable.
func IsDockerUnavailable(err error) bool {
	return err == ErrDockerUnavailable
}
