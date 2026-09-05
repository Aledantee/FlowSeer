//go:build linux || darwin

package credential

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// testKey is the credential key every test fixture in this file uses.
const testKey = "icx7150-lab-snmp"

func writeCredential(t *testing.T, dir string, content []byte, mode os.FileMode, version uint64) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, testKey), content, mode); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	writeMeta(t, dir, version)
}

func writeMeta(t *testing.T, dir string, version uint64) {
	t.Helper()
	meta := []byte(`{"version":` + strconv.FormatUint(version, 10) + `}`)
	if err := os.WriteFile(filepath.Join(dir, testKey+metaSuffix), meta, 0o600); err != nil {
		t.Fatalf("write credential metadata: %v", err)
	}
}

func openProvider(t *testing.T, dir string) *Provider {
	t.Helper()
	p, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func wantCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatal("Get succeeded, want an error")
	}
	code, ok := errs.CodeOf(err)
	if !ok || code != want {
		t.Errorf("code = %v (ok=%v), want %v: %v", code, ok, want, err)
	}
}

func TestProviderGetValidCredential(t *testing.T) {
	dir := t.TempDir()
	writeCredential(t, dir, []byte("s3cr3t"), 0o600, 3)
	p := openProvider(t, dir)

	got, err := p.Get(testKey, 3)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "s3cr3t" {
		t.Errorf("material = %q, want %q", got, "s3cr3t")
	}
}

func TestProviderRefusesInvalidKeys(t *testing.T) {
	dir := t.TempDir()
	p := openProvider(t, dir)

	for _, key := range []string{"../root", "site/lab", "", "UPPER", "trailing/"} {
		t.Run(key, func(t *testing.T) {
			_, err := p.Get(key, 1)
			wantCode(t, err, ErrCodeInvalidKey)
		})
	}
}

func TestProviderRefusesMissingCredential(t *testing.T) {
	dir := t.TempDir()
	p := openProvider(t, dir)

	_, err := p.Get("absent", 1)
	wantCode(t, err, ErrCodeNotFound)
}

func TestProviderRefusesSymlinkedCredential(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "real"), []byte("attacker-secret"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "real"), filepath.Join(dir, testKey)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	writeMeta(t, dir, 1)
	p := openProvider(t, dir)

	_, err := p.Get(testKey, 1)
	wantCode(t, err, ErrCodeSymlinkRefused)
}

func TestProviderRefusesGroupOrWorldMode(t *testing.T) {
	dir := t.TempDir()
	writeCredential(t, dir, []byte("s3cr3t"), 0o640, 1)
	p := openProvider(t, dir)

	_, err := p.Get(testKey, 1)
	wantCode(t, err, ErrCodeInsecureMode)
}

func TestProviderRefusesUnparsableMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, testKey), []byte("s3cr3t"), 0o600); err != nil {
		t.Fatalf("write credential file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, testKey+metaSuffix), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write credential metadata: %v", err)
	}
	p := openProvider(t, dir)

	_, err := p.Get(testKey, 1)
	wantCode(t, err, ErrCodeInvalidMetadata)
}

func TestProviderRefusesVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	writeCredential(t, dir, []byte("s3cr3t"), 0o600, 2)
	p := openProvider(t, dir)

	_, err := p.Get(testKey, 3)
	wantCode(t, err, ErrCodeVersionMismatch)
}

// TestProviderRefusesReplacementRace proves that once a credential file
// has been read, replacing it with a symlink never lets a later Get follow
// it: the swap is refused at the open syscall itself, not raced between a
// check and a read.
func TestProviderRefusesReplacementRace(t *testing.T) {
	dir := t.TempDir()
	writeCredential(t, dir, []byte("original-secret"), 0o600, 1)
	if err := os.WriteFile(filepath.Join(dir, "attacker"), []byte("attacker-secret"), 0o600); err != nil {
		t.Fatalf("write attacker target: %v", err)
	}
	p := openProvider(t, dir)

	got, err := p.Get(testKey, 1)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if string(got) != "original-secret" {
		t.Fatalf("material = %q, want %q", got, "original-secret")
	}

	credPath := filepath.Join(dir, testKey)
	if err := os.Remove(credPath); err != nil {
		t.Fatalf("remove credential file: %v", err)
	}
	if err := os.Symlink(filepath.Join(dir, "attacker"), credPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	_, err = p.Get(testKey, 1)
	wantCode(t, err, ErrCodeSymlinkRefused)
}

// TestProviderConcurrentSwapNeverLeaksTheReplacedTarget races Get against a
// goroutine that repeatedly swaps the credential entry between the real
// file and a symlink to a canary file with different content. Every
// successful Get must return the original content; a Get that instead
// races the symlink is refused, never followed.
func TestProviderConcurrentSwapNeverLeaksTheReplacedTarget(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(dir, testKey)
	attackerPath := filepath.Join(dir, "attacker")
	const original = "original-secret"
	const attacker = "attacker-secret"

	if err := os.WriteFile(attackerPath, []byte(attacker), 0o600); err != nil {
		t.Fatalf("write attacker target: %v", err)
	}
	writeCredential(t, dir, []byte(original), 0o600, 1)
	p := openProvider(t, dir)

	regularStage := filepath.Join(dir, "regular-stage")
	if err := os.WriteFile(regularStage, []byte(original), 0o600); err != nil {
		t.Fatalf("write regular staging file: %v", err)
	}
	symlinkStage := filepath.Join(dir, "symlink-stage")
	if err := os.Symlink(attackerPath, symlinkStage); err != nil {
		t.Fatalf("symlink staging file: %v", err)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Go(func() {
		// os.Rename is the atomic half of the swap: whichever entry
		// Get() observes at credPath is always either a complete
		// regular file or a complete symlink, never a partial write.
		toggle := false
		for !stop.Load() {
			if toggle {
				_ = os.Rename(symlinkStage, credPath)
				_ = os.Symlink(attackerPath, symlinkStage)
			} else {
				_ = os.Rename(regularStage, credPath)
				_ = os.WriteFile(regularStage, []byte(original), 0o600)
			}
			toggle = !toggle
			time.Sleep(time.Microsecond)
		}
	})

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		got, err := p.Get(testKey, 1)
		if err == nil && string(got) != original {
			stop.Store(true)
			wg.Wait()
			t.Fatalf("Get returned %q, must never return the swapped-in target's content", got)
		}
		if err != nil {
			if code, ok := errs.CodeOf(err); !ok || (code != ErrCodeSymlinkRefused && code != ErrCodeNotFound) {
				stop.Store(true)
				wg.Wait()
				t.Fatalf("unexpected error: %v", err)
			}
		}
	}
	stop.Store(true)
	wg.Wait()

	// Leave the fixture in its regular-file state so the final assertion
	// below observes a normal read.
	_ = os.Remove(credPath)
	if err := os.WriteFile(credPath, []byte(original), 0o600); err != nil {
		t.Fatalf("restore credential file: %v", err)
	}
	got, err := p.Get(testKey, 1)
	if err != nil {
		t.Fatalf("final Get: %v", err)
	}
	if string(got) != original {
		t.Fatalf("material = %q, want %q", got, original)
	}
}
