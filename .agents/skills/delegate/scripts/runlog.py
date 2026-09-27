#!/usr/bin/env python3
"""Append and read agent run events in the machine-wide JSON Lines log."""

import argparse
from datetime import datetime, timezone
import fcntl
import json
import os
from pathlib import Path
import re
import sys
import uuid


DEFAULT_PATH = Path.home() / ".claude/models/runs.jsonl"
ROLE = re.compile(r"[a-z][a-z-]*\Z")
OUTCOMES = {"accepted", "amended", "rejected", "blocked"}
VERIFY = {"pass", "fail", "none"}


class ReadResult:
    def __init__(self, events, skipped):
        self.events = events
        self.skipped = skipped

    def __iter__(self):
        return iter(self.events)


def read(path=None):
    """Return parseable events and the count of malformed lines."""
    path = Path(path) if path is not None else log_path()
    events = []
    skipped = 0
    try:
        with path.open("rb") as stream:
            for line in stream:
                try:
                    event = json.loads(line)
                except (json.JSONDecodeError, UnicodeDecodeError):
                    skipped += 1
                    continue
                if isinstance(event, dict):
                    events.append(event)
                else:
                    skipped += 1
    except FileNotFoundError:
        pass
    return ReadResult(events, skipped)


def log_path():
    return Path(os.environ.get("FLOWSEER_RUNLOG", DEFAULT_PATH)).expanduser()


def required(event, *names):
    for name in names:
        if not isinstance(event.get(name), str) or not event[name].strip():
            raise ValueError("%s requires %s" % (event["event"], name))


def validate(event):
    if event.get("v") != 1 or event.get("event") not in {"start", "grade", "end", "review"}:
        raise ValueError("invalid event version or type")
    required(event, "run", "at")
    try:
        datetime.fromisoformat(event["at"].replace("Z", "+00:00"))
    except ValueError as exc:
        raise ValueError("invalid event time") from exc
    kind = event["event"]
    if kind == "start":
        required(event, "lane", "cli", "role", "worktree", "branch", "base")
        if not event.get("model") and not event.get("agent"):
            raise ValueError("start requires model or agent")
    elif kind == "grade":
        if event.get("outcome") not in OUTCOMES or event.get("verify") not in VERIFY:
            raise ValueError("invalid grade outcome or verify result")
    elif kind == "end":
        required(event, "head")
    else:
        required(event, "model", "role", "agent")
        counts = [event.get(name) for name in ("findings", "held", "unverified")]
        if any(type(count) is not int or count < 0 for count in counts):
            raise ValueError("review counts must be nonnegative integers")
        if counts[1] + counts[2] > counts[0]:
            raise ValueError("held plus unverified exceeds findings")
    if kind in {"start", "review"} and not ROLE.fullmatch(event["role"]):
        raise ValueError("invalid role")


def append(event, path=None):
    validate(event)
    target = Path(path) if path is not None else log_path()
    target.parent.mkdir(parents=True, exist_ok=True)
    data = (json.dumps(event, separators=(",", ":")) + "\n").encode("utf-8")
    descriptor = os.open(str(target), os.O_RDWR | os.O_CREAT | os.O_APPEND, 0o600)
    try:
        fcntl.flock(descriptor, fcntl.LOCK_EX)
        size = os.fstat(descriptor).st_size
        if size and os.pread(descriptor, 1, size - 1) != b"\n":
            if os.write(descriptor, b"\n") != 1:
                raise OSError("short run log separator write")
        if os.write(descriptor, data) != len(data):
            raise OSError("short run log write")
    finally:
        fcntl.flock(descriptor, fcntl.LOCK_UN)
        os.close(descriptor)


def parser():
    cli = argparse.ArgumentParser(description=__doc__)
    commands = cli.add_subparsers(dest="event", required=True)
    start = commands.add_parser("start")
    for flag in ("lane", "cli", "role", "worktree", "branch", "base"):
        start.add_argument("--" + flag, required=True)
    for flag in ("model", "effort", "agent", "plan", "unit"):
        start.add_argument("--" + flag)
    grade = commands.add_parser("grade")
    grade.add_argument("--run", required=True)
    grade.add_argument("--outcome", required=True)
    grade.add_argument("--verify", required=True)
    grade.add_argument("--note")
    end = commands.add_parser("end")
    end.add_argument("--run", required=True)
    end.add_argument("--head", required=True)
    review = commands.add_parser("review")
    for flag in ("model", "role", "agent"):
        review.add_argument("--" + flag, required=True)
    review.add_argument("--plan")
    for flag in ("findings", "held", "unverified"):
        review.add_argument("--" + flag, type=int, required=True)
    return cli


def main(argv=None):
    args = parser().parse_args(argv)
    fields = vars(args)
    kind = fields.pop("event")
    event = {"v": 1, "event": kind, "run": fields.pop("run", None) or uuid.uuid4().hex,
             "at": datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")}
    event.update({key: value for key, value in fields.items() if value is not None})
    try:
        append(event)
    except (OSError, ValueError) as exc:
        print("runlog: %s" % exc, file=sys.stderr)
        return 1
    if kind == "start":
        print(event["run"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
