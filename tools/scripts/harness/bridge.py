import argparse
import json
import math
import sqlite3
import sys
import time
from pathlib import Path

from harness.config import load
from harness.state import Store


def _text(value: object, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{field} must be a nonempty string")
    return value


def validate(kind: str, payload: object) -> dict:
    if not isinstance(payload, dict) or type(payload.get("version")) is not int or payload["version"] != 1:
        raise ValueError("unsupported payload version")
    _text(payload.get("request_id"), "request_id")
    fields = {"version", "request_id"}
    if kind == "ask":
        fields |= {"question", "options", "recommendation"}
        _text(payload.get("question"), "question")
        options = payload.get("options")
        if not isinstance(options, list) or not 1 <= len(options) <= 20:
            raise ValueError("options must contain between 1 and 20 alternatives")
        ids = set()
        for option in options:
            if not isinstance(option, dict) or set(option) != {"id", "label", "tradeoff"}:
                raise ValueError("options need id, label, and tradeoff")
            for field in ("id", "label", "tradeoff"):
                _text(option[field], field)
            if option["id"] in ids:
                raise ValueError("options must have distinct IDs")
            ids.add(option["id"])
        recommendation = _text(payload.get("recommendation"), "recommendation")
        if recommendation not in ids:
            raise ValueError("recommendation must name an option")
    elif kind == "progress":
        fields.add("summary")
        _text(payload.get("summary"), "summary")
    elif kind == "result":
        fields |= {"status", "artifacts", "evidence"}
        if payload.get("status") not in ("completed", "failed", "blocked"):
            raise ValueError("result status must be completed, failed, or blocked")
        for field in ("artifacts", "evidence"):
            items = payload.get(field)
            if not isinstance(items, list) or not items:
                raise ValueError(f"{field} must be a nonempty array")
            for item in items:
                _text(item, field)
            if field == "artifacts" and any(not Path(item).is_absolute() for item in items):
                raise ValueError("artifact paths must be absolute")
    elif kind == "ack":
        fields.add("action")
        if payload.get("action") not in ("cancel", "pause", "resume", "steer"):
            raise ValueError("ack action must be cancel, pause, resume, or steer")
    else:
        raise ValueError("unknown bridge command")
    if set(payload) != fields:
        raise ValueError("unexpected or missing payload fields")
    return payload


def read_request(path: Path, kind: str) -> dict:
    with path.open("rb") as stream:
        content = stream.read(65537)
    if len(content) > 65536:
        raise ValueError("request exceeds 64 KiB")
    return validate(kind, json.loads(content))


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py harness bridge")
    parser.add_argument("--config", type=Path, dest="global_config")
    commands = parser.add_subparsers(dest="command", required=True)
    for name in ("ask", "progress", "result", "ack"):
        command = commands.add_parser(name)
        command.add_argument("--config", type=Path)
        command.add_argument("--task", required=True)
        command.add_argument("--request", type=Path, required=True)
        if name == "ask":
            command.add_argument("--timeout", type=float)
    args = parser.parse_args(argv)
    try:
        timeout = getattr(args, "timeout", None)
        if timeout is not None and (not math.isfinite(timeout) or timeout < 0):
            raise ValueError("timeout must be finite and nonnegative")
        payload = read_request(args.request, args.command)
        config = load(args.config or args.global_config)
        store = Store(config)
        if args.command != "ask":
            store.record_event(args.task, args.command, payload)
            return 0
        decision = store.request_decision(args.task, payload)
        deadline = None if timeout is None else time.monotonic() + timeout
        while True:
            answer = store.answer(decision)
            if answer is not None:
                print(json.dumps(answer, ensure_ascii=False))
                return 0
            if store.decision(decision)["status"] == "cancelled":
                return 3
            if deadline is not None and time.monotonic() >= deadline:
                return 4
            time.sleep(1 if deadline is None else min(1, max(0, deadline - time.monotonic())))
    except (OSError, ValueError, sqlite3.Error) as error:
        print(f"harness bridge: {error}", file=sys.stderr)
        return 2
