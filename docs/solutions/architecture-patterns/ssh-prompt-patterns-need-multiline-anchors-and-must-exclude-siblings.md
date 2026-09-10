---
title: A src/protocol/ssh Prompt Pattern Needs a Multiline Anchor and Must Exclude Every Sibling Prompt's Shape
date: 2026-09-05
category: architecture-patterns
module: src/protocol/ssh
problem_type: bug
component: protocol_client
severity: high
symptoms:
  - every Session.Run call against a real device times out and closes the
    session, even though the device is answering correctly
  - a Run call that should detect a privilege or mode transition instead
    matches the wrong Prompt, so MatchedPrompt names the caller's own
    starting level
root_cause: >
  a caller-supplied Prompt.Pattern anchored with bare ^ or $ (no (?m) flag)
  matches only the start or end of the whole accumulated buffer, not the
  start or end of the line the prompt is actually on, so it never matches
  once any command output precedes the prompt in the buffer; separately, a
  loosely-written prompt pattern (e.g. "ends in #") can also match a sibling
  prompt shape (e.g. a configuration-mode prompt, which also ends in #), and
  scanPrompt's earliest-match-wins rule resolves the resulting tie in favor
  of whichever Prompt is listed first
resolution_type: design_fix
applies_when:
  - writing a new Prompt.Pattern for src/protocol/ssh.Command, for any
    device family or shell
  - two or more prompts a session can be in look alike except for one
    substring (a privilege level, a configuration mode, a sub-mode)
  - reviewing a plan or PR that specifies Prompt patterns before any test
    has driven them against a real or scripted multi-line transcript
related_components: [ssh_client, device_access, protocol_client]
tags: [expect-pattern, ssh, prompt-scanning, regexp, review-finding]
---

# A src/protocol/ssh Prompt Pattern Needs a Multiline Anchor and Must Exclude Every Sibling Prompt's Shape

## The situation

`src/protocol/ssh.Command.Prompts` is a list of caller-supplied `Prompt`
values, each a `Name` and a `*regexp.Regexp`. `scanPrompt`
(`src/protocol/ssh/scan.go:49`) runs every pattern's `FindIndex` against the
whole buffer of output accumulated since the command was sent — not the last
line, the whole buffer — and picks whichever pattern matches earliest in it;
a tie (two patterns matching the identical span) resolves to whichever
`Prompt` came first in `Command.Prompts` (`scan.go:65`, the strict `<`
comparison never lets a later entry override an earlier one at the same
position).

Planning the FastIron capability's four session prompts (unprivileged,
privileged, configuration, and per-interface configuration —
`src/modules/localnet/access/internal/capability/fastiron/commands.go`), the
first draft used:

```go
privilegedPattern = regexp.MustCompile(`^\S+#\s*$`)
configPattern     = regexp.MustCompile(`^\S+\(config\)#\s*$`)
```

An independent review of the plan caught two separate defects in this shape
before any code existed, by tracing what `scanPrompt` actually does rather
than trusting the pattern's apparent intent.

## The two defects

**`^`/`$` without `(?m)` anchor to the whole buffer, not a line.** Go's
`regexp` syntax states plainly: `^` is "at beginning of text or line (flag
m=true)" and `$` "at end of text ... or line (flag m=true)". Without `(?m)`,
both anchor to the absolute start and end of the *entire accumulated
buffer*. A `show interfaces` reply is many lines of output before the
prompt; `^\S+#\s*$` can only match if the buffer holds nothing but the
prompt's own line, which is true only for the very first prompt after
`Dial` and false for every command that produces output first. Every
existing prompt pattern in the repository already avoids this — the
package's own README example (`src/protocol/ssh/README.md:22-23`) and every
test fixture (`src/protocol/ssh/scan_test.go:10-11`,
`src/protocol/ssh/command_test.go:58`) either omit `^` entirely or use
`(?m)`. A new caller copying a pattern's *shape* rather than its anchoring
convention reintroduces the bug the existing patterns already avoid.

**`\S+` matches parentheses, so a strict-looking pattern still matches a
sibling prompt.** `\S` is "any non-whitespace," which includes `(` and `)`.
A privileged-prompt pattern written as `^\S+#\s*$` (or, corrected for the
first defect, `(?m)^\S+#\s*$`) still matches `device(config)#`, because
`\S+` is happy to consume `device(config)` as the "hostname" run before the
`#`. Both the privileged and the configuration prompt end in `#`; without an
explicit exclusion, the privileged pattern's laxness makes it a superset of
the configuration pattern's, and `scanPrompt`'s tie-break silently resolves
in favor of whichever one is listed first in `Command.Prompts` — a
transition into configuration mode gets reported as still-privileged, with
no error at all.

## The fix

```go
unprivilegedPattern = regexp.MustCompile(`(?m)^\S[^()\r\n]*>\s*$`)
privilegedPattern   = regexp.MustCompile(`(?m)^\S[^()\r\n]*#\s*$`)
configPattern       = regexp.MustCompile(`(?m)^\S[^()\r\n]*\(config\)#\s*$`)
configIfPattern     = regexp.MustCompile(`(?m)^\S[^()\r\n]*\(config-if-[^)]*\)#\s*$`)
```

`(?m)` lets `^`/`$` anchor to the prompt's own line regardless of what
precedes it in the buffer. `[^()\r\n]*` (after a required non-whitespace
first character, so an indented output line can never match either) forbids
a parenthesis anywhere in the "hostname" run, so the privileged pattern can
no longer match a configuration-mode prompt's text — the two patterns are
now mutually exclusive by construction rather than by list order.

The non-whitespace-first-character requirement earns a second win for free:
FastIron's `show interfaces` output indents every line but its unindented
header two or more spaces (confirmed against the vendor command reference),
so a prompt-shaped substring inside a description or other field value
(e.g. a description reading `see SSH@device# for details`, printed as
`  Port name is see SSH@device# for details`) can never be mistaken for the
real prompt on its own unindented line — closing the same hazard the
"expect-style prompt scanner" solution's `root_cause` names from the
package's internal side, from the caller's side instead.

## Why this matters

Both defects are invisible in a design review that reads the pattern's
apparent intent ("ends in `>`," "ends in `(config)#`") rather than tracing
`scanPrompt`'s actual matching rule against a real multi-line transcript. A
plan or PR that specifies `Prompt.Pattern` values is not implementation-ready
until at least one of its patterns has been checked against a fixture that
has *other output before the prompt* and *every sibling prompt shape it must
not also match*.

## When to apply

- Any time `src/protocol/ssh.Prompt.Pattern` is written for a new device
  family or shell: add `(?m)` unless the pattern is proven to run only
  against a single-line buffer, and check every other prompt the same
  session can reach for a shared suffix (`#`, `>`, a common trailing
  punctuation) that an unqualified pattern would also match.
- Reviewing such a pattern: trace `scanPrompt` (`src/protocol/ssh/scan.go`)
  against a buffer containing realistic multi-line output ahead of the
  prompt, and against every sibling prompt's exact text, not just the
  pattern's apparent English meaning.

## Related

- [An Expect-Style Prompt Scanner Must Reset Its Window Before Each Command, Not Just Strip Matches After](expect-style-prompt-scanner-must-reset-its-window-per-command.md)
  covers the same package's internal per-command reset; this document covers
  a caller's own `Prompt.Pattern` construction.
- `src/protocol/ssh/README.md` — the package's own prompt examples, already
  written without the first defect.
