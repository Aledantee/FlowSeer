#!/usr/bin/env bash
# Deny common direct shell mutations of buf-managed output and buf.lock.

set -uo pipefail

input=$(cat)
jq -e . >/dev/null 2>&1 <<<"$input" || {
  jq -n '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:"Generated-file Bash guard received malformed hook input and failed closed."}}'
  exit 0
}
command=$(jq -r '.tool_input.command // ""' <<<"$input")
[[ -n $command ]] || exit 0

case "$command" in
  *generated/*|*buf.lock*) ;;
  *) exit 0 ;;
esac

deny() {
  jq -n --arg r "$1" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'
  exit 0
}

# Bash cannot be parsed safely with a regular expression. Guard the common
# mutation paths and rely on the native sandbox plus review for opaque scripts.
if grep -Eq '(^|[;&|()[:space:]])(rm|mv|cp|touch|truncate|tee|sed|perl|python|python3|ruby|dd|install|chmod|chown|nano|vim|vi|ex|ed|emacs|rsync|patch|apply_patch)([[:space:]]|$)' <<<"$command"; then
  deny "Do not mutate generated/, frontend/web/generated/, or buf.lock from Bash. Change the source of truth, then use 'buf generate' or 'buf dep update'."
fi

if grep -Eq '(^|[;&|()[:space:]])git[[:space:]]+(apply|am|checkout|restore|clean|rm|mv)([[:space:]]|$)' <<<"$command"; then
  deny "Git may not overwrite or remove buf-managed output directly. Regenerate it from the source of truth."
fi

output_pattern="(>|-o[[:space:]]+|-O[[:space:]]+)[[:space:]]*[\"']?([^[:space:]\"']*/)?((frontend/web/)?generated/|buf\\.lock)"
if grep -Eq "$output_pattern" <<<"$command"; then
  deny "Shell redirection or download output may not write buf-managed files. Regenerate them from the source of truth."
fi

if grep -Eq '(^|[;&|()[:space:]])find[[:space:]].*(-delete|-exec|-execdir)([[:space:]]|$)' <<<"$command"; then
  deny "Do not mutate buf-managed files through find. Regenerate them from the source of truth."
fi

exit 0
