#!/usr/bin/env python3
"""Report test changes that weaken what the suite proves.

Coding agents pass the tests they can see, and the recorded ways of doing
so are deleting a test, skipping it, and rewriting the expected output.
Each is sometimes right in a repository that breaks APIs on purpose, so
this script reports rather than fails: the verifier prints the list, the
Finish step of `implement` quotes it with a reason per line, and a
reviewer checks the reasons. Usage:

    check-test-integrity.py BASE [-- PATH...]

Compares BASE with the working tree, limited to PATHs when given. Reports:
a deleted `_test.go` file; a `func Test…`, `Benchmark…`, `Fuzz…`, or
`Example…` whose declaration is removed and not added back; a `t.Skip`,
`t.Skipf`, or `t.SkipNow` call added; a file under a `testdata/` directory
that existed at BASE and is modified or deleted. Exits 0 with the list on
stdout, or nothing when there is none; exits 2 when git cannot diff.
"""

from __future__ import annotations

import re
import subprocess
import sys

DECL = re.compile(r"^[-+]func\s+(Test|Benchmark|Fuzz|Example)\w*")
SKIP = re.compile(r"^\+.*\bt\.(Skip|Skipf|SkipNow)\(")


def git(*args: str) -> str:
    proc = subprocess.run(["git", *args], capture_output=True, text=True)
    if proc.returncode != 0:
        print(proc.stderr.strip(), file=sys.stderr)
        sys.exit(2)
    return proc.stdout


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    base = argv[1]
    paths = [p for p in argv[2:] if p != "--"]
    findings: list[str] = []

    status = git("diff", "--name-status", base, "--", *paths)
    for line in status.splitlines():
        parts = line.split("\t")
        if len(parts) < 2:
            continue
        code, path = parts[0][0], parts[-1]
        if code == "D" and path.endswith("_test.go"):
            findings.append(f"deleted test file: {path}")
        if "/testdata/" in f"/{path}" and code in ("M", "D", "R"):
            what = "deleted" if code == "D" else "modified"
            findings.append(f"{what} existing testdata: {path}")

    diff = git("diff", "--unified=0", base, "--", *paths)
    current = ""
    removed: dict[str, set[str]] = {}
    added: dict[str, set[str]] = {}
    for line in diff.splitlines():
        if line.startswith("+++ "):
            current = line[4:].removeprefix("b/")
            continue
        if line.startswith("--- ") or not current.endswith("_test.go"):
            continue
        decl = DECL.match(line)
        if decl:
            name = line[1:].split("(")[0].removeprefix("func ").strip()
            (removed if line[0] == "-" else added).setdefault(current, set()).add(name)
        if SKIP.match(line):
            findings.append(f"skip added: {current}: {line[1:].strip()}")
    for path, names in removed.items():
        for name in sorted(names - added.get(path, set())):
            findings.append(f"removed test: {path}: {name}")

    for finding in findings:
        print(finding)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
