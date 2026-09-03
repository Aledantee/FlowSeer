package output

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
)

// ErrCodeJSONWrite is the wire identity for a JSONL writer failure (a stdout
// write error mid-run, typically EPIPE when the consumer closed the pipe).
var ErrCodeJSONWrite = errs.NewCode("netpen/json-write")

// ErrCodeJSONMarshal identifies records that cannot be encoded as JSON.
var ErrCodeJSONMarshal = errs.NewCode("netpen/json-marshal")

var errStdoutClosed = errs.New().
	Code(ErrCodeJSONWrite).
	ExitCode(1).
	Msg("stdout write failed")

// JSONWriter emits one JSON object per line. Use [JSONWriter.Run] for a batch,
// or [JSONWriter.WriteMeta], [JSONWriter.Write], and [JSONWriter.WriteSummary]
// for a stream. Callers report returned errors and supply the closing summary.
// Construct it with [NewJSONWriter]; the zero value is unusable. Methods are
// not safe for concurrent use, and each instance represents one run.
type JSONWriter struct {
	out  *bufio.Writer
	meta *findings.Meta
}

// NewJSONWriter buffers records for a non-nil stdout. It copies meta without
// writing it; use [JSONWriter.Run] or [JSONWriter.WriteMeta] to emit the header.
// stderr is unused; callers decide where to report returned errors.
func NewJSONWriter(stdout, _ io.Writer, meta findings.Meta) *JSONWriter {
	return &JSONWriter{
		out:  bufio.NewWriter(stdout),
		meta: &meta,
	}
}

// Run writes the meta header followed by recs and flushes on success. recs must
// exclude the header and include the closing summary. It stops at the first
// error, leaving any buffered records unflushed. Errors wrap the original cause
// with [ErrCodeJSONWrite] for stdout failures or [ErrCodeJSONMarshal] for invalid
// JSON. A failed stdout writer must be discarded.
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

	return w.Flush()
}

func (w *JSONWriter) writeRecord(r findings.Record) error {
	data, err := json.Marshal(r)
	if err != nil {
		return errs.From(err).Code(ErrCodeJSONMarshal).Msg("marshal JSONL record")
	}

	if _, err := w.out.Write(data); err != nil {
		return stdoutWriteError(err)
	}
	if _, err := w.out.Write([]byte("\n")); err != nil {
		return stdoutWriteError(err)
	}
	return nil
}

// Write emits and flushes one record so a streaming consumer sees it immediately.
// Errors have the same codes and causes as [JSONWriter.Run]. Invalid JSON emits
// no bytes for that record and leaves the writer usable.
func (w *JSONWriter) Write(r findings.Record) error {
	if err := w.writeRecord(r); err != nil {
		return err
	}
	return w.Flush()
}

// WriteMeta emits and flushes the run header. Call it once before streaming
// records. Errors have the same codes and causes as [JSONWriter.Run].
func (w *JSONWriter) WriteMeta() error {
	header := findings.NewRecord(findings.KindMeta)
	header.Time = w.meta.Started
	header.Meta = w.meta
	if err := w.writeRecord(header); err != nil {
		return err
	}
	return w.Flush()
}

// WriteSummary emits and flushes a closing summary with the current timestamp.
// Errors have the same codes and causes as [JSONWriter.Run].
func (w *JSONWriter) WriteSummary(s findings.Summary) error {
	rec := findings.NewRecord(findings.KindSummary)
	rec.Time = time.Now()
	rec.Summary = &s
	if err := w.writeRecord(rec); err != nil {
		return err
	}
	return w.Flush()
}

// Flush writes buffered records to stdout. Errors wrap the underlying write
// failure with [ErrCodeJSONWrite]; discard the writer after a stdout failure.
func (w *JSONWriter) Flush() error {
	if err := w.out.Flush(); err != nil {
		return stdoutWriteError(err)
	}
	return nil
}

func stdoutWriteError(err error) error {
	return errs.From(err).Code(ErrCodeJSONWrite).ExitCode(1).Msg("stdout write failed")
}

// IsStdoutClosed reports whether err wraps a stdout write failure identified by
// [ErrCodeJSONWrite]. It matches any stdout failure, including a short write.
func IsStdoutClosed(err error) bool {
	return errors.Is(err, errStdoutClosed)
}
