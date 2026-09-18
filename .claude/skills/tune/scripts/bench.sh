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

lane= cli= model= effort= agent= brief= dir= out=
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

raw="${out%.json}.raw"
prompt=$(cat "$brief")
start=$(date +%s)
cd "$dir" || exit 2
case "$cli" in
  claude)
    env -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT claude -p "$prompt" --model "$model" \
      ${effort:+--effort "$effort"} --output-format json --dangerously-skip-permissions >"$raw" 2>"$raw.err" </dev/null ;;
  codex)
    codex exec --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox \
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
    kill "$spid" 2>/dev/null ;;
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
    for line in text.splitlines():
        try:
            e = json.loads(line)
        except json.JSONDecodeError:
            continue
        t = e.get("tokens") or (e.get("part") or {}).get("tokens") or (e.get("info") or {}).get("tokens")
        if isinstance(t, dict):
            add("input", t.get("input")); add("output", t.get("output")); add("reasoning", t.get("reasoning"))
            add("cache_read", (t.get("cache") or {}).get("read"))
        c = e.get("cost") or (e.get("info") or {}).get("cost")
        if isinstance(c, (int, float)):
            cost = (cost or 0) + c

json.dump({"lane": lane, "cli": cli, "model": model, "effort": effort or None,
           "wall_s": int(wall), "exit": int(code), "usage": usage, "cost_usd_reported": cost},
          open(out, "w"), indent=1)
print(open(out).read())
EOF
