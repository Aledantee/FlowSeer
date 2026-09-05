---
name: Agent steering
last_updated: 2026-09-03
---

# Agent steering

FlowSeer keeps the always-loaded instruction layer small and stores detailed
knowledge beside the code or decision it explains. The aim is to give an agent a
reliable map without displacing the task, source, and tests from its context.

This document describes how to maintain that map. It is not itself an
always-loaded instruction file.

## Steering surfaces

| Concern | Authoritative location |
| --- | --- |
| Cross-tool rules needed on most tasks | Root [`AGENTS.md`](../AGENTS.md) |
| Human orientation and first successful commands | Root [`README.md`](../README.md) |
| Area-specific engineering rules | The relevant convention or paired scoped agent entry points |
| Accepted architectural direction | [`architecture/`](architecture/) |
| Reusable lessons from verified work | [`solutions/`](solutions/) |
| Repeatable multi-step procedures | A checked-in skill with its scripts |
| Bounded delegation behavior | A checked-in agent definition |
| Deterministic prohibitions and formatting | Hooks, linters, schema checks, and tests |
| Personal preferences or tentative notes | Tool-local memory outside the repository |

`CLAUDE.md` is a symlink to `AGENTS.md`, so Codex and Claude receive the same
project contract without duplicated text. Tool-specific settings may wire that
contract into a runtime, but must not restate it.

## Write instructions for the right scope

Keep a rule in the root `AGENTS.md` only when it matters across the repository or
when every task needs it to find the next source. Put language and schema details
in the existing style guides. A subtree should gain its own `AGENTS.md` only when
it has stable local rules that would otherwise burden unrelated work. Add a
matching `CLAUDE.md` symlink or import beside it so Codex and Claude load the same
local contract.

An instruction should be:

- specific enough to verify, with an exact command or path where one exists;
- short enough to scan in context;
- explicit about its scope and any safe exception;
- backed by a reason when the constraint is surprising;
- written once, with links from other entry points.

Do not add dependency lists, file inventories, or architecture summaries that an
agent can recover cheaply from the repository. Keep only the routing fact or
boundary that changes how work should proceed. Do not store current task state,
branch names, absolute machine paths, credentials, or unverified guesses in a
shared instruction file.

## Separate judgment from enforcement

Natural-language guidance is appropriate for choices that require context:
where a domain type belongs, what evidence is persuasive, or why an apparent
shortcut violates an architectural boundary.

Use a deterministic mechanism when compliance must not depend on model behavior.
FlowSeer uses hooks to deny edits to generated sources, format supported files,
and run fast layout checks. Linters and tests remain authoritative. The prose
explains the boundary and the tool enforces what can be checked mechanically.

Do not copy a linter manual into `AGENTS.md`. Name the command and document only
the project-specific judgment that the tool cannot express.

## Skills and delegated agents

A skill is appropriate for a conditional procedure with several steps, such as
diff-aware verification. Its description must say when it applies, and its body
must define inputs, observable completion, and failure behavior. Keep scripts
with the skill when exact execution matters.

A delegated agent should have one bounded responsibility, the least tool access
needed for it, and an output contract the caller can inspect. Read-only research
and independent review are useful examples because their permissions match their
jobs. The coordinating agent owns integration and authoritative verification;
delegation does not transfer responsibility for the final result.

## Project skills

FlowSeer ships four workflow skills under `.claude/skills/`: `plan`,
`implement`, `review`, and `compound`, next to the `verify-change` gate and
the `delegate` routing skill that the four load before dispatching an agent.
They replace the third-party compound-engineering plugin, which the
repository used from August 2026 until 2026-09-05. The decisions below were
taken against the plugin's issue tracker, published measurements, the
research listed at the end of this document, and this project's own session
history; revisit them when that evidence changes.

Keep only the workflows the project uses. Session transcripts for this
repository showed six of the plugin's 33 skills carrying every invocation, and
`/skill-doctor` showed 19 skills never invoked on this machine. Each unused
skill still cost listing tokens on every turn. The four skills map onto the
six used ones: brainstorm folds into `plan`, doc review into `plan` and
`review`, refresh into `compound`.

Keep each skill short and specific to this repository. Anthropic's authoring
guidance caps a `SKILL.md` body at 500 lines and says a skill that restates
what the model does by default adds context without value. Measured evidence
agrees: SWE-Skills-Bench found 39 of 49 public skills gave no pass-rate gain,
and Vercel found skills never fired in 56% of eval cases while a compressed
always-on index did. The plugin's `ce-plan` alone was 111 KB and loaded a
further 110 KB of references per run. The FlowSeer skills stay under about
150 lines each and contain only the procedure, the file layout, and the
repository rules an agent cannot infer from the tree.

Use one reviewer, split by file group, never a persona panel. The plugin's
own maintainers wrote that running the full multi-agent review after every
implementation "didn't earn its place" (issue #726) and had to add a serial
mode after reviewers exhausted context (issue #166). This repository's
transcripts showed the plugin's review dispatching 8.5 subagents per call on
average, with a peak of 14. `review` dispatches `independent-reviewer` once,
reads the diff itself with a fixed checklist, and verifies every finding
before reporting it.

Skip ceremony when the work is small. The plugin's author advised skipping
brainstorming when requirements are clear and warned that auto-running steps
"generates a lot of mess". `plan` opens with a skip rule, `implement` does
not trigger a review, and `compound` opens with a gate that refuses one-off
or derivable lessons, because reviewers of the plugin reported its solutions
folder turning into "compounding noise".

Keep plan labels out of code. Commit `7b0c5cd8` stripped plan identifiers
that the plugin's work skill had told the implementer to cite in comments.
`plan` and `implement` both state the rule; `docs/code-style.md` enforces it
in review.

Leave policy surfaces to people. The plugin's compound skills edited
`AGENTS.md`, `CLAUDE.md`, and `CONCEPTS.md` after a chat consent. `compound`
proposes vocabulary and never edits an instruction file.

Enforce with the verifier, not with prose. Every skill ends by running
`verify-change` on the changed paths. Anthropic's guidance is explicit that
an instruction in a skill is a request and a hook is a guarantee.

Drop what the repository cannot use. There is no remote, so pull-request,
CI-watching, and push skills are inert here. Cross-model review would send
diffs to an external CLI by default. The skills rely on `git`, `go`, `buf`,
and the existing agents only.

One artifact format each. Every plan under `docs/plans/` carries
`artifact_contract: flowseer-plan/v1` and the `artifact_readiness` field that
`docs/README.md` documents. Every solution carries `applies_when` frontmatter
and a row in `docs/solutions/README.md`. The skills describe these formats
and nothing else.

Name the model for every delegate. `repo-researcher` is pinned to Sonnet and
`independent-reviewer` to Opus, `delegate` sends pure lookups to `Explore`
on Haiku and editing workers to Sonnet, and no agent uses `inherit` any
more: the coordinating session may run the most expensive model, and none
of the delegated work needs it. Anthropic's subagent guide recommends Haiku
for read-only exploration; its research-system report measured an Opus lead
with Sonnet workers beating a single Opus agent by 90.2% on its internal
eval, while a multi-agent run costs about 15 times a chat turn; ProgRouter
arrives at the same shape by routing each workflow step to the cheapest
model that still makes progress. Users describe the failure mode from the
other side: a task that spawned seven subagents on the session model and
exhausted a budget before one of them finished, cured by naming a smaller
model for them. `delegate` caps concurrent workers at three for the same
reason.

Send editing workers to Orca when its runtime is reachable. The
asynchronous-agent study behind CAID found that isolated workspaces, a
central integrator, and test-based verification at merge improved paper
reproduction by 25.6 points and library development by 14.7. Orca's
`worker-start` provides exactly that: a child worktree per worker, a named
model and effort per launch, and a `worker_done` report the coordinator
waits on. Read-only delegates stay native subagents, which load their
definition and nothing else, where an Orca worker is a full Claude Code
session. Outside Orca, `delegate` falls back to native subagents with
worktree isolation. The Orca command surface is version-matched and served
by the binary (`orca skills get orca-cli`, `orca skills get orchestration`),
so the skills show the shape of the loop and defer to that guide for flags.
Two facts found on 2026-09-05 shape the skill's wording: the CLI reaches the
app over a local socket that Claude's Bash sandbox blocks, so a sandboxed
`orca status` reports the app as not running from inside an Orca terminal;
and `orca account list` reports which providers are signed in and how much
of each rate-limit window is used, which is why `delegate` discovers the
worker agent and provider per session instead of assuming Claude and picks
the provider for each wave by remaining quota. The 85% threshold is a
starting point chosen so that a three-worker wave cannot push a window
over its limit mid-run; tune it when a wave gets cut off or when quota sits
idle.

The skill frontmatter uses two Claude Code fields outside the portable
Agent Skills key set, `argument-hint` and `user-invocable`. Anthropic's
`skill-creator` validator flags them; Claude Code documents them and Codex
ignores unknown keys, so they stay.

Brief the reviewer without the author's claims. A study of confirmation bias
in LLM code review found that framing a diff as bug-free cut detection
sharply, and that redacting such metadata plus an explicit neutral
instruction restored it in every affected case. Anthropic's best-practices
guide adds that a reviewer asked for gaps reports some even when the work is
sound, so `independent-reviewer` is told to report only what affects
correctness, the stated requirements, or a repository rule.

Keep the plan explicit and keep re-reading it. An analysis of 21,120
SWE-agent trajectories found that an explicit plan raises resolution, that
periodic reminders of the plan cut violations, and that a poor plan hurts
more than none. `plan` therefore ends with an implementer's read and an
independent review, and `implement` re-reads each unit before starting it.
Units carry an `After` line so that `implement` can run independent units
in parallel without guessing.

Log process corrections, apply them by hand. Task Observer, a widely used
meta-skill, keeps an observation log of corrections and skill gaps that a
person reviews on a schedule, and runs as an always-on monitor from the
first tool call. FlowSeer takes the log and leaves the monitor: `compound`'s
Observe mode appends to `docs/agent-observations.md` when a workflow skill
ends with a process correction, and a maintainer applies entries through the
process below. An always-on observer would spend context on every session
for a signal that appears at the end of a few.

Report outcome first. Each skill's report step leads with the verdict or
result and keeps the rest to a short ordered list, which is what readers of
agent output ask for and what the `i-have-adhd` skill codifies.

## Change and review process

Treat changes to the policy surfaces named in `AGENTS.md` as policy changes, even
when they only edit Markdown. Use the same review discipline for another
steering surface when it changes permissions, enforcement, or cross-tool
behavior:

1. Start from an observed repeated failure, a new stable boundary, or a missing
   onboarding fact.
2. Choose the narrowest surface that will load when the fact matters.
3. Search all instruction and convention files for conflicting or duplicated
   guidance.
4. Include one concrete example when wording could be interpreted two ways.
5. Verify instruction discovery from the repository root and any affected
   subtree. Run the repository verifier for the changed files.
6. Request an independent guardrail review when the change meets the policy or
   cross-tool threshold above.

Remove a rule when the code or toolchain makes it obvious, when a narrower source
now owns it, or when the repository no longer behaves as described. A short file
that stays correct is more useful than an exhaustive one.

## Research basis

These sources were checked on 2026-09-03:

- [OpenAI, “Harness engineering: leveraging Codex in an agent-first world”](https://openai.com/index/harness-engineering/)
  describes a short root map, a versioned documentation system of record, and
  mechanical enforcement of invariants.
- [OpenAI Codex, “Custom instructions with AGENTS.md”](https://learn.chatgpt.com/docs/agent-configuration/agents-md)
  documents root-to-leaf discovery, nearer-scope precedence, size limits, and
  ways to verify which instructions loaded.
- [AGENTS.md](https://agents.md/) distinguishes the human README from the
  agent-focused instruction file and recommends exact setup and test commands.
- [Anthropic, “How Claude remembers your project”](https://code.claude.com/docs/en/memory)
  recommends concise, specific instructions, path-scoped rules for local facts,
  skills for conditional procedures, and hooks for enforced behavior.
- [Anthropic, “Create custom subagents”](https://code.claude.com/docs/en/sub-agents)
  recommends focused roles, precise routing descriptions, limited tools, and
  version-controlled project agents.
- [GitHub, “About customizing GitHub Copilot responses”](https://docs.github.com/en/copilot/concepts/prompting/response-customization)
  recommends short, self-contained instructions and warns against conflicts
  across instruction scopes.

Sources checked on 2026-09-05 for the project skills:

- [Anthropic, “Skill authoring best practices”](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/best-practices):
  500-line body ceiling, description as the selection signal, context as a
  shared budget.
- [Anthropic, “Extend Claude with skills”](https://code.claude.com/docs/en/skills)
  and [“Create plugins”](https://code.claude.com/docs/en/plugins): the 1%
  listing budget, `/skill-doctor`, and `.claude/` for project-specific work
  with plugins reserved for distribution.
- [Anthropic, “How we use skills”](https://claude.com/blog/lessons-from-building-claude-code-how-we-use-skills):
  a skill that restates default behavior adds context without value.
- [SWE-Skills-Bench](https://arxiv.org/abs/2603.15401): 39 of 49 public skills
  gave zero pass-rate improvement; token cost rose up to 451%.
- [Vercel, “AGENTS.md outperforms skills in our agent evals”](https://vercel.com/blog/agents-md-outperforms-skills-in-our-agent-evals):
  skills never invoked in 56% of cases.
- [compound-engineering-plugin issues](https://github.com/EveryInc/compound-engineering-plugin/issues)
  #20, #63, #139, #166, #338, #726 and pull request #161 on context cost,
  monolithic packaging, forced layout, and review expense; the maintainers'
  description-budget fix reported 316% of the character budget in use.
- [Kieran Klaassen, “Compound Engineering Camp”](https://every.to/source-code/compound-engineering-camp-every-step-from-scratch)
  on skipping brainstorm when requirements are clear and the mess that
  auto-running steps produces.
- [Ry Walker, review of the plugin](https://rywalker.com/research/compound-engineering-plugin)
  and [MoClaw on compound engineering](https://moclaw.ai/blog/compound-engineering)
  on review cost, release churn, and the solutions folder as a junk drawer.

Sources checked on 2026-09-05 for delegation, model choice, and review
framing:

- [Anthropic, "Create custom subagents"](https://code.claude.com/docs/en/sub-agents):
  the `model` and `effort` fields, Haiku for read-only exploration, a 15,000
  token ceiling for combined agent descriptions.
- [Anthropic, "Best practices for Claude Code"](https://code.claude.com/docs/en/best-practices):
  plan when the approach is uncertain or the change spans files, skip it
  when the diff fits in a sentence; a reviewer asked for gaps reports some
  regardless; subagents keep investigation out of the main context.
- [Anthropic, "How we built our multi-agent research system"](https://www.anthropic.com/engineering/multi-agent-research-system):
  Opus lead with Sonnet workers beat single Opus by 90.2%; multi-agent runs
  use about 15 times the tokens of a chat; scale agent count to task
  complexity.
- [Effective Strategies for Asynchronous Software Engineering Agents](https://arxiv.org/abs/2603.21489):
  CAID, isolated workspaces with branch-and-merge integration and test-based
  verification, +25.6 points on PaperBench and +14.7 on Commit0.
- [Evaluating Plan Compliance in Autonomous Programming Agents](https://arxiv.org/abs/2604.12147):
  21,120 trajectories; explicit plans help, reminders cut violations, a poor
  plan hurts more than none.
- [Measuring and Exploiting Confirmation Bias in LLM-Assisted Security Code Review](https://arxiv.org/abs/2603.18740):
  bug-free framing suppresses detection; metadata redaction plus neutral
  instructions restore it.
- [ProgRouter](https://arxiv.org/abs/2608.25992): step-wise routing of a
  multi-agent workflow to the cheapest model that still makes progress.
- [Hacker News thread on subagent token cost](https://news.ycombinator.com/item?id=48883796):
  seven subagents on the session model exhausted a budget; naming a smaller
  subagent model and limiting concurrency fixed it.
- [Task Observer](https://github.com/rebelytics/one-skill-to-rule-them-all):
  an observation log of corrections and skill gaps, reviewed by a person on
  a schedule.
- [HyperFrames skills](https://github.com/heygen-com/hyperframes): a router
  skill that confirms the brief up front and loads domain skills on demand,
  the same shape as `AGENTS.md` plus scoped skills here.
- [i-have-adhd](https://github.com/ayghri/i-have-adhd): action-first
  responses, numbered steps, no preamble or recap.
- Orca CLI and orchestration guides, served version-matched by the binary
  through `orca skills get orca-cli` and `orca skills get orchestration`.

The common recommendation is progressive disclosure. The inference for
FlowSeer is to keep `AGENTS.md` near its current size, add scoped steering only
when a real local rule appears, and invest in executable checks before adding
more prose.
