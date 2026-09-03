// Package testenv shares lab targets and checks Docker/containerlab tools
// for netpen integration tiers. Tier packages own lab setup and teardown.
package testenv

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// target carries the lab target address established by a tier's TestMain.
var target atomic.Value // string

// SetTarget records the lab target a tier's TestMain has brought online.
// It is safe to call concurrently with Target or another SetTarget call.
func SetTarget(addr string) {
	target.Store(addr)
}

// Target returns the lab target previously set by [SetTarget], or "".
// It is safe for concurrent use.
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
// The CLI call has a five-second deadline and at most one second to drain
// its output pipes. A failed or empty response returns false.
func DockerDaemonRunning() bool {
	return dockerOutput("info", "--format", "{{.ServerVersion}}") != ""
}

// SkipIfNoDocker calls t.Skip when Docker is not available.
func SkipIfNoDocker(t testing.TB) {
	t.Helper()
	if !HasDocker() {
		t.Skip("docker not found on PATH; install Docker to run this tier")
	}
}

// DockerComposeVersion returns the trimmed Compose version, or an empty
// string when the CLI fails, returns no version, or exceeds the five-second
// deadline. Output pipes have at most one additional second to drain.
func DockerComposeVersion() string {
	return dockerOutput("compose", "version", "--format", "{{.Version}}")
}

func dockerOutput(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// ErrDockerUnavailable is returned by tier start functions when the
// Docker daemon is not running, so TestMain can distinguish "skip" from
// "fail".
var ErrDockerUnavailable = errs.Msg("docker daemon not running")

// IsDockerUnavailable reports whether err wraps [ErrDockerUnavailable].
func IsDockerUnavailable(err error) bool {
	return errors.Is(err, ErrDockerUnavailable)
}
