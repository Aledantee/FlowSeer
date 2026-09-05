---
title: The errs Package — FlowSeer's Owned Error Type and Its Conventions
date: 2026-08-20
category: architecture-patterns
module: src/common/errs
problem_type: architecture_pattern
component: service_layer
severity: high
applies_when:
  - creating, wrapping, or matching errors anywhere in hand-written Go under src/
  - declaring a new errs.Code — the append-only, never-renamed, never-reused wire contract
  - deciding whether a fact belongs on the error payload, as an attribute, or only in a log
  - attaching attributes near secret material, or exposing an error across an untrusted boundary
  - implementing the deferred errs wire codec or transport-boundary mapping
related_components:
  - observability
  - api_layer
  - tooling
  - documentation
tags:
  - error-handling
  - errs
  - golang
  - dependency-ownership
  - error-codes
  - slog
  - wire-contract
  - retry-semantics
---

# The errs Package — FlowSeer's Owned Error Type and Its Conventions

`src/common/errs/doc.go` is the authoritative contract for this package, and
`docs/code-style.md` §Errors is the enforceable rule surface. This learning
carries what neither can: why the package is shaped the way it is, which
alternatives were rejected, and which mistakes were made and corrected on the way
there — so the shape is not relitigated from scratch, and the corrections are not
undone by someone who never saw them.

All `file:line` citations are against the tree at `main`. The repository has no
git remote configured, so there are no PR numbers to cite; the four commit
subjects below are local-history references only.

## Context

FlowSeer's error handling was built directly on `go.aledante.io/ae`, an external
module the project does not control. A grounding inventory taken before any
design work found that across all 20 SNMP files plus generated code, the repo
touched exactly six `ae` symbols — `ae.Msg` for sentinels, `ae.New`, `ae.Wrap`
and `ae.Wrapf`, and the `.Attr()` / `.Cause()` builder methods (session history).
Everything else in that module — a fourteen-field struct, printers, exit codes,
hints, tags, trace and span IDs, OTel integration, the recoverable flag — was
carried but unused. Depending on an outside module for something as pervasive as
the error type, while exercising a fraction of its surface, meant importing that
module's design decisions unchanged into roughly two hundred call sites.

Three of those decisions were actively wrong for this codebase, per the plan's
Problem Frame (`docs/plans/2026-08-17-2254-refactor-internal-errs-package-plan.md`):

- `ae.Wrapf` placed the wrapped error *between* the format string and its
  arguments. Every call site read backwards relative to `fmt.Errorf`, and the
  argument in the middle is exactly the position a reader's eye skips.
- The builder re-cloned its attribute maps on every wrap, putting allocation on a
  path the SNMP decode loop hits constantly.
- `Cause` and `CauseUnwrap` hid two different semantics — one joining the
  `errors.Is` chain and one not — behind near-identical names.

The second pressure was structural rather than aesthetic. FlowSeer is growing
into a distributed system: an edge/backend split, Connect RPC, message brokers.
Errors will cross process boundaries. `ae` has no wire story at all, and a survey
of prior art (cockroachdb/errors, samber/oops, pkg/errors, gRPC/Connect practice)
concluded that transit concerns — cross-boundary identity and attribute safety —
have to *shape the core API* rather than being bolted on later. An error type
that decides after the fact which fields are safe to show an untrusted caller
ends up either leaking internal detail or scrubbing at the boundary with a
heuristic. So codes and attribute safety landed in the core payload immediately,
even though the codec that will consume them is still a follow-up.

The result is `src/common/errs`, shipped across four commits: *Add the errs
package as FlowSeer's own error type*, *Migrate hand-written snmp code from ae to
errs*, *Close the errs contract gaps found in review*, and *Give errs a
client-facing, process-facing, and retry-facing payload*, merged as
*Merge branch 'refactor/internal-errs-package'*.

## Guidance

### The builder shape and its terminals

`Builder` is a defined type over the error struct — `type Builder Error`
(`src/common/errs/builder.go:11`), not a type alias — so the terminal is a
conversion rather than a field copy. Every builder method takes a value
receiver and returns a new `Builder`, which makes a partially built value
reusable as the base of several errors — a property
`TestBuilderReuseDoesNotAlias` (`src/common/errs/builder_test.go:60`) pins, and
which `clip` (`src/common/errs/builder.go:146`) makes safe by capping the slice so
the next append copies rather than writing into a sibling's backing array.

The chain terminates only in `Msg` or `Msgf` (`src/common/errs/builder.go:115`,
`:121`), both returning `error`, not `Builder`. There is no `.Build()`. The
canonical shape, from `src/common/errs/doc.go:40`:

```go
return errs.From(err).
    Code(ErrCodePrivDecrypt).
    Attr("engine_id", id).
    PubAttr("proto", "usm-aes").
    Msg("privacy decryption failed")
```

Sentinels are the exception: `errs.Msg` and `errs.Msgf`
(`src/common/errs/errs.go:35`, `:41`) build an error directly with no builder
and, deliberately, no stack — a stack recorded at package init describes the
declaration site, not the failure.

Wrapping is error-first in both forms, and both return `nil` for a `nil` error so
a call site can wrap unconditionally (`src/common/errs/wrap.go:10`, `:20`).

### The payload, and the rule that governs it

The struct is nine fields (`src/common/errs/errs.go:19`): message, code, attrs,
causes; then `userMsg`, `hint`, `exitCode`, `retry`; then the stack. The doc
comment states the bar explicitly (`src/common/errs/doc.go:12`) and the README
repeats it as a convention (`src/common/errs/README.md:79-81`): *a field earns a
place only if a mechanism consumes it* — the process, a retry loop, `errors.Is`,
or the boundary filter — not because a reader might find it interesting. Facts a
reader merely finds interesting are attributes.

That rule kept tags, timestamps, trace IDs, related-errors, and the printer suite
out, and it is also what let user messages, hints, exit codes, and the retry flag
back *in* after they had initially been cut: each clears the bar because a named
mechanism reads it. Severity/log level was considered at the same time and
declined — a code→level table at the handler covers it without growing the
payload.

### Stdlib-native composition

Cause composition is exposed through `Unwrap() []error`
(`src/common/errs/errs.go:85`), with no parallel matching API, no `errs.Match`,
and no second non-matching cause channel. Causes attached with `From` or `.Cause`
join the `errors.Is` chain, full stop (`src/common/errs/builder.go:21`, `:48`) —
which is how the `Cause`/`CauseUnwrap` split is closed.

`Unwrap` returns the error's own slice rather than a copy, and the doc comment
states the aliasing rule the caller must honor instead of claiming immutability
(`src/common/errs/errs.go:77-84`). This was a deliberate reversal during review:
`Unwrap` runs once per node on every `errors.Is` and `errors.As` walk, so copying
there would put an allocation on the package's hottest path. It is the same
contract `errors.Join`'s result carries.

`Error()` renders the error's own message, then `": "` and the cause for a single
cause, or `": [first; second]"` for several (`src/common/errs/errs.go:47-75`) —
reproducing `ae`'s rendering because existing SNMP tests assert cause text through
wrappers.

### Codes as cross-boundary identity

A `Code` is a `string` (`src/common/errs/code.go:17`) declared through `NewCode`,
which validates the `<package>/<name>` shape and registers it in a process-level
`sync.Map`, panicking on a malformed *or already-registered* name
(`src/common/errs/code.go:35-45`). Both segments must start with a lowercase
letter or digit and continue with lowercase letters, digits, `-`, or `_`
(`src/common/errs/code.go:97-111`).

Code equality — not pointer identity — is the matching rule:

```go
func (e *Error) Is(target error) bool {
	if e == nil || target == nil {
		return false
	}
	if e.code == "" {
		return false
	}
	//goland:noinspection GoTypeAssertionOnErrors
	t, ok := target.(*Error)

	return ok && t != nil && t.code == e.code
}
```
(`src/common/errs/errs.go:98-111`)

Read the shipped semantics precisely: `Error.Is` matches *only* on code, and only
when the receiver carries one. Plain identity matching is not implemented here —
it falls out of `errors.Is`'s own `==` comparison before it ever calls the method,
which is why `TestIsReflexive` (`src/common/errs/code_test.go:43`) passes for
uncoded errors too. The comparison is against the *target's own* code field, not
a chain search; `CodeOf` (`src/common/errs/code.go:66`) is the chain-searching
extractor, and it resolves outermost-first.

Because the in-process registry only sees packages the current binary links, the
repo-wide uniqueness gate is a source scan in `code_test.go` —
`TestDeclaredCodesAreUniqueRepoWide` (`src/common/errs/code_test.go:182`). The
scan resolves `NewCode` calls *by import path*, not by the spelling of the package
qualifier, with `src/common/errs/testdata/scan/` fixtures covering an aliased import, a dot
import, and an unrelated same-named function
(`src/common/errs/code_test.go:369`). The gate can only read string literals, so
the README states the corresponding author obligation: declare codes with a
literal or the gate cannot check them (`src/common/errs/README.md:85-86`).

### Client-safe attribute marking

Attributes are flat key-value pairs on an append-only slice, merged only at
extraction (`src/common/errs/attr.go:5-8`, `:22`). `.Attr` is internal; `.PubAttr`
sets a `safe` bit at the point of attachment (`src/common/errs/builder.go:34`,
`:42`). Safety is decided where the value is attached, never guessed at the
boundary.

The traversal is fixed and shared: outermost error first, joined branches left to
right, first value seen for a key wins (`src/common/errs/attr.go:80-104`).
Foreign errors are unwrapped *through* but contribute nothing in that same
traversal.

The subtlety worth preserving is in `collect` (`src/common/errs/attr.go:37-62`): a
key is claimed by the first attribute that carries it *whether or not that
attribute is safe*, and only then is the safe filter applied. `SafeAttributes` is
therefore a strict subset of `Attributes` — a key the merge awards to an internal
attribute is *absent* from the safe view rather than falling through to a safe
value deeper in the chain. Getting this backwards (filter, then claim) makes the
two extractors report different values for the same key, which is exactly what
review caught and `TestSafeAttributesAreAStrictSubset`
(`src/common/errs/attr_test.go:162`) now pins.

### `slog.LogValuer`

`(*Error).LogValue` (`src/common/errs/slog.go:30`) renders the whole tree as one
group under fixed keys (`src/common/errs/slog.go:9-18`): the rendered message, the
chain's code, whichever dispositions the chain sets, merged attributes under
`attributes`, and symbolized stacks under `stack`. It uses `eachAttr` — the same
traversal the extractors use (`src/common/errs/slog.go:82-98`) — so logs and
`errs.Attributes` cannot disagree; `TestLogValueAndAttributesAgree`
(`src/common/errs/slog_test.go:80`) enforces that against a shared fixture.

`dispositionAttrs` (`src/common/errs/slog.go:58`) emits each of the four mechanism
fields *only when the chain actually carries it*, using the `ok`-returning
internal forms `exitCodeOf` and `retryOf` rather than the public
`ExitCode`/`Retryable`. Without that, an unset exit code would log as `ExitCode`'s
fallback of `1` and an unset disposition as `retryable=false`, asserting in the
log a decision nobody made. `LogValue` is nil-safe on the receiver
(`src/common/errs/slog.go:31`) and tolerates odd attribute values
(`src/common/errs/slog_test.go:112`).

Note that internal attributes *do* reach the log — logs are a trusted surface
(`src/common/errs/slog.go:28`). The `PubAttr` distinction exists for the client
boundary, not for logging.

### Origin-only stacks with lazy symbolization

`capture()` records unsymbolized program counters, at most 32 frames, skipping
four (`src/common/errs/stack.go:10`, `:20-25`). It is called only from
`Builder.build`, and only when no cause branch already carries a stack:

```go
func (b Builder) build(msg string) error {
	b.msg = msg
	if !anyStack(b.causes) {
		b.stack = capture()
	}
	e := Error(b)
	return &e
}
```
(`src/common/errs/builder.go:127-137`)

Because `Wrap`/`Wrapf` route through `From(err).build(...)`
(`src/common/errs/wrap.go:15`, `:25`), a wrap over a stack-carrying error captures
nothing — one stack per origin, not one per wrap. A tree joined from
independently created origins legitimately holds several, and `stacks`
(`src/common/errs/stack.go:85`) returns all of them, rendered as numbered groups
by `stackAttr` (`src/common/errs/slog.go:102`). Symbolization is deferred to
`frames()` (`src/common/errs/stack.go:29`) because most errors are handled, not
logged. The frame-skip count in `capture` is load-bearing and documented as such:
its callers must sit exactly one frame below the call site the stack should name
(`src/common/errs/builder.go:125`).

### Extraction, uniformly outermost-first

Every extractor resolves outermost-first through the same `walk`: `CodeOf`
(`src/common/errs/code.go:66`), `UserMessage`/`Hint` via `firstString`
(`src/common/errs/usermsg.go:10`, `:17`, `:24`), `ExitCode` via `exitCodeOf`
(`src/common/errs/exitcode.go:13`, `:27`), `Retryable` via `retryOf`
(`src/common/errs/retry.go:23`, `:29`). The rationale is one sentence, repeated at
each site: the level closest to the caller knows what that caller was trying to
do.

Two of these carry non-obvious defaults, both deliberate:

- `ExitCode` returns `0` for `nil`, the outermost set value, and `1` otherwise
  (`src/common/errs/exitcode.go:13-23`) — so `main` may exit on any error without
  first asking whether a status was named. `.ExitCode(0)` and `.ExitCode(-1)` are
  ignored rather than stored (`src/common/errs/builder.go:90-96`), since zero
  means "unset" and would report success.
- `retry` is tri-state internally — `retryUnset`, `retryYes`, `retryNo`
  (`src/common/errs/retry.go:6-12`) — because a bool cannot distinguish "not
  retryable" from "expressed no view". That distinction is precisely what lets an
  undecided wrapper defer to its cause while `.Fatal()` overrules a transient one.
  A chain expressing nothing is not retryable: retrying is the claim that needs
  making, and a caller that retries a permanent failure loops forever
  (`src/common/errs/retry.go:20-22`).

## Why This Matters

### The alternatives each decision beat

**Redesign freely rather than fork `ae` faithfully or trim it to usage.** A
faithful fork would have carried the `Wrapf` argument order, the per-wrap map
cloning, and the `Cause`/`CauseUnwrap` split into code the project *does* own —
the worst of both, since owning a flawed design removes the excuse for it.

**`errs` at `src/common/errs`, not `errors`.** Naming it `errors` shadows the
stdlib and forces an alias at nearly every call site. Worth noting that the
opening ask named `common/errors`; the rename to `errs` was a deliberate
correction of the original framing, not a default (session history). The package
clause is `package errs` (`src/common/errs/errs.go:1`) and the SNMP migration
imports it unaliased.

**Curated builder plus `slog.LogValuer`, rather than a minimal stdlib-only
surface or a full variadic slog-shaped rewrite.** The chosen shape keeps call
sites reading well and made the migration mostly mechanical. Both losing options
had real costs: stdlib-only means every structured fact becomes string
interpolation, and a variadic rewrite would have touched every one of ~200 call
sites semantically rather than syntactically.

**Cross-boundary identity via stable string codes, not encoded type registries.**
cockroachdb/errors is the cautionary tale: its registry machinery is the price of
encoding types across the wire. Because both ends of a FlowSeer boundary compile
the same `NewCode` declarations, `errors.Is` works across processes with no
registration protocol at all — the whole mechanism is the `Is` method at
`src/common/errs/errs.go:98` plus a `sync.Map` used only for duplicate detection.
One thing was adopted from cockroachdb/errors: an unknown cause decodes to an
opaque leaf preserving message and code rather than failing (session history).

**Attribute safety marked at creation, not scrubbed at the edge.** Boundary-time
sanitization means a heuristic deciding, at the worst possible moment, whether
`engine_id` is safe to show a stranger. Marking at attachment
(`src/common/errs/builder.go:42`) lets the future RPC boundary filter mechanically
with `SafeAttributes`.

**The wire layer ships design-ready, not implemented.** No consumer exists yet, so
a codec would be speculative — but codes and attribute safety land *now* because
they shape the core API and cannot be retrofitted without breaking every call
site. The accepted direction record
`docs/architecture/2026-09-04-error-wire-design-direction.md` is the
specification the eventual codec must follow; `doc.go` no longer carries a
wire section, only the codes-are-a-wire-contract rule.

### The failure modes this design avoids

- **`ae`'s `Wrapf` argument order.** Error-in-the-middle is a readability bug that
  compounds across every in-scope `Wrapf` call site. The migration moved the error
  first at all of them.
- **Per-wrap map re-cloning.** `errs` appends to a slice and merges only at
  extraction (`src/common/errs/attr.go:37`), and `clip`
  (`src/common/errs/builder.go:146`) keeps builder reuse safe without a copy per
  method call.
- **The `Cause`/`CauseUnwrap` split.** Two similarly named methods with different
  `errors.Is` visibility is a trap that only bites at the moment someone needs
  matching to work. `errs` has exactly one cause channel and it is always visible.
- **The samber/oops `Is` bug class.** That project's issue tracker was read as a
  test checklist (session history): a custom `Is` panicking on non-comparable
  types and giving false positives, duplicated stacks on re-wrap, nil attribute
  values panicking. The tree answers each: `TestIsReflexive`,
  `TestIsWithNilOperands`, `TestIsWithUncomparableCauses`,
  `TestCodedErrorDoesNotMatchUncodedSentinel` (`src/common/errs/code_test.go:43`,
  `:62`, `:84`, `:96`); `TestStackCapturedOnceAtOrigin` and
  `TestJoinedOriginsKeepTheirStacks` (`src/common/errs/stack_test.go:10`, `:40`);
  `TestNilReceiverIsSafe` and `TestLogValueToleratesOddValues`
  (`src/common/errs/slog_test.go:142`, `:112`).
- **Duplicate stacks per wrap** — the pkg/errors lesson: capture once at origin
  (`src/common/errs/builder.go:127-137`), never per wrap.

### A usage inventory measures today's code, not the intended system

This is the most transferable lesson in the whole arc, and it cost a second pass
to learn (session history).

The design opened from a grounding inventory: only six `ae` symbols are used,
therefore everything else — stacks, exit codes, user messages, hints, the
recoverable flag — is dead weight. That inventory was accurate about the call
sites that existed and wrong about the system being built. After the package had
shipped with a four-field payload, the scope was reopened to reinstate exit codes
and user messages, which `doc.go` had explicitly disclaimed.

Two things are worth copying from how that was handled. First, rather than adding
only the two fields named, the field set was settled *once* — a ranked list
considered together — instead of growing the payload twice. Second, the strongest
candidate on that list turned out to be one nobody had asked for: a
retryable/transient flag, argued for because three separate mechanisms (the SNMP
poll loop, broker redelivery, and the Connect boundary mapping to a retryable RPC
code) must each ask "is this worth retrying?". `Error` grew from four fields to
eight, and the *bar* — a field must be consumed by a machine, not merely rendered
— is what kept that growth principled rather than a second round of accretion.

The generalizable rule: a grep of current call sites tells you what a dependency
is used for, never what an owned replacement needs to become. Inventory to size
the migration; design against the intended architecture.

### What review changed, and why those fixes must not be undone

Several errors were caught in document review *before* any code was written
(session history) — including a declared surface that could not have compiled,
because it declared both a `Code` type and a `Code(err)` extractor in one package.
That is why the extractor is named `CodeOf`.

The third commit, *Close the errs contract gaps found in review*, reports five
findings from a nine-reviewer pass. The four that were contract mismatches in the
new package, rather than defects in the migration, are worth carrying forward:

- `SafeAttributes` was not the subset it claimed to be. Fixed by having `collect`
  claim a key *before* applying the safety filter
  (`src/common/errs/attr.go:43-59`); the fix was validated by reverting to the old
  logic to prove the new test actually fails (session history).
- The code-uniqueness gate resolved calls by identifier spelling, so an aliased or
  dot import evaded it entirely. Rewritten to resolve by import path
  (`src/common/errs/code_test.go:369`) with the `src/common/errs/testdata/scan/` fixtures.
- `Unwrap`'s doc comment overclaimed immutability and now states the aliasing rule
  (`src/common/errs/errs.go:77-84`). Note this was a case where the review's
  proposed fix (clone the slice) was deliberately *not* taken — documenting the
  rule was chosen over paying an allocation on the hot path.
- A public `ErrorCode` function with no callers, no test, and semantics shadowing
  `CodeOf` was removed — it is absent from the current tree.

That commit also added a `govet` `printf.funcs` entry naming `errs.Wrapf`,
`errs.Msgf`, and `errs.Builder.Msgf` (`.golangci.yml`), so format-string checking
survives if a wrapper ever stops forwarding directly to `fmt.Sprintf`, plus a
decode-fallback benchmark that puts the error branches on the perf gate. Per that
commit message, the measured cost of origin-stack capture on the `EndOfMibView`
branch that ends every completed `BulkWalk` was 392 B/op and 4 allocs/op with
capture against 136 B/op and 3 allocs/op without. Those figures are substantiated
by the commit message alone — nothing in the tree as committed re-measures them,
so treat them as a recorded observation rather than a reproducible artifact.
Eager capture stayed; the point
was to make the cost visible, not to change the decision. The underlying concern —
that every malformed datagram on the trap listener's unauthenticated path
allocates and captures PCs before the packet is dropped — is documented rather
than eliminated (session history), and is the thing to re-measure if that path
ever becomes hot.

## When to Apply

**Adding a field to the error payload.** Apply the rule at
`src/common/errs/doc.go:12` first: name the mechanism that consumes it — the
process, a retry loop, `errors.Is`, or the boundary filter. If the answer is "a
reader might want it", it is an attribute, not a field. Tags, related errors,
timestamps, trace/span IDs, printers, OTel hooks, and severity were already
rejected under this test and should not be relitigated without new evidence. Any
new field also needs a decision about whether it crosses the wire, and a
`dispositionAttrs`-style entry in `LogValue` that renders it only when set.

**Adding a code.** Codes are a wire contract: append-only, never renamed, never
reused for a different meaning (`src/common/errs/code.go:15`,
`src/common/errs/doc.go:56-58`). Declare at package level with a string literal
argument, or the repo-wide scan gate cannot see it. The first real users are the
MIB parser's diagnostics: `src/protocol/smi/internal/diag/zz_generated_codes.go`
declares every code in the `smi` namespace, generated from the table in
`src/protocol/smi/internal/catalog/catalog.go` precisely so the literal the scan
gate needs exists in ordinary source and lives in exactly one place. That set
also learned something the rule above does not say: append-only is a claim
nothing enforces on its own, so the catalog carries a committed golden of
shipped codes that fails when one disappears.

**Crossing a process boundary.** Read the direction record
`docs/architecture/2026-09-04-error-wire-design-direction.md` before designing
anything. The rules it fixes:
the proto message carries code, message, safe attributes, user message, hint,
retry disposition, and the cause chain; the stack field is populated only on
trusted internal transit and always absent toward a client; the exit code is not
carried at all, because it describes the process that failed rather than the
failure. Decoding must be total — an unrecognized cause degrades to an opaque leaf
preserving message, code, and safe attributes rather than being dropped, and an
unknown code surfaces as an internal error rather than a silent unknown some
`errors.Is` might match by accident. Message text and the cause chain are
trusted-internal content; a client-facing boundary exposes only the code, the safe
attributes, and the user message and hint, falling back to a generic string when
the chain carries none. Decoded errors are accepted only from authenticated,
integrity-protected peers, and peer-supplied codes and attributes never drive
authorization decisions.

**The `ae` migration is complete.** `cmd/mibgen`, its golden fixture, and
`generated/go/mib` now use `src/common/errs`. Neither the root module nor the
standalone SNMP benchmark module contains `go.aledante.io/ae` or
`go.aledante.io/as`. `test/conformance/dependencies/no_as_test.go` scans both
module files and production Go imports so neither dependency can return
unnoticed.

**Implementing the deferred wire layer.** Four pieces were deferred: the proto
message and `Encode`/`Decode`, the Connect boundary interceptor, the
internal-code→RPC-code mapping table with message sanitization, and broker
envelope integration. The first piece landed in
`spec/proto/flowseer/errs/v1/error.proto` and `src/common/errs/wire.go`
(`Encode`, `EncodeForClient`, `Decode`), and two mistakes surfaced only when
review pushed on the total-decode and never-drop-a-code guarantees the design
record states:

- **A captured stack's PC count is not its symbolized frame count.**
  `capture()` bounds `maxStackDepth` program counters
  (`src/common/errs/stack.go:10`), but `stack.frames()` symbolizes them
  through `runtime.CallersFrames`, which expands one PC into several frames
  across an inlined call. `encodeNode` copying `e.stack.frames()` straight
  onto the wire's `stack` field, itself bounded at 32 items
  (`spec/proto/flowseer/errs/v1/error.proto:47`), could therefore emit an
  invalid payload from a legal capture — caught by review, not by a test that
  existed at the time. `boundStackFrames` (`src/common/errs/wire.go:127-134`)
  truncates before assignment, pinned by
  `TestBoundStackFramesEnforcesTheSchemaLimit`
  (`src/common/errs/wire_test.go`). Any field the codec copies from an
  unbounded Go slice onto a bounded wire field needs the same truncation, not
  just this one.
- **`Encode`'s non-`*Error` branch must unwrap, or a coded error one level
  under a plain wrapper vanishes.** `docs/code-style.md` blesses
  `fmt.Errorf("...: %w", err)` "where nothing structured is needed", so a
  `*Error` commonly sits one level below a plain wrapper that carries no code
  or attributes of its own. The first version of `encodeNode` rendered that
  wrapper as a single leaf carrying only `err.Error()`, silently dropping the
  `*Error`'s code and its own causes — the opposite of the design record's
  "an unrecognized cause degrades to a generic opaque leaf that **preserves
  its message, code, and safe attributes**." The fix mirrors `attr.go`'s
  `walk`: the non-`*Error` branch also checks `interface{ Unwrap() error }`
  and `interface{ Unwrap() []error }` and recurses into what it finds
  (`src/common/errs/wire.go:100-113`), pinned by
  `TestEncodeUnwrapsForeignWrapperOverCodedError`. Any future encoder or
  extractor added to this package that walks the error tree by hand, rather
  than through `walk`, needs to unwrap through foreign types the same way or
  it will silently stop at the first one.

## Examples

### Error-first wrapping

The `ae` form put the wrapped error between format and arguments. In
`src/protocol/snmp/ber.go`:

```go
// before
return 0, 0, ae.Wrapf("ber: long-form length with %d octets", errLengthOverflow, n)
// after
return 0, 0, errs.Wrapf(errLengthOverflow, "ber: long-form length with %d octets", n)
```

and the plain form:

```go
// before
return 0, 0, ae.Wrap("ber: parse length", errTruncated)
// after
return 0, 0, errs.Wrap(errTruncated, "ber: parse length")
```

`errors.Is(err, errTruncated)` holds in both, which is exactly why the unchanged
SNMP suite could serve as the migration's oracle. Note that `ae.Wrap` is also
message-first, not just `Wrapf` — an undercount caught in review, since it roughly
doubled the number of call sites whose argument order flipped (session history).

### Sentinels

```go
// before
var ErrPrivDecrypt = ae.Msg("USM decryption failed")
// after
var ErrPrivDecrypt = errs.Msg("USM decryption failed")
```
(`src/protocol/snmp/usm_priv.go:37`.) Sentinels capture no stack, so a package-level
`var` block costs nothing at init.

### Builder with attributes

```go
// before
return nil, ae.New().Attr("have", len(key)).Attr("need", need).Attr("proto", proto.String()).
	Msg("priv key length mismatch")
// after
return nil, errs.New().Attr("have", len(key)).Attr("need", need).Attr("proto", proto.String()).
	Msg("priv key length mismatch")
```
(`src/protocol/snmp/usm_priv.go:61`.) Mechanically identical — which was the point of
keeping `ae`'s method names. One semantic change rides along: `ae.Attributes(err)`
read only the top error's own attributes, while `errs.Attributes` merges the whole
chain. The existing assertions passed because each asserted attribute sits on the
outermost error and no test asserted an exact attribute set or a key's absence — a
condition that was *audited* before the sweep, not assumed. (The plan had
originally justified chain merging by claiming existing tests depended on that
shape; review established the premise was false, and the behavior was kept on its
own merits — session history.)

### The secret-material rule

This is the convention `src/common/errs/doc.go` codifies and the migration
was required to audit against: raw secret material never becomes an attribute and
never reaches a message — no keys, salts, passwords, or derived key bytes. Attach
the length and the protocol name instead. Material that has to be carried at all
is a `secret.Value` (`src/common/secret`), which redacts itself wherever the
error is rendered.

The USM code reads this way throughout:

```go
if len(key) != need {
	return nil, errs.New().Attr("have", len(key)).Attr("need", need).Attr("proto", proto.String()).
		Msg("priv key length mismatch")
}
```
(`src/protocol/snmp/usm_priv.go:60-62`) — the key's *length* and the protocol's
*name*, never the key.

```go
if len(privParams) != 8 {
	return nil, errs.New().Attr("len", len(privParams)).Cause(ErrPrivDecrypt).Msg("AES salt wrong length")
}
```
(`src/protocol/snmp/usm_priv.go:133`) — the salt's length, never the salt.

```go
return nil, errs.New().Attr("proto", proto.String()).Msg("no key derivation for AuthProtocolNone")
```
(`src/protocol/snmp/usm_kdf.go:52`), and the same shape at
`src/protocol/snmp/usm_kdf.go:71`, `:160`; `src/protocol/snmp/usm_auth.go:38`, `:94`,
`:120`.

`ErrPrivDecrypt`'s own doc comment states the guarantee inline — "It carries no
key material" (`src/protocol/snmp/usm_priv.go:36`). That is the pattern worth
copying: when a sentinel or builder sits on a path where secrets are in scope, say
in the comment what it does *not* carry, so the next person editing the attribute
list sees the constraint before they add to it.

## Related

- `src/common/errs/doc.go` — the authoritative package contract, including the
  secrets rule; the wire design moved to
  `docs/architecture/2026-09-04-error-wire-design-direction.md`. This learning
  explains the reasoning; `doc.go` is what must be obeyed. `README.md` is a thin
  surface map and is expected to drift, a convention borrowed from the SNMP
  package.
- `docs/code-style.md` §Errors — the enforceable repo-wide rules this work
  produced. Consult it for what to do; consult this doc for why.
- [SNMP Collection Library — Architecture and Fast-Path Conventions](snmp-collection-library-architecture-and-fast-path-conventions.md)
  — `src/protocol/snmp` is the largest consumer of `errs` and served as the
  migration oracle; its unchanged test suite is what proved the rename
  behavior-preserving. The `doc.go`-is-authoritative documentation convention used
  here is borrowed from that package.
- `docs/plans/2026-08-17-2254-refactor-internal-errs-package-plan.md` — the tracked
  source plan, with the full requirement list and prior-art survey. Use it for
  historical provenance; the current package contract lives in
  `src/common/errs/doc.go`.
