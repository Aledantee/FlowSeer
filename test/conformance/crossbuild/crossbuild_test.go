package crossbuild

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vmTags lists the opt-in build tags used by FreeBSD VM test suites.
var vmTags = []string{
	"bpfdev_vm",
}

var architectures = []string{
	"amd64",
	"arm64",
}

var rootTargets = []string{
	"./src/common/net/bpfdev/...",
	"./src/common/service/...",
	"./src/edge/agent/...",
	"./src/modules/capture/...",
	"./src/edge/simload/...",
}

const agentTarget = "./src/edge/agent/cmd/agent"

// TestCrossBuild validates that platform-specific packages and nested modules
// compile cleanly and the agent binary links under FreeBSD for amd64 and arm64.
func TestCrossBuild(t *testing.T) {
	root := repoRoot(t)

	tagSets := []struct {
		name string
		tags string
	}{
		{name: "default", tags: ""},
		{name: "vm_tags", tags: strings.Join(vmTags, ",")},
	}

	modules := []struct {
		name    string
		dir     string
		targets []string
	}{
		{
			name:    "root",
			dir:     root,
			targets: rootTargets,
		},
		{
			name:    "netpen",
			dir:     filepath.Join(root, "src", "edge", "netpen"),
			targets: []string{"./..."},
		},
	}

	for _, mod := range modules {
		for _, arch := range architectures {
			for _, tagSet := range tagSets {
				testName := fmt.Sprintf("%s_%s_%s", mod.name, arch, tagSet.name)
				t.Run(testName, func(t *testing.T) {
					runVet(t, mod.dir, arch, tagSet.tags, mod.targets)
				})
			}
		}
	}

	for _, arch := range architectures {
		testName := fmt.Sprintf("build_agent_%s", arch)
		t.Run(testName, func(t *testing.T) {
			runBuild(t, root, arch, agentTarget)
		})
	}
}

func runVet(t *testing.T, dir, arch, tags string, targets []string) {
	t.Helper()

	watchInputs(t, dir, arch, tags, targets)

	out, err := goCommand(dir, arch, tags, "vet", targets).CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			t.Fatalf("go vet:\n%s", out)
		}
		t.Fatalf("go vet: %v", err)
	}
}

func runBuild(t *testing.T, dir, arch, target string) {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "agent")
	out, err := goCommand(dir, arch, "", "build", []string{target}, "-o", bin).CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			t.Fatalf("go build:\n%s", out)
		}
		t.Fatalf("go build: %v", err)
	}
}

// goCommand returns a go command for FreeBSD on arch, run in dir.
func goCommand(dir, arch, tags, verb string, targets []string, flags ...string) *exec.Cmd {
	args := append([]string{verb}, flags...)
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, targets...)

	cmd := exec.Command("go", args...)
	cmd.Dir = dir

	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOOS=") && !strings.HasPrefix(kv, "GOARCH=") && !strings.HasPrefix(kv, "CGO_ENABLED=") {
			env = append(env, kv)
		}
	}
	env = append(env,
		"GOOS=freebsd",
		"GOARCH="+arch,
		"CGO_ENABLED=0",
	)
	cmd.Env = env

	return cmd
}

// watchInputs makes the files go vet reads inputs of the test. go test caches
// a pass and rechecks only the files the test opened and the directories it
// read inside the module root, and what a child process reads is not among
// them.
// Reading each first-party directory that go list reports, test packages
// included, puts its entry names, sizes, and modification times into the cache
// key, and reading go.mod and go.sum covers dependency changes, so an edit
// reruns the gate. A go list that fails or lists nothing fails the gate, since
// a skipped gate reads as a pass.
func watchInputs(t *testing.T, dir, arch, tags string, targets []string) {
	t.Helper()

	root := repoRoot(t)

	cmd := goCommand(dir, arch, tags, "list", targets, "-deps", "-test", "-f", "{{.Dir}}")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			t.Fatalf("go list:\n%s", exitErr.Stderr)
		}
		t.Fatalf("go list: %v", err)
	}

	dirs := map[string]bool{}
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !within(root, line) {
			continue
		}
		dirs[line] = true
	}
	if len(dirs) == 0 {
		t.Fatalf("go list found no first-party package under %s; the gate checked nothing", dir)
	}

	for d := range dirs {
		if _, err := os.ReadDir(d); err != nil {
			t.Fatalf("reading directory %q: %v", d, err)
		}
	}

	for _, name := range []string{
		filepath.Join(root, "go.mod"),
		filepath.Join(root, "go.sum"),
		filepath.Join(root, "src", "edge", "netpen", "go.mod"),
		filepath.Join(root, "src", "edge", "netpen", "go.sum"),
	} {
		if _, err := os.ReadFile(name); err != nil {
			t.Fatalf("reading %q: %v", name, err)
		}
	}
}

// within reports whether path is root or lies under it.
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
