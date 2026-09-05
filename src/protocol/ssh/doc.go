// Package ssh is FlowSeer's SSH interactive-shell client: one shell
// per session, driven by caller-supplied prompt and pagination
// patterns rather than any vocabulary of its own.
//
// This file is the authoritative statement of the package contract;
// if a README ever disagrees, this file wins.
//
// # Scope
//
// The package speaks SSH transport and an interactive shell over it.
// It knows no device, vendor, or firmware: a caller expresses "the
// device just switched privilege level" or "this is a pagination
// prompt" entirely through the [Prompt] and pagination fields it
// passes to [Session.Run], never through a name this package
// recognizes. Telnet is out of scope; a caller that needs it uses a
// different transport.
//
// # Session lifecycle
//
// [Dial] opens the TCP connection, completes the SSH handshake, opens
// the one shell channel with a PTY, and starts it, returning a ready
// [Session]. Host-key verification has no implicit default:
// [Options.HostKeySHA256] or [Options.InsecureIgnoreHostKey] must be
// set, never both. Two goroutines drain the shell's stdout and stderr
// continuously into bounded, drop-oldest buffers so a remote that
// floods either stream cannot deadlock the session or the other
// stream. [Session.Close] is idempotent and stops both goroutines.
//
// # Commands
//
// [Session.Run] sends one [Command] and blocks until the tail of the
// accumulated stdout matches one of the command's [Prompt] patterns,
// or a pagination marker fires the configured continuation keystroke
// and reading resumes. A per-command deadline
// ([Command.Deadline], else the session default) and the caller's
// context both bound the wait; either one firing, or the peer closing
// the connection mid-wait, closes the session — the local read cursor
// can no longer be trusted to align with the remote shell's state
// once a wait is abandoned, so the session is fenced rather than left
// attached. A canceled or timed-out wait surfaces the unwrapped
// context error, not a package error code.
//
// [Command.Redacted], when set, replaces [Command.Line] in the
// returned [Evidence]; the package never scans a command for secret
// shapes on its own. [Evidence] never contains device output beyond
// byte counts and timing, so it is safe to log or attach to a durable
// record.
package ssh
