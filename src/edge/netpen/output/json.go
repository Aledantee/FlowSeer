package output

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// ErrCodeJSONWrite is the wire identity for a JSONL writer failure (a stdout
// write error mid-run, typically EPIPE when the consumer closed the pipe).
var ErrCodeJSONWrite = errs.NewCode("netpen/json-write")

// errStdoutClosed is the named error returned when a stdout write fails. It
// carries ErrCodeJSONWrite so callers can branch on the code.
var errStdoutClosed = errs.New().
	Code(ErrCodeJSONWrite).
	ExitCode(1).
	Msg("stdout write failed")

// JSONWriter emits findings records as JSONL on stdout. Stdout carries only
// records — one JSON object per line, the run-header meta record first, and a
// trailing summary. Human progress and diagnostics go to stderr. The writer is
// single-use: one Run call per instance.
type JSONWriter struct {
	out    *bufio.Writer
	stderr io.Writer
	meta   *findings.Meta
}

// NewJSONWriter constructs a JSONWriter that writes records to stdout and
// diagnostics to stderr. The meta record is emitted as the first line of the
// run.
func NewJSONWriter(stdout, stderr io.Writer, meta findings.Meta) *JSONWriter {
	return &JSONWriter{
		out:    bufio.NewWriter(stdout),
		stderr: stderr,
		meta:   &meta,
	}
}

// Run writes the meta header, then each record from recs as one JSONL line,
// then flushes. It returns a named coded error if a stdout write fails
// (typically EPIPE when the consumer closes the pipe); the error carries
// ErrCodeJSONWrite.
func (w *JSONWriter) Run(recs []findings.Record) error {
	header := findings.NewRecord(findings.KindMeta)
	header.Time = w.meta.Started
	header.Meta = w.meta
	if err := w.writeRecord(header); err != nil {
		return err
	}

	for _, r := range recs {
		if err := w.writeRecord(r); err != nil {
			return err
		}
	}

	if err := w.out.Flush(); err != nil {
		return errStdoutClosed
	}
	return nil
}

// writeRecord marshals and writes one record as a JSONL line. A write failure
// (EPIPE, closed pipe) is wrapped into the named coded error so the caller
// degrades rather than panicking.
func (w *JSONWriter) writeRecord(r findings.Record) error {
	data, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintf(w.stderr, "json marshal error: %v\n", err)
		return errStdoutClosed
	}

	if _, err := w.out.Write(data); err != nil {
		return errStdoutClosed
	}
	if _, err := w.out.Write([]byte("\n")); err != nil {
		return errStdoutClosed
	}
	return nil
}

// Write marshals and writes one record as a JSONL line and flushes the
// buffer, so a streaming consumer sees each record as it arrives (live
// arrival). A write failure returns the named coded error so the caller
// can degrade rather than panic. Use [Run] for the buffered batch path.
func (w *JSONWriter) Write(r findings.Record) error {
	if err := w.writeRecord(r); err != nil {
		return err
	}
	return w.Flush()
}

// WriteMeta writes only the run-header meta record and flushes. It is the
// empty-run path: a run with no findings still emits the meta record followed
// by a summary.
func (w *JSONWriter) WriteMeta() error {
	header := findings.NewRecord(findings.KindMeta)
	header.Time = w.meta.Started
	header.Meta = w.meta
	if err := w.writeRecord(header); err != nil {
		return err
	}
	if err := w.out.Flush(); err != nil {
		return errStdoutClosed
	}
	return nil
}

// WriteSummary writes a closing summary record and flushes.
func (w *JSONWriter) WriteSummary(s findings.Summary) error {
	rec := findings.NewRecord(findings.KindSummary)
	rec.Time = time.Now()
	rec.Summary = &s
	if err := w.writeRecord(rec); err != nil {
		return err
	}
	if err := w.out.Flush(); err != nil {
		return errStdoutClosed
	}
	return nil
}

// Flush flushes the buffered writer to stdout.
func (w *JSONWriter) Flush() error {
	if err := w.out.Flush(); err != nil {
		return errStdoutClosed
	}
	return nil
}

// IsStdoutClosed reports whether err is the named stdout-closed error.
func IsStdoutClosed(err error) bool {
	return errors.Is(err, errStdoutClosed)
}
