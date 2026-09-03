#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR

set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=common.sh
source "$script_dir/common.sh"

hook_init || exit 0

missing_tools=""
for formatter in gofumpt goimports; do
  if ! command -v "$formatter" >/dev/null 2>&1; then
    missing_tools="$missing_tools${missing_tools:+, }$formatter"
  fi
done
if [ -n "$missing_tools" ]; then
  hook_context "$missing_tools is not on PATH; edited Go files were not formatted. Install the repository toolchain and format them before stopping."
  exit 0
fi

reformatted=""
while IFS= read -r candidate_file; do
  [ -n "$candidate_file" ] || continue
  relative_file=$(hook_relative_path "$candidate_file") || continue
  case "$relative_file" in
    *.go) ;;
    *) continue ;;
  esac
  case "$relative_file" in
    generated/*|frontend/web/generated/*) continue ;;
  esac

  absolute_file=$(hook_absolute_path "$relative_file")
  [ -f "$absolute_file" ] || continue

  before=$(shasum "$absolute_file" 2>/dev/null | cut -d' ' -f1)
  if ! format_output=$(gofumpt -w "$absolute_file" 2>&1); then
    printf 'gofumpt failed on %s:\n%s\n' "$relative_file" "$format_output" >&2
    exit 2
  fi
  if ! format_output=$(goimports -w "$absolute_file" 2>&1); then
    printf 'goimports failed on %s:\n%s\n' "$relative_file" "$format_output" >&2
    exit 2
  fi
  after=$(shasum "$absolute_file" 2>/dev/null | cut -d' ' -f1)

  if [ "$before" != "$after" ]; then
    reformatted="$reformatted${reformatted:+, }$relative_file"
  fi
done < <(hook_paths)

if [ -n "$reformatted" ]; then
  hook_context "gofumpt + goimports reformatted $reformatted. Re-read those files before editing the same regions."
fi
