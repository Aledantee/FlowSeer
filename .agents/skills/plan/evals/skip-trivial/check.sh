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
  grep -E '^docs/plans/' | sort -u)

if [ -z "$new_plans" ]; then
  pass "no-new-plan"
else
  fail "no-new-plan"
fi

if echo "$changed" | grep -qx 'src/common/secret/README.md'; then
  pass "readme-changed"
else
  fail "readme-changed"
fi
