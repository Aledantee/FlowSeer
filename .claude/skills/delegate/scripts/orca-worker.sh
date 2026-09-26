#!/usr/bin/env bash
# Drive one supervised worker through Orca: a child worktree, a terminal
# running the chosen CLI with the model on its launch line, the brief, and
# the settled-state wait.
#
# orca-worker.sh start --lane SLUG --cli claude|codex|agy --model ID [--effort LEVEL] --role ROLE [--plan FILE] [--unit NAME] --brief FILE [--base REF]
# orca-worker.sh start --lane SLUG --cli opencode --model provider/model --role ROLE [--plan FILE] [--unit NAME] --brief FILE [--base REF]
# orca-worker.sh wait   SLUG [--timeout MS] [--stall S]  # prints idle|exited|stalled|timeout, then the screen
# orca-worker.sh read   SLUG [--lines N]
# orca-worker.sh keys   SLUG TEXT                # raw text into the terminal, no Enter; 200 characters at most
# orca-worker.sh tell   SLUG FILE                # a message longer than keys takes, delivered like the brief
# orca-worker.sh status
# orca-worker.sh grade  SLUG --outcome accepted|amended|rejected|blocked --verify pass|fail|none [--note TEXT]
# orca-worker.sh stop   SLUG [--stalled]         # --stalled: after wait printed stalled
#
# `start` prints one JSON line {"name","terminal","worktree","path","branch","run"}
# and exits 0 only when the worker was pointed at its brief and lane state was
# saved; failures after the worktree exists remove it when cleanup succeeds.
# Orca prefixes the branch with the git user, so read `branch` from that line
# instead of assuming the slug.
# The launch line carries the model because `orca orchestration worker-start
# --model` pins Claude, Codex, and Cursor ids only. Lane state lives in
# <git common dir>/orca-workers/. Run unsandboxed: Orca is a local socket.

set -uo pipefail

die() { echo "orca-worker: $*" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "$1 not on PATH"; }
need orca; need python3; need git
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

json() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {"d": d}))' "$1" 2>/dev/null; }
state_dir=$(git rev-parse --path-format=absolute --git-common-dir)/orca-workers
field() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$state_dir/$1.json" "$2" 2>/dev/null; }
screen() { [[ -n $1 ]] || return 1; orca terminal read --terminal "$1" --screen 2>/dev/null; }
brief_name=.orca-brief.md
note_name=.orca-note.md
# Every agent TUI here shows an "esc ... interrupt" hint only while a turn
# runs; agy words it "esc to cancel".
working() { grep -q -i -E 'esc( to)? (interrupt|cancel)' <<<"$1"; }
# Delivers FILE into the lane's checkout under NAME, excluded from the tree,
# and points the terminal at it. A long paragraph through `orca terminal
# send` arrives truncated or as stray characters, so text goes in a file.
deliver() {
  local term=$1 path=$2 file=$3 name=$4 exclude pointer
  exclude=$(git rev-parse --path-format=absolute --git-common-dir)/info/exclude
  if ! grep -q -x -F "/$name" "$exclude" 2>/dev/null; then
    mkdir -p "$(dirname "$exclude")" || return 1
    echo "/$name" >>"$exclude" || return 1
  fi
  cp "$file" "$path/$name" || return 1
  pointer="Read $name in this directory and carry it out. Never commit or delete that file."
  # An earlier pointer may still be on screen, so delivery is one more of them.
  local before
  before=$(screen "$term" | grep -c -F -- "Read $name")
  for _ in 1 2; do
    orca terminal send --terminal "$term" --text "$pointer" --enter --wait-submit 20 --json >/dev/null 2>&1
    sleep 2
    (( $(screen "$term" | grep -c -F -- "Read $name") > before )) && return 0
    sleep 2
  done
  return 1
}

cmd=${1:-}; shift || true
case "$cmd" in
  start)
    lane='' cli='' model='' effort='' agent='' role='' plan='' unit='' brief='' base=''
    while (($#)); do
      case "$1" in
        --lane) lane=$2; shift 2 ;;
        --cli) cli=$2; shift 2 ;;
        --model) model=$2; shift 2 ;;
        --effort) effort=$2; shift 2 ;;
        --agent) agent=$2; shift 2 ;;
        --role) role=$2; shift 2 ;;
        --plan) plan=$2; shift 2 ;;
        --unit) unit=$2; shift 2 ;;
        --brief) brief=$2; shift 2 ;;
        --base) base=$2; shift 2 ;;
        *) die "unknown flag $1" ;;
      esac
    done
    for v in lane cli brief role; do [[ -n "${!v}" ]] || die "--$v is required"; done
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
    # A failed cleanup keeps lane state so status still names what needs removal.
    term='' run_id='' base_sha=''
    save_state() {
      python3 -c 'import json,sys; json.dump(dict(zip(("name","cli","terminal","worktree","path","branch","run"), sys.argv[2:])), open(sys.argv[1],"w"))' \
        "$state_dir/$lane.json" "$lane" "$cli" "$term" "$wt" "$path" "$branch" "$run_id"
    }
    undo() {
      local reason="$*" head_sha end_out cleanup_failed='' state_out=''
      if [[ -n $run_id ]]; then
        head_sha=$(git -C "$path" rev-parse HEAD 2>/dev/null) || head_sha=$base_sha
        end_out=$(python3 "$script_dir/runlog.py" end --run "$run_id" --head "$head_sha" 2>&1) \
          || reason="${reason}; run log end failed: $end_out"
      fi
      if [[ -n $term ]] && ! orca terminal close --terminal "$term" --json >/dev/null 2>&1; then
        cleanup_failed='terminal close failed'
      fi
      if [[ -z $cleanup_failed ]] && ! orca worktree rm --worktree "id:$wt" --force --json >/dev/null 2>&1; then
        cleanup_failed='worktree removal failed'
      fi
      if [[ -n $cleanup_failed ]]; then
        if ! mkdir -p "$state_dir" 2>/dev/null || ! state_out=$(save_state 2>&1); then
          reason="${reason}; cannot save lane state for $lane${state_out:+: $state_out}"
        fi
        die "$reason; $cleanup_failed; lane $lane is still live and must be removed by hand"
      fi
      rm -f "$state_dir/$lane.json" >/dev/null 2>&1 || true
      die "$reason"
    }
    base_sha=$(git -C "$path" rev-parse HEAD 2>&1) || undo "cannot resolve initial HEAD at $path: $base_sha"

    # A safety-classifier flag must not silently move a Claude worker to the
    # fallback model for the rest of its session: with switching off, the
    # worker stops at the switch-or-edit prompt, which a screen read shows.
    case "$cli" in
      claude) line="claude --model $model --dangerously-skip-permissions --settings '{\"switchModelsOnFlag\":false}'${effort:+ --effort $effort}" ;;
      codex)  line="codex -a never --sandbox danger-full-access -c check_for_update_on_startup=false -m $model${effort:+ -c model_reasoning_effort=$effort}" ;;
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
    # The wait only paces startup: it fails within seconds for an agy
    # terminal that is running. The status check below catches a CLI that
    # exited.
    orca terminal wait --terminal "$term" --for tui-idle --timeout-ms 90000 --json >/dev/null 2>&1

    # Codex startup dialog: the hooks review for a repository with
    # .codex/hooks.json. A prompt sent into it is lost. The update offer is
    # off on the launch line rather than answered: its option numbering is
    # not stable across versions, and a wrong answer runs the upgrade and
    # leaves the terminal at a shell. An offer that shows anyway fails the
    # start before the brief pointer's Enter can pick an option.
    if [[ $cli == codex ]]; then
      for _ in 1 2 3 4; do
        sleep 2; s=$(screen "$term")
        grep -q 'restart Codex' <<<"$s" && undo "Codex updated itself and exited at startup for $lane"
        grep -q 'Skip until next version' <<<"$s" && undo "Codex showed its update offer despite check_for_update_on_startup=false for $lane"
        if grep -q 'hook needs review' <<<"$s"; then
          orca terminal send --terminal "$term" --text t --json >/dev/null \
            || undo "cannot dismiss Codex hooks review for $lane"
          sleep 1
          orca terminal send --terminal "$term" --text $'\e' --json >/dev/null \
            || undo "cannot dismiss Codex hooks review for $lane"
        else
          break
        fi
      done
      grep -q 'hook needs review' <<<"$(screen "$term")" && undo "$lane still shows the hooks review after four rounds"
    fi
    st=$(orca terminal show --terminal "$term" --json 2>/dev/null | json 'd["result"]["terminal"].get("status") or d["result"].get("status")') \
      || undo "terminal show failed for $lane"
    [[ -n $st ]] || undo "terminal show returned no status for $lane"
    [[ $st == exited ]] && undo "$cli exited at startup: $(screen "$term" | tail -5)"

    start_args=(
      start
      --lane "$lane"
      --cli "$cli"
      --role "$role"
      --worktree "$path"
      --branch "$branch"
      --base "$base_sha"
    )
    [[ -n $model ]] && start_args+=(--model "$model")
    [[ -n $effort ]] && start_args+=(--effort "$effort")
    [[ -n $agent ]] && start_args+=(--agent "$agent")
    [[ -n $plan ]] && start_args+=(--plan "$plan")
    [[ -n $unit ]] && start_args+=(--unit "$unit")
    run_out=$(python3 "$script_dir/runlog.py" "${start_args[@]}" 2>&1) || undo "run log start failed: $run_out"
    run_id=$run_out

    # The brief goes in a file in the worker's checkout and the terminal gets
    # a one-line pointer: a long paragraph through `orca terminal send`
    # arrives as stray characters, silently at both ends. The file is
    # excluded through the shared info/exclude so the tree stays clean. Orca
    # cannot observe delivery for every CLI ("provider: unsupported"), so the
    # screen is the check, and a dropped submission is sent once more.
    exclude=$(git rev-parse --path-format=absolute --git-common-dir)/info/exclude
    if ! grep -q -x -F "/$brief_name" "$exclude" 2>/dev/null; then
      mkdir -p "$(dirname "$exclude")" || undo "cannot create git exclude directory"
      echo "/$brief_name" >>"$exclude" || undo "cannot write git exclude file"
    fi
    cp "$brief" "$path/$brief_name" || undo "cannot write the brief into $path"
    pointer="Read $brief_name in this directory and carry it out. Never commit or delete that file."
    ok='' send_error=''
    for _ in 1 2; do
      send_out=$(orca terminal send --terminal "$term" --text "$pointer" --enter --wait-submit 20 --json 2>&1) \
        || { send_error="; send failed: $send_out"; continue; }
      sleep 2
      grep -q -F -- "Read $brief_name" <<<"$(screen "$term")" && { ok=1; break; }
      sleep 2
    done
    [[ -n $ok ]] || undo "pointer not on $lane's screen after two submissions${send_error}: $(screen "$term" | tail -8)"
    mkdir -p "$state_dir" || undo "cannot create lane state directory $state_dir"
    state_out=$(save_state 2>&1) \
      || undo "cannot write lane state for $lane: $state_out"
    printf '{"name":"%s","terminal":"%s","worktree":"%s","path":"%s","branch":"%s","run":"%s"}\n' "$lane" "$term" "$wt" "$path" "$branch" "$run_id"
    ;;
  wait)
    name=${1:-}; shift || true; timeout=0 stall=1200
    while (($# >= 2)); do
      case "$1" in
        --timeout) timeout=$2; shift 2 ;;
        --stall) stall=$2; shift 2 ;;
        *) die "unknown flag $1" ;;
      esac
    done
    term=$(field "$name" terminal); [[ -n $term ]] || die "no lane named $name; status lists them"
    # tui-idle alone is not the end of a turn: it was seen satisfied while
    # the agent was mid-turn, and the interrupt hint vanishes between tool
    # calls. The turn has ended when two reads five seconds apart show no
    # hint and the same screen. A screen that shows the hint and has not
    # changed for --stall seconds is a hung model stream (opencode lanes
    # ignored Escape for an hour): `stalled`, for the coordinator to close.
    deadline=$(( timeout > 0 ? $(date +%s) + timeout / 1000 : 0 ))
    last='' since=$(date +%s) show_screen=true
    while :; do
      out=$(orca terminal wait --terminal "$term" --for tui-idle --timeout-ms 60000 --json 2>&1)
      st=$(printf '%s' "$out" | json 'd["result"]["wait"]["status"]')
      [[ $st == exited ]] && { echo exited; break; }
      # A terminal closed outside this script reads as nothing, twice, which
      # would otherwise pass for a settled screen.
      s1=$(screen "$term") || { echo exited; show_screen=false; break; }
      if ! working "$s1"; then
        sleep 5
        s2=$(screen "$term")
        ! working "$s2" && [[ $s1 == "$s2" ]] && { echo idle; break; }
      fi
      now=$(date +%s)
      if [[ $s1 != "$last" ]]; then
        last=$s1 since=$now
      elif (( stall > 0 && now - since >= stall )); then
        echo stalled; break
      fi
      (( deadline > 0 && now >= deadline )) && { echo timeout; exit 0; }
      sleep 5
    done
    # A permission dialog also reads as idle, so the screen follows unless its
    # failed read is what reported the terminal as exited.
    if [[ $show_screen == true ]]; then
      screen "$term" | grep -v -E '^\s*$' | tail -30
    fi
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
    # Longer text arrives with only its tail on the screen.
    (( ${#1} <= 200 )) || die "keys takes 200 characters at most; write the text to a file and use tell"
    orca terminal send --terminal "$term" --text "$1" --json >/dev/null
    ;;
  tell)
    name=${1:-}; file=${2:-}
    term=$(field "$name" terminal); path=$(field "$name" path)
    [[ -n $term && -n $path ]] || die "tell SLUG FILE"
    [[ -f $file ]] || die "file $file not found"
    deliver "$term" "$path" "$file" "$note_name" || die "message not on $name's screen after two submissions: $(screen "$term" | tail -8)"
    ;;
  status)
    shopt -s nullglob; files=("$state_dir"/*.json)
    ((${#files[@]})) || { echo "no live lanes"; exit 0; }
    for f in "${files[@]}"; do
      n=$(basename "$f" .json); term=$(field "$n" terminal)
      if [[ -z $term ]]; then
        echo "$n $(field "$n" cli) unavailable-terminal $(field "$n" path)"
        continue
      fi
      s=$(screen "$term") || { echo "$n $(field "$n" cli) gone $(field "$n" path)"; continue; }
      if working "$s"; then st=working; else st=idle; fi
      echo "$n $(field "$n" cli) $st $(field "$n" path)"
    done
    ;;
  grade)
    name=${1:-}; shift || true
    [[ -n $name ]] || die "grade SLUG --outcome OUTCOME --verify VERIFY [--note NOTE]"
    outcome='' verify='' note=''
    while (($#)); do
      case "$1" in
        --outcome) outcome=$2; shift 2 ;;
        --verify) verify=$2; shift 2 ;;
        --note) note=$2; shift 2 ;;
        *) die "unknown flag $1" ;;
      esac
    done
    [[ -n $outcome ]] || die "--outcome is required"
    [[ -n $verify ]] || die "--verify is required"
    [[ -f "$state_dir/$name.json" ]] || die "no lane named $name; status lists them"
    run_id=$(field "$name" run)
    [[ -n $run_id ]] || die "lane $name has no run"
    grade_args=(
      grade
      --run "$run_id"
      --outcome "$outcome"
      --verify "$verify"
    )
    [[ -n $note ]] && grade_args+=(--note "$note")
    out=$(python3 "$script_dir/runlog.py" "${grade_args[@]}" 2>&1) || die "$out"
    ;;
  stop)
    name=${1:-}; [[ -n $name ]] || die "stop SLUG [--stalled]"
    stalled=false; [[ ${2:-} == --stalled ]] && stalled=true
    term=$(field "$name" terminal); wt=$(field "$name" worktree); path=$(field "$name" path)
    [[ -n $wt ]] || die "no lane named $name; status lists them"
    run_id=$(field "$name" run)
    [[ -n $run_id ]] || die "lane $name has no run; cannot verify grade"
    # -B: a bytecode cache beside runlog.py would leave the tree untracked.
    python3 -B -c 'import sys; sys.path.insert(0, sys.argv[1]); import runlog; sys.exit(0 if any(e.get("event") == "grade" and e.get("run") == sys.argv[2] for e in runlog.read()) else 1)' \
      "$script_dir" "$run_id" || die "lane $name has no grade event; grade it before stop"
    # A stalled lane shows the working hint over a turn that will not end.
    [[ $stalled == true ]] || ! working "$(screen "$term")" \
      || die "$name is still working; wait for it, or pass --stalled after wait printed stalled"
    rm -f "$path/$brief_name" "$path/$note_name"
    dirty=$(git -C "$path" status --porcelain 2>/dev/null)
    [[ -z $dirty ]] || die "$path is dirty; nothing removed: $dirty"
    # `orca worktree rm` deletes the branch with the checkout, so commits
    # that were not merged here would go with it.
    branch=$(field "$name" branch)
    git merge-base --is-ancestor "$branch" HEAD 2>/dev/null \
      || die "$branch has commits that are not merged into $(git branch --show-current); merge it first, or remove the lane by hand in Orca"
    head_sha=$(git rev-parse "$branch" 2>&1) || die "cannot resolve branch $branch: $head_sha"
    # `end` waits for the terminal to close, so the log never ends a run
    # whose worker still runs, and precedes the removal, so a failed removal
    # still leaves the run ended.
    orca terminal close --terminal "$term" --json >/dev/null 2>&1 \
      || die "terminal close failed for $name; nothing removed"
    end_out=$(python3 "$script_dir/runlog.py" end --run "$run_id" --head "$head_sha" 2>&1) \
      || die "run log end failed after $name's terminal closed: $end_out"
    out=$(orca worktree rm --worktree "id:$wt" --json 2>&1) || die "worktree rm refused: $out"
    rm -f "$state_dir/$name.json" || die "cannot remove lane state for $name"
    ;;
  *) sed -n '2,23p' "$0" >&2; exit 2 ;;
esac
