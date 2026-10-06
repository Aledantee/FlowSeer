# Agent observations

Entries here record where a FlowSeer skill, agent definition, or hook did not
fit the work: a correction the user had to make to the procedure, a step that
was skipped or done by hand, a brief that missed something, a sequence that
recurs with no skill behind it. The `compound` skill's Observe mode appends
them. Nothing reads this file on its own; the `steer` skill works the queue
when a maintainer asks for it.

`steer` applies or rejects each entry through the change process in
[`agent-steering.md`](agent-steering.md) and deletes it. An edit to a policy surface `AGENTS.md`, Hard boundaries, names stops at a
staged diff for a person's review. An entry that sits here is not a
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

## 2026-10-06 drive: stage branches merge at the end, and the plan state has no branch field
Skill or agent: `.claude/skills/drive/SKILL.md`, step 2, "After each stage", item 3, and `.claude/skills/plan/scripts/plan_record.py`.
What happened: the user corrected the drive of the uv scripts plan. Stage branches are no longer merged into the coordinator branch after each stage. Every stage commits on one branch, and the work is merged once at the end. The user wants that as the default. The user also asked for the branch to be recorded in the plan state. The state contract has no field for it, so the branch went into a `## Branch` section of `docs/plans/2026-10-05-2216-refactor-uv-python-scripts-plan.md`. The step was followed as written and was still the wrong default.
Suggested change: add a `branch` field to the plan state, written by a `plan_record.py branch <plan> <name>` subcommand and read by `show`, `check`, and `plan-queue.py`. Then change `drive` step 2 so later stages run on the recorded branch and the merge happens once, before `land`.

## 2026-10-06 drive: a no-Orca drive in auto mode cannot finish a stage on its own
Skill or agent: `.claude/skills/drive/SKILL.md`, step 1, "Before the first dispatch", with `.claude/skills/delegate/references/no-orca.md`.
What happened: Orca was unreachable, so the uv scripts Phase 1 stages ran as native subagents in a Claude Code session in auto mode. The auto-mode classifier refused the `.claude/settings.json` edit (`[Self-Modification]`). It refused the U1 commit that touched policy surfaces, the verifier run with the sandbox disabled (`[Safety Bypass Flag]`), and a test-mutation run (`[Security Test Removal]`). The worktree-isolated session also refused git in a stage worker's worktree. Every refused step went back to the user as a `!` command. A plan whose units touch a policy surface hits this on every stage. The steps were followed as written. Step 1 checks pools and Orca but not the permission mode.
Suggested change: in `drive` step 1, when Orca is unreachable and the plan names a policy surface or the stage skills need an unsandboxed verifier, ask the user before the first dispatch to leave auto mode, or to accept one `!` command per refused step.

## 2026-10-05 delegate: no review-unit lane survives when Orca is down and units ran on Sonnet
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Pick the role" with `references/no-orca.md`, as `review` step 3 uses it.
What happened: without Orca, `execute` resolves to `claude-sonnet-5-5` (the only Claude model in its `fit`), so every unit is Anthropic-written. `review-unit` then drops Anthropic by `vendor_differs_from`, and its non-Claude models fall away because the codex pool row lists only `gpt-5.5` and `no-orca.md` gives a non-Claude review lane no reviewer. The phase review had no independent reviewer by construction. The steps were followed as written.
Suggested change: give `review-unit` a `last_resort` that a native subagent can run (for example `claude-opus-5-5`, model differing from the executor), or let `no-orca.md` launch a codex review lane directly when a pool model fits, so a no-Orca host still gets a second reader.

## 2026-10-05 review: findings that give the untrusted author nothing new still held the verdict
Skill or agent: `.claude/skills/review/SKILL.md`, step 4 with `references/fix-loop.md`, "Which findings block", and `.claude/skills/plan/SKILL.md`, step 3, the Out of scope trust sentence.
What happened: a web phase validated a component tree that an in-page AI handler yields. Its plan said the handler author "is not trusted" and that an unknown component or prop "rejects the whole tree". Reviewers reported inputs that need a crafted object: an accessor that answers differently on a second read, a Proxy, a changed array prototype. Each reviewer said no security boundary was crossed, since the handler is script in the page already. The coordinator still classed the findings as behavior, because they contradicted a Requirement's literal text for some input, and behavior holds the verdict. Two reviews, one re-plan, and four fix rounds followed (`cca58380`, `534bf4ef`, `ea532a0e`, `2dcaccdc`). Each fix closed one double read and the next reviewer found another on the neighbouring field. The loop ended when the user asked what the problem was and decided that crafted objects are outside the contract (`a03f9a8f` reverts the last fix). The steps were followed as written: the class table has no row for a behavior finding whose input only a principal with the result's capability can supply, and the plan rule asks whether the author is trusted but not what the checker defends against.
Suggested change: in `fix-loop.md`, "Which findings block", class a finding as hardening when its failing input can only come from a principal that can already cause the finding's result directly, whatever Requirement text it contradicts, and have `review` step 4 name that principal's existing capability before classing. In `plan` step 3, extend the trust rule: when an untrusted author already holds the capability a check would deny (script in the page, a process on the host), the Out of scope sentence states what the check is for, malformed data or a hostile object, so a Requirement's "rejects" is read against that. In `review` step 5, when the first fix round on a finding of this kind is not clean, ask the user what the check defends against before offering another round.

## 2026-10-05 web-component: the i18n and AI reference still says the catalog has not landed
Skill or agent: `.agents/skills/web-component/references/i18n-and-ai.md`, lines 4 to 17.
What happened: the reference says "The generative UI catalog has not, so check what exists" and carries a section "While the AI migration has not landed". `frontend/web/src/ai/catalog.ts` and `frontend/web/src/ui/ai/UiAiRender.vue` now exist. The migration's parent plan asked for the interim rules to go as their migration landed, and no phase unit named this file.
Suggested change: delete the check and the interim section, and state that the catalog is in `src/ai/catalog.ts` and rendered by `UiAiRender`.

## 2026-10-06 next: harness work does not come before product work
Skill or agent: `.claude/skills/next/SKILL.md`, steps 1, 2, and 4, with `.claude/skills/next/scripts/plan-queue.py`.
What happened: `next` took the queue order as `plan-queue.py` printed it and
recommended driving the sim package overhaul, a product plan, ahead of the
unchecked plan state file change, which changes the skills and their scripts.
The user corrected the order: in general, `next` should work on harness tasks
first. The step was followed as written, since neither the skill nor the
script tells harness work from product work.
Suggested change: rank harness plans first within each group. A harness plan
is one whose Units change only `.agents/skills/`, `.claude/`, `tools/hooks/`,
the verifier, or docs about the workflow. Do this in `plan-queue.py` so the
order is computed, and say in `next` step 2 that the top three are taken in
that order.

## 2026-10-06 delegate: the sandbox denies Bash writes under .agents/skills
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Write the brief" and "Reading a worker's report", and `.claude/skills/review/references/fix-loop.md`, One round, step 2.
What happened: the session's sandbox lists the worktree's `.agents/skills`
among the paths it never writes, since `.claude/skills` links there. The
project's `.claude/settings.json` does not set this. Fix workers for the
plan state review reported that `sed` writes under `.agents/skills` were
denied, so they switched to the Edit tool. The coordinator's
`git merge` of a worker branch that touched skill files failed with
"unable to unlink old '.agents/skills/compound/SKILL.md': Operation not
permitted" and succeeded only with the sandbox disabled. The steps were
followed as written, and neither mentions this.
Suggested change: say in the brief that a worker edits files under
`.agents/skills` with the Edit tool, and say in `fix-loop.md` and
`delegate`'s merge step what `land` step 3 already says: a merge touching
`.agents/skills` runs with the sandbox disabled.
