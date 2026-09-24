#!/usr/bin/env bash

set -uo pipefail

hook_init() {
  HOOK_INPUT=$(cat)
  jq -e . >/dev/null 2>&1 <<<"$HOOK_INPUT" || return 1
  HOOK_CWD=$(jq -r '.cwd // "."' <<<"$HOOK_INPUT")
  HOOK_ROOT=$(git -C "$HOOK_CWD" rev-parse --show-toplevel 2>/dev/null) || exit 0
  HOOK_ROOT=$(cd "$HOOK_ROOT" && pwd -P) || exit 0
  HOOK_PREFIX=$(git -C "$HOOK_CWD" rev-parse --show-prefix 2>/dev/null) || exit 0
  hook_go_tools_on_path
}

# The toolchain itself has the same problem one level down. A hook inherits
# the environment of the process that started the client, and a client
# launched from anything but a login shell never read ~/.profile, so the
# directory a tarball install puts go in is missing and every Go gate reports
# "go: command not found" on a machine where go works fine. Add the standard
# install locations, and only when go is not already resolvable, so a PATH
# that is already correct is left alone.
hook_go_on_path() {
  local candidate
  command -v go >/dev/null 2>&1 && return 0
  for candidate in /usr/local/go/bin "${HOME:-}/go/bin" /usr/lib/go/bin; do
    [ -x "$candidate/go" ] || continue
    case ":$PATH:" in
      *":$candidate:"*) ;;
      *) PATH=$PATH:$candidate ;;
    esac
    export PATH
    return 0
  done
  return 0
}

# Go tools install to $(go env GOPATH)/bin, which a session's PATH does not
# always carry; the format hook then reports every Go edit as unformatted
# although the formatters are installed. Search that directory whenever go
# itself is found, so the session's PATH does not decide what a hook can do.
hook_go_tools_on_path() {
  local gobin gopath
  hook_go_on_path
  command -v go >/dev/null 2>&1 || return 0
  # GOBIN wins when set; otherwise the first GOPATH entry, which is where
  # go install writes. An empty answer adds nothing: "/bin" ahead of PATH
  # would shadow every later entry.
  gobin=$(go env GOBIN 2>/dev/null) || gobin=""
  if [ -z "$gobin" ]; then
    gopath=$(go env GOPATH 2>/dev/null) || gopath=""
    gopath=${gopath%%:*}
    [ -n "$gopath" ] && gobin=$gopath/bin
  fi
  [ -n "$gobin" ] || return 0
  case ":$PATH:" in
    *":$gobin:"*) ;;
    *) PATH=$gobin:$PATH ;;
  esac
  return 0
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
