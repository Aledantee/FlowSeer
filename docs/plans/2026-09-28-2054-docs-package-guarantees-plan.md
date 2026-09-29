---
title: Package Guarantees - Plan
type: docs
date: 2026-09-28
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: implemented
review: rework
execution: mixed
---

# Package Guarantees - Plan

> Implemented. 7 units, 2026-09-28T19:09:14Z to 2026-09-29T16:44:30Z.
> Pilot outcome: 4 of 7 README contracts were proved by existing tests as-is,
> 2 needed a new test (session closure on peer disconnection and prompt
> earliest-match: TestScanPromptEarliestMatchAndTieOrder and
> TestRunPromptEarliestMatchInStream), and 1 needed a test change (output cap
> truncation without buffer ring saturation). The host-key guarantee also cites
> the Dial-level test because the helper-level one does not call Dial. The stop
> condition held (did not trigger); the convention and check format are
> validated.
> Redesign outcome: the checker parses GUARANTEES.md into a CommonMark AST via
> goldmark (tools/check-guarantees), enforcing allowed block structures (one
> normative MUST/MUST NOT paragraph, one bullet list of - WHEN ... THEN ...
> scenarios, one Proved by: paragraph) and failing closed on unallowed blocks
> (code fences, underlines, indented code, blockquotes, HTML blocks, ordered
> lists, extra headings). It decodes HTML character entities in headings, parses
> multiline code spans without hiding clauses, excludes raw HTML from visible
> text, and resolves top-level package tests through go list -mod=readonly -json .
> and a Go token scanner validating test signatures (rejecting packages with
> invalid Test signatures). Obsolete Python checker scripts were removed and
> verify-change was updated to invoke the Go checker.

## Goal

A Go package's current guaranteed behavior has one normative home: a
`GUARANTEES.md` beside its `README.md`, where each guarantee names the test
that proves it and the verifier fails when that test is gone. This plan
lands the convention, the verifier check, and a pilot on `src/protocol/ssh`.
A follow-up plan, written once the pilot is judged, makes `plan`,
`implement`, and `review` carry guarantee deltas. The idea is borrowed from
OpenSpec's living specs and change deltas
(<https://github.com/Fission-AI/OpenSpec>, `docs/concepts.md`); the tool
itself is not adopted. Stop condition: if the pilot shows that most README
contract statements have no test that asserts them, the file would be
unproven prose, and the format or the check is redesigned before the
follow-up plan is written.

## Decisions

- The checker reads `GUARANTEES.md` through a conforming CommonMark parser
  and checks the parsed document, not the source lines (decided by the
  user, 2026-09-29, after the second review ended `rework`). Headings,
  normative sentences, WHEN/THEN bullets, and `Proved by:` lines are taken
  from the parser's block tree and rendered text, so markup a reader never
  sees (HTML comments, escapes, code spans across lines, entity-equal
  headings) cannot count or hide a clause. The parser is goldmark
  (`github.com/yuin/goldmark`, MIT, pure Go), run from a small Go command,
  because the check already needs Go on PATH for `go list` and the repo
  manages no Python dependencies. Why: two review passes of hand-matched
  CommonMark leaked first at the block level, then across lines, then
  inside code spans.

- Where the Go command lives: `tools/check-guarantees/` (with `main.go`,
  `check.go`, and `check_test.go`). Why: the repository layout in
  `AGENTS.md:126-133` and `README.md:57-67` reserves `src/` exclusively for
  product runtime subtrees (`protocol/`, `common/`, `modules/`, `services/`,
  `edge/`), while developer and verification tooling lives under `tools/`
  (matching `tools/test/service-otel-integration.sh`). Placing the command in
  `tools/check-guarantees/` satisfies the conformance gates under
  `test/conformance/`: `test/conformance/dependencies/no_as_test.go:11-33`
  forbids `go.aledante.io/as` and `ae` (goldmark and stdlib imports comply),
  and `test/conformance/panic/panic_policy_test.go:37-38` and
  `test/conformance/errs/errs_policy_test.go:28-29` inspect only `src/`
  (`srcRoot := filepath.Join(root, "src")`). It satisfies
  `docs/code-style.md:214-220` (constructing errors via `src/common/errs`),
  `docs/code-style.md:250-252` (process exit codes via `errs.ExitCode(err)`),
  `docs/code-style.md:45-50` (doc comments on exported types), and
  `docs/code-style.md:449` (plain `testing` package without assertion
  frameworks). Standard Go toolchain commands (`go list ./...`,
  `go test -race ./...`, `golangci-lint run`) naturally discover and test
  `tools/check-guarantees/`, which they do not do for dot-directories like
  `.agents/`. Adding `github.com/yuin/goldmark` to `go.mod` updates `go.sum`.

- How the Go command is invoked (Python vs Go tradeoff): The Go command in
  `tools/check-guarantees` replaces the Python checker (`check-guarantees.py`
  and `test_check_guarantees.py`) entirely, invoked directly by
  `verify-change.sh:518-520` via `go run ./tools/check-guarantees` (`--all` or
  paths). Tradeoff: Having `check-guarantees.py` invoke a Go parser command via
  subprocess would preserve the existing Python test harness and avoid
  modifying `verify-change.sh`, but it incurs double subprocess overhead
  (`verify-change.sh` -> Python -> `go run` -> stdout JSON -> Python ->
  `go list`), requires maintaining an IPC JSON bridge, and leaves Python
  orchestrating Go in a codebase that manages no Python dependencies.
  Replacing the Python checker unifies the gate in Go, eliminates IPC
  overhead, aligns with FlowSeer's pure-Go toolchain preference, and lets
  the test suite run under `go test -race ./tools/check-guarantees/...`.
  `verify-change.sh` is merge-gate configuration (`AGENTS.md:37-39`), so the
  unit modifying it keeps the guardrail-review rule.

- Preserved mechanics: `go list -mod=readonly -json .` test discovery in the
  package directory with `GOWORK=off` (`check-guarantees.py:355-386`,
  `verify-change.sh:305-308`), the Go test token scanner rules
  (`check-guarantees.py:276-343`) recognizing top-level `Test` functions where
  the 5th character is `!unicode.IsLower` and supporting `*testing.T`,
  `*<pkg>.T`, anonymous parameters, multiline parameters, and empty returns,
  the one-MUST-sentence rule (`check-guarantees.py:432-440, 597-599`), and the
  error format `<path>:<line>: <message>` (`check-guarantees.py:429-466`) stay
  as they are. Line numbers are derived directly from goldmark's AST node line
  and segment offsets against the source buffer.

- Hand-rolled grammar rules vs rules on the parsed tree:
  Hand-rolled line rules become obsolete and are omitted: `strip_code_spans`
  (`check-guarantees.py:157-193`), `has_hidden_markup`
  (`check-guarantees.py:196-204`), `is_text_line`
  (`check-guarantees.py:226-243`), `is_blank_text`
  (`check-guarantees.py:206-209`), `starts_paragraph`
  (`check-guarantees.py:212-224`), regexes `ORDERED_LIST_RE` and
  `THEMATIC_BREAK_RE` (`check-guarantees.py:23-24`), line indentation checks
  (`check-guarantees.py:493-496`), heading trailing `#` regex
  (`check-guarantees.py:541`), and continuation line tracking
  (`check-guarantees.py:468-472, 503-524`).
  The rules on the parsed tree enforce:
  - Document level: At most one `# ` title (`ast.KindHeading` with
    `Level == 1`), only before sections; preamble paragraphs
    (`ast.KindParagraph`), only before first section; at least one `## `
    guarantee section (`Level == 2`); unique `## ` headings with HTML entity
    decoding (`&amp;` -> `&`) and unspaced `#` preserved; any unexpected block
    at root fails closed.
  - Section level: Exactly three allowed block kinds:
    1. Exactly one normative paragraph (`ast.KindParagraph` containing
       whole-word MUST or MUST NOT in visible text).
    2. Exactly one bullet list (`ast.KindList` where `!IsOrdered()`) containing
       `- WHEN ... THEN ...` scenario items (`ast.KindListItem`).
    3. Exactly one `Proved by:` paragraph (`ast.KindParagraph` starting with
       `Proved by:`), containing comma-separated Go test identifiers,
       disallowing trailing commas.
    Any other block kind (`ast.KindCodeBlock`, `ast.KindFencedCodeBlock`,
    `ast.KindThematicBreak`, `ast.KindBlockquote`, `ast.KindHTMLBlock`, ordered
    `ast.KindList`, unallowed headings `###`, unindented non-normative
    paragraphs, extra paragraphs) fails closed as an unknown block kind.

- Review findings and regression tests:
  The three open findings of the second review become regression cases:
  1. An escaped backtick followed by `<!-- MUST -->` must not count as MUST:
     `\` ` is parsed as literal text (`ast.KindText`) and `<!-- MUST -->` as
     raw HTML (`ast.KindRawHTML`). Visible text does not contain MUST; fails
     with no normative MUST sentence.
  2. A code span opening on a `- WHEN` line and closing on the next must not
     hide THEN: parsed as a single `ast.KindListItem` containing
     `ast.KindCodeSpan` and trailing text containing `THEN`. Passes without
     hiding `THEN`.
  3. `## A &amp; B` and `## A & B` are duplicate headings: HTML character
     references in headings are decoded, yielding identical heading text
     `A & B`. Fails as duplicate heading.
  The two low findings of that review are closed:
  1. A package where one test file has a badly-signed test (go test fails the
     package) while the checker still resolves the good one: The Go test
     scanner validates signatures for all top-level functions matching
     `Test...` (`!unicode.IsLower` on 5th char); if any function named
     `Test...` in any discovered test file has an invalid test signature (wrong
     parameter count, wrong parameter type, return value), report a malformed
     test signature error, failing package test resolution so citations cannot
     resolve.
  2. The hidden-markup check on WHEN continuation lines has no test that fails
     without it: Under CommonMark AST, continuation lines are absorbed into
     the `ListItem` node and raw HTML is excluded from text; this hand-rolled
     check (`check-guarantees.py:517-519`) is obsolete and omitted.
  All 73 checker test cases from `test_check_guarantees.py` preserve their
  intent in Go table-driven unit tests in `tools/check-guarantees/check_test.go`.

- The checker reads `GUARANTEES.md` with a strict line grammar and resolves
  tests through `go list` (decided by the user, 2026-09-28, after review
  ended `rework`). Every line must be one of the kinds the format allows,
  or the check fails on it; cited tests resolve from the package's
  `TestGoFiles` and `XTestGoFiles` as `go list` reports them, scanned with
  the Go lexer. Why: three review rounds of patching a CommonMark subset
  kept failing open (a setext underline, an indented code block, `#` in a
  heading), and a file lookup counted `_foo_test.go` and
  `//go:build ignore` files Go never compiles. A grammar that rejects
  every unknown line fails closed by construction. The check needs Go on
  PATH, as the lexer's oracle test already does. Landed in U5; superseded by
  the CommonMark AST parser decision above.
- `verify-change.sh` classifies `.agents/*` paths as tooling (`hook_tooling=true`),
  so `--base main` and direct skill script edits run `test_check_guarantees.py`
  and other skill unit tests. Why: `.claude/skills` symlinks `.agents/skills`,
  and git tracks files under `.agents/skills/`. Without `.agents/*` in
  `verify-change.sh`'s classification patterns (lines 255 and 288), diffs
  touching `.agents/skills/...` skipped the Python unit tests unless `--full` was
  passed. Because `verify-change.sh` is merge-gate configuration, any unit
  touching it requires guardrail review. Landed in U4.

- Guarantees live in `GUARANTEES.md` beside the package's `README.md`, not
  in the README. Why: the README is written for a person learning the
  package and explains why; a list of normative blocks with test citations
  reads as machine text there and duplicates the prose.
- The README explains a behavior and links to its guarantee; it does not
  restate the rule as a rule. Symbol doc comments keep stating their
  symbol's contract, as `docs/code-style.md` requires; a guarantee may
  overlap one, and both change in the diff that changes the behavior. Why:
  `docs/README.md` forbids a second source of truth, and the doc comment
  sits on the code it describes, so it is reviewed with it, while README
  prose is not.
- Only Go package directories carry a `GUARANTEES.md`; `spec/proto/`,
  `frontend/web/`, and skill scripts do not. Why: the citation rule resolves
  Go test functions, and `spec/proto/` is source-only (`AGENTS.md`, Hard
  boundaries; `tools/hooks/pre-tool-policy.sh` denies other files there).
- A guarantee's identity is its `##` heading. A later delta matches on it,
  the way OpenSpec matches a requirement by name. Why: headings are what a
  reader and a diff already see, and a rename shows as a removal plus an
  addition.
- Block format: a `##` heading, one normative sentence using MUST or
  MUST NOT in the RFC 2119 sense as clarified by RFC 8174, one or more
  `- WHEN … THEN …` scenario bullets, and one `Proved by:` line naming
  top-level Go test functions. Why: scenarios map one-to-one onto test
  cases, and the `Proved by:` line is the only part a script needs to
  parse. GIVEN is dropped because a WHEN clause carries the precondition in
  every README contract read for this plan.
- Cited tests resolve to `func Test…(` in a `*_test.go` file in the same
  directory as `GUARANTEES.md`, not in subdirectories. Subtests are not
  citable. Why: Go test names are unique only within a package, so a
  recursive lookup would let a subpackage's `TestX` hide the deletion of
  the parent's; a subtest name is a runtime string, not a symbol.
- A guarantee claims only what its cited tests assert. Why: the check
  proves a test exists, not what it proves; a sentence broader than its
  tests is the unproven prose the stop condition warns about.
- The rule is a convention doc (`docs/conventions/guarantees.md`), not a
  direction record. Why: it describes how documentation and tests are
  shaped, which `docs/README.md` sends to an existing convention or a new
  one under `conventions/`; `docs/architecture/` holds system direction.
- The verifier check fails the run; it does not only report. Why: a
  missing proof is a mechanical fact, and `docs/agent-steering.md` puts a
  checkable rule in enforcement rather than prose.
- Rollout is on touch, once the follow-up plan lands: a package gains a
  `GUARANTEES.md`, holding only the behavior a plan changes, when a plan
  first changes its behavior. Why: a backfill is a large diff with no
  review context. The convention states this now so the pilot is not read
  as a mandate to backfill.

## Requirements

1. Every `GUARANTEES.md` the verifier checks has unique `##` headings, and
   each section has at least one `- WHEN` bullet containing `THEN` and
   exactly one `Proved by:` line. Example: a section with no `Proved by:`
   line fails with `src/protocol/ssh/GUARANTEES.md:14: "Output is capped"
   has no Proved by: line`.
2. Every test a `Proved by:` line names exists as a top-level test function
   in the file's directory. Example: after `TestRunOutputCapTruncates` is
   renamed in `src/protocol/ssh/command_test.go`, `verify-change.sh --
   src/protocol/ssh/command_test.go` fails, naming the guarantee and the
   missing test, although `GUARANTEES.md` itself is unchanged.
3. The check runs for the `GUARANTEES.md` in each changed path's
   directory, whether or not the changed path still exists, and for all of
   them under `--full`. Example: a change to `src/protocol/ssh/scan.go`
   checks `src/protocol/ssh/GUARANTEES.md`; deleting
   `src/protocol/ssh/command_test.go` checks it too; a change to
   `docs/README.md` checks none.
4. `src/protocol/ssh/GUARANTEES.md` exists, passes the check, and each
   README contract section links to its guarantees. Example: "Host-key
   verification has no default" in the README links to the guarantee of the
   same name, which cites `TestHostKeyCallbackRequiresExplicitVerification`.
5. `verify-change.sh` classifies `.agents/*` paths as tooling (`hook_tooling=true`).
   Changes to agent skill scripts run shellcheck, hook tests, and Python test
   discovery. Example: `verify-change.sh -- .agents/skills/verify-change/scripts/test_check_guarantees.py`
   executes `test_check_guarantees.py` rather than reporting zero gates selected.
6. `check-guarantees.py` rejects every line not matching an allowed grammar
   shape (blank line, `# ` title, preamble paragraph, `## ` heading, normative
   MUST/MUST NOT sentence, `- WHEN ... THEN ...` bullet, column-0 `Proved by:` line,
   or indented citation continuation line). Example: a line containing `---`, a
   numbered list `1. item`, or an indented code block `    Proved by: TestA` fails
   with `GUARANTEES.md:12: unknown line format`.
7. Guarantee heading titles retain unspaced `#` characters, stripping only
   trailing `#` sequences preceded by whitespace. Example: `## Parses C#` parses
   as guarantee heading `Parses C#` and does not collide with `## Parses C`.
8. Cited tests resolve exclusively from the package's `TestGoFiles` and
   `XTestGoFiles` reported by `go list -json`. Example: a test defined in
   `_foo_test.go` or in a file carrying `//go:build ignore` fails with
   `GUARANTEES.md:5: "Capped" cites test "TestIgnored" which does not exist in src/pkg`.
9. The Go test scanner recognizes top-level `Test` functions across multiline
   signatures, anonymous parameters, and aliased `testing` imports, while
   rejecting Unicode lowercase test names. Example: `func TestValid(\n  *tst.T,\n)`
   resolves `TestValid`, while `func Testé(t *testing.T)` fails resolution.
10. Every branch in `check-guarantees.py`'s lexer and line grammar has a test in
    `test_check_guarantees.py` that fails when that branch is inverted. Example:
    inverting the check for unspaced `#` in headings causes
    `test_heading_with_unspaced_hash` to fail.
11. `docs/conventions/guarantees.md` documents the strict line grammar and `go list`
    test resolution rules, and `src/protocol/ssh/GUARANTEES.md` passes the new
    checker. Example: `check-guarantees.py src/protocol/ssh/GUARANTEES.md` exits 0.
12. `tools/check-guarantees` parses `GUARANTEES.md` through goldmark
    (`github.com/yuin/goldmark`) into a CommonMark AST, deriving 1-based source
    lines from node text segments. Block kinds outside the allowed set (one
    normative paragraph, one bullet list of `- WHEN ... THEN ...` items, one
    `Proved by:` paragraph per section) fail closed with `<path>:<line>: unknown
    block kind`. Example: a guarantee section containing a fenced code block
    ` ```go ` fails with `GUARANTEES.md:14: unknown block kind: fenced code block`.
13. Text content extracted from the AST ignores raw HTML nodes
    (`ast.KindRawHTML`, `ast.KindHTMLBlock`), unescapes HTML character entities
    in headings (`&amp;` -> `&`), and correctly evaluates code spans spanning
    multiple lines. Examples: `\` ` <!-- MUST -->` fails with no normative MUST
    sentence; `- WHEN ` `code\nmore` ` THEN ...` passes; `## A &amp; B` and
    `## A & B` fail as duplicate headings.
14. The Go test scanner reports an error if any function named `Test...` in a
    package's test files has an invalid test signature, preventing packages
    with broken tests from passing citation resolution. Example: a package
    containing `func TestGood(t *testing.T)` and `func TestBad(x int)` fails
    resolution with `bad_test.go:3: TestBad has invalid test signature`.
15. `verify-change.sh` invokes `go run ./tools/check-guarantees` (`--all` under
    `--full`, changed paths otherwise) and passes guardrail review before
    commit. `tools/check-guarantees` replaces `check-guarantees.py` and
    `test_check_guarantees.py`. Example: `verify-change.sh --
    src/protocol/ssh/command_test.go` executes `go run ./tools/check-guarantees`
    and passes.
16. `docs/conventions/guarantees.md` and `.agents/skills/verify-change/SKILL.md`
    document the CommonMark block-based grammar and Go checker command.
    `src/protocol/ssh/GUARANTEES.md` passes. Example: `go run
    ./tools/check-guarantees src/protocol/ssh/GUARANTEES.md` exits 0.

## Out of scope

- The OpenSpec CLI, its `openspec/` tree, and its `/opsx:*` commands.
- Changes to `plan`, `implement`, and `review` so plans carry guarantee
  deltas and units apply them. That is the follow-up plan (Open
  questions), written after the pilot, since changing the skills first
  would require a format the pilot has not tested.
- Backfilling guarantees for any package except `src/protocol/ssh`.
- An `AGENTS.md` Conventions entry for the new doc. `AGENTS.md` is a policy
  surface; `docs/README.md`'s map links the doc.
- Checking that a cited test asserts its scenario. The check proves only
  that the test exists; `review` judges the rest.
- Changes to `land`, `compound`, `drive`, or `next`, whose scripts parse no
  plan section beyond the frontmatter and unit fields.
- Updating `--print-selection` output assertions for `hook_tooling` in
  `tools/hooks/tests/run.sh` (policy surface; recorded under Open questions).

## Units

### U1. Guarantees convention

Files: `docs/conventions/guarantees.md`, `docs/README.md`
After: none
Change: `docs/conventions/guarantees.md` states the file name and place
(Go package directories only), what a guarantee is (behavior of the
exported surface a caller relies on, not an implementation detail), the
block format from Decisions with a complete worked block, same-directory
citation resolution, the rule that a guarantee claims only what its tests
assert, the relation to the README and to symbol doc comments, and the
on-touch rollout. `docs/README.md` gains a map row for the convention; its
"Authority and freshness" paragraph on README and source as one contract
names `GUARANTEES.md` as the normative list of guaranteed behavior, with
the README explaining it; and its "Package-level behavior belongs beside
the package" sentence names both files.
Tests: none; prose only. The verifier's markdown link check covers the new
links.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/conventions/guarantees.md docs/README.md`

### U2. Verifier check

Files: `.claude/skills/verify-change/scripts/check-guarantees.py`, `.claude/skills/verify-change/scripts/test_check_guarantees.py`, `.claude/skills/verify-change/scripts/verify-change.sh`, `.claude/skills/verify-change/SKILL.md`
After: U1
Change: `check-guarantees.py` takes changed paths (or `--all`), selects the
`GUARANTEES.md` Requirement 3 names without requiring the changed path to
exist, and exits non-zero listing every violation of Requirements 1 and 2
as `<path>:<line>: <message>`. `verify-change.sh` runs it next to
`check-plan-status.py`, with `--all` under `--full` and the changed paths
otherwise. `SKILL.md` names the gate and links the convention.
`verify-change.sh` is part of the merge gate and no hook prompts for it,
so this unit runs in the coordinating session, and its diff goes to an
`independent-reviewer` as a guardrail review before the unit is committed.
Tests: `test_check_guarantees.py` loads the script with
`importlib.util.spec_from_file_location`, as
`.claude/skills/drive/scripts/test_plan_state.py` does, and builds fixture
trees in a temp directory: a valid file passes; a missing `Proved by:`
line, a duplicated heading, a section without a WHEN/THEN bullet, and a
cited test that does not exist each fail with the expected `path:line`; a
test defined only in a subdirectory or under `testdata/` does not resolve;
a changed path that no longer exists selects its directory's
`GUARANTEES.md`; a changed path whose directory holds none selects nothing.
Watch the missing-test case fail against a checker whose lookup always
succeeds.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .claude/skills/verify-change/scripts/check-guarantees.py .claude/skills/verify-change/scripts/test_check_guarantees.py .claude/skills/verify-change/scripts/verify-change.sh .claude/skills/verify-change/SKILL.md`

### U3. ssh pilot

Files: `src/protocol/ssh/GUARANTEES.md`, `src/protocol/ssh/README.md`, `src/protocol/ssh/scan_test.go`, `src/protocol/ssh/command_test.go`
After: U2
Change: `GUARANTEES.md` holds one guarantee per contract the README
states, each sentence no broader than its tests. Evidence from the plan
review, to confirm by reading each test before citing it:
- Host-key verification has no default: the refusal without a policy is
  proved network-free by `TestHostKeyCallbackRequiresExplicitVerification`
  (`options_test.go`); a mismatch by `TestDialHostKeyMismatchRefused`.
- A failed wait closes the session: `TestRunCommandDeadlineExceeded` and
  `TestRunCancellationMidWait` prove it for deadline and cancellation,
  with unwrapped context errors. The peer-closed case needs a new test in
  `command_test.go` that calls `Run` again after
  `TestRunConnectionLostAfterCommandSent`'s scenario and expects
  `ErrSessionClosed`.
- A command ends at the earliest prompt match, ties going to slice order:
  no test asserts this; add a two-prompt case to `scan_test.go`.
- Pagination markers are answered and never reach the output:
  `TestRunPagination`.
- Output is capped with the true byte count kept: `TestRunOutputCapTruncates`.
  It does not prove the most recent bytes are kept (its payload is uniform);
  claim tail retention only if a case with distinguishable bytes is added.
- Stream draining: `TestRunStderrSaturationDoesNotBlockStdout` proves one
  direction; the guarantee states that direction only.
- Evidence: `TestRunRedactsSecretFromEvidence` proves `Command.Redacted`
  replaces `Command.Line` in `Evidence.Sent`, and that is the guarantee;
  the package does no scrubbing of its own, so "evidence never carries a
  credential" is not claimed unconditionally.
Error and field names come from `options.go`, `command.go`, and
`errors.go`. Each new test is watched failing against a mutation first.
The README keeps its explanations, drops sentences that only restate a
rule now in `GUARANTEES.md`, and links each contract section to its
guarantees.
Tests: the check over the new file; the new cases in `scan_test.go` and
`command_test.go` named above.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh`

### U4. Tooling classification for skill scripts in verify-change

Files: `.agents/skills/verify-change/scripts/verify-change.sh`
After: U3
Change: `verify-change.sh` classifies `.agents/*` paths as tooling
(`hook_tooling=true`) in the path classification `case` statements (lines 255
and 288), matching `.claude/*`, `.codex/*`, and `tools/hooks/*`. This ensures
`--base main` and targeted verifier runs against `.agents/skills/...` run
shellcheck, hook tests, and the Python test discovery under
`.claude/skills/*/scripts/` (which symlinks `.agents/skills/`). Because
`verify-change.sh` is merge-gate configuration, this change requires a
guardrail review before commit.
Tests: `verify-change.sh -- .agents/skills/verify-change/scripts/test_check_guarantees.py`
executes `test_check_guarantees.py` and passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/verify-change/scripts/verify-change.sh`

### U5. Strict line grammar and go list test resolution

Files: `.agents/skills/verify-change/scripts/check-guarantees.py`, `.agents/skills/verify-change/scripts/test_check_guarantees.py`, `.agents/skills/verify-change/SKILL.md`, `docs/conventions/guarantees.md`, `src/protocol/ssh/GUARANTEES.md`
After: U4
Change: `check-guarantees.py` is redesigned to enforce a strict line grammar for
`GUARANTEES.md` and resolve test symbols via `go list` and a Go lexer:
- Strict line grammar: every line in a `GUARANTEES.md` file must match one of
  the allowed line shapes, failing closed on any other line with
  `<path>:<line>: unknown line format`. Allowed shapes are: empty lines,
  top-level `# ` document title, preamble text prior to the first `## `
  heading, `## ` guarantee headings, normative sentences (single unindented line
  containing MUST or MUST NOT), `- WHEN ... THEN ...` scenario bullets and
  continuation lines, column-0 `Proved by:` lines, and indented `Proved by:`
  continuation lines (disallowing trailing commas). Fenced code blocks
  (``` or ~~~), setext underlines (`---`), indented code blocks (4 spaces or tabs),
  numbered lists (`1. ...`), and unallowed headings (`###`) fail closed as
  unknown line formats. Indented `Proved by:` lines fail as unknown line format
  and never count as citations. Heading parsing preserves unspaced `#`
  characters, retaining titles such as `## Parses C#`.
- Go test resolution: `check-guarantees.py` invokes `go list -json .` in the
  package directory (`cwd=pkg_dir`) with an environment inheriting `os.environ`
  plus `GOWORK=off` to obtain `TestGoFiles` and `XTestGoFiles`, passing
  `-mod=readonly` on the command line so a verifier run never rewrites `go.mod`
  or `go.sum`. Inheriting `os.environ` keeps `GOPATH` and `GOMODCACHE` valid;
  `go list` excludes `_foo_test.go` and `//go:build ignore` files by construction.
  Discovered test files are scanned with a Go token scanner that ignores
  whitespace, comments, and strings. Function declarations qualify as tests
  when the name starts with `Test` and its 5th character (if present) is not
  Unicode lowercase (`!unicode.IsLower`, rejecting `Testé`).
  Parameter lists support `*testing.T`, an aliased import `*<pkg>.T`, optional
  parameter names, an empty `()` result list, and multiline parameter layouts
  with optional trailing commas before `)`.
- Branch coverage: every branch in the lexer and line grammar has a test in
  `test_check_guarantees.py` that fails when inverted.
- Test fixture setup: `test_check_guarantees.py` provides a minimal `go.mod`
  in temporary test package directories so `go list -json .` resolves without
  error.
- Documentation: `docs/conventions/guarantees.md` and `.agents/skills/verify-change/SKILL.md`
  state the strict line grammar rules, the allowed line shapes (dropping code
  fence allowances), and `go list` test resolution.
- Pilot validation: `src/protocol/ssh/GUARANTEES.md` conforms to the strict
  line grammar and passes the redesigned checker.
Tests: `test_check_guarantees.py` tests:
- Strict line grammar acceptance (blank, H1, preamble, H2, MUST/MUST NOT,
  `- WHEN ... THEN ...`, `Proved by:`, continuation lines).
- Strict line grammar rejection: setext underline `---` fails; numbered list
  `1. item` followed by `---` fails; code fences fail; indented code block
  (4 spaces) fails; `Proved by:` inside an indented code block fails and is
  not counted.
- Heading preservation: `## Parses C#` resolves as `Parses C#` and does not
  collide with `## Parses C`.
- Test resolution: tests in `_foo_test.go` or behind `//go:build ignore` do
  not resolve; Unicode lowercase test name `Testé` does not resolve; valid
  signatures with an unnamed `*testing.T` parameter, multiline parameters,
  and aliased `testing` imports all resolve.
- Every lexer and grammar branch fails when inverted.
- Pilot check: `src/protocol/ssh/GUARANTEES.md` passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/verify-change/scripts/check-guarantees.py .agents/skills/verify-change/scripts/test_check_guarantees.py .agents/skills/verify-change/SKILL.md docs/conventions/guarantees.md src/protocol/ssh/GUARANTEES.md`

### U6. CommonMark guarantees checker command in tools/check-guarantees

Files: `go.mod`, `go.sum`, `tools/check-guarantees/main.go`, `tools/check-guarantees/check.go`, `tools/check-guarantees/check_test.go`
After: U5
Change: `tools/check-guarantees` implements the package guarantees checker in
pure Go using goldmark:
- Dependency: `github.com/yuin/goldmark` is added to `go.mod` and `go.sum`.
- Command CLI (`main.go`): parses `--all`, `--root <path>`, `paths`, and `--`
  separator. Selects `GUARANTEES.md` files matching `select_guarantee_files`
  (selecting `GUARANTEES.md` in directory or parent directory of each changed
  path, skipping `.git`, `node_modules`, `vendor`, `testdata`). Exits with
  `errs.ExitCode(err)` (`docs/code-style.md:250-252`).
- Checker logic (`check.go`): parses Markdown with `goldmark.DefaultParser()`,
  deriving 1-based source lines from node line/segment offsets. Validates
  allowed block kinds: at most one `# ` title before sections, preamble
  paragraphs before first section, `## ` section headings with HTML entity
  decoding (`&amp;` -> `&`), unspaced `#` preserved, and uniqueness check.
  Each section allows exactly three block kinds: one normative MUST/MUST NOT
  paragraph, one bullet list of `- WHEN ... THEN ...` items, and one
  `Proved by:` paragraph; any other block kind fails closed as unknown block
  kind. Text extraction ignores `ast.KindRawHTML` and handles multiline code
  spans. Resolves tests via `go list -mod=readonly -json .` with `GOWORK=off`
  and inherited environment. Scans discovered test files with Go token
  scanner, validating signatures (`*testing.T`, `*<pkg>.T`, anonymous
  parameters, multiline parameters); if any function named `Test...` has an
  invalid test signature, reports a malformed test error failing resolution.
  Removes obsolete hand-rolled line grammar rules.
- Test suite (`check_test.go`): Go table-driven unit tests covering all 73
  checker test intents, the 3 open review regression cases (escaped backtick
  followed by comment, multiline code span on WHEN line, entity duplicate
  headings), the badly-signed test package failure, and pilot file validation.
Tests: `go test -race ./tools/check-guarantees/...` proves all 73 test intents,
the 3 open review regression cases, and the invalid signature case.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- go.mod go.sum tools/check-guarantees/main.go tools/check-guarantees/check.go tools/check-guarantees/check_test.go`

### U7. Wire Go checker into verify-change, remove Python checker, and update documentation

Files: `.agents/skills/verify-change/scripts/verify-change.sh`, `.agents/skills/verify-change/scripts/check-guarantees.py`, `.agents/skills/verify-change/scripts/test_check_guarantees.py`, `docs/conventions/guarantees.md`, `.agents/skills/verify-change/SKILL.md`, `src/protocol/ssh/GUARANTEES.md`
After: U6
Change: `verify-change.sh` switches from the Python checker to the Go command,
obsolete Python scripts are removed, and documentation is updated:
- `verify-change.sh`: updates lines 518-520 to invoke `go run ./tools/check-guarantees`
  with `--all` under `--full`, and `-- "${paths[@]}"` otherwise. Because
  `verify-change.sh` is merge-gate configuration (`AGENTS.md:37-39`), this
  change undergoes guardrail review before commit.
- Python checker removal: `.agents/skills/verify-change/scripts/check-guarantees.py`
  and `.agents/skills/verify-change/scripts/test_check_guarantees.py` are deleted.
- Documentation: `docs/conventions/guarantees.md:44-98` replaces the strict line
  grammar rules with the CommonMark block-based grammar specification, allowed
  block kinds, entity-unescaped headings, and `tools/check-guarantees`
  validator. `.agents/skills/verify-change/SKILL.md:164-187` updates the "Package
  guarantees" gate description to document CommonMark block validation and the
  Go tool invocation.
- Pilot validation: `src/protocol/ssh/GUARANTEES.md` conforms to the CommonMark
  specification and passes the new checker.
Tests: `go run ./tools/check-guarantees src/protocol/ssh/GUARANTEES.md` passes;
`verify-change.sh -- src/protocol/ssh/command_test.go` executes the new gate and
passes.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills/verify-change/scripts/verify-change.sh .agents/skills/verify-change/scripts/check-guarantees.py .agents/skills/verify-change/scripts/test_check_guarantees.py docs/conventions/guarantees.md .agents/skills/verify-change/SKILL.md src/protocol/ssh/GUARANTEES.md`

Waves: U1 | U2 | U3 | U4 | U5 | U6 | U7

## Verification

```bash
go test -race ./tools/check-guarantees/...
```

Pilot validation:
```bash
go run ./tools/check-guarantees src/protocol/ssh/GUARANTEES.md
```

Full repository verification:
```bash
.claude/skills/verify-change/scripts/verify-change.sh --base main
```

Targeted test of changed package:
```bash
.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol/ssh/command_test.go
```

Then rename `TestRunOutputCapTruncates` in
`src/protocol/ssh/command_test.go`, confirm the verifier fails naming it
(Requirement 2), and restore the file from a copy taken first.

## Definition of done

- [x] Verifier green for every changed path in U1–U3.
- [x] `docs/README.md` authority paragraph, map, and placement sentence
      updated in U1.
- [x] The initial `verify-change.sh` edit passed the guardrail review before its
      commit in U2.
- [x] The pilot outcome recorded in this plan's outcome note: how many
      README contracts were already proved, how many needed a new test,
      and whether the stop condition held.
- [x] Verifier green for every changed path across U1–U5.
- [x] The `verify-change.sh` `.agents/*` classification edit passed guardrail
      review before U4 commit.
- [x] Checker redesign in U5 recorded as landed history.
- [x] Goldmark added to `go.mod` and `go.sum`, and Go checker command
      implemented in `tools/check-guarantees/` in U6.
- [x] All 73 checker test intents, 3 open review regression cases, and the
      invalid signature case pass under `go test -race ./tools/check-guarantees/...`.
- [x] `verify-change.sh` updated to invoke `go run ./tools/check-guarantees` and
      passes guardrail review before U7 commit.
- [x] Obsolete Python checker scripts removed in U7.
- [x] `docs/conventions/guarantees.md` and `.agents/skills/verify-change/SKILL.md`
      state CommonMark block rules and `tools/check-guarantees` invocation.
- [x] `src/protocol/ssh/GUARANTEES.md` passes under the new checker.
- [x] No plan labels in code, scripts, or skill text.

## Open questions

- The third review ended `rework` after three rounds on how the checker
  derives the text a reader sees, none of them clean. The rounds settled
  the parser (`goldmark.DefaultParser()` unchanged: a line opening an HTML
  block interrupts a paragraph or list item and fails closed) and block
  lines (`n.Pos()`). Text derivation moved from raw segments, to
  hand-chained `util` decoding (fails open on `\&#77;UST`, `&#0115;UST`,
  over-long hex references), to rendering the node and stripping tags.
  That last form still fails open: a hard line break inside image alt
  text renders `<br>` inside the `alt` attribute, so `It is ![x\` +
  newline + `MUST](y.png).` counts as normative, and the same leaks THEN.
  Re-plan the derivation as a stated property with a generated or
  exhaustive check against goldmark's renderer, not a fourth patch (a
  quote-aware tag stripper is the candidate). Also open: citation lines
  fall back to the `Proved by:` line for every name after one written
  with an entity or escape (`Test&#65;`); the Go test that string-matches
  a line of `verify-change.sh` belongs in `tools/hooks/tests/run.sh` or
  nowhere; Requirement 15 and the invocation Decision still say `go run`,
  while the verifier now builds the checker and runs the binary.

- The follow-up plan: `plan`'s template gains a `## Guarantee changes`
  section with `Added:`, `Changed:`, and `Removed:` groups under one
  `### <package path>` heading each, the blocks in fenced code so their
  `##` headings do not join the plan's outline; `phases.md` puts a
  guarantee change in the phase whose unit implements it; `implement`
  step 4 applies a unit's blocks in the same unit; `review` checks
  `GUARANTEES.md` against the diff and the delta. Write it once this
  plan's outcome note shows the stop condition did not trigger.
- Whether to add `docs/conventions/guarantees.md` to `AGENTS.md`'s
  Conventions list; propose it through `steer` after the follow-up lands.
- Updating `--print-selection` assertions in `tools/hooks/tests/run.sh`:
  `verify-change.sh` classified `.agents/*` as `hook_tooling=true` in U4, so
  `verify-change.sh --print-selection -- .agents/skills/...` now includes
  `hook_tooling` in its output. `tools/hooks/tests/run.sh:765` tests
  `--print-selection` output without `hook_tooling`. Updating `run.sh` touches
  `tools/hooks/`, which is a policy surface requiring a separate guardrail
  review; this update is left for a future policy-surface maintenance pass.
- Parked by drive: the third review ended `rework`. The checker now
  parses with goldmark, but it takes visible text by rendering a block to
  HTML and stripping tags (`tools/check-guarantees/check.go`
  `extractVisibleText`, `stripHTMLTags`), and a `<br>` inside image alt
  text ends the strip early, so `It is ![x\` + newline + `MUST](y.png).`
  counts as a normative sentence and the same trick hides a THEN. Open
  besides: an entity-written citation misplaces later errors' lines; a Go
  test string-matches `verify-change.sh`; Requirement 15 and the
  invocation Decision still say `go run` where the verifier now builds
  and runs the binary. How should visible text be derived? Options: inline
  allowlist on the AST (counted blocks may hold only text, line breaks,
  code spans, and emphasis; raw HTML, images, links, and autolinks fail
  closed; visible text is the concatenated AST text segments, with no
  renderer or stripping; no known leak class stays open, but guarantees
  cannot contain links or images) | quote-aware HTML stripper (a fourth
  patch on the renderer path; keeps every inline kind, but each earlier
  round leaked through a new construct) | accept the current checker
  (land with the alt-text leak recorded; a crafted file can still hide a
  MUST or THEN). Recommended: inline allowlist, because it removes the
  renderer-and-strip step that each leak went through, and a guarantee
  sentence needs no link or image.
