#!/usr/bin/env bash
# Run one calibration lane: a brief through one CLI on one model, inside a
# worktree, recording wall time and the CLI's own usage report.
#
# bench.sh --lane NAME --cli claude|codex|agy|omp --model ID [--effort LEVEL]
#          --brief FILE --dir WORKTREE --out FILE.json
#
# The raw CLI output lands beside --out as FILE.raw. Each CLI runs with its
# permission prompts disabled; the worktree is the isolation boundary, so
# never point --dir at a checkout you care about. Run unsandboxed.

set -uo pipefail

lane='' cli='' model='' effort='' brief='' dir='' out=''
while (($#)); do
  case "$1" in
    --lane) lane=$2; shift 2 ;;
    --cli) cli=$2; shift 2 ;;
    --model) model=$2; shift 2 ;;
    --effort) effort=$2; shift 2 ;;
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
# takes effort in the model id, not as a flag.
if [[ -n "$effort" && "$cli" == agy ]]; then
  echo "--effort cannot be applied on $cli; agy takes the level in the model id" >&2
  exit 2
fi

raw="${out%.json}.raw"
prompt=$(cat "$brief")
start=$(date +%s)
cd "$dir" || exit 2
# Clear per-run artifacts before launch so a lane that never starts or
# fails early cannot parse stale raw output from an earlier run on the same
# output path.
rm -f "$raw" "$raw.err" "$out"
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
  omp)
    # The synthetic pool's CLI: it pins the model rather than rerouting, does
    # not hang headless, takes the effort level through --thinking, and reports
    # cost inline.
    omp -p --model "$model" ${effort:+--thinking "$effort"} --auto-approve \
      --mode json --no-session --no-title "$prompt" >"$raw" 2>"$raw.err" </dev/null ;;
  *) echo "unknown cli $cli" >&2; exit 2 ;;
esac
code=$?
end=$(date +%s)

python3 - "$cli" "$raw" "$lane" "$model" "$effort" "$((end - start))" "$code" "$out" <<'EOF'
import json, re, sys
cli, raw, lane, model, effort, wall, code, out = sys.argv[1:]
usage = {"input": 0, "output": 0, "cache_read": 0, "reasoning": 0}
cost = error = None
# served: the model ids the CLI reports actually ran the lane, so a silent
# downgrade (a safety reroute to another model, a fallback tier) is visible
# against the requested `model`. refused: the CLI declined the work rather
# than doing it, which a zero-usage exit-0 lane can hide. denied: the tool
# calls the lane was denied, kept apart from refused because this
# repository's own hooks deny edits too, and a repo guard is not the model
# declining.
served, refused, refuse_reason, denied = set(), False, None, None
REFUSAL_TEXT = re.compile(r"could not be submitted|sensitive words|prohibited|I can't help|I cannot help|I won't", re.I)

def add(k, v):
    usage[k] += int(v or 0)

try:
    text = open(raw, errors="replace").read()
except OSError:
    text = None

if text is None or (text == "" and int(code) != 0):
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
            # modelUsage keys and canonicalModel name the model that answered,
            # so a cyber reroute to another model shows up here.
            for mid, mu in (d.get("modelUsage") or {}).items():
                served.add(mid)
                if isinstance(mu, dict) and mu.get("canonicalModel"):
                    served.add(mu["canonicalModel"])
            if d.get("stop_reason") == "refusal" or d.get("subtype") == "refusal":
                refused, refuse_reason = True, "stop_reason=refusal"
            # Whether a hook's deny lands in `permission_denials` is
            # unverified, so a denial is recorded, not graded as a refusal.
            if d.get("permission_denials"):
                denied = sorted({p.get("tool_name", "?") for p in d["permission_denials"]})
        except json.JSONDecodeError:
            pass
    elif cli == "codex":
        for line in text.splitlines():
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue
            if isinstance(e, dict) and e.get("model"):
                served.add(e["model"])
            if e.get("type") == "turn.completed":
                u = e.get("usage", {})
                add("input", u.get("input_tokens")); add("output", u.get("output_tokens"))
                add("cache_read", u.get("cached_input_tokens")); add("reasoning", u.get("reasoning_output_tokens"))
    elif cli == "agy":
        try:
            j = json.loads(text)
            u = j.get("usage", {})
            add("input", u.get("input_tokens")); add("output", u.get("output_tokens"))
            add("cache_read", u.get("cache_read_tokens")); add("reasoning", u.get("thinking_tokens"))
            if j.get("model"):
                served.add(j["model"])
            # A prompt Google's filter rejects returns status SUCCESS, exit 0,
            # zero usage, and the refusal as `response` text in under 5 s, so
            # the exit code alone reports it as a clean run (evidence.md).
            resp = j.get("response") or ""
            if (str(j.get("status")).upper() == "SUCCESS" and not any(usage.values())
                    and REFUSAL_TEXT.search(resp)):
                refused, refuse_reason = True, "google filter: " + resp[:120]
        except json.JSONDecodeError:
            pass
    elif cli == "omp":
        # A JSON-lines stream; each assistant message_end carries usage with an
        # inline cost, the model that answered, and the stop reason.
        for line in text.splitlines():
            try:
                e = json.loads(line)
            except json.JSONDecodeError:
                continue
            if e.get("type") != "message_end":
                continue
            msg = e.get("message") or {}
            if msg.get("role") != "assistant":
                continue
            u = msg.get("usage") or {}
            add("input", u.get("input")); add("output", u.get("output"))
            add("cache_read", u.get("cacheRead")); add("reasoning", u.get("reasoning"))
            total = (u.get("cost") or {}).get("total")
            if isinstance(total, (int, float)):
                cost = (cost or 0) + total
            if msg.get("model"):
                served.add(msg["model"])
            if msg.get("stopReason") in ("refusal", "content_filter", "safety"):
                refused, refuse_reason = True, "stopReason=" + msg["stopReason"]

if error is None and int(code) != 0:
    try:
        error = open(raw + ".err", errors="replace").read().strip() or None
    except OSError:
        pass

# A safety reroute or fallback answers under a different model id. Compare on
# a base id so a dated Claude snapshot (`-YYYYMMDD`) is not read as a change.
# omp pins its model and does not reroute, so its downgrade check is left null.
def base_id(m):
    return re.sub(r"-\d{8}$", "", (m or "").rsplit("/", 1)[-1])

served_list = sorted(served)
# A reroute or fallback shows up as a foreign model in the served set, even
# when the requested model also answered some turns (the safety classifier
# reroutes only the triggering turns). Flag any foreign served model, and
# name them, since a partial reroute is the silent downgrade worth catching.
downgraded = None
served_foreign = None
if cli in ("claude", "codex", "agy") and served_list:
    want = base_id(model)
    foreign = sorted({s for s in served_list if base_id(s) != want})
    downgraded = bool(foreign)
    served_foreign = foreign or None

result = {"lane": lane, "cli": cli, "model": model, "effort": effort or None,
          "wall_s": int(wall), "exit": int(code), "usage": usage, "cost_usd_reported": cost,
          "served_model": served_list or None, "served_foreign": served_foreign,
          "downgraded": downgraded, "refused": refused, "refuse_reason": refuse_reason,
          "denied_tools": denied}
if error is not None:
    result["error"] = error
json.dump(result, open(out, "w"), indent=1)
print(open(out).read())
EOF
