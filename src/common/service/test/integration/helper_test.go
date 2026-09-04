package integration_test

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type brokerHelper struct {
	command *exec.Cmd
	stderr  bytes.Buffer
	lines   *bufio.Scanner
	stdin   io.WriteCloser
}

func buildBrokerHelper(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate integration helper source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "..", ".."))
	name := "service-broker-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "test", "-c", "-o", path, "./src/common/service")
	command.Dir = repositoryRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build broker helper: %v\n%s", err, output)
	}
	return path
}

func startBrokerHelper(t *testing.T, executable, mode, storeDir, sequence string) *brokerHelper {
	return startBrokerHelperConfigured(t, executable, mode, storeDir, sequence, "", "")
}

func startBrokerHelperConfigured(t *testing.T, executable, mode, storeDir, sequence, policy, version string) *brokerHelper {
	t.Helper()
	command := exec.Command(executable, "-test.run=^TestBrokerHelperProcess$", "-test.count=1")
	command.Env = append(os.Environ(),
		"FLOWSEER_BROKER_HELPER_MODE="+mode,
		"FLOWSEER_BROKER_HELPER_STORE="+storeDir,
		"FLOWSEER_BROKER_HELPER_SEQUENCE="+sequence,
		"FLOWSEER_BROKER_HELPER_FSYNC_POLICY="+policy,
		"FLOWSEER_BROKER_HELPER_VERSION="+version,
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	helper := &brokerHelper{command: command, lines: bufio.NewScanner(stdout), stdin: stdin}
	command.Stderr = &helper.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if command.ProcessState == nil {
			_ = helper.stdin.Close()
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	return helper
}

func (h *brokerHelper) waitForPrefix(t *testing.T, prefix string) string {
	t.Helper()
	for h.lines.Scan() {
		line := h.lines.Text()
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	if err := h.lines.Err(); err != nil {
		t.Fatalf("read broker helper output: %v", err)
	}
	err := h.command.Wait()
	t.Fatalf("broker helper exited before %q: %v\n%s", prefix, err, h.stderr.String())
	return ""
}

func (h *brokerHelper) killAndWait(t *testing.T) {
	t.Helper()
	if err := h.command.Process.Kill(); err != nil {
		t.Fatalf("kill broker helper: %v", err)
	}
	if err := h.command.Wait(); err == nil {
		t.Fatal("killed broker helper exited successfully")
	}
}

func (h *brokerHelper) closeAndWait(t *testing.T) {
	t.Helper()
	if _, err := io.WriteString(h.stdin, "close\n"); err != nil {
		t.Fatalf("request broker helper shutdown: %v", err)
	}
	if err := h.stdin.Close(); err != nil {
		t.Fatalf("close broker helper input: %v", err)
	}
	if err := h.command.Wait(); err != nil {
		t.Fatalf("broker helper failed: %v\n%s", err, h.stderr.String())
	}
}

func helperSequence(t *testing.T, ready string) string {
	t.Helper()
	var sequence string
	if _, err := fmt.Sscan(ready, new(string), &sequence); err != nil {
		t.Fatalf("parse helper readiness %q: %v", ready, err)
	}
	return sequence
}
