# errs

FlowSeer's error package — a small, owned type built for a distributed system.
The semantic payload is four fields (message, code, attributes, causes) plus a
diagnostic stack that is never payload. Errors are stdlib-native: they
implement `Unwrap` and compose with `errors.Is`, `errors.As`, and
`errors.Join`, so there is no parallel matching API.

```go
return errs.From(err).
    Code(ErrCodePrivDecrypt).
    Attr("engine_id", id).
    PubAttr("proto", "usm-aes").
    Msg("privacy decryption failed")
```

The package's own `doc.go` is the authoritative reference — including the code
discipline, the attribute-safety rule, and the wire design this package is
built for but does not yet implement. This README maps the surface.

## Public surface

**Sentinels** (`errs.go`) — `Msg(msg)`, `Msgf(format, args...)`. Package-level
values callers branch on with `errors.Is`. They capture no stack.

**Wrapping** (`wrap.go`) — `Wrap(err, msg)`, `Wrapf(err, format, args...)`.
The wrapped error comes first, and both return nil for a nil error.

**Builder** (`builder.go`) — `New()` and `From(err)` return a `Builder`;
`.Attr(key, val)`, `.PubAttr(key, val)`, `.Cause(errs...)`, and `.Code(c)`
chain; `.Msg(msg)` and `.Msgf(format, args...)` terminate and return the
error. A partially built `Builder` is reusable as the base of several errors.

**Codes** (`code.go`) — `NewCode("<package>/<name>")` registers a `Code` and
panics on a duplicate or malformed name; `Codes()` enumerates the registry;
`CodeOf(err)` finds the outermost code in a chain. Equal codes match under
`errors.Is` without shared identity, which is how a decoded peer error matches
a local sentinel.

**Extraction** (`attr.go`) — `Attributes(err)` merges the chain's attributes
outermost-first, joined branches left to right, first value winning;
`SafeAttributes(err)` returns only the `PubAttr`-marked subset a boundary may
expose to an untrusted client.

**Logging** (`slog.go`) — `Error` implements `slog.LogValuer`: one group with
the rendered message, the chain's code, the merged attributes, and symbolized
stack frames. It shares the extractor's traversal, so logs and `Attributes`
never disagree.

**Stacks** (`stack.go`) — program counters captured once at origin and
symbolized only when logged.

## Conventions

- Never attach raw secret material. Attach a length and a protocol name.
- Codes are append-only: never rename one, never reuse it for a new meaning.
- Declare codes with a string literal — the repo-wide uniqueness test in
  `code_test.go` reads the source, and cannot check a computed name.
