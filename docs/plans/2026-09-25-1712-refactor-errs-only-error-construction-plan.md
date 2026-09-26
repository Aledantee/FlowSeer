---
title: Errs-Only Error Construction - Plan
type: refactor
date: 2026-09-25
artifact_contract: flowseer-plan/v1
artifact_readiness: implementation-ready
status: planned
execution: code
amends: docs/code-style.md
---

# Errs-Only Error Construction - Plan

## Goal

Non-test Go under `src/` constructs errors only through `src/common/errs`. No
`errors.New` and no `fmt.Errorf` survive outside that package, `docs/code-style.md`
Errors says so instead of allowing plain `fmt.Errorf`, and a conformance gate
under `test/conformance/errs` fails the build on a new one. 654 call sites move:
575 `fmt.Errorf` and 79 `errors.New`.

Stop condition: a package that cannot import `errs` without a cycle. None exists
today — `go list -deps ./src/common/errs` names only
`generated/go/proto/flowseer/errs/v1` and `errs` itself, so no package under
`src/` can cycle on it — but a unit that finds one stops rather than working
around it.

## Decisions

These are the user's rulings, not this plan's proposals.

- Non-test Go uses `errs` only, with this mapping: `errs.Msg` / `errs.Msgf` for a
  new error or a package-level sentinel, `errs.Wrap` / `errs.Wrapf` to add
  context to a cause, and the `errs.New()` builder (or `errs.From(err)`) where the
  error carries a code, attributes, retryability, or a `UserMsg`.
- Test files are out of scope. `_test.go` keeps whatever it uses.
- `docs/code-style.md` Errors is amended in the same change as the conversions,
  dropping the sentence that allows plain `fmt.Errorf`. A convention that still
  permits what the gate rejects would send the next reader the wrong way.
- The gate lands last, after every conversion. A gate that lands first fails the
  build for as long as the migration runs.
- Keeping netpen off Claude is a preference, not a rule. When no non-Claude lane
  with a fitting model has quota, the work runs on Claude rather than waiting.
  Lanes are chosen in this order: `gpt-5.6-sol` on `codex` while it is under its
  limit and sparingly, because its remaining quota is nearly spent; then
  `gemini-3.8-flash` on `google` wherever the role's fit set includes it; then
  Claude. On netpen or any `sensitive_paths` unit, Claude is pinned to
  `claude-opus-4-8` from the start — not Opus 5.x, whose cyber flags produce the
  silent fallback that cost this work its first review.
- E3, the `src/edge/netpen` unit, therefore runs now on `claude-opus-4-8` rather
  than waiting for `codex` to reset: `codex` is at 95%, `kimi-k3`'s pool is at 96%,
  and `google` is not in the `execute-sensitive` fit set.
- No unit that touches `src/edge/netpen` runs on a Claude model. A Claude lane can
  fall back from its pinned model on a cyber refusal without failing, and the
  fallback is silent in the lane's own output. It has already happened here:
  `style-review` was pinned to `claude-opus-5` and switched to
  `claude-opus-4-8` at 2026-09-25T18:56:13Z, recorded in
  `docs/agent-observations.md` on the coordinator branch at `5a3114b7`. E6 also
  avoids Claude: it does not edit netpen files, but the gate it builds parses them
  on every run, which is the same material in front of the same classifier.
- Claude is the last choice on a sensitive unit, not a peer one, but it is a real
  choice rather than a reason to stop: it runs when every non-Claude model in the
  role's fit set is out of quota, on `claude-opus-4-8` at high effort. Never Opus
  5.x there.
- Every Claude lane's transcript is read before its work is accepted, at
  `~/.claude/projects/<worktree path with / replaced by ->/<session>.jsonl`. A
  `{"type":"system","subtype":"model_refusal_fallback"}` event, or any `model`
  field other than the pinned one, means the lane's output is not accepted: state
  it as a blocker and stop. Checking the screen's footer is not enough — it shows
  the current model, not that a switch happened.

This plan's own decisions:

- Units are cut per package tree, six of them, with `src/edge/netpen` alone
  because it is a nested module and holds 213 of the 654 sites. Why: the trees
  share no file, so five units run in one wave; only the gate depends on all of
  them.
- `fmt.Errorf("…: %w", err)` becomes `errs.Wrap(err, "…")` rather than the
  builder. Why: `errs.Wrap` keeps the cause in the `errors.Is` chain, returns nil
  when `err` is nil so an unconditional call site is safe, and its documentation
  states the wrapped error comes first "matching `fmt.Errorf` reading order", so
  the rendered message does not change.
- A `var ErrX = errors.New("…")` sentinel becomes `errs.Msg("…")`. Why: `errs.Msg`
  is documented as the constructor for package-level sentinels and records no
  stack on purpose, because a stack captured at package init describes the
  declaration site rather than the failure.
- The gate resolves `fmt` and `errors` by import path, not by the bare qualifier,
  and handles an alias and a dot-import. Why: `src/common/errs/code_test.go`
  already does exactly this for `NewCode` (`errsQualifiers` at `:376`,
  `isNewCode` at `:403`) with fixtures at `errs/testdata/scan/{aliased,dotimport,
  foreign}.go`; a gate matching the text `fmt.Errorf` is defeated by
  `import f "fmt"`.
- Error strings keep their current text. Why: `revive`'s `error-strings` already
  holds them lowercase and unpunctuated, and changing message text in the same
  change as 654 mechanical call-site edits would hide the one that matters.

## Requirements

1. No non-test Go file under `src/` outside `src/common/errs` calls `errors.New`
   or `fmt.Errorf`. Acceptance:
   `rg -n '\b(errors\.New|fmt\.Errorf)\(' src --glob '!*_test.go' --glob '!src/common/errs/**' --glob '!**/testdata/**'`
   prints no line.
2. Every converted site keeps its message and its `errors.Is` behavior.
   Acceptance: `src/common/net/arp/arp.go:43` becomes
   `ErrMalformed = errs.Msg("malformed ARP message")`, `errors.Is(err,
   arp.ErrMalformed)` still holds for a malformed frame, and the rendered string
   is unchanged. `src/protocol/syslog/errors.go` is the shape to copy — its five
   sentinels are already `errs.Msg`.
3. A wrapped cause stays wrapped. Acceptance: a site that was
   `fmt.Errorf("read header: %w", err)` is `errs.Wrap(err, "read header")` and
   `errors.Unwrap` still returns the cause.
4. `docs/code-style.md` Errors states the rule and no longer allows plain
   `fmt.Errorf`. Acceptance: the clause at `docs/code-style.md:215-217` — "Plain
   `fmt.Errorf("open session: %w", err)` stays fine where nothing structured is
   needed." — is gone, replaced by the errs-only rule and the Decisions' mapping.
5. `test/conformance/errs` fails on a violation and passes on the tree this plan
   leaves. Acceptance: the gate reports
   `src/common/service/bus.go:200: fmt.Errorf outside src/common/errs` when that
   call is restored, and reports nothing on the converted tree.
6. The gate cannot pass without having read anything. Acceptance: it counts the
   files it parsed and fails with "scanned no source files" at zero, as
   `test/conformance/panic/panic_policy_test.go:85` does.
7. The gate resolves the two packages by import path. Acceptance: its table test
   accepts `errs.Msg`, rejects `fmt.Errorf`, rejects `f.Errorf` where the file
   reads `import f "fmt"`, rejects a bare `Errorf` in a file that dot-imports
   `fmt`, and accepts an `Errorf` method on an unrelated receiver.

## Out of scope

- `_test.go` files, per the user's decision.
- `generated/`, which no gate reads and no hand edit touches.
- The message text of any converted error, and any change to which errors carry a
  code. A site that should gain `errs.New().Code(…)` is a separate judgement; this
  plan moves construction, not classification.
- `src/common/errs` itself, which is where `errors.New` and `fmt.Errorf` are
  allowed to live.
- The excluded files the style plan lists. They hold no violation of this rule
  today, confirmed by the user, so nothing in them needs converting; if one
  appears while they are still held by another session, it is a blocker.
- `.golangci.yml`. The gate makes the linter rule unnecessary, so no policy
  surface changes: `tools/hooks/stop-check.sh:56` globs `test/conformance/*/`, so
  a new package there is a merge gate the moment it exists.

## Units

### E1. common foundations
Files: `src/common/net/`, `src/common/service/`
After: none
Change: 119 sites take the mapping — `src/common/net/pcap` 40, `src/common/service`
67, and one or two each in `net/{arp,icmp,igmp,lacp,mld,ndp,tcp,udp}`, whose
`ErrMalformed` and `ErrUnsupported` sentinels (`arp.go:43,45`, `mld.go:86,88`,
`ndp.go:54,56`, `lacp.go:66`) become `errs.Msg`. Find the rest with requirement
1's grep scoped to these two trees.
Tests: no new test. The existing suites assert the messages and the `errors.Is`
chains these calls produce; a conversion that changes either fails them. Run
`go test -race ./src/common/net/... ./src/common/service/...`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/net src/common/service`

### E2. netsim
Files: `src/common/netsim/`
After: none
Change: 159 sites — `internal/` 120 (the bulk in `netsimtest/comparison_cases.go`,
42 of them the same eight-way repetition), `stream/` 38, one elsewhere. The
repetition in `comparison_cases.go` is a helper waiting to be extracted; extract
it if the conversion makes the duplication plainer, and say so in the report.
Tests: no new test; the corpus and comparison suites already assert these
failures. Run `go test -race ./src/common/netsim/...`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/common/netsim`

### E3. netpen
Files: `src/edge/netpen/`
After: none
Lane: `claude-opus-4-8` at high effort, pinned from the start. Its transcript is
read before the work is accepted.
Change: 213 sites in the nested module — `attacks/` 129, `layers/` 59, `test/` 15
(non-test files only), `catalog/` 9, one elsewhere. The module already imports
`errs`, so no `go.mod` changes; if one does, that is a blocker.
Tests: no new test. The layer decoders' error strings are asserted by
`layers/layers_test.go` and the fixture-pin tests, so a changed message fails
them. Run `go test -race ./...` from `src/edge/netpen`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/netpen`

### E4. edge agent, netsimload, and modules
Files: `src/edge/agent/`, `src/edge/netsimload/`, `src/modules/`
After: none
Change: 107 sites — `netsimload/` 44 (`cmd/` 18 of them), `modules/capture/` 56
(`filter/` 23, `mirror/` 19, the package root 11, `rawsocket/` 2),
`agent/internal/` 4, and one each in `modules/edgebus` and `modules/localnet`.
Tests: no new test. `capture/filter/compile_test.go` and `capture/mirror` assert
their refusal messages. Run
`go test -race ./src/edge/agent/... ./src/edge/netsimload/... ./src/modules/...`.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/edge/agent src/edge/netsimload src/modules`

### E5. protocol and services
Files: `src/protocol/`, `src/services/`
After: none
Change: 56 sites — `services/device/internal/` 27, `protocol/smi/internal/` 17,
`protocol/internal/conformance/` 5, `protocol/smi/differential/` 5,
`protocol/snmp/bench/` 2 (a nested module). `src/protocol/snmp` proper holds only
two, because it already uses `errs`; the pattern there is the model for the rest.
Tests: no new test. `smi`'s diagnostic suites and `services/device`'s handler
tests assert these messages. Run
`go test -race ./src/protocol/... ./src/services/...` plus the `snmp/bench` module.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- src/protocol src/services`

### E6. the convention and the gate
Files: `docs/code-style.md`, `test/conformance/errs/`
After: E1, E2, E3, E4, E5. The gate fails on any `fmt.Errorf` left under `src/`,
and netpen alone holds 213 of them, so it cannot land before E3.
Change: `docs/code-style.md` Errors drops "Plain `fmt.Errorf(…)` stays fine where
nothing structured is needed." and states the errs-only rule with the Decisions'
mapping, keeping the section's existing sentences about wrapping, sentinels,
codes, `UserMsg`, `Retryable`, and `ExitCode`. `test/conformance/errs/` gains one
package modeled on `test/conformance/panic/panic_policy_test.go`: it walks
`src/` with `filepath.WalkDir`, skips directories named `testdata` and files
ending `_test.go`, skips `src/common/errs`, parses each file with `go/parser`,
resolves the `fmt` and `errors` import paths to their qualifier sets the way
`src/common/errs/code_test.go:376` does, reports every `errors.New` and
`fmt.Errorf` call as `path:line: <call> outside src/common/errs`, counts the files
it parsed, and fails at zero. Reading by path rather than importing is what lets
one test in the root module cover the nested modules, as the panic gate's doc
comment explains.
Tests: the gate's own table test, covering requirement 7's five cases, with
fixtures under `test/conformance/errs/testdata/` for the alias and dot-import
shapes — the gate skips `testdata` when walking `src/`, so fixtures there are read
directly by the table test, not by the walk. Prove the gate fails before it
passes: restore `fmt.Errorf` at one converted site, quote the gate's failure in
the report, then revert that site.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- docs/code-style.md test/conformance/errs`

Waves: E1 E2 E3 E4 E5 | E6

Lanes, from the Decisions above and `delegate`'s resolution at 2026-09-25T21:5xZ
(`codex` 85%, `synthetic` 96%, both past the cutoff; `google` 7%; `claude` 56%):

| Unit | Lane | Why |
| --- | --- | --- |
| E1, E2 | `gemini-3.8-flash-high` (`google`) | no `sensitive_paths`, and the only prepaid pool with headroom |
| E3 | `claude-opus-4-8` high | netpen; `codex` 95%, `kimi-k3` 96%, `google` not in the fit set. Pinned Opus 4.8 from the start; transcript checked |
| E4, E5 | `claude-opus-4-8` high | `sensitive_paths` (`src/modules/localnet`, `src/protocol/snmp`); both non-Claude models in the `execute-sensitive` fit set are out of quota. Transcript checked |
| E6 | `gemini-3.8-flash-high` (`google`) | the gate parses netpen on every run |

With a one-worker budget these run in turn, so the wave grouping only fixes the
order E6 comes last in.

## Verification

```bash
# per unit
.claude/skills/verify-change/scripts/verify-change.sh -- <the unit's paths>

# after the last merge, over the union
.claude/skills/verify-change/scripts/verify-change.sh -- \
  src/common/net src/common/service src/common/netsim src/edge/netpen \
  src/edge/agent src/edge/netsimload src/modules src/protocol src/services \
  docs/code-style.md test/conformance/errs
```

Not `--full`: it builds and race-tests `generated/go/yang`, which is large enough
to exhaust host memory, and a run that dies is not a gate. A targeted run already
expands to every package importing a changed file, and the script builds and vets
the nested modules after a root change. Never point `golangci-lint` at
`generated/go/yang`; `verify-change.sh` drops `/generated/` from its lint list in
every mode.

Then requirement 1's grep:

```bash
rg -n '\b(errors\.New|fmt\.Errorf)\(' src \
  --glob '!*_test.go' --glob '!src/common/errs/**' --glob '!**/testdata/**'
```

It prints no line. The gate under `test/conformance/errs` is the durable form of
the same check and runs on every `go test`; the grep is what an implementer uses
mid-unit, before the gate exists.

What no test covers: requirement 2's "message unchanged" holds only where a suite
already asserts the message. Where none does, the conversion is unobserved, and
the reviewer reading the diff is the check. The units say so rather than claiming
the suites cover 654 sites.

## Definition of done

- The verifier is green on every changed path and on the union after the last
  merge.
- Requirement 1's grep prints no line, and `go test ./test/conformance/errs/`
  passes.
- The gate's failure against a restored violation is quoted in the report.
- `docs/code-style.md` Errors and the gate agree; neither permits what the other
  rejects.
- This plan's `status` is set with an outcome note under its title.
- No plan label in code, comments, or commit messages.
- `git diff --stat` names no file under `generated/`, `buf.lock`, `.golangci.yml`,
  `AGENTS.md`, or `tools/hooks/`.

## Open questions

Empty. Every decision above is the user's. The rule, the mapping, the test-file exclusion, the document amendment, and
the gate's position are all the user's decisions, recorded under Decisions.
