#!/usr/bin/env bash
# PostToolUse on Bash: report a new commit whose message holds a literal
# `\n\n`.
#
# `git commit -m "subject\n\nbody"` stores the backslashes, and the log then
# shows one long line where paragraphs were meant. The message is read off
# the commit rather than guessed from the command text, which cannot tell a
# quoted "\n" from $'\n'. The hook reports and does not block: the commit
# already exists, and an amend from standard input repairs it.
# shellcheck source-path=SCRIPTDIR

set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=common.sh
source "$script_dir/common.sh"

input=$(hook_read_input)
jq -e . >/dev/null 2>&1 <<<"$input" || exit 0
cwd=$(jq -r '.cwd // ""' <<<"$input")
[[ -n $cwd ]] || exit 0

head=$(git -C "$cwd" rev-parse --verify -q HEAD 2>/dev/null) || exit 0
git_dir=$(git -C "$cwd" rev-parse --absolute-git-dir 2>/dev/null) || exit 0
state=$git_dir/flowseer-commit-message-checked
seen=$(cat "$state" 2>/dev/null || true)
[[ $head != "$seen" ]] || exit 0
printf '%s\n' "$head" >"$state" 2>/dev/null || exit 0
# The first call in a worktree records where the session started: that
# commit is not this session's to rewrite.
[[ -n $seen ]] || exit 0

git -C "$cwd" log -1 --format=%B "$head" | grep -qF '\n\n' || exit 0
short=$(git -C "$cwd" rev-parse --short "$head")
jq -n --arg message "The message of commit $short holds a literal \\n\\n where a paragraph break was meant: git commit -m does not expand escapes. If this session wrote it, rewrite it with the message on standard input (git commit --amend -F -) or with one -m per paragraph." \
  '{hookSpecificOutput:{hookEventName:"PostToolUse",additionalContext:$message}}'
