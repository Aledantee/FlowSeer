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
# takes effort in the model id and opencode in the agent's model config.
if [[ -n "$effort" && ( "$cli" == agy || "$cli" == opencode ) ]]; then
  echo "--effort cannot be applied on $cli; put the level in the model id (agy) or the opencode agent" >&2
  exit 2
fi

raw="${out%.json}.raw"
prompt=$(cat "$brief")
start=$(date +%s)
cd "$dir" || exit 2
case "$cli" in
  claude)
    env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT claude -p "$prompt" --model "$model" \
      ${effort:+--effort "$effort"} --output-format json --dangerously-skip-permissions >"$raw" 2>"$raw.err" </dev/null ;;
  codex)
    # The user's global compound-engineering plugin runs its own review
    # workflow inside the lane; it turned a 7/7 run into 110 minutes on
    # 2026-09-09, so a lane measures the model without it.
    codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox \
      -c 'plugins."compound-engineering@compound-engineering-plugin".enabled=false' \
      -m "$model" ${effort:+-c model_reasoning_effort="$effort"} "$prompt" >"$raw" 2>"$raw.err" </dev/null ;;
  agy)
    # print mode returns partial output after 5 minutes unless told otherwise.
    agy -p "$prompt" --model "$model" --output-format json --dangerously-skip-permissions \
      --print-timeout 60m >"$raw" 2>"$raw.err" </dev/null ;;
  opencode)
    # `opencode run` never returns headless (1.18.30); the server API does.
    port=$((20000 + RANDOM % 20000))
    opencode serve --port "$port" >"$raw.serve" 2>&1 &
    spid=$!
    for _ in $(seq 1 30); do
      curl -sf -X POST "http://127.0.0.1:$port/session" -H 'content-type: application/json' -d '{}' \
        -o "$raw.session" && break
      sleep 1
    done
    sid=$(python3 -c "import json; print(json.load(open('$raw.session'))['id'])")
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
    python3 - "$port" "$sid" "$raw.steps" 2>>"$raw.err" <<'EOF'
import json, sys, urllib.request
port, sid, dest = sys.argv[1:]
get = lambda p: json.load(urllib.request.urlopen("http://127.0.0.1:%s%s" % (port, p), timeout=60))
out, todo = [], [sid]
while todo:
    s = todo.pop()
    out += [p for m in get("/session/%s/message" % s) for p in m["parts"] if p["type"] == "step-finish"]
    todo += [c["id"] for c in get("/session/%s/children" % s)]
json.dump(out, open(dest, "w"))
EOF
    kill "$spid" 2>/dev/null
    # code=$? below reads the last command of the branch, which is kill here.
    (exit "$rc") ;;
  *) echo "unknown cli $cli" >&2; exit 2 ;;
esac
code=$?
end=$(date +%s)

python3 - "$cli" "$raw" "$lane" "$model" "$effort" "$((end - start))" "$code" "$out" <<'EOF'
import json, sys
cli, raw, lane, model, effort, wall, code, out = sys.argv[1:]
usage = {"input": 0, "output": 0, "cache_read": 0, "reasoning": 0}
cost = None
text = open(raw, errors="replace").read()

def add(k, v):
    usage[k] += int(v or 0)

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
elif cli == "opencode":
    # Step-finish parts, not message infos: a message's info may hold only
    # its last step's tokens.
    try:
        steps = json.load(open(raw + ".steps"))
    except (OSError, json.JSONDecodeError):
        steps, usage = [], None
    for p in steps:
        t = p.get("tokens") or {}
        add("input", t.get("input")); add("output", t.get("output")); add("reasoning", t.get("reasoning"))
        add("cache_read", (t.get("cache") or {}).get("read"))
        if isinstance(p.get("cost"), (int, float)):
            cost = (cost or 0) + p["cost"]

json.dump({"lane": lane, "cli": cli, "model": model, "effort": effort or None,
           "wall_s": int(wall), "exit": int(code), "usage": usage, "cost_usd_reported": cost},
          open(out, "w"), indent=1)
print(open(out).read())
EOF
