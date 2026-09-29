# Package guarantees

A Go package's guaranteed behavior is collected in `GUARANTEES.md` beside its
`README.md`. Each guarantee describes an invariant or contract that callers
rely on and cites the top-level Go test that proves it.

## Scope and placement

`GUARANTEES.md` lives only in Go package directories (alongside `doc.go` or the
package's `.go` source files and `README.md`). It does not belong under
`spec/proto/`, `frontend/web/`, or skill directories. `spec/proto/` contains
only protobuf source files (`AGENTS.md`, Hard boundaries), and the citation
mechanism resolves Go test functions.

A guarantee specifies observable behavior of the exported package API: outcomes,
invariants, state transitions, and error conditions callers depend on. Internal
implementation details, private helper contracts, and transient code layout do
not belong in `GUARANTEES.md`.

## Rollout

Rollout is on touch. On-touch rollout starts once `plan`, `implement`, and
`review` carry guarantee changes; until then, only the pilot package holds a
`GUARANTEES.md`. Once active, a package gains a `GUARANTEES.md` only when a plan
touches that package's behavior. The file initially records only the contracts
the plan introduces or alters. Backfilling guarantees across untouched packages
is avoided because ungrounded backfills create large diffs without review
context.

## Relation to README and doc comments

`GUARANTEES.md`, `README.md`, and source doc comments divide responsibilities:

- `GUARANTEES.md` is the normative catalog of guaranteed behavior. It states the
  rules in RFC 2119 / RFC 8174 terms and binds each rule to a test function.
- `README.md` explains the package design, concepts, and reasons to a person
  learning the package. The README explains behavior and links to the relevant
  guarantee in `GUARANTEES.md` rather than restating the normative rule.
- Go doc comments on exported symbols remain the primary contract on the code
  itself (`docs/code-style.md`). Doc comments sit on the declarations and are
  reviewed alongside code changes. When behavior changes, both doc comments and
  `GUARANTEES.md` change in the same commit.

## Block format and document structure

`tools/check-guarantees` parses `GUARANTEES.md` with goldmark into an abstract
syntax tree and evaluates the document's block structure and visible text. The
checker fails closed where goldmark is known to disagree with CommonMark 0.31.2,
including lowercase HTML declarations. Any unexpected block kind fails the check.

At document scope:

- At most one top-level `# ` document title, placed before any guarantee section.
- Preamble paragraphs placed before the first guarantee section.
- At least one `## ` guarantee section.
- Unique `## ` guarantee headings. Headings are compared after decoding HTML
  character entities (so `## A &amp; B` and `## A & B` collide as duplicates).
  Unspaced `#` characters are preserved (such as `## Parses C#`), stripping only
  trailing `#` characters preceded by whitespace.

Within each guarantee section, exactly three blocks must appear in order:

1. Exactly one normative paragraph: visible paragraph text containing whole-word
   MUST or MUST NOT in the RFC 2119 / RFC 8174 sense. A section lacking a
   normative statement reports `has no normative MUST sentence`; a section with
   multiple normative paragraphs reports `has more than one normative sentence`.
   Placing blocks out of order fails closed with an unknown block kind error.
2. Exactly one bullet list: an unordered list using the `-` marker only (other
   markers like `*` or `+` fail as an unknown block kind) containing one or more
   `- WHEN … THEN …` scenario items. Each list item must contain exactly one
   paragraph or text block; nested blocks (nested lists, code blocks, block
   quotes, headings, HTML blocks) fail closed as unknown block kinds at the
   nested block's line. A list item whose visible text contains `Proved by:`
   fails closed with `unknown block kind: list item`.
3. Exactly one `Proved by:` paragraph: a paragraph beginning with `Proved by:`
   followed by comma-separated Go test identifiers, optionally wrapped in
   backticks. Trailing commas, empty items, duplicate `Proved by:` paragraphs,
   and invalid identifiers fail.

All text evaluations (headings, normative MUST/MUST NOT clauses, WHEN/THEN clauses,
and `Proved by:` test citations) inspect visible text derived directly from
allowed CommonMark AST inline nodes: text (with HTML entity references and
backslash-escaped punctuation decoded), soft and hard line breaks, code spans
(with newlines normalized to single spaces), and emphasis. Every other inline
node kind (raw HTML, images, links, autolinks, and extensions) fails closed with
an unknown inline kind error. No HTML rendering or tag stripping is performed.
An unescaped literal `<` before an ASCII letter, `/`, `!`, or `?` in a text node
also fails as `unknown inline kind: raw html`, including in preamble paragraphs.
Write a literal `<` before a letter as `\<` or inside a code span.

Any block kind outside this grammar fails closed with `unknown block kind`:
fenced code blocks, indented code blocks, thematic breaks, block quotes, HTML
blocks, ordered lists, and unallowed heading levels (`###`).

### Example block

```markdown
## Host-key verification has no default

Dial MUST refuse Options specifying neither or both of HostKeySHA256 and InsecureIgnoreHostKey, and MUST refuse a host-key mismatch on dial.

- WHEN neither HostKeySHA256 nor InsecureIgnoreHostKey is set, or both are set THEN Dial returns an error refusing the connection.
- WHEN HostKeySHA256 does not match the remote host's key fingerprint THEN Dial returns an error refusing the connection.

Proved by: TestHostKeyCallbackRequiresExplicitVerification, TestDialOptionsRequireExplicitHostKeyVerification, TestDialHostKeyMismatchRefused
```

## Citation rules and test bounds

- When passed explicit paths, the checker looks only in each path's own directory
  for `GUARANTEES.md` (a directory path checks that directory; a file or
  nonexistent path checks its parent directory). It does not fall back to parent
  directories or traverse subdirectories.
- Cited tests resolve from the package's `TestGoFiles` and `XTestGoFiles` as
  reported by `go list -json .`, run with `GOWORK=off` and `-mod=readonly` on
  the command line. The flag overrides any `-mod` in `GOFLAGS` or the `go env`
  file, so a verifier run never rewrites `go.mod` or `go.sum`. `go list`
  excludes `_foo_test.go` and `//go:build ignore` files by construction.
- When `go list` fails (no `go.mod`, no Go files, a missing `go`, bad JSON), the
  check reports one `<path>:1: go list failed in <pkg>: ...` error for the file
  and skips the per-citation existence errors, rather than reporting every
  citation as missing.
- Discovered test files are scanned with a Go token scanner that ignores
  whitespace, comments (`//` and `/* */`), and strings (interpreted, raw, and rune
  literals).
- Function declarations qualify as tests when the name starts with `Test` and
  its fifth character (if present) is not Unicode lowercase (Go's
  `!unicode.IsLower`, category `Ll`, so `Testé` is rejected). `TestMain` is
  treated like any other name: `func TestMain(m *testing.M)` is excluded because
  its parameter is not `*T`, while `func TestMain(t *testing.T)` resolves.
- Parameter lists are the forms Go accepts: `*testing.T` through an ordinary or
  dot import (`*T`), an aliased `*<pkg>.T`, optional parameter names, an empty
  `()` result list, and multiline layouts with optional trailing commas before
  `)`. A parenthesized, array, or slice type such as `(*testing.T)`,
  `[]*testing.T`, or `[4]*testing.T`, a second parameter, and a non-empty
  result list are not accepted, matching Go's AST check. If any top-level
  `Test...` function in a package has an invalid test signature, the checker
  reports `<GUARANTEES path>:1: <pkg>/<file>:<line>: <TestName> has invalid test signature`
  and fails test resolution for the package so broken test files cannot pass
  citations.
- Only test files in the default build for the host platform are citable. An
  untagged `go list` puts `//go:build <tag>` files and files for another GOOS in
  `IgnoredGoFiles`, so a test behind a build tag does not resolve; see
  [An untagged go list misses imports made only from build-tagged files](../solutions/conventions/go-list-deps-misses-imports-behind-build-tags.md).
- Citations do not search subdirectories. Subdirectories are separate Go
  packages. Searching subdirectories would allow a subpackage test to mask the
  deletion of a parent package's test.
- Subtests (`t.Run`) cannot be cited because subtest names are runtime strings
  rather than static symbols.
- A guarantee MUST claim only what its cited tests assert. The verifier proves
  that cited test symbols exist; it cannot verify test semantics. A guarantee
  statement broader than its tests creates unproven prose.
