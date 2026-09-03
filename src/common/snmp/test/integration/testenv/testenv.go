// Package testenv holds the shared environment plumbing for the SNMP
// integration test suite: the per-tier agent target ([SetTarget] /
// [Target]) and the Docker / containerlab availability gates.
//
// Tiers reach the wire by calling the public [snmp.NewSession] /
// [snmp.ListenTraps] constructors directly — there is no backend swap
// seam, since the SNMP implementation lives in package snmp itself.
// Startup helpers are selected by the owning tier's build tag. Their tests use
// fake containers and executables; for example, from the repository root:
//
//	go test -race -short -tags=snmp_integration_t1 ./src/common/snmp/test/integration/testenv
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

// SetTarget records the agent target a tier's TestMain has brought online.
// It is safe to call concurrently with Target. Tiers set it before m.Run so
// all tests use the same target.
func SetTarget(addr string) {
	target.Store(addr)
}

// Target returns the last address passed to [SetTarget], or an empty string
// before the first call. It is safe for concurrent use. Tests pass this address
// to snmp.NewSession, which rejects an empty target.
func Target() string {
	if v, ok := target.Load().(string); ok {
		return v
	}
	return ""
}

// SkipIfNoDocker skips a test when the Docker CLI is absent from PATH.
// It does not check the daemon. TestMain uses [HasDocker] directly because
// testing.M cannot skip an individual test.
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
