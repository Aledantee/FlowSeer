#!/usr/bin/env bash

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required for the service OpenTelemetry integration tier." >&2
  exit 1
fi
if ! docker info >/dev/null 2>&1; then
  echo "Docker is installed but its daemon is unavailable." >&2
  exit 1
fi

artifact_dir=$(mktemp -d "${TMPDIR:-/tmp}/flowseer-service-otel.XXXXXX")
keep_artifacts=false
trap 'if [[ $keep_artifacts == false ]]; then rm -rf "$artifact_dir"; fi' EXIT

set +e
(
  while IFS='=' read -r name _; do
    [[ $name == OTEL_* ]] && unset "$name"
  done < <(env)
  export FLOWSEER_OTEL_TEST_ARTIFACT_DIR=$artifact_dir
  cd "$repo_root"
  GOFLAGS= go test -race -count=1 -short=false -tags=service_otel_integration \
    ./src/common/service/test/integration/...
)
test_rc=$?
set -e

if [[ $test_rc -eq 0 ]]; then
  exit 0
fi

set +e
grep -r -F -q -- 'flowseer-otel-artifact-secret' "$artifact_dir"
scrub_rc=$?
set -e
if [[ $scrub_rc -eq 0 ]]; then
  echo "Collector artifacts contained the synthetic secret sentinel and were removed." >&2
  exit "$test_rc"
fi
if [[ $scrub_rc -ne 1 ]]; then
  echo "Collector artifacts could not be scanned safely and were removed." >&2
  exit "$test_rc"
fi

keep_artifacts=true
echo "Collector failure artifacts retained at: $artifact_dir" >&2
exit "$test_rc"
