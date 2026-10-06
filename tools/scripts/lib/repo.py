"""Locate the repository and its tracked files through git."""

from pathlib import Path

from lib import proc


def root(cwd: Path | None = None) -> Path:
    """Return the repository root that contains cwd."""
    result = proc.run(["git", "rev-parse", "--show-toplevel"], cwd=cwd)
    if result.code != 0:
        raise RuntimeError(f"not in a git repository: {result.stderr.strip()}")
    return Path(result.stdout.strip())


def tracked_files(path: Path | str, cwd: Path | None = None) -> list[Path]:
    """Return the tracked files under path, relative to the directory git ran in."""
    result = proc.run(["git", "ls-files", "-z", "--", str(path)], cwd=cwd)
    if result.code != 0:
        raise RuntimeError(f"git ls-files failed: {result.stderr.strip()}")
    return [Path(name) for name in result.stdout.split("\0") if name]
