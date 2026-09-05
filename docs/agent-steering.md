---
name: Agent steering
last_updated: 2026-09-05
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
FlowSeer uses hooks to deny edits to generated sources, keep writes out of the
primary checkout, format supported files, and run fast layout checks. Linters
and tests remain authoritative. The prose explains the boundary and the tool
enforces what can be checked mechanically. A hook that cannot tell a decision
from a mistake reports instead of denying: the policy-surface prompt, the
suppression notice, and the unverified-edit message at Stop all leave the
call to the person. A guard earns its deny by being precise: the Bash guard
judges the operands of a mutating verb rather than any command that names
`generated/`, the edit guard resolves paths whose parent directory does not
exist yet, and the message-sync note stays silent about members the
file-level comment names. A guard that fires more often on legitimate work
than on the mistake it was written for gets narrowed, not explained.

Do not copy a linter manual into `AGENTS.md`. Name the command and document only
the project-specific judgment that the tool cannot express.

## Skills and delegated agents

A skill is appropriate for a conditional procedure with several steps, such as
diff-aware verification. Its description must say when it applies, and its body
must define inputs, observable completion, and failure behavior. Keep scripts
with the skill when exact execution matters.

Two authoring rules hold for every skill and agent file. A body carries only
what changes behavior on each invocation; material for one kind of episode
goes to a `references/` file whose pointer says when to load it, because a
pointer without a trigger reads as optional and a body loaded every time is
fixed overhead. And every command embedded in the text is run once,
verbatim, from a fresh shell before the file is saved: a prose rule is
reinterpreted each time it is read, while a wrong command runs unattended
and reads as correct on every re-read.

A delegated agent should have one bounded responsibility, the least tool access
needed for it, and an output contract the caller can inspect. Read-only research
and independent review are useful examples because their permissions match their
jobs. The coordinating agent owns integration and authoritative verification;
delegation does not transfer responsibility for the final result.

## Project skills

FlowSeer ships six workflow skills under `.claude/skills/`: `plan`,
`implement`, `review`, `compound`, `close`, and `steer`, next to the
`verify-change` gate and the `delegate` routing skill that the others load
before dispatching an agent. The decisions below were taken against published
measurements, the research listed at the end of this document, and this
project's own session history; revisit them when that evidence changes.

Keep only the workflows the project uses. Session transcripts for this
repository showed six steps carrying every invocation of the 33-skill set
the repository used before 2026-09-05, and `/skill-doctor` showed 19 of
those skills never invoked on this machine. Each unused skill still cost
listing tokens on every turn. The first four skills map onto the six used
steps: brainstorming folds into `plan`, doc review into `plan` and
`review`, refreshing solutions into `compound`. `close` was added on
2026-09-05 for a step every session repeated by hand.

Gate the merge on evidence, not on the conversation. `close` is the one
skill whose action reaches every other worktree, and a session cannot see
which skills ran before it, so `implement`, `review`, and `compound` each
leave a checkpoint that `close` reads: the plan's `status` field, the
verifier receipt under the git dir, and the Orca card's status and comment.
A missing checkpoint stops the merge and names the skill to run next;
`close` does not run that skill itself, for the same reason `implement`
does not trigger a review. The skill leaves the worktree ready for
`orca worktree rm` and stops there: that command kills the terminal that
issues it and discards the workspace's terminal history, so it stays a
person's action taken after reading the report.

Keep each skill short and specific to this repository. Anthropic's authoring
guidance caps a `SKILL.md` body at 500 lines and says a skill that restates
what the model does by default adds context without value. Measured evidence
agrees: SWE-Skills-Bench found 39 of 49 public skills gave no pass-rate gain,
and Vercel found skills never fired in 56% of eval cases while a compressed
always-on index did. The planning skill this repository used before was
111 KB and loaded a further 110 KB of references per run. The FlowSeer skills stay under about
150 lines each and contain only the procedure, the file layout, and the
repository rules an agent cannot infer from the tree.

Use one reviewer, split by file group, never a persona panel. This
repository's transcripts showed the earlier persona-panel review dispatching
8.5 subagents per call on average, with a peak of 14, and reviewers
exhausting their context before they reported. Anthropic's research-system
report puts a multi-agent run at about 15 times the tokens of a chat turn,
which a review after every implementation does not earn back. `review`
dispatches `independent-reviewer` once,
reads the diff itself with a fixed checklist, and verifies every finding
before reporting it.

Skip ceremony when the work is small. Anthropic's best-practices guide says
to plan when the approach is uncertain or the change spans files and to skip
it when the diff fits in a sentence; a step that runs itself after every
other step produces artifacts nobody asked for. `plan` opens with a skip
rule, `implement` does not trigger a review, and `compound` opens with a
gate that refuses one-off or derivable lessons, so that `docs/solutions/`
stays a set of lessons rather than a log of every session.

Promote lasting decisions out of the plan, from the plan. A plan under
`docs/plans/` is the planning artifact and goes stale once the work lands,
while a record under `docs/architecture/` is the decision record that later
plans read as a constraint. Before 2026-09-05 nothing in the workflow
produced a record; the four that exist were written by hand after a
brainstorm, and a cross-cutting decision taken inside a plan stayed there
where the next plan would not look. `plan` now carries a promotion test
(hard to reverse, constrains other packages or a wire contract, amends an
accepted record, or has been decided before) and drafts a record with
`status: proposed-direction`. Acceptance stays a person's action because an
agent-drafted record captures what was decided and tends to invent why.
`compound` refuses to carry a decision in a solution for the same reason a
solution never restates a convention.

Keep plan labels out of code. Commit `7b0c5cd8` stripped plan identifiers
that earlier tooling had told the implementer to cite in comments. `plan`
and `implement` both state the rule; `docs/code-style.md` enforces it in
review.

Leave policy surfaces to people. Earlier tooling edited `AGENTS.md`,
`CLAUDE.md`, and `CONCEPTS.md` after a chat consent. `compound` proposes
vocabulary and never edits an instruction file.

Enforce with the verifier, not with prose. Every skill ends by running
`verify-change` on the changed paths. Anthropic's guidance is explicit that
an instruction in a skill is a request and a hook is a guarantee.

Verify once, on the integrated result, at the scope the change can reach.
A test run of the workflow on 2026-09-05 (two 25-line example tests, two
Orca workers) produced three module-wide race runs of about ten minutes
each, because every worker and then the coordinator ran the verifier and
the verifier raced the whole module for any Go path. The verifier now
vets, tests, and lints the changed packages and their importers (a
fixpoint over `go list` dependency and test-import data) and keeps the
module-wide scope for `--full`; workers run their package's focused tests
and the coordinator runs the verifier once after merging.

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

Look up third-party library docs through Context7 when it is connected, and
nowhere else through a dedicated skill. `plan` and `implement` name the
Context7 tools, the `ctx7` CLI as a fallback, and WebFetch on the official
docs when neither exists; Go dependencies stay with `go doc` and the module
cache. The scope follows the evidence: retrieving API documentation improved
code generation on rarely seen libraries by 83 to 220 percent, with the code
examples carrying almost all of the gain, while a noisy retriever hurt on
well-known APIs. Context7 was chosen over Ref because its server is MIT and
self-hostable, it ships as an official Claude Code plugin, and it costs
nothing to trial; Ref's index is a closed API with a one-time free tier and
almost no independent user reports. The repository checks in no
`.mcp.json`: each machine connects the server through its own config, and
the skills fall back cleanly when it is absent. No web-research or academic-research skill was
added. Claude Code's first-party deep research skill covers the fan-out,
papers have appeared in one document here, and the measured failure of
research agents is fabricated citations (3 to 13 percent of URLs even with
web search), which the plan skill's rule to fetch every cited URL addresses
more cheaply than a procedure would.

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

Log process corrections in one place, apply them on request. Task Observer,
a widely used meta-skill, keeps an observation log of corrections and skill
gaps that a person reviews on a schedule, and runs as an always-on monitor
from the first tool call. FlowSeer takes the log and the review and leaves
the monitor: `compound`'s Observe mode appends to
`docs/agent-observations.md` when a workflow skill ends with a process
correction, and `steer` works the queue when a maintainer asks, applying
the change process below to one entry at a time. An always-on observer
would spend context on every session for a signal that appears at the end
of a few. `steer` edits skills, agents, and this document in place and
stages hook, hook-registration, and `AGENTS.md` changes for a person, the
policy-surface line that `compound` also keeps. It is subject to its own
queue: an observation about `steer` is worked by `steer`. Two of Task
Observer's signals carry over as its decision rule: a step that was
followed as written and still failed wants a hook or a verifier check, not
louder prose, and a step that never fires is removed rather than kept in
case. Its audit step closes the gap the hook tests leave: those pin each
runtime's registrations but cannot say whether a rule in `AGENTS.md` has
an enforcer at all, or whether a hook added for Claude was also registered
for Codex.

Report outcome first. Each skill's report step leads with the verdict or
result and keeps the rest to a short ordered list, which is what readers of
agent output ask for and what the `i-have-adhd` skill codifies.

Trim narration, not evidence. Anthropic's Opus 5 guide says the model's
responses run longer than earlier Opus models', that effort does not
shorten them, and that an explicit instruction placed near the end of the
prompt does; `independent-reviewer` runs on Opus, so its definition ends
with one. The instruction names the parts to leave out (preamble, a
restatement of the brief, a closing summary) rather than asking for
brevity, for two reasons. Giskard's Phare study found that "answer briefly"
instructions cut hallucination resistance by up to 20%: models keep the
claim and drop the explanation, which for a reviewer means the failure
scenario. And the Opus 5 and Sonnet 5 guides both warn that a review prompt
saying "be conservative" is followed literally and lowers recall; the
reviewer already filters to correctness, and a length cap would compound
that filter. The rule stays out of `AGENTS.md` because the Fable 5.1 guide
says the coordinating model already writes too few progress updates and
that instructions to keep that text brief should be removed.

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

Sources checked on 2026-09-05 for direction records:

- [Spotify Engineering, "When Should I Write an Architecture Decision Record"](https://engineering.atspotify.com/2020/04/when-should-i-write-an-architecture-decision-record):
  a record for every decision of significant impact, with the team defining
  significant; backfill undocumented blessed solutions.
- [Catio, "Architecture Decision Records: The 2026 Guide"](https://www.catio.tech/blog/architecture-decision-record):
  write one when the decision is hard to reverse, crosses components, or
  has been debated before; a design doc yields several records that outlive
  it because nobody edits a record after acceptance.
- [Codex Knowledge Base, "Architecture Decision Records with Codex CLI"](https://codex.danielvaughan.com/2026/04/28/codex-cli-architecture-decision-records-adr-automated-governance/):
  agent-drafted records capture what was decided and tend to invent why;
  require human review before acceptance and point agents at the record
  directory before they propose.

Sources checked on 2026-09-05 for documentation lookup and research skills:

- [When LLMs Meet API Documentation](https://arxiv.org/abs/2503.15231):
  retrieval improved code generation on 1,017 rarely seen APIs by 83 to 220
  percent; code examples carried the gain, descriptions and parameter lists
  did not.
- [On Mitigating Code LLM Hallucinations with API Documentation](https://arxiv.org/abs/2407.09726):
  documentation retrieval helped low-frequency APIs and hurt high-frequency
  ones under a noisy retriever; trigger it selectively.
- [Context7 vs DeepWiki vs Ref vs Docfork](https://mcp.directory/blog/context7-vs-deepwiki-vs-ref-vs-docfork-2026):
  licensing, hosting, and free-tier comparison; declined to publish one-shot
  numbers because region and prompt shape swamped the differences.
- [Upstash, Context7 vs web search benchmark](https://upstash.com/blog/context7-vs-web-search-benchmark):
  vendor benchmark of 100 queries, 37 percent fewer total tokens than
  WebSearch, accuracy deferred to a later study.
- [Detecting and Correcting Reference Hallucinations in LLMs and Deep Research Agents](https://arxiv.org/abs/2604.03173):
  3 to 13 percent of cited URLs never existed even with web search; a URL
  liveness check cut non-resolving citations below 1 percent.
- [LiveMCPBench](https://arxiv.org/abs/2508.01780): tool retrieval errors
  cause nearly half of agent failures across 527 tools; every connected
  server adds selection noise.
- [Task Observer](https://github.com/rebelytics/one-skill-to-rule-them-all):
  an observation log of corrections and skill gaps, reviewed by a person on
  a schedule.
- [HyperFrames skills](https://github.com/heygen-com/hyperframes): a router
  skill that confirms the brief up front and loads domain skills on demand,
  the same shape as `AGENTS.md` plus scoped skills here.
- [i-have-adhd](https://github.com/ayghri/i-have-adhd): action-first
  responses, numbered steps, no preamble or recap.
- [Anthropic, "Prompting Claude Opus 5"](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-opus-5):
  longer default responses, effort does not shorten them, a conciseness
  line repeated near the end of the prompt does; "be conservative" in a
  review prompt lowers recall.
- [Anthropic, "Prompting Claude Sonnet 5"](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-sonnet-5):
  length already tracks task complexity; literal instruction following;
  positive examples beat "don't" lists.
- [Anthropic, "Prompting Claude Fable 5.1"](https://platform.claude.com/docs/en/build-with-claude/prompt-engineering/prompting-claude-fable-5-1):
  fewer progress updates by default; remove instructions that keep
  user-facing text brief.
- [Giskard, Phare hallucination analysis](https://huggingface.co/blog/davidberenstein1957/phare-analysis-of-hallucination-in-leading-llms):
  conciseness instructions cut hallucination resistance by up to 20%; the
  explanation goes before the claim does.
- Orca CLI and orchestration guides, served version-matched by the binary
  through `orca skills get orca-cli` and `orca skills get orchestration`.

The common recommendation is progressive disclosure. The inference for
FlowSeer is to keep `AGENTS.md` near its current size, add scoped steering only
when a real local rule appears, and invest in executable checks before adding
more prose.
