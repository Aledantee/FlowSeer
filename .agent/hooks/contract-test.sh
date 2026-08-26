#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR

set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
root=$(git -C "$script_dir" rev-parse --show-toplevel) || exit 1

expect_denied() {
  local name="$1"
  local payload="$2"
  local output

  output=$(printf '%s' "$payload" | "$script_dir/pre-tool-policy.sh")
  if [ "$(jq -r '.hookSpecificOutput.permissionDecision // empty' <<<"$output")" != deny ]; then
    printf '%s: expected deny, got %s\n' "$name" "${output:-no decision}" >&2
    return 1
  fi
}

expect_allowed() {
  local name="$1"
  local payload="$2"
  local output

  output=$(printf '%s' "$payload" | "$script_dir/pre-tool-policy.sh")
  if [ -n "$output" ]; then
    printf '%s: expected allow, got %s\n' "$name" "$output" >&2
    return 1
  fi
}

claude_payload=$(jq -n --arg cwd "$root" --arg file "spec/proto/rules_test.go" \
  '{cwd:$cwd,tool_input:{file_path:$file}}')
expect_denied "Claude direct path" "$claude_payload"

codex_payload=$(jq -n --arg cwd "$root" --arg command $'*** Begin Patch\n*** Update File: spec/proto/flowseer/net/addr/v1/ip.proto\n*** Add File: spec/proto/layering_test.go\n*** End Patch' \
  '{cwd:$cwd,tool_input:{command:$command}}')
expect_denied "Codex mixed patch" "$codex_payload"

codex_payload=$(jq -n --arg cwd "$root" --arg command $'*** Begin Patch\n*** Update File: spec/proto/flowseer/net/addr/v1/ip.proto\n*** End Patch' \
  '{cwd:$cwd,tool_input:{command:$command}}')
expect_allowed "Codex schema patch" "$codex_payload"

codex_payload=$(jq -n --arg cwd "$root/src/common" --arg command $'*** Begin Patch\n*** Add File: ../../spec/proto/bypass_test.go\n*** End Patch' \
  '{cwd:$cwd,tool_input:{command:$command}}')
expect_denied "traversal path" "$codex_payload"
