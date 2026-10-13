# /// script
# requires-python = "==3.13.*"
# dependencies = []
# ///
"""Commit the fixture: a new pump helper whose loop bound is off by one against its commit message.

The Go sources sit beside this script as .txt files, as calibration's accept
files do, so no Go tool builds them in place.
"""

from pathlib import Path
import shutil
import subprocess
import sys

MESSAGE = """feat(pump): add Take, which receives at most n values from a pump

Take returns early when the data channel closes or the context ends, and
never returns more than n values."""


def commit(message: str) -> None:
    subprocess.run(["git", "add", "src/common/pump"], check=True)
    subprocess.run(["git", "commit", "-q", "-m", message], check=True)


def main() -> int:
    here = Path(__file__).resolve().parent
    shutil.copy(here / "take.go.txt", "src/common/pump/take.go")
    shutil.copy(here / "take_test.go.txt", "src/common/pump/take_test.go")
    commit(MESSAGE)
    return 0


if __name__ == "__main__":
    sys.exit(main())
