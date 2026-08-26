# Agent knowledge boundaries

FlowSeer keeps durable knowledge in the repository and treats tool-specific
memory as a private convenience. Use the narrowest durable location that fits the
information.

| Knowledge | Location | Lifetime and audience |
| --- | --- | --- |
| Project entry points and binding rules | `AGENTS.md`, imported by `CLAUDE.md` | Durable; humans and all coding agents |
| Accepted system direction | `docs/architecture/` | Durable; architecture decisions and constraints |
| Solved problems and reusable lessons | `docs/solutions/` | Durable; evidence-backed implementation knowledge |
| Repeatable agent workflows | `.claude/skills/` | Durable; task procedures with scripts when useful |
| Narrow specialist behavior | `.claude/agents/` | Durable; bounded delegation roles |
| Claude auto-memory | Claude's local memory directory | Personal, machine-local, advisory, and potentially stale |
| Session notes and scratch findings | Current conversation or worktree | Temporary; discard or promote before handoff |

Do not copy auto-memory into the repository wholesale. Before promoting a memory,
verify it against the current code and place it in the appropriate shared document.
Never store secrets, credentials, personal data, transient branches, absolute
machine paths, or unverified guesses in shared knowledge.

When shared guidance and auto-memory disagree, the repository wins. Update or
remove the stale memory; do not weaken a project rule to preserve it.

