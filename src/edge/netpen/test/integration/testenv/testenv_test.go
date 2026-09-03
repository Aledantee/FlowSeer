package testenv_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/test/integration/testenv"
)

func TestDockerUnavailableClassification(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil"},
		{name: "sentinel", err: testenv.ErrDockerUnavailable, want: true},
		{name: "wrapped", err: fmt.Errorf("start tier: %w", testenv.ErrDockerUnavailable), want: true},
		{name: "same message", err: errors.New(testenv.ErrDockerUnavailable.Error())},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := testenv.IsDockerUnavailable(tt.err); got != tt.want {
				t.Errorf("got unavailable=%t, want %t", got, tt.want)
			}
		})
	}
}

func fakeDocker(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\n"+script+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestDockerComposeVersion(t *testing.T) {
	for _, tt := range []struct {
		name   string
		script string
		want   string
	}{
		{name: "available", script: "printf '  v2.99.0\\n'", want: "v2.99.0"},
		{name: "empty", script: "printf '  \\n'"},
		{name: "failed", script: "printf 'v2.99.0'; exit 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fakeDocker(t, tt.script)
			if got := testenv.DockerComposeVersion(); got != tt.want {
				t.Errorf("got version %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDockerComposeVersionTimeout(t *testing.T) {
	fakeDocker(t, "exec /bin/sleep 30")
	started := time.Now()
	if got := testenv.DockerComposeVersion(); got != "" {
		t.Errorf("got version %q from stalled command, want empty", got)
	}
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Errorf("version probe took %v, want completion within 8 seconds", elapsed)
	}
}
