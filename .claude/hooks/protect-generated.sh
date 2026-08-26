#!/usr/bin/env bash
#
# protect-generated.sh — PreToolUse on Edit|Write|NotebookEdit.
#
# AGENTS.md: "generated/ is buf generate output and is never edited by hand."
# This makes that rule non-negotiable rather than advisory. Same for the TS leg
# under frontend/web/generated/ and for buf.lock.

set -uo pipefail

input=$(cat)
jq -e . >/dev/null 2>&1 <<<"$input" || {
  jq -n '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:"Generated-file guard received malformed hook input and failed closed."}}'
  exit 0
}
file=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // ""' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")
[ -n "$file" ] || exit 0

case "$file" in
  /*) abs="$file" ;;
  *)  abs="$cwd/$file" ;;
esac

# Resolve to physical paths — /var vs /private/var style symlinks otherwise
# defeat the repo-prefix match below. dirname only: the file may not exist yet
# on a Write.
# Non-fatal: a Write may target a directory that does not exist yet, in which
# case the unresolved path is still the best guess and must stay guarded.
if absdir=$(cd "$(dirname "$abs")" 2>/dev/null && pwd -P); then
  abs="$absdir/$(basename "$abs")"
fi

root=$(cd "$cwd" 2>/dev/null && git rev-parse --show-toplevel 2>/dev/null) || exit 0
root=$(cd "$root" 2>/dev/null && pwd -P) || exit 0
rel=${abs#"$root"/}

deny() {
  jq -n --arg r "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'
  exit 0
}

case "$rel" in
  generated/*|frontend/web/generated/*)
    deny "$rel is buf generate output and is never edited by hand (AGENTS.md). Change the source of truth in spec/proto/ or the codegen config in buf.gen.yaml, then run 'buf generate'." ;;
  buf.lock)
    deny "buf.lock is managed by buf. Edit deps in buf.yaml and run 'buf dep update' instead of writing the lockfile directly." ;;
esac

exit 0
