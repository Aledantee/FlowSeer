---
name: Protobuf Style
last_updated: 2026-08-16
---

# FlowSeer — Protobuf Style

Conventions for the schemas under `spec/proto/`. The [Buf style
guide][buf-style] is the baseline; `buf lint` and `buf breaking` (configured in
`buf.yaml`) enforce the mechanical part. This document owns the judgement rules.

The comment discipline and the *Rules for coding agents* in
[`code-style.md`](code-style.md) apply language-independently — comments explain
*why*, no process narration, no planning identifiers, no TODOs.

[buf-style]: https://buf.build/docs/best-practices/style-guide/

## Two modules, two standards

`buf.yaml` splits `spec/proto/` into two modules:

- **`spec/proto/ruckus/`** — vendor telemetry schemas. Mirrors of upstream; they keep
  their historical lint excepts and `WIRE` breaking checks. Do not "clean up" vendor
  protos to match our conventions — fidelity to the vendor wire format wins.
- **`spec/proto/flowseer/`** — schemas we own. The `MINIMAL` lint category is the
  enforced floor; the conventions below are the actual bar, held in review.

## FlowSeer-owned schemas

**Files & packages**
- One top-level entity per file; file names `lower_snake_case.proto`.
- Package names are versioned: `flowseer.<domain>.v1`. Directory structure matches
  the package (`spec/proto/flowseer/<domain>/v1/…`).
- Never use `import public`. Never edit anything under `generated/` — it is
  `buf generate` output, regenerated from these sources.

**Naming**
- Messages and enums `PascalCase`; fields `lower_snake_case`; enum values
  `UPPER_SNAKE_CASE`; services `PascalCase` with a `Service` suffix.
- Enum values are prefixed with the enum name, and the zero value is
  `<ENUM_NAME>_UNSPECIFIED = 0`. The prefix is not decoration: enum values share the
  package scope in proto3, so unprefixed values from two enums collide.
- RPCs are named for the action (`GetFlow`, `ListDevices`); each takes a dedicated
  `<Rpc>Request` and returns `<Rpc>Response`, even when a message would seem
  shareable — shared request/response types weld unrelated RPCs together at the
  first divergence.

**Evolution**
- Never reuse or renumber a field. On removal, `reserved` the number *and* the name
  in the same change.
- Prefer adding an optional field over changing a field's meaning. A semantic change
  behind an unchanged field number is invisible on the wire and is the worst class
  of schema bug.
- Breaking checks are suspended (`breaking.use: []`) until the first stable release;
  until then the two rules above are held in review, after it `buf breaking` takes
  over.

**Validation & comments**
- Constraints live in the schema via `protovalidate` options (`buf.validate.field`),
  not in prose or in per-consumer application code.
- A schema comment states the field's *contract*: units, encoding, valid range if
  protovalidate can't express it, and what an absent value means. Generated docs are
  read by consumers who never open this repo — write for them.

```protobuf
// Good — units, meaning of absence, constraint in-schema
// Poll interval for this device. Unset means the collector default applies.
google.protobuf.Duration poll_interval = 4 [(buf.validate.field).duration.gte = {seconds: 1}];
```

## Workflow

`buf lint` and `buf generate` run in CI; a schema change and its regenerated code
land in the same commit so `generated/` never drifts from `spec/proto/`.

## Sources

Researched 2026-08-16:

- Buf style guide — https://buf.build/docs/best-practices/style-guide/
- Buf lint rules & categories — https://buf.build/docs/lint/rules/
- Protobuf style guide — https://protobuf.dev/programming-guides/style/
- Buf, "Enum names need prefixes" — https://buf.build/blog/totw-3-enum-names-need-prefixes
