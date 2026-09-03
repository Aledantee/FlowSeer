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

The common recommendation is progressive disclosure. The inference for
FlowSeer is to keep `AGENTS.md` near its current size, add scoped steering only
when a real local rule appears, and invest in executable checks before adding
more prose.
