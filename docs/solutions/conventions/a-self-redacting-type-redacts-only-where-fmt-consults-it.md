---
title: A Self-Redacting Type Redacts Only Where fmt Consults Its Methods
date: 2026-09-06
last_verified: 2026-09-06
category: conventions
module: src/common/secret
problem_type: convention
component: service_layer
applies_when:
  - "adding a field that holds a password, passphrase, private key, or community string, or deciding what type it takes"
  - "writing a type whose String, Format, or LogValue exists to hide or shorten what the value carries"
  - "claiming in a doc comment or README that a value cannot leak into a log, an error, or a test failure"
  - "keeping a secret.Value in an unexported field, or printing a struct that has one"
severity: high
related_components: [observability, testing_framework, documentation]
tags: [secrets, redaction, fmt, logging, go-stdlib]
---

# A Self-Redacting Type Redacts Only Where fmt Consults Its Methods

## The situation

`src/common/secret` exists so a credential can sit in a struct that gets
logged, wrapped in an error, or dumped by a failing test without the material
appearing. `Value` implements `Stringer`, `Formatter`, `GoStringer`,
`json.Marshaler`, `encoding.TextMarshaler`, and `slog.LogValuer`, which covers
the paths a caller normally reaches.

The first draft of its documentation said it renders redacted "under every path
Go offers for turning a value into text". That is wrong in two places, and the
first of them is a shape the SNMP library uses.

## What is true, and why

`fmt` walks a struct with reflection and consults a field's own methods only
when it can hand that field out as an `any`:

```go
// go1.27.1 fmt/print.go:758, printValue
if depth > 0 && value.IsValid() && value.CanInterface() {
	arg := value.Interface()
	if p.handleMethods(arg, value, verb) {
		return
	}
}
```

A field read through reflection from an **unexported** field is not
addressable-for-interface, so `CanInterface()` is false, `handleMethods` is
skipped, and the walker descends into the value and prints what it holds.
Measured on Go 1.27.1, darwin/arm64:

```
unexported %+v: {pass:{b:[104 117 110 116 101 114 50]}}
exported   %+v: {Pass:[REDACTED]}
```

The second hole is `%w` on something that is not an error: `handleMethods`
routes it to `badVerb`, which sets `p.erroring` and reprints the operand
through the same walker. `go vet` rejects the literal form
(`fmt.Errorf format %w has arg v of wrong type`), so reaching it takes an
indirect format string — which is why it is the narrower of the two.

## How to apply it

An exported field is the safe shape, and the one the guard enforces:

```go
type Options struct {
	Username string
	Password secret.Value // %+v on Options prints [REDACTED]
}
```

An unexported field is fine for holding the material — it is what
`src/protocol/snmp/usm_security.go:44` and `src/protocol/snmp/reactor.go:181`
do — but the struct around it is then **not safe to print**. Do not add a
`%+v` of such a struct to a log line, an error message, or a test failure, and
do not write a doc comment promising that it is redacted.

The rule this is the boundary of lives in
`docs/architecture/2026-09-06-secret-material-carrying-direction.md`: in
hand-written Go under `src/`, an exported field holding credential material is
a `secret.Value`. `src/common/internal/secretguard` enforces the declaration
side and scans exported fields only, for exactly this reason.

## Evidence

- `src/common/secret/doc.go` states both holes; `src/common/secret/README.md`
  repeats them under "Limits".
- `TestRedactionLimits` in `src/common/secret/value_test.go` pins them by
  asserting the raw bytes *do* appear, so the day Go closes either hole, or a
  caller assumes it is already closed, the test says so.
- The unexported shape is live in the tree: `usm_security.go:44` (`authPass`,
  `privPass`), `session_engine.go:29` (`community`), `reactor.go:181`, `:218`.
  None of those structs is printed today.

## What it does not cover

Reflection-based printers that are not `fmt` — `go-cmp`, `spew`, a struct
walked by a custom logger — make their own decision about unexported fields
and about calling a `Stringer`. Nothing here constrains them.

It also says nothing about material that has already left the type:
`Reveal` hands out the slice and `RevealString` returns a copy, and neither is
redacted by anything.
