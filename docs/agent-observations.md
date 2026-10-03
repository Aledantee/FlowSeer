# Agent observations

Entries here record where a FlowSeer skill, agent definition, or hook did not
fit the work: a correction the user had to make to the procedure, a step that
was skipped or done by hand, a brief that missed something, a sequence that
recurs with no skill behind it. The `compound` skill's Observe mode appends
them. Nothing reads this file on its own; the `steer` skill works the queue
when a maintainer asks for it.

`steer` applies or rejects each entry through the change process in
[`agent-steering.md`](agent-steering.md) and deletes it. Edits to a hook, a
hook registration, or `AGENTS.md` stop at a staged diff for a person's
review, since those are policy surfaces. An entry that sits here is not a
rule; the skill or hook it names stays authoritative until it changes. A
lesson about the code belongs under [`solutions/`](solutions/README.md),
not here.

Entry format:

```markdown
## <YYYY-MM-DD> <skill>: <one-line title>
Skill or agent: <path and step>, or `new skill candidate: <working name>`.
What happened: <what was corrected or did not fit, and whether the step
was followed as written>.
Suggested change: <smallest edit to the skill, agent, or hook>.
```

## Entries

## 2026-10-02 hooks: proto source guard allows every dotfile
Skill or agent: `tools/hooks/pre-tool-policy.sh`, the `spec/proto/` branch,
and `test/conformance/proto/layout_test.go`, `protoPathViolation`.
What happened: both checks explicitly allow every dotfile, including a hidden
script such as `.audit.sh`. The tests pin that allowance, while `AGENTS.md`,
Hard boundaries, permits only `.proto` files and package-boundary `README.md`
files. The enforced source-only rule therefore has an exception its authority
does not grant.
Suggested change: remove the dotfile allowance in both checks and pin the
rejection in the hook suite and conformance cases. The change is staged in
`tools/hooks/pre-tool-policy.sh`, `tools/hooks/tests/run.sh`, and
`test/conformance/proto/layout_test.go` for guardrail review. It remains pending
until that review accepts it.

## 2026-10-02 delegate: pool rows carry no account plan, so a percent window reads as equal headroom on every plan
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Pick the role, then
resolve the lane" step 1, Wave size, and Dispatch by quota, with
`.claude/skills/delegate/scripts/pool-usage.sh` and
`.claude/skills/tune/scripts/discover-host.sh`, which write the pool rows.
What happened: the `codex` row read `signed_in: true` with its only window at
0%, so a review wave gave the pool two slots. The account was on the free
plan. Codex answered `400 The 'gpt-6.1-sol' model is not supported when using
Codex with a ChatGPT account`, then the same for `gpt-6-sol`, and four lanes
were started and stopped before the user said to avoid the pool. The steps
were followed as written. The user's correction: check which kind of account
each pool is signed in to, `codex` and `claude` included, because half of a
20x plan's window is far more capacity than 80% of a standard plan's.
Suggested change: `pool-usage.sh` and `discover-host.sh` record each pool's
account plan in its row. `delegate` step 1 drops a model the plan does not
serve. Wave size counts slots from the capacity left, the plan's multiplier
times the unused share of the window, instead of from the percent alone.

## 2026-10-03 review: the round cap and the fourth round disagree
Skill or agent: `.claude/skills/review/references/fix-loop.md`, When to stop.
What happened: the cap reads "A review runs at most three rounds in total",
and the same bullet says "A fourth round runs only on that answer". A
fourth round that is not clean has no stated outcome. Under `drive` the
same answer starts a new review with a fresh count, while interactively it
is one more round. The step was followed as written and gives two readings.
Suggested change: make the user's answer start a new review, as "one more
gap pass" does, or state the outcome of a fourth round that is not clean.

## 2026-10-03 land, next: a `fixes needed` plan is sent to review step 6, not step 1
Skill or agent: `.claude/skills/land/SKILL.md`, the missing-checkpoint
table, and `.claude/skills/next/SKILL.md`, the `unchecked` row.
What happened: both send a plan whose verdict is `fixes needed` to
"`review`, step 6". `review` step 1 now reads the record of open items and
calls every run on a scope with a recorded verdict a new review. A session
entering at step 6 skips that read and has no stated round count.
Suggested change: both rows say to run `review` from step 1 with step 6.

## 2026-10-03 review: the record and the verdict are lost on two worker paths
Skill or agent: `.claude/skills/drive/references/parking.md` and
`.claude/skills/land/references/missing-checkpoint.md`.
What happened: the record of open review items travels only with the plan
file or the checkpoints file. A phase of a parent that `drive` parks
without merging the stage worker's branch loses it, and so does a planless
review run in a `land` worker's own git directory. The next review there
starts fresh. Both paths lose behavior findings the same way.
Suggested change: merge or copy the verdict commit and the checkpoints
lines back before the worker's checkout is released.

## 2026-10-03 delegate: a reviewer on a pool CLI ran the verifier and sat idle
Skill or agent: `.claude/skills/delegate/SKILL.md`, Write the brief, items
4 and 6.
What happened: a `review-unit` lane on `agy` started
`verify-change.sh --base main` as a background task before reviewing and
then waited on it. `orca-worker.sh wait` printed `idle` twice with no
report written, and the lane continued only after a `tell`. Item 6 keeps
the verifier from a unit worker and says nothing for a lane that returns a
report. The brief named no check the coordinator had already run.
Suggested change: item 6 says a report lane runs no verifier, and item 4's
"name the checks the coordinator already ran" is required for review lanes.

## 2026-10-03 hooks: the verifier scripts are not a policy surface
Skill or agent: `tools/hooks/pre-tool-policy.sh`, the policy-surface case,
and `AGENTS.md`, Hard boundaries.
What happened: the hook asks for approval on `AGENTS.md`, `buf.yaml`,
`.golangci.yml`, `.claude/settings.json`, `.codex/hooks.json`,
`tools/hooks/*`, and `test/conformance/a11y/*`. It does not match
`.agents/skills/verify-change/scripts/` or the other gate scripts under
`.agents/skills/*/scripts/`, so an agent can edit the verifier it must pass
with no prompt. The hook's list and the `AGENTS.md` list also differ:
`.golangci.yml` and `test/conformance/a11y/` appear only in the hook.
Suggested change: add the gate scripts to the hook's case and pin it in
`tools/hooks/tests/run.sh`, and make the `AGENTS.md` list name the same
paths. Both are policy surfaces and stop at a staged diff.

## 2026-10-03 land, drive: the model may invoke skills that merge
Skill or agent: `.claude/skills/land/SKILL.md` and
`.claude/skills/drive/SKILL.md`, frontmatter.
What happened: neither sets `disable-model-invocation`.
`https://code.claude.com/docs/en/skills.md` recommends
`disable-model-invocation: true` "for workflows with side effects or that
you want to control timing". Setting it on `land` would stop `drive` from
loading `land`, so the rule "commit or merge only on that answer" in
`AGENTS.md`, Work sequence, holds by prose alone.
Suggested change: decide and record in `docs/agent-steering.md` whether
`land` stays model-invocable. If it does, name the reason there.

## 2026-10-03 tune, delegate: the committed registry is the full registry
Skill or agent: `.claude/skills/tune/SKILL.md`, the files table, and
`.claude/skills/delegate/SKILL.md`, the registry read.
What happened: `tune` calls `.claude/models/registry.yaml` "Overrides for
that project, committed". The file is 28 KB, lists every pool and model
with calibration records, and carries one person's subscriptions in its
`pools` comments. `delegate` reads it on every dispatch.
Suggested change: keep only project overrides in the committed file
(`sensitive_paths`, fit-set changes) and move calibration records to
`evidence.md`, or change the table row to say what the file holds.

## 2026-10-03 skills: descriptions fill the listing budget on a small context
Skill or agent: every `SKILL.md` description under `.agents/skills/`.
What happened: the 14 descriptions total 6,236 characters.
`https://code.claude.com/docs/en/skills.md` says the listing budget "scales
at 1% of the model's context window" and that overflow drops descriptions
"starting with the skills you invoke least". On a 200,000-token worker the
project descriptions take most of that budget before any global or plugin
skill is listed. The character size of the budget is unverified.
Suggested change: move the ordering detail out of the `next`, `drive`,
`steer`, and `land` descriptions into their bodies, or set
`skillListingBudgetFraction` in `.claude/settings.json` (a policy surface).

## 2026-10-03 skills: references nest two levels and long ones lack contents
Skill or agent: `.claude/skills/tune/references/calibration.md`, and the
references of `drive`, `land`, `review`, and `web-component`.
What happened: the files under `tune/references/calibration/` are named
only in `calibration.md`, not in `tune/SKILL.md`. Four more reference
files link to other references (`drive/references/concurrent-phases.md`,
and `missing-checkpoint.md`, `orca-cleanup.md`, `retire-plan.md` under
`land`). `review/references/fix-loop.md` (211 lines), `calibration.md`
(251), and `web-component/references/overlays-and-motion.md` (168) have no
contents list. The one-level and contents rules are attributed to the
skill authoring best-practices page that `docs/agent-steering.md` cites.
That attribution is unverified here.
Suggested change: confirm the rule on that page, then name each nested
file with its trigger in the owning `SKILL.md` and add a contents list to
the three long files.

## 2026-10-03 web-component: no `paths` field
Skill or agent: `.claude/skills/web-component/SKILL.md`, frontmatter.
What happened: the skill applies to `frontend/web/src/ui/` and
`frontend/web/src/components/` and relies on its description to load.
`https://code.claude.com/docs/en/skills.md` documents `paths`: "Claude
loads the skill automatically only when working with files matching the
patterns". The skill also has no `argument-hint`.
Suggested change: add `paths` for the two directories.

## 2026-10-03 hooks: the Stop gate is untested against its timeout on a cold cache
Skill or agent: `.claude/settings.json`, the `Stop` entry, and
`tools/hooks/stop-check.sh`.
What happened: the entry sets `timeout: 30` and the script runs `go test`
on every package under `test/conformance/`. With a warm build cache it
took 2.8 seconds. A cold cache was not measured, and what the session sees
when the hook is cancelled is unverified.
Suggested change: measure once after `go clean -cache`. If it exceeds 30
seconds, raise the timeout (a policy surface) or have the script report
the cancelled run.

## 2026-10-03 steer: two steering documents drifted
Skill or agent: `docs/agent-knowledge.md`, the table, and
`docs/agent-steering.md`, Project skills.
What happened: `agent-knowledge.md` says `AGENTS.md` is "imported by
`CLAUDE.md`". `CLAUDE.md` is a symlink to it. The Project skills section
of `agent-steering.md` runs from line 104 to line 1002 with no
subheading, so the rationale for one skill cannot be found without
reading the section.
Suggested change: say "linked from `CLAUDE.md`", and give each skill a
subheading in Project skills.
