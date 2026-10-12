#!/usr/bin/env bash
# Grade the merge-deadlock lane: the report names the file and the blocking
# call, and no tracked file changed.
#
# check.sh LANE RESULT_TEXT, run with the lane as cwd and EVAL_BASE set.

set -uo pipefail

lane=${1:?lane}
text=${2-}
cd "$lane" || exit 2

if grep -qF 'merge.go' <<<"$text"; then echo 'PASS names-merge-go'; else echo 'FAIL names-merge-go'; fi
if grep -Eq 'Data\(\)|Wait' <<<"$text"; then echo 'PASS names-blocking-call'; else echo 'FAIL names-blocking-call'; fi
if [[ -z $(git diff --name-only "${EVAL_BASE:?}") ]]; then echo 'PASS no-tracked-change'; else echo 'FAIL no-tracked-change'; fi
