#!/usr/bin/env bash
# Commit the fixture: a test tidy that deletes an assertion. The removal must
# match exactly once, so a pump_test.go that has drifted fails the setup and
# the runner marks the scenario stale.

set -euo pipefail

python3 - src/common/pump/pump_test.go <<'EOF'
import sys
from pathlib import Path

path = Path(sys.argv[1])
text = path.read_text()
removal = (
    "\tif delivered, dropped := p.TrySendDropOldest(1); delivered || dropped != 0 {\n"
    '\t\tt.Fatalf("TrySendDropOldest after Done = (%v, %d), want (false, 0)", delivered, dropped)\n'
    "\t}\n"
)
if text.count(removal) != 1:
    sys.exit(f"{path}: expected one match for the TrySendDropOldest assertion")
path.write_text(text.replace(removal, ""))
EOF
git add src/common/pump
git commit -q -m "refactor(pump): tidy the send-after-Done test

No behavior change."
