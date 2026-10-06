"""Run a subprocess from an argument list."""

import subprocess
from collections.abc import Sequence
from pathlib import Path
from typing import NamedTuple


class Result(NamedTuple):
    code: int
    stdout: str
    stderr: str


def run(args: Sequence[str], *, cwd: Path | None = None, stdin: str | None = None) -> Result:
    """Run args without a shell, so no argument is ever parsed as shell syntax."""
    if isinstance(args, str):
        raise TypeError("args must be a list of arguments, not a string")
    done = subprocess.run(
        list(args), cwd=cwd, input=stdin, capture_output=True, text=True, check=False
    )
    return Result(done.returncode, done.stdout, done.stderr)
