#!/usr/bin/env bash

set -uo pipefail

hook_init() {
  HOOK_INPUT=$(cat)
  jq -e . >/dev/null 2>&1 <<<"$HOOK_INPUT" || return 1
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

# Prints the physical path of a directory that may not exist yet: the
# deepest existing ancestor is resolved through symlinks and the missing
# suffix is appended verbatim.
hook_canonical_dir() {
  local candidate="$1"
  local parent
  local suffix=""

  while [[ ! -d "$candidate" ]]; do
    suffix="/$(basename "$candidate")$suffix"
    parent=$(dirname "$candidate")
    [[ "$parent" != "$candidate" ]] || return 1
    candidate=$parent
  done

  candidate=$(cd "$candidate" 2>/dev/null && pwd -P) || return 1
  printf '%s%s\n' "$candidate" "$suffix"
}

# Prints the repository-relative path for a candidate file.
# Returns 1 when the path cannot be resolved safely, 2 when the path is
# absolute and lies outside the repository (repository policy does not apply).
hook_relative_path() {
  local candidate_file="$1"
  local candidate_dir
  local relative_file

  if [[ "$candidate_file" == /* ]]; then
    # A Write may create the file's parent directories, so resolve the
    # deepest ancestor that exists and reattach the missing tail unchanged.
    candidate_dir=$(hook_canonical_dir "$(dirname "$candidate_file")") || return 1
    candidate_file="$candidate_dir/$(basename "$candidate_file")"
  fi

  case "$candidate_file" in
    "$HOOK_ROOT") relative_file="" ;;
    "$HOOK_ROOT"/*) relative_file=${candidate_file#"$HOOK_ROOT"/} ;;
    /*) return 2 ;;
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

hook_ask() {
  jq -n --arg reason "$1" \
    '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"ask",permissionDecisionReason:$reason}}'
  exit 0
}

hook_context() {
  jq -n --arg message "$1" \
    '{hookSpecificOutput:{hookEventName:"PostToolUse",additionalContext:$message}}'
}
