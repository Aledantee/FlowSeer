package integration_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	connect "connectrpc.com/connect"

	devicev1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/api/device/v1"
	accessv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/device/access/v1"
)

// runbookPath is the document under test. It is read rather than duplicated:
// the commands an operator is given and the commands this suite runs are the
// same characters, so editing the document edits the test and the two cannot
// drift apart in silence.
var runbookPath = filepath.Join("..", "..", "..", "..", "..", "docs", "runbooks", "lab-icx7150-first-write.md")

// repoRoot is what the runbook calls FLOWSEER_REPO.
var repoRoot = filepath.Join("..", "..", "..", "..", "..")

// block is one fenced block in the runbook, with the line its fence opened on
// so a failure can name where to look.
type block struct {
	kind string
	line int
	body string
}

// The fence kinds the runbook may use, and what each means.
//
// sh is run by this suite exactly as written. cli is typed at the switch and
// cannot be run here. text is output or illustration.
const (
	kindShell  = "sh"
	kindDevice = "cli"
	kindText   = "text"
)

// runbookBlocks parses every fenced block out of the runbook.
//
// Every block is returned, including ones with no info string, because the
// point of the classification below is that nothing escapes it.
func runbookBlocks(t *testing.T) []block {
	t.Helper()
	body, err := os.ReadFile(runbookPath)
	if err != nil {
		t.Fatalf("read the runbook: %v", err)
	}

	var blocks []block
	var current *block
	for i, line := range strings.Split(string(body), "\n") {
		if !strings.HasPrefix(line, "```") {
			if current != nil {
				current.body += line + "\n"
			}
			continue
		}
		if current == nil {
			current = &block{kind: strings.TrimSpace(strings.TrimPrefix(line, "```")), line: i + 1}
			continue
		}
		blocks = append(blocks, *current)
		current = nil
	}
	if current != nil {
		t.Fatalf("the runbook has an unclosed fence opened at line %d", current.line)
	}
	return blocks
}

// Every fenced block in the runbook is classified, and the runnable ones are
// not none.
//
// This is the guard on the guard. A test that extracts blocks by fence
// convention can extract zero of them and pass: rename a fence, reformat the
// document, add a block under a label the parser does not know, and the suite
// stays green having checked nothing. That is an instrument whose refusal to
// run is indistinguishable from its passing, which this repository has already
// shipped once, and the runbook is the last place anyone would notice.
//
// So an unclassified fence is an error naming its line — adding a block forces
// a decision rather than defaulting to skipped — and the runnable set is
// asserted non-empty, because a classification pass over nothing also
// succeeds.
func TestEveryRunbookBlockIsClassified(t *testing.T) {
	t.Parallel()

	blocks := runbookBlocks(t)
	if len(blocks) == 0 {
		t.Fatal("the runbook has no fenced blocks; either it lost its commands or this parser stopped finding them")
	}

	runnable := 0
	for _, b := range blocks {
		switch b.kind {
		case kindShell:
			runnable++
		case kindDevice, kindText:
		default:
			t.Errorf("the block at line %d is fenced %q, which is none of %q, %q or %q — "+
				"every block is run here, typed at the switch, or illustration, and a fourth kind is a decision nobody made",
				b.line, b.kind, kindShell, kindDevice, kindText)
		}
	}

	if runnable == 0 {
		t.Error("no runnable block in the runbook; every command an operator is given is unverified")
	}
}

// The commands the runbook prints are run, in order, against a real
// deployment.
//
// Not an equivalent call assembled in Go: the literal line, with the shell
// variables the document tells an operator to export. Anything else would
// prove the RPC works and leave the documented command unverified, which is
// the state that produced this document's cold-read findings.
//
// The blocks run as one shell script rather than one at a time, because they
// are written to be run that way — an export in one is depended on by the
// next, which is how an operator would use them and therefore what has to
// work.
func TestTheRunbooksCommandsRun(t *testing.T) {
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skipf("buf is not on PATH: %v", err)
	}

	d := assemble(t)
	fingerprint := d.waitForFingerprint(t)
	if fingerprint == "" {
		t.Fatal("central holds no fingerprint; the runbook's step 7 would have nothing to read")
	}

	absRepo, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}

	var script strings.Builder
	script.WriteString("set -eu\n")
	// The document's own variables, given the values this deployment has.
	// An operator exports these by hand from their own deployment; the names
	// and the commands that consume them are the runbook's.
	script.WriteString("export FLOWSEER_REPO=" + shellQuote(absRepo) + "\n")
	script.WriteString("export CENTRAL=" + shellQuote(d.central.baseURL()) + "\n")
	script.WriteString("export CACERT=" + shellQuote(filepath.Join(d.dir, "central-state", "tls.crt")) + "\n")
	script.WriteString("export DEVICE_ID=" + shellQuote(fixtureDeviceID) + "\n")
	script.WriteString("export INTERFACE=" + shellQuote(fixtureInterface) + "\n")
	script.WriteString("export DESCRIPTION='uplink to core'\n")
	script.WriteString("export OPERATOR=e2e-operator\n")
	script.WriteString("export IDEMPOTENCY_KEY=0192e6a0-0000-7000-8000-0000000ab001\n")
	script.WriteString("export POLICY_KEY=" + shellQuote(fixturePolicyKey) + "\n")
	script.WriteString("export POLICY_VERSION=1\n")

	// The steps, and only the steps. The section before them starts the
	// binaries, which is the bootstrap test's subject; the section after them
	// is what an operator does when a mutation does not resolve, which needs
	// a mutation that has not resolved and has its own test below. Between
	// the three, every runnable block in the document is executed.
	script.WriteString(runbookSection(t, "## Step 0", "## If it does not resolve"))

	ctxTimeout := 120 * time.Second
	cmd := exec.Command("sh", "-c", script.String())
	done := make(chan struct{})
	var out []byte
	var runErr error
	go func() {
		out, runErr = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(ctxTimeout):
		_ = cmd.Process.Kill()
		t.Fatalf("the runbook's commands did not finish within %v", ctxTimeout)
	}

	if runErr != nil {
		t.Fatalf("a command the runbook prints failed: %v\n%s", runErr, out)
	}
	t.Logf("the runbook's steps ran:\n%s", out)
}

// shellQuote renders s as a single-quoted shell word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// The recovery steps run too, against a mutation that is actually stuck.
//
// They are the part of the document an operator reaches on their worst day,
// and until now the least likely to have been tried: they only make sense
// against a state that does not arise unless something has gone wrong. A cold
// read found that two of the three arms this section could have named cannot
// work on a first write at all — `restore` needs an expectation only a
// verified mutation writes, `accept` needs an observation only a managed
// interface retains — so the document names `replace`, and this is what
// checks that the one it names is the one that works.
//
// The device is made to hide the change, which is what leaves a mutation
// nobody can establish the effect of. The mutation is then abandoned and
// replaced exactly as the document says.
func TestTheRunbooksRecoveryStepsRun(t *testing.T) {
	if _, err := exec.LookPath("buf"); err != nil {
		t.Skipf("buf is not on PATH: %v", err)
	}

	d := assemble(t)
	fingerprint := d.waitForFingerprint(t)

	d.device.pinReads("as found")
	mutation := d.apply(t, "0192e6a0-0000-7000-8000-0000000ac001", "uplink to core", fingerprint)

	// Waited for, because abandoning a mutation the edge has not admitted
	// takes a different path: central closes the lane itself and there is
	// nothing left to resolve, which is correct behavior and not this
	// section's subject.
	deadline := time.Now().Add(60 * time.Second)
	for {
		status, err := d.central.devices().GetDeviceAccessStatus(context.Background(),
			connect.NewRequest(devicev1.GetDeviceAccessStatusRequest_builder{Device: deviceRef()}.Build()))
		if err == nil && status.Msg.GetUnresolved().GetPhase() == accessv1.OperationPhase_OPERATION_PHASE_POSSIBLY_APPLIED {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the mutation never reached the state these steps are for (error %v)", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	d.device.unpinReads()

	absRepo, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("resolve the repository root: %v", err)
	}

	var script strings.Builder
	script.WriteString("set -eu\n")
	script.WriteString("export FLOWSEER_REPO=" + shellQuote(absRepo) + "\n")
	script.WriteString("export CENTRAL=" + shellQuote(d.central.baseURL()) + "\n")
	script.WriteString("export CACERT=" + shellQuote(filepath.Join(d.dir, "central-state", "tls.crt")) + "\n")
	script.WriteString("export DEVICE_ID=" + shellQuote(fixtureDeviceID) + "\n")
	script.WriteString("export INTERFACE=" + shellQuote(fixtureInterface) + "\n")
	script.WriteString("export OPERATOR=e2e-operator\n")
	script.WriteString("export POLICY_KEY=" + shellQuote(fixturePolicyKey) + "\n")
	script.WriteString("export POLICY_VERSION=1\n")
	script.WriteString("export FINGERPRINT=" + shellQuote(fingerprint) + "\n")
	script.WriteString("export SEQUENCE=" + shellQuote(uintToString(mutation.GetSequence())) + "\n")
	// The document says to retry the resolution until it stops being refused,
	// because nothing reports when the edge has acknowledged the abandonment.
	// Running it once would be running something no operator would.
	script.WriteString("retry() { for _ in $(seq 1 100); do if \"$@\"; then return 0; fi; sleep 0.5; done; return 1; }\n")
	script.WriteString(retryWrap(t, runbookSection(t, "## If it does not resolve", "## Afterwards")))

	out, err := runScript(t, script.String(), 180*time.Second)
	if err != nil {
		t.Fatalf("a recovery command the runbook prints failed: %v\n%s", err, out)
	}
	t.Logf("the runbook's recovery steps ran:\n%s", out)
}

// retryWrap puts every call in the recovery section behind the retry the
// document tells an operator to do, because central refuses a resolution
// until the edge has acknowledged the abandonment and nothing reports when
// that has happened.
//
// It matches on the start of a command rather than on the text of one. An
// exact-string wrapper is the kind that stops matching when the document is
// reflowed and then silently wraps nothing — the test would keep passing and
// would have quietly stopped doing the retry an operator is told to do, which
// is a smaller version of the extraction problem this file already guards
// against. So the wrapping is asserted by the caller instead.
func retryWrap(t *testing.T, script string) string {
	t.Helper()
	var out strings.Builder
	wrapped := 0
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(line, "buf curl ") {
			out.WriteString("retry ")
			wrapped++
		}
		out.WriteString(line + "\n")
	}
	if wrapped == 0 {
		t.Fatal("no command in the recovery section was wrapped in a retry; the section's calls have moved or its formatting has")
	}
	return out.String()
}

func uintToString(n uint64) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
