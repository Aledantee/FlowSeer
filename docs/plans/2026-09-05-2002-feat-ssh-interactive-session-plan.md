---
title: SSH Interactive Session Library - Plan
type: feat
date: 2026-09-05
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
execution: code
---

# SSH Interactive Session Library - Plan

> Implemented. Units U1-U4 landed as described; the two Open questions were
> resolved as written (two independent pinned-key scenarios instead of a
> `known_hosts` format; buffer defaults documented in the README). One
> pre-existing suppression pattern was reused, not invented: `options.go`
> carries the same `//nolint:gosec` on `ssh.InsecureIgnoreHostKey()` that
> `src/protocol/netconf/session.go` already carries for the identical
> finding. An independent review round found and fixed real defects the
> initial implementation missed: `Run` did not discard bytes left over from
> before the command (a login banner could be matched as the command's own
> prompt), the echo of the sent line was scanned for a prompt match before
> being stripped (a command ending in prompt-shaped text could terminate
> itself), `Result.Truncated`/`StderrTruncated` only reflected the
> `MaxOutput` cap and never the ring's own drop-oldest loss, and
> `Evidence.BytesReceived` was left at zero on every error path. All four
> are fixed and covered by new tests; see the commit history for detail.

## Goal

Add `src/protocol/ssh/`, a domain-free SSH interactive-shell client: one
caller opens one shell per session, runs commands against caller-supplied
prompt and pagination patterns with per-command deadlines, and gets back an
evidence record that never carries a credential. A later plan builds the
FastIron adapter on top of it. Stop condition: if driving a real FastIron
transcript through the designed `Prompt`/pagination API turns out to need the
package to know an enable transition or a `--More--` string by name, the
design is wrong and this plan is void.

## Decisions

- Follow `src/protocol/netconf/options.go`'s `Options` shape for SSH
  transport: `HostKeySHA256` XOR `InsecureIgnoreHostKey`, no implicit
  default, same mutual-exclusivity error. Why: it is the accepted precedent
  the task names, and duplicating its refusal behavior keeps the two
  packages' security posture identical for reviewers.
- One package-level `Dial` opens the TCP connection, the SSH transport, and
  the one shell channel (with a PTY) in a single call, returning a ready
  `*Session`. Why: decision 11 keeps this package domain-free and single-shell
  per decision 11 and the task; splitting transport-dial from shell-open
  (as `netconf` does for its swappable `Transport`) buys nothing here because
  there is no second transport implementation to swap in — the test seam is
  a real `golang.org/x/crypto/ssh` server on loopback, not a fake
  `Transport`. Unconfirmed: revisit if a second transport (e.g. a serial
  console) is ever asked for.
- Prompt and pagination patterns are supplied per `Run` call, not fixed at
  `Dial` time, as `Command.Prompts` and `Command.MorePattern`/
  `Command.MoreKeystroke`. Why: this is what lets a privilege-level
  transition happen without the package knowing what "enable" is — the
  adapter calls `Run` with `Prompts` naming both the password prompt and the
  next privilege prompt as candidates, reads which one matched from
  `Result.MatchedPrompt`, and decides what to send next. The package sees an
  opaque `Prompt.Name` string it never interprets.
- A command's secret material is redacted by the caller, not detected by the
  package: `Command.Redacted`, if non-empty, replaces `Command.Line` in the
  evidence record; the package does no pattern-based secret scrubbing. Why:
  `docs/code-style.md` "never attach or interpolate raw secret material"
  is a producer-side obligation; a scrubbing heuristic in the transport
  layer would either miss a credential shape it doesn't recognize or redact
  configuration text that happens to look like one.
- Bounded output is a sliding tail window (drop-oldest), not a hard cap that
  stops reading. Why: a prompt or a pagination marker is expected at the end
  of the stream, so keeping the most recent bytes preserves the signal that
  matters even when a command floods output past the cap; `Result.Truncated`
  and `Evidence.BytesReceived` (the true total) tell the caller what was
  lost.
- Context cancellation during `Run` closes the session rather than leaving it
  attached. Why: the byte stream position relative to the remote shell is
  only known while a scan is in progress; once a caller gives up mid-scan,
  the local read cursor and the remote prompt state can no longer be
  trusted to align, so the safe move is decision 11's spirit applied
  locally — treat it as fenced, not resumable. This is also literally what
  the task's "explicit close-on-cancel" requirement asks for.
- Timeouts surface as the unwrapped `ctx.Err()` (`context.DeadlineExceeded`/
  `context.Canceled`), not a package error code, per
  `docs/code-style.md` §Errors "Context cancellation surfaces as the
  unwrapped `ctx.Err()`". A command deadline is implemented as
  `context.WithTimeout` around the wait, nothing more.
- "Fixed and known-host keys" test coverage means two independent positive
  pin scenarios (two servers, two distinct host keys, each pinned
  correctly), not a `known_hosts` file feature. Why: nothing in the task or
  decision 10 asks for `known_hosts` parsing, and the single `HostKeySHA256`
  field already is the pin; the point of two servers is proving the pin
  check is not accidentally hardcoded to one key. Unconfirmed, flagged in
  Open questions.

## Requirements

1. `Dial(ctx, addr, Options)` refuses to connect when neither
   `HostKeySHA256` nor `InsecureIgnoreHostKey` is set, and when both are set.
   Acceptance: `Options{}` (all zero) returns an error before any network
   I/O.
2. `Dial` accepts a peer whose host key's SHA-256 fingerprint equals
   `HostKeySHA256` (with or without the `SHA256:` prefix) and refuses one
   that does not, without the mismatch error ever containing the observed
   key. Acceptance: two fake servers with different generated host keys,
   each dialed with its own correct pin, both succeed; dialing either with
   the other's pin fails.
3. `Dial` respects `ctx` and `Options.DialTimeout`: a server that accepts the
   TCP connection but never completes the SSH handshake causes `Dial` to
   return by the deadline with `context.DeadlineExceeded` (or the timeout
   it derives), not hang. Acceptance: a raw `net.Listener` that `Accept`s and
   never writes the SSH version line.
4. `Session.Run` sends `Command.Line` (or, if I already hold the shell's
   stdin, `Line`) and blocks until one of `Command.Prompts` matches the tail
   of the accumulated stdout, returning `Result.MatchedPrompt` as that
   prompt's `Name`. Acceptance: a scripted fake shell that writes a prompt
   split across three separate TCP writes still matches.
5. `Session.Run` strips the remote's local echo of the sent line from
   `Result.Output` when the shell echoes it back verbatim as the first
   line of the response. Acceptance: fake shell that echoes `"show version\r\n"`
   before its real output; `Result.Output` does not start with the echoed
   command.
6. When `Command.MorePattern` matches before any `Command.Prompts` entry,
   `Session.Run` writes `Command.MoreKeystroke`, keeps reading, and the
   pagination marker text does not appear in `Result.Output`. Acceptance: a
   fake shell that sends two pages separated by `"--More--"` and only
   continues after receiving the configured keystroke.
7. A per-command deadline (`Command.Deadline`, else the session default)
   bounds `Run` independently of `Dial`'s timeout. Acceptance: a fake shell
   that never sends a matching prompt causes `Run` to return
   `context.DeadlineExceeded` at the configured bound, and the session is
   closed afterward.
8. Canceling the `ctx` passed to `Run` mid-wait returns `context.Canceled`
   and leaves the session closed (a subsequent `Run` returns
   `ErrSessionClosed`). Acceptance: cancel a context while a fake shell is
   deliberately silent.
9. Output and stderr are held in bounded, drop-oldest buffers; a command that
   floods far past the configured cap still returns (not deadlocks), with
   `Result.Truncated` set and `Evidence.BytesReceived` equal to the true
   byte count observed, not the retained tail length. Acceptance: fake shell
   writes 10x the configured stdout cap before the matching prompt.
10. A remote that writes continuously to stderr without the caller ever
    reading it does not block stdout delivery or the session. Acceptance:
    fake shell goroutine writes to the stderr channel in a tight loop while
    a command runs to completion on stdout.
11. If the connection is lost after `Command.Line` has been written but
    before a prompt matches, `Run` returns an error and `Result.Evidence`
    still reports the bytes actually sent. Acceptance: fake shell closes the
    underlying `net.Conn` immediately after reading the command line.
12. `Evidence.Sent` never contains `Command.Line` when `Command.Redacted` is
    set; it contains `Command.Redacted` instead. Acceptance: a command
    with a password in `Line` and a fixed placeholder in `Redacted`.
13. A fuzz test over the prompt/pagination scanner never panics and never
    reports a match position outside the scanned buffer, for arbitrary
    input bytes against a fixed set of prompt and more patterns.

## Out of scope

- Telnet.
- A `known_hosts` file format, host-key rotation, or certificate-based host
  verification — `HostKeySHA256` pinning only, matching `netconf`.
- Any FastIron, Ruckus, or other vendor vocabulary; that is the later
  adapter plan under `src/modules/localnet`.
- OpenTelemetry spans or metrics: the task states none are required and the
  package has no caller yet to size cardinality against.
- Re-authentication, connection pooling, or multiple shells per session.
- Line-oriented terminal emulation (ANSI/VT100 escape interpretation); the
  scanner works on raw bytes and leaves escape sequences in `Result.Output`
  for the caller to strip if it cares.

## Units

### U1. Options and host-key verification
Files: `src/protocol/ssh/options.go`, `src/protocol/ssh/errors.go`,
`src/protocol/ssh/doc.go`
After: none
Change: `Options` (`Username`, `Password`, `PrivateKeyPEM`, `HostKeySHA256`,
`InsecureIgnoreHostKey`, `DialTimeout`, `PTYTerm`, `PTYWidth`, `PTYHeight`,
`StdoutBufferBytes`, `StderrBufferBytes`) with `withDefaults`; an unexported
`sshConfig(Options) (*ssh.ClientConfig, error)` mirroring
`src/protocol/netconf/options.go`'s auth and host-key branches
(`pinnedHostKey`, `hasSHA256Prefix`) exactly, adapted to this package's error
codes. `errors.go` declares `ErrCodeTransport`, `ErrCodeShell`,
`ErrCodeSessionClosed`, `ErrCodeConnectionLost`, and `ErrSessionClosed`.
Tests: `options_test.go` — missing username/credential, missing and
double host-key settings, `SHA256:`-prefixed and bare pin acceptance.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh/options.go src/protocol/ssh/errors.go src/protocol/ssh/doc.go src/protocol/ssh/options_test.go`

### U2. Dial, Session lifecycle, and bounded draining
Files: `src/protocol/ssh/session.go`, `src/protocol/ssh/buffer.go`
After: U1
Change: `Dial(ctx, addr string, opts Options) (*Session, error)` dials TCP
with `net.Dialer.DialContext`, completes the SSH handshake with a watcher
goroutine that closes the raw conn if `ctx` is done before
`ssh.NewClientConn` returns (the pattern the task's "context-aware dial"
requirement asks for, since `x/crypto/ssh` has no context-aware dial of its
own), opens one `ssh.Session`, requests a PTY, starts the shell, and launches
two goroutines draining stdout/stderr into the bounded, drop-oldest ring
buffer type in `buffer.go`. `Session.Close() error` closes the shell,
signals both drain goroutines, and waits for them; it takes no context
because nothing on the close path itself can block indefinitely (deviates
from the plan's original `Close(ctx)` sketch — nothing needed one). The
bounded buffer exposes a cursor-based wait (`waitFor(ctx, match)
([]byte, error)`) that U3 drives, plus a monotonic total-bytes-written
counter independent of the retained tail so truncation never loses the
true count.
Tests: `session_test.go` (package `ssh_test`, using a shared fake-SSH-server
helper) — host-key pin match on two distinct fake servers, a bare
(unprefixed) pinned fingerprint, pin mismatch refusal (error never contains
the observed fingerprint), dial timeout against a non-responding accepted
connection, `Close` is idempotent; `command_test.go`'s
`TestSessionCloseDuringRunUnblocksTheWait` covers a concurrent `Close`
unblocking an in-flight `Run`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh/session.go src/protocol/ssh/buffer.go src/protocol/ssh/session_test.go`

### U3. Command execution: scanning, pagination, deadlines, evidence
Files: `src/protocol/ssh/command.go`, `src/protocol/ssh/scan.go`
After: U2
Change: `Prompt{Name string; Pattern *regexp.Regexp}`, `Command{Line,
Redacted string; Prompts []Prompt; MorePattern *regexp.Regexp;
MoreKeystroke []byte; MaxOutput int; Deadline time.Duration}`,
`Result{Output, Stderr []byte; Truncated, StderrTruncated bool;
MatchedPrompt string; Evidence Evidence}`, `Evidence{Sent []byte;
BytesReceived, StderrBytesReceived int; Started time.Time; Elapsed
time.Duration}`. `scan.go` holds the pure scanner
`scanPrompt(buf []byte, prompts []Prompt, more *regexp.Regexp) (result
scanResult)` that `Session.Run` drives against the bounded buffer's tail:
matches a `more` pattern first (triggering the keystroke-and-continue
loop) and otherwise the longest-anchored prompt match, returning enough
position information for `Run` to split echoed input, pagination markers,
and the matched prompt text out of `Result.Output`. `Run` writes
`Command.Line`+`"\n"` to stdin, records `Evidence.Sent` from
`Redacted`-or-`Line`, and on any wait failure (deadline, cancellation,
closed session, connection loss) closes the session and returns the error
with whatever `Evidence` was populated so far.
Tests: `command_test.go` (package `ssh_test`) — prompt split across reads,
echoed input stripped, pagination round-trip, per-command deadline exceeded
and session closed after, context cancellation mid-wait and session closed
after, stdout flood past cap (`Truncated` + true `BytesReceived`), stderr
flood while stdout completes normally, connection closed by the peer right
after the command line is read (evidence still reports bytes sent), secret
redaction never reaching `Evidence.Sent`.
`scan_test.go` (package `ssh`) — table tests for `scanPrompt`, plus
`FuzzScanPrompt` seeded from the table cases, asserting no panic and every
returned index stays within `len(buf)`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh/command.go src/protocol/ssh/scan.go src/protocol/ssh/command_test.go src/protocol/ssh/scan_test.go`

### U4. Package README and protocol table
Files: `src/protocol/ssh/README.md`, `src/protocol/README.md`
After: U3
Change: `README.md` documents the package's contract (dial/host-key rule,
one shell per session, prompt-driven command boundaries and privilege
transitions, pagination, bounded buffers, evidence, close-on-cancel) with a
worked example resembling the FastIron adapter's future usage (an `enable`
step and a paginated `show` command) without naming FastIron or Ruckus.
`src/protocol/README.md`'s table gains an `ssh` row next to `netconf`.
Tests: none (docs only); verified by prose review against `docs/doc-style.md`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh/README.md src/protocol/README.md`

## Verification

`go build ./... && go vet ./... && go test -race ./src/protocol/ssh/...`,
plus `go test -race -fuzz=FuzzScanPrompt -fuzztime=20s ./src/protocol/ssh/`
run once manually (the merge gate runs the seed corpus only). No lab device
is touched; every server in the test suite is an in-process
`golang.org/x/crypto/ssh` server on loopback.

## Definition of done

- Verifier green for every path in every unit above.
- `src/protocol/ssh/README.md` exists and `src/protocol/README.md`'s table
  lists `ssh`.
- This plan's `status` is set to `implemented` (or
  `partially-implemented` with a note) with an outcome note under the title.
- No unit label (U1-U4) appears in code, comments, or commit messages.
- No test successfully dials with `InsecureIgnoreHostKey: true`. (One test,
  `TestDialOptionsRequireExplicitHostKeyVerification`, constructs it paired
  with `HostKeySHA256` to prove the mutual-exclusion refusal itself; that
  dial is never allowed to succeed.)

## Open questions

- Whether "fixed and known-host keys" in the task wants an actual
  `known_hosts`-file-shaped input in addition to `HostKeySHA256` pinning.
  Unconfirmed: I read it as two independent pinned-key test scenarios (see
  Decisions) and proceeded without one, since neither the task's API
  sketch nor decision 10 names a `known_hosts` format and adding one
  un-asked would be scope the task didn't request.
- Default buffer sizes (`StdoutBufferBytes`, `StderrBufferBytes`,
  `MaxOutput`) are implementation judgment calls with no stated requirement;
  I will pick defaults generous enough for a device CLI page (tens of KiB)
  and document them in the README rather than treat them as a design
  decision worth a person's sign-off.
