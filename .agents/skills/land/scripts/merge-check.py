#!/usr/bin/env python3
"""Check merge commits for changes silently dropped from either parent.

For each nontrivial line, the checker compares counts in the merge base, a
parent, the other parent, and the merge. A parent addition is unique only for
the count above both the base and the other parent. A parent removal is unique
only for the fall below the base beyond the other parent's fall, and is kept
when the merge count falls below the other parent's count. A parent change is
lost when it has a unique addition or removal, no unique addition is kept
beyond the other parent's count, and no unique removal is kept. A nontrivial
file reset to the merge base is also reported as lost.
"""

from collections import Counter
from functools import lru_cache
import os
import subprocess
import sys


def git(*args):
    return subprocess.run(
        ["git", *args], check=True, capture_output=True
    ).stdout


def decode(value):
    return value.decode("utf-8", errors="surrogateescape")


@lru_cache
def empty_tree():
    return decode(git("hash-object", "-t", "tree", "/dev/null")).strip()


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
def parse_tree_entries(output):
    entries = {}
    for entry in output.split(b"\0"):
        if not entry:
            continue
        metadata, path = entry.split(b"\t", 1)
        mode, object_type, object_id = metadata.split()
        entries[decode(path)] = (
            decode(mode),
            decode(object_type),
            object_id,
        )
    return entries


@lru_cache(maxsize=128)
def tree_entries(commit):
    return parse_tree_entries(git("ls-tree", "-r", "-z", commit))


@lru_cache(maxsize=256)
def tree_entries_for_paths(commit, paths):
    if not paths:
        return {}
    return parse_tree_entries(
        git(
            "--literal-pathspecs",
            "ls-tree",
            "-r",
            "-z",
            commit,
            "--",
            *paths,
        )
    )


@lru_cache(maxsize=128)
def tree_objects(commit):
    return {
        path: object_id
        for path, (_mode, object_type, object_id) in tree_entries(commit).items()
        if object_type == "blob"
    }


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


@lru_cache(maxsize=4096)
def nontrivial_lines(content):
    return tuple(
        line
        for line in decode(content).splitlines()
        if nontrivial(line)
    )


def counts_from_contents(contents):
    return {
        path: Counter(nontrivial_lines(content))
        for path, content in contents.items()
    }


@lru_cache(maxsize=256)
def file_contents_for_paths(commit, paths):
    if len(paths) == 1:
        path = paths[0]
        try:
            content = git("show", f"{commit}:{path}")
        except subprocess.CalledProcessError:
            return {}
        return {path: content}
    return blob_contents(commit, paths)


@lru_cache(maxsize=256)
def file_counts_for_paths(commit, paths):
    return counts_from_contents(file_contents_for_paths(commit, paths))


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
    fields = []
    for field in output.split(b"\0"):
        if not field:
            continue
        parts = field.split(b"\t", 2)
        if len(parts) != 3:
            continue
        added, deleted, path = parts
        path = decode(path)
        fields.append((added, deleted, path))
    paths = tuple(path for _added, _deleted, path in fields)
    base_entries = tree_entries_for_paths(base, paths)
    parent_entries = tree_entries_for_paths(parent, paths)
    for added, deleted, path in fields:
        base_entry = base_entries.get(path)
        parent_entry = parent_entries.get(path)
        if (
            (base_entry and base_entry[1] == "commit")
            or (parent_entry and parent_entry[1] == "commit")
        ):
            kinds[path] = "submodule"
            continue
        if added == b"-" and deleted == b"-":
            kinds[path] = "binary"
        elif added == b"0" and deleted == b"0":
            if (
                base_entry
                and parent_entry
                and base_entry[0] != parent_entry[0]
            ):
                kinds[path] = "mode-only"
    return kinds


@lru_cache
def merge_parents(merge):
    fields = decode(git("rev-list", "--parents", "-n", "1", merge)).split()
    if len(fields) > 3:
        print(
            f"merge {merge[:12]}: not compared {len(fields) - 1} "
            "parents (octopus)"
        )
        return None
    if len(fields) != 3:
        raise RuntimeError(
            f"{merge} has {len(fields) - 1} parents; "
            "merge-check supports exactly two"
        )
    return fields[1], fields[2]


@lru_cache
def merge_bases(first, second):
    try:
        output = git("merge-base", "--all", first, second)
    except subprocess.CalledProcessError as error:
        if error.returncode == 1:
            return []
        raise
    return decode(output).splitlines()


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


def report_no_merge_base(merge, first, second):
    reports = set()
    empty_tree_id = empty_tree()
    for side, parent in (("first", first), ("second", second)):
        for change in changed_paths(empty_tree_id, parent):
            reports.add(
                f"merge {merge[:12]}: not compared {side}-parent "
                f"{changed_path_name(change)} (no merge base)"
            )
    for report in sorted(reports):
        print(report)


def check_merge(merge):
    parents = merge_parents(merge)
    if parents is None:
        return False
    first, second = parents
    bases = merge_bases(first, second)
    if len(bases) != 1:
        if len(bases) > 1:
            report_multiple_bases(merge, bases, first, second)
            return False
        report_no_merge_base(merge, first, second)
        return False
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
    contents = {
        commit: file_contents_for_paths(commit, count_paths)
        for commit in (base, first, second, merge)
    }
    objects = {
        commit: tree_entries_for_paths(commit, count_paths)
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
                changed_lines = (
                    nontrivial_lines(contents[parents[side]].get(path, b""))
                    != nontrivial_lines(contents[base].get(path, b""))
                )
            else:
                changed_lines = True
            base_entry = objects[base].get(path)
            merge_entry = objects[merge].get(path)
            side_entry = objects[parents[side]].get(path)
            if (
                base_entry
                and merge_entry
                and side_entry
                and merge_entry[2] == base_entry[2]
                and side_entry[2] != base_entry[2]
                and changed_lines
            ):
                print(
                    f"merge {merge[:12]}: lost {side}-parent change in {path}"
                )
                lost = True
                continue
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
            if not unique_additions and not unique_removals:
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
            if missing:
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
        repository_root = decode(git("rev-parse", "--show-toplevel")).strip()
        os.chdir(repository_root)
        merges = decode(git("rev-list", "--merges", argv[0])).splitlines()
        if not merges:
            print("no merges")
            return 0
        lost = False
        for merge in merges:
            lost = check_merge(merge) or lost
    except Exception as error:
        if isinstance(error, subprocess.CalledProcessError):
            detail = decode(error.stderr).strip() if error.stderr else str(error)
        else:
            detail = f"{type(error).__name__}: {error}"
        print(detail, file=sys.stderr)
        return 2
    return 1 if lost else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
