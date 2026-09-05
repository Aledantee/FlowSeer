#!/usr/bin/env bash
# Deny direct shell mutations of buf-managed output and buf.lock.
#
# Bash cannot be parsed safely with a regular expression, so this guard works
# per simple command: it denies when a guarded path is the target of a
# redirect or download, or when the command's verb mutates files and the
# command names a guarded path. Reads such as `sed -n 5p generated/x.pb.go`
# or a `grep ... | sed` pipeline pass. Opaque scripts are left to the sandbox
# and review.

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

mutate_reason="Do not mutate generated/, frontend/web/generated/, or buf.lock from Bash. Change the source of truth, then use 'buf generate' or 'buf dep update'."
guarded='((^|[^[:alnum:]_.-])(frontend/web/)?generated/|(^|[^[:alnum:]_.-])buf\.lock([^[:alnum:]_.-]|$))'
mutators='rm|mv|cp|touch|truncate|tee|dd|install|chmod|chown|rsync|patch|apply_patch|nano|vim|vi|ex|ed|emacs'

# An apply_patch heredoc names its files on "*** ... File:" lines, not as
# operands of the verb, so judge the whole command for it first.
if grep -Eq '^\*\*\* (Add|Update|Delete) File: .*((frontend/web/)?generated/|buf\.lock)' <<<"$command"; then
  deny "$mutate_reason"
fi

while IFS= read -r simple; do
  simple=${simple#"${simple%%[![:space:]]*}"}
  [[ -n $simple ]] || continue
  grep -Eq "$guarded" <<<"$simple" || continue

  if grep -Eq "(>|-o[[:space:]]+|-O[[:space:]]+)[[:space:]]*[\"']?([^[:space:]\"']*/)?((frontend/web/)?generated/|buf\\.lock)" <<<"$simple"; then
    deny "Shell redirection or download output may not write buf-managed files. Regenerate them from the source of truth."
  fi

  read -r verb rest <<<"$simple"
  case "$verb" in
    sudo|time|command|nice|nohup|env|exec) read -r verb rest <<<"$rest" ;;
  esac
  verb=${verb##*/}

  if [[ $verb =~ ^($mutators)$ ]]; then
    deny "$mutate_reason"
  fi
  case "$verb" in
    sed|perl)
      if grep -Eq '(^|[[:space:]])-[a-zA-Z]*i' <<<"$rest"; then
        deny "$mutate_reason"
      fi
      ;;
    python|python3|ruby)
      # A -c one-liner that only prints is a read; a script file or an
      # open() for writing is not.
      if ! grep -Eq '(^|[[:space:]])-c[[:space:]]' <<<"$rest" || grep -Eq "open\\([^)]*[\"'](w|a|r\\+)" <<<"$rest"; then
        deny "$mutate_reason"
      fi
      ;;
    git)
      if grep -Eq '^(-C[[:space:]]+[^[:space:]]+[[:space:]]+)?(apply|am|checkout|restore|clean|rm|mv)([[:space:]]|$)' <<<"$rest"; then
        deny "Git may not overwrite or remove buf-managed output directly. Regenerate it from the source of truth."
      fi
      ;;
    find)
      if grep -Eq '(^|[[:space:]])(-delete|-exec|-execdir)([[:space:]]|$)' <<<"$rest"; then
        deny "Do not mutate buf-managed files through find. Regenerate them from the source of truth."
      fi
      ;;
  esac
done < <(printf '%s\n' "$command" | tr ';&|()' '\n')

exit 0
