#!/usr/bin/env bash

set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required for the service OpenTelemetry integration tier." >&2
  exit 1
fi
# A daemon that stops answering leaves `docker info` blocked forever, and
# the verifier's --full run with it; macOS has no `timeout`, so the probe is
# bounded by hand. The knob exists so the hook tests can hit the bound in
# under a second.
docker_probe_timeout=${FLOWSEER_DOCKER_PROBE_TIMEOUT:-20}
if [[ ! $docker_probe_timeout =~ ^[0-9]+$ ]]; then
  echo "FLOWSEER_DOCKER_PROBE_TIMEOUT must be a whole number of seconds." >&2
  exit 1
fi
docker info >/dev/null 2>&1 &
docker_probe=$!
for ((tick = 0; tick < docker_probe_timeout * 10; tick++)); do
  kill -0 "$docker_probe" 2>/dev/null || break
  sleep 0.1
done
if kill -0 "$docker_probe" 2>/dev/null; then
  # The wait after the kill must not become the hang it replaces, so a
  # probe that ignores TERM for a second is killed outright.
  kill "$docker_probe" 2>/dev/null
  for ((tick = 0; tick < 10; tick++)); do
    kill -0 "$docker_probe" 2>/dev/null || break
    sleep 0.1
  done
  # A probe that honoured TERM is already gone, and a kill of a dead pid
  # fails; under set -e that exit would replace the message below.
  kill -9 "$docker_probe" 2>/dev/null || true
  wait "$docker_probe" 2>/dev/null || true
  echo "Docker daemon did not answer 'docker info' within ${docker_probe_timeout}s." >&2
  exit 1
fi
if ! wait "$docker_probe"; then
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
  GOFLAGS='' go test -race -count=1 -short=false -tags=service_otel_integration \
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
