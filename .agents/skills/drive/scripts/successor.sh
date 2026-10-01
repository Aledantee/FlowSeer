#!/usr/bin/env bash
# Start the next Claude coordinator in this worktree after a phase lands.

set -uo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
worker="$script_dir/../../delegate/scripts/orca-worker.sh"

refuse() {
  echo "successor: $*" >&2
  exit 1
}

may_be_running() {
  if (($#)); then
    echo "successor may be running: $1" >&2
  else
    echo "successor may be running" >&2
  fi
  exit 2
}

if (($# == 0)); then
  refuse "parent is required"
fi
parent=$1
shift
model=''
effort=''
effort_given=false
while (($#)); do
  case "$1" in
    --model)
      (($# >= 2)) || refuse "--model requires a value"
      model=$2
      shift 2
      ;;
    --effort)
      (($# >= 2)) || refuse "--effort requires a value"
      effort=$2
      effort_given=true
      shift 2
      ;;
    *)
      refuse "unknown argument: $1"
      ;;
  esac
done

[[ $model == claude-* ]] || refuse "model must start with claude-: $model"
if [[ $effort_given == true ]] && [[ ! $effort =~ ^[a-z]+$ ]]; then
  refuse "effort must contain lowercase letters: $effort"
fi
if [[ ! $parent =~ ^docs/plans/[^/]+-plan\.md$ ]] || [[ ! -f $parent ]]; then
  refuse "parent must be an existing docs/plans/<name>-plan.md: $parent"
fi

dirty=$(git status --porcelain 2>&1) || refuse "cannot inspect the working tree: $dirty"
if [[ -n $dirty ]]; then
  refuse "working tree is dirty: $dirty"
fi

reachable=$(orca status --json 2>/dev/null | python3 -c '
import json
import sys

try:
    data = json.load(sys.stdin)
    print(data["result"]["runtime"]["reachable"])
except (KeyError, TypeError, json.JSONDecodeError):
    raise SystemExit(1)
') || refuse "Orca runtime is not reachable"
[[ $reachable == True ]] || refuse "Orca runtime is not reachable"

children=$(orca worktree show --worktree active --json 2>/dev/null | python3 -c '
import json
import sys

try:
    worktree = json.load(sys.stdin)["result"]["worktree"]
    children = worktree["childWorktreeIds"]
    if not isinstance(children, list):
        raise KeyError("childWorktreeIds")
except (KeyError, TypeError, json.JSONDecodeError):
    raise SystemExit(1)
print("\n".join(str(child) for child in children))
') || refuse "child query returned no childWorktreeIds"
if [[ -n $children ]]; then
  refuse "child worktrees remain: $children"
fi

if [[ $effort_given == true ]]; then
  launch=$("$worker" line --cli claude --model "$model" --effort "$effort" 2>/dev/null) ||
    refuse "cannot build the Claude launch line"
else
  launch=$("$worker" line --cli claude --model "$model" 2>/dev/null) ||
    refuse "cannot build the Claude launch line"
fi
prompt="Read .claude/skills/drive/SKILL.md and drive $parent."
command="$launch \"$prompt\""

created=$(orca terminal create --worktree active --title drive --command "$command" --json 2>/dev/null) ||
  may_be_running
handle=$(printf '%s' "$created" | python3 -c '
import json
import sys

try:
    print(json.load(sys.stdin)["result"]["terminal"]["handle"])
except (KeyError, TypeError, json.JSONDecodeError):
    raise SystemExit(1)
' 2>/dev/null) || may_be_running
[[ -n $handle ]] || may_be_running

for _ in {1..20}; do
  screen=$(orca terminal read --terminal "$handle" --screen 2>/dev/null || true)
  if grep -q -i -E 'esc( to)? interrupt' <<<"$screen"; then
    echo "$handle"
    exit 0
  fi
  sleep 3
done

if orca terminal close --terminal "$handle" --json >/dev/null 2>&1; then
  echo "successor refused: Claude did not show the working hint" >&2
  exit 1
fi
may_be_running "$handle"
