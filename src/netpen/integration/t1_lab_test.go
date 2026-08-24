//go:build netpen_t1

package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// startFRRLab brings up the FRR docker-compose topology, waits for the
// OSPF adjacency to form between r1 and r2, and returns the target
// address (r1's IP on the lab bridge) plus a cleanup function that
// tears the lab down. Uses plain docker compose (containerlab is not
// installed on this host); the containerlab branch is documented but
// not exercised here.
//
// The returned target is r1's IP (10.99.0.11) — the router netpen's
// OSPF injection attacks.
func startFRRLab(ctx context.Context, composeFile string) (string, func(), error) {
	// Pull images first (may take a while on first run).
	pull := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "pull")
	if out, err := pull.CombinedOutput(); err != nil {
		return "", func() {}, fmt.Errorf("docker compose pull: %w\n%s", err, out)
	}

	// Bring up the topology.
	up := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "up", "-d")
	if out, err := up.CombinedOutput(); err != nil {
		return "", func() {}, fmt.Errorf("docker compose up: %w\n%s", err, out)
	}

	cleanup := func() {
		down := exec.Command("docker", "compose", "-f", composeFile, "down", "-v", "--remove-orphans")
		_ = down.Run()
	}

	// Wait for OSPF adjacency to form. We check r1's OSPF neighbor list
	// via `vtysh -c "show ip ospf neighbor"` inside the container.
	target := "10.99.0.11"
	if err := waitForOSPFAdjacency(ctx, "netpen-t1-frr-r1", "10.99.0.12"); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("OSPF adjacency did not form: %w", err)
	}

	return target, cleanup, nil
}

// waitForOSPFAdjacency polls the FRR container's vtysh until the OSPF
// neighbor reaches Full state. Timeout is the context deadline.
func waitForOSPFAdjacency(ctx context.Context, container, neighborIP string) error {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(3 * time.Minute)
	}

	for {
		// Check OSPF neighbor state via vtysh inside the container.
		cmd := exec.CommandContext(ctx, "docker", "exec", container,
			"vtysh", "-c", "show ip ospf neighbor")
		out, err := cmd.Output()
		if err == nil {
			text := string(out)
			// A Full adjacency line looks like:
			// 10.99.0.12    1   Full/DR    ...    ...
			if strings.Contains(text, neighborIP) && strings.Contains(text, "Full") {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for OSPF Full adjacency with %s", neighborIP)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
