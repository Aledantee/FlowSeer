---
title: Python Scripts Under uv Phase 4, Verifier - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: code
---

# Python Scripts Under uv Phase 4, Verifier - Plan

## Goal

`.agents/skills/verify-change/scripts/verify-change.sh` (1,084 lines) and
`tools/test/service-otel-integration.sh` (86 lines) are Python under
`tools/scripts/verify/`, started as `uv run tools/scripts/run.py verify
change [--full] [--print-selection] [--base REF] [-- PATH...]` and `uv run
tools/scripts/run.py verify service-otel-integration`. The means: the
verifier suites are widened against the shell scripts first, run against
both implementations, and the shell is deleted only when both pass.

**Stop condition:** a case of the widened suites cannot pass against the
shell verifier and the Python command with one expectation, for a reason
other than the rows under Cases outside the shared set. The port has then
changed a verdict.

## Decisions

- The parent plan's Decisions and the
  [repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
  apply.
- The plan stays whole. Why: its six units are one cluster, joined by
  `tools/scripts/verify/__init__.py` and the suites every unit runs.
- `test_verify_paths.py` and `test_ledger.py` take the verifier command as a
  parameter and are extended first, against the shell verifier, until they
  cover every path class and every exit path the script has. The classes
  and exit paths are the two tables below. Why: parent Requirement 6.
- A case reaches the verifier through
  `tools/scripts/tests/verify/support.py`. `support.implementations()`
  returns the argument lists that exist: the shell script while the file
  exists, and `uv run tools/scripts/run.py verify change` once the `verify`
  group registers `change`. Each case loops over them with `subTest`, and
  an empty list fails the case. Why: `tools/scripts/tests/hooks/support.py:42`
  already works this way, and no switch can be forgotten.
- The port keeps each fix `docs/agent-steering.md:1151` to `:1194` records.
  Each has a named test in the Fixes table.
- The embedded Python block (`verify-change.sh:752` to `:769`) becomes the
  function `change.affected(rows, changed)`, which returns the changed
  packages and every package that imports one, sorted.
- Gates stay subprocesses with argument lists: `go`, `gofumpt`,
  `goimports`, `golangci-lint`, `buf` through `go tool`, `docker`, `git`,
  `jq`, `shellcheck`, and the six binaries under
  `frontend/web/node_modules/.bin/`. The script starts those binaries
  directly and never `pnpm` (`verify-change.sh:635` to `:640`), and the
  port does the same. The verifier still searches `$(go env GOPATH)/bin`,
  by prepending it to `os.environ["PATH"]` in its own process.
- The checks under `tools/scripts/verify/` run in the verifier's process
  through their `main(argv)`, under the labels the script prints (`run.py
  verify check-prose`). Why: each is a function with no process state
  (`docs/code-style-python.md`). A `SystemExit` from one is read as its
  exit code. `change.main` changes to the root first, as `:90` does, since
  `tools/scripts/verify/check_markdown_links.py:35` reads the working
  directory. The suite gate `uv run tools/scripts/run.py test` stays a
  subprocess in the tree being verified (`verify-change.sh:970`).
- `diff -qr` (`:910`, `:912`), the wrapper's `grep -r -F -q`, and
  `getconf` (`:781`) become functions, since parent Requirement 10 rules
  them out as subprocesses. The tree comparison keeps the label `diff
  generated/go/proto`, the symptom
  `docs/solutions/conventions/the-buf-cli-version-decides-one-option-byte-in-every-generated-descriptor.md`
  names.
- `required_tools` (`verify-change.sh:561` to `:582`) is ported as it
  stands, `jq`, `shellcheck`, and `python3` included, and `jq empty` stays
  a subprocess. Why: the Phase 7 plan's Requirement 3 removes the three,
  and a host without one gets the same verdict from both implementations
  until then. The port globs `tools/test/*.sh` and
  `.claude/skills/*/scripts/*.sh` itself and starts `shellcheck` only when
  a file matches. Once this phase deletes the last two host-side shell
  scripts none does, and the run prints `shellcheck skipped: no host-side
  shell script matched.`
- Where the shell ignores a failure, the port ignores it, and where `set
  -e` ends the run, the port returns that status with no gate named. The
  ignored sites are the process substitutions at `:139`, `:142`, `:177`,
  `:296`, `:299`, `:421`, `:488`, `:685`, `:707`, `:752`, and `:800`, and
  every `|| true` and `|| continue`. Why: parent Requirement 6. A fix to one
  of them is a verdict change and belongs in its own plan.
- Arguments are parsed by a loop over `argv`, not `argparse`. Why: the
  script tells `--` followed by nothing from no `--` (`:145`) and prints
  its own messages, which the suites pin. The usage text names `run.py
  verify change`.
- The port reads git's output as bytes and splits `-z` output on NUL. It
  reads the marker with `lib/treestate.py`'s encoding and splits it on
  `"\n"` only. `TMPDIR` and `FLOWSEER_DOCKER_PROBE_TIMEOUT` are read with
  `or`, so an empty value takes the default. Why:
  `docs/solutions/conventions/a-shell-to-python-port-changes-empty-variables-and-line-breaks.md`.
  `lib/proc.py` reads in text mode and is not changed here.
- A `case` pattern's `*` crosses `/`, so the port matches classes with
  `fnmatch.fnmatchcase`, never `glob`. `src/common/service/*.go` matches
  `src/common/service/a/b.go` (`:376`).
- The surviving marker lines (`:1053`) and the build tags (`:408`) are
  sorted by bytes, where `sort -u` follows the locale. Why: `hooks
  stop-check` sorts the marker that way
  (`tools/scripts/hooks/stop_check.py:65`).
- Receipts and the dirty marker keep their file formats. The receipt holds
  `verified_at`, `base`, `full`, and `paths`, in that order. Only
  `verified_at` is parsed (`tools/scripts/verify/ledger.py:105`). `full`
  is read by a person following
  `.agents/skills/land/references/mechanical-remedies.md`. `paths` and the
  `+ ` command lines are quoted as bash's `printf %q` does: `/bin/bash`
  3.2.57 on darwin/arm64 prints `a\ b`, `''`, `x\$y`, `a\,b`, `$'a\nb'`,
  and `plain/p.go` unchanged (probed 2026-10-09). A `--full` run writes
  `paths='' `, the one empty word of `"${paths[@]:-}"` (`:1080`). The
  listing is written in process through `treestate.listing`, as bytes, and
  a listing that fails ends the run with exit 1 (`:1069`).
- The verdict line is written by `change.main` after everything else, for
  every return and every exception, `KeyboardInterrupt` included. A gate
  killed by signal `n` gives exit `128 + n`, as bash reports it.
- The OpenTelemetry tier is `verify/service_otel_integration.py`. The
  verifier calls its `main` in process under the label `run.py verify
  service-otel-integration`. The probe is `docker info` with a timeout,
  then terminate, one second, then kill. The scan reports the sentinel
  when any readable file holds it and a scan error otherwise when a file
  cannot be read, as `/usr/bin/grep -r -F -q` exits 0 and 2 for those two
  trees (probed 2026-10-09, BSD grep 2.6.0).
- While both verifiers exist the shell one is the authority, and U1 to U4
  verify with it. U5 and U6 verify with `verify change`.
- `AGENTS.md`, every `SKILL.md` and reference with a verifier command, the
  `plan` unit template, `README.md`, `CONTRIBUTING.md`, and
  `docs/conventions/testing.md` name the new commands. A solution that
  cites the script as live code cites `tools/scripts/verify/change.py` and
  a function name. A citation of history keeps the old path behind a
  commit, as `861d0994:<path>`.
- Open plans under `docs/plans/` keep their `Verify:` lines. Why: no script
  runs one. `tools/scripts/skills/next/plan_queue.py:75` matches the key
  to find where a unit's `Files:` text ends, and `implement` gives its own
  command (`.agents/skills/implement/SKILL.md:135`). A person who runs an
  old line gets `No such file or directory`, which cannot read as a pass.
- `.agents/skills/verify-change/scripts/` leaves the policy surfaces in
  `AGENTS.md` and `tools/scripts/hooks/pre_tool_policy.py:44` when the
  directory is deleted. Why: its replacement `tools/scripts/verify/` is
  already listed, and the parent rules out a reference to a path that is
  gone.
- U3, U4, and U5 change a policy surface. Each stops uncommitted with the
  verifier green, and the person approves it by blob hash before the
  commit, since a prompt inside a worker is not reliable
  (`docs/agent-observations.md`, entry of 2026-10-07 on policy-surface
  approval). They run on Claude workers, since Codex cannot answer the
  hook's `ask`.
- The Phase 3 review left items on
  `tools/scripts/tests/verify/test_service_otel_integration.py:107`
  (standard output is not asserted) and on `lib/hookio.py`. U2 takes the
  first, since it rewrites those assertions. The rest stay with Phase 7.

### Path classes

From the `case` arms of `verify-change.sh`. Classes 3 to 14 are one `case`
statement (`:316`), so the first match wins. Classes 1, 2, 15, 16, and 17
are tested on their own for every path.

| # | Pattern (line) | Selects |
| --- | --- | --- |
| 1 | `frontend/web/*` (`:309`) | the six web gates, and `test/conformance/...` when no Go module is selected |
| 2 | `docs/plans/*` (`:314`) | `check-plan-state` |
| 3 | `*.md` that exists (`:317`) | link and prose checks |
| 4 | `*.md` that is gone | nothing, unless class 5 matches |
| 5 | `*.md` under `.agents/`, `.claude/`, `.codex/`, `tools/scripts/`, or named `CLAUDE.md`, `AGENTS.md`, `docs/agent-knowledge.md` (`:320`) | hook tooling, whether or not the file exists |
| 6 | `*.go` that exists outside `generated/` and `frontend/web/generated/` (`:324`) | format gate and its module |
| 7 | `*.go` under `generated/` inside a nested module, deleted included (`:328`) | that module, build and sample lint |
| 8 | `*.go` under `generated/` in the root module, under `frontend/web/generated/`, or deleted elsewhere | nothing |
| 9 | `tools/buf/go.mod`, `tools/buf/go.sum` (`:337`) | no Go module, then class 16 |
| 10 | `go.mod`, `go.sum` at any depth (`:343`) | the module, module-wide |
| 11 | `*.proto` that exists (`:347`) | protobuf gates with `--path` |
| 12 | `*.proto` that is gone | protobuf gates, whole-module `breaking` |
| 13 | `buf.yaml`, `buf.work.yaml`, `buf.gen.yaml` at the root | protobuf gates, no `--path` |
| 14 | `.agents/*`, `.claude/*`, `.codex/*`, `tools/test/*`, `tools/scripts/*`, `uv.toml` (`:359`) | hook tooling |
| 15 | `mibgen.yaml`, `spec/mib/*`, `src/protocol/snmp/cmd/mibgen/*`, `src/protocol/smi/*` (`:367`) | mibgen drift and baseline |
| 16 | `tools/buf/*` (`:370`) | protobuf gates and `go -C tools/buf mod verify` |
| 17 | root `go.mod`, `go.sum`, `src/common/service/*.go`, `src/common/service/test/integration/otel*`, its `testdata/otel-collector.yaml`, the wrapper (`:376`) | OpenTelemetry tier |
| 18 | `.golangci.yml` without `--full` (`:528`) | refusal, exit 2 |
| 19 | any other path | nothing |
| 20 | `--full` (`:289`) | every gate, every module |

Beside the classes: a directory expands to its files (`:171`), a path
through a linked directory is rewritten (`:104`), a file under `testdata/`
belongs to the enclosing package (`:388`), and a nested module that
replaces the root is a dependent (`:447`).

### Exit paths

| # | Path (line) | Exit | Last line |
| --- | --- | --- | --- |
| 1 | `--base` with no ref (`:60`) | 2 | `FAILED (exit 2).` |
| 2 | empty word after `--` (`:71`) | 2 | same |
| 3 | `-h`, `--help` (`:76`) | 0 | the usage text |
| 4 | unknown argument (`:80`) | 2 | `FAILED (exit 2).` |
| 5 | outside a repository (`:89`) | git's status | `FAILED (exit N).` |
| 6 | base that is no commit (`:136`) | git's status | same |
| 7 | `--` with no path (`:145`) | 2, or 0 under `--print-selection` | `FAILED (exit 2).` |
| 8 | no changed path (`:154`) | 2, or 0 under `--print-selection` | same |
| 9 | directories holding no file (`:195`) | 2 | same |
| 10 | `--print-selection` (`:491`) | 0 | the selection |
| 11 | `.golangci.yml` without `--full` (`:528`) | 2 | `FAILED (exit 2).` |
| 12 | a web binary is missing (`:577`) | 1 | `FAILED (exit 1).` |
| 13 | tools missing, all named, with the GOPATH hint (`:591`) | 1 | same |
| 14 | a gate started by `run` fails (`:218`) | the gate's | `FAILED (exit N) in gate: <label>` |
| 15 | format differs (`:668`) | 1 | `in gate: gofumpt/goimports -d` |
| 16 | a command outside `run` fails under `set -e` (`:623`, `:747`) | its status | `FAILED (exit N).` |
| 17 | no `main` or `master` for `breaking` (`:885`) | 1 | `FAILED (exit 1).` |
| 18 | the script suite fails (`:970`) | 1 | `in gate: run.py test` |
| 19 | the script suite ran zero tests (`:975`) | 1 | `in gate: uv run tools/scripts/run.py test` |
| 20 | no gate selected, after `git diff --check` (`:997`) | 2 | `FAILED (exit 2).` |
| 21 | the listing fails (`:1069`) | 1 | `FAILED (exit 1).` |
| 22 | pass (`:1084`) | 0 | `FlowSeer verification passed.` |

### Fixes and their tests

The fixes `docs/agent-steering.md` records, each with the case that pins
it. `paths` is `test_verify_paths.py`, `gates` is `test_verify_gates.py`,
`marker` is `test_verify_marker.py`, `otel` is
`test_service_otel_integration.py`.

| Fix | Test |
| --- | --- |
| a directory expands to its files | `paths`: `test_directory_expands_to_its_files` |
| a no-gate run leaves receipt and marker untouched | `marker`: `test_no_gate_run_leaves_receipt_and_marker_as_they_were` |
| the corpus tier carries `-count=1` | `gates`: `test_full_run_gives_the_corpus_tier_count_1` |
| `buf breaking` targets only files `main` holds | `gates`: `test_breaking_names_only_the_schema_files_main_holds` |
| the last line names the verdict and the gate | `paths`: `test_failing_gate_is_named_in_the_last_line` |
| the `docker info` probe is bounded | `otel`: `test_service_opentelemetry_wrapper_gives_up_on_a_docker_daemon_that_does_not_answer` |
| invariant packages run once from the root | `gates`: `test_invariant_packages_run_once_from_the_root` |
| `--full` bounds `go test -p` | `gates`: `test_full_run_bounds_test_parallelism` |
| lint passes `--allow-serial-runners` | `gates`: `test_lint_waits_for_the_machine_lock` |
| `$(go env GOPATH)/bin` is searched | `gates`: `test_tools_under_gopath_bin_are_found` |
| every tool is checked before the first gate | `gates`: `test_missing_tools_stop_the_run_before_any_gate` |
| a pass prints the marker lines it could not clear | `marker`: `test_passing_run_names_the_lines_it_left` |
| a web path selects the web gates | `gates`: `test_web_path_runs_the_six_web_gates` |
| a marked path absent from tree and base clears | `marker`: `test_path_absent_from_tree_and_base_clears` |
| a path through a link is rewritten | `paths`: `test_file_through_a_symlink_is_rewritten_to_the_tracked_spelling` |
| the listing is rewritten after a pass | `marker`: `test_passing_run_rewrites_the_tree_state` |
| the Bash-mutation line clears under `--base` | `marker`: `test_bash_mutation_line_clears_when_its_files_match_the_base` |
| changed packages and their importers are the targets | `gates`: `test_targets_are_the_changed_packages_and_their_importers` |

### Cases outside the shared set

| Case | Why | What replaces it |
| --- | --- | --- |
| `RunHelperTest` (`test_verify_selection.py:264`) | evaluates the shell `run()` in bash | exit path 18, where a failing command is read inside a capture |
| `shellcheck` with no script to lint | the shell passes the unmatched patterns on, and `shellcheck` fails | a case against the Python command only, in U5 |
| `shellcheck` with one pattern unmatched | the same, for that pattern | the hook-tooling fixture holds a file for each pattern |
| class 17, the wrapper's path | the path changes with the port | the row names the module in U5 |
| the first line of the usage text | it names the command | exit paths 3 and 4 pin the options line and the last line |
| the `+ ` line of a check | the shell prints `+ uv run --quiet <run.py> verify <check>`, and the port starts no `uv` | the port prints `+ run.py verify <check>`, pinned against the Python command in U4 |

## Requirements

1. The suites pass against both verifiers before the shell one is deleted.
   Example: after U4, `uv run tools/scripts/run.py test
   tools/scripts/tests/verify` reports no failure, and every case of
   `test_verify_paths.py` ran two sub-tests.
2. A run that selects no gate exits non-zero and leaves the receipt
   untouched. Example: `verify change -- LICENSE` exits 2, and the bytes
   and modification time of `.git/flowseer-verification-receipt` and
   `.git/flowseer-verification-dirty` are unchanged.
3. The last line of every run names the verdict. Example: with a `go` that
   fails `vet`, the last line is `FlowSeer verification FAILED (exit 3) in
   gate: go vet`.
4. No document names the shell verifier as a present file. Example:
   `git grep -n 'verify-change\.sh' -- . ':!docs/plans'` prints only
   lines that hold `861d0994:` or another commit before the path.
5. Both implementations start the same gates. Example: for `--full` in the
   recorded fixture, the argument log of the stub tools is equal line for
   line between the two.
6. A receipt written by either implementation is read by `verify ledger`.
   Example: after a passing run, `verify ledger set U1 passed` takes
   `verified_at` from the receipt and exits 0.

## Out of scope

- The verifier reads the repository's own tree, git's output, and the
  marker the hook writes. Their authors are trusted. A path with a line
  feed or a carriage return is handled as data, not as an attack.
- Removing `jq`, `shellcheck`, and `python3` from the required tools, the
  Windows run, and `.cmd` shims of the web binaries. Phase 7 owns them.
  The suites' stub tools stay `#!/bin/sh` files until then.
- `lib/proc.py`, `lib/hookio.py`, and the other Phase 3 review items.
- The `Verify:` lines of other plans under `docs/plans/`.
- New verifier behavior. A verdict that differs from the shell is a
  defect, apart from the two orderings Decisions names.

## Units

### U1. Verifier suites take the command and cover every class and exit

Files: tools/scripts/tests/verify/support.py, tools/scripts/tests/verify/test_verify_paths.py, tools/scripts/tests/verify/test_verify_selection.py, tools/scripts/tests/verify/test_verify_gates.py, tools/scripts/tests/verify/test_verify_marker.py, tools/scripts/tests/verify/test_ledger.py
After: none
Change: `support.py` holds `implementations()`, the fixture repository
builder the two existing suites each carry, and `Stubs`. `Stubs` writes a
directory of recording tools and returns the environment that puts it
first on `PATH`. Each stub appends its working directory and arguments to
one log. The stubs are `go`, `gofumpt`, `goimports`, `golangci-lint`,
`jq`, `shellcheck`, `docker`, `node`, and the six web binaries. `uv`,
`git`, and `diff` are real. The `go` stub hands `list` and `env` to the
real `go`, writes an executable for `build -o`, copies `generated/` for
`buf generate -o`, and exits with the code in `FLOWSEER_FAKE_FAIL_CODE`
when its first argument equals the value of `FLOWSEER_FAKE_FAIL` (`vet`
for Requirement 3). The `--full` fixture holds a copy of
`tools/test/service-otel-integration.sh` taken from the checkout, because
the shell starts the wrapper by a path relative to the tree it verifies
(`:984`). U5 drops the copy. Every existing case loops over `implementations()`.
All cases pass against the shell verifier, the only implementation yet.
Tests:
- `test_verify_paths.py`: one row per path class 2, 4, 5, 7 to 10, 13,
  14, 16, 17, and 19 through `--print-selection`, with a `.go` file under
  `tools/scripts/` (class 6, not 14) and `src/common/service/a/b.go`
  (class 17). Exit paths 1 to 11, each with its code and last line.
  `test_failing_gate_is_named_in_the_last_line` and
  `test_directory_expands_to_its_files`. A file whose name holds a
  carriage return and whose prose cites a session fails in gate `run.py
  verify check-prose`. A deleted `.agents/skills/x/SKILL.md` selects hook
  tooling.
- `test_verify_gates.py`: recorded runs for classes 1, 3, 6, 11, 12, 15,
  and 20, asserting the log, and the eleven `gates` rows of the Fixes
  table. Exit paths 12 to 19. The hook-tooling fixture holds
  `tools/test/x.sh` and `.agents/skills/x/scripts/y.sh` behind the
  `.claude/skills` link, so each pattern matches and both implementations
  lint the same two files. The parallelism case takes its expected `-p`
  from `os.cpu_count()`.
- `test_verify_marker.py`: the five `marker` rows, a `--full` run that
  removes the marker, a marker line with a carriage return cleared by its
  exact name, the surviving lines sorted by bytes and unique, and exit
  paths 20 and 22.
- `test_ledger.py`: `test_ledger_reads_the_receipt_a_passing_run_wrote`
  per implementation, and the receipt's four lines for a path with a
  space and for `--full`, as literals.
- Nothing in the unit covers exit path 21, a `--base` run over a web
  path, the quoting of a non-ASCII path, or a host where `getconf` and
  `os.cpu_count()` disagree.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/tests/verify`

### U2. The OpenTelemetry suite takes the command

Files: tools/scripts/tests/verify/test_service_otel_integration.py
After: none
Change: the suite reaches the wrapper through a list built in the file:
the shell wrapper while it exists, and `uv run tools/scripts/run.py verify
service-otel-integration` once registered. The scan-error case leaves an
unreadable file in the artifact directory beside the failing `grep` stub,
so one case reaches the error in both implementations. Every case asserts
an empty standard output beside the exact standard error.
Tests: the seven existing cases per implementation, the knob exported
empty taking the 20 second default, and a knob of `1x` refused with exit 1.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/tests/verify/test_service_otel_integration.py`

### U3. `verify service-otel-integration`

Files: tools/scripts/verify/service_otel_integration.py, tools/scripts/verify/__init__.py
After: U2
Change: the command does what the wrapper does, with the probe and the
scan as Decisions gives them, and keeps the wrapper's messages and exit
codes. The unit stops uncommitted for approval by blob hash.
Tests: U2's suite, which now runs two sub-tests per case.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/verify tools/scripts/tests/verify`

### U4. `verify change`

Files: tools/scripts/verify/change.py, tools/scripts/verify/__init__.py, tools/scripts/tests/verify/test_change.py
After: U1, U3
Change: the command is the script as Decisions gives it, with the class
patterns of `:359` and `:376` as they stand. The unit stops uncommitted
for approval by blob hash.
Tests: U1's suites, two sub-tests per case, and Requirement 5's
comparison of the two logs. `test_change.py` calls the module in process:
`affected` over literal rows (a chain of three importers, a test-only
import, a package nobody imports), the `%q` quoting over the six literals
of Decisions and against `bash -c 'printf %q'` for printable ASCII, the
gate label for a `uv run` of `run.py` and for a command with leading
flags, and the verdict line last when a check raises.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/verify tools/scripts/tests/verify`

### U5. The shell verifier and wrapper are deleted

Files: .agents/skills/verify-change/scripts/verify-change.sh, tools/test/service-otel-integration.sh, tools/scripts/verify/change.py, tools/scripts/hooks/stop_check.py, tools/scripts/hooks/pre_tool_policy.py, AGENTS.md, tools/scripts/tests/verify/support.py, tools/scripts/tests/verify/test_verify_selection.py, tools/scripts/tests/verify/test_verify_gates.py, tools/scripts/tests/verify/test_service_otel_integration.py, tools/scripts/tests/hooks/test_pre_tool_policy.py, tools/scripts/tests/hooks/test_stop_check.py
After: U4
Change: both shell files are gone. Class 14 loses `tools/test/*`, and class
17 names `tools/scripts/verify/service_otel_integration.py`. The Stop
hook's message and `AGENTS.md`, Work sequence, name
`uv run tools/scripts/run.py verify change -- <changed paths>`. The policy
surfaces lose the deleted directory. `implementations()` and the wrapper
list return the Python command only, `RunHelperTest` and `WRAPPER_TOOLS`'
shell entries go. The unit stops uncommitted for approval by blob hash.
Tests: the suites pass with one sub-test per case. `test_stop_check.py`
asserts the message's command. `test_pre_tool_policy.py` drops the two
script rows. `test_verify_gates.py` gains the `shellcheck skipped` case.
Verify: `uv run tools/scripts/run.py verify change -- AGENTS.md tools/scripts tools/test .agents/skills/verify-change`

### U6. Documents name the new commands

Files: .agents/skills/verify-change/SKILL.md, .agents/skills/verify-change/references/gate-coverage.md, .agents/skills/plan/SKILL.md, .agents/skills/implement/SKILL.md, .agents/skills/diagnose/SKILL.md, .agents/skills/drive/SKILL.md, .agents/skills/land/SKILL.md, .agents/skills/land/references/retire-plan.md, .agents/skills/steer/SKILL.md, .agents/skills/web-component/SKILL.md, .agents/skills/tune/references/calibration.md, .claude/models/evidence.md, README.md, CONTRIBUTING.md, docs/conventions/testing.md, docs/agent-steering.md, docs/solutions/README.md, docs/solutions/conventions/a-gate-selected-by-name-stops-running-silently.md, docs/solutions/conventions/a-package-behind-a-build-tag-fails-untagged-go-vet.md, docs/solutions/conventions/a-package-rename-moves-every-importers-sort-key.md, docs/solutions/conventions/a-required-state-key-breaks-the-hand-written-fixture-in-the-hook-tests.md, docs/solutions/conventions/go-list-deps-misses-imports-behind-build-tags.md, docs/solutions/conventions/the-buf-cli-version-decides-one-option-byte-in-every-generated-descriptor.md, docs/solutions/conventions/a-false-double-bracket-does-not-stop-set-e-under-bash-3-2.md
After: U4
Change: every file names the commands as Decisions gives them. The
`verify-change` skill's table uses `verify change`, and its background
advice says "the command" where it says "the script".
`docs/agent-steering.md` says under "`verify-change` and the hooks" that
the verifier is Python and where its suites are.
Tests: Requirement 4's command with the pathspec narrowed to the files
above. The whole-tree form holds once U5 has landed too, and Verification
runs it.
Verify: `uv run tools/scripts/run.py verify change -- <the files above>`

Waves: U1 U2 | U3 | U4 | U5 U6

## Verification

- `uv run tools/scripts/run.py test` passes.
- `uv run tools/scripts/run.py verify change --full` passes on the result,
  outside the sandbox with Docker running.
- `git ls-files '*.sh'` lists only paths under `spec/` and `testdata/`.
- Requirement 4's command.

## Definition of done

- [ ] The verifier is green for every changed path of each unit.
- [ ] U3, U4, and U5 were approved by blob hash before their commits.
- [ ] This plan's outcome is recorded with `uv run tools/scripts/run.py
      plan record implemented <plan> --units 6 --from <t> --to <t>` or
      `partial`.
- [ ] No plan labels in code.

## Open questions

- Removing `.agents/skills/verify-change/scripts/` from the policy
  surfaces narrows a guardrail. The plan takes it, and the approver of U5
  sees it in the diff. Unconfirmed.
- `printf %q` for a non-ASCII path depends on the locale. Unverified. No
  script parses the `paths` line.
- Whether bash runs the exit trap on `SIGTERM` is unverified. The port
  prints the verdict line on `KeyboardInterrupt` and does not handle
  `SIGTERM`.
- The Phase 7 plan's Requirement 3 still has all three tools to remove
  after this phase, and `python3` is then required by nothing that calls
  it.
