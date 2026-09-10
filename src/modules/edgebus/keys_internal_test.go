package edgebus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidEdgeID(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want bool
	}{
		{"0192e6a0-0000-7000-8000-0000000000ed", true},
		{"edge_1", true},
		{strings.Repeat("a", 64), true},
		{"", false},
		{strings.Repeat("a", 65), false},
		{"../escape", false},
		{"edge/1", false},
		{"edge.1", false},
		{"edge 1", false},
		{"edge*", false},
		{"edge>", false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			if got := validEdgeID(tc.id); got != tc.want {
				t.Errorf("validEdgeID(%q) = %v, want %v", tc.id, got, tc.want)
			}
		})
	}
}

func TestWriteSecretFileRestrictsAFileAnEarlierRunLeftReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hub.creds")
	if err := os.WriteFile(path, []byte("previous"), 0o644); err != nil {
		t.Fatalf("plant the readable file: %v", err)
	}

	if err := writeSecretFile(path, []byte("current")); err != nil {
		t.Fatalf("writeSecretFile: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600: os.WriteFile would have kept the mode the file already had", perm)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(body) != "current" {
		t.Errorf("content = %q, want %q", body, "current")
	}
}

func TestWriteSecretFileSweepsWhatAnInterruptedWriteLeft(t *testing.T) {
	dir := t.TempDir()
	leftover := filepath.Join(dir, secretTempPrefix+"271828")
	if err := os.WriteFile(leftover, []byte("a usable credential"), 0o600); err != nil {
		t.Fatalf("plant the leftover: %v", err)
	}

	if err := writeSecretFile(filepath.Join(dir, "hub.creds"), []byte("current")); err != nil {
		t.Fatalf("writeSecretFile: %v", err)
	}

	remaining, err := filepath.Glob(filepath.Join(dir, secretTempPrefix+"*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(remaining) != 0 {
		t.Errorf("temporary files left holding secret material: %v", remaining)
	}
}

func TestKeySeedsAreWrittenPrivatelyAndLeaveNoTemporaries(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadOrCreateKeys(dir); err != nil {
		t.Fatalf("loadOrCreateKeys: %v", err)
	}

	keysDir := filepath.Join(dir, "keys")
	entries, err := os.ReadDir(keysDir)
	if err != nil {
		t.Fatalf("read keys directory: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("keys directory holds %d entries, want the three account seeds", len(entries))
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), secretTempPrefix) {
			t.Errorf("%s is a temporary file the write should have renamed away", entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("stat %s: %v", entry.Name(), err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %o, want 600", entry.Name(), perm)
		}
	}
}
