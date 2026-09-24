#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR

set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=common.sh
source "$script_dir/common.sh"

hook_init || exit 0
[ -d "$HOOK_ROOT/spec/proto" ] || exit 0

proto_files=""
while IFS= read -r candidate_file; do
  [ -n "$candidate_file" ] || continue
  relative_file=$(hook_relative_path "$candidate_file") || continue
  case "$relative_file" in
    spec/proto/*.proto) proto_files="$proto_files$relative_file"$'\n' ;;
  esac
done < <(hook_paths)

[ -n "$proto_files" ] || exit 0

lint_failures=""
context=""
if command -v buf >/dev/null 2>&1; then
  while IFS= read -r relative_file; do
    [ -n "$relative_file" ] || continue
    absolute_file=$(hook_absolute_path "$relative_file")
    [ -f "$absolute_file" ] || continue

    buf format -w "$absolute_file" >/dev/null 2>&1
    if ! lint_output=$(cd "$HOOK_ROOT" && buf lint --path "$relative_file" 2>&1); then
      lint_failures="$lint_failures${lint_failures:+$'\n\n'}buf lint failed on $relative_file:"$'\n'"$lint_output"
    fi
  done <<<"$proto_files"
else
  context="buf is not on PATH; edited proto files were not formatted or linted. Install the repository toolchain and check them before stopping."
fi

seen=" "
sync_notes=""

check_family() {
  local base="$1"
  shift
  local family="$1"

  case "$seen" in
    *" $family:$base "*) return ;;
  esac
  seen="$seen$family:$base "

  local missing=""
  local suffix
  for suffix in "$@"; do
    if grep -rqE "^[[:space:]]*message[[:space:]]+${base}${suffix}[[:space:]]*\\{" "$HOOK_ROOT/spec/proto"; then
      continue
    fi
    # docs/conventions/protobuf.md asks the file-level comment of a
    # deliberately partial family to name the absent member. A named member
    # is a decision, not an omission, so it is not reported.
    if grep -Eq "^[[:space:]]*//.*\\b${base}${suffix}\\b" "$absolute_file"; then
      continue
    fi
    missing="$missing ${base}${suffix}"
  done
  if [ -n "$missing" ]; then
    sync_notes="$sync_notes${sync_notes:+$'\n'}- $base: no message found for$missing"
  fi
}

while IFS= read -r relative_file; do
  [ -n "$relative_file" ] || continue
  absolute_file=$(hook_absolute_path "$relative_file")
  [ -f "$absolute_file" ] || continue

  while IFS= read -r message_name; do
    case "$message_name" in
      *Config) check_family "${message_name%Config}" Config State Event ;;
      *State) check_family "${message_name%State}" Config State Event ;;
      *Event) check_family "${message_name%Event}" Config State Event ;;
      *GlobalRef) check_family "${message_name%GlobalRef}" GlobalRef LocalRef ;;
      *LocalRef) check_family "${message_name%LocalRef}" GlobalRef LocalRef ;;
    esac
  done < <(sed -nE 's/^[[:space:]]*message[[:space:]]+([A-Za-z0-9_]+).*$/\1/p' "$absolute_file" | sort -u)
done <<<"$proto_files"

if [ -n "$sync_notes" ]; then
  sync_message="Message sync: edited schemas have missing Config/State/Event or GlobalRef/LocalRef counterparts:"$'\n'"$sync_notes"$'\n'"Add them, or name each absent member and why in the file-level doc comment as docs/conventions/protobuf.md describes; a named member is not reported again."
  context="$context${context:+$'\n\n'}$sync_message"
fi

if [ -n "$lint_failures" ]; then
  printf '%s\n' "$lint_failures${context:+$'\n\n'}$context" >&2
  exit 2
fi

if [ -n "$context" ]; then
  hook_context "$context"
fi
