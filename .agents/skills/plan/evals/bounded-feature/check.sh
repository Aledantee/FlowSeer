#!/usr/bin/env bash
# Usage: check.sh <lane> <result-text>
# Grades the lane with EVAL_BASE set to the fixture commit. Prints one PASS or FAIL line per check.
set -uo pipefail

lane=${1:?usage: check.sh <lane> <result-text>}
cd "$lane" || exit 1

pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; }

# A lane may leave its work uncommitted, so changed paths include untracked files.
changed=$( { git diff --name-only "$EVAL_BASE"; git ls-files --others --exclude-standard; } | sort -u)
new_plans=$( { git diff --name-only --diff-filter=A "$EVAL_BASE"; git ls-files --others --exclude-standard; } |
  grep -E '^docs/plans/.*-plan\.md$' | sort -u)

plan=""
state=""
if [ -n "$new_plans" ] && [ "$(echo "$new_plans" | wc -l)" -eq 1 ]; then
  plan=$new_plans
  state="${plan%.md}.state.json"
  pass "one-new-plan"
else
  fail "one-new-plan"
fi

if [ -n "$plan" ] && [ -f "$plan" ] && grep -qx 'artifact_contract: flowseer-plan/v2' "$plan"; then
  pass "artifact-contract"
else
  fail "artifact-contract"
fi

if [ -n "$state" ] && [ -f "$state" ] && [ "$(jq -r '.status' "$state" 2>/dev/null)" = "planned" ]; then
  pass "state-planned"
else
  fail "state-planned"
fi

if [ -n "$plan" ] && [ -f "$plan" ] && grep -q '^### U1\.' "$plan"; then
  pass "unit-u1"
else
  fail "unit-u1"
fi

# Every "### U<n>." section must hold a Files: line and an After: line.
if [ -n "$plan" ] && [ -f "$plan" ] && awk '
  function close_unit() {
    if (in_unit) {
      units++
      if (!(files && after)) bad = 1
    }
  }
  /^##+ / { close_unit(); in_unit = 0 }
  /^### U[0-9]+\./ { in_unit = 1; files = 0; after = 0; next }
  in_unit && /^Files:/ { files = 1 }
  in_unit && /^After:/ { after = 1 }
  END { close_unit(); exit (units > 0 && !bad) ? 0 : 1 }
' "$plan"; then
  pass "files-and-after-per-unit"
else
  fail "files-and-after-per-unit"
fi

if [ -n "$plan" ] && [ -f "$plan" ] && grep -q '^Waves:' "$plan"; then
  pass "waves-line"
else
  fail "waves-line"
fi

if [ -n "$plan" ] && [ -f "$plan" ] && uv run tools/scripts/run.py verify check-prose "$plan" >/dev/null 2>&1; then
  pass "check-prose-clean"
else
  fail "check-prose-clean"
fi

group=""
if [ -n "$plan" ]; then
  group=$(uv run tools/scripts/run.py next plan-queue --json 2>/dev/null |
    jq -r --arg p "$plan" '.[] | select(.path == $p) | .group' 2>/dev/null)
fi
if [ "$group" = "ready" ] || [ "$group" = "replan" ]; then
  pass "plan-queue-ready"
else
  fail "plan-queue-ready"
fi

outside=$(echo "$changed" | grep -v -e '^$' -e '^docs/plans/')
if [ -n "$changed" ] && [ -z "$outside" ]; then
  pass "changes-only-under-docs-plans"
else
  fail "changes-only-under-docs-plans"
fi
