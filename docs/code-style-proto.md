---
name: Protobuf Style
last_updated: 2026-08-20
---

# FlowSeer — Protobuf Style

Conventions for the schemas under `spec/proto/`. The [Buf style
guide][buf-style] is the baseline; `buf lint` and `buf breaking` (configured in
`buf.yaml`) enforce the mechanical part. This document owns the judgement rules.

FlowSeer-owned schemas are written in **Protobuf Edition 2024** — not `syntax =
"proto3"`. Editions replace the two hardcoded dialects with one file format whose
behaviour is set by *features*, overridable at file, message, field, or enum
scope. The consequences are not cosmetic: presence, symbol visibility, naming
enforcement, and the shape of the generated Go API all change. The
[Editions overview][editions] is the reference; the rules below are what FlowSeer
does with it.

The comment discipline and the *Rules for coding agents* in
[`code-style.md`](code-style.md) apply language-independently — comments explain
*why*, no process narration, no planning identifiers, no TODOs. Explanatory
prose in schema comments and package READMEs additionally follows
[`doc-style.md`](doc-style.md): state what the type cannot, and avoid the
machine-writing tells it lists.

[buf-style]: https://buf.build/docs/best-practices/style-guide/
[editions]: https://protobuf.dev/editions/overview/

## Two modules, two standards

`buf.yaml` splits `spec/proto/` into two modules:

- **`spec/proto/ruckus/`** — vendor telemetry schemas. Mirrors of upstream, mechanically
  converted from Ruckus's proto2 to edition 2024: file-level proto2-equivalent feature
  pins (`enum_type = CLOSED`, `repeated_field_encoding = EXPANDED`,
  `utf8_validation = NONE`, `json_format = LEGACY_BEST_EFFORT`,
  `enforce_naming_style = STYLE_LEGACY`) plus per-field
  `features.field_presence = LEGACY_REQUIRED` where the vendor wrote `required`. The
  resulting descriptors are wire-identical to the vendor's proto2 originals
  (see [`spec/proto/ruckus/README.md`](../spec/proto/ruckus/README.md)). They keep their historical lint excepts and
  `WIRE` breaking checks. Beyond that mechanical conversion, do not "clean up" vendor
  protos to match our conventions — no renames, no feature-pin changes, no
  restructuring; fidelity to the vendor wire format wins.
- **`spec/proto/flowseer/`** — schemas we own. Edition 2024, no exceptions. The
  `MINIMAL` lint category is the enforced floor; the conventions below are the actual
  bar, held in review.

Both modules are edition 2024, but they follow different standards: vendor files pin
proto2 semantics wholesale at file scope — exactly what the rules below tell you never
to do in FlowSeer-owned schemas. The split is a source-convention boundary, not a
compatibility one.

## FlowSeer-owned schemas

**File header**

Every file we own opens with the edition declaration. There is no `syntax` line.

```protobuf
edition = "2024";

package flowseer.device.v1;
```

- Apart from `README.md` files at package boundaries, `spec/proto/` contains
  protobuf definitions only. Put executable schema tests in
  `src/common/protoconformance/` and fixtures in that package's `testdata/` so every
  schema file remains normal Buf input. Never add a `buf.yaml` exclude or lint ignore
  to shelter test artifacts inside the schema tree.
- One top-level declaration per file by default; file names `lower_snake_case.proto`.
  A tightly coupled Primitive family may share one file when its variants and
  value types are designed, imported, and evolved as one contract. The
  `net/addr/v1/ip.proto` family is the worked exception; each Entity triad and
  ref message remains in its own file.
- Package names are versioned: `flowseer.<domain>.v1`. Directory structure matches
  the package (`spec/proto/flowseer/<domain>/v1/…`).
- Never use `import public` — it is still legal grammar in edition 2024, and still
  forbidden here. `weak` imports no longer exist at all. Use `import option` for a
  file imported solely to bring custom options into scope.
- Never edit anything under `generated/` — it is `buf generate` output, regenerated
  from these sources.
- Do not set file-level feature overrides casually. A feature set at file scope
  silently retunes every field below it; when a single field needs different
  behaviour, override on that field.

**Presence: every field is optional**

This is the rule that catches proto3 habits. In edition 2024 the `optional` and
`required` labels do not exist — `repeated` is the only remaining label, and the
compiler rejects the others outright:

```
t.proto:3:15: unexpected `optional`
t.proto:3:15: unexpected `required`
```

Presence is a feature, not a keyword, and its edition 2024 default is `EXPLICIT`.
So every singular field — scalars included — tracks presence, and nothing is ever
required on the wire:

- **Absence is always representable.** Every singular field can arrive unset, and
  `0`/`""`/`false` are distinguishable from unset. Write schemas and consumers that
  answer "what does absent mean here?" for every field.
- **Never `LEGACY_REQUIRED`.** `features.field_presence = LEGACY_REQUIRED` reproduces
  proto2 `required` and inherits its whole failure mode: a required field can never be
  removed, and a producer that omits it makes the message unparseable for everyone.
  Buf's `FIELD_NOT_REQUIRED` lint rule (in `BASIC` and `STANDARD`) forbids it.
- **"Required" is a validation concern, not a schema-presence one.** Express it with
  protovalidate — `(buf.validate.field).required` — where it is enforced at the
  boundary and can be relaxed without a wire-format change.
- **`IMPLICIT` is a deliberate, documented choice.** `features.field_presence =
  IMPLICIT` restores proto3's no-presence behaviour: no `Has` accessor, no explicit
  default allowed, and the zero value indistinguishable from unset. Use it only where
  that collapse is genuinely correct (a counter, a flag whose false *is* its default),
  and say so in the field comment.

```protobuf
// Poll interval for this device. Unset means the collector default applies.
google.protobuf.Duration poll_interval = 4 [(buf.validate.field).duration.gte = {seconds: 1}];

// Consecutive failed polls. Implicit presence: 0 and "never polled" are the
// same state, and the collector treats them identically.
int32 failure_streak = 5 [features.field_presence = IMPLICIT];
```

The absence of `required` on `poll_interval` above is the deliberate kind: the field is
optional, and its `gte` rule only applies once a value is supplied. See
[Validation](#validation-protovalidate-always) — under editions that distinction is
carried entirely by `required`, so it has to be a decision rather than an omission.

**Symbol visibility**

Edition 2024 adds `export` / `local` on messages and enums, with the file default set
by `features.default_symbol_visibility`. The edition default is `EXPORT_TOP_LEVEL`:
top-level types are importable, **nested types are not**. Referencing one across files
is a compile error, not a lint warning:

```
u.proto:4:16: found unexported message type `t.v1.Outer.Inner`
```

- Keep the default. Do not set `EXPORT_ALL`; it exists for migrating legacy files.
- This makes top-level placement load-bearing: a file's top-level types are its
  exported surface, and nested types are implementation detail by construction. If a
  nested type needs to be shared, that is the signal it was never nested — promote it
  to the top level of its file, don't widen visibility.
- Mark a top-level type `local` when it exists only to structure the file it lives in.
  It costs one keyword and stops it from becoming someone else's dependency.

**Naming**

Under edition 2024 `features.enforce_naming_style` defaults to `STYLE2024`, so the
*compiler* now rejects what used to be lint-only advice:

```
t.proto:3:9:  message type name should be PascalCase
t.proto:3:26: field name should be snake_case
```

That covers casing. The rules the compiler does not check still stand:

- Messages and enums `PascalCase`; fields `lower_snake_case`; enum values
  `UPPER_SNAKE_CASE`; services `PascalCase` with a `Service` suffix.
- Enum values are prefixed with the enum name. The prefix is not decoration: enum
  values share the package scope, so unprefixed values from two enums collide. A
  FlowSeer-normalized enum uses `<ENUM_NAME>_UNSPECIFIED = 0`. A registry pass-through
  enum instead uses the registry's assigned value at zero; absence, not a fabricated
  sentinel, means unobserved. Enums are `OPEN` in editions — a consumer will receive
  values it does not know, and must handle them.
- RPCs are named for the action (`GetFlow`, `ListDevices`); each takes a dedicated
  `<Rpc>Request` and returns `<Rpc>Response`, even when a message would seem
  shareable — shared request/response types weld unrelated RPCs together at the
  first divergence.

**Evolution**

- Never reuse or renumber a field. On removal, `reserved` the number *and* the name
  in the same change. One dated exception: on 2026-08-26, inside the pre-release
  window while breaking checks are suspended, a one-time reviewed collapse removed
  every reserved tombstone under `spec/proto/flowseer/` and renumbered the remaining
  fields and enum values to contiguous
  (plan: `docs/plans/2026-08-26-1856-refactor-proto-docs-standards-cleanup-plan.md`).
  From the first stable release onward the prohibition is absolute.
- Prefer adding a field over changing a field's meaning. A semantic change behind an
  unchanged field number is invisible on the wire and is the worst class of schema bug.
- **A feature change is a schema change.** Editions add a failure mode proto3 did not
  have: flipping `field_presence`, `enum_type`, `utf8_validation`, or `json_format`
  alters the contract while every field number stays put. Treat a feature edit with the
  same care as a renumber. `buf breaking` has editions-aware rules for exactly this —
  `FIELD_SAME_CARDINALITY`, `FIELD_WIRE_COMPATIBLE_CARDINALITY`, `ENUM_SAME_TYPE`,
  `FIELD_SAME_UTF8_VALIDATION`, `MESSAGE_SAME_JSON_FORMAT`.
- Breaking checks are suspended until the first stable release via a module-wide
  `breaking.ignore` of `spec/proto/flowseer` in `buf.yaml` (an empty `breaking.use: []`
  would not disable them — buf treats it as unset and falls back to its default `FILE`
  category); until then the rules above are held in review, after it `buf breaking`
  takes over.
- Bumping the edition of an existing file is a deliberate, reviewed migration, not
  housekeeping: the new edition's defaults apply to every field at once. Edition 2026
  exists on paper — it changes `default_symbol_visibility` to `STRICT`, naming to
  `STYLE2026`, and adds `enforce_proto_limits` — but no toolchain in this repo accepts
  it yet. Stay on 2024 until it does.

**Comments**

A schema comment states the field's *contract*: units, encoding, valid range if
protovalidate can't express it, and what an absent value means. Since edition 2024
makes *every* singular field absent-able, the last of those is no longer optional
prose — it is the field's most easily-missed state. Generated docs are read by
consumers who never open this repo — write for them.

**Message field spacing**

Inside any message under `spec/proto/`, place adjacent fields directly next to
each other. Do not insert a blank line between one field declaration and the
doc comment for the next field; the comments already provide the visual
separation. Keep blank lines around structurally distinct blocks such as
message-level options, `reserved` declarations, nested declarations, and
`oneof` blocks. For vendor mirrors, this whitespace-only normalization is the
sole exception to preserving the upstream layout.

```protobuf
message DeviceSummary {
  // The device identifier. Unset is invalid.
  string id = 1 [(buf.validate.field).required = true];
  // The display name. Unset means no name was reported.
  string name = 2;
}
```

## Validation: protovalidate, always

**Every rule a field has, the schema carries.** If a constraint can be expressed in
protovalidate, it is expressed there — not in prose, not in a handler, not in each
consumer's own defensive check. The schema is the single place a constraint is stated
and the single place it is changed. A validation rule living in Go is a rule the web
client does not have, and vice versa.

This is a default, not an absolute. The escape hatches are narrow and each one is
worth a comment saying why:

- The rule needs data the message does not contain — a database lookup, another
  request's state, the caller's permissions. That is authorization or business logic,
  and it belongs in the service.
- The rule is genuinely unexpressible in CEL over this message. Rare; try
  `(buf.validate.field).cel` and `(buf.validate.message).cel` before concluding it.
- The field is a vendor mirror under `spec/proto/ruckus/`, which we do not annotate.

If you catch yourself writing an `if req.GetX() == ""` guard in a handler, the rule
belonged in the schema.

**The editions trap: a rule without `required` is a no-op on absence.**

This is the single most important interaction between edition 2024 and protovalidate,
and it silently reverses a proto3 habit. Protovalidate skips a field's rules when the
field tracks presence and is unset. Under proto3 most scalars had no presence, so
rules always ran. Under edition 2024 **every** singular field tracks presence, so
every rule is now conditional on the field being set. Verified at runtime against an
empty message:

```protobuf
// Rule, but no `required` — silently passes when unset.
string id = 1 [(buf.validate.field).string.min_len = 3];

// The same rule, actually enforced.
string name = 2 [
  (buf.validate.field).required = true,
  (buf.validate.field).string.min_len = 3
];
```

```
empty message:
 - name: value is required     <- id is absent from this list
```

So: **if a field must be present, say `required`.** Its rules alone will not say it
for you. Conversely, omitting `required` is how you spell an optional field with a
"valid if provided" contract — which is a real and common design, just make it the
deliberate one. State which it is in the field comment; "unset means X" and
`required` are answers to the same question.

`IGNORE_IF_ZERO_VALUE` has no place in a field we own. On a presence-tracking field it
is redundant with the default, and buf lint says so — *"has
(buf.validate.field).ignore=IGNORE_IF_ZERO_VALUE and tracks presence. This is the same
the default and the ignore option can be removed."* Combined with `required` it is a
contradiction and a hard lint error. It is only meaningful on a field you have
explicitly set to `IMPLICIT` presence.

**Reach for the whole vocabulary**

`required` plus a type rule is the common case, not the whole toolbox. The rule set is
richer than most schemas use, and a rule that already exists beats a hand-rolled CEL
expression:

```protobuf
// Well-known formats — prefer the named rule over a regex.
string email = 1 [(buf.validate.field).string.email = true];
string device_uuid = 2 [(buf.validate.field).string.uuid = true];
string endpoint = 3 [(buf.validate.field).string.uri = true];

// Collections validate the container and its elements.
repeated string tags = 4 [(buf.validate.field).repeated = {
  min_items: 1,
  items: {string: {min_len: 1}}
}];

// Enums: reject the zero value where "unspecified" is not a legal input.
Status status = 5 [(buf.validate.field).enum.defined_only = true, (buf.validate.field).required = true];

// Cross-field invariants belong at message level, guarded for absence.
option (buf.validate.message).cel = {
  id: "flow_window.ordered"
  message: "window_end must be after window_start"
  expression: "!has(this.window_start) || !has(this.window_end) || this.window_end > this.window_start"
};

// A oneof that must be populated.
oneof target {
  option (buf.validate.oneof).required = true;
  string device_id = 6;
  string site_id = 7;
}
```

Two notes on CEL. Write message-level expressions to tolerate absent fields — under
explicit presence `has(this.x)` is meaningful for scalars too, and an expression that
assumes population will fire on partial messages. And give every `cel` rule a stable
`id` and a `message` written for the API consumer who will read it in an error
response, not for the reviewer of this file.

**Where rules are enforced**

Declaring rules does nothing on its own; something must run them. Both edges of the
system validate, from the same schema:

- **Go / Connect** — a validating interceptor built on `buf.build/go/protovalidate`
  (`connectrpc.com/validate`) on every server. Wired once at the server, not per
  handler; a handler that re-checks what the interceptor already enforced is the
  duplication this section exists to prevent.
- **Web** — `@bufbuild/protovalidate` against the generated TS, so the client rejects
  what the server would reject and the user sees it before the round trip.

Rules are also a build-time artifact: buf's `PROTOVALIDATE` lint rule compiles every
CEL expression and checks rules for well-formedness, so a malformed or unenforceable
rule fails CI rather than production.

## What editions change downstream

**Go: the opaque API is the default.** Edition 2024 sets `(pb.go).api_level =
API_OPAQUE`. Generated structs no longer expose their fields — they carry
`xxx_hidden_` members plus `Get*`/`Set*`/`Has*`/`Clear*` accessors and a
`<Message>_builder` for construction. Consequences for Go under `src/`:

```go
// Construction — builder, used as an immediate value, never stored in a variable.
d := devicev1.Device_builder{
    Id:           proto.String(id),
    PollInterval: durationpb.New(interval),
}.Build()

// Mutation — setters. Preferred over builders on hot paths; a builder walks
// every field and can allocate.
d.SetPollInterval(durationpb.New(interval))

// Presence — Has*, not a nil check or a zero-value comparison.
if d.HasPollInterval() { … }
```

Struct literals, direct field reads, and `== nil` presence tests do not compile. This
is the upstream recommendation for new development, and FlowSeer takes it: the checks
it buys — no accidental zero-vs-unset conflation, no pointer-identity comparisons —
are worth more here than familiar syntax. `HasX` is generated only for fields with
explicit presence, which is another reason `IMPLICIT` needs justification.

**TypeScript.** Protobuf-ES v2 (`buf.build/bufbuild/es`) supports editions fully; the
generated TS surface is unchanged by the migration. Web-side rules live in
[`code-style-web.md`](code-style-web.md).

## Workflow

`buf lint` and `buf generate` run in CI; a schema change and its regenerated code
land in the same commit so `generated/` never drifts from `spec/proto/`.

Bare `buf generate` uses `buf.gen.yaml`, which includes the TypeScript leg and
writes it under `frontend/web/generated/proto/`. `--path` narrows the *inputs*,
not the plugins, and buf creates a missing output directory — so a scoped run
with the default template still emits TS into a web tree that may not exist in
your checkout. To regenerate only the owned Go output:

```
buf generate --template buf.gen.go.yaml --path spec/proto/flowseer
```

Toolchain floor for edition 2024 — below any of these, the schemas do not build:

| Tool | Minimum | Note |
| --- | --- | --- |
| `buf` | 1.68.1 | 1.68.0 shipped editions 2024 support gated; 1.68.1 ungated it |
| `protoc` | 35.x | only needed outside the buf path |
| `google.golang.org/protobuf` | 1.36.x | declares `EDITION_2024` as its maximum |
| `protoc-gen-connect-go` | current | advertises editions up to 2024 |
| `@bufbuild/protoc-gen-es` | v2 | v1 is proto2/proto3 only |
| `buf.build/go/protovalidate` | v1.x | canonical Go import path; verified against v1.3.0 |

## Sources

Researched 2026-08-16, editions material added 2026-08-20:

- Buf style guide — https://buf.build/docs/best-practices/style-guide/
- Buf lint rules & categories — https://buf.build/docs/lint/rules/
- Protobuf style guide — https://protobuf.dev/programming-guides/style/
- Buf, "Enum names need prefixes" — https://buf.build/blog/totw-3-enum-names-need-prefixes
- Protobuf Editions overview — https://protobuf.dev/editions/overview/
- Feature settings for editions (per-edition defaults) — https://protobuf.dev/editions/features/
- Edition 2024 language specification — https://protobuf.dev/reference/protobuf/edition-2024-spec/
- Symbol visibility — https://protobuf.dev/programming-guides/symbol_visibility/
- Go opaque API migration & FAQ — https://protobuf.dev/reference/go/opaque-migration/
- Buf CLI changelog (editions support history) — https://github.com/bufbuild/buf/blob/main/CHANGELOG.md
- Protovalidate rule reference — https://protovalidate.com/reference/rules/
- Protovalidate standard rules — https://protovalidate.com/schemas/standard-rules/
- `buf/validate/validate.proto` (`required` and `Ignore` semantics) —
  https://github.com/bufbuild/protovalidate/blob/main/proto/protovalidate/buf/validate/validate.proto

Compiler messages, lint messages, and validation output quoted above were reproduced
locally against buf 1.72.0 and protovalidate with the repo's own `buf.gen.yaml` plugin
set — including the empty-message result showing that a rule without `required` does
not fire on an absent field.
