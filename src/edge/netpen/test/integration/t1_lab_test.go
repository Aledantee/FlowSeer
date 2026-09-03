//go:build netpen_t1

package integration

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// startFRRLab returns cleanup even after a partial startup failure. The caller
// must run it before exiting to remove containers and report teardown errors.
func startFRRLab(ctx context.Context, composeFile string) (string, func() error, error) {
	pull := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "pull")
	if out, err := pull.CombinedOutput(); err != nil {
		return "", func() error { return nil }, fmt.Errorf("docker compose pull: %w\n%s", err, out)
	}

	cleanup := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		down := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "down", "-v", "--remove-orphans")
		if out, err := down.CombinedOutput(); err != nil {
			return fmt.Errorf("docker compose down: %w\n%s", err, out)
		}
		return nil
	}

	up := exec.CommandContext(ctx, "docker", "compose", "-f", composeFile, "up", "-d")
	if out, err := up.CombinedOutput(); err != nil {
		return "", cleanup, fmt.Errorf("docker compose up: %w\n%s", err, out)
	}

	// Wait for OSPF adjacency to form. We check r1's OSPF neighbor list
	// via `vtysh -c "show ip ospf neighbor"` inside the container.
	target := "10.99.0.11"
	if err := waitForOSPFAdjacency(ctx, "netpen-t1-frr-r1", "10.99.0.12"); err != nil {
		return "", cleanup, fmt.Errorf("OSPF adjacency did not form: %w", err)
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
