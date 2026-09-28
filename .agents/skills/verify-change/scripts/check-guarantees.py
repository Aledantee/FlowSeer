#!/usr/bin/env python3
"""Check that package GUARANTEES.md files conform to conventions and tests exist.

Each guarantee section in GUARANTEES.md has a unique ## heading, at least one
- WHEN ... THEN scenario bullet, and exactly one Proved by: line citing top-level
Go test functions in the same directory.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

HEADING_RE = re.compile(r"^##\s+(.+)$")
WHEN_BULLET_START_RE = re.compile(r"^\s*-\s+WHEN\b")
THEN_RE = re.compile(r"\bTHEN\b")
PROVED_BY_RE = re.compile(r"^\s*Proved by:\s*(.*)$")
GO_TEST_FUNC_RE = re.compile(
    r"^func\s+(Test(?:[^a-z]\w*)?)\s*\(\s*\w+\s+\*testing\.T\s*\)"
)
CODE_FENCE_RE = re.compile(r"^\s*(`{3,}|~{3,})(.*)$")


def find_test_functions(pkg_dir: Path) -> set[str]:
    """Find all top-level Test functions in *_test.go files directly in pkg_dir."""
    test_funcs: set[str] = set()
    if not pkg_dir.is_dir():
        return test_funcs
    for test_file in pkg_dir.glob("*_test.go"):
        if not test_file.is_file():
            continue
        try:
            content = test_file.read_text(encoding="utf-8")
        except OSError:
            continue
        in_block_comment = False
        for line in content.splitlines():
            if in_block_comment:
                end_idx = line.find("*/")
                if end_idx != -1:
                    in_block_comment = False
                    line = line[end_idx + 2:]
                else:
                    continue
            while not in_block_comment:
                start_idx = line.find("/*")
                if start_idx == -1:
                    break
                end_idx = line.find("*/", start_idx + 2)
                if end_idx != -1:
                    line = line[:start_idx] + line[end_idx + 2:]
                else:
                    line = line[:start_idx]
                    in_block_comment = True
                    break
            m = GO_TEST_FUNC_RE.match(line)
            if m:
                func_name = m.group(1)
                if func_name != "TestMain":
                    test_funcs.add(func_name)
    return test_funcs


def check_guarantees_file(file_path: Path, root: Path) -> list[str]:
    """Check a single GUARANTEES.md file for syntax and test citations."""
    try:
        content = file_path.read_text(encoding="utf-8")
    except OSError as err:
        return [f"{file_path}: cannot read file: {err}"]

    root = root.resolve()
    file_path = file_path.resolve()
    try:
        display_path = str(file_path.relative_to(root))
    except ValueError:
        display_path = str(file_path)

    pkg_dir = file_path.parent
    try:
        pkg_display = str(pkg_dir.relative_to(root))
    except ValueError:
        pkg_display = str(pkg_dir)

    available_tests = find_test_functions(pkg_dir)
    errors: list[str] = []
    seen_headings: dict[str, int] = {}

    current_heading: str | None = None
    current_heading_line: int = 0
    current_has_when_then: bool = False
    current_proved_by_blocks: list[dict[str, object]] = []
    in_when_bullet: bool = False
    in_proved_by: bool = False

    def finish_current_section() -> None:
        if current_heading is None:
            return
        if not current_has_when_then:
            errors.append(
                f'{display_path}:{current_heading_line}: "{current_heading}" has no - WHEN ... THEN scenario bullet'
            )
        if len(current_proved_by_blocks) == 0:
            errors.append(
                f'{display_path}:{current_heading_line}: "{current_heading}" has no Proved by: line'
            )
        elif len(current_proved_by_blocks) > 1:
            for extra_block in current_proved_by_blocks[1:]:
                errors.append(
                    f'{display_path}:{extra_block["start_line"]}: "{current_heading}" has duplicate Proved by: line'
                )
        else:
            block = current_proved_by_blocks[0]
            tests = block["tests"]
            if not tests:
                errors.append(
                    f'{display_path}:{block["start_line"]}: "{current_heading}" Proved by: line names no tests'
                )
            else:
                for line_no, test_name in tests:
                    if test_name not in available_tests:
                        errors.append(
                            f'{display_path}:{line_no}: "{current_heading}" cites test "{test_name}" which does not exist in {pkg_display}'
                        )

    fence_char: str | None = None
    fence_len: int = 0
    fence_start_line: int = 0
    lines = content.splitlines()
    for line_num, line in enumerate(lines, 1):
        stripped = line.strip()

        if fence_char is not None:
            m = CODE_FENCE_RE.match(line)
            if m:
                marker, info = m.group(1), m.group(2).strip()
                if (
                    marker[0] == fence_char
                    and len(marker) >= fence_len
                    and not info
                ):
                    fence_char = None
                    fence_len = 0
                    fence_start_line = 0
            continue

        m = CODE_FENCE_RE.match(line)
        if m:
            marker = m.group(1)
            fence_char = marker[0]
            fence_len = len(marker)
            fence_start_line = line_num
            in_proved_by = False
            in_when_bullet = False
            continue

        heading_match = HEADING_RE.match(line)
        if heading_match:
            finish_current_section()
            current_heading = heading_match.group(1).strip()
            current_heading_line = line_num
            current_has_when_then = False
            current_proved_by_blocks = []
            in_when_bullet = False
            in_proved_by = False

            if current_heading in seen_headings:
                errors.append(
                    f'{display_path}:{line_num}: duplicate guarantee heading "{current_heading}"'
                )
            else:
                seen_headings[current_heading] = line_num
            continue

        if current_heading is None:
            if PROVED_BY_RE.match(line):
                errors.append(
                    f"{display_path}:{line_num}: Proved by: line found outside of any guarantee section"
                )
            continue

        if in_proved_by:
            if not stripped or HEADING_RE.match(line) or PROVED_BY_RE.match(line):
                in_proved_by = False
            else:
                raw_tests = [
                    t.strip("`'\" \t") for t in line.split(",") if t.strip("`'\" \t")
                ]
                current_proved_by_blocks[-1]["tests"].extend(
                    (line_num, t) for t in raw_tests
                )
                continue

        proved_by_match = PROVED_BY_RE.match(line)
        if proved_by_match:
            in_when_bullet = False
            in_proved_by = True
            raw_tests_str = proved_by_match.group(1).strip()
            test_names = [
                t.strip("`'\" \t")
                for t in raw_tests_str.split(",")
                if t.strip("`'\" \t")
            ]
            current_proved_by_blocks.append(
                {
                    "start_line": line_num,
                    "tests": [(line_num, t) for t in test_names],
                }
            )
            continue

        if WHEN_BULLET_START_RE.match(line):
            in_when_bullet = True
            in_proved_by = False
            if THEN_RE.search(line):
                current_has_when_then = True
            continue

        if in_when_bullet:
            if stripped.startswith("- ") or stripped.startswith("#"):
                in_when_bullet = False
            elif THEN_RE.search(line):
                current_has_when_then = True

    finish_current_section()

    if fence_char is not None:
        errors.append(f"{display_path}:{fence_start_line}: unclosed code fence")

    if len(seen_headings) == 0:
        errors.append(f"{display_path}:1: no guarantee sections (## headings) found")

    return errors


def find_guarantees_in_dir(directory: Path) -> Path | None:
    """Find a file named exactly GUARANTEES.md in directory, case-sensitive."""
    if not directory.is_dir():
        return None
    try:
        for child in directory.iterdir():
            if child.name == "GUARANTEES.md" and child.is_file():
                return child.resolve()
    except OSError:
        return None
    return None


def select_guarantee_files(
    paths: list[str | Path], root: Path, all_files: bool = False
) -> list[Path]:
    """Select the GUARANTEES.md files to check based on changed paths or --all."""
    root = root.resolve()
    if all_files:
        guarantee_files: list[Path] = []
        for path in root.rglob("GUARANTEES.md"):
            try:
                rel_parts = path.relative_to(root).parts
            except ValueError:
                rel_parts = path.parts
            if any(
                part in {".git", "node_modules", "vendor", "testdata"}
                or part.startswith(".")
                for part in rel_parts[:-1]
            ):
                continue
            actual = find_guarantees_in_dir(path.parent)
            if actual is not None:
                guarantee_files.append(actual)
        return sorted(set(guarantee_files))

    selected: set[Path] = set()
    for raw in paths:
        if raw == "--":
            continue
        p = Path(raw)
        target = p if p.is_absolute() else (root / p)
        g = find_guarantees_in_dir(target if target.is_dir() else target.parent)
        if g is not None:
            selected.add(g)
            continue
        g_parent = find_guarantees_in_dir(target.parent)
        if g_parent is not None:
            selected.add(g_parent)
    return sorted(selected)


def check_guarantees(
    paths: list[str | Path], root: Path, all_files: bool = False
) -> list[str]:
    """Select and check all relevant GUARANTEES.md files."""
    files = select_guarantee_files(paths, root, all_files=all_files)
    all_errors: list[str] = []
    for f in files:
        all_errors.extend(check_guarantees_file(f, root))
    return all_errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Check package GUARANTEES.md contracts."
    )
    parser.add_argument(
        "--all",
        action="store_true",
        help="Check all GUARANTEES.md files across the repository",
    )
    parser.add_argument(
        "--root",
        type=Path,
        default=None,
        help="Root directory (default: current working directory)",
    )
    parser.add_argument(
        "paths", nargs="*", help="Changed paths to check GUARANTEES.md for"
    )

    args = parser.parse_args(argv)
    root = (args.root or Path.cwd()).resolve()

    filtered_paths = [p for p in args.paths if p != "--"]
    errors = check_guarantees(filtered_paths, root, all_files=args.all)
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
