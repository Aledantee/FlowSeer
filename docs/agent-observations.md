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

## 2026-10-05 delegate: no review-unit lane survives when Orca is down and units ran on Sonnet
Skill or agent: `.claude/skills/delegate/SKILL.md`, "Pick the role" with `references/no-orca.md`, as `review` step 3 uses it.
What happened: without Orca, `execute` resolves to `claude-sonnet-5-5` (the only Claude model in its `fit`), so every unit is Anthropic-written. `review-unit` then drops Anthropic by `vendor_differs_from`, and its non-Claude models fall away because the codex pool row lists only `gpt-5.5` and `no-orca.md` gives a non-Claude review lane no reviewer. The phase review had no independent reviewer by construction. The steps were followed as written.
Suggested change: give `review-unit` a `last_resort` that a native subagent can run (for example `claude-opus-5-5`, model differing from the executor), or let `no-orca.md` launch a codex review lane directly when a pool model fits, so a no-Orca host still gets a second reader.

## 2026-10-05 review: findings that give the untrusted author nothing new still held the verdict
Skill or agent: `.claude/skills/review/SKILL.md`, step 4 with `references/fix-loop.md`, "Which findings block", and `.claude/skills/plan/SKILL.md`, step 3, the Out of scope trust sentence.
What happened: a web phase validated a component tree that an in-page AI handler yields. Its plan said the handler author "is not trusted" and that an unknown component or prop "rejects the whole tree". Reviewers reported inputs that need a crafted object: an accessor that answers differently on a second read, a Proxy, a changed array prototype. Each reviewer said no security boundary was crossed, since the handler is script in the page already. The coordinator still classed the findings as behavior, because they contradicted a Requirement's literal text for some input, and behavior holds the verdict. Two reviews, one re-plan, and four fix rounds followed (`cca58380`, `534bf4ef`, `ea532a0e`, `2dcaccdc`). Each fix closed one double read and the next reviewer found another on the neighbouring field. The loop ended when the user asked what the problem was and decided that crafted objects are outside the contract (`a03f9a8f` reverts the last fix). The steps were followed as written: the class table has no row for a behavior finding whose input only a principal with the result's capability can supply, and the plan rule asks whether the author is trusted but not what the checker defends against.
Suggested change: in `fix-loop.md`, "Which findings block", class a finding as hardening when its failing input can only come from a principal that can already cause the finding's result directly, whatever Requirement text it contradicts, and have `review` step 4 name that principal's existing capability before classing. In `plan` step 3, extend the trust rule: when an untrusted author already holds the capability a check would deny (script in the page, a process on the host), the Out of scope sentence states what the check is for, malformed data or a hostile object, so a Requirement's "rejects" is read against that. In `review` step 5, when the first fix round on a finding of this kind is not clean, ask the user what the check defends against before offering another round.
