"""Check the models recorded by a Claude lane's session files."""

import argparse
from datetime import datetime
import json
import os
from pathlib import Path
import re


DATE_SUFFIX = re.compile(r"-\d{8}\Z")


def timestamp(value):
    if not isinstance(value, str):
        return None
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None
    return parsed if parsed.tzinfo is not None else None


def session_directory(lane_path):
    name = re.sub(r"[^a-zA-Z0-9]", "-", lane_path)
    config = os.environ.get("CLAUDE_CONFIG_DIR")
    if config:
        base = Path(config).expanduser()
    else:
        home = os.environ.get("HOME")
        base = Path(home).expanduser() / ".claude" if home else Path.home() / ".claude"
    return base / "projects" / name, name


def json_records(path):
    try:
        with path.open(encoding="utf-8") as stream:
            for line in stream:
                try:
                    record = json.loads(line)
                except (json.JSONDecodeError, UnicodeDecodeError):
                    continue
                if isinstance(record, dict):
                    yield record
    except (OSError, UnicodeError):
        return


def file_modified_at_or_after(path, since):
    try:
        return path.stat().st_mtime >= since.timestamp()
    except OSError:
        return False


def top_level_files(directory):
    try:
        return sorted((path for path in directory.glob("*.jsonl") if path.is_file()), key=str)
    except OSError:
        return []


def subagent_files(directory):
    try:
        return sorted((path for path in directory.glob("*/subagents/**/*.jsonl") if path.is_file()), key=str)
    except OSError:
        return []


def display_path(path, directory):
    try:
        return str(path.relative_to(directory))
    except ValueError:
        return str(path)


def model_matches(model, expected):
    return model == expected or (
        model.startswith(expected) and DATE_SUFFIX.fullmatch(model[len(expected):]) is not None
    )


def check(lane_path, expected, since_text):
    since = timestamp(since_text)
    if since is None:
        print(f"invalid since timestamp: {since_text}")
        return 1

    directory, name = session_directory(lane_path)
    if len(name) > 200:
        print(f"unsupported lane path: session directory name is {len(name)} characters")
        return 1

    top_files = [path for path in top_level_files(directory) if file_modified_at_or_after(path, since)]
    all_files = top_files + [path for path in subagent_files(directory) if file_modified_at_or_after(path, since)]
    violations = []
    qualifying_file = False

    for path in top_files:
        for record in json_records(path):
            record_time = timestamp(record.get("timestamp"))
            if record_time is None or record_time < since:
                continue
            if record.get("type") != "assistant":
                continue
            message = record.get("message")
            model = message.get("model") if isinstance(message, dict) else None
            if not isinstance(model, str) or model == "<synthetic>":
                continue
            qualifying_file = True
            if not model_matches(model, expected):
                relative = display_path(path, directory)
                violations.append(
                    f"{relative}: assistant model {model} does not match expected {expected}"
                )

    for path in all_files:
        for record in json_records(path):
            record_time = timestamp(record.get("timestamp"))
            if record_time is None or record_time < since:
                continue
            if record.get("subtype") != "model_refusal_fallback":
                continue
            relative = display_path(path, directory)
            original = record.get("originalModel", "")
            fallback = record.get("fallbackModel", "")
            violations.append(
                f"{relative}: model_refusal_fallback {original} -> {fallback}"
            )

    if not qualifying_file:
        violations.append(f"no qualifying session file after {since_text} in {directory}")

    for violation in violations:
        print(violation)
    return 1 if violations else 0


def main(argv):
    parser = argparse.ArgumentParser(prog="run.py delegate model-check", description=__doc__)
    parser.add_argument("lane_path")
    parser.add_argument("expected_model")
    parser.add_argument("since")
    args = parser.parse_args(argv)
    return check(args.lane_path, args.expected_model, args.since)

