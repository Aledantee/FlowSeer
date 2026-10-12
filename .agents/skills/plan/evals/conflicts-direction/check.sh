#!/usr/bin/env bash
# Usage: check.sh <lane> <result-text>
# Grades the lane with EVAL_BASE set to the fixture commit. Prints one PASS or FAIL line per check.
set -uo pipefail

lane=${1:?usage: check.sh <lane> <result-text>}
cd "$lane" || exit 1

pass() { echo "PASS $1"; }
fail() { echo "FAIL $1"; }

record_name="schema-building-blocks-direction"

# A lane may leave its work uncommitted, so new plans include untracked files.
new_plans=$( { git diff --name-only --diff-filter=A "$EVAL_BASE"; git ls-files --others --exclude-standard; } |
  grep -E '^docs/plans/.*-plan\.md$' | sort -u)

plan=""
if [ -n "$new_plans" ]; then
  plan=$(echo "$new_plans" | head -n 1)
fi

if [ -n "$plan" ] && [ -f "$plan" ] && grep -q "$record_name" "$plan"; then
  pass "plan-names-record"
else
  fail "plan-names-record"
fi

honored=0
if [ -n "$plan" ] && [ -f "$plan" ]; then
  state="${plan%.md}.state.json"
  if grep -E '^Files:' "$plan" | grep -q "$record_name"; then
    honored=1
  elif [ -f "$state" ] && [ "$(jq -r '.readiness' "$state" 2>/dev/null)" = "needs-decisions" ]; then
    honored=1
  elif grep -Eq '[a-z0-9]_bps\b' "$plan"; then
    honored=1
  fi
fi
if [ "$honored" -eq 1 ]; then
  pass "plan-honors-record"
else
  fail "plan-honors-record"
fi
