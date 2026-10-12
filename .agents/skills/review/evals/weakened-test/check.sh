#!/usr/bin/env bash
# Grade the weakened-test lane: the report names the test file and the test
# function that lost an assertion, and no tracked file changed.
#
# check.sh LANE RESULT_TEXT, run with the lane as cwd and EVAL_BASE set.

set -uo pipefail

lane=${1:?lane}
text=${2-}
cd "$lane" || exit 2

if grep -qF 'pump_test.go' <<<"$text"; then echo 'PASS names-test-file'; else echo 'FAIL names-test-file'; fi
if grep -qF 'TestSendAfterDoneReturnsFalse' <<<"$text"; then echo 'PASS names-test-function'; else echo 'FAIL names-test-function'; fi
if [[ -z $(git diff --name-only "${EVAL_BASE:?}") ]]; then echo 'PASS no-tracked-change'; else echo 'FAIL no-tracked-change'; fi
