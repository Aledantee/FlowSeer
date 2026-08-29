package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// TestAE4JSONModeHygiene verifies JSON-mode output hygiene: stdout decodes as JSONL with
// schema_version on every line, a leading meta record, and zero escape
// sequences.
func TestAE4JSONModeHygiene(t *testing.T) {
	recs := syntheticFeed()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	w := NewJSONWriter(stdout, stderr, *recs[0].Meta)
	if err := w.Run(recs[1:]); err != nil {
		t.Fatalf("Run: unexpected error: %v", err)
	}

	out := stdout.Bytes()

	// No escape sequences (0x1b) may appear in stdout in JSON mode.
	if idx := bytes.IndexByte(out, 0x1b); idx >= 0 {
		t.Errorf("stdout contains escape sequence at byte %d", idx)
	}

	lines := splitJSONL(t, out)
	if len(lines) == 0 {
		t.Fatal("stdout has no JSONL lines")
	}

	// First line must be the meta record.
	var first map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("first line is not valid JSON: %v", err)
	}
	if first["kind"] != "meta" {
		t.Errorf("first line kind = %v, want meta", first["kind"])
	}

	// Every line must carry schema_version: 1.
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Errorf("line %d is not valid JSON: %v", i, err)
			continue
		}
		sv, ok := rec["schema_version"]
		if !ok {
			t.Errorf("line %d: missing schema_version", i)
			continue
		}
		if sv != float64(1) {
			t.Errorf("line %d: schema_version = %v, want 1", i, sv)
		}
	}
}

// TestJSONLAllRecordKinds verifies every record kind passes through the
// writer verbatim.
func TestJSONLAllRecordKinds(t *testing.T) {
	recs := syntheticFeed()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	w := NewJSONWriter(stdout, stderr, *recs[0].Meta)
	if err := w.Run(recs[1:]); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := splitJSONL(t, stdout.Bytes())
	wantKinds := []string{
		"meta", "finding", "progress",
		"resisted", "skipped", "pending",
		"error", "refusal", "summary",
	}
	if len(lines) != len(wantKinds) {
		t.Fatalf("got %d lines, want %d", len(lines), len(wantKinds))
	}
	for i, want := range wantKinds {
		var rec map[string]any
		if err := json.Unmarshal(lines[i], &rec); err != nil {
			t.Errorf("line %d: %v", i, err)
			continue
		}
		if rec["kind"] != want {
			t.Errorf("line %d kind = %v, want %s", i, rec["kind"], want)
		}
	}
}

// TestEmptyRunJSON verifies an empty run produces meta + summary only.
func TestEmptyRunJSON(t *testing.T) {
	recs := emptyFeed()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	w := NewJSONWriter(stdout, stderr, *recs[0].Meta)
	if err := w.Run(recs[1:]); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := splitJSONL(t, stdout.Bytes())
	if len(lines) != 2 {
		t.Fatalf("empty run produced %d lines, want 2 (meta + summary)", len(lines))
	}

	var first map[string]any
	if err := json.Unmarshal(lines[0], &first); err != nil {
		t.Fatalf("line 0: %v", err)
	}
	if first["kind"] != "meta" {
		t.Errorf("line 0 kind = %v, want meta", first["kind"])
	}

	var second map[string]any
	if err := json.Unmarshal(lines[1], &second); err != nil {
		t.Fatalf("line 1: %v", err)
	}
	if second["kind"] != "summary" {
		t.Errorf("line 1 kind = %v, want summary", second["kind"])
	}
}

// TestSecretValueExclusion feeds a Secret-carrying record through the writer
// and asserts the secret value bytes are absent from stdout bytes.
func TestSecretValueExclusion(t *testing.T) {
	recs := secretCarryingFeed()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	w := NewJSONWriter(stdout, stderr, *recs[0].Meta)
	if err := w.Run(recs[1:]); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := stdout.Bytes()
	secretValue := []byte("supersecretcommunity")
	if bytes.Contains(out, secretValue) {
		t.Errorf("stdout contains the secret value bytes")
	}

	// The redacted form (protocol + length) should be present.
	if !bytes.Contains(out, []byte(`"snmp"`)) {
		t.Errorf("stdout missing the secret protocol name")
	}
	if !bytes.Contains(out, []byte(`"length":20`)) {
		t.Errorf("stdout missing the secret length")
	}
}

// TestEPIPEHandling verifies a stdout write failure (simulated by a writer
// that returns an error) degrades to a named coded error rather than a panic.
func TestEPIPEHandling(t *testing.T) {
	recs := syntheticFeed()
	stderr := &bytes.Buffer{}

	w := NewJSONWriter(&errorWriter{}, stderr, *recs[0].Meta)
	err := w.Run(recs[1:])
	if err == nil {
		t.Fatal("Run: expected error for stdout write failure, got nil")
	}
	if !IsStdoutClosed(err) {
		t.Errorf("Run: error is not the named stdout-closed error: %v", err)
	}
}

// TestNoEscapeBytesSyntheticFeed scans stdout bytes for 0x1b over the full
// synthetic feed, asserting no escape sequences can ever reach stdout in JSON
// mode.
func TestNoEscapeBytesSyntheticFeed(t *testing.T) {
	for _, feed := range [][]findings.Record{syntheticFeed(), emptyFeed(), secretCarryingFeed()} {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}

		w := NewJSONWriter(stdout, stderr, *feed[0].Meta)
		if err := w.Run(feed[1:]); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if idx := bytes.IndexByte(stdout.Bytes(), 0x1b); idx >= 0 {
			t.Errorf("stdout contains escape sequence at byte %d", idx)
		}
	}
}

// TestStdoutPurity verifies stderr receives nothing (no records leak to
// stderr) and stdout contains only JSONL lines.
func TestStdoutPurity(t *testing.T) {
	recs := syntheticFeed()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	w := NewJSONWriter(stdout, stderr, *recs[0].Meta)
	if err := w.Run(recs[1:]); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if stderr.Len() > 0 {
		t.Errorf("stderr is not empty: %q", stderr.String())
	}

	// Every non-empty stdout line must be valid JSON.
	for i, line := range bytes.Split(stdout.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Errorf("stdout line %d is not valid JSON: %v", i, err)
		}
	}
}

// errorWriter is an io.Writer that always returns an error, simulating a
// closed stdout pipe (EPIPE).
type errorWriter struct{}

func (errorWriter) Write(_ []byte) (int, error) {
	return 0, io.ErrClosedPipe
}

// splitJSONL splits stdout bytes into JSONL lines, skipping trailing empty
// lines.
func splitJSONL(t *testing.T, data []byte) [][]byte {
	t.Helper()
	var lines [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

// TestRollupParityJSON verifies resisted/skipped/pending rollup records pass
// through the JSON writer with their verdict intact.
func TestRollupParityJSON(t *testing.T) {
	recs := syntheticFeed()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	w := NewJSONWriter(stdout, stderr, *recs[0].Meta)
	if err := w.Run(recs[1:]); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines := splitJSONL(t, stdout.Bytes())
	rollups := map[string]bool{}
	for _, line := range lines {
		var rec findings.Record
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.Kind {
		case findings.KindResisted, findings.KindSkipped, findings.KindPending:
			if rec.Rollup != nil {
				rollups[string(rec.Kind)] = true
			}
		}
	}
	for _, kind := range []string{"resisted", "skipped", "pending"} {
		if !rollups[kind] {
			t.Errorf("missing %s rollup record in JSON output", kind)
		}
	}
}

// TestStdoutClosedIsErrors verifies IsStdoutClosed matches the named error
// and not unrelated errors.
func TestStdoutClosedIsErrors(t *testing.T) {
	if !IsStdoutClosed(errStdoutClosed) {
		t.Error("IsStdoutClosed does not match errStdoutClosed")
	}
	if IsStdoutClosed(errors.New("unrelated")) {
		t.Error("IsStdoutClosed matched an unrelated error")
	}
	if IsStdoutClosed(nil) {
		t.Error("IsStdoutClosed matched nil")
	}
}

// TestJSONWriterFlush verifies Flush works without error on a valid writer.
func TestJSONWriterFlush(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	w := NewJSONWriter(stdout, stderr, findings.Meta{
		Tool:      "netpen",
		Version:   "test",
		AttackLeg: "eth0",
		Started:   time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC),
	})
	if err := w.Flush(); err != nil {
		t.Errorf("Flush: %v", err)
	}
}
