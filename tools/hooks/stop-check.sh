#!/usr/bin/env bash
# Stop gate: the repository conformance gates must hold, and unverified
# source edits are reported (not blocked) so the handoff names them.

set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=tools/hooks/common.sh
source "$script_dir/common.sh"
# The conformance gates below shell out to go, which the environment that
# started the client need not have on PATH.
hook_go_on_path

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

# Every package under test/conformance/ is a gate, and each runs whole. The
# hook enumerates the directory rather than naming packages or tests: a
# `-run` pattern for a renamed test matches nothing and `go test` reports
# ok, and a listed path for a renamed package is skipped the same way, so
# a gate could vanish with no signal on either axis. Enumeration has no
# name to rot; a gate added later runs without a hook edit. Each child
# runs recursively, so a gate that lands one level down (a
# test/conformance/proto/layering/, say) runs here and not only at the
# merge gate; a child holding no Go package fails loudly with `no
# packages to test` instead of counting as a pass.
#
# The third axis is the root directory itself: a moved or emptied
# test/conformance/ leaves the glob unmatched, and a hook that only
# tolerated that would print {} with nothing run. So the gates are
# counted, and zero in a checkout of this repository, which go.mod
# identifies, is a failed gate. A fixture without go.mod has no gates to
# run and still stops cleanly.
#
# This is fast feedback, not the authority. `go test -race ./...` runs the
# same tests at the merge gate, and that is what AGENTS.md points at.
output=""
gates_ok=true
gates_run=0
for package in "$root"/test/conformance/*/; do
  # With no test/conformance/ the glob stays literal; nothing else is skipped.
  [ -d "$package" ] || continue
  gates_run=$((gates_run + 1))
  if ! output=$(cd "$root" && go test "./${package#"$root/"}..." 2>&1); then
    gates_ok=false
    break
  fi
done
if [ "$gates_run" -eq 0 ] && [ -f "$root/go.mod" ]; then
  gates_ok=false
  output="no conformance gate found under test/conformance/; the directory is missing or empty, so nothing was checked"
fi

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
