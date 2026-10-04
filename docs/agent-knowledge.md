# Agent knowledge boundaries

FlowSeer keeps durable knowledge in the repository, not in any tool's memory.
Use the narrowest durable location that fits the information.

| Knowledge | Location | Lifetime and audience |
| --- | --- | --- |
| Human overview and documentation map | `README.md`, `docs/README.md` | Durable; contributors learning or navigating the repository |
| Contribution workflow and checks | `CONTRIBUTING.md` | Durable; anyone preparing a change, restating rules that `AGENTS.md` owns |
| Project entry points and binding rules | `AGENTS.md`, linked from `CLAUDE.md` | Durable; humans and all coding agents |
| Steering design and maintenance | `docs/agent-steering.md` | Durable; maintainers changing instructions, skills, agents, or enforcement |
| Accepted system direction | `docs/architecture/` | Durable; architecture decisions and constraints |
| Solved problems and reusable lessons | `docs/solutions/` | Durable; evidence-backed implementation knowledge |
| Repeatable agent workflows | `.agents/skills/` (`.claude/skills/` links here) | Durable; task procedures with scripts when useful |
| Narrow specialist behavior | `.claude/agents/` | Durable; bounded delegation roles |
| Observed gaps in skills, agents, or hooks | `docs/agent-observations.md` | Queue; `steer` applies or rejects each entry on a maintainer's request and deletes it |
| Scratch notes and findings | Current conversation or worktree | Temporary; discard or promote before handoff |

Claude auto-memory is off for this repository (`autoMemoryEnabled: false` in
`.claude/settings.json`). Several people work on FlowSeer from several machines,
and a machine-local memory reaches none of the others. A fact worth keeping goes
into one of the documents above.

Before writing one down, verify it against the current code. Never store
secrets, credentials, personal data, transient branches, absolute machine
paths, or unverified guesses in shared knowledge.

Before promoting a recurring instruction, use
[`agent-steering.md`](agent-steering.md) to choose its scope. Put behavior that
must be enforced in a hook, linter, schema check, or test; prose remains the
place for context and judgment.
