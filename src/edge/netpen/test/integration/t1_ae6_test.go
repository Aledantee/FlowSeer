//go:build netpen_t1

package integration

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/test/integration/testenv"
)

var supersetAttacks = []string{
	"ospf",
	"eigrp",
	"wpad",
	"etherchannel",
	"mld",
	"raflood",
	"lldpspoof",
	"glbp",
}

// TestAE6_SupersetReproducibility compares the record kinds and finding modules
// from two successful runs of each attack inside FRR r1 on the lab segment.
// This checks reproducibility; it does not assert vendor behavior or wire shape.
func TestAE6_SupersetReproducibility(t *testing.T) {
	if testing.Short() {
		t.Skip("live t1 lab disabled in short mode")
	}
	if testenv.Target() == "" {
		t.Skip("no t1 target; lab did not start")
	}

	for _, attack := range supersetAttacks {
		t.Run(attack, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			class1 := runAttackClass(ctx, t, attack)
			class2 := runAttackClass(ctx, t, attack)
			if class1 != class2 {
				t.Fatalf("AE6 %s: findings class differs between runs\n  run1: %s\n  run2: %s",
					attack, class1, class2)
			}
			t.Logf("AE6 %s: reproducible (class=%s)", attack, class1)
		})
	}
}

func runAttackClass(ctx context.Context, t *testing.T, attack string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "docker", "exec", "netpen-t1-frr-r1",
		"netpen", attack, "--json=true", "-i", "eth0", "--duration", "2s", "--timeout", "20s")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("netpen %s: %v\nstderr: %s\nstdout: %s", attack, err, &stderr, out)
	}
	class, err := parseFindingsClass(string(out))
	if err != nil {
		t.Fatalf("netpen %s output: %v\n%s", attack, err, out)
	}
	return class
}
