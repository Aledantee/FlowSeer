#!/usr/bin/env python3
"""Check Markdown prose against docs/doc-style.md.

A provenance finding (prose citing an agent run instead of the tree) fails
the check. Style findings are warnings unless --strict is given, because
most existing files predate the rules and a touched file should not force
a rewrite of text the change did not write.

Skipped as literals: YAML frontmatter, fenced code blocks (``` or ~~~,
closed per CommonMark), HTML comments (also across lines), inline code
spans on one line, link targets, and bare URLs.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

APOS = "['’]"

# Prose that names an agent run, a transcript, or a conversation as its
# source. Each pattern needs a cue that only an agent run has, because the
# product has its own agents, sessions, and users: "the edge agent
# reported", "reuse the previous SSH session", and "user-approved firmware"
# are domain prose.
PROVENANCE = [
    (re.compile(r"\bsession history\b|\bsession's history\b|\bsession[- ]settled\b", re.I), "cites a session"),
    (re.compile(r"\((this|that|an? (earlier|prior|previous)) (session|run|conversation)\)", re.I), "cites a run"),
    (re.compile(r"\b(earlier|prior|previous) (agent )?(run|conversation)s?\b", re.I), "cites a run"),
    (re.compile(r"\b(chat|conversation) (history|transcripts?|logs?)\b|\bagent (history|transcripts?)\b", re.I), "cites a transcript"),
    (re.compile(r"\((session-settled|user-(directed|approved|decided))\b|\buser-(directed|approved|decided)[:.)]|^\s*[-*]?\s*user-(directed|approved|decided)\b", re.I), "attributes to a conversation"),
    (re.compile(r"\((the )?user,? \d{4}-\d{2}-\d{2}\)", re.I), "attributes to a conversation"),
    (re.compile(rf"\bas (discussed|agreed) (with|by) the user\b|\b(the )?user{APOS}s (direction|decision|ruling)\b", re.I), "attributes to a conversation"),
    (re.compile(r"\b(the )?user (chose|ruled|picked|requested|decided|approved)\b(?! (it|this|that)\?)", re.I), "attributes to a conversation"),
    (re.compile(r"\b(the|a) (worker|coordinator|lane) (reported|measured)\b|\bthe coordinator (found|observed)\b", re.I), "cites an agent run"),
]

STYLE = [
    (re.compile(r"—|(?<=\s)--(?=\s)"), "em dash: use a period, comma, colon, or parentheses"),
    (re.compile(r";(?=\s)"), "semicolon: split the sentence"),
    (re.compile(r"\b(delve|delves|robust|seamless(ly)?|comprehensive|leverag(e|es|ing)|vibrant|pivotal|landscape|tapestry|testament|underscor(e|es|ing)|crucial|foster(s|ing)?|showcas(e|es|ing)|holistic|streamlin(e|es|ed|ing)|empower(s|ing)?|cutting-edge|game-changer|paramount)\b", re.I), "puffery word"),
    (re.compile(r"\b(serves|stands|acts) as\b", re.I), "use the plain verb (is, does)"),
    (re.compile(rf"\b(it{APOS}?s|it is) (worth noting|important to note|worth mentioning)\b|\bnote that\b|\bimportantly,|\bin (conclusion|summary)\b|\bto summari[sz]e\b|\bsimply put\b|\bessentially,|\blet{APOS}s\b|\blet us\b|\bhere{APOS}?s (what|how|why)\b", re.I), "signposting: say the thing"),
    (re.compile(r"\bnot (only|just|merely)\b[^.]*\bbut\b", re.I), "negative parallelism"),
    (re.compile(r",\s(ensuring|highlighting|underscoring|emphasizing|reflecting|showcasing|enabling|allowing for)\b", re.I), "trailing participle: end the sentence"),
    (re.compile(r"\b(plays? a (vital|key|crucial|pivotal) role)\b", re.I), "inflated significance"),
]

FENCE = re.compile(r"^\s{0,3}(`{3,}|~{3,})")
INLINE_CODE = re.compile(r"`+[^`]*`+")
LINK_TARGET = re.compile(r"\]\([^)]*\)|<https?://[^>]*>|https?://[^\s`]+")


def prose_lines(text: str):
    """Yield (line number, prose) with literals blanked out."""
    lines = text.splitlines()
    start = 0
    if lines and lines[0].strip() == "---":
        for i in range(1, len(lines)):
            if lines[i].strip() == "---":
                start = i + 1
                break
    # CommonMark: a fence closes on a run of the same character at least as
    # long as the one that opened it, so ```` can wrap a ``` example.
    fence = None
    in_comment = False
    for number, line in enumerate(lines[start:], start + 1):
        if in_comment:
            end = line.find("-->")
            if end < 0:
                continue
            in_comment = False
            line = line[end + 3 :]
        elif match := FENCE.match(line):
            run = match.group(1)
            if fence is None:
                fence = run
            elif run[0] == fence[0] and len(run) >= len(fence) and not line.strip()[len(run) :].strip():
                fence = None
            continue
        if fence is not None:
            continue
        # Inline code first, so a URL inside a code span cannot eat the
        # closing backtick and blank the prose after it.
        cleaned = LINK_TARGET.sub("]()", INLINE_CODE.sub("``", line))
        cleaned = re.sub(r"<!--.*?-->", "", cleaned)
        if (opened := cleaned.find("<!--")) >= 0:
            in_comment = True
            cleaned = cleaned[:opened]
        yield number, cleaned


def check_text(text: str, name: str, strict: bool) -> tuple[list[str], list[str]]:
    errors: list[str] = []
    warnings: list[str] = []
    for number, line in prose_lines(text):
        for pattern, label in PROVENANCE:
            if match := pattern.search(line):
                errors.append(f"{name}:{number}: provenance, {label}: {match.group(0)!r}")
        for pattern, label in STYLE:
            if match := pattern.search(line):
                (errors if strict else warnings).append(f"{name}:{number}: {label}: {match.group(0)!r}")
    return errors, warnings


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--strict", action="store_true", help="fail on style findings too")
    parser.add_argument("--quiet", action="store_true", help="print only failures and a warning count")
    parser.add_argument("paths", nargs="+", type=Path)
    args = parser.parse_args()

    errors: list[str] = []
    warnings: list[str] = []
    for path in args.paths:
        if path.suffix != ".md" or not path.is_file():
            continue
        e, w = check_text(path.read_text(encoding="utf-8"), str(path), args.strict)
        errors += e
        warnings += w

    for line in errors:
        print(line, file=sys.stderr)
    if args.quiet:
        if warnings:
            print(f"prose: {len(warnings)} style warning(s); rerun check-prose.py on the file to list them")
    else:
        for line in warnings:
            print(f"warning: {line}")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
