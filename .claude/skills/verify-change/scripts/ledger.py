#!/usr/bin/env python3
"""Maintain the plan status ledger and the checkpoints file of a worktree.

Both files live in the worktree's git directory, beside the verifier
receipt, and `check-plan-status.py` validates the ledger on every verifier
run. The skills update them through this script rather than by writing the
files themselves: the script resolves the directory with `git rev-parse`
and writes the whole file, so a caller names only the unit, the status,
and the note, and the `verify-change` contract stays in one place.

    ledger.py init docs/plans/<plan>.md U1 U2 U3
    ledger.py set U1 in_progress
    ledger.py set U1 passed --note "Diagnostics keep the source span."
    ledger.py set U2 blocked --note "<reason>"
    ledger.py show
    ledger.py checkpoint review "accept"
    ledger.py checkpoint --replace implemented "<request in a few words>"

`set ... passed` takes the commit from `HEAD` and `verified_at` from the
receipt of the verifier run that just passed, unless `--commit` or
`--verified-at` overrides them. `resume` is recomputed on every write: the
units in progress, else the first pending unit, else empty. A note is kept
until `--note` replaces it; `--note ""` clears it.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import tempfile
from pathlib import Path

CONTRACT = "flowseer-plan-status/v1"
STATUSES = ("pending", "in_progress", "passed", "blocked")
LEDGER_NAME = "flowseer-plan-status.json"
CHECKPOINTS_NAME = "flowseer-checkpoints"
RECEIPT_NAME = "flowseer-verification-receipt"


def fail(message: str) -> None:
    print(f"ledger: {message}", file=sys.stderr)
    sys.exit(1)


def git_output(*args: str) -> str:
    return subprocess.run(["git", *args], check=True, capture_output=True, text=True).stdout.strip()


def state_dir() -> Path:
    directory = Path(git_output("rev-parse", "--git-dir"))
    if not directory.is_absolute():
        directory = Path(git_output("rev-parse", "--show-toplevel")) / directory
    return directory


def write_whole(path: Path, text: str) -> None:
    # Written whole, then renamed: a reader that races the write sees the
    # previous file, never a truncated one.
    handle, tmp = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    with os.fdopen(handle, "w", encoding="utf-8") as out:
        out.write(text)
    os.replace(tmp, path)


def read_ledger(path: Path) -> dict:
    if not path.exists():
        fail(f"{path} does not exist; run `init` first")
    try:
        ledger = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as error:
        fail(f"{path} is not valid JSON: {error}")
    if not isinstance(ledger, dict) or ledger.get("contract") != CONTRACT:
        fail(f"{path} does not carry contract {CONTRACT!r}")
    return ledger


def recompute_resume(ledger: dict) -> None:
    units = ledger["units"]
    in_progress = [unit["id"] for unit in units if unit["status"] == "in_progress"]
    if in_progress:
        ledger["resume"] = in_progress
        return
    pending = [unit["id"] for unit in units if unit["status"] == "pending"]
    ledger["resume"] = pending[:1]


def write_ledger(path: Path, ledger: dict) -> None:
    recompute_resume(ledger)
    write_whole(path, json.dumps(ledger, indent=2) + "\n")


def receipt_verified_at(directory: Path) -> str:
    receipt = directory / RECEIPT_NAME
    if not receipt.exists():
        fail(f"{receipt} does not exist; a unit passes only after a verifier run")
    for line in receipt.read_text(encoding="utf-8").splitlines():
        key, sep, value = line.partition("=")
        if sep and key == "verified_at":
            return value.strip()
    fail(f"{receipt} carries no verified_at line")
    raise AssertionError("unreachable")


def cmd_init(args: argparse.Namespace) -> None:
    root = Path(git_output("rev-parse", "--show-toplevel"))
    if not (root / args.plan).is_file():
        fail(f"plan {args.plan!r} does not exist under {root}")
    ids = list(args.units)
    if len(set(ids)) != len(ids):
        fail("unit ids must be unique")
    path = state_dir() / LEDGER_NAME
    if path.exists() and not args.force:
        existing = read_ledger(path)
        if existing.get("plan") != args.plan:
            fail(
                f"{path} names plan {existing.get('plan')!r}; pass --force after the user "
                "confirms it may be replaced"
            )
        fail(f"{path} already tracks this plan; `set` updates it, --force starts over")
    ledger = {
        "contract": CONTRACT,
        "plan": args.plan,
        "resume": [],
        "units": [
            {"id": uid, "status": "pending", "commit": None, "verified_at": None, "note": None} for uid in ids
        ],
    }
    write_ledger(path, ledger)
    print(f"ledger: {path} tracks {args.plan} with {len(ids)} pending units")


def cmd_set(args: argparse.Namespace) -> None:
    directory = state_dir()
    path = directory / LEDGER_NAME
    ledger = read_ledger(path)
    matches = [unit for unit in ledger["units"] if unit["id"] == args.unit]
    if not matches:
        fail(f"{args.unit!r} is not a unit of {ledger['plan']}")
    unit = matches[0]
    unit["status"] = args.status
    if args.status == "passed":
        unit["commit"] = args.commit or git_output("rev-parse", "--short=8", "HEAD")
        unit["verified_at"] = args.verified_at or receipt_verified_at(directory)
    else:
        if args.commit:
            unit["commit"] = args.commit
        if args.verified_at:
            unit["verified_at"] = args.verified_at
    if args.note is not None:
        unit["note"] = args.note or None
    if args.status == "blocked" and not unit["note"]:
        fail("a blocked unit needs --note with the reason")
    write_ledger(path, ledger)
    print(f"ledger: {args.unit} {args.status}; resume {ledger['resume']}")


def cmd_show(_: argparse.Namespace) -> None:
    path = state_dir() / LEDGER_NAME
    if not path.exists():
        print(f"ledger: {path} does not exist")
        return
    sys.stdout.write(path.read_text(encoding="utf-8"))


def cmd_checkpoint(args: argparse.Namespace) -> None:
    if ":" in args.key or not args.key:
        fail("the key is one word, such as implemented, review, or compound")
    if "\n" in args.value:
        fail("the value is one line")
    path = state_dir() / CHECKPOINTS_NAME
    line = f"{args.key}: {args.value}\n"
    if args.replace or not path.exists():
        write_whole(path, line)
    else:
        with path.open("a", encoding="utf-8") as out:
            out.write(line)
    print(f"checkpoints: {line.rstrip()}")


def main(argv: list[str]) -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    commands = parser.add_subparsers(dest="command", required=True)

    init = commands.add_parser("init", help="write a fresh ledger with every unit pending")
    init.add_argument("plan", help="plan path relative to the tree root")
    init.add_argument("units", nargs="+", metavar="UNIT", help="unit ids in plan order")
    init.add_argument("--force", action="store_true", help="replace a ledger that exists")
    init.set_defaults(run=cmd_init)

    setter = commands.add_parser("set", help="update one unit's status")
    setter.add_argument("unit")
    setter.add_argument("status", choices=STATUSES)
    setter.add_argument("--commit", help="commit that landed the unit (default: HEAD when passed)")
    setter.add_argument("--verified-at", help="verifier time (default: the receipt's, when passed)")
    setter.add_argument("--note", help="one line the next unit needs; an empty string clears it")
    setter.set_defaults(run=cmd_set)

    show = commands.add_parser("show", help="print the ledger")
    show.set_defaults(run=cmd_show)

    checkpoint = commands.add_parser("checkpoint", help="record a `key: value` line for close")
    checkpoint.add_argument("key")
    checkpoint.add_argument("value")
    checkpoint.add_argument("--replace", action="store_true", help="write the file anew instead of appending")
    checkpoint.set_defaults(run=cmd_checkpoint)

    args = parser.parse_args(argv[1:])
    args.run(args)


if __name__ == "__main__":
    main(sys.argv)
