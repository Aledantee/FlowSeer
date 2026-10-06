#!/usr/bin/env bash
# Drive one supervised worker through Orca: a child worktree, a terminal
# running the chosen CLI with the model on its launch line, the brief, and
# the settled-state wait.
#
# orca-worker.sh start --lane SLUG --cli claude|codex|agy --model ID [--effort LEVEL] --role ROLE [--plan FILE] [--unit NAME] --brief FILE [--base REF | --join LANE]
# orca-worker.sh start --lane SLUG --cli omp --model provider/model [--effort LEVEL] --role ROLE [--plan FILE] [--unit NAME] --brief FILE [--base REF | --join LANE]
# orca-worker.sh line   --cli CLI --model ID [--effort LEVEL]
# orca-worker.sh wait   SLUG [--until CMD] [--max S] [--stall S]  # prints idle|limited|idle-children|done|exited|stalled|timeout, then the screen
# orca-worker.sh read   SLUG [--lines N]
# orca-worker.sh keys   SLUG TEXT                # raw text into the terminal, no Enter; 200 characters at most
# orca-worker.sh tell   SLUG FILE                # any message the worker must act on, delivered and submitted like the brief
# orca-worker.sh check  SLUG                    # verify the Claude lane's recorded model
# orca-worker.sh status
# orca-worker.sh grade  SLUG --outcome accepted|amended|rejected|blocked --verify pass|fail|none [--note TEXT]
# orca-worker.sh stop   SLUG [--stalled] [--keep-worktree]  # --stalled: after wait printed stalled; an unmerged lane needs its commits on parked/SLUG
#                                                           # --keep-worktree: close the terminal, keep the checkout for `start --join`
#
# `start` prints one JSON line {"name","terminal","worktree","path","branch","run"}
# and exits 0 only when the worker was pointed at its brief and lane state was
# saved; failures after the worktree exists remove it when cleanup succeeds.
# Orca prefixes the branch with the git user, so read `branch` from that line
# instead of assuming the slug. `--join LANE` skips the worktree and starts a
# new terminal in the checkout of a kept lane (`stop --keep-worktree`), so one
# plan's stages share one branch. A kept lane's state file stays, with an empty
# terminal and "kept": true, until a `stop` without the flag removes the
# worktree and every state file that names it.
# The launch line carries the model because `orca orchestration worker-start
# --model` pins Claude, Codex, and Cursor ids only. Lane state lives in
# <git common dir>/orca-workers/. Run unsandboxed: Orca is a local socket.

set -uo pipefail

die() { local msg="$*"; echo "orca-worker: ${msg#orca-worker: }" >&2; exit 1; }
need() { command -v "$1" >/dev/null || die "$1 not on PATH"; }
need orca; need python3; need git
script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

json() { python3 -c 'import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1], {"d": d}))' "$1" 2>/dev/null; }
state_dir=$(git rev-parse --path-format=absolute --git-common-dir)/orca-workers
field() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$state_dir/$1.json" "$2" 2>/dev/null; }
# Orca drops the lineage of a removed worktree's children, so a lane that
# started lanes of its own would leave them top-level, unmerged work and all.
# Newlines keep spaces in a worktree id inside one child entry.
children() {
  orca worktree show --worktree "id:$1" --json 2>/dev/null \
    | json '"\n".join(d["result"]["worktree"]["childWorktreeIds"])'
}
child_info() {
  local child=$1 file lane terminal
  for file in "$state_dir"/*.json; do
    [[ -f $file ]] || continue
    lane=${file##*/}; lane=${lane%.json}
    [[ $(field "$lane" worktree) == "$child" ]] || continue
    terminal=$(field "$lane" terminal)
    printf '%s\t%s\n' "$lane" "$terminal"
    return 0
  done
  printf '%s\t\n' "$child"
}
# A kept lane has closed its terminal and left the worktree for a later lane.
# Every other lane whose state names a worktree has a live terminal.
lanes_on() {
  local file lane
  for file in "$state_dir"/*.json; do
    [[ -f $file ]] || continue
    lane=${file##*/}; lane=${lane%.json}
    [[ $(field "$lane" worktree) == "$1" ]] && echo "$lane"
  done
  return 0
}
is_kept() { [[ $(field "$1" kept) == True ]]; }
live_lane_on() {
  local lane
  for lane in $(lanes_on "$1"); do
    [[ $lane == "${2:-}" ]] && continue
    is_kept "$lane" || { echo "$lane"; return 0; }
  done
  return 1
}
screen() { [[ -n $1 ]] || return 1; orca terminal read --terminal "$1" --screen 2>/dev/null; }
brief_name=.orca-brief.md
note_name=.orca-note.md
# Every agent TUI here shows an "esc ... interrupt" hint only while a turn
# runs; agy words it "esc to cancel".
working() { grep -q -i -E 'esc( to)? (interrupt|cancel)' <<<"$1"; }
# A worker waiting out a pool's window shows no hint and a still screen,
# which is otherwise the settled state. Each CLI words that wait its own
# way and none was captured, so the match is broad and the outcome only
# tells the coordinator to read the screen before treating the lane as done.
# A bare "quota" is not matched: a finished report on quota code names it.
limited() { grep -v -E '^\s*$' <<<"$1" | tail -30 | grep -q -i -E 'rate.?limit|usage limit|limit reached|quota.{0,20}(exceeded|exhausted|reached)|(exceeded|exhausted|reached).{0,20}quota|try again (in|at)|resets? (in|at)'; }
launch_line() {
  local cli=$1 model=$2 effort=${3:-} line
  case "$cli" in
    # A safety-classifier flag must not silently move a Claude worker to the
    # fallback model for the rest of its session: with switching off, the
    # worker stops at the switch-or-edit prompt, which a screen read shows.
    claude) [[ -n $model ]] || die "--model is required for claude"; line="claude --model $model --dangerously-skip-permissions --settings '{\"switchModelsOnFlag\":false}'${effort:+ --effort $effort}" ;;
    codex)  [[ -n $model ]] || die "--model is required for codex"; line="codex -a never --sandbox danger-full-access -c check_for_update_on_startup=false -c background_terminal_max_timeout=3600000 -m $model${effort:+ -c model_reasoning_effort=$effort}" ;;
    agy)    [[ -n $model ]] || die "--model is required for agy"; [[ -z $effort ]] || die "--effort does not apply to agy: it is part of the model id"; line="agy --model $model --dangerously-skip-permissions" ;;
    omp)    [[ -n $model ]] || die "--model is required for omp, as provider/model"; line="omp --model $model${effort:+ --thinking $effort}" ;;
    *) die "unknown cli $cli" ;;
  esac
  # A Claude worker started under this session's child-session variables runs
  # with transcript saving off.
  printf 'env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_CHILD_SESSION %s\n' "$line"
}
wait_sleep() {
  local deadline=$1 remaining delay=5
  remaining=$((deadline - $(date +%s)))
  (( remaining > 0 )) || return 1
  (( remaining < delay )) && delay=$remaining
  sleep "$delay"
}
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

model_check_lane() {
  local lane=$1 cli run_id start_data model at path
  cli=$(field "$lane" cli); path=$(field "$lane" path); run_id=$(field "$lane" run)
  [[ -n $cli && -n $path && -n $run_id ]] || die "lane $lane has incomplete state"
  if [[ $cli != claude ]]; then
    echo "not checked: $cli"
    return 0
  fi
  start_data=$(python3 -B -c 'import sys; sys.path.insert(0, sys.argv[1]); import runlog; events = [event for event in runlog.read() if event.get("event") == "start" and event.get("run") == sys.argv[2]]; event = events[-1] if events else {}; print("\t".join((event.get("model", ""), event.get("at", ""))))' "$script_dir" "$run_id") \
    || die "cannot read start event for lane $lane"
  IFS=$'\t' read -r model at <<<"$start_data"
  [[ -n $model && -n $at ]] || die "lane $lane has no start model and time"
  python3 "$script_dir/model_check.py" "$path" "$model" "$at"
}

cmd=${1:-}; shift || true
case "$cmd" in
  line)
    cli='' model='' effort=''
    while (($#)); do
      case "$1" in
        --cli) [[ $# -ge 2 ]] || die "--cli requires a value"; cli=$2; shift 2 ;;
        --model) [[ $# -ge 2 ]] || die "--model requires a value"; model=$2; shift 2 ;;
        --effort) [[ $# -ge 2 ]] || die "--effort requires a value"; effort=$2; shift 2 ;;
        *) die "unknown flag $1" ;;
      esac
    done
    [[ -n $cli ]] || die "--cli is required"
    launch_line "$cli" "$model" "$effort"
    ;;
  start)
    lane='' cli='' model='' effort='' agent='' role='' plan='' unit='' brief='' base='' join=''
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
        --join) [[ $# -ge 2 ]] || die "--join requires a lane"; join=$2; shift 2 ;;
        *) die "unknown flag $1" ;;
      esac
    done
    [[ -z $join || -z $base ]] || die "--join and --base are exclusive: a joined lane starts from its worktree's HEAD"
    for v in lane cli brief role; do [[ -n "${!v}" ]] || die "--$v is required"; done
    [[ "$lane" =~ ^[a-z][a-z0-9_-]{0,31}$ ]] || die "lane must match [a-z][a-z0-9_-]{0,31}"
    [[ -f "$brief" ]] || die "brief $brief not found"
    [[ -s "$brief" ]] || die "brief $brief is empty"
    case "$cli" in
      claude|codex) [[ -n $model ]] || die "--model is required for $cli"; [[ -z $agent ]] || die "--agent does not apply to $cli" ;;
      agy) [[ -n $model ]] || die "--model is required for agy"; [[ -z $effort ]] || die "--effort does not apply to agy: it is part of the model id"; [[ -z $agent ]] || die "--agent does not apply to agy" ;;
      # omp has no agent profiles; it pins the model with --model and maps
      # --effort to --thinking on the launch line.
      omp) [[ -n $model ]] || die "--model is required for omp, as provider/model"; [[ -z $agent ]] || die "--agent does not apply to omp: it has no agent profiles" ;;
      *) die "unknown cli $cli" ;;
    esac
    orca status --json 2>/dev/null | json 'd["result"]["runtime"]["reachable"]' | grep -q True \
      || die "Orca runtime not reachable; sandboxed calls report this too"
    [[ -e $state_dir/$lane.json ]] && die "a lane named $lane exists; stop it or pick another slug"

    if [[ -n $join ]]; then
      # A joined lane runs in the worktree of a kept lane, so a later stage
      # of one plan continues on the same branch without a merge in between.
      wt=$(field "$join" worktree); path=$(field "$join" path); branch=$(field "$join" branch)
      [[ -n $wt && -n $path && -n $branch ]] || die "no lane named $join to join; status lists them"
      live=$(live_lane_on "$wt") && die "lane $live still has a live terminal in $path; stop it with --keep-worktree first"
      dirty=$(git -C "$path" status --porcelain 2>&1) || die "cannot read the state of $path: $dirty"
      [[ -z $dirty ]] || die "$path is dirty; commit or discard before joining: $dirty"
    else
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
    fi
    # A failed cleanup keeps lane state so status still names what needs removal.
    term='' run_id='' base_sha=''
    save_state() {
      python3 -c 'import json,sys; json.dump(dict(zip(("name","cli","terminal","worktree","path","branch","run"), sys.argv[2:])), open(sys.argv[1],"w"))' \
        "$state_dir/$lane.json" "$lane" "$cli" "$term" "$wt" "$path" "$branch" "$run_id"
    }
    undo() {
      local reason="$*" head_sha end_out cleanup_failed='' state_out='' kids
      if [[ -n $run_id ]]; then
        head_sha=$(git -C "$path" rev-parse HEAD 2>/dev/null) || head_sha=$base_sha
        end_out=$(python3 "$script_dir/runlog.py" end --run "$run_id" --head "$head_sha" 2>&1) \
          || reason="${reason}; run log end failed: $end_out"
      fi
      if [[ -n $term ]] && ! orca terminal close --terminal "$term" --json >/dev/null 2>&1; then
        cleanup_failed='terminal close failed'
      fi
      # A joined lane's worktree holds the plan's earlier stages, so a failed
      # start closes its terminal and leaves the checkout.
      if [[ -z $cleanup_failed && -n $join ]]; then
        rm -f "$state_dir/$lane.json" >/dev/null 2>&1 || true
        die "$reason"
      fi
      # Checked after the close, so the worker cannot start another lane.
      if [[ -z $cleanup_failed ]]; then
        kids=$(children "$wt") || cleanup_failed='child worktree check failed'
        if [[ -z $cleanup_failed && -n $kids ]]; then
          kids=${kids//$'\n'/ }
          cleanup_failed="it has child worktrees: $kids"
        fi
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

    line=$(launch_line "$cli" "$model" "$effort")
    started=$(orca terminal create --worktree "id:$wt" --title "$lane" --command "$line" --json 2>&1) \
      || undo "terminal create failed: $started"
    term=$(printf '%s' "$started" | json 'd["result"]["terminal"]["handle"]')
    [[ -n $term ]] || undo "terminal create returned no handle: $started"
    # The wait only paces startup: it fails within seconds for an agy
    # terminal that is running. The status check below catches a CLI that
    # exited. A timed-out wait prints a normal result with `satisfied`
    # false, and a pointer sent into a TUI that is still starting is lost,
    # so that result gets one longer wait.
    waited=$(orca terminal wait --terminal "$term" --for tui-idle --timeout-ms 90000 --json 2>/dev/null)
    if [[ $(printf '%s' "$waited" | json 'd["result"]["wait"]["satisfied"]') == False ]]; then
      orca terminal wait --terminal "$term" --for tui-idle --timeout-ms 180000 --json >/dev/null 2>&1
    fi

    # Codex startup dialog: "Hooks need review", shown when a hook in
    # .codex/hooks.json or ~/.codex/hooks.json is new or changed. A prompt
    # sent into it is lost, and its Enter picks "Review hooks", which opens a
    # detail view. The answer is "Trust all and continue", picked by the
    # number its line carries rather than a fixed one, since the numbering
    # is not stable across versions: the digit moves the selection and Enter
    # confirms it. The update offer is off on the launch line rather than
    # answered, for the same reason: a wrong answer runs the upgrade and
    # leaves the terminal at a shell. An offer that shows anyway fails the
    # start before the brief pointer's Enter can pick an option.
    if [[ $cli == codex ]]; then
      for _ in 1 2 3 4; do
        sleep 2; s=$(screen "$term")
        grep -q 'restart Codex' <<<"$s" && undo "Codex updated itself and exited at startup for $lane"
        grep -q 'Skip until next version' <<<"$s" && undo "Codex showed its update offer despite check_for_update_on_startup=false for $lane"
        grep -q -i 'hooks need review' <<<"$s" || break
        n=$(sed -n 's/.*\([0-9]\)\. Trust all and continue.*/\1/p' <<<"$s" | head -1)
        [[ -n $n ]] || undo "Codex hooks review for $lane has no \"Trust all and continue\" option: $(tail -8 <<<"$s")"
        orca terminal send --terminal "$term" --text "$n" --json >/dev/null \
          || undo "cannot answer Codex hooks review for $lane"
        sleep 1
        orca terminal send --terminal "$term" --text '' --enter --json >/dev/null \
          || undo "cannot answer Codex hooks review for $lane"
      done
      # The last round's Enter needs the same redraw time as the others.
      sleep 2
      grep -q -i 'hooks need review' <<<"$(screen "$term")" && undo "$lane still shows the hooks review after four rounds"
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
    name=${1:-}; shift || true; until_cmd='' max=3600 stall=1200
    while (($#)); do
      case "$1" in
        --until) [[ $# -ge 2 ]] || die "--until requires a command"; until_cmd=$2; shift 2 ;;
        --max) [[ $# -ge 2 ]] || die "--max requires seconds"; max=$2; shift 2 ;;
        --stall) [[ $# -ge 2 ]] || die "--stall requires seconds"; stall=$2; shift 2 ;;
        --timeout) die "--timeout was replaced by --max" ;;
        *) die "unknown flag $1" ;;
      esac
    done
    [[ $max =~ ^[1-9][0-9]*$ ]] || die "--max must be a positive integer"
    [[ $stall =~ ^[0-9]+$ ]] || die "--stall must be a non-negative integer"
    term=$(field "$name" terminal); wt=$(field "$name" worktree); path=$(field "$name" path)
    [[ -n $term && -n $wt && -n $path ]] || die "no lane named $name; status lists them"
    # tui-idle alone is not the end of a turn: it was seen satisfied while
    # the agent was mid-turn, and the interrupt hint vanishes between tool
    # calls. The turn has ended when two reads five seconds apart show no
    # hint and the same screen. Child lanes use the same test and keep the
    # parent waiting while a child changes or shows a working hint.
    deadline=$(( $(date +%s) + max ))
    since=$(date +%s) last_lane='' show_screen=true outcome=''
    while :; do
      out=$(orca terminal wait --terminal "$term" --for tui-idle --timeout-ms 60000 --json 2>&1)
      st=$(printf '%s' "$out" | json 'd["result"]["wait"]["status"]')
      [[ $st == exited ]] && { outcome=exited; break; }

      child_names=() child_terms=() child_first=() child_readable=() child_ids=()
      if kids=$(children "$wt"); then
        if [[ -n $kids ]]; then
          while IFS= read -r child; do
            child_ids+=("$child")
          done <<<"$kids"
          for child in "${child_ids[@]}"; do
            info=$(child_info "$child")
            child_name=${info%%$'\t'*}
            child_term=${info#*$'\t'}
            child_names+=("$child_name")
            child_terms+=("$child_term")
            if [[ -n $child_term ]]; then
              if child_screen=$(screen "$child_term"); then
                child_first+=("$child_screen")
                child_readable+=(true)
              else
                child_first+=('')
                child_readable+=(false)
              fi
            else
              child_first+=('')
              child_readable+=(false)
            fi
          done
        fi
      else
        child_names+=(unreadable)
        child_terms+=('')
        child_first+=('')
        child_readable+=(false)
      fi

      # A terminal closed outside this script reads as nothing, twice, which
      # would otherwise pass for a settled screen.
      s1=$(screen "$term") || { outcome=exited; show_screen=false; break; }
      wait_sleep "$deadline" || { outcome=timeout; break; }
      s2=$(screen "$term") || { outcome=exited; show_screen=false; break; }

      child_active=false
      for i in "${!child_names[@]}"; do
        [[ ${child_readable[$i]} == true ]] || continue
        child_second=$(screen "${child_terms[$i]}") || continue
        if working "${child_first[$i]}" || working "$child_second" || [[ ${child_first[$i]} != "$child_second" ]]; then
          child_active=true
        fi
      done

      lane_idle=false
      if ! working "$s1" && ! working "$s2" && [[ $s1 == "$s2" ]]; then
        lane_idle=true
      fi
      now=$(date +%s)
      if (( now >= deadline )); then
        outcome=timeout
        break
      fi
      lane_changed=false
      if [[ $s1 != "$last_lane" ]]; then
        lane_changed=true
      elif [[ $s1 != "$s2" ]]; then
        lane_changed=true
      fi
      if [[ $lane_changed == true || $child_active == true ]]; then
        since=$now
      fi
      last_lane=$s1

      if [[ $lane_idle == true && $child_active == false ]]; then
        if [[ -n $until_cmd ]] && (cd "$path" && bash -c "$until_cmd" >/dev/null 2>&1); then
          outcome='done'
          break
        fi
        if ((${#child_names[@]} == 0)); then
          outcome=idle
          limited "$s2" && outcome=limited
          break
        fi
        if (( stall > 0 && now - since >= stall )); then
          outcome="idle-children ${child_names[*]}"
          break
        fi
      elif [[ $lane_idle == false && $child_active == false && $stall -gt 0 && $((now - since)) -ge $stall ]]; then
        outcome=stalled
        break
      fi

      wait_sleep "$deadline" || { outcome=timeout; break; }
    done
    echo "$outcome"
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
      if is_kept "$n"; then
        echo "$n $(field "$n" cli) kept $(field "$n" path)"
        continue
      fi
      if [[ -z $term ]]; then
        echo "$n $(field "$n" cli) unavailable-terminal $(field "$n" path)"
        continue
      fi
      s=$(screen "$term") || { echo "$n $(field "$n" cli) gone $(field "$n" path)"; continue; }
      if working "$s"; then st=working; else st=idle; fi
      echo "$n $(field "$n" cli) $st $(field "$n" path)"
    done
    ;;
  check)
    name=${1:-}
    [[ -n $name ]] || die "check SLUG"
    [[ -f "$state_dir/$name.json" ]] || die "no lane named $name; status lists them"
    model_check_lane "$name"
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
    if [[ $outcome == accepted || $outcome == amended ]]; then
      check_output=$(model_check_lane "$name" 2>&1) || die "${check_output#orca-worker: }"
      [[ -z $check_output ]] || echo "$check_output"
    fi
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
    name=${1:-}; [[ -n $name ]] || die "stop SLUG [--stalled] [--keep-worktree]"
    shift
    stalled=false keep=false
    while (($#)); do
      case "$1" in
        --stalled) stalled=true; shift ;;
        --keep-worktree) keep=true; shift ;;
        *) die "unknown flag $1" ;;
      esac
    done
    term=$(field "$name" terminal); wt=$(field "$name" worktree); path=$(field "$name" path)
    [[ -n $wt ]] || die "no lane named $name; status lists them"
    kept=false; is_kept "$name" && kept=true
    [[ $kept == false || $keep == false ]] || die "lane $name is already kept; stop it without --keep-worktree to remove the worktree"
    run_id=$(field "$name" run)
    [[ -n $run_id ]] || die "lane $name has no run; cannot verify grade"
    # -B: a bytecode cache beside runlog.py would leave the tree untracked.
    python3 -B -c 'import sys; sys.path.insert(0, sys.argv[1]); import runlog; sys.exit(0 if any(e.get("event") == "grade" and e.get("run") == sys.argv[2] for e in runlog.read()) else 1)' \
      "$script_dir" "$run_id" || die "lane $name has no grade event; grade it before stop"
    # Removing the worktree takes every lane's checkout with it.
    if [[ $keep == false ]] && other=$(live_lane_on "$wt" "$name"); then
      die "lane $other still has a live terminal in $path; stop it first, nothing removed"
    fi
    # A stalled lane shows the working hint over a turn that will not end.
    [[ $kept == true || $stalled == true ]] || ! working "$(screen "$term")" \
      || die "$name is still working; wait for it, or pass --stalled after wait printed stalled"
    if [[ $keep == false ]]; then
      kids=$(children "$wt") || die "cannot read the child worktrees of $name; nothing removed"
      if [[ -n $kids ]]; then
        kids=${kids//$'\n'/ }
        die "$name has child worktrees of its own; merge and remove them first, nothing removed: $kids"
      fi
    fi
    rm -f "$path/$brief_name" "$path/$note_name"
    dirty=$(git -C "$path" status --porcelain 2>/dev/null)
    [[ -z $dirty ]] || die "$path is dirty; nothing removed: $dirty"
    branch=$(field "$name" branch)
    if [[ $keep == false ]]; then
      # `orca worktree rm` deletes the branch with the checkout, so commits
      # that were not merged here would go with it. A parked lane's commits
      # stay on parked/<slug>, which the removal leaves alone.
      git merge-base --is-ancestor "$branch" HEAD 2>/dev/null \
        || git merge-base --is-ancestor "$branch" "refs/heads/parked/$name" 2>/dev/null \
        || die "$branch has commits that are not merged into $(git branch --show-current) or kept on parked/$name; merge it first, park it with 'git branch parked/$name $branch', or remove the lane by hand in Orca"
    fi
    head_sha=$(git rev-parse "$branch" 2>&1) || die "cannot resolve branch $branch: $head_sha"
    if [[ $kept == false ]]; then
      # `end` waits for the terminal to close, so the log never ends a run
      # whose worker still runs, and precedes the removal, so a failed removal
      # still leaves the run ended. A kept lane ran both when it was kept.
      orca terminal close --terminal "$term" --json >/dev/null 2>&1 \
        || die "terminal close failed for $name; nothing removed"
      end_out=$(python3 "$script_dir/runlog.py" end --run "$run_id" --head "$head_sha" 2>&1) \
        || die "run log end failed after $name's terminal closed: $end_out"
    fi
    if [[ $keep == true ]]; then
      python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); d["terminal"]=""; d["kept"]=True; json.dump(d, open(sys.argv[1],"w"))' "$state_dir/$name.json" \
        || die "cannot mark lane $name kept; its terminal is closed and the worktree stays"
      exit 0
    fi
    # A stalled worker may have started a lane after the first check.
    kids=$(children "$wt") || die "cannot read the child worktrees of $name after its terminal closed; worktree kept"
    if [[ -n $kids ]]; then
      kids=${kids//$'\n'/ }
      die "$name started child worktrees before its terminal closed; merge and remove them, then remove $name in Orca: $kids"
    fi
    out=$(orca worktree rm --worktree "id:$wt" --json 2>&1) || die "worktree rm refused: $out"
    # The removal ends every lane that named the worktree.
    for lane in $(lanes_on "$wt"); do
      rm -f "$state_dir/$lane.json" || die "cannot remove lane state for $lane"
    done
    ;;
  *) sed -n '2,25p' "$0" >&2; exit 2 ;;
esac
