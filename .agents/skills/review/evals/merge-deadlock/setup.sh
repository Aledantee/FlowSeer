#!/usr/bin/env bash
# Commit the fixture: a cleanup of pump.Merge that removes the forwarder stop
# channel. Run in the lane, so the patch sits beside this script.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
git apply "$here/fixture.patch"
git add src/common/pump
git commit -q -m "refactor(pump): simplify Merge's forwarder shutdown

Forwarders already watch the merged pump's Stopped channel and context, so
the extra stop channel the coordinator closed on a source failure is
redundant. Drop it."
