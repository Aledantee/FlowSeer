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
`implement`, `review`, and `compound`, next to the `verify-change` gate. They
replace the third-party compound-engineering plugin, which the repository used
from August 2026 until 2026-09-05. The decisions below were taken against the
plugin's issue tracker, published measurements, and this project's own session
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

The common recommendation is progressive disclosure. The inference for
FlowSeer is to keep `AGENTS.md` near its current size, add scoped steering only
when a real local rule appears, and invest in executable checks before adding
more prose.
