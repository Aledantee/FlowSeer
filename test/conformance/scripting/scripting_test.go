// Package scripting gates the repository's scripting rule: host-side scripts
// are Python run through uv, never shell, and nothing starts one with a bare
// interpreter.
//
// docs/architecture/2026-10-05-repository-scripting-direction.md states the
// rule and docs/code-style-python.md names this gate. The gate catches a
// script added by habit, not one hidden on purpose: a shell script named
// x.txt with no #! line passes.
package scripting

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// exemption is a path prefix, when it ends in a slash, or an exact path. The
// reason says why the file stays shell. Adding an entry changes the
// repository scripting record.
type exemption struct {
	path   string
	reason string
}

var exemptions = []exemption{
	{
		path:   "src/protocol/snmp/test/integration/testdata/snmpd/pass-scripts/",
		reason: "snmpd runs a pass-script as a child process inside the integration container image, which holds no uv",
	},
	{
		path:   "src/protocol/yang/test/integration/testenv/testdata/clixon/startup.sh",
		reason: "the clixon container image starts it as its entrypoint, and the image holds no uv",
	},
	{
		path:   "spec/",
		reason: "vendored third-party files keep their upstream form",
	},
	{
		path:   "docs/research/",
		reason: "research notes keep the scripts they quote as they ran",
	},
}

// scannedPrefixes, scannedNames, and scannedFiles select the files whose
// lines are searched for a bare interpreter call: the places a command is
// written down for a person or an agent to run.
var (
	scannedPrefixes = []string{".agents/", ".claude/", ".codex/", "tools/", "docs/runbooks/"}
	scannedNames    = []string{"Taskfile.yml"}
	scannedFiles    = []string{"AGENTS.md", "CONTRIBUTING.md"}
)

const (
	scriptsDir = "tools/scripts/"

	// headBytes is enough to hold the first line of any real script.
	headBytes = 512

	metadataOpen  = "# /// script"
	metadataClose = "# ///"

	assignmentWords = `(?:[A-Za-z_][A-Za-z0-9_]*=(?:[^\s"'\\]|"(?:[^"\\]|\\.)*"|'[^']*'|\\.)*[ \t]+)*`
)

var (
	shellSuffixes     = []string{".sh", ".bash", ".zsh"}
	shellInterpreters = []string{"sh", "bash", "zsh", "dash", "ksh"}
	bareInterpreters  = []string{"python", "python3"}

	// lineStartCall matches an interpreter as the command word at the start of
	// a line, after blanks, an optional prompt, and NAME=value words.
	lineStartCall = regexp.MustCompile(`^[ \t]*(?:\$ )?` + assignmentWords + `python3?(?:[ \t]|$)`)
	// yamlStartCall is lineStartCall for a YAML list item.
	yamlStartCall = regexp.MustCompile(`^[ \t]*(?:- )?(?:\$ )?` + assignmentWords + `python3?(?:[ \t]|$)`)
	// commandKey captures the string value of a "command" key in a JSON hook
	// configuration.
	commandKey = regexp.MustCompile(`"command"[ \t]*:[ \t]*"((?:[^"\\]|\\.)*)"`)
	// commandCall finds an interpreter as a command word inside such a string.
	commandCall = regexp.MustCompile(`(?:^|[ \t;&|(])python3?(?:[ \t]|$)`)
)

// opener returns a reader for the repository-relative slash path.
type opener func(name string) (io.ReadCloser, error)

// TestScriptingLayout lists the repository's files and fails on every shell
// script, bare interpreter, bare interpreter call, and Python file without a
// metadata block that no exemption covers.
//
// The listing includes files not yet added to git, so a script is judged when
// it is created. A failing git fails the gate, since a skipped gate reads as
// a pass.
func TestScriptingLayout(t *testing.T) {
	root := repoRoot(t)

	listed := listFiles(t, root)
	if len(listed) == 0 {
		t.Fatal("git listed no files; the gate checked nothing")
	}

	files := regularFiles(root, listed)

	// go test caches a pass and rechecks only what the test opened or
	// stat'ed inside the module root, and the output of git is not an input.
	// Reading each directory on the path of a listed file puts the entry names
	// of those directories into the cache key, so a file that appears there
	// reruns the test.
	for _, dir := range pathDirs(files) {
		if _, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
			t.Fatalf("reading directory %q: %v", dir, err)
		}
	}

	open := func(name string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(root, filepath.FromSlash(name)))
	}
	for _, finding := range findings(exemptions, files, open) {
		t.Error(finding)
	}
	for _, finding := range unusedExemptions(exemptions, files) {
		t.Error(finding)
	}
}

// listFiles returns the files git tracks and the files it would track, less
// the ones .gitignore hides, as slash paths relative to root.
func listFiles(t *testing.T, root string) []string {
	t.Helper()

	cmd := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("listing files with git: %v: %s", err, strings.TrimSpace(stderr.String()))
	}

	var paths []string
	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			paths = append(paths, name)
		}
	}

	return paths
}

// regularFiles drops the listed paths that are not regular files: a link such
// as .claude/skills, and a tracked file deleted from the working tree.
func regularFiles(root string, listed []string) []string {
	var files []string
	for _, name := range listed {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
		if err == nil && info.Mode().IsRegular() {
			files = append(files, name)
		}
	}

	return files
}

// pathDirs returns the repository root (".") and every directory on the path
// of a file outside the exempt trees, once each.
func pathDirs(files []string) []string {
	seen := map[string]bool{".": true}
	dirs := []string{"."}
	for _, name := range files {
		if exempt(exemptions, name) {
			continue
		}
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			if seen[dir] {
				break
			}
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// exempt reports whether an entry covers name.
func exempt(list []exemption, name string) bool {
	for _, entry := range list {
		if name == entry.path || (strings.HasSuffix(entry.path, "/") && strings.HasPrefix(name, entry.path)) {
			return true
		}
	}

	return false
}

// findings judges each path against the four rules.
func findings(list []exemption, files []string, open opener) []string {
	var out []string
	for _, name := range files {
		if !exempt(list, name) {
			out = append(out, fileFindings(name, open)...)
		}
	}

	return out
}

// unusedExemptions reports each entry that covers no listed path, since an
// exemption nothing uses is a rule nobody reads.
func unusedExemptions(list []exemption, files []string) []string {
	var out []string
	for _, entry := range list {
		used := false
		for _, name := range files {
			if exempt([]exemption{entry}, name) {
				used = true

				break
			}
		}
		if !used {
			out = append(out, fmt.Sprintf("exempt entry %q matches no listed file; remove it (%s)", entry.path, entry.reason))
		}
	}

	return out
}

// fileFindings returns the findings for one file that no exemption covers.
func fileFindings(name string, open opener) []string {
	var out []string

	base := path.Base(name)
	for _, suffix := range shellSuffixes {
		if strings.HasSuffix(base, suffix) {
			out = append(out, fmt.Sprintf("%s: shell script; write it as Python run through uv", name))

			break
		}
	}

	needsBody := isScanned(name) || (strings.HasSuffix(base, ".py") && !strings.HasPrefix(name, scriptsDir))

	f, err := open(name)
	if err != nil {
		return append(out, fmt.Sprintf("%s: %v", name, err))
	}
	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, headBytes))
	if err == nil && needsBody {
		var rest []byte
		rest, err = io.ReadAll(f)
		data = append(data, rest...)
	}
	if err != nil {
		return append(out, fmt.Sprintf("%s: %v", name, err))
	}

	if line, ok := interpreterLine(data); ok {
		switch interpreter := shebangInterpreter(line); {
		case slices.Contains(shellInterpreters, interpreter):
			out = append(out, fmt.Sprintf("%s: shell script (first line %q); write it as Python run through uv", name, line))
		case slices.Contains(bareInterpreters, interpreter):
			out = append(out, fmt.Sprintf("%s: bare interpreter (first line %q); start it with uv run --script", name, line))
		}
	}

	if bytes.IndexByte(data, 0) >= 0 {
		return out
	}

	if isScanned(name) {
		out = append(out, callFindings(name, data)...)
	}
	if strings.HasSuffix(base, ".py") && !strings.HasPrefix(name, scriptsDir) && !hasMetadataBlock(data) {
		out = append(out, fmt.Sprintf("%s: Python file without an inline metadata block (%s ... %s); add one so uv can run it", name, metadataOpen, metadataClose))
	}

	return out
}

// isScanned reports whether name is a file whose lines are searched for a
// bare interpreter call.
func isScanned(name string) bool {
	for _, prefix := range scannedPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return slices.Contains(scannedNames, path.Base(name)) || slices.Contains(scannedFiles, name)
}

// interpreterLine returns the first line of data without its line ending when
// it starts with #!.
func interpreterLine(data []byte) (string, bool) {
	if !bytes.HasPrefix(data, []byte("#!")) {
		return "", false
	}
	first, _, _ := bytes.Cut(data, []byte("\n"))

	return strings.TrimRight(string(first), "\r"), true
}

// shebangInterpreter returns the program a #! line starts: the first word's
// base name, or for env the first word after its options and assignments.
func shebangInterpreter(line string) string {
	words := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(words) == 0 {
		return ""
	}

	program := path.Base(words[0])
	if program != "env" {
		return program
	}
	for i := 1; i < len(words); i++ {
		word := words[i]
		if strings.HasPrefix(word, "-") {
			if !strings.HasPrefix(word, "--") {
				// -S leaves the split words here for the next iteration.
				option := strings.IndexAny(word, "SuPC")
				if option == len(word)-1 && word[option] != 'S' {
					i++
				}
			}

			continue
		}
		if strings.Contains(word, "=") {
			continue
		}

		return path.Base(word)
	}

	return ""
}

// callFindings returns one finding per line that starts a bare interpreter.
func callFindings(name string, data []byte) []string {
	start := lineStartCall
	if ext := path.Ext(name); ext == ".yml" || ext == ".yaml" {
		start = yamlStartCall
	}

	var out []string
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if start.MatchString(line) || commandStringCalls(line) {
			out = append(out, fmt.Sprintf("%s:%d: bare interpreter call; start it with uv run: %s", name, i+1, strings.TrimSpace(line)))
		}
	}

	return out
}

// commandStringCalls reports whether the line holds a "command" key whose
// string starts a bare interpreter.
func commandStringCalls(line string) bool {
	for _, match := range commandKey.FindAllStringSubmatch(line, -1) {
		if commandCall.MatchString(match[1]) {
			return true
		}
	}

	return false
}

// hasMetadataBlock reports whether data holds the opening delimiter line of an
// inline script metadata block and, after it, the closing one. uv reads the
// fields and refuses a block it cannot parse.
func hasMetadataBlock(data []byte) bool {
	open := false
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case !open && line == metadataOpen:
			open = true
		case open && line == metadataClose:
			return true
		}
	}

	return false
}

func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getting working directory: %v", err)
	}

	for {
		data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
		if err == nil && strings.Contains(string(data), "module go.aledante.io/FlowSeer\n") {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found above the conformance package")
		}

		dir = parent
	}
}

// memFS opens files from a map, so a table row needs no file in the tree.
func memFS(files map[string]string) opener {
	return func(name string) (io.ReadCloser, error) {
		content, ok := files[name]
		if !ok {
			return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("not in the fixture")}
		}

		return io.NopCloser(strings.NewReader(content)), nil
	}
}

// TestFindings is the positive control for the gate. The tree holds no
// finding, so TestScriptingLayout alone cannot tell a working classifier from
// one that returns nothing.
func TestFindings(t *testing.T) {
	const pyBlock = "# /// script\n# dependencies = []\n# ///\nprint(1)\n"

	tests := []struct {
		name   string
		exempt []exemption
		file   string
		body   string
		// want is a substring of the one expected finding, empty when the file passes.
		want string
	}{
		{name: "shell script suffix", file: "tools/scripts/x.sh", body: "echo hi\n", want: "tools/scripts/x.sh: shell script"},
		{name: "bash suffix", file: "deploy/x.bash", body: "echo hi\n", want: "deploy/x.bash: shell script"},
		{name: "zsh suffix", file: "deploy/x.zsh", body: "echo hi\n", want: "deploy/x.zsh: shell script"},
		{name: "exempt pass-script", file: "src/protocol/snmp/test/integration/testdata/snmpd/pass-scripts/endofmibview.sh", body: "echo hi\n"},
		{name: "exempt exact path", file: "src/protocol/yang/test/integration/testenv/testdata/clixon/startup.sh", body: "echo hi\n"},
		{name: "sibling of an exempt exact path", file: "src/protocol/yang/test/integration/testenv/testdata/clixon/other.sh", body: "echo hi\n", want: "other.sh: shell script"},
		{name: "bash first line without suffix", file: "tools/x", body: "#!/bin/bash\necho hi\n", want: "tools/x: shell script"},
		{name: "zsh first line without suffix", file: "tools/x", body: "#!/bin/zsh\n", want: "tools/x: shell script"},
		{name: "dash first line without suffix", file: "tools/x", body: "#!/bin/dash\n", want: "tools/x: shell script"},
		{name: "ksh first line without suffix", file: "tools/x", body: "#!/bin/ksh\n", want: "tools/x: shell script"},
		{name: "env bash first line", file: "tools/x", body: "#!/usr/bin/env bash\n", want: "tools/x: shell script"},
		{name: "env split unset first line", file: "tools/x", body: "#!/usr/bin/env -S -u PYTHONPATH python3\n", want: "tools/x: bare interpreter"},
		{name: "env split unset uv first line", file: "tools/x", body: "#!/usr/bin/env -S -u PYTHONPATH uv run --script\n"},
		{name: "env unset first line", file: "tools/x", body: "#!/usr/bin/env -u PYTHONPATH bash\n", want: "tools/x: shell script"},
		{name: "env unset uv first line", file: "tools/x", body: "#!/usr/bin/env -u PYTHONPATH uv run --script\n"},
		{name: "env path first line", file: "tools/x", body: "#!/usr/bin/env -P /opt/bin bash\n", want: "tools/x: shell script"},
		{name: "env path uv first line", file: "tools/x", body: "#!/usr/bin/env -P /opt/bin uv run --script\n"},
		{name: "env directory first line", file: "tools/x", body: "#!/usr/bin/env -C /tmp bash\n", want: "tools/x: shell script"},
		{name: "env directory uv first line", file: "tools/x", body: "#!/usr/bin/env -C /tmp uv run --script\n"},
		{name: "env combined unset first line", file: "tools/x", body: "#!/usr/bin/env -iu PYTHONPATH bash\n", want: "tools/x: shell script"},
		{name: "env combined unset uv first line", file: "tools/x", body: "#!/usr/bin/env -iu PYTHONPATH uv run --script\n"},
		{name: "env attached unset first line", file: "tools/x", body: "#!/usr/bin/env -iuPYTHONPATH bash\n", want: "tools/x: shell script"},
		{name: "env attached unset uv first line", file: "tools/x", body: "#!/usr/bin/env -iuPYTHONPATH uv run --script\n"},
		{name: "sh with a flag", file: "tools/x", body: "#!/bin/sh -e\n", want: "tools/x: shell script"},
		{name: "python first line", file: "tools/x", body: "#!/usr/bin/env python3\n", want: "tools/x: bare interpreter"},
		{name: "env python first line", file: "tools/x", body: "#!/usr/bin/env python\n", want: "tools/x: bare interpreter"},
		{name: "uv first line", file: "tools/x", body: "#!/usr/bin/env -S uv run --script\n"},
		{name: "uv first line with quiet", file: "src/edge/netpen/tools/l2l3-audit/l2l3-audit", body: "#!/usr/bin/env -S uv run --quiet --script\n"},
		{name: "SKILL line starts python3", file: ".agents/skills/x/SKILL.md", body: "text\npython3 a.py\n", want: "SKILL.md:2: bare interpreter call"},
		{name: "tools Markdown line starts python3", file: "tools/x.md", body: "python3 a.py\n", want: "tools/x.md:1: bare interpreter call"},
		{name: "CONTRIBUTING line starts python3", file: "CONTRIBUTING.md", body: "python3 a.py\n", want: "CONTRIBUTING.md:1: bare interpreter call"},
		{name: "SKILL line starts python3x", file: ".agents/skills/x/SKILL.md", body: "python3x a.py\n"},
		{name: "prompt and python", file: ".agents/skills/x/SKILL.md", body: "  $ python a.py\n", want: "SKILL.md:1: bare interpreter call"},
		{name: "assignment and python3", file: ".agents/skills/x/SKILL.md", body: "A=1 python3 a.py\n", want: "SKILL.md:1: bare interpreter call"},
		{name: "double quoted assignment in skill", file: ".agents/skills/x/SKILL.md", body: "OUTPUT=\"two words\" python3 a.py\n", want: "SKILL.md:1: bare interpreter call"},
		{name: "double quoted assignment with uv in skill", file: ".agents/skills/x/SKILL.md", body: "OUTPUT=\"two words\" uv run a.py\n"},
		{name: "double quoted assignment in Taskfile", file: "Taskfile.yml", body: "  - OUTPUT=\"two words\" python3 a.py\n", want: "Taskfile.yml:1: bare interpreter call"},
		{name: "double quoted assignment with uv in Taskfile", file: "Taskfile.yml", body: "  - OUTPUT=\"two words\" uv run a.py\n"},
		{name: "double quoted assignment in command key", file: ".codex/hooks.json", body: "{\"command\":\"OUTPUT=\\\"two words\\\" python3 a.py\"}\n", want: "hooks.json:1: bare interpreter call"},
		{name: "double quoted assignment with uv in command key", file: ".codex/hooks.json", body: "{\"command\":\"OUTPUT=\\\"two words\\\" uv run a.py\"}\n"},
		{name: "single quoted assignment in skill", file: ".agents/skills/x/SKILL.md", body: "OUTPUT='two words' python3 a.py\n", want: "SKILL.md:1: bare interpreter call"},
		{name: "single quoted assignment with uv in skill", file: ".agents/skills/x/SKILL.md", body: "OUTPUT='two words' uv run a.py\n"},
		{name: "single quoted assignment in Taskfile", file: "Taskfile.yml", body: "  - OUTPUT='two words' python3 a.py\n", want: "Taskfile.yml:1: bare interpreter call"},
		{name: "single quoted assignment with uv in Taskfile", file: "Taskfile.yml", body: "  - OUTPUT='two words' uv run a.py\n"},
		{name: "single quoted assignment in command key", file: ".codex/hooks.json", body: "{\"command\":\"OUTPUT='two words' python3 a.py\"}\n", want: "hooks.json:1: bare interpreter call"},
		{name: "single quoted assignment with uv in command key", file: ".codex/hooks.json", body: "{\"command\":\"OUTPUT='two words' uv run a.py\"}\n"},
		{name: "escaped assignment in skill", file: ".agents/skills/x/SKILL.md", body: "OUTPUT=two\\ words python3 a.py\n", want: "SKILL.md:1: bare interpreter call"},
		{name: "escaped assignment with uv in skill", file: ".agents/skills/x/SKILL.md", body: "OUTPUT=two\\ words uv run a.py\n"},
		{name: "escaped assignment in Taskfile", file: "Taskfile.yml", body: "  - OUTPUT=two\\ words python3 a.py\n", want: "Taskfile.yml:1: bare interpreter call"},
		{name: "escaped assignment with uv in Taskfile", file: "Taskfile.yml", body: "  - OUTPUT=two\\ words uv run a.py\n"},
		{name: "escaped assignment in command key", file: ".codex/hooks.json", body: "{\"command\":\"OUTPUT=two\\\\ words python3 a.py\"}\n", want: "hooks.json:1: bare interpreter call"},
		{name: "escaped assignment with uv in command key", file: ".codex/hooks.json", body: "{\"command\":\"OUTPUT=two\\\\ words uv run a.py\"}\n"},
		{name: "assignment with a quoted-free value", file: "AGENTS.md", body: "A=1 B=two python3 a.py\n", want: "AGENTS.md:1: bare interpreter call"},
		{name: "list item in a Taskfile", file: "src/protocol/smi/bench/Taskfile.yml", body: "cmds:\n  - python3 a.py\n", want: "Taskfile.yml:2: bare interpreter call"},
		{name: "list item in a yaml file", file: "tools/x.yaml", body: "  - python3 a.py\n", want: "tools/x.yaml:1: bare interpreter call"},
		{name: "list item with assignment in a Taskfile", file: "src/protocol/smi/bench/Taskfile.yml", body: "  - COUNT=1 python3 a.py\n", want: "Taskfile.yml:1: bare interpreter call"},
		{name: "uv run in a Taskfile", file: "src/protocol/smi/bench/Taskfile.yml", body: "  - COUNT=1 uv run bench-gate.py\n"},
		{name: "command key is python3", file: ".claude/settings.json", body: "{\"type\": \"command\", \"command\": \"python3\"}\n", want: "settings.json:1: bare interpreter call"},
		{name: "command key runs python3 after a conjunction", file: ".codex/hooks.json", body: "\"command\": \"cd x && exec python3 a.py\"\n", want: "hooks.json:1: bare interpreter call"},
		{name: "command key runs python3 after a semicolon", file: ".codex/hooks.json", body: "\"command\": \"cd x;python3 a.py\"\n", want: "hooks.json:1: bare interpreter call"},
		{name: "command key runs python3 after adjacent conjunction", file: ".codex/hooks.json", body: "\"command\": \"x&&python3 a.py\"\n", want: "hooks.json:1: bare interpreter call"},
		{name: "command key runs python3 after a pipe", file: ".codex/hooks.json", body: "\"command\": \"x|python3\"\n", want: "hooks.json:1: bare interpreter call"},
		{name: "command key runs python3 in parentheses", file: ".codex/hooks.json", body: "\"command\": \"(python3 a.py)\"\n", want: "hooks.json:1: bare interpreter call"},
		{name: "command key runs uv", file: ".claude/settings.json", body: "\"command\": \"uv\",\n"},
		{name: "command key names a path ending python3", file: ".claude/settings.json", body: "\"command\": \"exec xpython3 a.py\"\n"},
		{name: "list item in Markdown is prose", file: ".agents/skills/x/SKILL.md", body: "- python3 is gone\n"},
		{name: "uv run in a SKILL", file: ".agents/skills/x/SKILL.md", body: "uv run tools/scripts/run.py list\n"},
		{name: "quoted tuple in a Python test", file: "tools/scripts/tests/hooks/test_x.py", body: "(\"python3 fix.py\", \"deny\"),\n"},
		{name: "python3 line in a plan", file: "docs/plans/x.md", body: "python3 a.py\n"},
		{name: "carriage return after python3", file: "docs/runbooks/x.md", body: "python3\r\n", want: "x.md:1: bare interpreter call"},
		{name: "file holding a NUL byte", file: "tools/blob", body: "python3 a.py\x00\n"},
		{name: "missing metadata block", file: "deploy/lab/x.py", body: "print(1)\n", want: "deploy/lab/x.py: Python file without an inline metadata block"},
		{name: "opening delimiter only", file: "deploy/lab/x.py", body: "# /// script\nprint(1)\n", want: "deploy/lab/x.py: Python file without"},
		{name: "closing delimiter before the opening", file: "deploy/lab/x.py", body: "# ///\n# /// script\n", want: "deploy/lab/x.py: Python file without"},
		{name: "metadata block", file: "deploy/lab/write-registry.py", body: pyBlock},
		{name: "metadata block with carriage returns", file: "deploy/lab/x.py", body: "# /// script\r\n# dependencies = []\r\n# ///\r\nprint(1)\r\n"},
		{name: "research Python without a block", file: "docs/research/a/b.py", body: "print(1)\n"},
		{name: "script package Python without a block", file: "tools/scripts/lib/x.py", body: "print(1)\n"},
		{
			name:   "exempt entry matching no file",
			exempt: []exemption{{path: "gone/", reason: "nothing lives there"}},
			file:   "README.md",
			body:   "text\n",
			want:   `exempt entry "gone/" matches no listed file`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list := tt.exempt
			if list == nil {
				list = exemptions
			}
			files := map[string]string{tt.file: tt.body}

			got := findings(list, []string{tt.file}, memFS(files))
			if tt.exempt != nil {
				got = append(got, unusedExemptions(list, []string{tt.file})...)
			}
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("got findings %q, want none", got)
				}

				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tt.want) {
				t.Errorf("got findings %q, want one holding %q", got, tt.want)
			}
		})
	}
}
