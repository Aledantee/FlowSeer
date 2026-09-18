#!/usr/bin/env bash
# Print one "<hash><TAB><path>" line per dirty or untracked source/config path
# of the worktree at $1, sorted. The Bash branch of mark-verification-dirty.sh
# compares this listing with the stored one to learn what a command changed,
# and the verifier rewrites the stored one after a pass. A deleted path hashes
# as "deleted". Paths of a file type no gate verifies are left out, by the
# same list the edit branch of the marker hook uses.

set -uo pipefail

# comm in the marker hook needs the byte order sort produces here.
export LC_ALL=C

root=${1:-}
[[ -n $root ]] || exit 2
cd "$root" 2>/dev/null || exit 2

present=()
deleted=()
while IFS= read -r -d '' entry; do
  rel=${entry:3}
  case "$rel" in
    *.go|*.proto|*.ts|*.tsx|*.js|*.jsx|*.py|*.sh|*.md|*.json|*.yaml|*.yml) ;;
    go.mod|go.sum|buf.lock|*/go.mod|*/go.sum|*/buf.lock) ;;
    *) continue ;;
  esac
  if [[ -f $rel ]]; then
    present+=("$rel")
  else
    deleted+=("$rel")
  fi
done < <(git status --porcelain=v1 -z --no-renames --untracked-files=all 2>/dev/null)

listing() {
  if [[ ${#deleted[@]} -gt 0 ]]; then
    printf 'deleted\t%s\n' "${deleted[@]}"
  fi
  if [[ ${#present[@]} -gt 0 ]]; then
    # One hash-object for the whole list; it answers in input order.
    paste <(printf '%s\n' "${present[@]}" | git hash-object --stdin-paths) \
      <(printf '%s\n' "${present[@]}")
  fi
}

# During a merge every incoming file is a staged change. One whose content
# is the other side's blob is that branch's verified work, not an edit made
# here; only a path the merge or a person changed beyond it stays listed.
if git rev-parse -q --verify MERGE_HEAD >/dev/null 2>&1; then
  comm -23 <(listing | sort) \
    <(git ls-tree -r MERGE_HEAD | awk -F'[ \t]' '{print $3 "\t" substr($0, index($0, "\t") + 1)}' | sort)
else
  listing | sort
fi
