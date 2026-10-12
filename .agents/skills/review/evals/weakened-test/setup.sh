#!/usr/bin/env bash
# Commit the fixture: a test tidy that deletes an assertion. Run in the lane,
# so the patch sits beside this script.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
git apply "$here/fixture.patch"
git add src/common/pump
git commit -q -m "refactor(pump): tidy the send-after-Done test

No behavior change."
