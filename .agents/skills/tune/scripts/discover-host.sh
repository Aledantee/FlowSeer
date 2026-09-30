#!/usr/bin/env bash
# Print, as YAML on stdout, what this machine can route delegated work to:
# the agent CLIs present, which prepaid pools are signed in, what Orca can
# pin with --model, every pool's rate-limit windows, and the synthetic model
# ids omp serves. Run unsandboxed: `orca` talks to a local socket, `agy`
# reads the keyring, and `omp` reads its config.

set -uo pipefail

iso() { date -u +%Y-%m-%dT%H:%M:%SZ; }
have() { command -v "$1" >/dev/null 2>&1; }
ver() { "$1" --version 2>/dev/null | head -1 | tr -d '\n'; }

echo "generated: $(iso)"
echo "clis:"
for c in claude codex agy omp; do
  if have "$c"; then
    echo "  $c: {path: $(command -v "$c"), version: \"$(ver "$c")\"}"
  else
    echo "  $c: null"
  fi
done

echo "orca:"
if have orca && orca status --json 2>/dev/null | python3 -c 'import json,sys; sys.exit(0 if json.load(sys.stdin)["result"]["runtime"]["reachable"] else 1)' 2>/dev/null; then
  echo "  reachable: true"
  pin=$(orca orchestration worker-start --help 2>/dev/null | sed -n 's/.*--model supports \(.*\) opaque provider model ids.*/\1/p' | tr -d ',' | tr '[:upper:]' '[:lower:]' | sed 's/ and / /')
  echo "  model_pinnable: [${pin// /, }]"
else
  echo "  reachable: false"
fi

echo "pools:"
# The prepaid pools, each read from the source that owns its numbers.
# Orca's `unavailable` row for antigravity says only that Orca cannot read
# it, so it is not used.
"$(dirname "$0")/../../delegate/scripts/pool-usage.sh" | sed 's/^/  /'

# omp serves the synthetic pool by pinning an id with --model; list the
# synthetic ids it offers, without the `synthetic/` selector prefix. The
# catalogue can keep ids Synthetic has stopped serving, and Synthetic answers
# requests for some of those without an error, so its served list is
# GET api.synthetic.new/openai/v1/models.
if have omp; then
  omp models synthetic --json 2>/dev/null | python3 -c '
import json, sys
try:
    models = json.load(sys.stdin).get("models", [])
except (ValueError, AttributeError):
    models = []
ids = [m["id"] for m in models if m.get("id")]
print("omp_models:")
print("  synthetic: [%s]" % ", ".join("\"%s\"" % m for m in ids))
'
else
  echo "omp_models: {synthetic: []}"
fi
