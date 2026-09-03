package integration_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestBrokerStoreSurvivesAbruptProcessExit(t *testing.T) {
	executable := buildBrokerHelper(t)
	storeDir := t.TempDir()
	holder := startBrokerHelper(t, executable, "hold", storeDir, "")
	ready := holder.waitForPrefix(t, "READY ")
	sequence := helperSequence(t, ready)

	locked := exec.Command(executable, "-test.run=^TestBrokerHelperProcess$", "-test.count=1")
	locked.Env = append(os.Environ(),
		"FLOWSEER_BROKER_HELPER_MODE=reopen",
		"FLOWSEER_BROKER_HELPER_STORE="+storeDir,
		"FLOWSEER_BROKER_HELPER_SEQUENCE="+sequence,
	)
	output, err := locked.CombinedOutput()
	if err == nil {
		t.Fatalf("second process opened the active store:\n%s", output)
	}
	if !strings.Contains(string(output), "local bus store is already in use") {
		t.Fatalf("second process returned the wrong failure: %v\n%s", err, output)
	}

	holder.killAndWait(t)
	reopened := startBrokerHelper(t, executable, "reopen", storeDir, sequence)
	if got := reopened.waitForPrefix(t, "FOUND "); got != "FOUND durable" {
		t.Fatalf("reopened helper output = %q", got)
	}
	if err := reopened.command.Wait(); err != nil {
		t.Fatalf("reopened helper failed: %v\n%s", err, reopened.stderr.String())
	}
}
