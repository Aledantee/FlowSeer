package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateResolution rewrites the committed resolution fixture. Run with
//
//	go test ./src/protocol/snmp/cmd/mibgen -run TestModuleResolution -update-resolution
//
// after a deliberate change to what a configured module name resolves
// to — a re-synced spec/mib/ tree, or a new module in mibgen.yaml.
var updateResolution = flag.Bool("update-resolution", false, "rewrite testdata/module-resolution.txt")

const resolutionFixture = "testdata/module-resolution.txt"

const resolutionFixtureHeader = `# Module resolution pinned for mibgen.
#
# One line per configured module: the module name, the file that name
# resolves to relative to the repository root, and the SHA-256 of that
# file. The generated header carries the same path and digest, so a
# module quietly resolving to a sibling file — a dated revision of the
# same MIB, say — would move every binding in its package without
# anything else in the build noticing. Pinning it here turns that into a
# named test failure.
#
# Regenerate with:
#   go test ./src/protocol/snmp/cmd/mibgen -run TestModuleResolution -update-resolution
`

// repoRoot returns the repository root as seen from this package's test
// working directory.
func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}

	return root
}

// TestModuleResolution pins which file each configured module name
// resolves to, and what that file contains.
//
// The generated header names the source path and its digest, so this is
// the one input that can move every byte of a generated package while
// every other test still passes.
func TestModuleResolution(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "spec", "mib")); err != nil {
		t.Skipf("spec/mib not available: %v", err)
	}

	cfg, err := LoadConfig(filepath.Join(root, "mibgen.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	set, err := LoadModules(cfg)
	if err != nil {
		t.Fatalf("load modules: %v", err)
	}

	lines := make([]string, 0, len(cfg.Modules))
	for _, cm := range cfg.Modules {
		mod, ok := set.Module(cm.Name)
		if !ok {
			t.Fatalf("module %q missing from the resolved set", cm.Name)
		}
		rel, err := filepath.Rel(root, mod.File)
		if err != nil {
			t.Fatalf("module %q: relativize %s: %v", cm.Name, mod.File, err)
		}
		b, err := os.ReadFile(mod.File)
		if err != nil {
			t.Fatalf("module %q: read %s: %v", cm.Name, mod.File, err)
		}
		sum := sha256.Sum256(b)
		lines = append(lines, cm.Name+"\t"+filepath.ToSlash(rel)+"\t"+hex.EncodeToString(sum[:]))
	}

	got := resolutionFixtureHeader + "\n" + strings.Join(lines, "\n") + "\n"

	if *updateResolution {
		if err := os.WriteFile(resolutionFixture, []byte(got), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		t.Logf("updated %s (%d modules)", resolutionFixture, len(lines))

		return
	}

	want, err := os.ReadFile(resolutionFixture)
	if err != nil {
		t.Fatalf("read fixture (rerun with -update-resolution if first time): %v", err)
	}
	if string(want) != got {
		t.Errorf("module resolution moved.\n--- committed ---\n%s\n--- resolved ---\n%s", want, got)
	}
}
