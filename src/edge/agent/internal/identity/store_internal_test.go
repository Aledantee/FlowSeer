package identity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteSyncsTheDirectoryEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state")
	synced := false
	store := &Store{
		dir: dir,
		syncDirectory: func() error {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("directory sync ran before the rename: %v", err)
			}
			synced = true
			return nil
		},
	}

	if err := store.writeAtomically(path, []byte("durable")); err != nil {
		t.Fatalf("writeAtomically: %v", err)
	}
	if !synced {
		t.Error("writeAtomically returned before syncing the directory entry")
	}
}
