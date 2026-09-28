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

`GUARANTEES.md` is parsed with a strict line grammar that rejects any line not
matching an allowed shape. Allowed line shapes are:

- Empty lines.
- Exactly one top-level `# ` document title before any guarantee section.
- Unindented preamble paragraph text before the first guarantee section.
- `## ` guarantee headings giving each guarantee's name. Heading titles retain
  unspaced `#` characters (such as `## Parses C#`), stripping only trailing `#`
  characters preceded by whitespace.
- Exactly one normative sentence per section: a single unindented line
  containing MUST or MUST NOT in the RFC 2119 / RFC 8174 sense.
- One or more scenario bullets starting with `- WHEN ` and containing `THEN`. A
  scenario bullet may continue onto indented continuation lines (indented 1 to 3
  spaces).
- Exactly one column-0 `Proved by:` line listing comma-separated top-level Go
  test names. The list may continue across following indented lines (1 to 3
  spaces) until an empty line, a new heading, or another `Proved by:` line. A
  `Proved by:` list ending with a trailing comma is an error.

Fenced code blocks (``` or ~~~), setext underlines (`---`), indented code blocks
(4 spaces or tabs), numbered lists (`1. ...`), and unallowed heading levels
(`###`) fail closed as unknown line formats. Indented `Proved by:` lines fail as
unknown line format and never count as citations.

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
  reported by `go list -json .` (invoked with `GOWORK=off` and read-only `-mod`).
  `go list` excludes `_foo_test.go` and `//go:build ignore` files by construction.
- Discovered test files are scanned with a Go token scanner that ignores
  whitespace, comments (`//` and `/* */`), and strings (interpreted, raw, and rune
  literals).
- Function declarations qualify as tests when the name starts with `Test`, is
  not `TestMain`, and its fifth character (if present) is not Unicode lowercase
  (`!unicode.IsLower`, rejecting `Testé`).
- Parameter lists support `*testing.T`, `(*testing.T)`, or aliased imports
  `*<pkg>.T` / `(*<pkg>.T)`, optional parameter names, and multiline parameter
  layouts with optional trailing commas before `)`.
- Citations do not search subdirectories. Subdirectories are separate Go
  packages. Searching subdirectories would allow a subpackage test to mask the
  deletion of a parent package's test.
- Subtests (`t.Run`) cannot be cited because subtest names are runtime strings
  rather than static symbols.
- A guarantee MUST claim only what its cited tests assert. The verifier proves
  that cited test symbols exist; it cannot verify test semantics. A guarantee
  statement broader than its tests creates unproven prose.
