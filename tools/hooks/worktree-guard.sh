#!/usr/bin/env bash
#
# worktree-guard.sh — force each Claude Code session to write inside its own
# git worktree instead of the primary checkout's protected branch.
#
# Wired to two events in .claude/settings.json and .codex/hooks.json:
#   SessionStart  -> injects context telling Claude to call EnterWorktree first.
#   PreToolUse    -> denies file-writing tools while the session sits in the
#                    primary checkout on a protected branch. This is the actual
#                    enforcement; SessionStart cannot block anything.
#
# What stays allowed in the primary checkout: `git add`, `git commit`, and
# `git merge`. Direct file writes are blocked, so a commit there can only
# capture content the session did not write itself (user-copied files,
# generated output), and a merge can only land branches produced in a
# worktree. That is the intended landing flow — no user intervention needed.
#
# Also blocks writes that escape a linked worktree back into the parent checkout.
#
# Opt out:
#   export CLAUDE_WORKTREE_GUARD=off          # whole machine / one shell
#   touch <repo>/.claude/no-worktree-guard    # one repository
# Tune:
#   CLAUDE_WORKTREE_GUARD_BRANCHES="main master develop trunk"   (default)

set -uo pipefail

[ "${CLAUDE_WORKTREE_GUARD:-}" = "off" ] && exit 0

input=$(cat)
event=$(jq -r '.hook_event_name // ""' <<<"$input")
cwd=$(jq -r '.cwd // ""' <<<"$input")

[ -n "$cwd" ] && [ -d "$cwd" ] || exit 0
cd "$cwd" || exit 0

toplevel=$(git rev-parse --show-toplevel 2>/dev/null) || exit 0
# Compare physical paths throughout: git reports the toplevel through
# resolved symlinks (/private/tmp on macOS) while the git dirs may come back
# logically (/tmp), and a mismatch would misread the primary checkout as a
# linked worktree.
toplevel=$(cd "$toplevel" 2>/dev/null && pwd -P) || exit 0
cwd=$(pwd -P)

# Physical form of a file path that may not exist yet: the deepest existing
# ancestor is resolved through symlinks and the rest is appended verbatim.
physical_path() {
  local candidate=$1 suffix=""
  while [ ! -d "$candidate" ]; do
    suffix="/$(basename "$candidate")$suffix"
    case "$candidate" in
      */*) candidate=$(dirname "$candidate") ;;
      *) printf '%s\n' "$1"; return ;;
    esac
  done
  candidate=$(cd "$candidate" 2>/dev/null && pwd -P) || { printf '%s\n' "$1"; return; }
  printf '%s%s\n' "$candidate" "$suffix"
}
[ -e "$toplevel/.claude/no-worktree-guard" ] && exit 0

git_dir=$(git rev-parse --absolute-git-dir 2>/dev/null) || exit 0
git_dir=$(cd "$git_dir" 2>/dev/null && pwd -P) || exit 0
common_rel=$(git rev-parse --git-common-dir 2>/dev/null) || exit 0
common_dir=$(cd "$common_rel" 2>/dev/null && pwd -P) || common_dir="$git_dir"

# In a linked worktree the per-worktree git dir lives under <common>/worktrees/<name>.
in_linked_worktree=0
[ "$git_dir" != "$common_dir" ] && in_linked_worktree=1

branch=$(git symbolic-ref --quiet --short HEAD 2>/dev/null || echo "")

deny() {
  jq -n --arg r "$1" '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'
  exit 0
}

# ---------------------------------------------------------------- linked worktree
# Passive, except: never let an edit leak back into the parent checkout.
if [ "$in_linked_worktree" = 1 ]; then
  [ "$event" = "PreToolUse" ] || exit 0
  tool=$(jq -r '.tool_name // ""' <<<"$input")
  case "$tool" in
    Edit|Write|NotebookEdit) ;;
    *) exit 0 ;;
  esac
  target=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // ""' <<<"$input")
  [ -n "$target" ] || exit 0
  case "$target" in
    /*) abs="$target" ;;
    *)  abs="$cwd/$target" ;;
  esac
  abs=$(physical_path "$abs")
  parent=$(cd "$common_dir/.." 2>/dev/null && pwd -P) || exit 0
  # The worktree's own git dir sits under <parent>/.git/worktrees/<name>,
  # inside the parent by path and private to this worktree by ownership:
  # implement keeps its plan ledger there and the verifier its receipt.
  case "$abs" in
    "$toplevel"/*|"$git_dir"/*) exit 0 ;;
    "$parent"/*)
      deny "This session is isolated in the worktree $toplevel, but the edit target is $abs — inside the parent checkout $parent. Write to the corresponding path under $toplevel instead; changes reach the parent through a commit and merge, never through a direct edit." ;;
  esac
  exit 0
fi

# ------------------------------------------------------------- primary checkout
protected="${CLAUDE_WORKTREE_GUARD_BRANCHES:-main master develop trunk}"
is_protected=0
for b in $protected; do
  [ "$branch" = "$b" ] && is_protected=1
done
# A detached HEAD in the primary checkout is just as unsafe to share.
[ -z "$branch" ] && is_protected=1
[ "$is_protected" = 1 ] || exit 0

where="branch '$branch'"
[ -z "$branch" ] && where="a detached HEAD"

if [ "$event" = "SessionStart" ]; then
  jq -n --arg ctx "This session started in the primary checkout $toplevel on $where, which is protected. Before any file edit or other file-writing command, call the EnterWorktree tool with a short descriptive name for the task — this project requires each session to write in its own git worktree so concurrent Claude sessions cannot clobber each other; a hook denies file-writing tools here until you do. Allowed directly in this checkout: read-only exploration, git add / git commit (to record files that already exist here, e.g. user-copied or generated ones), and git merge of a finished worktree branch. If a merge conflicts, run git merge --abort and resolve by rebuilding the worktree branch instead — conflict edits cannot be made here." \
    '{hookSpecificOutput:{hookEventName:"SessionStart",additionalContext:$ctx}}'
  exit 0
fi

[ "$event" = "PreToolUse" ] || exit 0
tool=$(jq -r '.tool_name // ""' <<<"$input")

nudge="Call the EnterWorktree tool with a short descriptive name (e.g. EnterWorktree name:\"fix-snmp-timeout\") to get an isolated worktree, then retry. Read-only commands, git add, git commit, and git merge of a worktree branch still work here."

case "$tool" in
  Edit|Write|NotebookEdit)
    target=$(jq -r '.tool_input.file_path // .tool_input.notebook_path // ""' <<<"$input")
    case "$target" in
      /*) tabs="$target" ;;
      *)  tabs="$cwd/$target" ;;
    esac
    tabs=$(physical_path "$tabs")
    # Only the repository needs protecting. Editing dotfiles, this hook, or
    # anything else outside the checkout cannot clobber a concurrent session.
    case "$tabs" in
      "$toplevel"/*) ;;
      *) exit 0 ;;
    esac
    deny "Refusing to modify $target: this session is in the primary checkout $toplevel on $where. $nudge" ;;
  Bash)
    cmd=$(jq -r '.tool_input.command // ""' <<<"$input")

    # Drop redirections that do not write files, so plain `>` can imply a
    # write. Whitespace-free <token> pairs go too: they are email addresses
    # in commit trailers or <placeholder> text, never a redirect.
    scrub=$(printf '%s' "$cmd" \
      | sed -e 's/2>&1//g' \
            -e 's/2>[^[:space:]]*//g' \
            -e 's/>&2//g' \
            -e 's/[0-9]*>[[:space:]]*\/dev\/null//g' \
            -e 's/<[^<>[:space:]]*>//g' \
            -e 's/->//g' -e 's/=>//g' -e 's/>=//g')
    reason=""
    case "$scrub" in
      *">"*) reason="it redirects output into a file" ;;
    esac
    if [ -z "$reason" ]; then
      if printf '%s' "$cmd" | grep -Eq '(^|[;&|(]|[[:space:]])(rm|mv|cp|mkdir|rmdir|touch|chmod|chown|ln|patch|tee|dd|truncate|install)([[:space:]]|$)'; then
        reason="it modifies files on disk"
      elif printf '%s' "$cmd" | grep -Eq '(^|[;&|(]|[[:space:]])(sed|perl|ruby)[[:space:]]+[^|;]*-i'; then
        reason="it edits files in place"
      # add/commit/merge are deliberately absent: with file writes blocked,
      # staging, committing, and merging worktree branches are the safe
      # landing operations this checkout exists for.
      elif printf '%s' "$cmd" | grep -Eq '(^|[;&|(]|[[:space:]])git[[:space:]]+(rebase|cherry-pick|revert|apply|am|push|reset|checkout|switch|restore|stash|clean|rm|mv)([[:space:]]|$)'; then
        reason="it rewrites the working tree or history"
      elif printf '%s' "$cmd" | grep -Eq '(^|[;&|(]|[[:space:]])(go[[:space:]]+(mod|generate|get)|buf[[:space:]]+generate|(npm|pnpm|yarn|bun)[[:space:]]+(install|add|remove))([[:space:]]|$)'; then
        reason="it regenerates or rewrites tracked files"
      fi
    fi
    [ -n "$reason" ] || exit 0

    # Deleting untracked files cannot clobber a concurrent session: the guard
    # protects shared tracked state, and untracked strays exist only in this
    # checkout. Allow a plain `rm` (no shell metacharacters) whose every
    # operand resolves inside the checkout to a path git does not track.
    if [ "$reason" = "it modifies files on disk" ] \
      && printf '%s' "$cmd" | grep -Eq '^[[:space:]]*rm([[:space:]]|$)' \
      && ! printf '%s' "$cmd" | grep -q "[;&|<>\`\$()]"; then
      all_untracked=1
      first=1
      for tok in $cmd; do
        if [ "$first" = 1 ]; then first=0; continue; fi
        tok=${tok#[\"\']}
        tok=${tok%[\"\']}
        case "$tok" in
          -*) continue ;;
          /*) p="$tok" ;;
          *)  p="$cwd/$tok" ;;
        esac
        p=$(physical_path "$p")
        case "$p" in
          "$toplevel"/*) ;;
          *) all_untracked=0; break ;;
        esac
        # For a directory this lists tracked files beneath it, so a tracked
        # descendant blocks a recursive delete too.
        if [ -n "$(git ls-files -- "$p" 2>/dev/null)" ]; then
          all_untracked=0
          break
        fi
      done
      [ "$all_untracked" = 1 ] && exit 0
    fi

    # The checkout is the only thing this guard protects. If the command
    # names at least one absolute (or ~-prefixed) path and every such path
    # lies outside the checkout, the write cannot clobber a concurrent
    # session here (e.g. `sed -i ... ~/.serena/serena_config.yml`).
    # Commands that use only relative paths keep the deny: those resolve
    # into the checkout. Best-effort token scan, not a parser — a command
    # mixing outside-absolute and bare relative operands can slip through.
    saw_path=0
    outside_only=1
    for tok in $cmd; do
      tok=${tok#[\"\']}
      tok=${tok%[\"\']}
      tok=${tok#>}
      case "$tok" in
        /*) p="$tok" ;;
        "~") p="$HOME" ;;
        \~/*) p="$HOME/${tok#\~/}" ;;
        *) continue ;;
      esac
      p=$(physical_path "$p")
      saw_path=1
      case "$p" in
        "$toplevel"|"$toplevel"/*) outside_only=0 ;;
      esac
    done
    if [ "$saw_path" = 1 ] && [ "$outside_only" = 1 ]; then
      exit 0
    fi

    deny "Refusing to run this command because $reason, and the session is in the primary checkout $toplevel on $where. $nudge" ;;
esac

exit 0
