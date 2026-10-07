"""List the dirty and untracked source paths of a worktree with their hashes."""

import posixpath
import subprocess
from pathlib import Path

from lib import proc

# The file types a gate verifies. The marker hook's edit branch keeps its own
# list, which differs, and both stay as they are.
SUFFIXES = (".go", ".proto", ".ts", ".tsx", ".js", ".jsx", ".py", ".sh", ".md", ".json", ".yaml", ".yml")
NAMES = ("go.mod", "go.sum", "buf.lock")
ENCODING = "utf-8"
ERRORS = "surrogateescape"


def sort_key(line: str) -> bytes:
    """Order lines by their bytes, as `LC_ALL=C sort` does."""
    return line.encode(ENCODING, errors=ERRORS)


def listing(root: Path) -> list[str]:
    """Return one `<hash><TAB><path>` line per dirty or untracked path, sorted by bytes.

    A deleted path hashes as `deleted`. During a merge every incoming file is
    a staged change, and one whose content is the other side's blob is that
    branch's verified work, not an edit made here, so only a path the merge
    or a person changed beyond it stays listed.

    Raises RuntimeError when git cannot hash the paths it listed.
    """
    present, deleted = _changed(root)
    lines = [f"deleted\t{path}" for path in deleted]
    if present:
        hashed = _checked(root, "hash-object", "--stdin-paths", stdin="".join(f"{path}\n" for path in present))
        hashes = hashed.split()
        if len(hashes) != len(present):
            raise RuntimeError(f"git hash-object answered {len(hashes)} lines for {len(present)} paths")
        lines += [f"{digest}\t{path}" for digest, path in zip(hashes, present)]
    if _git(root, "rev-parse", "-q", "--verify", "MERGE_HEAD").code == 0:
        lines = list(set(lines) - _merge_blobs(root))
    return sorted(lines, key=sort_key)


def _changed(root: Path) -> tuple[list[str], list[str]]:
    status = _git(root, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all")
    present: list[str] = []
    deleted: list[str] = []
    for entry in status.stdout.split("\0") if status.code == 0 else []:
        path = entry[3:]
        if not (path.endswith(SUFFIXES) or posixpath.basename(path) in NAMES):
            continue
        (present if (root / path).is_file() else deleted).append(path)
    return present, deleted


def _merge_blobs(root: Path) -> set[str]:
    """Return `<hash><TAB><path>` for every blob of the merge's other side."""
    blobs = set()
    for line in _checked(root, "ls-tree", "-r", "MERGE_HEAD").split("\n"):
        meta, _, path = line.partition("\t")
        if path:
            blobs.add(f"{meta.split(' ')[2]}\t{path}")
    return blobs


def _checked(root: Path, *args: str, stdin: str | None = None) -> str:
    result = _git(root, *args, stdin=stdin)
    if result.code != 0:
        raise RuntimeError(f"git {args[0]} failed: {result.stderr.strip()}")
    return result.stdout


def _git(root: Path, *args: str, stdin: str | None = None) -> proc.Result:
    """Run git and decode its bytes untranslated.

    `proc.run` reads in text mode, which turns a carriage return inside a
    path into a line feed, and a path is any byte string but NUL.
    """
    done = subprocess.run(
        ["git", *args],
        cwd=root,
        input=None if stdin is None else stdin.encode(ENCODING, errors=ERRORS),
        capture_output=True,
        check=False,
    )
    return proc.Result(
        done.returncode,
        done.stdout.decode(ENCODING, errors=ERRORS),
        done.stderr.decode(ENCODING, errors=ERRORS),
    )
