#!/usr/bin/env bash

set -uo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)
input=$(cat)
cwd=$(jq -r '.cwd // "."' <<<"$input")
root=$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null) || {
  printf '{}\n'
  exit 0
}

if output=$("$script_dir/contract-test.sh" 2>&1) && \
  output=$(cd "$root" && go test ./test/conformance 2>&1); then
  printf '{}\n'
  exit 0
fi

reason="Repository layout policy failed. Fix the violations before stopping:"$'\n'"$output"
if [ "$(jq -r '.stop_hook_active // false' <<<"$input")" = true ]; then
  jq -n --arg message "$reason" '{systemMessage:$message}'
else
  jq -n --arg reason "$reason" '{decision:"block",reason:$reason}'
fi
