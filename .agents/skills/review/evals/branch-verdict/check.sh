#!/usr/bin/env bash
# Grade the branch-verdict lane: the checkpoints file holds a review line whose
# verdict is not accept, and no tracked file changed. The line format is
# `review: <verdict>`, as `verify ledger checkpoint` writes it
# (tools/scripts/verify/ledger.py, cmd_checkpoint).
#
# check.sh LANE RESULT_TEXT, run with the lane as cwd and EVAL_BASE set.

set -uo pipefail

lane=${1:?lane}
cd "$lane" || exit 2

checkpoints=$(git rev-parse --git-dir)/flowseer-checkpoints
verdict=''
if [[ -f $checkpoints ]]; then
  verdict=$(sed -n 's/^review: *//p' "$checkpoints" | tail -n 1)
fi
if [[ -n $verdict && $verdict != accept* ]]; then
  echo 'PASS verdict-not-accept'
else
  echo 'FAIL verdict-not-accept'
fi
if [[ -z $(git diff --name-only "${EVAL_BASE:?}") ]]; then echo 'PASS no-tracked-change'; else echo 'FAIL no-tracked-change'; fi
