#!/usr/bin/env python3
"""Check Markdown prose against docs/doc-style.md.

A provenance finding (prose citing an agent run instead of the tree) fails
the check. Style findings are warnings unless --strict is given, because
most existing files predate the rules and a touched file should not force
a rewrite of text the change did not write.

Code fences, inline code, link targets, HTML comments, and YAML
frontmatter are skipped: the rules govern prose, not literals.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

# Prose that names an agent run, a transcript, or a conversation as its
# source. The reader has none of these; cite a file, the code, or a commit.
PROVENANCE = [
    # A device's SSH or SNMP session transcript is evidence, so "session
    # transcript" alone is not flagged.
    (re.compile(r"\bsession (history|notes)\b|\b(agent|claude|codex|worker) session (transcripts?|logs?)\b", re.I), "cites a session"),
    (re.compile(r"\((this|that|an? (earlier|prior|previous)) (session|run|conversation)\)", re.I), "cites a run"),
    (re.compile(r"\b(earlier|prior|previous|last) (agent )?(session|conversation)\b", re.I), "cites a run"),
    (re.compile(r"\b(chat|conversation) (history|transcripts?|logs?)\b|\bagent (history|transcripts?)\b", re.I), "cites a transcript"),
    (re.compile(r"\buser[- ](directed|approved|decided)\b|\bsession[- ]settled\b", re.I), "attributes to a conversation"),
    (re.compile(r"\((the )?user,? \d{4}-\d{2}-\d{2}\)", re.I), "attributes to a conversation"),
    (re.compile(r"\bas (discussed|agreed) (with|by) the user\b", re.I), "attributes to a conversation"),
    (re.compile(r"\bthe user (said|asked|decided|confirmed|wanted)\b", re.I), "attributes to a conversation"),
    (re.compile(r"\b(a|the) (worker|subagent|review agent|agent) (reported|found|measured|observed|noted)\b", re.I), "cites an agent run"),
]

STYLE = [
    (re.compile(r"—|(?<=\s)--(?=\s)"), "em dash: use a period, comma, colon, or parentheses"),
    (re.compile(r";(?=\s)"), "semicolon: split the sentence"),
    (re.compile(r"\b(delve|delves|robust|seamless(ly)?|comprehensive|leverag(e|es|ing)|vibrant|pivotal|landscape|tapestry|testament|underscor(e|es|ing)|crucial|foster(s|ing)?|showcas(e|es|ing)|holistic|streamlin(e|es|ed|ing)|empower(s|ing)?|cutting-edge|game-changer|paramount)\b", re.I), "puffery word"),
    (re.compile(r"\b(serves|stands|acts) as\b", re.I), "use the plain verb (is, does)"),
    (re.compile(r"\b(it'?s|it is) (worth noting|important to note|worth mentioning)\b|\bnote that\b|\bin (conclusion|summary)\b|\bto summari[sz]e\b|\bsimply put\b|\bessentially,|\blet'?s\b|\bhere'?s (what|how|why)\b", re.I), "signposting: say the thing"),
    (re.compile(r"\bnot (only|just|merely)\b[^.]*\bbut\b", re.I), "negative parallelism"),
    (re.compile(r",\s(ensuring|highlighting|underscoring|emphasizing|reflecting|showcasing|enabling|allowing for)\b", re.I), "trailing participle: end the sentence"),
    (re.compile(r"\b(plays? a (vital|key|crucial|pivotal) role)\b", re.I), "inflated significance"),
]

INLINE_CODE = re.compile(r"`+[^`]*`+")
LINK_TARGET = re.compile(r"\]\([^)]*\)|<https?://[^>]*>|https?://\S+")
HTML_COMMENT = re.compile(r"<!--.*?-->")


def prose_lines(text: str):
    """Yield (line number, prose) with literals blanked out."""
    lines = text.splitlines()
    start = 0
    if lines and lines[0].strip() == "---":
        for i in range(1, len(lines)):
            if lines[i].strip() == "---":
                start = i + 1
                break
    fence = None
    for number, line in enumerate(lines[start:], start + 1):
        stripped = line.lstrip()
        marker = stripped[:3]
        if marker in ("```", "~~~"):
            if fence is None:
                fence = marker
            elif fence == marker:
                fence = None
            continue
        if fence is not None or line.startswith("    "):
            continue
        cleaned = HTML_COMMENT.sub("", INLINE_CODE.sub("``", LINK_TARGET.sub("]()", line)))
        yield number, cleaned


def check(path: Path, strict: bool) -> tuple[list[str], list[str]]:
    errors: list[str] = []
    warnings: list[str] = []
    for number, line in prose_lines(path.read_text(encoding="utf-8")):
        for pattern, label in PROVENANCE:
            if match := pattern.search(line):
                errors.append(f"{path}:{number}: provenance, {label}: {match.group(0)!r}")
        for pattern, label in STYLE:
            if match := pattern.search(line):
                (errors if strict else warnings).append(f"{path}:{number}: {label}: {match.group(0)!r}")
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
        e, w = check(path, args.strict)
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
