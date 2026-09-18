#!/usr/bin/env bash
# Record source/config edits that require a successful verify-change run.

set -uo pipefail

input=$(cat)
jq -e . >/dev/null 2>&1 <<<"$input" || exit 0
file=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // ""' <<<"$input")
command=$(jq -r '.tool_input.command // ""' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")
[[ -n $cwd ]] || exit 0

root=$(cd "$cwd" 2>/dev/null && git rev-parse --show-toplevel 2>/dev/null) || exit 0
root=$(cd "$root" 2>/dev/null && pwd -P) || exit 0
git_dir=$(cd "$root" && git rev-parse --git-dir) || exit 0
case "$git_dir" in
  /*) ;;
  *) git_dir=$root/$git_dir ;;
esac

if [[ -n $command ]]; then
  # What a command wrote is read off the tree, not guessed from its text: a
  # pattern over the command line missed python, cp and tee and flagged a
  # grep for "patch". Every dirty path whose content hash is not in the
  # stored listing changed since the last Bash call or the last verified
  # run, and is marked like an editor edit. No stored listing marks every
  # dirty path once, which in a fresh worktree is exactly what the command
  # wrote.
  state=$git_dir/flowseer-tree-state
  current=$(mktemp "$git_dir/flowseer-tree-state.XXXXXX") || exit 0
  if ! "$(dirname "$0")/tree-state.sh" "$root" >"$current"; then
    rm -f "$current"
    exit 0
  fi
  [[ -f $state ]] || : >"$state"
  full=false
  while IFS=$'\t' read -r _ rel; do
    [[ -n $rel ]] || continue
    printf '%s\n' "$rel" >>"$git_dir/flowseer-verification-dirty"
    # A generator's output and the module graph reach packages no path
    # names, so only these keep the module-wide run.
    case "$rel" in
      generated/*|*/generated/*|go.mod|go.sum|*/go.mod|*/go.sum|buf.lock|*/buf.lock) full=true ;;
    esac
  done < <(LC_ALL=C comm -13 "$state" "$current")
  if [[ $full == true ]]; then
    printf '%s\n' '<Bash mutation; verify with --full>' >>"$git_dir/flowseer-verification-dirty"
  fi
  mv "$current" "$state"
  exit 0
fi

[[ -n $file ]] || exit 0

case "$file" in
  /*) abs=$file ;;
  *) abs=$cwd/$file ;;
esac

if absdir=$(cd "$(dirname "$abs")" 2>/dev/null && pwd -P); then
  abs=$absdir/$(basename "$abs")
fi
case "$abs" in
  "$root"/*) rel=${abs#"$root"/} ;;
  *) exit 0 ;;
esac

case "$rel" in
  *.go|*.proto|*.ts|*.tsx|*.js|*.jsx|*.py|*.sh|*.md|*.json|*.yaml|*.yml|go.mod|go.sum)
    ;;
  *) exit 0 ;;
esac

printf '%s\n' "$rel" >>"$git_dir/flowseer-verification-dirty"
