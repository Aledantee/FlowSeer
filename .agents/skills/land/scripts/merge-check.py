#!/usr/bin/env python3
"""Check merge commits for changes silently dropped from either parent.

For each nontrivial line, the checker compares counts in the merge base, a
parent, the other parent, and the merge. A parent addition is unique only for
the count above both the base and the other parent. A parent removal is unique
only for the fall below the base beyond the other parent's fall, and is kept
when the merge count falls below the other parent's count. A parent change is
lost only when it has a unique addition, none of that addition is kept beyond
the other parent's count, and no unique removal is kept.
"""

from collections import Counter
from functools import lru_cache
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


@lru_cache
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


@lru_cache(maxsize=128)
def tree_objects(commit):
    entries = []
    for entry in git("ls-tree", "-r", "-z", commit).split(b"\0"):
        if not entry:
            continue
        metadata, path = entry.split(b"\t", 1)
        _mode, object_type, object_id = metadata.split()
        if object_type == b"blob":
            entries.append((decode(path), object_id))
    return dict(entries)


def blob_contents(commit, paths):
    objects = tree_objects(commit)
    entries = [
        (path, objects[path]) for path in paths if path in objects
    ]
    if not entries:
        return {}

    batch = subprocess.run(
        ["git", "cat-file", "--batch"],
        check=True,
        capture_output=True,
        input=b"".join(object_id + b"\n" for _, object_id in entries),
    ).stdout
    files = {}
    offset = 0
    for path, _ in entries:
        header_end = batch.index(b"\n", offset)
        header = batch[offset:header_end].split()
        size = int(header[2])
        content_start = header_end + 1
        content_end = content_start + size
        files[path] = batch[content_start:content_end]
        offset = content_end + 1
    return files


def counts_from_contents(contents):
    return {
        path: Counter(
            line
            for line in decode(content).splitlines()
            if nontrivial(line)
        )
        for path, content in contents.items()
    }


@lru_cache(maxsize=256)
def file_counts_for_paths(commit, paths):
    if len(paths) == 1:
        path = paths[0]
        try:
            content = git("show", f"{commit}:{path}")
        except subprocess.CalledProcessError:
            return {}
        return counts_from_contents({path: content})
    return counts_from_contents(blob_contents(commit, paths))


@lru_cache
def diff_kinds(base, parent):
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
    )
    kinds = {}
    for field in output.split(b"\0"):
        if not field:
            continue
        parts = field.split(b"\t", 2)
        if len(parts) != 3:
            continue
        added, deleted, path = parts
        if added == b"-" and deleted == b"-":
            kinds[decode(path)] = "binary"
        elif added == b"0" and deleted == b"0":
            kinds[decode(path)] = "mode-only"
    return kinds


@lru_cache
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


@lru_cache
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
    paths = sorted(set(comparable["first"]) | set(comparable["second"]))
    kinds = {
        side: diff_kinds(base, parents[side]) for side in ("first", "second")
    }
    count_paths = tuple(
        path
        for path in paths
        if any(path not in kinds[side] for side in ("first", "second"))
    )
    counts = {
        commit: file_counts_for_paths(commit, count_paths)
        for commit in (base, first, second, merge)
    }
    for path in paths:
        merged_counts = counts[merge].get(path, Counter())
        base_counts = counts[base].get(path, Counter())
        for side in ("first", "second"):
            if path not in comparable[side]:
                continue
            kind = kinds[side].get(path)
            if kind:
                reports.append(
                    f"merge {merge[:12]}: not compared {side}-parent "
                    f"{path} ({kind})"
                )
                continue
            side_counts = counts[parents[side]].get(path, Counter())
            added = Counter(
                {
                    line: side_counts[line] - base_counts[line]
                    for line in set(base_counts) | set(side_counts)
                    if side_counts[line] > base_counts[line]
                }
            )
            removed = Counter(
                {
                    line: base_counts[line] - side_counts[line]
                    for line in set(base_counts) | set(side_counts)
                    if base_counts[line] > side_counts[line]
                }
            )
            if not added and not removed:
                continue
            other = parents["second" if side == "first" else "first"]
            other_counts = counts[other].get(path, Counter())
            unique_additions = Counter()
            unique_removals = Counter()
            for line in set(base_counts) | set(side_counts) | set(other_counts):
                base_count = base_counts[line]
                side_rise = max(0, side_counts[line] - base_count)
                other_rise = max(0, other_counts[line] - base_count)
                unique_count = max(0, side_rise - other_rise)
                if unique_count:
                    unique_additions[line] = unique_count
                side_fall = max(0, base_count - side_counts[line])
                other_fall = max(0, base_count - other_counts[line])
                removal_count = max(0, side_fall - other_fall)
                if removal_count:
                    unique_removals[line] = removal_count
            if not unique_additions:
                continue
            has_added = any(
                merged_counts[line] > other_counts[line]
                for line in unique_additions
            )
            has_removed = any(
                merged_counts[line] < other_counts[line]
                for line in unique_removals
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
                if merged_counts[line] <= other_counts[line]
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
