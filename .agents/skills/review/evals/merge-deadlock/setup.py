# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Commit the fixture: a cleanup of pump.Merge that removes the forwarder stop channel.

Each removal must match exactly once, so a merge.go that has drifted fails the
setup and the runner marks the scenario stale.
"""

from pathlib import Path
import subprocess
import sys

REMOVALS = [
    "\tstopForwarders := make(chan struct{})\n",
    "\t\t\tselect {\n\t\t\tcase <-stopForwarders:\n\t\t\t\treturn nil\n\t\t\tdefault:\n\t\t\t}\n\n",
    "\t\t\tcase <-stopForwarders:\n\t\t\t\treturn nil\n",
    "\t\t\t\tclose(stopForwarders)\n",
]
MESSAGE = """refactor(pump): simplify Merge's forwarder shutdown

Forwarders already watch the merged pump's Stopped channel and context, so
the extra stop channel the coordinator closed on a source failure is
redundant. Drop it."""


def commit(message: str) -> None:
    subprocess.run(["git", "add", "src/common/pump"], check=True)
    subprocess.run(["git", "commit", "-q", "-m", message], check=True)


def main() -> int:
    path = Path("src/common/pump/merge.go")
    text = path.read_text()
    for removal in REMOVALS:
        if text.count(removal) != 1:
            sys.exit(f"{path}: expected one match for {removal!r}")
        text = text.replace(removal, "")
    path.write_text(text)
    commit(MESSAGE)
    return 0


if __name__ == "__main__":
    sys.exit(main())
