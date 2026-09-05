package ssh

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Command is one line sent to the shell and the caller-supplied
// patterns that mark where its output ends.
type Command struct {
	// Line is the text written to the shell, followed by "\n".
	Line string
	// Redacted, when non-empty, replaces Line in the returned
	// Evidence. Set it whenever Line carries a credential; this
	// package never scans Line for secret shapes on its own.
	Redacted string

	// Prompts are the candidate lines that mark the command
	// complete; the first to match the tail of the accumulated
	// output wins. At least one is required.
	Prompts []Prompt

	// MorePattern, when non-nil, detects a pagination marker (e.g. a
	// "--More--" line). On a match MoreKeystroke is written and
	// reading continues; the marker text is stripped from
	// Result.Output.
	MorePattern *regexp.Regexp
	// MoreKeystroke is written to the shell when MorePattern
	// matches. Required when MorePattern is set.
	MoreKeystroke []byte

	// MaxOutput bounds Result.Output. Non-positive means the
	// session default (see [Options]).
	MaxOutput int
	// Deadline bounds this Run call. Non-positive means the
	// session default ([Options.CommandDeadline]).
	Deadline time.Duration
}

// Result is what a command produced.
type Result struct {
	// Output is the command's stdout, with the echoed input line,
	// pagination markers, and the matched prompt text stripped.
	Output []byte
	// Truncated reports whether Output was capped short of the
	// stream's true size; Evidence.BytesReceived carries the true
	// count.
	Truncated bool
	// Stderr is the stderr bytes observed while this command ran.
	Stderr []byte
	// StderrTruncated mirrors Truncated for Stderr.
	StderrTruncated bool
	// MatchedPrompt is the Name of the Prompt that ended the
	// command.
	MatchedPrompt string
	// Evidence is the redacted record of what this command did.
	Evidence Evidence
}

// Evidence is a per-command audit record. It never contains device
// output beyond byte counts, and Sent never contains a credential
// when Command.Redacted was set: it is safe to log or attach to a
// durable record.
type Evidence struct {
	// Sent is exactly what was written to the shell's stdin:
	// Command.Redacted if set, else Command.Line, plus the
	// trailing newline.
	Sent []byte
	// BytesReceived is the true number of stdout bytes observed
	// while this command ran, independent of any truncation.
	BytesReceived int64
	// StderrBytesReceived mirrors BytesReceived for stderr.
	StderrBytesReceived int64
	// Started is when the command was written.
	Started time.Time
	// Elapsed is how long the command took to complete (or fail).
	Elapsed time.Duration
}

// Run sends cmd and blocks until one of cmd.Prompts matches, cmd's
// deadline (or the session default) or ctx expires, or the connection
// is lost. Run calls on one Session must not overlap: the shell has
// one input stream.
//
// Any failure to complete cleanly closes the Session — the local read
// cursor can no longer be trusted to align with the remote shell once
// a wait is abandoned — so a subsequent Run returns [ErrSessionClosed].
// A canceled or timed-out wait surfaces the unwrapped context error.
func (s *Session) Run(ctx context.Context, cmd Command) (Result, error) {
	if s.isClosed() {
		return Result{}, ErrSessionClosed
	}
	if len(cmd.Prompts) == 0 {
		return Result{}, errs.New().Code(ErrCodeShell).Msg("command: at least one prompt is required")
	}
	if cmd.MorePattern != nil && len(cmd.MoreKeystroke) == 0 {
		return Result{}, errs.New().Code(ErrCodeShell).Msg("command: MoreKeystroke is required when MorePattern is set")
	}

	deadline := cmd.Deadline
	if deadline <= 0 {
		deadline = s.opts.CommandDeadline
	}
	runCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	started := time.Now()
	sent := []byte(cmd.Line)
	if cmd.Redacted != "" {
		sent = []byte(cmd.Redacted)
	}
	evidence := Evidence{Sent: sent, Started: started}

	stdoutStart, _ := s.stdout.stats()
	stderrStart, _ := s.stderr.stats()

	if _, err := s.stdin.Write([]byte(cmd.Line + "\n")); err != nil {
		_ = s.Close()
		evidence.Elapsed = time.Since(started)
		return Result{Evidence: evidence}, s.connectionLostErr(err)
	}

	var output bytes.Buffer
	promptName := ""
loop:
	for {
		var res scanResult
		matched, err := s.stdout.waitFor(runCtx, func(buf []byte) (int, bool) {
			r, ok := scanPrompt(buf, cmd.Prompts, cmd.MorePattern)
			if !ok {
				return 0, false
			}
			res = r
			return r.consumed, true
		})
		if err != nil {
			_ = s.Close()
			evidence.Elapsed = time.Since(started)
			return Result{Output: output.Bytes(), Evidence: evidence}, s.waitErr(err)
		}
		output.Write(matched[:res.outputEnd])
		if res.kind == scanMore {
			if _, err := s.stdin.Write(cmd.MoreKeystroke); err != nil {
				_ = s.Close()
				evidence.Elapsed = time.Since(started)
				return Result{Output: output.Bytes(), Evidence: evidence}, s.connectionLostErr(err)
			}
			continue loop
		}
		promptName = res.promptName
		break
	}

	outBytes := stripEcho(output.Bytes(), cmd.Line)
	maxOutput := cmd.MaxOutput
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutputBytes
	}
	truncated := false
	if len(outBytes) > maxOutput {
		outBytes = outBytes[len(outBytes)-maxOutput:]
		truncated = true
	}

	stdoutEnd, _ := s.stdout.stats()
	stderrEnd, stderrRingTruncated := s.stderr.stats()
	evidence.BytesReceived = stdoutEnd - stdoutStart
	evidence.StderrBytesReceived = stderrEnd - stderrStart
	evidence.Elapsed = time.Since(started)

	return Result{
		Output:          outBytes,
		Truncated:       truncated,
		Stderr:          s.stderr.drain(),
		StderrTruncated: stderrRingTruncated,
		MatchedPrompt:   promptName,
		Evidence:        evidence,
	}, nil
}

// stripEcho removes a leading echo of line from buf, when the shell
// reflects the sent input back as the first line of its response.
func stripEcho(buf []byte, line string) []byte {
	echo := []byte(line)
	if !bytes.HasPrefix(buf, echo) {
		return buf
	}
	rest := buf[len(echo):]
	rest = bytes.TrimPrefix(rest, []byte("\r\n"))
	rest = bytes.TrimPrefix(rest, []byte("\n"))
	return rest
}

// connectionLostErr wraps a stdin write failure.
func (s *Session) connectionLostErr(err error) error {
	return errs.From(err).Code(ErrCodeConnectionLost).Msg("write command to shell")
}

// waitErr maps a ring closure or context failure into the error Run
// returns, preferring the unwrapped context error per convention.
func (s *Session) waitErr(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, ErrSessionClosed) {
		return ErrSessionClosed
	}
	return errs.From(err).Code(ErrCodeConnectionLost).Msg("read command output")
}
