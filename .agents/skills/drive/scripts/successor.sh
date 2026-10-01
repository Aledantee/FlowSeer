#!/usr/bin/env bash
# Start the next drive coordinator in this worktree after a phase lands.

set -uo pipefail

script_dir=$(cd "$(dirname "$0")" && pwd)
worker="$script_dir/../../delegate/scripts/orca-worker.sh"

registry_info() {
  local machine=${HOME:-}/.claude/models/registry.yaml
  local project="$PWD/.claude/models/registry.yaml"
  local requested_model=$1

  python3 - "$machine" "$project" "$requested_model" <<'PY'
import sys
from pathlib import Path


def remove_comment(line):
    quoted = False
    escaped = False
    for index, char in enumerate(line):
        if escaped:
            escaped = False
            continue
        if char == "\\" and quoted:
            escaped = True
            continue
        if char in "\"'":
            quoted = not quoted
        elif char == "#" and not quoted:
            return line[:index]
    return line


def split_fields(value):
    fields = []
    start = 0
    depth = 0
    quoted = False
    escaped = False
    for index, char in enumerate(value):
        if escaped:
            escaped = False
            continue
        if char == "\\" and quoted:
            escaped = True
            continue
        if char in "\"'":
            quoted = not quoted
        elif not quoted and char in "[{":
            depth += 1
        elif not quoted and char in "]}":
            depth -= 1
        elif char == "," and not quoted and depth == 0:
            fields.append(value[start:index])
            start = index + 1
    fields.append(value[start:])
    return fields


def scalar(value):
    value = value.strip()
    if value.startswith("[") and value.endswith("]"):
        return [scalar(item) for item in split_fields(value[1:-1]) if item.strip()]
    if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
        return value[1:-1]
    return value


def inline_fields(value):
    value = value.strip()
    if not (value.startswith("{") and value.endswith("}")):
        return {}
    fields = {}
    for item in split_fields(value[1:-1]):
        key, separator, raw = item.partition(":")
        if separator:
            fields[key.strip()] = scalar(raw)
    return fields


def parse(path):
    data = {"pools": {}, "models": {}, "roles": {}}
    section = None
    entry = None
    for raw_line in path.read_text().splitlines():
        line = remove_comment(raw_line).rstrip()
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip())
        text = line.strip()
        if indent == 0:
            key, separator, _ = text.partition(":")
            section = key if separator and key in data else None
            entry = None
            continue
        if section is None:
            continue
        if indent == 2:
            key, separator, raw = text.partition(":")
            if not separator:
                continue
            entry = key.strip()
            data[section][entry] = inline_fields(raw)
            continue
        if entry is not None and indent > 2:
            key, separator, raw = text.partition(":")
            if separator:
                data[section][entry][key.strip()] = scalar(raw)
    return data


paths = []
for raw_path in sys.argv[1:3]:
    path = Path(raw_path)
    if path.exists():
        if not path.is_file() or not path.stat().st_mode & 0o444:
            raise SystemExit(1)
        paths.append(path)
if not paths:
    raise SystemExit(1)

effective = {"pools": {}, "models": {}, "roles": {}}
for path in paths:
    parsed = parse(path)
    for section in effective:
        effective[section].update(parsed[section])

model = sys.argv[3]
role = effective["roles"].get("plan", {})
fit = role.get("fit", [])
if not isinstance(fit, list):
    fit = [fit]
model_fields = effective["models"].get(model, {})
pool = model_fields.get("pool", "")
pool_fields = effective["pools"].get(pool, {})
print("|".join((str(pool), str(pool_fields.get("cli", "")), str(model_fields.get("id_format", "")), " ".join(map(str, fit)))))
PY
}

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
cli=''
model=''
effort=''
effort_given=false
while (($#)); do
  case "$1" in
    --cli)
      (($# >= 2)) || refuse "--cli requires a value"
      cli=$2
      shift 2
      ;;
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

[[ -n $cli ]] || refuse "--cli is required"
[[ -n $model ]] || refuse "--model is required"
case "$cli" in
  claude|codex|agy) ;;
  *) refuse "unknown cli: $cli" ;;
esac

registry=$(registry_info "$model") || refuse "model registry is not readable"
IFS='|' read -r model_pool expected_cli id_format fit_entries <<<"$registry"
fit=false
for fit_entry in $fit_entries; do
  [[ ${fit_entry%@*} == "$model" ]] && fit=true
done
[[ $fit == true ]] || refuse "model is not in roles.plan.fit: $model"
[[ -n $model_pool && -n $expected_cli ]] || refuse "model has no pool and CLI in registry: $model"
[[ $cli == "$expected_cli" ]] || refuse "cli $cli does not match model pool cli $expected_cli: $model"

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

launch_model=$model
line_effort=$effort
if [[ -n $id_format ]]; then
  [[ $effort_given == true ]] || refuse "effort is required for model id format: $model"
  launch_model=${id_format//<effort>/$effort}
  line_effort=''
fi

if [[ $effort_given == true && -n $line_effort ]]; then
  launch=$("$worker" line --cli "$cli" --model "$launch_model" --effort "$line_effort" 2>/dev/null) ||
    refuse "cannot build the coordinator launch line"
else
  launch=$("$worker" line --cli "$cli" --model "$launch_model" 2>/dev/null) ||
    refuse "cannot build the coordinator launch line"
fi
prompt="Read .claude/skills/drive/SKILL.md and drive $parent."
case "$cli" in
  # Claude and Codex document the initial prompt as a positional argument.
  claude|codex) command="$launch \"$prompt\"" ;;
  # agy has no positional prompt and documents --prompt-interactive for it.
  agy) command="$launch --prompt-interactive \"$prompt\"" ;;
esac

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
  if grep -q -i -E 'esc( to)? (interrupt|cancel)' <<<"$screen"; then
    echo "$handle"
    exit 0
  fi
  sleep 3
done

if orca terminal close --terminal "$handle" --json >/dev/null 2>&1; then
  echo "successor refused: coordinator did not show the working hint" >&2
  exit 1
fi
may_be_running "$handle"
