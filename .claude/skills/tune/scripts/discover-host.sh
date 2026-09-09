#!/usr/bin/env bash
# Print, as YAML on stdout, what this machine can route delegated work to:
# the agent CLIs present, which prepaid pools are signed in, what Orca can
# pin with --model, the opencode model ids split by pool, and the Claude
# rate-limit windows. Run unsandboxed: `orca` talks to a local socket.

set -uo pipefail

iso() { date -u +%Y-%m-%dT%H:%M:%SZ; }
have() { command -v "$1" >/dev/null 2>&1; }
ver() { "$1" --version 2>/dev/null | head -1 | tr -d '\n'; }

echo "generated: $(iso)"
echo "clis:"
for c in claude codex agy opencode; do
  if have "$c"; then
    echo "  $c: {path: $(command -v "$c"), version: \"$(ver "$c")\"}"
  else
    echo "  $c: null"
  fi
done

echo "orca:"
if have orca && orca status --json 2>/dev/null | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin)["result"]["runtime"]["reachable"] else 1)' 2>/dev/null; then
  echo "  reachable: true"
  pin=$(orca orchestration worker-start --help 2>/dev/null | sed -n 's/.*--model supports \(.*\) opaque provider model ids.*/\1/p' | tr -d ',' | tr 'A-Z' 'a-z' | sed 's/ and / /')
  echo "  model_pinnable: [${pin// /, }]"
else
  echo "  reachable: false"
fi

echo "pools:"
# Claude: Orca reports the OAuth rate-limit windows.
if have orca; then
  orca account list --json 2>/dev/null | python3 -c '
import json, sys
try:
    r = json.load(sys.stdin)["result"]
except Exception:
    print("  claude: {signed_in: null}"); print("  codex: {signed_in: null}"); sys.exit()
rl = r.get("rateLimits", {}).get("claude", {})
ok = rl.get("status") == "ok"
win = {k: v["usedPercent"] for k, v in rl.items() if isinstance(v, dict) and "usedPercent" in v}
print("  claude: {signed_in: %s, windows: %s}" % (str(ok).lower(), json.dumps(win)))
cx = r.get("codex", {}).get("systemDefault", {})
print("  codex: {signed_in: %s, windows: null}" % str(bool(cx.get("hasAuth"))).lower())
'
else
  echo "  claude: {signed_in: null}"
  echo "  codex: {signed_in: null}"
fi

# Google: `agy models` succeeds only with a live session.
if have agy && agy models >/dev/null 2>&1; then
  echo "  google: {signed_in: true, windows: null}"
else
  echo "  google: {signed_in: false}"
fi

# opencode: split the model list by provider prefix.
if have opencode; then
  opencode models 2>/dev/null | python3 -c '
import sys
go, zen = [], []
for line in sys.stdin:
    line = line.strip()
    if line.startswith("opencode-go/"): go.append(line.split("/", 1)[1])
    elif line.startswith("opencode/"): zen.append(line.split("/", 1)[1])
print("  go: {signed_in: %s, windows: null, models: [%s]}" % (str(bool(go)).lower(), ", ".join(go)))
print("  zen: {signed_in: %s, models: [%s]}" % (str(bool(zen)).lower(), ", ".join(zen)))
'
else
  echo "  go: {signed_in: false}"
  echo "  zen: {signed_in: false}"
fi
