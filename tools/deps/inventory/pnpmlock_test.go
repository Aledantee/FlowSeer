package inventory

import (
	"path/filepath"
	"testing"
)

func TestParsePnpmLockCollapsesPeerVariants(t *testing.T) {
	entries, direct, err := parsePnpmLock(filepath.Join("testdata", "pnpm", "pnpm-lock.yaml"), "frontend/web/pnpm-lock.yaml")
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if got := entries[0]; got.Name != "reka-ui" || got.Version != "2.10.5" || got.Hash != "sha512-reka" || !got.Direct {
		t.Fatalf("first entry = %#v", got)
	}
	if got := entries[1]; got.Name != "vue" || got.Version != "3.5.43" || got.Hash != "sha512-vue" || !got.Direct {
		t.Fatalf("second entry = %#v", got)
	}
	if len(direct) != 2 || direct[0].Name != "reka-ui" || direct[0].Version != "2.10.5" {
		t.Fatalf("direct = %#v", direct)
	}
}
