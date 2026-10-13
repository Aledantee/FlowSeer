package identity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/prototext"

	agentv1 "go.aledante.io/FlowSeer/generated/go/proto/flowseer/store/agent/v1"
	"go.aledante.io/FlowSeer/src/common/errs"
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

func newBootStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

func readBootFile(t *testing.T, store *Store) string {
	t.Helper()
	body, err := os.ReadFile(store.bootPath())
	if err != nil {
		t.Fatalf("read the boot generation file: %v", err)
	}
	return string(body)
}

// persistedGeneration parses the file as an operator would read it. The text
// encoder varies its spacing on purpose, so a test compares the parsed value.
func persistedGeneration(t *testing.T, store *Store) uint64 {
	t.Helper()
	persisted := &agentv1.RawWindowEpoch{}
	if err := prototext.Unmarshal([]byte(readBootFile(t, store)), persisted); err != nil {
		t.Fatalf("parse the boot generation file: %v", err)
	}
	return persisted.GetBootGeneration()
}

func TestAdvanceBootGenerationCountsFromOneAcrossRestarts(t *testing.T) {
	dir := t.TempDir()

	for want := uint64(1); want <= 3; want++ {
		store, err := NewStore(dir)
		if err != nil {
			t.Fatalf("NewStore: %v", err)
		}
		got, err := store.AdvanceBootGeneration()
		if err != nil {
			t.Fatalf("AdvanceBootGeneration: %v", err)
		}
		if got != want {
			t.Fatalf("generation after %d starts = %d, want %d", want, got, want)
		}
	}

	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if got := persistedGeneration(t, store); got != 3 {
		t.Errorf("persisted generation = %d, want 3", got)
	}
}

func TestAdvanceBootGenerationFailsWhenTheDirectoryEntryCannotBeFlushed(t *testing.T) {
	store := newBootStore(t)
	store.syncDirectory = func() error { return errors.New("disk refused the flush") }

	got, err := store.AdvanceBootGeneration()
	if err == nil {
		t.Fatalf("AdvanceBootGeneration = %d with no error, want the failed flush returned", got)
	}
	if code, ok := errs.CodeOf(err); !ok || code != ErrCodeState {
		t.Errorf("error code = %v, want %v", code, ErrCodeState)
	}
	if got != 0 {
		t.Errorf("generation = %d alongside an error, want 0: the caller must not collect", got)
	}
}

func TestAdvanceBootGenerationFailsWhenTheNewValueCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("a root process writes into a read-only directory")
	}
	store := newBootStore(t)
	if _, err := store.AdvanceBootGeneration(); err != nil {
		t.Fatalf("first AdvanceBootGeneration: %v", err)
	}

	if err := os.Chmod(store.dir, 0o500); err != nil {
		t.Fatalf("make the state directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(store.dir, 0o700) })

	got, err := store.AdvanceBootGeneration()
	if err == nil {
		t.Fatalf("AdvanceBootGeneration = %d with no error, want the failed write returned", got)
	}
	if got != 0 {
		t.Errorf("generation = %d alongside an error, want 0", got)
	}

	if err := os.Chmod(store.dir, 0o700); err != nil {
		t.Fatalf("restore the state directory: %v", err)
	}
	if got := persistedGeneration(t, store); got != 1 {
		t.Errorf("persisted generation after the failed write = %d, want the previous 1", got)
	}
	if got, err := store.AdvanceBootGeneration(); err != nil || got != 2 {
		t.Errorf("AdvanceBootGeneration after the failure = %d, %v, want 2", got, err)
	}
}

func TestAdvanceBootGenerationRefusesAFileItCannotTrust(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "garbage", body: "not a message"},
		{name: "empty file", body: ""},
		{name: "zero generation", body: "boot_generation: 0\n"},
		{name: "wrong type", body: "boot_generation: \"three\"\n"},
		{name: "epoch that is not a uuid", body: "boot_generation: 4\nprocess_epoch: \"epoch\"\n"},
		{name: "largest counter", body: "boot_generation: 18446744073709551615\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newBootStore(t)
			if err := os.WriteFile(store.bootPath(), []byte(tt.body), 0o600); err != nil {
				t.Fatalf("seed the boot generation file: %v", err)
			}

			got, err := store.AdvanceBootGeneration()
			if err == nil {
				t.Fatalf("AdvanceBootGeneration = %d with no error, want the file refused", got)
			}
			if code, ok := errs.CodeOf(err); !ok || code != ErrCodeState {
				t.Errorf("error code = %v, want %v", code, ErrCodeState)
			}
			if body := readBootFile(t, store); body != tt.body {
				t.Errorf("file = %q after a refusal, want it left as it was: %q", body, tt.body)
			}
		})
	}
}

func TestAdvanceBootGenerationRefusesADirectoryInPlaceOfTheFile(t *testing.T) {
	store := newBootStore(t)
	if err := os.Mkdir(store.bootPath(), 0o700); err != nil {
		t.Fatalf("make a directory at the file's path: %v", err)
	}

	if got, err := store.AdvanceBootGeneration(); err == nil {
		t.Errorf("AdvanceBootGeneration = %d with no error, want the unreadable path refused", got)
	}
}
