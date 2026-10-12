#!/usr/bin/env bash
# Commit the fixture: a cleanup of pump.Merge that removes the forwarder stop
# channel. Each removal must match exactly once, so a merge.go that has
# drifted fails the setup and the runner marks the scenario stale.

set -euo pipefail

python3 - src/common/pump/merge.go <<'EOF'
import sys
from pathlib import Path

path = Path(sys.argv[1])
text = path.read_text()
removals = [
    "\tstopForwarders := make(chan struct{})\n",
    "\t\t\tselect {\n\t\t\tcase <-stopForwarders:\n\t\t\t\treturn nil\n\t\t\tdefault:\n\t\t\t}\n\n",
    "\t\t\tcase <-stopForwarders:\n\t\t\t\treturn nil\n",
    "\t\t\t\tclose(stopForwarders)\n",
]
for removal in removals:
    if text.count(removal) != 1:
        sys.exit(f"{path}: expected one match for {removal!r}")
    text = text.replace(removal, "")
path.write_text(text)
EOF
git add src/common/pump
git commit -q -m "refactor(pump): simplify Merge's forwarder shutdown

Forwarders already watch the merged pump's Stopped channel and context, so
the extra stop channel the coordinator closed on a source failure is
redundant. Drop it."
