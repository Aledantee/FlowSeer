"""Run validated manual reproduction steps and report captured observations."""

import argparse
import json
import re
import sys
from pathlib import Path


def load_steps(path: Path) -> list[dict[str, str]]:
    steps = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(steps, list) or not steps:
        raise ValueError("steps must be a nonempty array")
    keys = set()
    for item in steps:
        if not isinstance(item, dict):
            raise ValueError("each step must be an object")
        if set(item) == {"step"}:
            text = item["step"]
        elif set(item) == {"capture", "question"}:
            key = item["capture"]
            if not isinstance(key, str) or not re.fullmatch(r"[A-Z][A-Z0-9_]*", key):
                raise ValueError("capture keys must match [A-Z][A-Z0-9_]*")
            if key in keys:
                raise ValueError(f"duplicate capture key: {key}")
            keys.add(key)
            text = item["question"]
        else:
            raise ValueError("expected step or capture/question fields")
        if not isinstance(text, str) or not text:
            raise ValueError("instructions and questions must be nonempty strings")
    return steps


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py diagnose hitl-loop")
    parser.add_argument("--steps", required=True, type=Path)
    args = parser.parse_args(argv)
    try:
        steps = load_steps(args.steps)
        captured = []
        for item in steps:
            if "step" in item:
                print(f"\n>>> {item['step']}")
                input("    [Enter when done] ")
            else:
                print(f"\n>>> {item['question']}")
                answer = input("    > ").strip(" \t")
                captured.append((item["capture"], answer))
    except (OSError, ValueError, EOFError, KeyboardInterrupt) as error:
        print(f"hitl-loop: {str(error) or type(error).__name__}", file=sys.stderr)
        return 1
    print("\n--- Captured ---")
    for key, answer in captured:
        print(f"{key}={answer}")
    return 0
