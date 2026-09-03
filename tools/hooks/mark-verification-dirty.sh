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
  if grep -Eq '(^|[;&|()[:space:]])(apply_patch|patch|sed[[:space:]]+-i|perl[[:space:]]+-pi|gofmt[[:space:]].*-w|gofumpt[[:space:]].*-w|goimports[[:space:]].*-w|buf[[:space:]]+(format[[:space:]].*-w|generate|dep[[:space:]]+update)|go[[:space:]]+mod[[:space:]]+(tidy|edit))([[:space:]]|$)|>[[:space:]]*[^[:space:]]+\.(go|proto|ts|tsx|js|jsx|py|sh|md|json|ya?ml)' <<<"$command"; then
    printf '%s\n' '<Bash mutation; verify with --full>' >>"$git_dir/flowseer-verification-dirty"
  fi
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
