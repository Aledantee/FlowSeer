---
name: Code & Comment Style
last_updated: 2026-09-15
---

# FlowSeer — Code & Comment Style

The authoritative style guide for FlowSeer's Go. It binds **human contributors and
coding agents equally**. Mechanical rules (formatting, import order, lint) are owned
by the toolchain and enforced in CI; this document owns the judgement rules a linter
cannot check — what a doc comment must say, when a comment earns its place, error and
concurrency discipline, and how to keep agent-written code clean.

Target runtime: **Go 1.27+**. The baseline is [Effective Go], the [Go doc-comment
conventions][doc-comments], and the [Google Go Style Guide][google-style]; where they
are silent, the [Uber Go Style Guide][uber-style] is advisory. When a rule below
conflicts with older advice found online, this document wins.

[Effective Go]: https://go.dev/doc/effective_go
[doc-comments]: https://go.dev/doc/comment
[google-style]: https://google.github.io/styleguide/go/
[uber-style]: https://github.com/uber-go/guide/blob/master/style.md

## Principles

1. **Idiomatic Go over imported habits.** No Java-style class hierarchies, no
   exception-shaped panic flows, no dependency-injection frameworks. Write the code a
   longtime Go reader expects. [Panics](#panics) says what the few sanctioned
   panics look like and what a reader may assume about them.
2. **Doc comments are mandatory on the exported surface.** Every exported name states
   its *contract* — what it promises, what it requires, what errors it returns,
   whether it is safe for concurrent use. Never restate the signature.
3. **Comments are rare and explain _why_.** Code says what happens; a comment exists
   only when the *reason* is not recoverable from the code. Prefer making the code
   clear enough that the comment is unnecessary.
4. **Readable over clever.** A competent Go reader who doesn't know this codebase
   should follow any function top to bottom on first read. Clarity beats brevity;
   brevity beats ceremony.
5. **Simplest thing that satisfies the requirement.** No speculative abstraction, no
   configurability nobody asked for, no handling of errors that cannot occur given the
   caller's contract.

## Doc comments

**Every exported name — package, type, function, method, constant, variable — has a
doc comment.** Unexported names are exempt; name them well instead, and document them
only when the contract is genuinely non-obvious (a background goroutine's lifecycle, a
struct that guards an invariant, a panic the function inherits from something it
calls). An inherited panic is the case an unexported name cannot carry on its own,
so [Panics](#panics) makes the comment mandatory there.

Form follows [go.dev/doc/comment][doc-comments]:

- Complete sentences, present tense, starting with the symbol name:
  `// Session is …`, `// NewSession opens …`.
- One package comment per package (`// Package snmp …`), in the file that best
  anchors the package; multi-paragraph package overviews live in `doc.go`.
- Link to other symbols with `[Symbol]` / `[pkg.Symbol]` doc links, not backticks.
- Mark deprecations with a `// Deprecated:` paragraph.

A doc comment documents the **contract**, not the mechanics:

- What the symbol *does* at a level the name alone can't convey.
- Preconditions, invariants, and side effects — including whether the zero value is
  usable and whether the type is safe for concurrent use.
- Error semantics: which sentinel errors callers can `errors.Is` against, and what a
  wrapped error means.
- The *meaning* of each parameter where it isn't obvious — never its type.

```go
// Bad — restates the signature, adds nothing
// Connect connects to the server with a timeout.
func Connect(addr string, timeout time.Duration) (*Conn, error)

// Good — states the contract the signature can't
// Connect opens a persistent connection to the collector at addr
// ("host:port"). It blocks until the handshake completes or timeout
// elapses, whichever comes first. The returned Conn is safe for
// concurrent use. Connect returns [ErrRefused] if the collector
// actively rejected the dial; context-style cancellation is not
// supported — close the Conn instead.
func Connect(addr string, timeout time.Duration) (*Conn, error)
```

## Comments

Comments are the exception, not the habit. Before writing one, try to make the code
say it instead — a precise name, a small extracted function, an explicit intermediate
variable.

Write a comment only to capture what code cannot:

- **Why**, not what: the reason a non-obvious decision was made (why the socket is
  connected rather than bound, why this retry uses full jitter, why an error is
  deliberately discarded).
- A surprising-but-correct behavior (`// NOTE:`), or a workaround with a link to the
  upstream issue it works around.

```go
// Bad
i++ // increment i

// Good — captures a non-obvious reason
// Skip the message header; the PDU offset table starts here.
i++
```

Never commit: comments that restate the line, banner/section dividers, commented-out
code, `// TODO` notes (solve it or raise it in review), or hedging asides
(`// this might be slow`). A stale comment is worse than none — update or delete it
the moment the code changes.

How the prose itself reads — in comments, READMEs, and everything under `docs/` —
is governed by [`doc-style.md`](doc-style.md): explain why, show a working
example, document the hard parts, no marketing register, and none of the
machine-writing tells it catalogues. Comment density is part of the same rule:
a comment on every line is the most-cited sign of unreviewed machine output.

## Naming

- **MixedCaps**, never underscores. Initialisms keep their case: `ID`, `OID`, `URL`,
  `SNMP` (`sessionID`, not `sessionId`).
- **Package names** are short, lowercase, singular, and content-describing: `snmp`,
  not `snmputil` or `common`. No stutter — the caller reads `snmp.Session`, so the
  type is `Session`, not `SNMPSession`.
- No `Get` prefix on accessors (`s.Version()`, not `s.GetVersion()`). The one
  accepted exception: an accessor whose natural name is taken by the embedded or
  promoted field it exposes (`GetHeader` beside an embedded `Header`, the same
  reason protobuf getters carry the prefix).
- Receiver names are one or two letters, consistent across the type's methods
  (`func (s *Session)`), never `this` or `self`.
- Short names for short scopes (`i`, `buf`, `ok`); longer names as scope grows. The
  greater the distance between declaration and use, the more descriptive the name.
- Error variables are `errXxx` (exported: `ErrXxx`); error types are `XxxError`.
- Comments, identifiers, and messages use **US English** ("serialize",
  "behavior") — the Go ecosystem's convention, enforced by `misspell`.

## Readability & spacing

- One job per function. If you can't name it without "and", split it.
- **Line of sight: keep the happy path at minimal indentation.** Handle the error or
  special case and return early; avoid `else` after a terminating `if`.
- **Use blank lines to separate logical paragraphs within a function.** Group related
  statements — setup, a guard block, one unit of work, the return — with a single
  blank line between groups. The whitespace lets a reader scan structure without
  parsing every line. If a paragraph needs a label to be clear, extract it into its
  own function. No more than one consecutive blank line (gofumpt enforces this).
- Group related constants and variables in `const (…)` / `var (…)` blocks; one block
  per concern, not one block for the whole file.
- No naked returns outside trivial few-line functions. `panic` is not error
  handling; [Panics](#panics) has the rule.
- Match the style of the surrounding code exactly, even where it differs from your
  preference.

```go
// Good — blank lines mark setup, the guard, the work, and the result
func (s *Session) walk(ctx context.Context, root OID) ([]VarBind, error) {
	next := root
	var out []VarBind

	if err := s.checkOpen(); err != nil {
		return nil, err
	}

	for {
		vb, err := s.getNext(ctx, next)
		if err != nil {
			return out, err
		}
		if !root.Contains(vb.OID) {
			return out, nil
		}

		out = append(out, vb)
		next = vb.OID
	}
}
```

## Interfaces & API design

- **Accept interfaces, return concrete types.** Define an interface where it is
  *consumed*, not next to the implementation, and only once a second implementation
  or a test seam actually needs it.
- Keep interfaces small — one to three methods. A large interface is a missing
  decomposition.
- **Make the zero value useful** where practical; otherwise reject it loudly in the
  constructor rather than silently defaulting (see `Version` in `snmp`: the zero
  value is invalid by design so the choice is visible in review).
- **Required parameters are positional arguments, never options** — the compiler,
  not a constructor's runtime check, enforces that they are supplied.
- **Optional configuration prefers a plain options struct** (`New(addr,
  Options{…})` or exported fields on the returned type, the stdlib's own idiom —
  `http.Server`, `tls.Config`). A struct is self-documenting at the call site,
  discoverable from the type alone, and free of per-option closure ceremony —
  the reasons much of the community has cooled on functional options.
- **Functional options (`WithX(…)`) are the exception**, justified only when the
  Google-style profile holds: there are many knobs, most callers set none, and
  each individual option is used sparsely. `snmp.Session` fits that profile and
  keeps the pattern; do not introduce it for a type with a handful of options or
  where typical callers set several of them.
- Generics only where they remove real duplication across concrete types. Do not
  introduce a type parameter an `interface` argument would serve equally well.

## Errors

- When error handling emits telemetry, follow the [observability
  conventions](conventions/observability.md) for signal selection, severity,
  message formulation, attributes, and span status.
- **Handle every error.** Handle it or return it — never both (no log-and-return:
  the caller will log it again). `_ =` discards must be justifiable in review.
- The project's own `src/common/errs` package is the norm: `errs.Msg` for sentinels,
  error-first `errs.Wrap(err, "open session")` / `errs.Wrapf(err, "dial %s", target)`
  for context, and the `errs.New()` / `errs.From(err)` builder when the error carries
  a code or attributes. Its `doc.go` is the authoritative reference. Plain
  `fmt.Errorf("open session: %w", err)` stays fine where nothing structured is
  needed.
- Wrap with context when crossing a meaningful boundary. Add context the caller
  doesn't already have; never prefix with `failed to` at every level.
- Sentinel errors and error types exist for callers to branch on — match with
  `errors.Is` / `errors.As`, never string comparison. Export a sentinel only when a
  caller genuinely needs to distinguish it; otherwise keep it unexported.
- Attach a value someone will query or branch on as an attribute
  (`errs.New().Attr("got", n)`) rather than only interpolating it, so it survives
  into logs as a field. Interpolating a value that exists to make the message
  readable is fine — most of `src/protocol/snmp` does exactly that. Never attach or
  interpolate raw secret material — attach a length and a protocol name instead.
- In hand-written Go under `src/`, an exported field holding a password,
  passphrase, private key, or community string is a `secret.Value`
  (`src/common/secret`), never a `string` or `[]byte`. The type redacts itself
  under `fmt`, JSON, and `slog`, so the struct holding it stays printable; the
  `src/common/internal/secretguard` test fails when such a field takes a raw
  type. Unexported fields are outside the rule, and a struct holding one is not
  safe to print — see the package documentation.
- Errors that cross a process boundary carry an `errs.NewCode("<package>/<name>")`
  code, their stable identity on the wire. Codes are append-only: never renamed,
  never reused for a different meaning.
- The error message is written for the log and may name hosts, engine IDs, and
  call paths. What an end user or an untrusted caller sees goes in `.UserMsg`,
  with the remedy in `.Hint` — set them at the level closest to that caller,
  which knows what they were trying to do. Never write the internal message so
  it can double as both.
- Mark a transient failure `.Retryable()` where it is diagnosed, and `.Fatal()`
  at the level that gives up on it, so a poll loop, a broker, or an RPC boundary
  never has to pattern-match on codes to decide whether to try again. Silence
  means not retryable.
- A command that ends the process sets `.ExitCode(n)` on the error that ended
  it; `main` exits with `errs.ExitCode(err)`, which is 0 for nil and 1 for any
  error that named no status.
- Error strings are lowercase and unpunctuated (`"request timed out"`), because they
  compose into larger messages.
- Context cancellation surfaces as the unwrapped `ctx.Err()`, not a look-alike
  timeout error, so `errors.Is(err, context.Canceled)` works end to end.

## Panics

A panic is not a way to report an error, and no panic in a running service is an
outcome this codebase plans for — the restart that follows is the last resort.
Three clauses must all hold for every `panic` in non-test code under `src/`. A
panic that satisfies two of them is still a bug.

**Named.** A panic appears only inside a function whose name begins with `Must`
or `must`, exported or not. The prefix is what a reader sees at the call site,
which is the only place the risk is actionable: `snmp.MustOID`,
`snmp.MustChangeIndicator` and `catalog.MustRegister` already read that way. One
exemption exists, for a site whose comment cites a committed benchmark measuring
what the error return costs. Cite it as a repository-relative path and a function
name, never as "a benchmark in this module" — benchmarks live in separate modules
on purpose, so their dependencies stay out of the main graph, and a same-module
requirement would make the exemption unreachable where it is most likely earned.

**Handled.** Three answers count, and the panicking site names the one it relies
on.

- *Init-time.* The panic is evaluated during package initialization and depends
  only on values fixed at compile time. That is worded by behavior rather than by
  position in the file for a reason.
  `var x = sync.OnceValue(func() … MustCompile(cfg.Pattern))` sits in a `var`
  block and panics at first use inside a serving process, and
  `var x = MustLoad(os.Getenv("…"))` fails only in the deployment whose
  environment is wrong — every replica of a rolling deploy at once, which is an
  outage. A closure stored at init and invoked later is a runtime panic.
- *Proven.* An earlier stage established the invariant with a check that runs on
  every `go test`, cited at the panicking site. The check has to run, not merely
  exist: a validation a generator performed does not re-verify the committed
  artifact another package reads, so that proof decays to "a check that ran
  once". Write the proof as a test over the artifacts the panic reads, not over
  the generator that wrote them.
- *Foreign-code boundary.* A recover stands between the panic and the process
  top, and the panicking code is code that boundary does not own: a
  caller-supplied handler, a gate probe, a third-party library. First-party code
  inside a boundary may not name that boundary as its handler. The recover sites
  in `src/common/service` cover every module and service in the repository, so
  the other reading would let a `mustDecodeField` helper panic inside a NATS
  handler, be caught by `callHandler`, and pass the rule — while what landed is a
  throw/catch flow over a half-processed message. Those boundaries exist to
  contain other people's bugs, not ours.

**Documented.** The function that can panic states the panic, the invariant
behind it, and which of the three answers above it relies on. So does every
caller, up to the first one that fixes the invariant. The obligation stops at a
caller supplying literal or compile-time constant arguments, and a panic proven
unreachable carries no caller obligation at all. That bound is why
`regexp.MustCompile`'s callers document nothing and are not being lax: the
argument is closed at the call site, so the invariant never travels.

### A `go` statement resets handling

A panic inside a spawned goroutine is unhandled by definition, whatever recover
its spawner sits under, because Go does not propagate it to the spawning frame.
A recover in the spawning call chain catches nothing: the goroutine unwinds its
own stack and takes the process with it.

Every goroutine running first-party work is launched through
`spawn.Go` (`src/common/spawn`), which recovers and reports the panic as a
structured error for that unit of work — a log record always, and a
caller-supplied sink where one exists. One reviewed implementation is the point —
the alternative is an inline recover at every `go` statement in the repository,
and sixty-odd separate decisions about what reporting means.

The helper recovers and reports; it does nothing else. It does not join, restart,
back off, or cancel siblings. A caller that must wait keeps its own
`sync.WaitGroup`, and restart policy stays with the supervisor that owns the
work, so that a panic at one of sixty-odd call sites cannot quietly become a
retry loop nobody chose.

What the spawned function owes its caller on the panic path is the part that
has gone wrong repeatedly, in a dozen call sites across five packages, three of
them carrying a comment claiming it was handled. The recover runs *after* every
deferred call the function registered, so:

- A completion deferred inside it (`wg.Done`, `close(ch)`, `pump.Done`) runs
  before the error is recorded. A consumer that drains to a closed channel and
  then reads `Err()` sees `nil`, which it cannot tell from a clean finish. Put
  the completion on the normal path and in the sink, so exactly one of them
  reaches it. Where the joiner consumes what the sink produces, join by counted
  receive rather than by a `WaitGroup`. No gate catches this one. What
  separates a defect from a correct site is what the joiner reads, and the
  spawn call does not show that. Ask that question in review instead of
  looking for a deferred `Done`.
- A lock must be released from a `defer`. An explicit `Unlock` the panic skips
  leaves the mutex held for the life of the process — a hang where the
  unrecovered panic was a crash, and a hang has no signal but a log line.

The same reasoning applies to anything else the function was going to do after
the point it panicked: a recovery converts "this stops now" into "this stops
now and everything it still owed is never delivered".
[Supervised Goroutine Spawn](architecture/2026-09-15-supervised-goroutine-spawn-direction.md)
records why the package sits beside `errs`, `pump` and `service` rather than
inside one of them.

### What is gated, and what a reviewer has to catch

Two halves of the rule are decidable from the syntax, and the conformance test in
`test/conformance/panic` decides them on every `go test -race ./...`: a `panic`
whose nearest enclosing function declaration is not `Must`- or `must`-prefixed,
and a `go` statement in any package but `src/common/spawn`. It reads first-party
source under `src/` by path rather than importing it, so the nested modules are
checked too, and it skips `_test.go` files and `testdata` fixtures.

It sees the `go` keyword and nothing else. `sync.WaitGroup.Go` is an ordinary
call with no keyword to match — `pump/merge.go` held one until it was converted,
and the gate would not have found it. The next one is a review catch.

Everything else is review, because a cheap approximation of it is worse than
none. Which recover handles a panic is a property of a repository-wide call
graph, and `callOwned` takes a `Runner` function value, so it is not settled
until run time. Whether a caller documents an inherited panic is a judgment
about prose, and a check for the word "panic" is gameable. The benchmark
exemption to the placement rule is granted in review for the same reason; no
site claims it today, so the gate enforces the prefix outright.

### Remedies

An error return, deleting a branch the caller's contract makes unreachable, and
hoisting the evaluation to init time. Adding a new recover boundary is not a
remedy.

Deletion is held to the same standard as the proven answer above: the reason no
caller can reach the branch has to be checkable, and Principle 5 is what licenses
it. `newRing`'s non-positive limit was an error no caller could supply, and an
error return there would have added a shell-channel leak path on a branch that
never executes.

A conversion that replaces a panic with a status the caller must ask for — a
sticky `Err()` field, say — carries the caller-side assertion in the same change,
because `errcheck` cannot see it. A caller that never asks sees a run that ended
exactly like a run that finished.

### What this retired

"A panic that crosses a package boundary is a bug" used to sit in Readability &
spacing. It is retired rather than quietly dropped: `snmp.MustOID` panics into
every generated MIB package and `catalog.MustRegister` into `registrations.go`,
both by design. The successor ban is on *unnamed* panics crossing a package
boundary, which the named clause above already carries.

## Concurrency

- **Never start a goroutine without knowing how and when it stops.** Every
  long-lived goroutine has an owner, a shutdown signal (usually a `context.Context`
  or a closed channel), and a way for the owner to wait for it to exit.
- `context.Context` is always the first parameter, named `ctx`. Pass it; never store
  it in a struct field except in the request-scoped types the stdlib itself blesses.
- Prefer a mutex for guarding state, channels for transferring ownership. Document
  which mutex guards which fields on the struct itself.
- Every exported type's doc comment states whether it is safe for concurrent use.
- `go test -race` is part of the merge gate; code that only passes without `-race`
  does not merge.

## Project layout

```
src/
  protocol/    # protocol clients and schema-language libraries
  common/      # cross-cutting, domain-free foundations
  modules/     # reusable behavior assembled by a host
  services/    # control-plane services
  edge/        # applications designed to run at the edge
generated/     # generated bindings — never edited by hand
spec/          # protobuf, MIB, and YANG sources of truth
```

- Packages are organized by **what they provide**, not by layer (`snmp`, not
  `models`/`utils`/`helpers`). A `util` or `common` *package* is a naming failure —
  find the domain the code belongs to.
- Code that must not be imported from outside its subtree goes under an `internal/`
  directory; the compiler then enforces the boundary.
- Regenerate bindings with their owning generator; a hand edit under `generated/`
  is always a bug. From the repository root, use `buf generate` for
  `generated/go/proto/`, `go generate .` for `generated/go/mib/`, and
  `go run ./src/protocol/yang/cmd/yanggen -update` for `generated/go/yang/`.
  The [protobuf workflow](code-style-proto.md),
  [MIB generator](../src/protocol/snmp/cmd/mibgen/doc.go), and
  [YANG generator](../src/protocol/yang/cmd/yanggen/doc.go) describe their inputs
  and checks.

## Testing

- Plain `testing` package — no assertion frameworks. Failures read
  `t.Errorf("got %q, want %q", got, want)` with the got/want order fixed.
- Table-driven tests with named cases and `t.Run` subtests when three or more cases
  share a shape; a straight sequence of checks is fine below that.
- Mark helpers with `t.Helper()`; use `t.Cleanup` over deferred teardown in helpers.
- Unit tests and package-specific conformance tests live beside the implementation;
  use `package foo_test` when the test should be confined to the exported API.
- Integration suites live in the owning package's `test/integration/`, with their
  fixtures and environment helpers. Keep them in the owner's existing Go module.
  Tests that need real services use testcontainers and are guarded by
  `testing.Short()`.
- Repository-wide protobuf conformance checks live in `test/conformance/proto/`;
  executable tests and fixtures never live in `spec/` or `generated/`.
- A fixture that constructs a protobuf message passes `protovalidate.Validate`
  in the test that builds it. A fixture is a claim that the system could
  receive that message; a package once had twenty tests exercising a request
  the wire would have refused, found only when a fix added a loud failure.
- A test asserts what the fix causes, not what it prevents: an absence has
  more than one source, and a cancelled context supplies it as readily as
  the fix. When the test depends on the system being in a state, assert the
  state before the outcome. Three tests in one plan read as proof and
  asserted nothing, each found only by reverting the fix.
- A self-authored fake peer produces only the sequence the client was coded
  to expect. Seed it with leftover state ahead of the call under test: a
  banner, a retained buffer, an out-of-order message.
- The exported `Config` of a module under `src/modules/` gets one test in a
  package outside that directory. Only that package shows a host can name
  every field's type and construct or implement a value; three fields of one
  module's `Config` were unusable from outside and every internal test passed.
- When correctness rests on an invariant another component holds, the comment
  and a test go on the holding side, at the branch that carries it, and the
  test's name says whose recovery it protects. The depending side is read by
  the person who already knows; the holding side by the person about to break
  it.
- A branch that degrades on error makes the degraded state visible from
  outside, as [`conventions/observability.md`](conventions/observability.md)
  requires, or the feature behind it can be dead with every test passing. A
  swallow that is safe only because the callee cannot fail says so at the
  call site.
- [Test layout](conventions/testing.md) defines ownership, shared helper placement,
  and commands for running the suites.

## Toolchain & enforcement

`gofumpt` (a stricter gofmt) owns formatting; `goimports` semantics (grouped stdlib /
external / `go.aledante.io` imports) are enforced through it. `golangci-lint` v2 is
the single lint entry point, configured in the repo-root `.golangci.yml` — that file
is the source of truth for which linters run and how; the important ones:

- `govet`, `staticcheck` — correctness and modern idioms
- `errcheck` — no silently dropped errors
- `revive` — style, including exported-name doc comments
- `gocritic`, `unconvert`, `unparam`, `misspell` — hygiene
- formatters: `gofumpt`, `goimports`

**The merge gate**; all must pass:

1. `golangci-lint run` (includes the formatter check via `golangci-lint fmt --diff`)
2. `go build ./...`
3. `go vet ./...`
4. `go test -race ./...`

Style and formatting belong to the tools, not to reviewers or agents — never spend a
review comment on something the toolchain already enforces.

## Rules for coding agents

These target the documented failure modes of LLM-written code. They are
non-negotiable and checkable. (See sources below for the empirical basis.)

**Comments & doc comments**
- Do not write comments that restate what the code does. A comment explains *why*.
- Do not write doc comments that paraphrase the signature. State the contract; omit
  comments on trivial unexported helpers entirely.
- Do not leave process narration in code. Delete any comment of the form
  `// First, …`, `// Now we …`, `// Next, …`, `// This handles …`. That is thinking
  scaffolding, not documentation.
- Describe what the code *is*, never its development history or roadmap. A comment
  states the current contract in the present tense. Delete any reference to past or
  in-flight work — "the prior X", "previously", "reworked from", "now refactored",
  "replaces the old …", "deferred until … lands", "v1 / day-one / later phase". When
  code is intentionally a stub, say what it does now, not when the real version
  arrives. A reader has the git history; the source is for the present.
- No process or planning identifiers in code. Requirement numbers, ticket keys, and
  plan IDs (`R4`, `RB6`, `DR11`, and the like) are process exhaust — meaningless to
  a reader of the code. State the rule the code enforces, not its planning reference.
- Do not add banner comments, section dividers, hedging asides, `// TODO`s, or
  emoji — anywhere, including commit messages.

**Scope & simplicity**
- Implement only what was asked. No extra options, interfaces, or "flexibility" for
  a future that may not come.
- No abstraction with a single caller — inline it. No interface with a single
  implementation and no test seam.
- No error handling for conditions that cannot occur given the caller's contract.
- If a 150-line implementation could be 40 lines, write the 40.
- Break a landed API when the break improves the design. Nothing external consumes
  FlowSeer yet, so do not keep an old signature, wrapper, or alias for
  compatibility; change the callers in the same commit. See the same rule for
  schemas in [`code-style-proto.md`](code-style-proto.md).

**Reuse**
- Before writing any helper, search the codebase for an existing one. Implement only
  if nothing equivalent exists.
- Do not copy-paste logic; if it appears twice, extract a function.
- Do not re-implement what the stdlib or an existing dependency already provides.

**Surgical changes**
- Touch only the lines the task requires. Do not reformat or restructure adjacent
  code.
- Do not delete unrelated code. Flag dead code in your reply; don't silently remove
  it.

**Correctness**
- Before calling a library function, confirm it exists in the module's pinned
  version. If unsure, say so in your reply rather than guessing an API.
- Run `golangci-lint run` and `go test -race ./...` on the packages you touched
  before declaring work done.

## Sources

External grounding, researched 2026-08-16 (dated; refresh when revisiting tooling
choices):

- Effective Go — https://go.dev/doc/effective_go
- Go Doc Comments — https://go.dev/doc/comment
- Google Go Style Guide (+ Best Practices, Decisions) —
  https://google.github.io/styleguide/go/
- Uber Go Style Guide — https://github.com/uber-go/guide/blob/master/style.md
- Google Go Style, "Option structure" vs "Variadic options" —
  https://google.github.io/styleguide/go/best-practices.html
- R. Kabir, "Dysfunctional options pattern in Go" / "Configuring options in Go" —
  https://rednafi.com/go/dysfunctional_options_pattern/,
  https://rednafi.com/go/configure_options/
- golangci-lint v2 docs — https://golangci-lint.run/
- gofumpt — https://github.com/mvdan/gofumpt
- godoc-lint (doc-comment linting, in golangci-lint since v2.5) —
  https://github.com/godoc-lint/godoc-lint
- "Investigating The Smells of LLM Generated Code" (arXiv 2510.03029, Oct 2025)
- "AI-Generated Smells" (arXiv 2605.02741, May 2026) — over-commenting,
  over-abstraction, copy-paste invocation logic
- "Code Copycat Conundrum" (arXiv 2504.12608, Apr 2025) — LLM duplication
