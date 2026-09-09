package integration_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	// From the first step onward. The section before it brings a deployment
	// up by starting the binaries, which is the bootstrap test's subject and
	// needs a deployment that does not exist yet — this test talks to one
	// that is already running. Between them the two cover every runnable
	// block in the document.
	script.WriteString(runbookSection(t, "## Step 0", ""))

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
