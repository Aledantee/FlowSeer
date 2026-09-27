package testenv

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestFixtureModulesInSync fails when the fixture-module copies in
// the netopeer2 Docker build context drift from the yanggen testdata
// source of truth (build contexts cannot reach outside their
// directory, so the copies exist; this gate keeps them honest).
func TestFixtureModulesInSync(t *testing.T) {
	source := filepath.Join("..", "..", "..", "cmd", "yanggen", "testdata", "modules")
	copies := filepath.Join("testdata", "netopeer2")
	for _, name := range []string{
		"fixture-types.yang", "fixture-grp.yang", "fixture-main.yang", "fixture-main-sub.yang", "fixture-aug.yang",
	} {
		want, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatalf("read source %s: %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(copies, name))
		if err != nil {
			t.Fatalf("read copy %s: %v", name, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s drifted from cmd/yanggen/testdata/modules — re-copy it", name)
		}
	}
}

// yangDependency matches a YANG import or include statement's module name.
var yangDependency = regexp.MustCompile(`(?m)^\s*(?:import|include)\s+([A-Za-z_][\w.-]*)`)

// TestFixtureDependenciesInBuildContext fails when a fixture copy imports
// or includes a module the build context does not hold, which would make
// sysrepoctl reject the install and the image fail to build. Standard
// modules the base image ships are not imported by the fixtures.
func TestFixtureDependenciesInBuildContext(t *testing.T) {
	copies := filepath.Join("testdata", "netopeer2")
	files, err := filepath.Glob(filepath.Join(copies, "*.yang"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range yangDependency.FindAllSubmatch(src, -1) {
			dep := string(m[1]) + ".yang"
			if _, err := os.Stat(filepath.Join(copies, dep)); err != nil {
				t.Errorf("%s depends on %s, which is not in the netopeer2 build context", filepath.Base(f), dep)
			}
		}
	}
}
