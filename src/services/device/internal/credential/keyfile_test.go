//go:build linux || darwin

package credential_test

import (
	"os"
	"path/filepath"
	"testing"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/services/device/internal/credential"
)

func wantErrCode(t *testing.T, err error, want errs.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want %s", want)
	}
	got, ok := errs.CodeOf(err)
	if !ok {
		t.Fatalf("error carries no code: %v", err)
	}
	if got != want {
		t.Errorf("error code = %s, want %s (%v)", got, want, err)
	}
}

func TestReadKeyFileRefusesAbsentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent-key")

	_, err := credential.ReadKeyFile(path)
	wantErrCode(t, err, credential.ErrCodeNotFound)
}

func TestReadKeyFileRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "real-key")
	if err := os.WriteFile(realPath, []byte("super-secret"), 0o600); err != nil {
		t.Fatalf("WriteFile real-key: %v", err)
	}

	symlinkPath := filepath.Join(dir, "symlink-key")
	if err := os.Symlink(realPath, symlinkPath); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	_, err := credential.ReadKeyFile(symlinkPath)
	wantErrCode(t, err, credential.ErrCodeSymlinkRefused)
}

func TestReadKeyFileRefusesGroupOrWorldMode(t *testing.T) {
	dir := t.TempDir()

	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o666, 0o777} {
		path := filepath.Join(dir, "insecure-key")
		if err := os.WriteFile(path, []byte("super-secret"), mode); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		_, err := credential.ReadKeyFile(path)
		wantErrCode(t, err, credential.ErrCodeInsecureMode)
		_ = os.Remove(path)
	}
}

func TestReadKeyFileRefusesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	emptyPath := filepath.Join(dir, "empty-key")
	if err := os.WriteFile(emptyPath, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := credential.ReadKeyFile(emptyPath)
	wantErrCode(t, err, credential.ErrCodeInvalidMaterial)

	// A file containing only a newline also trims to empty and is refused.
	newlineOnlyPath := filepath.Join(dir, "newline-only-key")
	if err := os.WriteFile(newlineOnlyPath, []byte("\n"), 0o600); err != nil {
		t.Fatalf("WriteFile newline only: %v", err)
	}

	_, err = credential.ReadKeyFile(newlineOnlyPath)
	wantErrCode(t, err, credential.ErrCodeInvalidMaterial)
}

func TestReadKeyFileTrimsTrailingNewline(t *testing.T) {
	dir := t.TempDir()

	// 1. Secret with trailing newline
	keyWithNewline := filepath.Join(dir, "key-with-nl")
	if err := os.WriteFile(keyWithNewline, []byte("secret-token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	val, err := credential.ReadKeyFile(keyWithNewline)
	if err != nil {
		t.Fatalf("ReadKeyFile key-with-nl: %v", err)
	}
	if got := val.RevealString(); got != "secret-token" {
		t.Fatalf("got %q, want %q", got, "secret-token")
	}

	// 2. Secret without trailing newline
	keyNoNewline := filepath.Join(dir, "key-no-nl")
	if err := os.WriteFile(keyNoNewline, []byte("secret-token"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	val, err = credential.ReadKeyFile(keyNoNewline)
	if err != nil {
		t.Fatalf("ReadKeyFile key-no-nl: %v", err)
	}
	if got := val.RevealString(); got != "secret-token" {
		t.Fatalf("got %q, want %q", got, "secret-token")
	}

	// 3. Secret with two trailing newlines trims only one
	keyTwoNewlines := filepath.Join(dir, "key-two-nl")
	if err := os.WriteFile(keyTwoNewlines, []byte("secret-token\n\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	val, err = credential.ReadKeyFile(keyTwoNewlines)
	if err != nil {
		t.Fatalf("ReadKeyFile key-two-nl: %v", err)
	}
	if got := val.RevealString(); got != "secret-token\n" {
		t.Fatalf("got %q, want %q", got, "secret-token\n")
	}
}
