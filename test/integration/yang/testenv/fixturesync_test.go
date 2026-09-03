package testenv

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestFixtureModulesInSync fails when the fixture-module copies in
// the netopeer2 Docker build context drift from the yanggen testdata
// source of truth (build contexts cannot reach outside their
// directory, so the copies exist; this gate keeps them honest).
func TestFixtureModulesInSync(t *testing.T) {
	source := filepath.Join("..", "..", "..", "..", "src", "common", "yang", "cmd", "yanggen", "testdata", "modules")
	copies := filepath.Join("testdata", "netopeer2")
	for _, name := range []string{
		"fixture-types.yang", "fixture-main.yang", "fixture-main-sub.yang", "fixture-aug.yang",
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
