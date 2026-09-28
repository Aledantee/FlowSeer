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

## Block format and line grammar

`GUARANTEES.md` is parsed with a strict line grammar: every line must match an
allowed shape, and any other line fails the check with `unknown line format`.
Allowed line shapes are:

- Empty lines.
- At most one top-level `# ` document title, before any guarantee section.
- Preamble paragraph text before the first guarantee section.
- `## ` guarantee headings giving each guarantee's name. Heading titles retain
  unspaced `#` characters (such as `## Parses C#`), stripping only trailing `#`
  characters preceded by whitespace.
- Exactly one normative sentence per section: a text line containing MUST or
  MUST NOT in the RFC 2119 / RFC 8174 sense. A section with no normative line,
  or more than one, fails.
- One or more scenario bullets starting with `- WHEN ` and containing `THEN`. A
  scenario bullet may continue onto indented continuation lines (indented 1 to 3
  spaces).
- Exactly one column-0 `Proved by:` line listing comma-separated top-level Go
  test names. The list may continue across following indented lines (1 to 3
  spaces) until an empty line, a new heading, or another `Proved by:` line.

A text line is column-0 text whose first character is a Unicode letter or digit,
or a lone backtick that does not open a fence, and which is not an ordered-list
marker (`1.` or `1)` followed by a space). A scenario continuation is a text
line indented 1 to 3 spaces by the same rule. Each `Proved by:` item is a Go
identifier, optionally wrapped in one pair of backticks; `,,`, an item of only
backticks, and a list ending in a comma are errors.

Because the shapes are positive, any line that would open another CommonMark
block is rejected: a fence, a setext underline, an indented code block (4 spaces
or a tab), a thematic break, a block quote, a bullet or ordered-list item, an
HTML block, a link reference definition, a GFM table, or an ATX heading of any
level. An indented `Proved by:` line fails as unknown line format and never
counts as a citation.

### Example block

```markdown
## Host-key verification has no default

Dial MUST refuse Options specifying neither or both of HostKeySHA256 and InsecureIgnoreHostKey, and MUST refuse a host-key mismatch on dial.

- WHEN neither HostKeySHA256 nor InsecureIgnoreHostKey is set, or both are set THEN Dial returns an error refusing the connection.
- WHEN HostKeySHA256 does not match the remote host's key fingerprint THEN Dial returns an error refusing the connection.

Proved by: TestHostKeyCallbackRequiresExplicitVerification, TestDialOptionsRequireExplicitHostKeyVerification, TestDialHostKeyMismatchRefused
```

## Citation rules and test bounds

- Cited tests resolve from the package's `TestGoFiles` and `XTestGoFiles` as
  reported by `go list -json .`, run with `GOWORK=off` and any inherited `-mod=`
  removed so Go's read-only default applies. `go list` excludes `_foo_test.go`
  and `//go:build ignore` files by construction.
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
  dot import (`*T`), an aliased `*<pkg>.T`, optional parameter names, and
  multiline layouts with optional trailing commas before `)`. A parenthesized
  type such as `(*testing.T)` is not accepted, matching Go's AST check.
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
