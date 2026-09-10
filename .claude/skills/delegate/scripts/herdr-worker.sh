#!/usr/bin/env bash
# Drive one supervised worker through Herdr: a git worktree, an agent pane
# on the chosen CLI and model, the brief, and the settled-state wait.
#
# herdr-worker.sh start --lane SLUG --cli claude|codex|agy|opencode --model ID
#                       [--effort LEVEL] [--agent OPENCODE_AGENT] --brief FILE
#                       [--base REF] [--repo PATH] [--root PATH]
# herdr-worker.sh wait  NAME [--timeout MS]
# herdr-worker.sh read  NAME [--lines N]
# herdr-worker.sh keys  NAME KEY...
# herdr-worker.sh stop  NAME
#
# `start` prints one JSON line: {"name","pane","workspace","worktree","branch"}.
# The agent name is the lane slug. Run unsandboxed: Herdr is a local socket.
# Herdr must have been started from a plain terminal, not from inside a
# Claude Code session, or every worker inherits the child-session marker.

set -uo pipefail

die() { echo "herdr-worker: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "$1 not on PATH"; }
need herdr; need python3; need git

json() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {"d": d}))' "$1"; }

cmd=${1:-}; shift || true
case "$cmd" in
  start)
    lane= cli= model= effort= agent= brief= base=HEAD repo= root=
    while (($#)); do
      case "$1" in
        --lane) lane=$2; shift 2 ;;
        --cli) cli=$2; shift 2 ;;
        --model) model=$2; shift 2 ;;
        --effort) effort=$2; shift 2 ;;
        --agent) agent=$2; shift 2 ;;
        --brief) brief=$2; shift 2 ;;
        --base) base=$2; shift 2 ;;
        --repo) repo=$2; shift 2 ;;
        --root) root=$2; shift 2 ;;
        *) die "unknown flag $1" ;;
      esac
    done
    for v in lane cli model brief; do [[ -n "${!v}" ]] || die "--$v is required"; done
    [[ "$lane" =~ ^[a-z][a-z0-9_-]{0,31}$ ]] || die "lane must match [a-z][a-z0-9_-]{0,31}"
    [[ -f "$brief" ]] || die "brief $brief not found"
    herdr status server 2>/dev/null | grep -q 'status: running' || die "no Herdr server; start one from a plain terminal with: herdr server"

    # Herdr refuses to branch a worktree from a linked worktree, so resolve
    # the base to a commit here and create from the repository's main checkout.
    sha=$(git rev-parse --verify "$base^{commit}") || die "base $base does not resolve"
    repo=${repo:-$(git rev-parse --path-format=absolute --git-common-dir | sed 's#/\.git$##')}
    root=${root:-$(dirname "$repo")/worktrees/$(basename "$repo")}
    mkdir -p "$root"
    path=$root/$lane
    created=$(herdr worktree create --cwd "$repo" --branch "$lane" --base "$sha" --path "$path" --label "$lane" --no-focus) \
      || die "worktree create failed: $created"
    pane=$(printf '%s' "$created" | json 'd["result"]["root_pane"]["pane_id"]')
    ws=$(printf '%s' "$created" | json 'd["result"]["workspace"]["workspace_id"]')

    # The brief lives beside the worktree, outside the tree the worker
    # commits from, so a clean `git status` still means a clean unit.
    mkdir -p "$root/briefs"; cp "$brief" "$root/briefs/$lane.md"

    case "$cli" in
      claude) args=(--model "$model" --dangerously-skip-permissions); [[ -n $effort ]] && args+=(--effort "$effort") ;;
      codex)  args=(-a never --sandbox danger-full-access -m "$model"); [[ -n $effort ]] && args+=(-c "model_reasoning_effort=$effort") ;;
      agy)    args=(--model "$model" --dangerously-skip-permissions) ;;   # effort is part of the model id
      opencode) args=(); [[ -n $agent ]] && args+=(--agent "$agent"); [[ -n $model ]] && args+=(--model "$model") ;;
      *) die "unknown cli $cli" ;;
    esac
    started=$(herdr agent start "$lane" --kind "$cli" --pane "$pane" --timeout 90000 -- "${args[@]}") \
      || die "agent start failed: $started"

    # Codex opens a hooks-review dialog on a repository with .codex/hooks.json
    # and Herdr reports it idle, not blocked; trust and close it first.
    if [[ $cli == codex ]]; then
      sleep 3
      if herdr agent read "$lane" --source visible --lines 40 2>/dev/null | grep -q 'hook needs review'; then
        herdr agent send-keys "$lane" t >/dev/null; sleep 1
        herdr agent send-keys "$lane" esc >/dev/null; sleep 1
      fi
    fi

    prompt="Read the brief at $root/briefs/$lane.md and carry it out exactly. Reply only with what the brief asks for."
    # agy sometimes drops the first submission; a stalled prompt is retried once.
    for attempt in 1 2; do
      out=$(herdr agent prompt "$lane" "$prompt" --wait --timeout 15000 2>&1)
      code=$(printf '%s' "$out" | json 'd.get("error",{}).get("code","")' 2>/dev/null || echo parse)
      [[ $code == agent_prompt_stalled && $attempt == 1 ]] || break
      sleep 2
    done
    printf '{"name":"%s","pane":"%s","workspace":"%s","worktree":"%s","branch":"%s","prompt":"%s"}\n' \
      "$lane" "$pane" "$ws" "$path" "$lane" "${code:-submitted}"
    ;;
  wait)
    name=${1:-}; shift || true; timeout=3600000
    [[ $# -ge 2 && $1 == --timeout ]] && timeout=$2
    [[ -n $name ]] || die "wait NAME"
    out=$(herdr agent wait "$name" --until done --until idle --until blocked --timeout "$timeout" 2>&1)
    status=$(printf '%s' "$out" | json 'd.get("result",{}).get("agent",{}).get("agent_status") or d.get("error",{}).get("code")')
    echo "$status"
    if [[ $status == blocked ]]; then herdr agent read "$name" --source visible --lines 40; fi
    ;;
  read)
    name=${1:-}; shift || true; lines=120
    [[ $# -ge 2 && $1 == --lines ]] && lines=$2
    herdr agent read "$name" --source recent-unwrapped --lines "$lines"
    ;;
  keys)
    name=${1:-}; shift || true
    herdr agent send-keys "$name" "$@"
    ;;
  stop)
    name=${1:-}; [[ -n $name ]] || die "stop NAME"
    ws=$(herdr agent get "$name" | json 'd["result"]["agent"]["workspace_id"]') || die "no live agent $name"
    herdr agent send-keys "$name" ctrl+c >/dev/null 2>&1; sleep 1
    herdr agent send-keys "$name" ctrl+d >/dev/null 2>&1; sleep 1
    herdr worktree remove --workspace "$ws"
    ;;
  *) sed -n '2,16p' "$0" >&2; exit 2 ;;
esac
