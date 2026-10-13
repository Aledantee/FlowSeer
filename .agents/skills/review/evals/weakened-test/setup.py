# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Commit the fixture: a test tidy that deletes an assertion.

The removal must match exactly once, so a pump_test.go that has drifted fails
the setup and the runner marks the scenario stale.
"""

from pathlib import Path
import subprocess
import sys


def commit(message: str) -> None:
    subprocess.run(["git", "add", "src/common/pump"], check=True)
    subprocess.run(["git", "commit", "-q", "-m", message], check=True)


path = Path("src/common/pump/pump_test.go")
text = path.read_text()
removal = (
    "\tif delivered, dropped := p.TrySendDropOldest(1); delivered || dropped != 0 {\n"
    '\t\tt.Fatalf("TrySendDropOldest after Done = (%v, %d), want (false, 0)", delivered, dropped)\n'
    "\t}\n"
)
if text.count(removal) != 1:
    sys.exit(f"{path}: expected one match for the TrySendDropOldest assertion")
path.write_text(text.replace(removal, ""))
commit("""refactor(pump): tidy the send-after-Done test

No behavior change.""")
