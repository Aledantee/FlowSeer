# FlowSeer Claude instructions

@AGENTS.md

## Claude Code behavior

- Use Claude Code's native tools and subagents. The compatibility mapping inside
  `AGENTS.md` is for Codex only and does not replace Claude Code capabilities.
- Before the first mutation on a protected branch or in the primary checkout, use
  `EnterWorktree` with a short task-shaped name. Do not emulate isolation by
  editing the primary checkout.
- Use the read-only `repo-researcher` subagent for a bounded repository question
  that can be investigated independently. Use `independent-reviewer` for a fresh
  standards and correctness pass over specified changed files.
- Keep small, sequential tasks in the main conversation. Give every subagent a
  precise question, relevant paths, and the expected output; do not delegate the
  same investigation twice.
- Treat native auto-memory as personal and fallible. Promote durable team facts
  to the repository locations defined in `docs/agent-knowledge.md`.

## Investigation discipline

- State the leading hypothesis, plausible alternatives, and a discriminating test
  before claiming a root cause. Run the test and cite the evidence before editing.
- Never guess a device identity, hostname, or port mapping; query the available
  inventory or control plane first.
- After roughly 15 exploratory shell commands without a concrete finding, stop and
  summarize what is ruled out and the two best next checks before continuing.
- Before a live-device write or a data mutation affecting more than 10,000 records,
  state the blast radius and wait for explicit approval.
