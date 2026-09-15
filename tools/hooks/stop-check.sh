#!/usr/bin/env bash
# Stop gate: the repository conformance gates must hold, and unverified
# source edits are reported (not blocked) so the handoff names them.

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

# The gate packages cheap enough to run whole at every Stop. Whole, because
# a `-run` pattern naming a test that was renamed or split matches nothing,
# and `go test` then reports ok with nothing run; a gate can vanish that
# way with no signal. A package the checkout does not have is skipped, so
# a fixture or an older tree still stops cleanly.
#
# This is fast feedback, not the authority. `go test -race ./...` runs the
# same tests at the merge gate, and that is what AGENTS.md points at.
gates="test/conformance/proto
test/conformance/panic"

output=""
gates_ok=true
while read -r package; do
  [ -d "$root/$package" ] || continue
  # The loop's stdin is the gate list. `go test` in package-list mode gives
  # its test binary no stdin at all, but a gate that reads what it is
  # handed would swallow the remaining entries, so none gets the list.
  if ! output=$(cd "$root" && go test "./$package" 2>&1 </dev/null); then
    gates_ok=false
    break
  fi
done <<<"$gates"

if [ "$gates_ok" = true ]; then
  if [ -n "$unverified" ]; then
    jq -n --arg message "Edited but not verified: $unverified. Run .claude/skills/verify-change/scripts/verify-change.sh -- <changed paths> before handoff." \
      '{systemMessage:$message}'
  else
    printf '{}\n'
  fi
  exit 0
fi

reason="A repository conformance gate failed. Fix the violations before stopping:"$'\n'"$output"
if [ "$(jq -r '.stop_hook_active // false' <<<"$input")" = true ]; then
  jq -n --arg message "$reason" '{systemMessage:$message}'
else
  jq -n --arg reason "$reason" '{decision:"block",reason:$reason}'
fi
