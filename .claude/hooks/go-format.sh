#!/usr/bin/env bash
#
# go-format.sh — PostToolUse on Edit|Write|NotebookEdit.
#
# Runs the repo's format gate (gofumpt + goimports, per .golangci.yml) on any Go
# file Claude just touched, so edits never land lint-dirty. Silent on success.
#
# Skips generated/ — that tree is buf output and is never hand-edited.

set -uo pipefail

input=$(cat)
file=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // ""' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")

case "$file" in
  *.go) ;;
  *) exit 0 ;;
esac

case "$file" in
  /*) abs="$file" ;;
  *)  abs="$cwd/$file" ;;
esac
[ -f "$abs" ] || exit 0

case "$abs" in
  */generated/*) exit 0 ;;
esac

command -v gofumpt >/dev/null 2>&1 || exit 0

before=$(shasum "$abs" 2>/dev/null | cut -d' ' -f1)

gofumpt -w "$abs" 2>/dev/null
command -v goimports >/dev/null 2>&1 && goimports -w "$abs" 2>/dev/null

after=$(shasum "$abs" 2>/dev/null | cut -d' ' -f1)
[ "$before" = "$after" ] && exit 0

jq -n --arg f "$file" \
  '{hookSpecificOutput:{hookEventName:"PostToolUse",additionalContext:("gofumpt + goimports reformatted \($f) after your edit. The file on disk now differs from what you wrote — re-read it before making further edits to that region.")}}'
exit 0
