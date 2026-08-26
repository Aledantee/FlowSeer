---
name: Code & Comment Style
last_updated: 2026-08-16
---

# FlowSeer — Code & Comment Style

The authoritative style guide for FlowSeer's Go. It binds **human contributors and
coding agents equally**. Mechanical rules (formatting, import order, lint) are owned
by the toolchain and enforced in CI; this document owns the judgement rules a linter
cannot check — what a doc comment must say, when a comment earns its place, error and
concurrency discipline, and how to keep agent-written code clean.

Target runtime: **Go 1.26+**. The baseline is [Effective Go], the [Go doc-comment
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
   longtime Go reader expects.
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
struct that guards an invariant).

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
- No naked returns outside trivial few-line functions. No `panic` for ordinary
  errors — a panic that crosses a package boundary is a bug.
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
  readable is fine — most of `src/common/snmp` does exactly that. Never attach or
  interpolate raw secret material — attach a length and a protocol name instead.
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
  backend/     # collector/analysis services
  common/      # shared libraries (e.g. snmp)
  edge/        # edge agents
  frontend/    # UI (own toolchain; this document does not govern it)
generated/     # buf-generated code — never edited by hand
spec/          # protobuf + MIB sources of truth
```

- Packages are organized by **what they provide**, not by layer (`snmp`, not
  `models`/`utils`/`helpers`). A `util` or `common` *package* is a naming failure —
  find the domain the code belongs to.
- Code that must not be imported from outside its subtree goes under an `internal/`
  directory; the compiler then enforces the boundary.
- Anything under `generated/` is regenerated with `buf generate`; a hand edit there
  is always a bug.

## Testing

- Plain `testing` package — no assertion frameworks. Failures read
  `t.Errorf("got %q, want %q", got, want)` with the got/want order fixed.
- Table-driven tests with named cases and `t.Run` subtests when three or more cases
  share a shape; a straight sequence of checks is fine below that.
- Mark helpers with `t.Helper()`; use `t.Cleanup` over deferred teardown in helpers.
- Go behavior tests live in the package they test; use `package foo_test` when the
  test should be confined to the exported API. Cross-repository schema and layout
  checks live in `src/common/protoconformance/`, with fixtures under its `testdata/`;
  they never live in `spec/` or `generated/`.
- Integration tests that need real services use testcontainers and are guarded by
  `testing.Short()`.

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
