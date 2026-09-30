#!/usr/bin/env python3
"""Check merge commits for changes silently dropped from either parent."""

import subprocess
import sys


def git(*args):
    return subprocess.run(
        ["git", *args], check=True, capture_output=True
    ).stdout


def decode(value):
    return value.decode("utf-8", errors="surrogateescape")


def nontrivial(line):
    return sum(not character.isspace() for character in line) > 2


def changed_paths(base, parent):
    output = git(
        "diff",
        "-M",
        "--find-renames",
        "--name-status",
        "-z",
        base,
        parent,
        "--",
    )
    fields = output.split(b"\0")
    changes = []
    index = 0
    while index < len(fields) - 1:
        status = decode(fields[index])
        index += 1
        if not status:
            continue
        if status[0] == "R":
            old_path = decode(fields[index])
            new_path = decode(fields[index + 1])
            index += 2
            changes.append(("renamed", old_path, new_path))
        else:
            path = decode(fields[index])
            index += 1
            changes.append((status[0], path))
    return changes


def changed_lines(base, parent, path):
    output = decode(
        git(
            "diff",
            "--no-ext-diff",
            "--no-color",
            "--no-renames",
            "--unified=0",
            base,
            parent,
            "--",
            path,
        )
    )
    added = set()
    removed = set()
    for line in output.splitlines():
        if line.startswith(("+++ ", "--- ")):
            continue
        if line.startswith("+") and nontrivial(line[1:]):
            added.add(line[1:])
        elif line.startswith("-") and nontrivial(line[1:]):
            removed.add(line[1:])
    return added, removed


def file_lines(commit, path):
    try:
        output = git("show", f"{commit}:{path}")
    except subprocess.CalledProcessError:
        return set()
    return {line for line in decode(output).splitlines() if nontrivial(line)}


def merge_parents(merge):
    fields = decode(git("rev-list", "--parents", "-n", "1", merge)).split()
    if len(fields) < 3:
        raise RuntimeError(f"{merge} is not a two-parent merge")
    return fields[1], fields[2]


def check_merge(merge):
    first, second = merge_parents(merge)
    base = decode(git("merge-base", first, second)).strip()
    changes = {
        "first": changed_paths(base, first),
        "second": changed_paths(base, second),
    }
    skipped = set()
    reports = []
    for side, side_changes in changes.items():
        for change in side_changes:
            if change[0] == "D":
                path = change[1]
                skipped.add(path)
                reports.append(
                    f"merge {merge[:12]}: not compared {side}-parent "
                    f"{path} (deleted)"
                )
            elif change[0] == "renamed":
                old_path, new_path = change[1:]
                skipped.update((old_path, new_path))
                reports.append(
                    f"merge {merge[:12]}: not compared {side}-parent "
                    f"{old_path} -> {new_path} (renamed)"
                )

    comparable = {"first": {}, "second": {}}
    for side, side_changes in changes.items():
        for change in side_changes:
            if change[0] in ("D", "renamed"):
                continue
            path = change[1]
            if path not in skipped:
                comparable[side][path] = True

    lost = False
    parents = {"first": first, "second": second}
    for path in sorted(
        set(comparable["first"]) | set(comparable["second"])
    ):
        merged_lines = file_lines(merge, path)
        for side in ("first", "second"):
            if path not in comparable[side]:
                continue
            added, removed = changed_lines(base, parents[side], path)
            if not added and not removed:
                continue
            has_added = bool(added & merged_lines)
            if not has_added and removed <= merged_lines:
                print(
                    f"merge {merge[:12]}: lost {side}-parent change in {path}"
                )
                lost = True
                continue
            missing = sorted(added - merged_lines)
            if missing and (has_added or bool(removed - merged_lines)):
                print(f"merge {merge[:12]}: missing {side}-parent change in {path}:")
                for line in missing:
                    print(f"  + {line}")

    for report in reports:
        print(report)
    return lost


def main(argv):
    if len(argv) != 1:
        print(__doc__, file=sys.stderr)
        return 2
    try:
        merges = decode(git("rev-list", "--merges", argv[0])).splitlines()
        if not merges:
            print("no merges")
            return 0
        lost = False
        for merge in merges:
            lost = check_merge(merge) or lost
    except (subprocess.CalledProcessError, RuntimeError) as error:
        if isinstance(error, subprocess.CalledProcessError):
            detail = decode(error.stderr).strip() if error.stderr else str(error)
        else:
            detail = str(error)
        print(detail, file=sys.stderr)
        return 2
    return 1 if lost else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
