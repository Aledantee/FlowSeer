package ssh

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"go.aledante.io/FlowSeer/src/common/errs"
)

// Command is one line sent to the shell and the caller-supplied
// patterns that mark where its output ends.
type Command struct {
	// Line is the text written to the shell, followed by "\n". It is one
	// line: Run refuses a Line containing CR or LF.
	//
	// Assembling it is the caller's, and a caller that interpolates a value
	// it did not choose — an interface name, a description, anything that
	// reached it from outside — is the one deciding whether that value can
	// end this command and begin another. The refusal below is the backstop,
	// not the check: it fires after the value is already in the string, so a
	// caller that wants a diagnosis naming the field validates before
	// building the Line.
	Line string
	// Redacted, when non-empty, replaces Line in the returned
	// Evidence. Set it whenever Line carries a credential; this
	// package never scans Line for secret shapes on its own.
	Redacted string

	// Prompts are the candidate lines that mark the command complete. The
	// earliest match anywhere in the accumulated output wins, ties going to
	// the order of this slice — not the tail, and not the first prompt
	// listed. A pattern must therefore be anchored so it cannot match
	// inside the device's own output: a configuration line ending in the
	// prompt character would otherwise end the command early and return a
	// partial transcript indistinguishable from a complete one. At least
	// one is required.
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
	// package default of 1MiB.
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
	// Truncated reports whether Output is short of what the device
	// actually sent, either because it exceeded Command.MaxOutput or
	// because the session's stdout buffer dropped bytes before a
	// prompt matched; Evidence.BytesReceived carries the true count
	// either way.
	Truncated bool
	// Stderr is the stderr bytes observed since the previous Run
	// call (or since Dial, for the first one).
	Stderr []byte
	// StderrTruncated reports whether the session's stderr buffer
	// dropped bytes during this command's window.
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
	// Sent is exactly the text written to the shell's stdin, before
	// the trailing newline: Command.Redacted if set, else
	// Command.Line.
	Sent []byte
	// BytesReceived is the true number of stdout bytes observed
	// while this command ran, independent of any truncation. Set
	// even when Run returns an error.
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
// Any bytes the shell produced before Run was called (a login banner,
// or stray output between commands) are discarded first: only bytes
// produced in response to this command are scanned for a prompt or
// counted as this command's Stderr.
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
	// CR as well as LF: the shell runs under a PTY, where carriage return is
	// the enter key, so a Line carrying either sends everything after it as
	// a second command the caller never wrote. Refused here because this is
	// where the invariant is knowable — one line in, one command out — and
	// because a caller filtering only "\n" would still be wrong.
	if cmd.MorePattern != nil && cmd.MorePattern.MatchString("") {
		// A pattern matching the empty string consumes nothing, so the read
		// loop matches it again immediately and writes MoreKeystroke at CPU
		// speed for the whole command deadline.
		return Result{}, errs.New().Code(ErrCodeShell).Msg("command: MorePattern must not match the empty string")
	}
	for _, p := range cmd.Prompts {
		if p.Pattern != nil && p.Pattern.MatchString("") {
			return Result{}, errs.New().Code(ErrCodeShell).Attr("prompt", p.Name).
				Msg("command: a prompt pattern must not match the empty string")
		}
	}
	if strings.ContainsAny(cmd.Line, "\r\n") {
		return Result{}, errs.New().Code(ErrCodeShell).
			Msg("command: Line carries a line terminator, which would send a second command")
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

	// Discard anything left over from before this command (a login
	// banner ahead of the first Run, or stray bytes between
	// commands) so the scan below never matches stale output, and so
	// Result.Stderr and StderrTruncated cover only this command's
	// window.
	s.stdout.reset()
	s.stderr.reset()
	stdoutStart, _ := s.stdout.stats()
	stderrStart, _ := s.stderr.stats()

	finish := func(output []byte, err error) (Result, error) {
		stdoutEnd, stdoutTruncated := s.stdout.stats()
		stderrEnd, stderrTruncated := s.stderr.stats()
		evidence.BytesReceived = stdoutEnd - stdoutStart
		evidence.StderrBytesReceived = stderrEnd - stderrStart
		evidence.Elapsed = time.Since(started)
		return Result{
			Output:          output,
			Truncated:       stdoutTruncated,
			Stderr:          s.stderr.drain(),
			StderrTruncated: stderrTruncated,
			Evidence:        evidence,
		}, err
	}

	if err := runCtx.Err(); err != nil {
		// Checked before the write, not only after. A command written under
		// an already-canceled context reaches the device and then returns a
		// cancellation, which a caller reads as "nothing happened" — on the
		// mutation path, for a config command that executed.
		return finish(nil, err)
	}
	if _, err := s.stdin.Write([]byte(cmd.Line + "\n")); err != nil {
		_ = s.Close()
		return finish(nil, s.connectionLostErr(err))
	}

	echo := []byte(cmd.Line)
	var output bytes.Buffer
	promptName := ""
	for {
		var res scanResult
		var skip int
		matched, err := s.stdout.waitFor(runCtx, func(buf []byte) (int, bool) {
			skip = echoSkipLen(buf, echo)
			r, ok := scanPrompt(buf[skip:], cmd.Prompts, cmd.MorePattern)
			if !ok {
				return 0, false
			}
			r.outputEnd += skip
			r.consumed += skip
			res = r
			return r.consumed, true
		})
		if err != nil {
			_ = s.Close()
			return finish(output.Bytes(), s.waitErr(err))
		}
		output.Write(matched[skip:res.outputEnd])
		if res.kind == scanMore {
			if _, err := s.stdin.Write(cmd.MoreKeystroke); err != nil {
				_ = s.Close()
				return finish(output.Bytes(), s.connectionLostErr(err))
			}
			continue
		}
		promptName = res.promptName
		break
	}

	outBytes := output.Bytes()
	maxOutput := cmd.MaxOutput
	if maxOutput <= 0 {
		maxOutput = defaultMaxOutputBytes
	}
	capTruncated := false
	if len(outBytes) > maxOutput {
		outBytes = outBytes[len(outBytes)-maxOutput:]
		capTruncated = true
	}

	res, err := finish(outBytes, nil)
	res.Truncated = res.Truncated || capTruncated
	res.MatchedPrompt = promptName
	return res, err
}

// echoSkipLen reports how many leading bytes of buf are the shell's
// echo of echo, so the caller can exclude them from a prompt scan.
// It reports 0 until buf holds at least len(echo) bytes matching it
// exactly: a shell that never echoes is a supported configuration,
// not a partial match to wait out.
func echoSkipLen(buf, echo []byte) int {
	if len(echo) == 0 || !bytes.HasPrefix(buf, echo) {
		return 0
	}
	rest := buf[len(echo):]
	switch {
	case bytes.HasPrefix(rest, []byte("\r\n")):
		return len(echo) + 2
	case bytes.HasPrefix(rest, []byte("\n")):
		return len(echo) + 1
	default:
		return len(echo)
	}
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
