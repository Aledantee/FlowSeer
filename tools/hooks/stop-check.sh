#!/usr/bin/env bash
# Stop gate: the repository layout policy must hold, and unverified source
# edits are reported (not blocked) so the handoff names them.

set -uo pipefail

input=$(cat)
cwd=$(jq -r '.cwd // "."' <<<"$input")
root=$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null) || {
  printf '{}\n'
  exit 0
}

# The dirty marker is written by mark-verification-dirty.sh and cleared per
# path by the verifier. Non-empty at Stop means edits nobody verified.
git_dir=$(git -C "$root" rev-parse --git-dir 2>/dev/null) || git_dir=""
case "$git_dir" in
  ""|/*) ;;
  *) git_dir=$root/$git_dir ;;
esac
unverified=""
if [ -n "$git_dir" ] && [ -s "$git_dir/flowseer-verification-dirty" ]; then
  unverified=$(sort -u "$git_dir/flowseer-verification-dirty" | head -20 | paste -sd ' ' -)
fi

output=""
layout_ok=true
if [ -d "$root/test/conformance/proto" ]; then
  # The layout test is the fast slice of the conformance package; the rest of
  # the package compiles alongside it and runs in the verifier.
  if ! output=$(cd "$root" && go test -run 'TestProtoSourceTreeLayout|TestProtoPathPolicy' ./test/conformance/proto 2>&1); then
    layout_ok=false
  fi
fi

if [ "$layout_ok" = true ]; then
  if [ -n "$unverified" ]; then
    jq -n --arg message "Edited but not verified: $unverified. Run .claude/skills/verify-change/scripts/verify-change.sh -- <changed paths> before handoff." \
      '{systemMessage:$message}'
  else
    printf '{}\n'
  fi
  exit 0
fi

reason="Repository layout policy failed. Fix the violations before stopping:"$'\n'"$output"
if [ "$(jq -r '.stop_hook_active // false' <<<"$input")" = true ]; then
  jq -n --arg message "$reason" '{systemMessage:$message}'
else
  jq -n --arg reason "$reason" '{decision:"block",reason:$reason}'
fi
