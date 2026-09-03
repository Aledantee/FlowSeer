package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
	"go.aledante.io/FlowSeer/src/edge/netpen/findings"
	"go.aledante.io/FlowSeer/src/edge/netpen/output"
)

type failingWriter struct {
	writes int
	failAt int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestStreamAndRunPreservesOutputFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
	}{
		{name: "meta", failAt: 1},
		{name: "record", failAt: 2},
		{name: "summary", failAt: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch := make(chan findings.Record)
			var stderr bytes.Buffer
			err := streamAndRun(context.Background(), ch, findings.Meta{}, output.ModeJSON,
				&failingWriter{failAt: tc.failAt}, &stderr, func(context.Context) error {
					defer close(ch)
					ch <- findings.NewRecord(findings.KindFinding)
					ch <- findings.NewRecord(findings.KindFinding)
					return nil
				})
			if !errors.Is(err, io.ErrClosedPipe) {
				t.Errorf("error = %v, want closed pipe cause", err)
			}
			if errs.ExitCode(err) != 1 {
				t.Errorf("exit code = %d, want 1", errs.ExitCode(err))
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want caller-owned diagnostics", stderr.String())
			}
		})
	}
}

func TestStreamAndRunPreservesRunAndMarshalFailures(t *testing.T) {
	ch := make(chan findings.Record)
	runErr := errs.New().ExitCode(2).Msg("runner failed")
	var stdout, stderr bytes.Buffer
	err := streamAndRun(context.Background(), ch, findings.Meta{}, output.ModeJSON,
		&stdout, &stderr, func(context.Context) error {
			defer close(ch)
			rec := findings.NewRecord(findings.KindFinding)
			rec.Finding = &findings.Finding{Detail: json.RawMessage(`{`)}
			ch <- rec
			ch <- findings.NewRecord(findings.KindFinding)
			return runErr
		})
	var syntaxErr *json.SyntaxError
	if !errors.Is(err, runErr) || !errors.As(err, &syntaxErr) {
		t.Errorf("error = %v, want runner and JSON syntax causes", err)
	}
	if errs.ExitCode(err) != 2 {
		t.Errorf("exit code = %d, want runner's exit code 2", errs.ExitCode(err))
	}
}

func TestVersionOutputFailureExitsOne(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"version"}, &failingWriter{failAt: 1}, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

func TestOutputFailureCancelsOngoingProducer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	ch := make(chan findings.Record)
	cleanedUp := false
	err := streamAndRun(ctx, ch, findings.Meta{}, output.ModeJSON,
		&failingWriter{failAt: 1}, io.Discard, func(ctx context.Context) error {
			defer close(ch)
			for {
				select {
				case <-ctx.Done():
					ch <- findings.NewRecord(findings.KindSummary)
					cleanedUp = true
					return ctx.Err()
				case ch <- findings.NewRecord(findings.KindFinding):
				}
			}
		})
	if !errors.Is(err, io.ErrClosedPipe) || !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want closed pipe and cancellation causes", err)
	}
	if ctx.Err() != nil {
		t.Errorf("parent context = %v, want cancellation before its timeout", ctx.Err())
	}
	if !cleanedUp {
		t.Error("runner cleanup did not finish")
	}
}

func TestUnexpectedArgumentsDoNotDispatch(t *testing.T) {
	original := commands
	t.Cleanup(func() { commands = original })
	called := false
	commands = []subcommand{{name: "fake", run: func(context.Context, *cmdFlags, io.Writer, io.Writer) error {
		called = true
		return nil
	}}}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"fake", "unexpected", "-i", "test0"}, &stdout, &stderr); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if called {
		t.Error("command dispatched despite unexpected positional arguments")
	}
}
