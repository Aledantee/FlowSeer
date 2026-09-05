# errs

FlowSeer's error package — a small, owned type built for a distributed system.
The core payload is message, code, attributes, and causes, plus a diagnostic
stack that is never payload. Four further fields ride along because a
mechanism consumes them: the user message and hint a boundary sends to a
client, the exit code a process ends with, and the retry disposition a poll
loop or broker reads. Errors are stdlib-native: they implement `Unwrap` and
compose with `errors.Is`, `errors.As`, and `errors.Join`, so there is no
parallel matching API.

```go
return errs.From(err).
    Code(ErrCodePrivDecrypt).
    UserMsg("could not read the device").
    Hint("check the device's USM credentials").
    Attr("engine_id", id).
    PubAttr("proto", "usm-aes").
    Msg("privacy decryption failed")
```

The package's own `doc.go` is the authoritative reference — including the code
discipline and the attribute-safety rule. This README maps the surface.

## Public surface

**Sentinels** (`errs.go`) — `Msg(msg)`, `Msgf(format, args...)`. Package-level
values callers branch on with `errors.Is`. They capture no stack.

**Wrapping** (`wrap.go`) — `Wrap(err, msg)`, `Wrapf(err, format, args...)`.
The wrapped error comes first, and both return nil for a nil error.

**Builder** (`builder.go`) — `New()` and `From(err)` return a `Builder`;
`.Attr(key, val)`, `.PubAttr(key, val)`, `.Cause(errs...)`, `.Code(c)`,
`.UserMsg(msg)`, `.Hint(hint)`, `.ExitCode(n)`, `.Retryable()`, and `.Fatal()`
chain; `.Msg(msg)` and `.Msgf(format, args...)` terminate and return the
error. A partially built `Builder` is reusable as the base of several errors.
Attribute values and causes are retained by reference, so concurrent use of
the builder or error requires them to be safe for concurrent reads.

**Codes** (`code.go`) — `NewCode("<package>/<name>")` registers a `Code` and
panics on a duplicate or malformed name; `Codes()` enumerates the registry;
`CodeOf(err)` finds the outermost code in a chain. Equal codes match under
`errors.Is` without shared identity, which is how a decoded peer error matches
a local sentinel.

**Extraction** (`attr.go`) — `Attributes(err)` merges the chain's attributes
outermost-first, joined branches left to right, first value winning;
`SafeAttributes(err)` returns only the `PubAttr`-marked subset a boundary may
expose to an untrusted client — a strict subset, so the two never report
different values for one key.

**Client-facing text** (`usermsg.go`) — `UserMessage(err)` and `Hint(err)`
return the outermost values set with `.UserMsg` and `.Hint`, or `""`. They are
client-safe by definition and are what a boundary sends in place of the
internal message, which names hosts, engine IDs, and call paths.

**Exit codes** (`exitcode.go`) — `ExitCode(err)` is 0 for nil, the outermost
value set with `.ExitCode`, or 1 for any other error, so `main` can exit on an
error without checking whether one was set. Process-local; never on the wire.

**Retries** (`retry.go`) — `Retryable(err)` reports the outermost disposition
expressed with `.Retryable()` or `.Fatal()`, so a wrapper that spent its retry
budget overrules a transient cause. A chain that expressed none is not
retryable.

**Logging** (`slog.go`) — `Error` implements `slog.LogValuer`: one group with
the rendered message, the chain's code, the merged attributes, and symbolized
stack frames, plus whichever of the user message, hint, exit code, and retry
disposition the chain actually sets. It shares the extractor's traversal, so
logs and `Attributes` never disagree.

**Stacks** (`stack.go`) — program counters captured once at origin and
symbolized only when logged.

## Conventions

- Never attach raw secret material. Attach a length and a protocol name.
- A field earns a place in the payload only if a mechanism consumes it — the
  process, a retry loop, `errors.Is`, or the boundary filter. Facts a reader
  merely finds interesting are attributes.
- The internal message is for the log; `.UserMsg` is for the caller. Never
  write the internal one so it can double as both.
- Codes are append-only: never rename one, never reuse it for a new meaning.
- Declare codes with a string literal — the repo-wide uniqueness test in
  `code_test.go` reads the source, and cannot check a computed name.
