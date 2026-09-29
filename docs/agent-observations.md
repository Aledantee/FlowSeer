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

## 2026-09-29 review: reviewer attacked adversarial inputs for a trusted-author gate
Skill or agent: `.claude/skills/review/SKILL.md`, step 3, and `.claude/skills/plan/SKILL.md`, step 2.
What happened: four review passes ended in rework on `docs/plans/2026-09-28-2054-docs-package-guarantees-plan.md` because reviewers reported ways crafted Markdown could hide clauses (HTML comments, image alt text with line breaks, goldmark's 998-byte link-opener limit, lone CR). The plan's Goal (failing the verifier when a cited test is removed) was met by the first implement stage, but each re-plan turned the previous review's adversarial finding into a requirement, which the next review attacked. The user intervened on 2026-09-29 to descope adversarial input in the plan's first Decision: the checker catches honest mistakes by trusted contributors and agents, not adversarial inputs. Reviewers followed instructions to search for bypasses but lacked a specified threat model for trusted tooling.
Suggested change: require plans for verification and lint tooling to state author trust assumptions in Scope, and update review step 3 to treat adversarial inputs as out of scope unless the plan specifies an untrusted input threat model.

## 2026-09-29 review: fix worker rewrote user-attributed plan decision
Skill or agent: `.claude/skills/review/references/fix-loop.md`, step 1.
What happened: in commit `c21d12ac`, a review fix worker edited `docs/plans/2026-09-28-2054-docs-package-guarantees-plan.md` and rewrote a Decision explicitly marked as decided by the user (the goldmark CommonMark parser decision at lines 119-126). The drive coordinator restored the decision in commit `54279a6c`. The fix loop permits editing stale files left by a fix, but fix worker briefs lacked an explicit boundary against modifying user-attributed decisions in plans.
Suggested change: update `fix-loop.md` step 1 and `delegate` fix briefs to forbid editing plan Decisions marked as decided by the user.

## 2026-09-29 delegate: a land or drive stage without Orca stops instead of using a native subagent
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Orca or native", the
paragraph on stage workers of `land` or `drive`, and
`.claude/skills/land/SKILL.md`, step 2, which defers to it ("without Orca,
its 'Orca or native' stops here").
What happened: without Orca, the coordinator stopped a stage and handed it
to the user for a fresh session, as the paragraph says. The user
corrected the procedure: a native subagent is allowed when nothing else is
available. The step was followed as written.
Suggested change: without Orca, run the stage in a native `general-purpose`
subagent in the worktree, pinned to a model other than the change's author,
and stop only when no native subagent can run.

## 2026-09-29 delegate: a review stage fell back to Sonnet with no machine-wide registry
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Pick the role, then
resolve the lane".
What happened: with no `~/.claude/models/registry.yaml`, the coordinator
picked Sonnet for a `review-seam` stage. The user corrected it: review runs
on Opus or Fable, never on Sonnet. The skill states no default for a
missing registry, so the step gave no rule to follow. Where the project
registry is read, its review fit sets already omit Sonnet
(`.claude/models/registry.yaml:76-77`).
Suggested change: state that review roles' `fit` and `last_resort` never
hold Sonnet, and that with no registry read a review lane defaults to Opus
or Fable, whichever did not author the change.

## 2026-09-29 land: a driven phase plan reached main without its retire
Skill or agent: `.claude/skills/land/SKILL.md`, step 4 (retire), as reached from `drive`.
What happened: `docs/plans/2026-09-28-1844-refactor-web-component-contract-migration-phase1-plan.md` sat on `main` with `status: implemented`, review and compound recorded (`42297bab`, `5c9e8051`), and no `docs(plans): retire` commit. `steer`'s sweep retired it. Whether `land` ran for the phase or the phase reached `main` through a `drive` merge alone is not recorded.
Suggested change: have `drive` hand a finished phase to `land`, or have `land` retire every implemented phase plan on its branch, so a phase that lands never waits for the sweep.
