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

[`Options`](options.go) requires exactly one of `HostKeySHA256` (a pinned
SHA-256 fingerprint, with or without the `SHA256:` prefix) or the explicit
`InsecureIgnoreHostKey` opt-in. Neither set, or both set, is refused before
any network I/O. This mirrors `src/protocol/netconf`'s `Options` for the same
reason: a management protocol that can silently accept an unverified peer is
never a safe default.

## One shell, sequential commands

`Dial` opens the TCP connection, completes the SSH handshake, and opens the
session's one shell channel with a PTY, all under the caller's context and
`Options.DialTimeout`. Two goroutines drain the shell's stdout and stderr
continuously into bounded, drop-oldest buffers (`Options.StdoutBufferBytes`,
`Options.StderrBufferBytes`; 64 KiB and 16 KiB by default) so a remote that
floods either stream cannot deadlock the session or the other stream.
`Session.Run` calls must not overlap — the shell has one input stream, so a
caller serializes its own commands.

## Commands are bounded by prompts, not by time alone

`Session.Run` writes `Command.Line`, then blocks until one of
`Command.Prompts` matches the accumulated stdout — the earliest match
anywhere in it, ties going to the order of the slice, which is why a prompt
pattern has to be anchored against matching inside the device's own output.
`Result.MatchedPrompt`
carries the matched prompt's `Name` back to the caller, which is how an
adapter tells a privilege-level transition happened without this package
knowing what a privilege level is. A `Command.MorePattern` match instead
writes `Command.MoreKeystroke` and keeps reading; the marker text never
reaches `Result.Output`. A leading echo of `Command.Line` in the shell's
response is stripped automatically.

Every `Run` call has a deadline — `Command.Deadline`, else
`Options.CommandDeadline` (60s by default) — independent of `Dial`'s timeout,
and honors the caller's `ctx`. Any failure to complete cleanly — the deadline,
context cancellation, or the peer closing the connection mid-wait — closes
the `Session`: the local read cursor can no longer be trusted to align with
the remote shell once a wait is abandoned, so a subsequent `Run` returns
`ErrSessionClosed` rather than resuming against unknown state. A canceled or
timed-out wait surfaces the unwrapped `context.Canceled` /
`context.DeadlineExceeded`, not a package error code.

`Command.MaxOutput` (else the package's 1 MiB default) bounds
`Result.Output`; a command that produces more sets `Result.Truncated` and
keeps the most recent bytes, since a prompt or pagination marker is expected
at the tail of the stream. `Result.Evidence.BytesReceived` always reports the
true byte count observed, independent of truncation.

## Evidence never carries a credential

Every `Run` call returns an `Evidence` record: `Sent` (`Command.Line`, or
`Command.Redacted` when set, before the trailing newline `Run` appends),
`BytesReceived`/`StderrBytesReceived`, `Started`, and `Elapsed`.
`Command.Redacted`, when set, replaces `Command.Line` in `Sent`;
the package does no pattern-based secret scrubbing of its own; a caller that
sends a credential is responsible for setting `Redacted`. `Evidence` never
contains device output beyond byte counts and timing, so it is safe to log or
attach to a durable audit record.

## Scope

Telnet, a `known_hosts` file format, host-key rotation, connection pooling,
multiple shells per session, and terminal (ANSI/VT100) escape interpretation
are all out of scope; `Result.Output` is raw bytes and a caller that cares
about escape sequences strips them itself.
