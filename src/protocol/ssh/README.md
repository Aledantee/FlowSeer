# SSH

`ssh` is an interactive-shell client: one caller opens one shell, runs a
sequence of commands against it, and gets back the shell's own output plus a
redacted audit record. It has no vendor or protocol vocabulary of its own —
every prompt, pagination marker, and privilege transition is a pattern the
caller supplies per command.

Passwords and private keys enter the transport as `secret.Value`. A secret
sent as command text is revealed only when assigning `Command.Line`, which
accepts arbitrary shell input rather than credential material specifically.

```go
opts := ssh.Options{
    Username:      "admin",
    Password:      secret.NewString(password),
    HostKeySHA256: pinnedFingerprint,
}
session, err := ssh.Dial(ctx, "device.example:22", opts)
if err != nil {
    return err
}
defer session.Close()

userPrompt := ssh.Prompt{Name: "user", Pattern: regexp.MustCompile(`>\s*$`)}
privPrompt := ssh.Prompt{Name: "privileged", Pattern: regexp.MustCompile(`#\s*$`)}
passwordPrompt := ssh.Prompt{Name: "enable-password", Pattern: regexp.MustCompile(`(?i)password:\s*$`)}

// A privilege-level transition is just two candidate prompts: the
// package never learns that "enable" exists.
res, err := session.Run(ctx, ssh.Command{
    Line:    "enable",
    Prompts: []ssh.Prompt{passwordPrompt, privPrompt},
})
if err != nil {
    return err
}
if res.MatchedPrompt == "enable-password" {
    res, err = session.Run(ctx, ssh.Command{
        // Command.Line is the text sent to the device, so material is
        // revealed at this call and nowhere earlier; Redacted is what the
        // evidence record carries instead.
        Line:     enablePassword.RevealString(),
        Redacted: "[REDACTED]",
        Prompts:  []ssh.Prompt{privPrompt},
    })
}

// Pagination is a marker pattern and a keystroke, supplied per command.
res, err = session.Run(ctx, ssh.Command{
    Line:          "show running-config",
    Prompts:       []ssh.Prompt{privPrompt},
    MorePattern:   regexp.MustCompile(`--More--`),
    MoreKeystroke: []byte(" "),
})
```

## Host-key verification has no default

[`Options`](options.go) requires explicit host-key verification; see
[Host-key verification has no default](GUARANTEES.md#host-key-verification-has-no-default).
`HostKeySHA256` accepts the peer's base64 SHA-256 fingerprint with or without
the `SHA256:` prefix, or callers can pass the explicit `InsecureIgnoreHostKey`
opt-in. Setting neither or both is refused before any network I/O. This mirrors
`src/protocol/netconf`'s `Options` for the same reason: a management protocol
that can silently accept an unverified peer is never a safe default.

## One shell, sequential commands

`Dial` opens the TCP connection, completes the SSH handshake, and opens the
session's one shell channel with a PTY, all under the caller's context and
`Options.DialTimeout`. Two goroutines drain the shell's stdout and stderr
continuously into bounded, drop-oldest buffers (`Options.StdoutBufferBytes`,
`Options.StderrBufferBytes`; 64 KiB and 16 KiB by default) so a remote that
floods either stream cannot deadlock the session or the other stream; see
[Stderr saturation does not block stdout completion](GUARANTEES.md#stderr-saturation-does-not-block-stdout-completion).
`Session.Run` calls must not overlap — the shell has one input stream, so a
caller serializes its own commands.

## Commands are bounded by prompts, not by time alone

`Session.Run` writes `Command.Line`, then blocks until one of
`Command.Prompts` matches the accumulated stdout — the earliest match anywhere
in scanned output wins, ties resolving by slice order; see
[A command ends at the earliest prompt match](GUARANTEES.md#a-command-ends-at-the-earliest-prompt-match).
This is why prompt patterns must be anchored against matching inside the
device's own output.
`Result.MatchedPrompt` carries the matched prompt's `Name` back to the caller,
which is how an adapter tells a privilege-level transition happened without this
package knowing what a privilege level is. A `Command.MorePattern` match writes
`Command.MoreKeystroke` and keeps reading; see
[Pagination markers are answered and excluded from output](GUARANTEES.md#pagination-markers-are-answered-and-excluded-from-output).
A leading echo of `Command.Line` in the shell's response is stripped automatically.

Every `Run` call has a deadline — `Command.Deadline`, else
`Options.CommandDeadline` (60s by default) — independent of `Dial`'s timeout,
and honors the caller's `ctx`. Any failure to complete cleanly — deadline
expiration, context cancellation, or the peer closing the shell channel —
closes the `Session` so a caller never resumes against an untrusted read
cursor; see
[A failed wait closes the session](GUARANTEES.md#a-failed-wait-closes-the-session).
A canceled or timed-out wait surfaces unwrapped `context.Canceled` or
`context.DeadlineExceeded`, not a package error code.

`Command.MaxOutput` (else the package's 1 MiB default) bounds
`Result.Output`; see
[Output is capped with the true byte count kept](GUARANTEES.md#output-is-capped-with-the-true-byte-count-kept).
When output exceeds the limit, truncation keeps the most recent bytes, since a
prompt or pagination marker is expected at the tail of the stream.

## Evidence and redaction

Every `Run` call returns an `Evidence` record: `Sent` (`Command.Line`, or
`Command.Redacted` when set, before the trailing newline `Run` appends),
`BytesReceived`/`StderrBytesReceived`, `Started`, and `Elapsed`. When set,
`Command.Redacted` replaces `Command.Line` in `Sent`; see
[Command redaction replaces sent line in evidence](GUARANTEES.md#command-redaction-replaces-sent-line-in-evidence).
The package does no pattern-based secret scrubbing of its own; a caller that
sends a credential is responsible for setting `Redacted`, and `Sent` carries
`Command.Line`, credential included, until it does. `Evidence` contains no
device output beyond byte counts and timing, so with `Redacted` set it is safe
to log or attach to a durable audit record.

## Scope

Telnet, a `known_hosts` file format, host-key rotation, connection pooling,
multiple shells per session, and terminal (ANSI/VT100) escape interpretation
are all out of scope; `Result.Output` is raw bytes and a caller that cares
about escape sequences strips them itself.
