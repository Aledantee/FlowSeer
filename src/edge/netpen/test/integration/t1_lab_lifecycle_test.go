//go:build netpen_t1

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFRRStartupFailureCleanup(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "commands")
	t.Setenv("NETPEN_TEST_COMMAND_LOG", logPath)
	t.Setenv("PATH", dir)
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$NETPEN_TEST_COMMAND_LOG\"\ncase \"$*\" in\n  *' up -d') exit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := startFRRLab(context.Background(), "fake-compose.yml")
	if err == nil {
		t.Fatal("got nil startup error, want failed compose up")
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(commands), "down -v --remove-orphans") {
		t.Errorf("partial startup has no cleanup command: %s", commands)
	}
}

func TestFRRCleanupFailure(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	script := "#!/bin/sh\ncase \"$*\" in\n  *' up -d') exit 1;;\n  *' down -v --remove-orphans') printf 'cleanup unavailable\\n' >&2; exit 2;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	_, cleanup, err := startFRRLab(context.Background(), "fake-compose.yml")
	if err == nil {
		t.Fatal("got nil startup error, want failed compose up")
	}
	if err := cleanup(); err == nil || !strings.Contains(err.Error(), "cleanup unavailable") {
		t.Errorf("got cleanup error %v, want command failure with diagnostic", err)
	}
}
