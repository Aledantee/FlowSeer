#!/usr/bin/env bash
# Commit the fixture: a new pump helper whose loop bound is off by one against
# its commit message. Run in the lane, so the patch sits beside this script.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
git apply "$here/fixture.patch"
git add src/common/pump
git commit -q -m "feat(pump): add Take, which receives at most n values from a pump

Take returns early when the data channel closes or the context ends, and
never returns more than n values."
