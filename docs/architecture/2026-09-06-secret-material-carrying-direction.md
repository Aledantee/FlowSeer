---
title: Secret Material Carrying - Direction
type: direction
date: 2026-09-06
topic: secret-material-carrying
status: proposed-direction
---

# Secret Material Carrying - Direction

Passwords, passphrases, and private keys move through FlowSeer in Go values:
from a configuration source into a protocol library's options struct, and from
there into a transport. Today every such value is a plain `string` or
`[]byte` — `Password` in `src/protocol/netconf/options.go`,
`src/protocol/gnmi/options.go`, and `src/protocol/restconf/options.go`,
`AuthPassphrase` and `PrivPassphrase` in `src/protocol/snmp/usm.go`. A plain
field is rendered by anything that formats the struct it sits in, so a single
`%+v` in a log line, an error message, or a test failure prints the password.

SNMP v1/v2c community strings are the same class of value —
`src/protocol/snmp/backend.go:34` calls them cleartext shared secrets — and
`SessionConfig.Community` is a plain `string` too.

The rule against that exists only as prose. `src/common/errs/doc.go` says raw
secret material never becomes an attribute and never reaches a message, and
`docs/code-style.md` repeats it for error text. Nothing enforces either, and
`src/protocol/snmp/usm.go` shows what compliance costs when the type does not
help: roughly eighty lines of hand-written `String`, `GoString`, `Format`, and
per-field redaction that exist only to keep `USMConfig` printable. Every other
options struct skipped that cost and leaks accordingly.

One package solved this already. `src/edge/netpen/findings/findings.go:181`
defines `Secret`: unexported fields, a copying constructor, a JSON encoding
that exposes only protocol and length, and redaction tests in the attack
packages. It lost as the repository-wide carrier for two reasons. It is domain
shaped — a captured credential with a protocol label, which a configured
password is not — and it redacts only JSON. Unexported fields do not stop
`fmt`: `%+v` on a `findings.Secret` holding `public` prints
`{protocol:snmp value:[112 117 98 108 105 99]}`, verified on Go 1.27.1. A
carrier has to cover the `fmt` path, and `findings.Secret` covers one encoder.
It also lives in netpen's own Go module.

## Decision

Secret material is carried in a dedicated type, `secret.Value` in
`src/common/secret`, which redacts itself under every rendering path Go
offers — `fmt` verbs, `encoding/json`, `encoding.TextMarshaler`, and
`slog.LogValuer`. In hand-written Go under `src/`, no exported field holds raw
secret material in a `string` or `[]byte`.

The boundary is drawn there because the two exclusions cannot follow the rule.
`generated/` is `buf generate` and codegen output that hooks forbid editing by
hand, and `src/edge/netpen` is a separate Go module that cannot import
`src/common` without a module requirement; it keeps `findings.Secret` until
someone makes that change.

The classification travels with the value rather than with the field name.
`docs/conventions/observability.md` makes the case in the logging context: a
key-name denylist cannot detect a secret stored under an innocent key. The
same argument applies to struct fields, and a type is the only carrier that
survives being copied into a map, a wrapped error, or a struct someone else
prints.

This holds for protocol libraries, modules, services, and edge applications
alike. It is a repository rule, not one library's convention, because the value
crosses all four tiers: a credential provider in a service reads it, a module
passes it through, and a protocol library consumes it.

## What it does not decide

Storage, rotation, leasing, and the device service's credential provider are
separate concerns and stay in `docs/plans/2026-09-05-1709-feat-verified-local-device-access-plan.md`.
This record decides only the in-process carrier they all hand around.

Derived key material held in unexported fields — `authKey` in
`src/protocol/snmp/usm_security.go` is the example — stays raw. It never
crosses a package boundary, it sits on a per-packet path, and the redaction
problem is about what callers can print.

## Consequences

- Every protocol library takes a breaking API change: `Password string`
  becomes `Password secret.Value`. AGENTS.md permits this until the first
  stable release, and there is no external consumer.
- `USMConfig`'s bespoke redaction is deleted. Its rendered output changes
  shape, because `fmt` renders the struct rather than a hand-written string.
- A `secret.Value` is not comparable with `==`, since it holds a slice. The
  existing comparisons are emptiness checks (`c.AuthPassphrase == ""` in
  `Validate`, `opts.Password != ""` before SSH auth) and become `Empty`; the
  community check in `src/protocol/snmp/translate.go:21` is a real comparison
  and becomes constant time.
- `src/edge/netpen` keeps a carrier that leaks under `fmt` until the module
  question is answered. That is a known remaining case, not an exemption on the
  merits.
- The rule is enforceable: an AST scan can reject an exported field whose name
  reads as secret material and whose type is `string` or `[]byte`. That scan is
  a declaration-site lint, not a secret detector; it catches the reintroduction
  of the shape this record removes, and nothing else.
