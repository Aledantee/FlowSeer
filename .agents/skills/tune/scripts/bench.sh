#!/usr/bin/env bash
# Run one calibration lane: a brief through one CLI on one model, inside a
# worktree, recording wall time and the CLI's own usage report.
#
# bench.sh --lane NAME --cli claude|codex|agy|opencode --model ID [--effort LEVEL]
#          [--agent OPENCODE_AGENT] --brief FILE --dir WORKTREE --out FILE.json
#
# The raw CLI output lands beside --out as FILE.raw. Each CLI runs with its
# permission prompts disabled; the worktree is the isolation boundary, so
# never point --dir at a checkout you care about. Run unsandboxed.

set -uo pipefail

lane='' cli='' model='' effort='' agent='' brief='' dir='' out=''
while (($#)); do
  case "$1" in
    --lane) lane=$2; shift 2 ;;
    --cli) cli=$2; shift 2 ;;
    --model) model=$2; shift 2 ;;
    --effort) effort=$2; shift 2 ;;
    --agent) agent=$2; shift 2 ;;
    --brief) brief=$2; shift 2 ;;
    --dir) dir=$2; shift 2 ;;
    --out) out=$2; shift 2 ;;
    *) echo "unknown flag $1" >&2; exit 2 ;;
  esac
done
for v in lane cli model brief dir out; do
  [[ -n "${!v}" ]] || { echo "--$v is required" >&2; exit 2; }
done
# A lane recorded at an effort it never ran at is worse than no lane: agy
# takes effort in the model id, and opencode lanes have no effort setting.
if [[ -n "$effort" && ( "$cli" == agy || "$cli" == opencode ) ]]; then
  echo "--effort cannot be applied on $cli; agy takes the level in the model id, opencode has none" >&2
  exit 2
fi

raw="${out%.json}.raw"
prompt=$(cat "$brief")
start=$(date +%s)
cd "$dir" || exit 2
# Clear per-run artifacts before launch so a lane that never starts or
# fails early cannot parse stale raw output, session tokens, or SQLite state
# from an earlier run on the same output path.
rm -f "$raw" "$raw.err" "$raw.steps" "$raw.session" "$raw.serve" "$raw.body" "$raw.db"* "$out"
case "$cli" in
  claude)
    env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT claude -p "$prompt" --model "$model" \
      ${effort:+--effort "$effort"} --output-format json --dangerously-skip-permissions >"$raw" 2>"$raw.err" </dev/null ;;
  codex)
    # A globally installed compound-engineering plugin runs its own review
    # workflow inside the lane and can stretch a run past 100 minutes, so a
    # lane measures the model without it.
    codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox \
      -c 'plugins."compound-engineering@compound-engineering-plugin".enabled=false' \
      -m "$model" ${effort:+-c model_reasoning_effort="$effort"} "$prompt" >"$raw" 2>"$raw.err" </dev/null ;;
  agy)
    # print mode returns partial output after 5 minutes unless told otherwise.
    agy -p "$prompt" --model "$model" --output-format json --dangerously-skip-permissions \
      --print-timeout 60m >"$raw" 2>"$raw.err" </dev/null ;;
  opencode)
    # `opencode run` never returns headless (1.18.30); the server API does.
    #
    # One database per lane. Every `opencode serve` writes the same SQLite
    # file under ~/.local/share/opencode, and lanes running at once corrupt
    # each other through it (a server that never binds its port, or one that
    # dies on "Failed to execute statement"). OPENCODE_DB
    # moves only the database; credentials still come from auth.json.
    export OPENCODE_DB="$raw.db"
    port=$((20000 + RANDOM % 20000))
    opencode serve --port "$port" >"$raw.serve" 2>&1 &
    spid=$!
    for _ in $(seq 1 60); do
      curl -sf --max-time 5 -X POST "http://127.0.0.1:$port/session" -H 'content-type: application/json' -d '{}' \
        -o "$raw.session" && break
      kill -0 "$spid" 2>/dev/null || break
      sleep 1
    done
    sid=$(python3 -c "import json; print(json.load(open('$raw.session'))['id'])" 2>/dev/null)
    if [[ -z $sid ]]; then
      # A lane that never started must not report like a lane that ran and did
      # nothing: both leave an empty $raw, so say so here instead.
      echo "opencode serve never accepted a session on port $port" >"$raw.err"
      rc=1
    else
      python3 - "$prompt" "$agent" "$model" >"$raw.body" <<'EOF'
import json, sys
prompt, agent, model = sys.argv[1:]
provider, _, mid = model.partition("/")
body = {"model": {"providerID": provider, "modelID": mid}, "parts": [{"type": "text", "text": prompt}]}
if agent:
    body["agent"] = agent
print(json.dumps(body))
EOF
      curl -sS --max-time 3600 -X POST "http://127.0.0.1:$port/session/$sid/message" \
        -H 'content-type: application/json' --data-binary @"$raw.body" >"$raw" 2>"$raw.err"
      rc=$?
      # The reply carries only the final message's usage; a multi-step run
      # spends most of its tokens before it. Keep every step-finish part of
      # the session and of its child sessions (the task tool's subagents).
      # No file means the usage was not measured, which is not zero.
      rm -f "$raw.steps"
      python3 - "$port" "$sid" "$raw.steps" 2>>"$raw.err" <<'PY'
import json, sys, urllib.request
port, sid, dest = sys.argv[1:]
get = lambda p: json.load(urllib.request.urlopen("http://127.0.0.1:%s%s" % (port, p), timeout=60))
out, todo = [], [sid]
while todo:
    s = todo.pop()
    out += [p for m in get("/session/%s/message" % s) for p in m["parts"] if p["type"] == "step-finish"]
    todo += [c["id"] for c in get("/session/%s/children" % s)]
json.dump(out, open(dest, "w"))
PY
    fi
    kill "$spid" 2>/dev/null ;;
  *) echo "unknown cli $cli" >&2; exit 2 ;;
esac
# The opencode branch ends in `kill`, whose status says nothing about the run.
code=${rc-$?}
end=$(date +%s)

python3 - "$cli" "$raw" "$lane" "$model" "$effort" "$((end - start))" "$code" "$out" <<'EOF'
import json, sys
cli, raw, lane, model, effort, wall, code, out = sys.argv[1:]
usage = {"input": 0, "output": 0, "cache_read": 0, "reasoning": 0}
cost = error = finish = tools = None

def add(k, v):
    usage[k] += int(v or 0)

try:
    text = open(raw, errors="replace").read()
except OSError:
    text = None

if cli == "opencode":
    # Step-finish parts, not message infos: a message's info may hold only
    # its last step's tokens. Parse steps independently of the message response
    # so a failed final request preserves token and cost data from completed steps.
    try:
        steps = json.load(open(raw + ".steps"))
        if not isinstance(steps, list):
            raise ValueError("steps must be a list")
        for p in steps:
            t = p.get("tokens") or {}
            add("input", t.get("input")); add("output", t.get("output")); add("reasoning", t.get("reasoning"))
            add("cache_read", (t.get("cache") or {}).get("read"))
            if isinstance(p.get("cost"), (int, float)):
                cost = (cost or 0) + p["cost"]
    except (OSError, json.JSONDecodeError, ValueError):
        steps, usage = [], None
    if text:
        try:
            d = json.loads(text)
        except json.JSONDecodeError:
            d = {}
        if isinstance(d, dict) and "name" in d and "info" not in d:
            # The server answered with an error object instead of a message.
            error = "%s: %s" % (d["name"], (d.get("data") or {}).get("message", ""))
        info = d.get("info") if isinstance(d, dict) else None
        if isinstance(info, dict):
            finish = info.get("finish")
            parts = d.get("parts") or []
            tools = sum(1 for p in parts if p.get("type") == "tool")
elif text is None or (text == "" and int(code) != 0):
    usage = None
else:
    if cli == "claude":
        try:
            d = json.loads(text)
            u = d.get("usage", {})
            add("input", u.get("input_tokens")); add("output", u.get("output_tokens"))
            add("cache_read", u.get("cache_read_input_tokens"))
            add("reasoning", (u.get("output_tokens_details") or {}).get("thinking_tokens"))
            cost = d.get("total_cost_usd")
        except json.JSONDecodeError:
            pass
    elif cli == "codex":
        for line in text.splitlines():
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue
            if e.get("type") == "turn.completed":
                u = e.get("usage", {})
                add("input", u.get("input_tokens")); add("output", u.get("output_tokens"))
                add("cache_read", u.get("cached_input_tokens")); add("reasoning", u.get("reasoning_output_tokens"))
    elif cli == "agy":
        try:
            u = json.loads(text).get("usage", {})
            add("input", u.get("input_tokens")); add("output", u.get("output_tokens"))
            add("cache_read", u.get("cache_read_tokens")); add("reasoning", u.get("thinking_tokens"))
        except json.JSONDecodeError:
            pass

if error is None and int(code) != 0:
    try:
        error = open(raw + ".err", errors="replace").read().strip() or None
    except OSError:
        pass

result = {"lane": lane, "cli": cli, "model": model, "effort": effort or None,
          "wall_s": int(wall), "exit": int(code), "usage": usage, "cost_usd_reported": cost}
if finish is not None:
    result["finish"] = finish
if tools is not None:
    result["tool_calls"] = tools
if error is not None:
    result["error"] = error
json.dump(result, open(out, "w"), indent=1)
print(open(out).read())
EOF
