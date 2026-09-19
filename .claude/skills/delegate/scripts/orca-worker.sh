#!/usr/bin/env bash
# Drive one supervised worker through Orca: a child worktree, a terminal
# running the chosen CLI with the model on its launch line, the brief, and
# the settled-state wait.
#
# orca-worker.sh start --lane SLUG --cli claude|codex|agy --model ID [--effort LEVEL] --brief FILE [--base REF]
# orca-worker.sh start --lane SLUG --cli opencode --model provider/model --brief FILE [--base REF]
# orca-worker.sh wait   SLUG [--timeout MS]      # prints idle|exited|timeout, then the screen
# orca-worker.sh read   SLUG [--lines N]
# orca-worker.sh keys   SLUG TEXT                # raw text into the terminal, no Enter
# orca-worker.sh status
# orca-worker.sh stop   SLUG
#
# `start` prints one JSON line {"name","terminal","worktree","path","branch"}
# and exits 0 only when the worker was pointed at its brief; any failure
# after the worktree exists removes it again. Orca prefixes the branch with
# the git user, so read `branch` from that line instead of assuming the slug.
# The launch line carries the model because `orca orchestration worker-start
# --model` pins Claude, Codex, and Cursor ids only. Lane state lives in
# <git common dir>/orca-workers/. Run unsandboxed: Orca is a local socket.

set -uo pipefail

die() { echo "orca-worker: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "$1 not on PATH"; }
need orca; need python3; need git

json() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {"d": d}))' "$1" 2>/dev/null; }
state_dir=$(git rev-parse --path-format=absolute --git-common-dir)/orca-workers
field() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$state_dir/$1.json" "$2" 2>/dev/null; }
screen() { orca terminal read --terminal "$1" --screen 2>/dev/null; }
brief_name=.orca-brief.md
# Every agent TUI here shows an "esc ... interrupt" hint only while a turn runs.
working() { grep -q -i -E 'esc( to)? interrupt' <<<"$1"; }

cmd=${1:-}; shift || true
case "$cmd" in
  start)
    lane='' cli='' model='' effort='' agent='' brief='' base=''
    while (($#)); do
      case "$1" in
        --lane) lane=$2; shift 2 ;;
        --cli) cli=$2; shift 2 ;;
        --model) model=$2; shift 2 ;;
        --effort) effort=$2; shift 2 ;;
        --agent) agent=$2; shift 2 ;;
        --brief) brief=$2; shift 2 ;;
        --base) base=$2; shift 2 ;;
        *) die "unknown flag $1" ;;
      esac
    done
    for v in lane cli brief; do [[ -n "${!v}" ]] || die "--$v is required"; done
    [[ "$lane" =~ ^[a-z][a-z0-9_-]{0,31}$ ]] || die "lane must match [a-z][a-z0-9_-]{0,31}"
    [[ -f "$brief" ]] || die "brief $brief not found"
    case "$cli" in
      claude|codex) [[ -n $model ]] || die "--model is required for $cli"; [[ -z $agent ]] || die "--agent does not apply to $cli" ;;
      agy) [[ -n $model ]] || die "--model is required for agy"; [[ -z $effort ]] || die "--effort does not apply to agy: it is part of the model id"; [[ -z $agent ]] || die "--agent does not apply to agy" ;;
      # The default agent only. A custom agent profile that pins a model
      # carries no system prompt, and models on it reason without ever
      # calling a tool or answer nothing at all (2026-09-19); the same models
      # answer in seconds on the default agent with the model on the launch line.
      opencode) [[ -n $model ]] || die "--model is required for opencode, as provider/model"; [[ -z $agent$effort ]] || die "--agent and --effort do not apply to opencode: it runs the default agent" ;;
      *) die "unknown cli $cli" ;;
    esac
    orca status --json 2>/dev/null | json 'd["result"]["runtime"]["reachable"]' | grep -q True \
      || die "Orca runtime not reachable; sandboxed calls report this too"
    [[ -e $state_dir/$lane.json ]] && die "a lane named $lane exists; stop it or pick another slug"

    # Without --base-branch the child starts from main, and merging it then
    # also merges whatever landed on main since.
    base=${base:-$(git branch --show-current)}
    [[ -n $base ]] || die "detached HEAD: pass --base REF"
    created=$(orca worktree create --name "$lane" --parent-worktree active --base-branch "$base" --setup skip \
      --comment "delegate lane $lane ($cli)" --json 2>&1) || die "worktree create failed: $created"
    wt=$(printf '%s' "$created" | json 'd["result"]["worktree"]["id"]')
    path=$(printf '%s' "$created" | json 'd["result"]["worktree"]["path"]')
    branch=$(printf '%s' "$created" | json 'd["result"]["worktree"]["branch"].removeprefix("refs/heads/")')
    [[ -n $wt && -n $path && -n $branch ]] || die "worktree create returned no id, path, or branch: $created"
    # Every failure from here on removes what was created.
    term=
    undo() {
      [[ -n $term ]] && orca terminal close --terminal "$term" --json >/dev/null 2>&1
      orca worktree rm --worktree "id:$wt" --force --json >/dev/null 2>&1
      rm -f "$state_dir/$lane.json"; die "$*"
    }

    case "$cli" in
      claude) line="claude --model $model --dangerously-skip-permissions${effort:+ --effort $effort}" ;;
      codex)  line="codex -a never --sandbox danger-full-access -m $model${effort:+ -c model_reasoning_effort=$effort}" ;;
      agy)    line="agy --model $model --dangerously-skip-permissions" ;;
      opencode) line="opencode --model $model" ;;
    esac
    # A Claude worker started under this session's child-session variables
    # runs with transcript saving off.
    line="env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_CHILD_SESSION $line"
    started=$(orca terminal create --worktree "id:$wt" --title "$lane" --command "$line" --json 2>&1) \
      || undo "terminal create failed: $started"
    term=$(printf '%s' "$started" | json 'd["result"]["terminal"]["handle"]')
    [[ -n $term ]] || undo "terminal create returned no handle: $started"
    orca terminal wait --terminal "$term" --for tui-idle --timeout-ms 90000 --json >/dev/null 2>&1

    # Codex startup dialogs: its update offer and the hooks review for a
    # repository with .codex/hooks.json. A prompt sent into either is lost.
    # The update offer is matched on its "Skip until next version" option:
    # Codex keeps an "Update available!" banner up after the dialog is
    # answered. Option 3 skips the version, so the offer does not come back.
    if [[ $cli == codex ]]; then
      for _ in 1 2 3 4; do
        sleep 2; s=$(screen "$term")
        if grep -q 'Skip until next version' <<<"$s"; then
          orca terminal send --terminal "$term" --text 3 --enter --json >/dev/null
        elif grep -q 'hook needs review' <<<"$s"; then
          orca terminal send --terminal "$term" --text t --json >/dev/null; sleep 1
          orca terminal send --terminal "$term" --text $'\e' --json >/dev/null
        else
          break
        fi
      done
      grep -q 'hook needs review' <<<"$(screen "$term")" && undo "$lane still shows the hooks review after four rounds"
    fi
    st=$(orca terminal show --terminal "$term" --json 2>/dev/null | json 'd["result"]["terminal"].get("status") or d["result"].get("status")')
    [[ $st == exited ]] && undo "$cli exited at startup: $(screen "$term" | tail -5)"

    # The brief goes in a file in the worker's checkout and the terminal gets
    # a one-line pointer: a long paragraph through `orca terminal send`
    # arrives as stray characters, silently at both ends. The file is
    # excluded through the shared info/exclude so the tree stays clean. Orca
    # cannot observe delivery for every CLI ("provider: unsupported"), so the
    # screen is the check, and a dropped submission is sent once more.
    exclude=$(git rev-parse --path-format=absolute --git-common-dir)/info/exclude
    grep -q -x -F "/$brief_name" "$exclude" 2>/dev/null || { mkdir -p "$(dirname "$exclude")"; echo "/$brief_name" >>"$exclude"; }
    cp "$brief" "$path/$brief_name" || undo "cannot write the brief into $path"
    pointer="Read $brief_name in this directory and carry it out. Never commit or delete that file."
    ok=
    for _ in 1 2; do
      orca terminal send --terminal "$term" --text "$pointer" --enter --wait-submit 20 --json >/dev/null 2>&1
      sleep 2
      grep -q -F -- "Read $brief_name" <<<"$(screen "$term")" && { ok=1; break; }
      sleep 2
    done
    [[ -n $ok ]] || undo "pointer not on $lane's screen after two submissions: $(screen "$term" | tail -8)"
    mkdir -p "$state_dir"
    python3 -c 'import json,sys; json.dump(dict(zip(("name","cli","terminal","worktree","path","branch"), sys.argv[2:])), open(sys.argv[1],"w"))' \
      "$state_dir/$lane.json" "$lane" "$cli" "$term" "$wt" "$path" "$branch"
    printf '{"name":"%s","terminal":"%s","worktree":"%s","path":"%s","branch":"%s"}\n' "$lane" "$term" "$wt" "$path" "$branch"
    ;;
  wait)
    name=${1:-}; shift || true; timeout=0
    [[ $# -ge 2 && $1 == --timeout ]] && timeout=$2
    term=$(field "$name" terminal); [[ -n $term ]] || die "no lane named $name; status lists them"
    # tui-idle alone is not the end of a turn: it was seen satisfied while
    # the agent was mid-turn. The turn has ended when the screen shows no
    # interrupt hint on two reads five seconds apart.
    deadline=$(( timeout > 0 ? $(date +%s) + timeout / 1000 : 0 ))
    while :; do
      out=$(orca terminal wait --terminal "$term" --for tui-idle --timeout-ms 60000 --json 2>&1)
      st=$(printf '%s' "$out" | json 'd["result"]["wait"]["status"]')
      [[ $st == exited ]] && { echo exited; break; }
      if ! working "$(screen "$term")"; then
        sleep 5
        working "$(screen "$term")" || { echo idle; break; }
      fi
      (( deadline > 0 && $(date +%s) >= deadline )) && { echo timeout; exit 0; }
      sleep 5
    done
    # A permission dialog also reads as idle, so the screen always follows.
    screen "$term" | grep -v -E '^\s*$' | tail -30
    ;;
  read)
    name=${1:-}; shift || true; lines=200
    [[ $# -ge 2 && $1 == --lines ]] && lines=$2
    term=$(field "$name" terminal); [[ -n $term ]] || die "no lane named $name; status lists them"
    orca terminal read --terminal "$term" --screen --limit "$lines"
    ;;
  keys)
    name=${1:-}; shift || true
    term=$(field "$name" terminal); [[ -n $term && $# -ge 1 ]] || die "keys SLUG TEXT"
    orca terminal send --terminal "$term" --text "$1" --json >/dev/null
    ;;
  status)
    shopt -s nullglob; files=("$state_dir"/*.json)
    ((${#files[@]})) || { echo "no live lanes"; exit 0; }
    for f in "${files[@]}"; do
      n=$(basename "$f" .json); term=$(field "$n" terminal)
      s=$(screen "$term") || { echo "$n $(field "$n" cli) gone $(field "$n" path)"; continue; }
      if working "$s"; then st=working; else st=idle; fi
      echo "$n $(field "$n" cli) $st $(field "$n" path)"
    done
    ;;
  stop)
    name=${1:-}; [[ -n $name ]] || die "stop SLUG"
    term=$(field "$name" terminal); wt=$(field "$name" worktree); path=$(field "$name" path)
    [[ -n $wt ]] || die "no lane named $name; status lists them"
    # A worker mid-turn is not removed under it, and a dirty checkout stays
    # for a person to read.
    working "$(screen "$term")" && die "$name is still working; wait for it, or interrupt it by hand in Orca"
    rm -f "$path/$brief_name"
    dirty=$(git -C "$path" status --porcelain 2>/dev/null)
    [[ -z $dirty ]] || die "$path is dirty; nothing removed: $dirty"
    # `orca worktree rm` deletes the branch with the checkout, so commits
    # that were not merged here would go with it.
    branch=$(field "$name" branch)
    git merge-base --is-ancestor "$branch" HEAD 2>/dev/null \
      || die "$branch has commits that are not merged into $(git branch --show-current); merge it first, or remove the lane by hand in Orca"
    orca terminal close --terminal "$term" --json >/dev/null 2>&1
    out=$(orca worktree rm --worktree "id:$wt" --json 2>&1) || die "worktree rm refused: $out"
    rm -f "$state_dir/$name.json"
    ;;
  *) sed -n '2,21p' "$0" >&2; exit 2 ;;
esac
