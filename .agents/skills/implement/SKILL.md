---
name: implement
description: Implements a FlowSeer plan from docs/plans/ or a concrete, already-decided build request end to end, unit by unit, with the repository verifier run on every changed path. Use when asked to implement, build, execute, or work a plan. Not for open-ended bugs (`diagnose`) or for requests that still need design choices.
argument-hint: "[plan path]"
---

# Implement a FlowSeer plan

## Inputs

- A plan path under `docs/plans/`, or a request whose design is settled.
- The current worktree. On a protected branch in the primary checkout, enter a
  worktree first (`AGENTS.md`, Isolation).

## 1. Orient

Read the plan's Goal, Decisions, and Units; the rest when a unit cites it.
When `.claude/skills/plan/scripts/plan_record.py is <plan>
readiness=needs-decisions` succeeds, the plan is not executable:
say why and ask the user whether to run `plan` to settle it (recommended)
or stop here. A parent plan, whose Units name other plan files, is not
executable here either: ask whether to run `drive` on it (recommended,
since it lands each phase before the next starts), implement its ready
phase plan, naming the path, or stop here. Offer `plan` only when no phase
plan is ready and the next one needs re-planning. Check the plan's status with
`.claude/skills/plan/scripts/plan_record.py show <plan>` against the current
tree; the tree wins about what exists. Record a mismatch
in the plan's Open questions before touching code.

After a context compaction, or when resuming a session that planned, re-read
the plan and the ledger before trusting the summary.

### Resume from the ledger

The ledger is `$(git rev-parse --git-dir)/flowseer-plan-status.json`; its
shape is in `verify-change`'s `SKILL.md`. Load `references/resume.md` when it
exists before the first edit. A plan with no ledger gets one, every unit
`pending`, before the first edit; a planless request keeps no ledger. Every
write goes through `.claude/skills/verify-change/scripts/ledger.py`, which
resolves the git directory and recomputes `resume`. Read it with `show` or the
Read tool; never write it by hand.

```bash
.claude/skills/verify-change/scripts/ledger.py init <plan> U1 U2 U3
```

A unit added to the plan after the ledger exists has no entry, and `set`
refuses a unit the ledger does not list. Copy each `passed` unit's
`commit` and `verified_at` from `show` first, then start the ledger over
with every unit in plan order and mark the landed ones again:

```bash
.claude/skills/verify-change/scripts/ledger.py init --force <plan> U1 U2 U3 U4
.claude/skills/verify-change/scripts/ledger.py set U1 passed --commit <commit> --verified-at <verified_at>
```

Carry each unit's `--note` over the same way, and mark each `blocked` unit
again with `ledger.py set <unit> blocked --note <note>`, since a fresh
ledger would otherwise name it as the unit to resume. Do this between units, with
none `in_progress`: a unit marked `in_progress` again takes the current
`HEAD` as its base, and `passed` then no longer counts the commits it made
before.

Read the `docs/architecture/` record for the area, the `CONCEPTS.md` entries
the plan uses, and the conventions for the files you touch: `docs/code-style.md`
for Go and its Testing section for every test, `docs/code-style-proto.md` and
`docs/conventions/protobuf.md` for schema, `docs/conventions/observability.md`
for instrumentation, `docs/doc-style.md` for prose.

Record `git status --porcelain` before the first edit. Leave changes outside
the task alone; at Finish, report anything that appeared since.

## 2. Work the units

Group the units into waves from their `After` lines: a wave is every unit
whose prerequisites have landed. A wave of two or more units runs in workers,
as many at once as `delegate`'s Wave size allows (or the budget a `drive`
brief names). Load `references/workers.md` before the first such wave, and for
any plan for which `.claude/skills/plan/scripts/plan_record.py is <plan>
parent!=null` succeeds. A wave of one unit runs here, except in a plan with a
non-null parent state, which runs it in a worker as well. Running
independent units serially needs a reason in the report, such as no pool with
headroom.

For each unit:

1. Re-read the unit, run `ledger.py set <unit> in_progress`, then inspect the
   current source and tests for its files.
2. Make the smallest change that satisfies it, through the editor tools, which
   run the format and schema hooks a Bash write skips. A Bash command that
   changes `generated/`, a `go.mod` or `go.sum`, or `buf.lock` (`go tool -modfile=tools/buf/go.mod buf generate`,
   `go mod tidy`) marks the tree `<Bash mutation; verify with --full>` and
   turns Finish into a full module race run; any other Bash write is marked by
   path. Search for an existing helper first; no abstraction with a single
   caller. Before calling a third-party API the tree does not already use,
   check its signature: `go doc` for Go, Context7 (`mcp__context7__query-docs`
   or the `ctx7` CLI) for the rest. Before code relies on external behavior
   the plan does not cite, read its source (`AGENTS.md`, Investigation
   discipline) and cite it in the commit message. A fact you cannot check
   is a ruling (`references/rulings.md`): a `Ruled:` line, or the unit
   blocks, and the code that depends on it names the assumption in a test
   or comment. Where the plan and the working code
   disagree about a shape, the code wins: leave the member out and edit the
   plan, with the reason, in the same commit, since the next session reads the
   plan as the specification.
3. Write or extend the tests the unit names, under the Testing rules of
   `docs/code-style.md`. When the unit changes behavior, write the failing
   test first and watch it fail. A new test counts only once watched failing
   against the defect: revert the fix or feed a wrong implementation; in a new
   package, remove the guard the test pins. Undo from a copy taken first
   (`cp <path> "$TMPDIR/<name>.orig"`), never with `git checkout` or
   `git restore`, which discard everything since the last commit. The unit's
   commit body carries one line per new test: the mutation and the quoted
   `--- FAIL` line it produced, since `review` reads the commits, not this
   conversation. A unit without a test needs a stated reason in the plan.
4. In the same unit, update the package README, convention doc, solution
   citations, and any test or benchmark name the unit made false.
5. Check, commit, verify, in that order:
   - Run the focused checks (`go test -race ./<pkg>/...`, `go tool -modfile=tools/buf/go.mod buf lint`), never a
     script under `tools/hooks/` (`delegate`, Write the brief, item 6). Send
     output that may carry diagnostics to a file and grep it after
     (`go test ... > "$TMPDIR/run.log" 2>&1; grep -E '^(FAIL|--- FAIL)' "$TMPDIR/run.log"`),
     never through a filter that drops what it does not match.
   - When the unit adds or removes a name in a repository-wide namespace (an
     error code, a telemetry scope, an event or metric name, a bus subject, a
     bucket), grep the tree for it: no per-package gate sees two owners.
   - Commit the unit, a body of several paragraphs on standard input
     (`git commit -F -`) or as one `-m` each: `-m "a\n\nb"` stores the
     backslashes, not line breaks.
   - Run the verifier for the unit's paths, sandbox disabled, in the
     background while you read on, as the last command of its invocation (a
     trailing `echo` or `tail` reports its own exit code as the gate's):

   ```bash
   .claude/skills/verify-change/scripts/verify-change.sh -- <paths>
   ```

   When a unit leaves the package red until the next unit lands, verify those
   units together and say so.
6. Once that run's last line reads `FlowSeer verification passed.` (quote it
   verbatim in the report), run `ledger.py set U1 passed`, which records `HEAD`
   and the receipt's `verified_at` and moves `resume` on. It refuses a commit
   with nothing committed since the unit went `in_progress`, since a commit
   from an earlier round is not this unit's. Add `--note` only for
   a decision or pitfall the next unit needs, in one line. In Orca, set the
   worktree comment to the unit that landed.

A unit still red after three verifier rounds is `blocked`
(`ledger.py set U1 blocked --note "<reason>"`), and the Finish question offers
taking it back to `plan`. It holds back only the units whose `After` chain
reaches it: every other unit still runs in its wave. Example: with U1
blocked, U5 (`After: none`) runs and U2 (`After: U1`) waits.

Never write plan labels (`U2`, `R4`) or a plan filename into code, comments,
or commit messages.

### Rulings

Load `references/rulings.md` when a unit needs a decision the plan does not
make, or when evidence contradicts a `Ruled:` line already written: ask when the answer changes other units, the wire, or an accepted
record; otherwise rule, record a `Ruled:` line, and continue.

Delegate a bounded read-only question as `delegate` describes when it would
cost more than a few file reads.

## 3. Finish

Load `references/outcome-records.md` when
`.claude/skills/plan/scripts/plan_record.py is <plan> parent!=null` succeeds,
when the request skipped the plan, or when the session runs in Orca.

1. Read the final diff against the plan's Definition of done and
   `docs/code-style.md`, Rules for coding agents. Remove process narration,
   history references, and planning identifiers from comments.
2. Record the outcome in the reference's records. Planless work stops at
   that and has no plan to edit. With a plan, record a finished run with
   `.claude/skills/plan/scripts/plan_record.py implemented <plan> --units <n>
   --from <t> --to <t> [--landed <first>..<last>]`, or an open run with
   `.claude/skills/plan/scripts/plan_record.py partial <plan> --units <n>
   --from <t> --to <t> --note <units left and why>`. Read the arguments from
   the ledger: `--units` is the count of `passed` units, and `--from` and
   `--to` are the first and last of their `verified_at` values. A phase's
   `--landed` range runs from the first unit's commit to the last unit's
   commit, the one the ledger records, and no parent edit is needed. Commit
   the state on its own after the last unit's commit. Never amend it into
   that commit, which would replace the SHA the ledger and the landed range
   name with one that is no ancestor of `HEAD`. Leave the ledger in place
   for `land`.
3. Run the verifier as the last action of the task, sandbox disabled, since
   `land` refuses a receipt older than the last commit: `--base main -- <paths>`
   when the worktree holds unrelated changes, `--base main` alone otherwise.
   Never against `HEAD`, whose diff after the commit is empty and hides tests
   deleted units ago. When `$(git rev-parse --git-dir)/flowseer-verification-dirty`
   still holds the `<Bash mutation; verify with --full>` line after the
   `--base main` run, or the run stops on a `.golangci.yml` change and asks
   for `--full`, run `--full`. Quote the run's last line. Anything other
   than `FlowSeer verification passed.` blocks the report.
4. With a plan, read the deviations off the tree, not from memory (any edit
   this prompts goes back to item 1). Planless work has no units to compare
   and skips this item:

   ```bash
   .claude/skills/implement/scripts/plan-deviations.py <plan> main -- <paths>
   ```

   Report every path under "Changed, named by no unit" and every entry under
   "Named by a unit, unchanged" with its reason. Quote the verifier's
   `Test changes to account for:` block, when printed, with one reason per line.

Report, outcome first: rulings, units done with the waves they ran in,
commands run with results, the deviation and test-change lists with their
reasons, residual risk.

End by asking the user what happens next (`AGENTS.md`, Agent behavior): run
`review` on the branch now (recommended when every unit landed); continue with
the remaining units, naming them; take a `blocked` unit back to `plan`
(recommended over any further patch, which the three-round cap forbids); stop
here. Review runs only on that answer. A correction to this procedure is
logged as `compound`, Observe describes.
