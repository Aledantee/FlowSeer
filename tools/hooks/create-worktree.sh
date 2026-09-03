#!/usr/bin/env bash

set -euo pipefail

hook_input=$(cat)
if ! jq -e '.cwd | type == "string" and length > 0' >/dev/null 2>&1 <<<"$hook_input" ||
  ! jq -e '.name | type == "string" and length > 0' >/dev/null 2>&1 <<<"$hook_input"; then
  echo "WorktreeCreate requires string cwd and name fields." >&2
  exit 1
fi

cwd=$(jq -r '.cwd' <<<"$hook_input")
name=$(jq -r '.name' <<<"$hook_input")
if [[ ! $name =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ || ${#name} -gt 128 ]]; then
  echo "WorktreeCreate received an unsafe worktree name." >&2
  exit 1
fi

repo_root=$(git -C "$cwd" rev-parse --show-toplevel)
repo_root=$(cd "$repo_root" && pwd -P)
common_dir=$(git -C "$cwd" rev-parse --path-format=absolute --git-common-dir)
common_dir=$(cd "$common_dir" && pwd -P)
branch_name="worktree-$name"
if ! git -C "$cwd" check-ref-format --branch "$branch_name" >/dev/null; then
  echo "WorktreeCreate received a name that cannot form a Git branch." >&2
  exit 1
fi

if [[ -n ${FLOWSEER_WORKTREE_ROOT:-} ]]; then
  worktree_root=$FLOWSEER_WORKTREE_ROOT
else
  primary_root=$(dirname "$common_dir")
  worktree_root="$(dirname "$primary_root")/worktrees/$(basename "$primary_root")"
fi

case "$worktree_root" in
  /*) ;;
  *)
    echo "WorktreeCreate requires an absolute worktree root." >&2
    exit 1
    ;;
esac

is_within() {
  local candidate=$1
  local parent=$2

  [[ $candidate == "$parent" || $candidate == "$parent"/* ]]
}

canonical_path() {
  local candidate=$1
  local parent
  local suffix=

  while [[ ! -e $candidate ]]; do
    suffix="/$(basename "$candidate")$suffix"
    parent=$(dirname "$candidate")
    [[ $parent != "$candidate" ]] || return 1
    candidate=$parent
  done

  candidate=$(cd "$candidate" && pwd -P) || return 1
  printf '%s%s\n' "$candidate" "$suffix"
}

target=$(canonical_path "$worktree_root/$name") || {
  echo "WorktreeCreate could not resolve the worktree target." >&2
  exit 1
}
worktree_root=$(dirname "$target")

if is_within "$target" "$repo_root" || is_within "$target" "$common_dir"; then
  echo "WorktreeCreate refuses to place a worktree inside the repository or Git data directory." >&2
  exit 1
fi

existing_worktrees=()
while IFS= read -r -d '' record; do
  case "$record" in
    "worktree "*)
      worktree_path=${record#worktree }
      worktree_path=$(cd "$worktree_path" && pwd -P)
      existing_worktrees+=("$worktree_path")
      if is_within "$target" "$worktree_path"; then
        echo "WorktreeCreate refuses to place a worktree inside an existing checkout." >&2
        exit 1
      fi
      ;;
  esac
done < <(git -C "$cwd" worktree list --porcelain -z)

mkdir -p "$worktree_root"
target=$(canonical_path "$target") || {
  echo "WorktreeCreate could not resolve the worktree target." >&2
  exit 1
}

if is_within "$target" "$repo_root" || is_within "$target" "$common_dir"; then
  echo "WorktreeCreate refuses to place a worktree inside the repository or Git data directory." >&2
  exit 1
fi

for worktree_path in "${existing_worktrees[@]}"; do
  if is_within "$target" "$worktree_path"; then
    echo "WorktreeCreate refuses to place a worktree inside an existing checkout." >&2
    exit 1
  fi
done

git -C "$cwd" worktree add -b "$branch_name" "$target" HEAD >&2
printf '%s\n' "$target"
