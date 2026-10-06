---
title: Python Scripts Under uv Phase 2, Existing Python - Plan
type: refactor
date: 2026-10-05
artifact_contract: flowseer-plan/v2
execution: mixed
---

# Python Scripts Under uv Phase 2, Existing Python - Plan

## Goal

Every Python script under `.agents/skills/*/scripts/` is a registered command
of `tools/scripts/run.py`, its tests run under `uv run tools/scripts/run.py
test`, and no skill directory holds a `.py` file. The 28 files are the ones
`git ls-files '.agents/skills/*/scripts/*.py'` lists at `a4f3cf7c`, 13
scripts and 15 suites. The means: the registry learns the `skills/` layout
the scripting record names, the plan-state reader and a cross-platform lock
move into `lib/`, and the scripts move group by group with every caller.

**Stop condition:** a moved command cannot run from a fixture repository
that has no `tools/scripts/` of its own, as `test_verify_paths.py` and
`tools/hooks/tests/run.sh` do. The verifier then cannot call its checks
through `run.py`, and the call shape in U5 is wrong.

## Decisions

The parent plan's Decisions and the
[repository scripting record](../architecture/2026-10-05-repository-scripting-direction.md)
apply. Local to this phase:

- Skill groups live at `tools/scripts/skills/<skill>/`, and `run.py`
  collects groups there and from `tools/scripts/<group>/`. It reads each
  `COMMANDS` by parsing `__init__.py` with `ast` and imports only the
  dispatched module. Why: the record puts "one package per skill" under
  `skills/`, `tools/hooks/tests/run.sh:287` pins `tools/scripts/skills/x.py`
  as no policy surface, and `run.py:30` would read `skills/` as one group.
  Phase 1's review found that one group's import error stops every command
  and that a computed mapping passes (Phase 1 plan, Review gaps).
- The plan-state reader moves to `lib/plans.py`, and `plan_record.py`'s
  transitions become `plan record`. `plan_record.py check` becomes
  `verify check-plan-state`, with the same arguments and output. Why: the
  verifier, `drive`, and `next` import the reader (`check-plan-status.py:31`,
  `plan-state.py:21`, `plan-queue.py:48`), the record makes what the
  verifier runs a policy surface, and `plan_record.py:5` to `:6` keep one
  writer of the state file.
- Three Markdown readers remain, since `plan-state.py` and
  `check-plan-status.py` read state through `plan_record.py` and no script
  reads `After:` or `Landed:`. Rulings:
  - Unit headings. `plan-queue.py:56` matches `^### U\d+[a-z]*[.:]` and
    `plan-deviations.py:45` matches `^###\s+(U\d+[a-z]?)[.:]\s`.
    `lib/plans.py` matches `^### (U\d+[a-z]?)[.:]\s`, so `### U1ab.`
    (queue only), `###  U1.` (deviations only), and `### U1.Name` (queue
    only) count for neither. Why: every heading under `docs/plans/` is
    `### U<n>. ` or `### U3a. ` to `### U3d. `, and `plan/SKILL.md`, step
    3, says to copy that shape. Fenced blocks are not skipped, as today.
  - Frontmatter. `check-prose.py:107` keeps its own reader, since it blanks
    the frontmatter of every Markdown file, while `plan_record.py:235` reads
    plan keys.
  - `Files:` entries have one reader and stay in `plan_deviations`. The
    parent's U2 line is amended to match.
- The ledger contract lives once in `lib/plans.py` as `LEDGER_NAME`,
  `LEDGER_CONTRACT`, and `UNIT_STATUSES`. Why: `ledger.py:42` to `:44` and
  `check-plan-status.py:27` to `:29` define it, and their `CONTRACT` and
  `STATUSES` names collide with the plan-state ones at `plan_record.py:52`
  and `:55`.
- `lib/lock.exclusive(path)` opens the file for appending and yields the
  descriptor locked. On POSIX it calls `fcntl.flock` on that descriptor, as
  `runlog.py:98` does, so a checkout still on the old `runlog.py` excludes
  the new one. On Windows it locks byte 0 of a sidecar `<path>.lock` with
  `msvcrt.locking`. Why: a Windows lock covers the region that "extends from
  the current file position for nbytes bytes"
  (https://docs.python.org/3.13/library/msvcrt.html), and whether it blocks
  a reader of those bytes is unverified. `LK_LOCK` raises `OSError` after
  10 attempts one second apart (same page), so `exclusive` retries that
  timeout and re-raises any other error.
- `runlog.py` reads the last byte with `os.lseek` and `os.read`, since
  `runlog.py:100` calls `os.pread`, whose availability is "Unix"
  (https://docs.python.org/3.13/library/os.html). `merge-check.py:35`
  hashes empty standard input with `git hash-object -t tree --stdin`,
  which needs no device path. Whether Git for Windows maps `/dev/null` is
  unverified. On git 2.50.1 both forms print
  `4b825dc642cb6eb9a060e54bf8d69288fbee4904`.
- `orca-worker.sh` reads the run log through two new `runlog` subcommands,
  `last-start` and `has-grade`, in place of the inline `python3 -c` at
  `:123` and `:519`. Why: `runlog` imports `lib.lock` after the move, and
  the host `python3` (3.14 on the development host) is no interpreter the
  scripts declare.
- A shell caller finds `run.py` as `$(git -C "$script_dir" rev-parse
  --show-toplevel)/tools/scripts/run.py` and starts it with `uv run
  --quiet`. Why: the verifier and its tests run from fixture repositories,
  and `$script_dir/../../..` is not the root through the `.claude/skills`
  link (`verify-change.sh:88` keeps the logical path). The Phase 1 call at
  `verify-change.sh:634` is relative and fails from a fixture. Several
  callers capture stderr with stdout (`run.sh:1043`, `orca-worker.sh:288`),
  and whether `uv run` writes notices to stderr at default verbosity is
  unverified. `-q, --quiet` is in `uv run --help` for 0.12.23.
- A command's `main` takes the arguments after the command name and returns
  an int, `argparse` gets `prog="run.py <group> <command>"`, and a
  `SystemExit` passes through `run.py`. Why: `plan_record.py:674`,
  `plan-queue.py:188`, and `check-prose.py:155` read their arguments three
  ways, and one shape needs no rewrite of their exit paths.
- A test starts a command as `[sys.executable, <run.py>, <group>,
  <command>]`. A test of a shell script not yet ported finds it through
  `lib.repo.root(Path(__file__).parent)` at its current path. Why:
  `sys.executable` is the interpreter `run.py test` runs under, and Phases 3
  to 5 move the shell.
- U6 updates the prose after U3 to U5 land. Why: the reference files are
  shared across the moves, so per-unit edits would serialize wave 2. The
  phase merges once (parent, `## Branch`), so `main` never names a deleted
  path. From the unit that moves a command, the implementer runs the new
  command (`verify ledger`, `plan record`, `implement plan-deviations`)
  where a skill still names the old path.
- Phase 1 review gaps planned here: `run.py:73`, `:52`, `:36`, `:32`, and
  `:9`, `test_run.py:25`, `:28`, `:35`, and `:41`, `verify-change.sh:304`,
  `:634`, and `:987`, `run.sh:291`, `code-style-python.md:17` and `:33`,
  verify-change `SKILL.md:46`, and `gate-coverage.md:34`.

## Requirements

1. Each moved command answers as before. Example: on the tree before U5,
   `uv run tools/scripts/run.py next plan-queue --json` prints what
   `python3 .claude/skills/next/scripts/plan-queue.py --json` printed.
2. No tracked `.py` file remains under `.agents/skills/`. Example:
   `git ls-files '.agents/skills/**/*.py'` prints nothing.
3. No module under `tools/scripts/` outside `tests/` and `lib/lock.py`
   imports `fcntl` or `msvcrt` or calls `os.pread`. Example: adding `import
   fcntl` to `skills/delegate/runlog.py` fails `test_lock.py`.
4. The lock excludes a second writer. Example: two processes each writing
   500 lines, each line in two `os.write` calls under `exclusive`, leave
   1,000 whole lines.
5. The suite count does not drop. Example: `run.py test` reports at least
   the sum of the `Ran N tests` lines the per-skill loop prints at
   `a4f3cf7c`, plus the 18 tests of `tools/scripts/tests/`.
6. The registry reads skill groups. Example: `uv run tools/scripts/run.py
   list` prints `land merge-check` and `verify check-plan-state`.
7. A malformed registry is named, and a broken command module stops only
   its own command. Example: `COMMANDS = dict(a="a")` in a fixture group
   makes every command exit 2 naming the file, and a command module that
   raises on import leaves `list` and the other groups working.
8. The verifier calls its checks from any working directory. Example:
   `verify-change.sh` started through `.claude/skills/verify-change/scripts/`
   in a fixture repository names a failing prose check as
   `in gate: run.py verify check-prose`.

## Out of scope

- Porting shell. `orca-worker.sh` changes only its calls to moved commands.
- `docs/solutions/` and `docs/architecture/` lines that name old paths,
  since they quote evidence at a commit, and `.claude/models/evidence.md`,
  whose lines are dated.
- Phase 1 gaps no unit touches: the `uv.toml` type lists in
  `mark-verification-dirty.sh:74` and `tree-state.sh:23` (Phase 3 ports
  both), `test_proc.py:19`, and the exit 0 on a missing path in
  `check_markdown_links.py:39`, which `check-prose.py:165` shares and
  carries into `verify/` unchanged.
- Running the Windows branch of `lib/lock.py`. Phase 7 does.
- The commands read arguments from a contributor or an agent runtime and
  files from the repository's own tree. Both are trusted.

## Units

### U1. Registry for skill groups

Files: tools/scripts/run.py, tools/scripts/skills/__init__.py, tools/scripts/tests/test_run.py, tools/scripts/tests/test_compile.py, tools/scripts/tests/skills/__init__.py
After: none
Change: `run.py` collects groups from `tools/scripts/<g>/` (not `lib`,
`tests`, or `skills`) and `tools/scripts/skills/<g>/`. It accepts a
`COMMANDS` that is a module-level dict literal of string to string, and
anything else, or a group name in both places, exits 2 naming the file. It
imports `<g>.<module>` or `skills.<g>.<module>` only to dispatch.
`collect_groups`, `run_tests`, and `main` take the scripts root as a
parameter. `test` exits 2 naming a directory under its start that holds
`test_*.py` and no `__init__.py`. The comment at `run.py:9` says that
`.gitignore` hides `__pycache__/` and the flag keeps the directories from
collecting in every checkout. The `run.py` edits prompt the person.
Tests: `test_run.py` drops the exact live list at `:33` for a check that the
live list holds `list`, `test`, and `verify check-markdown-links`. Fixture
roots cover: sorted lines from an unsorted literal, a command receiving its
arguments, an unknown group printing the registry, `my_group` dispatched as
`my-group`, a computed mapping and a duplicate name each exiting 2, a
failing command module leaving `list` working, a test directory without
`__init__.py` exiting 2, and `test <empty dir>` exiting 1 with `Ran 0 tests`
while the fixture's own `tests/` holds one passing test.
`../tools/scripts/run.py list` from `docs/` exits 0.
`test_registry_modules_import` covers skill groups. `test_compile.py`
compiles every `.py` file under `tools/scripts/` with `compile()`, naming a
failing file, and a case feeds its helper a syntax error.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts`

### U2. Plan-state and lock library

Files: tools/scripts/lib/plans.py, tools/scripts/lib/lock.py, tools/scripts/tests/lib/test_plans.py, tools/scripts/tests/lib/test_lock.py
After: none
Change: `lib/plans.py` holds the read side of `plan_record.py`: the
constants at `:52` to `:77` except `LEDGER_NAME`, the ledger contract
under the names in Decisions, the `StateError` family, `plan_of`,
`state_path`, the `is_*` validators, `shape_faults`, `combination_faults`,
`moved_keys`, `read_tree`, `problems`, `load`, `computed_status`, `status`,
`on_main`, `sent_back`, and `finished`. Git goes through `lib.proc`, and the
root through `lib.repo.root`, whose `RuntimeError` becomes `StateError`.
The messages at `plan_record.py:262` and `:287` name `uv run
tools/scripts/run.py plan record init`. It adds `UNIT`, `unit_ids`, and
`title`. `lib/lock.py` holds `exclusive` as Decisions describe.
`plan_record.py` stays untouched until U5. Both files prompt the person.
Tests: `test_plans.py` counts `### U1. a`, `### U12: b`, and `### U3a. c`,
skips `### U1ab. d`, `###  U1. e`, and `### U1.f`, reads a title without
` - Plan`, finds `status` with `moved_keys`, and checks that `load` without
a state file raises `MissingState` naming the new command. The ledger note
records that every plan under `docs/plans/` counts the same units under the
new rule and both old ones. `test_lock.py` covers Requirement 4 and scans
the non-test modules with `ast` for Requirement 3. Nothing runs the
`msvcrt` branch.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts/lib tools/scripts/tests/lib`

### U3. Delegate and tune commands

Files: tools/scripts/skills/delegate/__init__.py, tools/scripts/skills/delegate/model_check.py, tools/scripts/skills/delegate/runlog.py, tools/scripts/skills/tune/__init__.py, tools/scripts/skills/tune/catalogue.py, tools/scripts/skills/tune/field.py, tools/scripts/tests/skills/delegate/__init__.py, tools/scripts/tests/skills/delegate/test_model_check.py, tools/scripts/tests/skills/delegate/test_runlog.py, tools/scripts/tests/skills/delegate/test_orca_worker.py, tools/scripts/tests/skills/delegate/test_pool_usage.py, tools/scripts/tests/skills/delegate/testdata/fallback-records.jsonl, tools/scripts/tests/skills/tune/__init__.py, tools/scripts/tests/skills/tune/test_field.py, tools/scripts/tests/skills/tune/test_bench.py, .agents/skills/delegate/scripts/model_check.py, .agents/skills/delegate/scripts/runlog.py, .agents/skills/delegate/scripts/test_model_check.py, .agents/skills/delegate/scripts/test_runlog.py, .agents/skills/delegate/scripts/test_orca_worker.py, .agents/skills/delegate/scripts/test_pool_usage.py, .agents/skills/delegate/scripts/testdata/fallback-records.jsonl, .agents/skills/delegate/scripts/orca-worker.sh, .agents/skills/delegate/references/pool-rows.md, .agents/skills/tune/scripts/catalogue.py, .agents/skills/tune/scripts/field.py, .agents/skills/tune/scripts/test_field.py, .agents/skills/tune/scripts/test_bench.py
After: U1, U2
Change: `delegate model-check`, `delegate runlog`, `tune catalogue`, and
`tune field` exist, and the old files are deleted. `runlog` appends through
`lib.lock.exclusive` and reads the last byte as Decisions say. `runlog
last-start --run <id>` prints the model and time of the run's last start
event, tab-separated and empty when there is none, and `runlog has-grade
--run <id>` exits 0 when a grade event exists and 1 otherwise.
`orca-worker.sh` sets `run_py`, requires `uv`, and calls `uv run --quiet
"$run_py" delegate ...` at `:123`, `:127`, `:198`, `:288`, `:509`, `:519`,
and `:545`, capturing only standard output at `:288`. It keeps `python3`
for `:34`, `:36`, and `:191`. `field.py` imports `skills.delegate.runlog`
and `skills.tune.catalogue` and drops `:13` to `:19`. The link at
`pool-rows.md:131` names the moved test.
Tests: the moved suites pass. `test_runlog.py` covers `last-start` and
`has-grade`, and `test_orca_worker.py` their callers. The lanes that set
`HOME` (`test_orca_worker.py:605` to `:607`) pass `UV_CACHE_DIR` and
`UV_PYTHON_INSTALL_DIR` resolved from the outer home, both listed in uv
0.12.23's help with defaults `~/.cache/uv` and `~/.local/share/uv/python`.
The ledger note records each suite's `Ran` count before and after.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts .agents/skills/delegate .agents/skills/tune`

### U4. Land merge check

Files: tools/scripts/skills/land/__init__.py, tools/scripts/skills/land/merge_check.py, tools/scripts/tests/skills/land/__init__.py, tools/scripts/tests/skills/land/test_merge_check.py, .agents/skills/land/scripts/merge-check.py, .agents/skills/land/scripts/test_merge_check.py
After: U1
Change: `land merge-check` exists and the old files are deleted.
`empty_tree` uses `--stdin` as Decisions say.
Tests: the moved suite imports `skills.land.merge_check` in place of
`test_merge_check.py:15`, and a case asserts the empty tree hash.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts .agents/skills/land`

### U5. Plan and verifier commands

Files: tools/scripts/skills/plan/__init__.py, tools/scripts/skills/plan/record.py, tools/scripts/skills/drive/__init__.py, tools/scripts/skills/drive/plan_state.py, tools/scripts/skills/next/__init__.py, tools/scripts/skills/next/plan_queue.py, tools/scripts/skills/implement/__init__.py, tools/scripts/skills/implement/plan_deviations.py, tools/scripts/verify/__init__.py, tools/scripts/verify/check_plan_state.py, tools/scripts/verify/check_plan_status.py, tools/scripts/verify/check_test_integrity.py, tools/scripts/verify/ledger.py, tools/scripts/verify/check_prose.py, tools/scripts/tests/lib/test_plans.py, tools/scripts/tests/skills/plan/__init__.py, tools/scripts/tests/skills/plan/test_record.py, tools/scripts/tests/skills/drive/__init__.py, tools/scripts/tests/skills/drive/test_plan_state.py, tools/scripts/tests/skills/drive/test_successor.py, tools/scripts/tests/skills/next/__init__.py, tools/scripts/tests/skills/next/test_plan_queue.py, tools/scripts/tests/skills/implement/__init__.py, tools/scripts/tests/skills/implement/test_plan_deviations.py, tools/scripts/tests/verify/test_check_plan_state.py, tools/scripts/tests/verify/test_ledger.py, tools/scripts/tests/verify/test_check_prose.py, tools/scripts/tests/verify/test_verify_paths.py, .agents/skills/plan/scripts/plan_record.py, .agents/skills/plan/scripts/test_plan_record.py, .agents/skills/drive/scripts/plan-state.py, .agents/skills/drive/scripts/test_plan_state.py, .agents/skills/drive/scripts/test_successor.py, .agents/skills/next/scripts/plan-queue.py, .agents/skills/next/scripts/test_plan_queue.py, .agents/skills/implement/scripts/plan-deviations.py, .agents/skills/implement/scripts/test_plan_deviations.py, .agents/skills/prose/scripts/check-prose.py, .agents/skills/prose/scripts/test_check_prose.py, .agents/skills/verify-change/scripts/check-plan-status.py, .agents/skills/verify-change/scripts/check-test-integrity.py, .agents/skills/verify-change/scripts/ledger.py, .agents/skills/verify-change/scripts/test_ledger.py, .agents/skills/verify-change/scripts/test_verify_paths.py, .agents/skills/verify-change/scripts/verify-change.sh, tools/hooks/tests/run.sh, tools/hooks/pre-tool-policy.sh, AGENTS.md
After: U1, U2
Change: `plan record`, `drive plan-state`, `next plan-queue`, `implement
plan-deviations`, `verify check-plan-state`, `verify check-plan-status`,
`verify check-test-integrity`, `verify ledger`, and `verify check-prose`
exist, the readers import `lib.plans`, and the old files are deleted.
`plan record` has no `check`. `verify-change.sh` sets `run_py` and calls
`uv run --quiet "$run_py" verify ...` at `:590`, `:594`, `:610`, `:612`,
`:634`, and `:635`. `required_tools` starts as `(uv go)` and adds `python3`
in the `modules` and `hook_tooling` branches, since the Go gate (`:683`),
`run.sh:267`, and the shell scripts the suites run still need it. The skill
loop at `:956` to `:977` and the `py_compile` at `:987` are removed.
`gate_label` names a `run.py` call as `run.py <group> <command>`. `run.sh`
starts the moved commands through `uv run --quiet
"$repo_root/tools/scripts/run.py"` at `:1015` to `:1025`, `:1073`, `:1113`,
`:1175` to `:1196`, and `:1199` to `:1228`, and the comment at `:1090` names
`plan record`. Its policy list at `:278` names
`.claude/skills/verify-change/scripts/verify-change.sh` and drops the prose
script and `ledger.py`, and the loop at `:287` to `:292` calls
`assert_allow`. `AGENTS.md` and `pre-tool-policy.sh:54` and `:58` to `:60`
drop `.agents/skills/prose/scripts/`. Edits to the verifier, `run.sh`,
`pre-tool-policy.sh`, `AGENTS.md`, and `verify/` prompt the person.
Tests: the moved suites import their modules in place of
`spec_from_file_location`. The reader cases of `test_plan_record.py` move
to `tests/lib/test_plans.py` and its `check` cases (`:162`, `:269`, `:342`
to `:431`) to `test_check_plan_state.py`. `test_verify_paths.py` finds the
verifier through `lib.repo.root` and gains two cases:
`tools/scripts/README.md` prints `hook_tooling=true` under
`--print-selection`, and Requirement 8, run with the `go` stub of
`test_format_failure_names_its_gate`. The `run.sh` cases pass. Before the
old readers go, the ledger note records that `plan-queue.py` and
`plan-state.py` print what the new commands print on the same tree.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts .agents/skills tools/hooks AGENTS.md`

### U6. Skill and document references

Files: .agents/skills/compound/SKILL.md, .agents/skills/delegate/SKILL.md, .agents/skills/delegate/references/no-orca.md, .agents/skills/delegate/references/review-lanes.md, .agents/skills/diagnose/SKILL.md, .agents/skills/drive/SKILL.md, .agents/skills/implement/SKILL.md, .agents/skills/implement/references/outcome-records.md, .agents/skills/implement/references/resume.md, .agents/skills/implement/references/workers.md, .agents/skills/land/SKILL.md, .agents/skills/land/references/missing-checkpoint.md, .agents/skills/land/references/retire-plan.md, .agents/skills/next/SKILL.md, .agents/skills/plan/SKILL.md, .agents/skills/plan/references/phases.md, .agents/skills/plan/references/replan-implemented.md, .agents/skills/plan/references/replan-phase.md, .agents/skills/prose/SKILL.md, .agents/skills/review/SKILL.md, .agents/skills/review/references/fix-loop.md, .agents/skills/review/references/subject-review.md, .agents/skills/steer/SKILL.md, .agents/skills/tune/SKILL.md, .agents/skills/tune/references/calibration.md, .agents/skills/verify-change/SKILL.md, .agents/skills/verify-change/references/gate-coverage.md, docs/README.md, docs/agent-steering.md, docs/agent-observations.md, docs/doc-style.md, docs/code-style-python.md
After: U3, U4, U5
Change: every line that starts or links a moved script names its command
(`uv run tools/scripts/run.py plan record show <plan>`) or its new path,
`plan_record.py check` reads `verify check-plan-state`, and the
verify-change `SKILL.md` lines that name `scripts/<file>.py` (`:119`,
`:123`, `:172`) follow. The verify-change `SKILL.md` describes
`hook_tooling=true` under `--print-selection`. `gate-coverage.md` says the
Markdown checks need `uv` and the suite compiles every module.
`code-style-python.md` adds the `skills/<skill>/` layout, the `main`
argument rule, the module-level rule scoped to command and library modules
with the `run.py` bytecode flag as its one exception, and the once-per-host
`uv python install 3.13`.
Tests: `grep -rnE '(model_check|runlog|catalogue|field|merge-check|plan-state|plan-queue|plan-deviations|plan_record|check-prose|check-plan-status|check-test-integrity|ledger|test_[a-z_]+)\.py' .agents docs AGENTS.md tools --exclude-dir=plans --exclude-dir=solutions --exclude-dir=architecture --exclude-dir=research`
prints only paths under `tools/scripts/`, and the Markdown checks pass.
Verify: `.claude/skills/verify-change/scripts/verify-change.sh -- .agents/skills docs/README.md docs/agent-steering.md docs/agent-observations.md docs/doc-style.md docs/code-style-python.md`

Waves: U1 U2 | U3 U4 U5 | U6

## Verification

- `uv run tools/scripts/run.py test` passes with at least Requirement 5's
  count, and `list` names the 13 moved commands, `verify check-plan-state`,
  and `verify check-markdown-links`.
- `git ls-files '.agents/skills/**/*.py'` prints nothing.
- `tools/hooks/tests/run.sh` passes.
- `.claude/skills/verify-change/scripts/verify-change.sh -- tools/scripts .agents/skills tools/hooks AGENTS.md docs`
  passes.

## Definition of done

- [ ] Verifier green for every changed path.
- [ ] `AGENTS.md`, every `SKILL.md`, and `docs/code-style-python.md` name
      what landed.
- [ ] The ledger notes hold the `Ran` counts and the comparisons the units
      name.
- [ ] Outcome recorded with `uv run tools/scripts/run.py plan record
      implemented <plan> --units 6 --from <t> --to <t> --landed
      <first>..<last>`.
- [ ] No plan labels in code.

## Open questions

- Which `errno` does `msvcrt.locking` set when `LK_LOCK` gives up?
  Unverified. U2 retries that one error, and Phase 7 confirms it on Windows.
- Does `uv run` 0.12.23 write to stderr at default verbosity? Unverified.
  The sandbox here refuses its cache, so no run could check it. `--quiet`
  covers either answer.
