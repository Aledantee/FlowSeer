#!/usr/bin/env bash
# Block one completion attempt when source/config edits lack verification.

set -uo pipefail

input=$(cat)
jq -e . >/dev/null 2>&1 <<<"$input" || exit 0
cwd=$(jq -r '.cwd // ""' <<<"$input")
event=$(jq -r '.hook_event_name // ""' <<<"$input")
[[ -n $cwd ]] || exit 0

root=$(cd "$cwd" 2>/dev/null && git rev-parse --show-toplevel 2>/dev/null) || exit 0
git_dir=$(cd "$root" && git rev-parse --git-dir) || exit 0
case "$git_dir" in
  /*) ;;
  *) git_dir=$root/$git_dir ;;
esac
marker=$git_dir/flowseer-verification-dirty
[[ -s $marker ]] || exit 0

if [[ $event == Stop ]] && [[ $(jq -r '.stop_hook_active // false' <<<"$input") == true ]]; then
  exit 0
fi

paths=$(sort -u "$marker" | head -n 12 | paste -sd ', ' -)
reason="Verification is stale after edits to: $paths. Run the FlowSeer verify-change skill for the edited scope, fix any failures, then complete the task."

if [[ $event == TaskCompleted ]]; then
  printf '%s\n' "$reason" >&2
  exit 2
fi

jq -n --arg reason "$reason" '{decision:"block",reason:$reason}'
