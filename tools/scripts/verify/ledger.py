"""Maintain the plan status ledger and the checkpoints file of a worktree.

Both files live in the worktree's git directory, beside the verifier
receipt, and `verify check-plan-status` validates the ledger on every verifier
run. The skills update them through this command rather than by writing the
files themselves: the command resolves the directory with `git rev-parse`
and writes the whole file, so a caller names only the unit, the status,
and the note, and the `verify-change` contract stays in one place.

    run.py verify ledger init docs/plans/<plan>.md U1 U2 U3
    run.py verify ledger set U1 in_progress
    run.py verify ledger set U1 passed --note "Diagnostics keep the source span."
    run.py verify ledger set U2 blocked --note "<reason>"
    run.py verify ledger show
    run.py verify ledger checkpoint review "accept"
    run.py verify ledger checkpoint --replace implemented "<request in a few words>"

`set ... passed` takes the commit from `HEAD` and `verified_at` from the
receipt of the verifier run that just passed, unless `--commit` or
`--verified-at` overrides them. `set ... in_progress` records `HEAD` as the
unit's `base`, and `passed` refuses a commit that is not in this branch or
holds nothing committed since that base: a commit from before the unit
started cannot be the unit's work, whatever it touched. Without
`--verified-at` it also refuses a receipt older than the unit's commit,
since that run verified a tree without the unit's work. `resume` is recomputed on every write: the
units in progress, else the first pending unit, else empty. A note is kept
until `--note` replaces it; `--note ""` clears it.
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import sys
import tempfile
from pathlib import Path

from lib import plans, proc

CHECKPOINTS_NAME = "flowseer-checkpoints"
RECEIPT_NAME = "flowseer-verification-receipt"


def fail(message: str) -> None:
    print(f"ledger: {message}", file=sys.stderr)
    sys.exit(1)


def git_output(*args: str) -> str:
    done = proc.run(["git", *args])
    if done.code != 0:
        fail(f"git {' '.join(args)} failed: {done.stderr.strip()}")
    return done.stdout.strip()


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
    if not isinstance(ledger, dict) or ledger.get("contract") != plans.LEDGER_CONTRACT:
        fail(f"{path} does not carry contract {plans.LEDGER_CONTRACT!r}")
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


def head() -> str:
    return git_output("rev-parse", "--short=8", "HEAD")


def check_unit_commit(unit: dict, commit: str) -> None:
    if proc.run(["git", "merge-base", "--is-ancestor", commit, "HEAD"]).code != 0:
        fail(f"commit {commit} is not in this branch's history")
    base = unit.get("base")
    if not base:
        return
    since = proc.run(["git", "rev-list", "--count", f"{base}..{commit}"])
    if since.code != 0:
        fail(f"cannot compare {commit} with the unit's base {base}: {since.stderr.strip()}")
    if since.stdout.strip() == "0":
        fail(
            f"commit {commit} holds nothing committed since {unit['id']} went in_progress at {base}; "
            "commit the unit's work first. When the tree already held it, the plan is wrong about "
            "the tree: record that under the plan's Open questions and commit the plan"
        )


def check_receipt_fresh(unit: dict, verified_at: str) -> None:
    # implement commits a unit before it verifies, and a failed run writes
    # no receipt, so a receipt older than the unit's commit is an earlier
    # unit's run.
    commit = unit.get("commit")
    if not commit:
        return
    try:
        verified = datetime.datetime.strptime(verified_at, "%Y-%m-%dT%H:%M:%SZ").replace(
            tzinfo=datetime.timezone.utc
        )
    except ValueError:
        fail(f"the receipt's verified_at {verified_at!r} is not YYYY-MM-DDTHH:MM:SSZ")
    committed = datetime.datetime.fromisoformat(git_output("log", "-1", "--format=%cI", commit))
    if verified < committed:
        fail(
            f"the receipt's verified_at {verified_at} is older than {unit['id']}'s commit {commit} "
            f"({committed.isoformat()}); run the verifier on the unit's tree first"
        )


def cmd_init(args: argparse.Namespace) -> None:
    root = Path(git_output("rev-parse", "--show-toplevel"))
    if not (root / args.plan).is_file():
        fail(f"plan {args.plan!r} does not exist under {root}")
    ids = list(args.units)
    if len(set(ids)) != len(ids):
        fail("unit ids must be unique")
    path = state_dir() / plans.LEDGER_NAME
    if path.exists() and not args.force:
        existing = read_ledger(path)
        if existing.get("plan") != args.plan:
            fail(
                f"{path} names plan {existing.get('plan')!r}; pass --force after the user "
                "confirms it may be replaced"
            )
        fail(f"{path} already tracks this plan; `set` updates it, --force starts over")
    ledger = {
        "contract": plans.LEDGER_CONTRACT,
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
    path = directory / plans.LEDGER_NAME
    ledger = read_ledger(path)
    matches = [unit for unit in ledger["units"] if unit["id"] == args.unit]
    if not matches:
        fail(f"{args.unit!r} is not a unit of {ledger['plan']}")
    unit = matches[0]
    if args.status == "in_progress" and unit["status"] != "in_progress":
        # Kept across a repeated `in_progress`, so a resumed session does not
        # move the base past the commit it already made for the unit.
        unit["base"] = head()
    unit["status"] = args.status
    if args.status == "passed":
        unit["commit"] = args.commit or head()
        check_unit_commit(unit, unit["commit"])
        if args.verified_at:
            unit["verified_at"] = args.verified_at
        else:
            unit["verified_at"] = receipt_verified_at(directory)
            check_receipt_fresh(unit, unit["verified_at"])
    else:
        if args.status == "pending":
            # The way out for a unit marked in progress after its commit.
            unit.pop("base", None)
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
    path = state_dir() / plans.LEDGER_NAME
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


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py verify ledger", description=__doc__.splitlines()[0])
    commands = parser.add_subparsers(dest="command", required=True)

    init = commands.add_parser("init", help="write a fresh ledger with every unit pending")
    init.add_argument("plan", help="plan path relative to the tree root")
    init.add_argument("units", nargs="+", metavar="UNIT", help="unit ids in plan order")
    init.add_argument("--force", action="store_true", help="replace a ledger that exists")
    init.set_defaults(run=cmd_init)

    setter = commands.add_parser("set", help="update one unit's status")
    setter.add_argument("unit")
    setter.add_argument("status", choices=plans.UNIT_STATUSES)
    setter.add_argument("--commit", help="commit that landed the unit (default: HEAD when passed)")
    setter.add_argument("--verified-at", help="verifier time (default: the receipt's, when passed)")
    setter.add_argument("--note", help="one line the next unit needs; an empty string clears it")
    setter.set_defaults(run=cmd_set)

    show = commands.add_parser("show", help="print the ledger")
    show.set_defaults(run=cmd_show)

    checkpoint = commands.add_parser("checkpoint", help="record a `key: value` line for land")
    checkpoint.add_argument("key")
    checkpoint.add_argument("value")
    checkpoint.add_argument("--replace", action="store_true", help="write the file anew instead of appending")
    checkpoint.set_defaults(run=cmd_checkpoint)

    args = parser.parse_args(argv)
    args.run(args)
    return 0
