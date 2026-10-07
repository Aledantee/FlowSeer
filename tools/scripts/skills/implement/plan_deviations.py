"""List where a branch's diff and a plan's units disagree.

A session's own account of what it changed covers a fraction of what it
did and drifts toward the plan it was given, so the Finish step reads the
deviations off the tree instead. Usage:

    run.py implement plan-deviations PLAN BASE [-- PATH...]

PLAN is the plan file; its Files field under each `### U<n>` heading names
what the unit may touch. BASE is the ref the branch forked from: the diff
starts at its merge base with HEAD, so commits main gained since the fork
are not read as this branch's changes. It covers tracked changes and
untracked files alike, with renames split into a deletion and an addition
so a unit naming the old path sees it changed. Explicit paths limit it to
the task's own changes when the worktree holds others. `.claude/skills/`
is a symlink to `.agents/skills/`, and git reports only the second, so
entries and paths under the first are read as the second.

Plans write the field two ways, `Files: ...` and `- **Files:**` with the
paths on the following lines; the entries are comma separated,
backtick-quoted or bare, may run over several lines, and a later quoted
entry with no slash is a file in the directory of the entry before it,
unless that file is absent there and present at the tree root
(`CONCEPTS.md`). A
directory entry covers everything under it, `{a,b}` braces expand, and a
word with neither a slash nor a dot is a symbol named in an aside, not a
file. On a line with no backticks every entry is a full path, a
parenthesised aside (`(regenerated)`) is dropped, and an entry holding a
space is prose. A line that mixes both forms keeps only its quoted
entries. The plan itself and
`docs/plans/` are never a deviation.

Prints two lists and exits 0; the caller quotes them. Exits 2 on a plan
without units or a base git cannot resolve.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

from lib import plans, proc

FIELD = re.compile(r"^(?:-\s+)?\**([A-Z][a-z]+)\**:\**\s*(.*)$")
QUOTED = re.compile(r"`([^`]+)`")
BRACES = re.compile(r"\{([^{}]*)\}")
ASIDE = re.compile(r"\([^()]*\)")
COMMA = re.compile(r",(?![^{}]*\})")
SKILLS_LINK = ".claude/skills"
SKILLS_DIR = ".agents/skills"


def canonical(path: str) -> str:
    """The path as git reports it, read through the `.claude/skills` symlink."""
    path = path.removeprefix("./")
    if path == SKILLS_LINK or path.startswith(SKILLS_LINK + "/"):
        return SKILLS_DIR + path[len(SKILLS_LINK) :]
    return path


def entries(text: str) -> tuple[list[str], bool]:
    """The path entries on one line of a Files field, and whether they are quoted."""
    quoted = QUOTED.findall(text)
    if quoted:
        return quoted, True
    bare = (item.strip().removeprefix("- ").strip() for item in COMMA.split(ASIDE.sub("", text)))
    return [item for item in bare if item and not re.search(r"\s", item)], False


def expand(entry: str) -> list[str]:
    match = BRACES.search(entry)
    if not match:
        return [entry]
    head, tail = entry[: match.start()], entry[match.end() :]
    return [
        expanded
        for alternative in match.group(1).split(",")
        for expanded in expand(head + alternative.strip() + tail)
    ]


def units(plan: Path, root: Path | None = None, fork: str | None = None) -> dict[str, list[str]]:
    """Each unit's Files entries.

    ROOT, the tree root, tells a quoted root file from a sibling, and FORK,
    the commit the branch left from, does so for a sibling the work deleted.
    """
    result: dict[str, list[str]] = {}
    current = None
    in_files = False
    last_dir = ""
    for line in plan.read_text(encoding="utf-8").splitlines():
        heading = plans.UNIT.match(line)
        if heading:
            current = heading.group(1)
            result[current] = []
            in_files = False
            continue
        text = line
        field = FIELD.match(line.strip())
        if field and not line.startswith((" ", "\t")):
            in_files = field.group(1) == "Files"
            last_dir = ""
            text = field.group(2)
        if not (in_files and current):
            continue
        items, quoted = entries(text)
        for item in items:
            item = item.strip()
            is_dir = item.endswith("/")
            item = item.rstrip("/")
            if not item or item.lower() == "none":
                continue
            if "/" not in item and "." not in item and not is_dir:
                continue
            # A bare list writes every path in full, so a root file in it
            # (`AGENTS.md`) is not a sibling of the entry before it.
            if "/" not in item and last_dir and quoted and not root_file(root, last_dir, item, fork):
                item = f"{last_dir}/{item}"
            item = canonical(item)
            for expanded in expand(item):
                result[current].append(expanded)
            parent = item if is_dir else str(Path(item).parent)
            last_dir = parent if parent != "." else ""
    return result


def root_file(root: Path | None, last_dir: str, name: str, fork: str | None = None) -> bool:
    """Whether a quoted NAME after an entry in LAST_DIR is the tree-root file of that name.

    A plan quotes `CONCEPTS.md` after `src/a/a.go` as often as it quotes
    `a_test.go`, and only the tree tells the two apart. A file in neither
    place is a new sibling, the shorthand's usual use. A sibling the work
    deleted is gone from the tree but still in FORK.
    """
    if root is None or any(c in name for c in "{*"):
        return False
    if not (root / name).exists() or (root / last_dir / name).exists():
        return False
    if fork is None:
        return True
    in_fork = proc.run(["git", "cat-file", "-e", f"{fork}:{last_dir}/{name}"], cwd=root)
    return in_fork.code != 0


def covers(entry: str, path: str) -> bool:
    if "*" in entry:
        return Path(path).match(entry)
    return path == entry or path.startswith(entry + "/")


def git_lines(*args: str) -> list[str]:
    done = proc.run(["git", *args])
    if done.code != 0:
        print(done.stderr.strip(), file=sys.stderr)
        sys.exit(2)
    return [line for line in done.stdout.split("\n") if line]


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    plan = Path(argv[0])
    base = argv[1]
    paths = [canonical(p) for p in argv[2:] if p != "--"]
    if not plan.is_file():
        print(f"plan not found: {plan}", file=sys.stderr)
        return 2
    root = Path(git_lines("rev-parse", "--show-toplevel")[0])
    fork = git_lines("merge-base", base, "HEAD")[0]
    unit_files = units(plan, root, fork)
    if not any(unit_files.values()):
        print(f"no unit with a Files field in {plan}", file=sys.stderr)
        return 2
    changed = git_lines("diff", "--name-only", "--no-renames", fork, "--", *paths)
    changed += git_lines("ls-files", "--others", "--exclude-standard", "--", *paths)

    unnamed = [
        p
        for p in changed
        if not p.startswith("docs/plans/")
        and not any(covers(e, p) for entries in unit_files.values() for e in entries)
    ]
    untouched = {
        unit: [e for e in entries if not any(covers(e, p) for p in changed)]
        for unit, entries in unit_files.items()
    }

    print("Changed, named by no unit:")
    for p in unnamed:
        print(f"  {p}")
    if not unnamed:
        print("  none")
    print("Named by a unit, unchanged:")
    printed = False
    for unit, entries in untouched.items():
        for e in entries:
            print(f"  {unit}: {e}")
            printed = True
    if not printed:
        print("  none")
    return 0
