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
		"FLOWSEER_BROKER_HELPER_FSYNC_POLICY=periodic",
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

func TestDurableHandlerResumesAfterAbruptProcessExit(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess durability test")
	}
	executable := buildBrokerHelper(t)
	storeDir := t.TempDir()
	first := startBrokerHelper(t, executable, "delivery_crash", storeDir, "")
	if got := first.waitForPrefix(t, "HANDLED "); got != "HANDLED before-crash" {
		t.Fatalf("first delivery output = %q", got)
	}
	first.killAndWait(t)

	second := startBrokerHelper(t, executable, "delivery_reopen", storeDir, "")
	if got := second.waitForPrefix(t, "RECOVERED "); got != "RECOVERED after-crash" {
		t.Fatalf("recovered delivery output = %q", got)
	}
	if err := second.command.Wait(); err != nil {
		t.Fatalf("recovery helper failed: %v\n%s", err, second.stderr.String())
	}
}

func TestCommittedRetryResumesAfterAbruptProcessExit(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess durability test")
	}
	executable := buildBrokerHelper(t)
	storeDir := t.TempDir()
	first := startBrokerHelper(t, executable, "delivery_retry_crash", storeDir, "")
	if got := first.waitForPrefix(t, "RETRIED "); got != "RETRIED after-settlement" {
		t.Fatalf("retry delivery output = %q", got)
	}
	first.killAndWait(t)

	second := startBrokerHelper(t, executable, "delivery_retry_reopen", storeDir, "")
	if got := second.waitForPrefix(t, "RECOVERED "); got != "RECOVERED retry-after-crash" {
		t.Fatalf("recovered retry output = %q", got)
	}
	if err := second.command.Wait(); err != nil {
		t.Fatalf("retry recovery helper failed: %v\n%s", err, second.stderr.String())
	}
}
