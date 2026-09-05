#!/usr/bin/env bash
# PostToolUse: report new lint or check suppressions introduced by an edit.
#
# AGENTS.md forbids adding an exclusion, ignore, or suppression to make one's
# own artifacts pass. That is a judgment call, so this hook does not block;
# it names the suppressions the edit wrote so the agent has to justify them
# in the handoff, and the reviewer sees them.

set -uo pipefail

input=$(cat)
jq -e . >/dev/null 2>&1 <<<"$input" || exit 0

added=$(jq -r '
  .tool_input
  | [.new_string?, .content?, (.edits? // [] | .[] | .new_string?)]
  | map(select(type == "string"))
  | join("\n")
' <<<"$input")
[ -n "$added" ] || exit 0

matches=$(grep -nE '//[[:space:]]*nolint|buf:lint:ignore|buf:breaking:ignore|shellcheck disable|#[[:space:]]*nosec|eslint-disable|@ts-ignore|@ts-expect-error|//[[:space:]]*lint:ignore|#[[:space:]]*noqa|exclude-rules|skip-dirs' <<<"$added" | head -5)
[ -n "$matches" ] || exit 0

file=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // "the edit"' <<<"$input")
jq -n --arg message "This edit to $file adds a lint or check suppression:"$'\n'"$matches"$'\n'"AGENTS.md forbids suppressions that make your own artifacts pass. Remove it and fix the finding, or state in the handoff why this exact suppression is the right policy change and propose it separately." \
  '{hookSpecificOutput:{hookEventName:"PostToolUse",additionalContext:$message}}'
