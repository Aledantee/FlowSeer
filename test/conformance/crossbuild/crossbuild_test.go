package crossbuild

import (
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

// TestCrossBuild validates that platform-specific packages and nested modules
// compile cleanly under FreeBSD across supported architectures and build tags.
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
}

func runVet(t *testing.T, dir, arch, tags string, targets []string) {
	t.Helper()

	args := []string{"vet"}
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

	out, err := cmd.CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			t.Fatalf("go vet:\n%s", out)
		}
		t.Fatalf("go vet: %v", err)
	}
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
