#!/usr/bin/env bash
# Print, as YAML on stdout, what this machine can route delegated work to:
# the agent CLIs present, which prepaid pools are signed in, what Orca can
# pin with --model, every pool's rate-limit windows, and the opencode model
# ids split by pool. Run unsandboxed: `orca` talks to a local socket, `agy`
# reads the keyring, and `opencode` writes a log file.

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
# The four prepaid pools, each read from the source that owns its numbers.
# Orca's `unavailable` rows for antigravity and opencodeGo say only that Orca
# cannot read them, so they are not used.
"$(dirname "$0")/../../delegate/scripts/pool-usage.sh" | sed 's/^/  /'

# opencode: zen is per-token and has no window; list the model ids by pool.
if have opencode; then
  opencode models 2>/dev/null | python3 -c '
import sys
go, zen = [], []
for line in sys.stdin:
    line = line.strip()
    if line.startswith("opencode-go/"): go.append(line.split("/", 1)[1])
    elif line.startswith("opencode/"): zen.append(line.split("/", 1)[1])
print("  zen: {signed_in: %s}" % str(bool(zen)).lower())
print("opencode_models:")
print("  go: [%s]" % ", ".join(go))
print("  zen: [%s]" % ", ".join(zen))
'
else
  echo "  zen: {signed_in: false}"
fi
