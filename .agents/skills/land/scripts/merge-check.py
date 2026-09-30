#!/usr/bin/env python3
"""Check merge commits for changes silently dropped from either parent.

For each nontrivial line, the checker compares counts in the merge base, a
parent, the other parent, and the merge. A parent addition is unique only for
the count above both the base and the other parent. A parent removal is kept
when the merge count falls below the base count. The parent change is lost
only when no unique addition is kept and no removal is kept.
"""

from collections import Counter
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
            "--literal-pathspecs",
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
    added = Counter()
    removed = Counter()
    in_hunk = False
    for line in output.splitlines():
        if line.startswith("@@"):
            in_hunk = True
            continue
        if not in_hunk:
            continue
        if line.startswith("+") and nontrivial(line[1:]):
            added[line[1:]] += 1
        elif line.startswith("-") and nontrivial(line[1:]):
            removed[line[1:]] += 1
    return added, removed


def file_counts(commit, path):
    try:
        output = git("show", f"{commit}:{path}")
    except subprocess.CalledProcessError:
        return Counter()
    return Counter(
        line
        for line in decode(output).splitlines()
        if nontrivial(line)
    )


def diff_kind(base, parent, path):
    output = git(
        "--literal-pathspecs",
        "diff",
        "--no-ext-diff",
        "--no-color",
        "--no-renames",
        "--numstat",
        "-z",
        base,
        parent,
        "--",
        path,
    )
    fields = output.split(b"\0")
    for field in fields:
        if not field:
            continue
        parts = field.split(b"\t", 2)
        if len(parts) != 3:
            continue
        added, deleted, _ = parts
        if added == b"-" and deleted == b"-":
            return "binary"
        if added == b"0" and deleted == b"0":
            summary = git(
                "--literal-pathspecs",
                "diff",
                "--no-ext-diff",
                "--no-color",
                "--no-renames",
                "--summary",
                base,
                parent,
                "--",
                path,
            )
            if summary:
                return "mode-only"
    return None


def merge_parents(merge):
    fields = decode(git("rev-list", "--parents", "-n", "1", merge)).split()
    if len(fields) > 3:
        raise RuntimeError(
            f"{merge} has more than two parents ({len(fields) - 1})"
        )
    if len(fields) != 3:
        raise RuntimeError(
            f"{merge} has {len(fields) - 1} parents; "
            "merge-check supports exactly two"
        )
    return fields[1], fields[2]


def merge_bases(first, second):
    return decode(git("merge-base", "--all", first, second)).splitlines()


def changed_path_name(change):
    if change[0] == "renamed":
        return f"{change[1]} -> {change[2]}"
    return change[1]


def report_multiple_bases(merge, bases, first, second):
    reports = set()
    for side, parent in (("first", first), ("second", second)):
        for base in bases:
            for change in changed_paths(base, parent):
                reports.add(
                    f"merge {merge[:12]}: not compared {side}-parent "
                    f"{changed_path_name(change)} (multiple merge bases)"
                )
    for report in sorted(reports):
        print(report)


def check_merge(merge):
    first, second = merge_parents(merge)
    bases = merge_bases(first, second)
    if len(bases) != 1:
        if len(bases) > 1:
            report_multiple_bases(merge, bases, first, second)
            return False
        raise RuntimeError(f"{merge} has no merge base")
    base = bases[0]
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
        merged_counts = file_counts(merge, path)
        for side in ("first", "second"):
            if path not in comparable[side]:
                continue
            kind = diff_kind(base, parents[side], path)
            if kind:
                reports.append(
                    f"merge {merge[:12]}: not compared {side}-parent "
                    f"{path} ({kind})"
                )
                continue
            added, removed = changed_lines(base, parents[side], path)
            if not added and not removed:
                continue
            other = parents["second" if side == "first" else "first"]
            base_counts = file_counts(base, path)
            side_counts = file_counts(parents[side], path)
            other_counts = file_counts(other, path)
            unique_additions = Counter()
            removals = Counter()
            for line in set(base_counts) | set(side_counts) | set(other_counts):
                base_count = base_counts[line]
                side_rise = max(0, side_counts[line] - base_count)
                other_rise = max(0, other_counts[line] - base_count)
                unique_count = max(0, side_rise - other_rise)
                if unique_count:
                    unique_additions[line] = unique_count
                removal_count = max(0, base_count - side_counts[line])
                if removal_count:
                    removals[line] = removal_count
            if not unique_additions and not removals:
                continue
            has_added = any(
                merged_counts[line] > max(base_counts[line], other_counts[line])
                for line in unique_additions
            )
            has_removed = any(
                merged_counts[line] < base_counts[line] for line in removals
            )
            if not has_added and not has_removed:
                print(
                    f"merge {merge[:12]}: lost {side}-parent change in {path}"
                )
                lost = True
                continue
            missing = sorted(
                line
                for line in unique_additions
                if merged_counts[line]
                <= max(base_counts[line], other_counts[line])
            )
            if missing and (has_added or has_removed):
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
