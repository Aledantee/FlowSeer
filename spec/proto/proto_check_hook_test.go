package proto_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// .claude/hooks/proto-check.sh fires on every save of a .proto and its output
// is what the author — usually an agent — acts on. Every claim it makes was
// hand-checked until now, which is how a `buf ls-files | grep -q` pipeline
// shipped that returned 141 under pipefail on a *successful* match and would
// have quietly stopped linting every production file.
//
// These cases run the real script against a synthetic checkout, so the
// dangerous direction — believing a file was checked when it was not — fails
// here instead of on someone's next edit.

// hookResult is what the harness observes: the two things a PostToolUse hook
// communicates with.
type hookResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// additionalContext returns the message the hook fed back to its caller, or ""
// when it stayed silent.
func (r hookResult) additionalContext(t *testing.T) string {
	t.Helper()
	if strings.TrimSpace(r.stdout) == "" {
		return ""
	}
	var envelope struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &envelope); err != nil {
		t.Fatalf("decoding hook stdout %q: %v", r.stdout, err)
	}
	return envelope.HookSpecificOutput.AdditionalContext
}

func TestProtoCheckHook(t *testing.T) {
	hook := filepath.Join(repoRoot(t), ".claude", "hooks", "proto-check.sh")
	if _, err := os.Stat(hook); err != nil {
		t.Fatalf("locating the hook under test: %v", err)
	}
	tools := lookupTools(t, "git", "jq", "buf")
	repo := syntheticCheckout(t, tools["git"])

	t.Run("a clean production file passes silently", func(t *testing.T) {
		got := runHook(t, hook, repo, "spec/proto/flowseer/net/addr/v1/clean.proto", "")
		if got.exitCode != 0 {
			t.Errorf("got exit %d (%s), want 0", got.exitCode, got.stderr)
		}
		if msg := got.additionalContext(t); msg != "" {
			t.Errorf("got %q, want no message", msg)
		}
	})

	// The lint leg must keep blocking. This is the case the pipefail bug broke:
	// the file was reported as excluded and never linted at all.
	t.Run("a lint-dirty production file blocks", func(t *testing.T) {
		got := runHook(t, hook, repo, "spec/proto/flowseer/net/addr/v1/dirty.proto", "")
		if got.exitCode != 2 {
			t.Errorf("got exit %d, want 2", got.exitCode)
		}
		if !strings.Contains(got.stderr, "buf lint failed") {
			t.Errorf("got stderr %q, want it to report the lint failure", got.stderr)
		}
	})

	// Skipping is fine; skipping silently is not — silence is indistinguishable
	// from a clean lint.
	t.Run("an excluded fixture is skipped and says so", func(t *testing.T) {
		got := runHook(t, hook, repo, "spec/proto/flowseer/net/addr/_test_fixtures/violates.proto", "")
		if got.exitCode != 0 {
			t.Errorf("got exit %d (%s), want 0", got.exitCode, got.stderr)
		}
		if msg := got.additionalContext(t); !strings.Contains(msg, "NOT checked") {
			t.Errorf("got %q, want it to say the file was not checked", msg)
		}
	})

	// A fixture directory nobody excluded is a misconfiguration, and the lint
	// failure is how its author finds out.
	t.Run("an unexcluded fixture is still linted", func(t *testing.T) {
		got := runHook(t, hook, repo, "spec/proto/flowseer/stray/_test_fixtures/violates.proto", "")
		if got.exitCode != 2 {
			t.Errorf("got exit %d, want 2; an unexcluded fixture must not be skipped", got.exitCode)
		}
	})

	t.Run("a missing buf is reported, not passed over", func(t *testing.T) {
		got := runHook(t, hook, repo, "spec/proto/flowseer/net/addr/v1/clean.proto", pathWithout(t, tools, "buf"))
		if got.exitCode != 0 {
			t.Errorf("got exit %d (%s), want 0", got.exitCode, got.stderr)
		}
		if msg := got.additionalContext(t); !strings.Contains(msg, "not on PATH") {
			t.Errorf("got %q, want it to report that buf was unavailable", msg)
		}
	})

	// The message-sync leg is the one part that must survive a missing buf,
	// because it needs nothing but the source text.
	t.Run("message sync runs whether or not buf is present", func(t *testing.T) {
		for _, withBuf := range []bool{true, false} {
			path := ""
			if !withBuf {
				path = pathWithout(t, tools, "buf")
			}
			got := runHook(t, hook, repo, "spec/proto/flowseer/net/addr/v1/partial.proto", path)
			msg := got.additionalContext(t)
			if !strings.Contains(msg, "PartialState") || !strings.Contains(msg, "PartialEvent") {
				t.Errorf("buf present=%v: got %q, want the missing triad members named", withBuf, msg)
			}
			if !strings.Contains(msg, "docs/conventions/protobuf.md") {
				t.Errorf("buf present=%v: got %q, want the conventions doc named", withBuf, msg)
			}
		}
	})
}

// runHook feeds the script one PostToolUse payload. An empty pathOverride
// inherits the caller's PATH.
func runHook(t *testing.T, hook, repo, rel, pathOverride string) hookResult {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"tool_input": map[string]string{"file_path": rel},
		"cwd":        repo,
	})
	if err != nil {
		t.Fatalf("encoding the hook payload: %v", err)
	}

	cmd := exec.Command("bash", hook)
	cmd.Dir = repo
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if pathOverride != "" {
		cmd.Env = append(os.Environ(), "PATH="+pathOverride)
	}

	// A non-zero exit is a result here, not a failure to run.
	var exitErr *exec.ExitError
	if err := cmd.Run(); err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("running the hook: %v", err)
	}
	return hookResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: cmd.ProcessState.ExitCode()}
}

// syntheticCheckout builds a throwaway repo the hook can resolve: a git root,
// a buf module, and one file per behavior under test. The real checkout is
// never touched.
func syntheticCheckout(t *testing.T, gitBin string) string {
	t.Helper()
	root := t.TempDir()

	// The hook finds its root with `git rev-parse --show-toplevel`.
	init := exec.Command(gitBin, "init", "--quiet", root)
	if out, err := init.CombinedOutput(); err != nil {
		t.Skipf("cannot create a synthetic git checkout (%v): %s", err, out)
	}

	write(t, root, "buf.yaml", `version: v2
modules:
  - path: spec/proto
    excludes:
      - spec/proto/flowseer/net/addr/_test_fixtures
    lint:
      use:
        - MINIMAL
`)
	write(t, root, "spec/proto/flowseer/net/addr/v1/clean.proto", `edition = "2024";

package flowseer.net.addr.v1;

message Clean {}
`)
	// The package does not match its directory, which MINIMAL rejects.
	write(t, root, "spec/proto/flowseer/net/addr/v1/dirty.proto", `edition = "2024";

package flowseer.wrong.v1;

message Dirty {}
`)
	// Names one member of a triad, so the sync leg has something to report.
	write(t, root, "spec/proto/flowseer/net/addr/v1/partial.proto", `edition = "2024";

package flowseer.net.addr.v1;

message PartialConfig {}
`)
	fixture := `edition = "2024";

package flowseer.wrong.v1;

message Violates {}
`
	write(t, root, "spec/proto/flowseer/net/addr/_test_fixtures/violates.proto", fixture)
	// Deliberately absent from buf.yaml's excludes.
	write(t, root, "spec/proto/flowseer/stray/_test_fixtures/violates.proto", fixture)

	fillModuleToOutgrowPipeBuffer(t, root)
	return root
}

// pipeBufferFillers is chosen so `buf ls-files` writes more than any platform's
// pipe buffer holds (~75KB here, against 16KB on macOS and 64KB on Linux).
const pipeBufferFillers = 900

// fillModuleToOutgrowPipeBuffer pads the module until buf cannot hand its whole
// listing to a reader in one go.
//
// This is what makes the lint cases above able to fail. The hook once piped
// that listing into `grep -q`; under `pipefail` grep exits at the first match,
// buf takes SIGPIPE on its next write, and the pipeline reports 141 for a
// *successful* match — so every production file looked excluded and stopped
// being linted. A module small enough for buf to write and exit before grep
// looks never reproduces it, which is exactly how the bug shipped. The fillers
// sort after every file under test, so a match lands early with plenty left to
// write.
func fillModuleToOutgrowPipeBuffer(t *testing.T, root string) {
	t.Helper()
	for i := range pipeBufferFillers {
		name := fmt.Sprintf("filler_%04d_padded_name_to_widen_each_listed_line.proto", i)
		write(t, root, "spec/proto/flowseer/zfill/v1/"+name, fmt.Sprintf(`edition = "2024";

package flowseer.zfill.v1;

message Filler%04d {}
`, i))
	}
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(rel), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s: %v", rel, err)
	}
}

// lookupTools resolves each executable the hook shells out to, skipping the
// suite when one is missing rather than reporting a failure the checkout
// cannot cause.
func lookupTools(t *testing.T, names ...string) map[string]string {
	t.Helper()
	found := make(map[string]string, len(names))
	for _, n := range names {
		p, err := exec.LookPath(n)
		if err != nil {
			t.Skipf("%s is not installed; the hook needs it", n)
		}
		found[n] = p
	}
	return found
}

// pathWithout builds a PATH carrying every named tool except one, by
// symlinking the rest into a directory of their own and falling back to the
// system directories for the ordinary utilities (grep, awk, sort) the hook
// also uses. Removing the omitted tool's directory from the real PATH would
// take its neighbors with it.
func pathWithout(t *testing.T, tools map[string]string, omit string) string {
	t.Helper()
	bin := t.TempDir()
	for name, full := range tools {
		if name == omit {
			continue
		}
		if err := os.Symlink(full, filepath.Join(bin, name)); err != nil {
			t.Fatalf("linking %s into the trimmed PATH: %v", name, err)
		}
	}
	return bin + ":/usr/bin:/bin"
}
