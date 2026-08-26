#!/usr/bin/env bash

set -uo pipefail

hook_init() {
  HOOK_INPUT=$(cat)
  HOOK_CWD=$(jq -r '.cwd // "."' <<<"$HOOK_INPUT")
  HOOK_ROOT=$(git -C "$HOOK_CWD" rev-parse --show-toplevel 2>/dev/null) || exit 0
  HOOK_ROOT=$(cd "$HOOK_ROOT" && pwd -P) || exit 0
  HOOK_PREFIX=$(git -C "$HOOK_CWD" rev-parse --show-prefix 2>/dev/null) || exit 0
}

hook_paths() {
  jq -r '.tool_input.file_path // .tool_input.notebook_path // empty' <<<"$HOOK_INPUT"
  jq -r '.tool_input.command // empty' <<<"$HOOK_INPUT" |
    sed -nE \
      -e 's/^\*\*\* (Add|Update|Delete) File: (.*)$/\2/p' \
      -e 's/^\*\*\* Move to: (.*)$/\1/p'
}

hook_relative_path() {
  local candidate_file="$1"
  local relative_file

  case "$candidate_file" in
    "$HOOK_ROOT") relative_file="" ;;
    "$HOOK_ROOT"/*) relative_file=${candidate_file#"$HOOK_ROOT"/} ;;
    /*) return 1 ;;
    *) relative_file="$HOOK_PREFIX$candidate_file" ;;
  esac

  while [[ "$relative_file" == ./* ]]; do
    relative_file=${relative_file#./}
  done
  case "/$relative_file/" in
    */../*|*/./*) return 1 ;;
  esac

  printf '%s\n' "$relative_file"
}

hook_absolute_path() {
  local relative_file="$1"
  printf '%s/%s\n' "$HOOK_ROOT" "$relative_file"
}

hook_deny() {
  jq -n --arg reason "$1" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$reason}}'
  exit 0
}

hook_context() {
  jq -n --arg message "$1" \
    '{hookSpecificOutput:{hookEventName:"PostToolUse",additionalContext:$message}}'
}
