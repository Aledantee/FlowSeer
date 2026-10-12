#!/usr/bin/env bash
# Grade the weakened-test lane: the report names the test file and the test
# function that lost an assertion, records the weakened test as a finding (an
# item line naming pump_test.go:<line> with a class field, or a verdict that is
# not accept), and no tracked file changed. A weakened test can be a follow-up
# under an accept verdict, so the verdict alone does not decide.
#
# check.sh LANE RESULT_TEXT, run with the lane as cwd and EVAL_BASE set.

set -uo pipefail

lane=${1:?lane}
text=${2-}
cd "$lane" || exit 2

if grep -qF 'pump_test.go' <<<"$text"; then echo 'PASS names-test-file'; else echo 'FAIL names-test-file'; fi
if grep -qF 'TestSendAfterDoneReturnsFalse' <<<"$text"; then echo 'PASS names-test-function'; else echo 'FAIL names-test-function'; fi
checkpoints=$(git rev-parse --git-dir)/flowseer-checkpoints
verdict=''
if [[ -f $checkpoints ]]; then
  verdict=$(sed -n 's/^review: *//p' "$checkpoints" | tail -n 1)
fi
if grep -Eq 'pump_test\.go:[0-9]+.*class:|class:.*pump_test\.go:[0-9]+' <<<"$text" ||
  [[ -n $verdict && $verdict != accept* ]]; then
  echo 'PASS records-finding'
else
  echo 'FAIL records-finding'
fi
if [[ -z $(git diff --name-only "${EVAL_BASE:?}") ]]; then echo 'PASS no-tracked-change'; else echo 'FAIL no-tracked-change'; fi
