---
title: An Expect-Style Prompt Scanner Must Reset Its Window Before Each Command, Not Just Strip Matches After
date: 2026-09-05
category: architecture-patterns
module: src/protocol/ssh
problem_type: bug
component: protocol_client
severity: high
symptoms:
  - a command's result is empty or truncated on the first call after a login
    banner, or after any command whose own text ends in prompt-shaped text
  - every later command's output looks shifted by one command, with no error
    and a plausible-looking matched-prompt value
  - a bounded output buffer reports "not truncated" on a command that in fact
    lost the bulk of its output to the buffer's own drop-oldest eviction
root_cause: >
  the scanner matched a caller-supplied prompt pattern against the whole
  unconsumed read buffer, which could still contain bytes from before the
  current command (a banner, or leftover output between commands) or the
  shell's own echo of the command just sent, either of which can contain the
  same prompt-shaped text the caller is waiting for
resolution_type: design_fix
applies_when:
  - implementing or reviewing an expect-style command/response loop over a
    persistent shell (SSH, serial, Telnet) that matches a caller-supplied
    prompt regex against accumulated output to find a command boundary
  - the shell may print unsolicited text before the first command (a login
    banner) or echo the sent command back as part of its response
  - a bounded ring or ring-like buffer tracks "was anything ever dropped" as
    a single flag rather than one scoped to the current read
related_components: [ssh_client, device_access, protocol_client]
tags: [expect-pattern, ssh, prompt-scanning, ring-buffer, review-finding]
---

# An Expect-Style Prompt Scanner Must Reset Its Window Before Each Command, Not Just Strip Matches After

## The situation

`src/protocol/ssh.Session.Run` (`src/protocol/ssh/command.go`) sends one line
to a persistent interactive shell and blocks until a caller-supplied `Prompt`
regex matches the tail of the accumulated stdout. Two independent report
sources routinely feed such a scanner content that is not the command's own
output:

1. Anything the shell printed before this `Run` call — a login banner ahead
   of the first command, or output the previous command's own matching left
   sitting in the buffer.
2. The shell's echo of the command line it was just sent, which the caller
   supplied and which can legally contain the same character the prompt
   pattern anchors on (e.g. `show run | include #` echoed back, scanned
   against a prompt anchored on a trailing `#`).

A scanner that runs the prompt regex against the *entire* unconsumed buffer,
then only strips the echo from the assembled output afterward, will match
either of these before the real response ever arrives. The command returns
immediately with the stale or echoed text mistaken for its own output, no
error, and a matched-prompt value that looks correct — the real response
lands in the ring and terminates the *next* command instead, silently
shifting every later result by one.

## What is true and why

**Reset the read window before writing, not just after matching.** Before
`Session.Run` writes `cmd.Line`, it calls `s.stdout.reset()` and
`s.stderr.reset()` (`src/protocol/ssh/command.go`, immediately before the
`s.stdin.Write` call), which discard whatever the rings currently retain. Any
banner or stray inter-command output is gone before the scan for this
command's own boundary ever runs.

**Exclude the echo from the region handed to the scanner, not from the
assembled output afterward.** `echoSkipLen` (`src/protocol/ssh/command.go`)
computes how many leading bytes of the current buffer are a verbatim echo of
the sent line (0 until the buffer holds the full echo, so a shell that never
echoes is not made to wait for one). The match closure passed to
`ring.waitFor` scans only `buf[skip:]`, so a prompt-shaped substring inside
the echo can never match:

```go
matched, err := s.stdout.waitFor(runCtx, func(buf []byte) (int, bool) {
    skip = echoSkipLen(buf, echo)
    r, ok := scanPrompt(buf[skip:], cmd.Prompts, cmd.MorePattern)
    ...
})
...
output.Write(matched[skip:res.outputEnd])
```

Stripping the echo from `output` *after* the wait loop — the initial,
incorrect version of this code — only hides the echo from the caller; it does
nothing to stop the scan itself from matching inside it first.

**A drop-oldest ring's truncation flag needs the same per-window reset.**
`ring.reset` (`src/protocol/ssh/buffer.go`) clears `truncated` alongside the
buffer on every `Run` call. Without that, one command overflowing the ring
sets a flag that then reports "truncated" on every subsequent command
regardless of whether that command's own window ever came close to the cap —
the same class of bug as scanning stale content, just for the truncation
signal instead of the match signal.

## How to apply

Any expect-style loop over a persistent shell needs, in this order, before
each command:

1. Discard/reset whatever the read side currently retains (buffer content
   and any "this window's" flags), so nothing from before the command can be
   mistaken for the command's own response.
2. Detect and exclude the echo from the *scanned* region, not just from the
   assembled result — computed fresh on every partial read, since the echo
   may itself arrive split across multiple reads.
3. Only then run the prompt/pagination scan against what remains.

## Evidence

- `src/protocol/ssh/command.go`: `s.stdout.reset()` / `s.stderr.reset()`
  before the stdin write; `echoSkipLen` and its use inside the `waitFor`
  match closure.
- `src/protocol/ssh/buffer.go`: `ring.reset` clearing both `buf` and
  `truncated`.
- Tests proving each failure mode is fixed:
  `TestRunDiscardsPreCommandBytes`,
  `TestRunEchoContainingPromptCharacterDoesNotSelfTerminate`,
  `TestRunStdoutBufferSaturationReportsTruncation`,
  `TestRunEvidenceBytesReceivedSetOnDeadlineError`
  (all in `src/protocol/ssh/command_test.go`).

## What this does not cover

A prompt pattern that is not anchored against the tail of a line (no `$` or
equivalent) can still false-match inside genuine device output that happens
to contain the same text; resetting the window and excluding the echo
narrows the false-match surface, it does not replace an anchored pattern.
This also does not cover a shell whose echo is not a byte-for-byte copy of
the sent line (e.g. one that reformats or delays it) — `echoSkipLen` requires
an exact prefix match and falls back to scanning from position 0 otherwise,
which is the safe (if less precise) default for that case.
