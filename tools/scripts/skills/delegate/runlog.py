"""Append and read agent run events in the machine-wide JSON Lines log."""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import sys
import uuid

from lib import lock, proc


DEFAULT_PATH = Path.home() / ".claude/models/runs.jsonl"
ROLE = re.compile(r"[a-z][a-z-]*\Z")
OUTCOMES = {"accepted", "amended", "rejected", "blocked"}
VERIFY = {"pass", "fail", "none"}
QUERIES = ("last-start", "has-grade", "start-base")


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
    with lock.exclusive(target) as descriptor:
        size = os.fstat(descriptor).st_size
        if size and last_byte(descriptor, size) != b"\n":
            if os.write(descriptor, b"\n") != 1:
                raise OSError("short run log separator write")
        if os.write(descriptor, data) != len(data):
            raise OSError("short run log write")


def last_byte(descriptor, size):
    os.lseek(descriptor, size - 1, os.SEEK_SET)
    return os.read(descriptor, 1)


def parser():
    cli = argparse.ArgumentParser(prog="run.py delegate runlog", description=__doc__)
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
    for name in QUERIES:
        commands.add_parser(name).add_argument("--run", required=True)
    commands.add_parser("executors").add_argument("--plan", required=True)
    writers = commands.add_parser("writers")
    for flag in ("plan", "range", "coordinator"):
        writers.add_argument("--" + flag, required=True)
    return cli


def last_start(run):
    """Return the model and time of the run's last start event, tab-separated."""
    starts = [event for event in read() if event.get("event") == "start" and event.get("run") == run]
    if not starts:
        return ""
    return "\t".join((starts[-1].get("model", ""), starts[-1].get("at", "")))


def has_grade(run):
    return any(event.get("event") == "grade" and event.get("run") == run for event in read())


def start_base(run):
    for event in read():
        if event.get("event") == "start" and event["run"] == run:
            return event["base"]
    raise ValueError("no start event for run %s" % run)


def executors(events, plan):
    grades = {event.get("run"): event.get("outcome") for event in events if event.get("event") == "grade"}
    return [event for event in events if event.get("event") == "start"
            and event["role"].startswith("execute") and event.get("plan") == plan
            and grades.get(event.get("run")) in {"accepted", "amended"}]


def revisions(*args):
    result = proc.run(["git", "rev-list", *args])
    if result.code:
        raise ValueError(result.stderr.strip())
    return result.stdout.split()


def writers(plan, revision_range, coordinator):
    events = list(read())
    heads = {event["run"]: event["head"] for event in events if event.get("event") == "end"}
    runs = [(set(revisions(event["base"] + ".." + heads[event["run"]])),
             event.get("model") or event.get("agent"), event["at"])
            for event in executors(events, plan) if event["run"] in heads]
    lines = []
    for commit in revisions("--no-merges", revision_range):
        writer = max((run for run in runs if commit in run[0]),
                     key=lambda run: (-len(run[0]), run[2]), default=(None, coordinator))[1]
        lines.append("%s %s" % (commit[:12], writer))
    return lines


def main(argv: list[str]) -> int:
    args = parser().parse_args(argv)
    fields = vars(args)
    kind = fields.pop("event")
    if kind == "last-start":
        print(last_start(fields["run"]))
        return 0
    if kind == "has-grade":
        return 0 if has_grade(fields["run"]) else 1
    if kind in {"start-base", "executors", "writers"}:
        try:
            if kind == "start-base":
                print(start_base(fields["run"]))
            elif kind == "executors":
                for event in executors(list(read()), fields["plan"]):
                    print(event.get("unit") or "-", event.get("model") or event.get("agent"))
            else:
                for line in writers(fields["plan"], fields["range"], fields["coordinator"]):
                    print(line)
        except (OSError, ValueError) as exc:
            print("runlog: %s" % exc, file=sys.stderr)
            return 1
        return 0
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
