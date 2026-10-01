package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"unknown"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("run() error = nil, want error")
	}
	if !strings.Contains(stderr.String(), "Usage: deps") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestCleanLineRemovesTSVDelimiters(t *testing.T) {
	if got, want := cleanLine("summary\twith\nline\rend"), "summary with line end"; got != want {
		t.Fatalf("cleanLine() = %q, want %q", got, want)
	}
}

func TestRunAgeReadsLockfilesWithoutClassification(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "go.mod"), "module example.test/root\n\ngo 1.27\n\nrequire example.test/dependency v1.0.0\n")
	writeTestFile(t, filepath.Join(root, "go.sum"), "example.test/dependency v1.0.0 h1:dependency\nexample.test/dependency v1.0.0/go.mod h1:mod\n")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"Version":"v1.0.0","Time":"2024-01-23T18:54:04Z"}`))
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{"age", "--root", root, "--go-proxy-url", server.URL}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v, stderr = %q", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "example.test/dependency\tv1.0.0") {
		t.Fatalf("stdout = %q, want publication output", stdout.String())
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
