# secret

The carrier for credential material — passwords, passphrases, private keys,
community strings. A `Value` redacts itself wherever it is the operand or sits
in an exported field, so a struct that holds one survives being logged,
formatted into an error, or dumped by a failing test.

```go
opts := netconf.Options{Username: "admin", Password: secret.NewString(pw)}
slog.Info("dialing", "opts", opts) // opts={Username:admin Password:[REDACTED] ...}
```

`doc.go` is the authoritative reference. This README maps the surface.

## Public surface

**Construction** — `New(b []byte)` takes ownership of the slice;
`NewString(s)` copies. The zero `Value` is unset and usable.

**Reading** — `Reveal() []byte` returns the value's own slice, which the caller
must not write to; `RevealString()` returns a copy. These are the only ways out
and are meant to be greppable: `grep -rn '\.Reveal' src` lists the audit surface
of every package that handles credentials.

**Asking** — `Len()` and `Empty()` are safe to log. A length is what an error
should carry in place of a credential.

**Comparing** — `Equal(Value)` and `EqualString(string)`, both taking time
independent of where two equal-length inputs first differ. `==` does not compile
on a `Value`, which is what keeps a comparison from silently becoming a timing
oracle.

**Wiping** — `Zero()` overwrites the material in place, so it reaches every copy
of the `Value`. It cannot reach a slice already handed out by `Reveal` or the
string `RevealString` returned.

**Rendering** — `String`, `GoString`, `Format`, `MarshalJSON`, `MarshalText`,
and `LogValue` all produce `[REDACTED]` for a set value and the empty string for
an unset one. `Format` covers every verb, including unknown ones, so no verb
reaches the reflect walker that would otherwise print the bytes.

**Decoding** — `UnmarshalJSON` and `UnmarshalText` accept material and reject
input equal to `[REDACTED]` with `ErrRedactedInput`; JSON null decodes to an
unset value. Marshaling is asymmetric on purpose: a round trip through a
redacted dump would otherwise hand back a value holding the placeholder and no
way to notice. It also means marshalling is for diagnostics only — writing a
config back out emits the placeholder silently, so a document that has to carry
the material takes `RevealString` at a call site that says so.

## Why a type rather than a naming rule

A field named `Password` can be found by a reviewer; the value copied out of it
into a map, a wrapped error, or someone else's struct cannot. The classification
has to travel with the value. `docs/conventions/observability.md` makes the same
argument for log attributes: a key-name denylist cannot recognize a secret
stored under an innocent key.

`src/common/internal/secretguard` enforces the declaration side — an exported
field whose name reads as secret material may not be a `string` or `[]byte`.

## Limits

Two fmt paths bypass the redaction, both properties of fmt rather than of the
type. A `Value` in an **unexported** field is printed by the reflect walker as
its raw bytes, because fmt consults a value's own methods only when the field
can be interfaced — `%+v` on such a struct yields `{pass:{b:[104 117 ...]}}`.
And `%w` on a non-error reprints the operand through the same walker, which
`go vet` rejects at compile time in the ordinary case. A package that keeps
material in an unexported field must not print that struct.

Zeroing is a best effort against a later disclosure of process memory, not a
defense against an attacker already reading it: the runtime may have copied the
material during a stack or heap move before `Zero` ran. Nothing here encrypts,
stores, or leases a credential — this is the in-process carrier the storage
layers hand around.
