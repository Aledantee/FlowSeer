//go:build netpen_t2

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	flowssh "go.aledante.io/FlowSeer/src/protocol/ssh"

	"go.aledante.io/FlowSeer/src/edge/netpen/test/integration/lab"
)

const ospfAttackerRouterID = "10.0.0.99"

func TestT2OSPFLiveLab(t *testing.T) {
	if testing.Short() {
		t.Skip("live OSPF vendor assertion disabled in short mode")
	}
	if t2Config.TargetPlatform != "iosxe" {
		t.Fatalf("target platform = %q, want iosxe", t2Config.TargetPlatform)
	}

	session, err := flowssh.Dial(t.Context(), lab.SSHAddress(t2Config.TargetHost), flowssh.Options{
		Username:        t2Config.TargetUser,
		Password:        t2Config.TargetPassword,
		HostKeySHA256:   t2Config.TargetHostKeySHA256,
		CommandDeadline: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("connect to target: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	ospf, err := session.Run(t.Context(), lab.IOSXEOSPFStatusCommand())
	if err != nil {
		t.Fatalf("read target OSPF status: %v", err)
	}
	if ospf.Truncated {
		t.Fatal("target OSPF status was truncated")
	}
	status := strings.ToLower(string(ospf.Output))
	if !strings.Contains(status, "routing process") || !strings.Contains(status, "ospf") {
		t.Skip("target OSPF prerequisite is not active; configure the injection-facing interface before the live run")
	}

	firstClass := runOSPFInjection(t.Context(), t)
	secondClass := runOSPFInjection(t.Context(), t)
	if firstClass != secondClass {
		t.Fatalf("OSPF finding class changed between injections: first %q, second %q", firstClass, secondClass)
	}

	presentOutput, err := waitForOSPFNeighbor(t.Context(), session, true, 10*time.Second)
	if err != nil {
		t.Fatalf("spoofed OSPF neighbor did not appear: %v\nlast output:\n%s", err, presentOutput)
	}
	clearedOutput, err := waitForOSPFNeighbor(t.Context(), session, false, 40*time.Second)
	if err != nil {
		t.Fatalf("spoofed OSPF neighbor did not clear within the dead interval: %v\nlast output:\n%s", err, clearedOutput)
	}

	t.Logf("validation matrix evidence: ospf; t1 AE6=t2 (b); t2=IOS-XE neighbor %s accepted and cleared; target=%s; finding-class=%s", ospfAttackerRouterID, t2Config.TargetHost, firstClass)
}

func runOSPFInjection(ctx context.Context, t *testing.T) string {
	t.Helper()
	argv := t2Config.InjectCommand("ospf", 2*time.Second, 20*time.Second)
	injectionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result, err := lab.RunInjector(injectionCtx, t2Config, argv)
	if err != nil {
		t.Fatalf("run OSPF injector: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("OSPF injector exit code = %d\nstderr: %s\nstdout: %s", result.ExitCode, result.Stderr, result.Stdout)
	}
	class, err := parseFindingsClass(string(result.Stdout))
	if err != nil {
		t.Fatalf("parse OSPF finding class: %v\n%s", err, result.Stdout)
	}
	if !strings.Contains(class, "finding:ospf") {
		t.Fatalf("OSPF finding class = %q, want finding:ospf", class)
	}
	return class
}

func waitForOSPFNeighbor(ctx context.Context, session *flowssh.Session, wantPresent bool, timeout time.Duration) (string, error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var lastOutput string
	for {
		result, err := session.Run(ctx, lab.IOSXEOSPFNeighborCommand())
		if err != nil {
			return lastOutput, fmt.Errorf("read OSPF neighbors: %w", err)
		}
		if result.Truncated {
			return string(result.Output), fmt.Errorf("OSPF neighbor output was truncated")
		}
		lastOutput = string(result.Output)
		present := lab.HasNeighbor(lab.ParseOSPFNeighbors(lastOutput), ospfAttackerRouterID)
		if present == wantPresent {
			return lastOutput, nil
		}

		select {
		case <-ctx.Done():
			return lastOutput, ctx.Err()
		case <-timer.C:
			return lastOutput, fmt.Errorf("timed out after %s", timeout)
		case <-ticker.C:
		}
	}
}
