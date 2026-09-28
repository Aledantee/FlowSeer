#!/usr/bin/env python3
"""Check that package GUARANTEES.md files conform to conventions and tests exist.

Each guarantee section in GUARANTEES.md has a unique ## heading, exactly one
normative MUST sentence, at least one - WHEN ... THEN scenario bullet, and
exactly one Proved by: line citing top-level Go test functions discovered via
go list. Every line must match one of the allowed line grammar shapes.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys
import unicodedata
from pathlib import Path

THEN_RE = re.compile(r"\bTHEN\b")
MUST_RE = re.compile(r"\bMUST\b")
ORDERED_LIST_RE = re.compile(r"^\d{1,9}[.)](\s|$)")
THEMATIC_BREAK_RE = re.compile(r"^[ \t]*([-*_][ \t]*){3,}$")
TOKEN_RE = re.compile(r"[^\W\d]\w*|[(),.*{}]")
GO_IDENTIFIER_RE = re.compile(r"^[^\W\d]\w*$", re.UNICODE)


def mask_comments_and_strings(content: str) -> str:
    """Mask Go comments and strings with spaces, preserving newlines and code."""
    out: list[str] = []
    n = len(content)
    i = 0
    state = "CODE"

    while i < n:
        c = content[i]

        if state == "CODE":
            if c == "/" and i + 1 < n and content[i + 1] == "/":
                state = "LINE_COMMENT"
                out.append("  ")
                i += 2
                continue
            if c == "/" and i + 1 < n and content[i + 1] == "*":
                state = "BLOCK_COMMENT"
                out.append("  ")
                i += 2
                continue
            if c == '"':
                state = "STRING"
                out.append(" ")
                i += 1
                continue
            if c == "'":
                state = "RUNE"
                out.append(" ")
                i += 1
                continue
            if c == "`":
                state = "RAW_STRING"
                out.append(" ")
                i += 1
                continue
            out.append(c)
            i += 1

        elif state == "LINE_COMMENT":
            if c == "\n":
                state = "CODE"
                out.append("\n")
                i += 1
            else:
                out.append(" ")
                i += 1

        elif state == "BLOCK_COMMENT":
            if c == "*" and i + 1 < n and content[i + 1] == "/":
                state = "CODE"
                out.append("  ")
                i += 2
            elif c == "\n":
                out.append("\n")
                i += 1
            else:
                out.append(" ")
                i += 1

        elif state == "STRING":
            if c == "\\":
                if i + 1 < n:
                    next_c = content[i + 1]
                    out.append(" ")
                    if next_c == "\n":
                        state = "CODE"
                        out.append("\n")
                    else:
                        out.append(" ")
                    i += 2
                else:
                    out.append(" ")
                    i += 1
            elif c == '"':
                state = "CODE"
                out.append(" ")
                i += 1
            elif c == "\n":
                state = "CODE"
                out.append("\n")
                i += 1
            else:
                out.append(" ")
                i += 1

        elif state == "RUNE":
            if c == "\\":
                if i + 1 < n:
                    next_c = content[i + 1]
                    out.append(" ")
                    if next_c == "\n":
                        state = "CODE"
                        out.append("\n")
                    else:
                        out.append(" ")
                    i += 2
                else:
                    out.append(" ")
                    i += 1
            elif c == "'":
                state = "CODE"
                out.append(" ")
                i += 1
            elif c == "\n":
                state = "CODE"
                out.append("\n")
                i += 1
            else:
                out.append(" ")
                i += 1

        elif state == "RAW_STRING":
            if c == "`":
                state = "CODE"
                out.append(" ")
                i += 1
            elif c == "\n":
                out.append("\n")
                i += 1
            else:
                out.append(" ")
                i += 1

    return "".join(out)


def is_text_line(line: str) -> bool:
    """Report whether a column-0 line starts CommonMark paragraph text.

    Any character that can open a leaf block or container (a fence, a heading
    marker, a list marker, a block quote, a thematic break, an HTML block, a
    link reference definition) is rejected, so only a Unicode letter, digit,
    or lone backtick begins text. An ordered-list marker is a paragraph only
    when the digits are not followed by `.` or `)` and a space.
    """
    if line.startswith("```"):
        return False
    first = line[0]
    if first == "`":
        return True
    if unicodedata.category(first)[0] not in ("L", "N"):
        return False
    return ORDERED_LIST_RE.match(line) is None


def citation_name(item: str) -> str | None:
    """Return the Go test name an item cites, or None when it is malformed."""
    name = item
    if len(name) >= 2 and name.startswith("`") and name.endswith("`"):
        name = name[1:-1]
    if GO_IDENTIFIER_RE.match(name):
        return name
    return None


def parse_citation_line(text: str) -> tuple[list[str], bool]:
    """Split a citation list body and validate every item.

    The body may be empty, so the list can continue on the next indented
    line. A trailing comma is tolerated here and reported once for the whole
    block; an empty item anywhere else is malformed.
    """
    parts = text.split(",")
    if not parts[-1].strip():
        parts = parts[:-1]
    names: list[str] = []
    for raw in parts:
        name = citation_name(raw.strip())
        if name is None:
            return names, False
        names.append(name)
    return names, True


def is_valid_test_param(param_tokens: list[str]) -> bool:
    """Validate the parameter tokens Go accepts for a top-level Test function.

    Go requires exactly one `*T` or `*<pkg>.T` parameter by AST shape, with
    no parenthesized type and no result value.
    """
    if param_tokens and param_tokens[-1] == ",":
        param_tokens = param_tokens[:-1]
    if not param_tokens:
        return False
    if param_tokens[0] not in ("*", "("):
        param_tokens = param_tokens[1:]
    if param_tokens == ["*", "T"]:
        return True
    return (
        len(param_tokens) == 4
        and param_tokens[0] == "*"
        and param_tokens[2] == "."
        and param_tokens[3] == "T"
        and param_tokens[1].isidentifier()
    )


def scan_test_functions(content: str) -> set[str]:
    """Scan masked Go code for top-level test functions."""
    test_funcs: set[str] = set()
    tokens = TOKEN_RE.findall(content)
    n = len(tokens)
    brace_depth = 0
    i = 0
    while i < n:
        tok = tokens[i]
        if tok == "{":
            brace_depth += 1
            i += 1
            continue
        if tok == "}":
            brace_depth = max(0, brace_depth - 1)
            i += 1
            continue
        if brace_depth == 0 and tok == "func":
            if i + 2 < n and tokens[i + 2] == "(":
                name = tokens[i + 1]
                if name.startswith("Test") and (
                    len(name) == 4 or unicodedata.category(name[4]) != "Ll"
                ):
                    j = i + 3
                    paren_depth = 1
                    while j < n and paren_depth > 0:
                        if tokens[j] == "(":
                            paren_depth += 1
                        elif tokens[j] == ")":
                            paren_depth -= 1
                        j += 1
                    if paren_depth == 0 and j < n and tokens[j] == "{":
                        param_tokens = tokens[i + 3 : j - 1]
                        if is_valid_test_param(param_tokens):
                            test_funcs.add(name)
            i += 1
            continue
        i += 1
    return test_funcs


def goflags_without_mod(goflags: str) -> str:
    """Drop any `-mod=` entry from inherited GOFLAGS, keeping the rest."""
    return " ".join(flag for flag in goflags.split() if not flag.startswith("-mod="))


def find_test_functions(pkg_dir: Path) -> tuple[set[str], str | None]:
    """Resolve top-level Test functions from the package's go list test files.

    Returns the names and, when go list cannot report the package, the first
    stderr line (or the OS error) explaining why no test file was scanned.
    """
    test_funcs: set[str] = set()
    if not pkg_dir.is_dir():
        return test_funcs, "package directory does not exist"
    env = dict(os.environ)
    # GOWORK=off keeps a stray go.work from pulling in unrelated packages;
    # GOFLAGS keeps the environment's flags but drops any -mod, so Go's
    # read-only default applies and a verifier run never rewrites go.mod.
    env["GOWORK"] = "off"
    env["GOFLAGS"] = goflags_without_mod(env.get("GOFLAGS", ""))
    try:
        res = subprocess.run(
            ["go", "list", "-json", "."],
            cwd=str(pkg_dir),
            capture_output=True,
            text=True,
            env=env,
        )
    except OSError as err:
        return test_funcs, str(err)
    if res.returncode != 0:
        first = next((line for line in res.stderr.splitlines() if line.strip()), "")
        return test_funcs, first or f"go list exited {res.returncode}"
    try:
        pkg_data = json.loads(res.stdout)
    except ValueError as err:
        return test_funcs, f"invalid go list JSON: {err}"
    test_filenames = pkg_data.get("TestGoFiles", []) + pkg_data.get("XTestGoFiles", [])
    for fname in test_filenames:
        test_file = pkg_dir / fname
        try:
            content = test_file.read_text(encoding="utf-8")
        except OSError:
            continue
        masked = mask_comments_and_strings(content)
        test_funcs.update(scan_test_functions(masked))
    return test_funcs, None


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

    available_tests, list_error = find_test_functions(pkg_dir)
    errors: list[str] = []
    if list_error is not None:
        errors.append(f"{display_path}:1: go list failed in {pkg_display}: {list_error}")

    seen_headings: dict[str, int] = {}
    seen_doc_title = False

    current_heading: str | None = None
    current_heading_line: int = 0
    current_has_when_then: bool = False
    current_normative_lines: list[int] = []
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
        if not current_normative_lines:
            errors.append(
                f'{display_path}:{current_heading_line}: "{current_heading}" has no normative MUST sentence'
            )
        else:
            for extra_line in current_normative_lines[1:]:
                errors.append(
                    f'{display_path}:{extra_line}: "{current_heading}" has more than one normative sentence'
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
            if block["ends_with_comma"]:
                errors.append(
                    f'{display_path}:{block["last_line"]}: "{current_heading}" Proved by: list ends with a comma'
                )
            tests = block["tests"]
            if not tests:
                errors.append(
                    f'{display_path}:{block["start_line"]}: "{current_heading}" Proved by: line names no tests'
                )
            elif list_error is None:
                for line_no, test_name in tests:
                    if test_name not in available_tests:
                        errors.append(
                            f'{display_path}:{line_no}: "{current_heading}" cites test "{test_name}" which does not exist in {pkg_display}'
                        )

    def clear_continuation() -> None:
        nonlocal in_when_bullet, in_proved_by
        in_when_bullet = False
        in_proved_by = False

    def reject(line_num: int) -> None:
        errors.append(f"{display_path}:{line_num}: unknown line format")
        clear_continuation()

    lines = content.splitlines()
    for line_num, line in enumerate(lines, 1):
        if not line.strip():
            clear_continuation()
            continue

        indent = len(line) - len(line.lstrip(" "))
        if indent > 3:
            reject(line_num)
            continue

        if indent > 0:
            body = line[indent:]
            if body.startswith("Proved by:"):
                reject(line_num)
                continue
            if in_proved_by:
                if THEMATIC_BREAK_RE.match(body):
                    reject(line_num)
                    continue
                names, valid = parse_citation_line(body)
                if not valid:
                    reject(line_num)
                    continue
                block = current_proved_by_blocks[-1]
                block["last_line"] = line_num
                block["ends_with_comma"] = body.rstrip().endswith(",")
                block["tests"].extend((line_num, name) for name in names)
                continue
            if in_when_bullet and is_text_line(body):
                if THEN_RE.search(body):
                    current_has_when_then = True
                continue
            reject(line_num)
            continue

        clear_continuation()

        if line.startswith("# "):
            if current_heading is not None or seen_doc_title:
                reject(line_num)
            else:
                seen_doc_title = True
            continue

        if line.startswith("## "):
            finish_current_section()
            title = re.sub(r"\s+#+$", "", line[3:].strip())
            if not title:
                reject(line_num)
                current_heading = None
                continue
            current_heading = title
            current_heading_line = line_num
            current_has_when_then = False
            current_normative_lines = []
            current_proved_by_blocks = []
            if title in seen_headings:
                errors.append(
                    f'{display_path}:{line_num}: duplicate guarantee heading "{title}"'
                )
            else:
                seen_headings[title] = line_num
            continue

        if current_heading is None:
            if not is_text_line(line):
                reject(line_num)
            continue

        if line.startswith("Proved by:"):
            body = line[len("Proved by:"):]
            names, valid = parse_citation_line(body)
            if not valid:
                reject(line_num)
                continue
            current_proved_by_blocks.append(
                {
                    "start_line": line_num,
                    "last_line": line_num,
                    "ends_with_comma": body.rstrip().endswith(","),
                    "tests": [(line_num, name) for name in names],
                }
            )
            in_proved_by = True
            continue

        if line.startswith("- WHEN "):
            in_when_bullet = True
            if THEN_RE.search(line):
                current_has_when_then = True
            continue

        if is_text_line(line):
            if MUST_RE.search(line):
                current_normative_lines.append(line_num)
                continue
            reject(line_num)
            continue

        reject(line_num)

    finish_current_section()
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
