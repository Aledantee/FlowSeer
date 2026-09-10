#!/usr/bin/env bash
# Drive one supervised worker through Herdr: a git worktree, an agent pane
# on the chosen CLI and model, the brief, and the settled-state wait.
#
# herdr-worker.sh start --lane SLUG --cli claude|codex|agy --model ID [--effort LEVEL] --brief FILE [--base REF]
# herdr-worker.sh start --lane SLUG --cli opencode --agent NAME --brief FILE [--base REF]
# herdr-worker.sh wait   SLUG [--timeout MS]      # prints done|idle|blocked|timeout; the pane follows blocked
# herdr-worker.sh read   SLUG [--lines N]
# herdr-worker.sh keys   SLUG KEY...
# herdr-worker.sh status
# herdr-worker.sh stop   SLUG
#
# `start` prints one JSON line {"name","pane","workspace","worktree","branch"}
# and exits 0 only when the brief was submitted and the agent is at work; any
# failure after the worktree exists removes it again. The agent name is the
# lane slug. Herdr reports CLI errors as JSON on stderr with exit 1. Run
# unsandboxed: Herdr is a local socket.

set -uo pipefail

die() { echo "herdr-worker: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "$1 not on PATH"; }
need herdr; need python3; need git

json() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {"d": d}))' "$1" 2>/dev/null; }
agent_status() { herdr agent get "$1" 2>/dev/null | json 'd["result"]["agent"]["agent_status"]'; }
alive() { herdr agent list >/dev/null 2>&1; }

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
    for v in lane cli brief; do [[ -n "${!v}" ]] || die "--$v is required"; done
    [[ "$lane" =~ ^[a-z][a-z0-9_-]{0,31}$ ]] || die "lane must match [a-z][a-z0-9_-]{0,31}"
    [[ -f "$brief" ]] || die "brief $brief not found"
    case "$cli" in
      claude|codex) [[ -n $model ]] || die "--model is required for $cli"; [[ -z $agent ]] || die "--agent does not apply to $cli" ;;
      agy) [[ -n $model ]] || die "--model is required for agy"; [[ -z $effort ]] || die "--effort does not apply to agy: it is part of the model id"; [[ -z $agent ]] || die "--agent does not apply to agy" ;;
      opencode) [[ -n $agent ]] || die "--agent is required for opencode: the agent fixes the model"; [[ -z $model$effort ]] || die "--model and --effort do not apply to opencode" ;;
      *) die "unknown cli $cli" ;;
    esac
    # `herdr status` is not a reliable liveness probe right after another
    # CLI call; an API call is.
    for _ in 1 2 3; do alive && break; sleep 1; done
    alive || die "no Herdr server; start one from a plain terminal with: herdr server"
    herdr agent get "$lane" >/dev/null 2>&1 && die "a live agent named $lane exists; stop it or pick another slug"

    # Herdr refuses to branch a worktree from a linked worktree, so resolve
    # the base to a commit here and create from the repository's main checkout.
    sha=$(git rev-parse --verify "$base^{commit}") || die "base $base does not resolve"
    repo=${repo:-$(git rev-parse --path-format=absolute --git-common-dir | sed 's#/\.git$##')}
    root=${root:-$(dirname "$repo")/worktrees/$(basename "$repo")}
    mkdir -p "$root"
    path=$root/$lane
    created=$(herdr worktree create --cwd "$repo" --branch "$lane" --base "$sha" --path "$path" --label "$lane" --no-focus 2>&1) \
      || die "worktree create failed: $created"
    pane=$(printf '%s' "$created" | json 'd["result"]["root_pane"]["pane_id"]')
    ws=$(printf '%s' "$created" | json 'd["result"]["workspace"]["workspace_id"]')
    [[ -n $pane && -n $ws ]] || die "worktree create returned no pane or workspace id: $created"
    # Every failure from here on removes what was created.
    undo() { herdr worktree remove --workspace "$ws" --force >/dev/null 2>&1; git branch -D "$lane" >/dev/null 2>&1; die "$*"; }

    case "$cli" in
      claude) args=(--model "$model" --dangerously-skip-permissions); [[ -n $effort ]] && args+=(--effort "$effort") ;;
      codex)  args=(-a never --sandbox danger-full-access -m "$model"); [[ -n $effort ]] && args+=(-c "model_reasoning_effort=$effort") ;;
      agy)    args=(--model "$model" --dangerously-skip-permissions) ;;
      opencode) args=(--agent "$agent") ;;
    esac
    # A start that returns agent_not_ready has the agent up but blocked on a
    # startup dialog; the name is live for read and send-keys.
    started=$(herdr agent start "$lane" --kind "$cli" --pane "$pane" --timeout 90000 -- "${args[@]}" 2>&1)
    startcode=$(printf '%s' "$started" | json 'd.get("error",{}).get("code","")')
    [[ -z $startcode || $startcode == agent_not_ready ]] || undo "agent start failed: $started"

    # Codex startup dialogs: its update offer (Herdr reports blocked) and the
    # hooks review for a repository with .codex/hooks.json (Herdr reports
    # idle, and a prompt sent into it is lost). Both are answered here;
    # anything else on screen stops the start.
    if [[ $cli == codex ]]; then
      for _ in 1 2 3 4; do
        sleep 2
        screen=$(herdr agent read "$lane" --source visible --lines 40 2>/dev/null)
        if grep -q 'Update available' <<<"$screen"; then
          herdr agent send-keys "$lane" 2 >/dev/null; sleep 0.5; herdr agent send-keys "$lane" enter >/dev/null
        elif grep -q 'hook needs review' <<<"$screen"; then
          herdr agent send-keys "$lane" t >/dev/null; sleep 1; herdr agent send-keys "$lane" esc >/dev/null
        else
          break
        fi
      done
      screen=$(herdr agent read "$lane" --source visible --lines 40 2>/dev/null)
      grep -q -E 'Update available|hook needs review' <<<"$screen" && undo "$lane still shows a startup dialog after four rounds"
    fi
    [[ $(agent_status "$lane") == blocked ]] && undo "$lane is blocked on a startup dialog this script does not know; read it with: herdr agent read $lane --source visible"

    # The brief is the prompt: one bracketed-paste submission, nothing for
    # the worker to fetch. agy drops a first submission now and then, so a
    # stalled prompt is sent once more.
    prompt=$(cat "$brief")
    code=
    for attempt in 1 2; do
      out=$(herdr agent prompt "$lane" "$prompt" --wait --timeout 20000 2>&1)
      code=$(printf '%s' "$out" | json 'd.get("error",{}).get("code") or d["result"]["agent"]["agent_status"]')
      [[ $code == agent_prompt_stalled && $attempt == 1 ]] && { sleep 2; continue; }
      break
    done
    case "$code" in
      working|timeout|done|idle|blocked) ;;   # timeout: the settled-state wait ran out, the agent is at work
      *) undo "prompt not accepted by $lane: ${code:-no response}; $out" ;;
    esac
    printf '{"name":"%s","pane":"%s","workspace":"%s","worktree":"%s","branch":"%s"}\n' \
      "$lane" "$pane" "$ws" "$path" "$lane"
    ;;
  wait)
    name=${1:-}; shift || true; timeout=
    [[ $# -ge 2 && $1 == --timeout ]] && timeout=$2
    [[ -n $name ]] || die "wait SLUG"
    out=$(herdr agent wait "$name" --until done --until idle --until blocked ${timeout:+--timeout "$timeout"} 2>&1)
    status=$(printf '%s' "$out" | json 'd.get("result",{}).get("agent",{}).get("agent_status") or d.get("error",{}).get("code")')
    [[ -n $status ]] || die "wait returned nothing readable: $out"
    echo "$status"
    if [[ $status == blocked ]]; then herdr agent read "$name" --source visible --lines 40; fi
    exit 0
    ;;
  read)
    name=${1:-}; shift || true; lines=120
    [[ $# -ge 2 && $1 == --lines ]] && lines=$2
    [[ -n $name ]] || die "read SLUG"
    herdr agent read "$name" --source recent-unwrapped --lines "$lines"
    ;;
  keys)
    name=${1:-}; shift || true
    [[ -n $name && $# -ge 1 ]] || die "keys SLUG KEY..."
    herdr agent send-keys "$name" "$@"
    ;;
  status)
    list=$(herdr agent list 2>&1) || die "no Herdr server reachable: $list"
    printf '%s' "$list" | python3 -c 'import json,sys
agents = json.load(sys.stdin)["result"]["agents"]
if not agents: print("no live agents")
for a in agents:
    print(a.get("name") or a.get("pane_id"), a.get("agent"), a.get("agent_status"), a.get("cwd"))'
    ;;
  stop)
    name=${1:-}; [[ -n $name ]] || die "stop SLUG"
    ws=$(herdr agent get "$name" 2>/dev/null | json 'd["result"]["agent"]["workspace_id"]')
    [[ -n $ws ]] || die "no live agent named $name; herdr agent list shows what runs"
    # A worker mid-turn is not removed under it. A settled one is: the
    # workspace removal ends the pane process, so the agent need not exit
    # on its own (Claude Code ignores repeated interrupts at its prompt).
    st=$(agent_status "$name")
    [[ $st == working ]] && die "$name is still working; wait for it, or interrupt it by hand with: herdr agent send-keys $name esc"
    herdr agent send-keys "$name" ctrl+c >/dev/null 2>&1; sleep 1
    out=$(herdr worktree remove --workspace "$ws" 2>&1) || die "worktree remove refused: $out"
    ;;
  *) sed -n '2,17p' "$0" >&2; exit 2 ;;
esac
