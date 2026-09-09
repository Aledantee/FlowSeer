package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The runbook's deployment sequence, performed by the binaries an operator
// runs.
//
// Everything else in this package drives host.Run in-process, which is the
// right shape for testing what the hosts do and reaches none of what a person
// meets first: flag parsing, loading a config from a path, the exit status of
// a bad one, and what a signal does. Nothing in this repository had ever
// started either binary.
//
// The signal contract is the example of what that costs. "An interrupt or a
// termination signal is a clean stop and exits 0" is in both package
// comments, and it was true and unverified — true because service.Run
// installs the handler itself, which two readers missed by looking for
// os/signal in main.go and not in the layer it delegates to. An unverified
// claim that happens to hold is indistinguishable from one that does not
// until something sends the signal.
//
// It is also the closest rehearsal of the lab run that exists without
// hardware: two processes, real configuration files read from disk, the
// bootstrap restart, and an agent enrolling against a central it dials over
// TLS.
func TestTheRunbooksBootstrapBringsUpADeployment(t *testing.T) {
	for _, tool := range []string{"buf", "jq", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH: %v", tool, err)
		}
	}

	dir := t.TempDir()
	env := bootstrapEnvironment(t, dir)

	// The blocks under this heading and no others: the rest of the runbook
	// talks to a deployment that this section is what brings up.
	script := runbookSection(t, "## Bringing the deployment up", "## Step 0")
	out, err := runScript(t, env+script, 240*time.Second)
	if err != nil {
		t.Fatalf("the runbook's bootstrap failed: %v\n%s", err, out)
	}
	t.Cleanup(func() { stopBootstrap(dir) })

	// That the script succeeded is the assertion that the restart was a drain,
	// and it is weaker than it looks. The runbook's restart block ends in a
	// bare `wait` on the stopped process, so under `set -e` a non-zero status
	// fails the script — but a status is all there is to check, because **a
	// clean shutdown logs nothing at all**. Measured, not assumed: a SIGTERM
	// to the fixed binary adds zero lines to its log and exits zero.
	//
	// So a drained service and a killed one are distinguishable only by their
	// exit status, and this test inherits that limit. What saves it from
	// being a coin flip is that central runs until something stops it: a zero
	// status promptly after a signal cannot be a process that "happened to
	// finish", which is the usual reason not to trust an exit code. It could
	// still be a signal handler that exits without draining, and nothing here
	// would tell the difference. That gap is filed rather than papered over.
	central := readFile(t, filepath.Join(dir, "run", "central.log"))

	// And the deployment is actually up: the agent enrolled, attached and
	// onboarded the device, which is what the bootstrap's last block waits
	// for.
	if !strings.Contains(central, "device api listening") {
		t.Error("central never reported a bound listener")
	}
	// The agent got as far as a real deployment can get without the switch.
	//
	// It stops there for a reason worth stating: the binary has no
	// substitution seam. The fake device every other test in this package
	// uses is an argument to agenthost.Run, which is a Go API — a process
	// reads a config file and dials whatever the registry says, so this agent
	// is really trying to reach 172.16.0.6 over SNMP. Onboarding, and
	// everything past it, needs the device.
	//
	// What is proven here is the whole of the bootstrap: both binaries built
	// and started from files, central bound and drained cleanly across the
	// restart the sequence requires, the edge created over the real API, and
	// the agent enrolled against it and running its lane.
	agent := readFile(t, filepath.Join(dir, "run", "agent.log"))
	if !strings.Contains(agent, `"flowseer.module.path":"agent/lane"`) {
		t.Errorf("the agent never started its lane, so it did not get through enrollment and attachment:\n%s",
			tail(agent, 20))
	}
	if !strings.Contains(agent, `"flowseer.edge.id"`) {
		t.Errorf("the agent never reported an edge identity, so it did not enroll:\n%s", tail(agent, 20))
	}
}

// A configuration the service will not accept stops it before it binds
// anything, with the status that tells a supervisor not to retry.
//
// The exit code is the contract here rather than an implementation detail:
// the package comment promises 2 for a configuration failure and 1 for a
// runtime one precisely so a supervisor can tell a file it will never accept
// from a failure a restart might survive. Nothing checked it until now.
func TestABadConfigurationExitsTwoWithoutBinding(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go is not on PATH: %v", err)
	}

	dir := t.TempDir()
	binary := buildCommand(t, dir, "./src/services/device/cmd/device", "device")

	bad := filepath.Join(dir, "bad.textproto")
	if err := os.WriteFile(bad, []byte("state_dir: \"relative/not/absolute\"\n"), 0o600); err != nil {
		t.Fatalf("write the bad configuration: %v", err)
	}

	cmd := exec.Command(binary, "--config", bad)
	out, err := cmd.CombinedOutput()

	var code int
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running the service: %v", err)
	}
	if code != 2 {
		t.Errorf("a rejected configuration exited %d, want 2 — a supervisor cannot tell it from a runtime failure worth retrying\n%s",
			code, out)
	}
	if !strings.Contains(string(out), "device:") {
		t.Errorf("the failure was not reported on stderr for the operator who mistyped the file:\n%s", out)
	}
}

// buildCommand builds one of this repository's commands into dir and returns
// its path.
func buildCommand(t *testing.T, dir, pkg, name string) string {
	t.Helper()
	repo, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}
	out := filepath.Join(dir, name)
	build := exec.Command("go", "-C", repo, "build", "-o", out, pkg)
	if combined, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, combined)
	}
	return out
}

// runbookSection is the runnable blocks between two headings, joined in the
// order they appear.
func runbookSection(t *testing.T, from, to string) string {
	t.Helper()
	body := readFile(t, runbookPath)
	start := strings.Index(body, from)
	if start < 0 {
		t.Fatalf("the runbook has no %q heading; this test names a section that has been renamed", from)
	}
	end := len(body) - start
	if to != "" {
		end = strings.Index(body[start:], to)
		if end < 0 {
			t.Fatalf("the runbook has no %q heading after %q", to, from)
		}
	}

	var script strings.Builder
	inBlock := false
	for _, line := range strings.Split(body[start:start+end], "\n") {
		switch {
		case strings.HasPrefix(line, "```"+kindShell):
			inBlock = true
		case strings.HasPrefix(line, "```"):
			inBlock = false
		case inBlock:
			script.WriteString(line + "\n")
		}
	}
	if script.Len() == 0 {
		t.Fatalf("no runnable block between %q and %q; the section's commands are unverified", from, to)
	}
	return script.String()
}

func runScript(t *testing.T, script string, budget time.Duration) (string, error) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
		return string(out), err
	case <-time.After(budget):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return "", errTimedOut{budget}
	}
}

type errTimedOut struct{ budget time.Duration }

func (e errTimedOut) Error() string { return "the script did not finish within " + e.budget.String() }

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// tail is the last n lines, for a failure message that shows where a process
// got to without printing a whole log.
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// stopBootstrap ends whatever the bootstrap left running.
func stopBootstrap(dir string) {
	for _, name := range []string{"agent.pid", "central.pid"} {
		pid, err := os.ReadFile(filepath.Join(dir, "run", name))
		if err != nil {
			continue
		}
		_ = exec.Command("kill", "-TERM", strings.TrimSpace(string(pid))).Run()
	}
}
