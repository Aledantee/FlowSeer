#!/usr/bin/env bash
# Commit the fixture: a new pump helper whose loop bound is off by one against
# its commit message. The Go sources sit beside this script as .txt files, as
# calibration's accept files do, so no Go tool builds them in place.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
cp "$here/take.go.txt" src/common/pump/take.go
cp "$here/take_test.go.txt" src/common/pump/take_test.go
git add src/common/pump
git commit -q -m "feat(pump): add Take, which receives at most n values from a pump

Take returns early when the data channel closes or the context ends, and
never returns more than n values."
