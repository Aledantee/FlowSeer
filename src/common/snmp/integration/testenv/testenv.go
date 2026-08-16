// Package testenv holds the shared environment plumbing for the SNMP
// integration test suite: the per-tier agent target ([SetTarget] /
// [Target]) and the Docker / containerlab availability gates.
//
// Tiers reach the wire by calling the public [snmp.NewSession] /
// [snmp.ListenTraps] constructors directly — there is no backend swap
// seam, since the SNMP implementation lives in package snmp itself.
package testenv

import (
	"os/exec"
	"sync/atomic"
	"testing"
)

// Environment variable names recognized by the integration tiers.
// Declared as exported constants so an operator (or a tooling agent)
// can survey the package via `go doc` and discover the override
// knobs without grepping _test.go files.
const (
	// EnvT2HostFromContainer overrides the address the SR Linux
	// container uses to reach the host's trap listener. Default is
	// "host.docker.internal" (resolves on Docker Desktop). Linux
	// Docker Engine callers should set this to the docker0 bridge
	// gateway IP or a LAN IP the lab can route to.
	EnvT2HostFromContainer = "FLOWSEER_T2_HOST_FROM_CONTAINER"

	// EnvT2TrapPort overrides the host-side UDP port the T2 trap
	// listener binds. Default is 12162. Set to a free port if 12162
	// collides on the developer's host or with another concurrent
	// integration run.
	EnvT2TrapPort = "FLOWSEER_T2_TRAP_PORT"
)

// target carries the agent address established by a tier's TestMain.
// Stored as atomic.Value so a tier that runs concurrent subtests
// observing the target sees a consistent value once TestMain is past
// SetTarget.
var target atomic.Value // string

// SetTarget records the agent target a tier's TestMain has brought
// online. Tests in that tier read it back via [Target]. Tier TestMains
// call SetTarget exactly once before m.Run.
func SetTarget(addr string) {
	target.Store(addr)
}

// Target returns the agent target previously set by [SetTarget], or the
// empty string if no tier TestMain has run. Test code uses the return
// value as the target argument to [snmp.NewSession]. The empty-string default
// surfaces as a Dial error from the underlying Backend rather than a
// silent connection to localhost.
func Target() string {
	if v, ok := target.Load().(string); ok {
		return v
	}
	return ""
}

// SkipIfNoDocker calls t.Skip with a clear message when the Docker CLI
// is not on PATH. Tier TestMains call this before any docker invocation
// so a developer without Docker sees a skip instead of a confusing
// exec.ErrNotFound surface. The check is intentionally cheap (a single
// PATH lookup) so it can run on every TestMain.
func SkipIfNoDocker(t testing.TB) {
	t.Helper()
	if !HasDocker() {
		t.Skip("docker not found on PATH; install Docker to run this tier")
	}
}

// HasDocker reports whether the Docker CLI is on PATH. Tier TestMains
// (which receive a *testing.M and have no *testing.T to skip from)
// branch on this before attempting container orchestration.
func HasDocker() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// SkipIfNoContainerlab calls t.Skip when the containerlab binary is
// not on PATH. T2 tests use this so a developer without containerlab
// installed sees a skip with a clear remediation, not a confusing
// exec.ErrNotFound failure.
func SkipIfNoContainerlab(t testing.TB) {
	t.Helper()
	if !HasContainerlab() {
		t.Skip("containerlab not found on PATH; install from https://containerlab.dev/ to run this tier")
	}
}

// HasContainerlab reports whether the containerlab binary is on PATH.
// T2's TestMain branches on this for the no-*testing.T case.
func HasContainerlab() bool {
	_, err := exec.LookPath("containerlab")
	return err == nil
}
