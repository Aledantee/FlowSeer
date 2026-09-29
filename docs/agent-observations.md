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
