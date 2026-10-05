#!/usr/bin/env python3
"""Own the state file beside each plan under docs/plans/.

A plan's prose is Markdown. Its machine state lives in a JSON file beside
it, `<name>-plan.state.json` for `<name>-plan.md`, and this script is that
file's only writer. Each transition is one command, and a command refuses
to leave a file `check` would reject. Readers import this module, so
`next`, `drive`, and the verifier share one test for a finished phase and
one for a plan sent back to `plan`.

    plan_record.py init <plan> [--needs-decisions] [--parent <p> [--after <q>...]]
    plan_record.py ready <plan>
    plan_record.py after <plan> [<path>...]
    plan_record.py implemented <plan> --units <n> --from <t> --to <t> [--landed <first>..<last>]
    plan_record.py partial <plan> --units <n> --from <t> --to <t> --note <units left and why>
    plan_record.py review <plan> <verdict> [--rounds <n>]
    plan_record.py compound <plan> <outcome>
    plan_record.py replan <plan> [--needs-decisions]
    plan_record.py supersede <plan> --by <path>
    plan_record.py abandon <plan>
    plan_record.py retire <plan>
    plan_record.py show <plan> [--json]
    plan_record.py is <plan> <field>=<value> | <field>!=<value>
    plan_record.py check [<path>...]

A phase names its parent, the phases it runs after, and the commit range it
landed as. A parent lists its phases on disk under `phases` and the ones
`retire` deleted under `retired`, and stores nothing else about them. A
parent's status is computed: `implemented` once no phase is left on disk
and a retired one carries a range, `planned` until then. A stored
`superseded` or `abandoned` stands.

`is` exits 0 when the test holds, 1 when it does not, and 2 when it cannot
be evaluated. `null` stands for an unset field, and `status` is the
computed one. `check` without paths reads every plan, and with paths only
the plans those paths belong to.
"""

from __future__ import annotations

import argparse
import copy
import datetime
import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

CONTRACT = "flowseer-plan-state/v1"
PLANS = "docs/plans"
LEDGER_NAME = "flowseer-plan-status.json"
STATUSES = ("planned", "partially-implemented", "implemented", "superseded", "abandoned")
FINAL = ("implemented", "superseded", "abandoned")
READINESS = ("implementation-ready", "needs-decisions")
VERDICTS = ("accept", "accept after fixes", "fixes needed", "rework")
ACCEPTED = ("accept", "accept after fixes")
MOVED = ("status", "artifact_readiness", "review", "review_rounds", "compound", "superseded_by", "parent")
SHA = re.compile(r"[0-9a-f]{7,40}")
TIME = re.compile(r"\d{4}-\d\d-\d\dT\d\d:\d\d(:\d\d)?Z")
BLANK = {
    "contract": CONTRACT,
    "status": "planned",
    "readiness": "implementation-ready",
    "review": None,
    "review_rounds": 0,
    "compound": None,
    "outcome": None,
    "superseded_by": None,
    "parent": None,
    "after": [],
    "landed": None,
    "phases": [],
    "retired": [],
}


class StateError(Exception):
    """A state file that cannot be used as it stands."""


class MissingState(StateError):
    """The plan has no state file."""


class InvalidState(StateError):
    """The state file does not hold the keys and values the contract names."""


def git(root: Path, *args: str) -> subprocess.CompletedProcess:
    return subprocess.run(["git", "-C", str(root), *args], capture_output=True, text=True, check=False)


def tree_root() -> Path:
    out = subprocess.run(["git", "rev-parse", "--show-toplevel"], capture_output=True, text=True, check=False)
    if out.returncode != 0:
        raise StateError("not inside a git work tree")
    return Path(out.stdout.strip())


def plan_of(path: str) -> str | None:
    """The plan a path belongs to: itself for a plan, the plan beside it
    for a state file, None for anything else."""
    path = Path(path).as_posix()
    if path.endswith("-plan.state.json"):
        return path.removesuffix(".state.json") + ".md"
    return path if path.endswith("-plan.md") else None


def state_path(plan: str) -> str:
    return plan.removesuffix(".md") + ".state.json"


def is_plan(value: object) -> bool:
    return isinstance(value, str) and plan_of(value) == value


def is_time(value: object) -> bool:
    if not (isinstance(value, str) and TIME.fullmatch(value)):
        return False
    try:
        datetime.datetime.strptime(value, "%Y-%m-%dT%H:%M:%SZ" if value.count(":") == 2 else "%Y-%m-%dT%H:%MZ")
    except ValueError:
        return False
    return True


def is_range(value: object) -> bool:
    return (
        isinstance(value, dict)
        and set(value) == {"first", "last"}
        and all(isinstance(sha, str) and SHA.fullmatch(sha) for sha in value.values())
    )


def is_outcome(value: object) -> bool:
    return (
        isinstance(value, dict)
        and set(value) == {"units", "from", "to", "note"}
        and type(value["units"]) is int
        and value["units"] > 0
        and all(is_time(value[key]) for key in ("from", "to"))
        and isinstance(value["note"], str)
    )


def is_retired(value: object) -> bool:
    return (
        isinstance(value, dict)
        and set(value) == {"plan", "status", "landed"}
        and is_plan(value["plan"])
        and value["status"] in FINAL
        and (is_range(value["landed"]) if value["status"] == "implemented" else value["landed"] is None)
    )


def shape_faults(state: object) -> list[str]:
    """What keeps one file, read alone, from being a state file."""
    if isinstance(state, InvalidState):
        return [str(state)]
    if not isinstance(state, dict):
        return ["the top level must be an object"]
    faults = [f"unknown key {key!r}" for key in state if key not in BLANK]
    faults += [f"missing key {key!r}" for key in BLANK if key not in state]
    if faults:
        return faults
    rounds = state["review_rounds"]
    rules = [
        (state["contract"] == CONTRACT, f"contract must be {CONTRACT!r}"),
        (state["status"] in STATUSES, f"status must be one of {', '.join(STATUSES)}"),
        (state["readiness"] in READINESS, f"readiness must be one of {', '.join(READINESS)}"),
        (state["review"] is None or state["review"] in VERDICTS, f"review must be null or one of {', '.join(VERDICTS)}"),
        (type(rounds) is int and rounds >= 0, "review_rounds must be a count"),
        (state["compound"] is None or (isinstance(state["compound"], str) and state["compound"] != ""),
         "compound must be null or a non-empty string"),
        (state["outcome"] is None or is_outcome(state["outcome"]),
         "outcome must be null or {units, from, to, note} with a positive count and UTC times"),
        (state["superseded_by"] is None or (isinstance(state["superseded_by"], str) and state["superseded_by"] != ""),
         "superseded_by must be null or a path"),
        (state["parent"] is None or is_plan(state["parent"]), "parent must be null or a plan path"),
        (state["landed"] is None or is_range(state["landed"]), "landed must be null or {first, last} commit ids"),
    ]
    for key in ("after", "phases"):
        paths = state[key]
        rules.append((isinstance(paths, list) and all(is_plan(path) for path in paths), f"{key} must list plan paths"))
    rules.append(
        (isinstance(state["retired"], list) and all(is_retired(entry) for entry in state["retired"]),
         "retired must list {plan, status, landed} with a final status and a range only beside implemented")
    )
    return [message for holds, message in rules if not holds]


def combination_faults(plan: str, state: dict, states: dict[str, dict], on_disk: set[str]) -> list[str]:
    """What makes a well-shaped file illegal beside the other plans."""
    faults = []
    status, parent = state["status"], state["parent"]
    if state["landed"] and status != "implemented":
        faults.append(f"landed is set while status is {status!r}; only an implemented phase carries a range")
    if state["landed"] and parent is None:
        faults.append("landed is set with parent null; only a phase carries a range")
    if status == "implemented" and parent and not state["landed"]:
        faults.append("status is implemented with parent set and landed null; an implemented phase carries its range")
    if (state["phases"] or state["retired"]) and parent:
        faults.append("phases or retired is non-empty with parent set; a phase has no phases of its own")
    if (state["phases"] or state["retired"]) and status in ("implemented", "partially-implemented"):
        faults.append(f"a parent's status is computed from its phases; the stored status must not be {status!r}")
    if (status == "superseded") != (state["superseded_by"] is not None):
        faults.append("superseded_by is set exactly when status is superseded")
    if parent is None:
        if state["after"]:
            faults.append("after is set with parent null; only a phase has prerequisites")
    elif parent not in states:
        faults.append(f"parent {parent} has no legal state file")
    else:
        holder = states[parent]
        if plan not in holder["phases"]:
            faults.append(f"parent {parent} does not list this plan under phases")
        known = set(holder["phases"]) | {entry["plan"] for entry in holder["retired"]}
        for path in state["after"]:
            if path == plan or path not in known:
                faults.append(f"after names {path}, which is neither in the phases nor in the retired list of {parent}")
    for path in state["phases"]:
        if path not in states or states[path]["parent"] != plan:
            faults.append(f"phases names {path}, whose state does not name this plan as parent")
    for entry in state["retired"]:
        if entry["plan"] in on_disk:
            faults.append(f"retired names {entry['plan']}, which is still on disk")
    return faults


def moved_keys(text: str) -> list[str]:
    """The frontmatter keys of a plan that belong in its state file."""
    if not text.startswith("---\n"):
        return []
    head = text[4:].split("\n---", 1)[0]
    lines = [line for line in head.splitlines() if ":" in line and not line.startswith(" ")]
    keys = {line.partition(":")[0].strip().strip("'\"") for line in lines}
    return [key for key in MOVED if key in keys]


def read_tree(root: Path) -> tuple[dict[str, list[str]], dict[str, object]]:
    """The plans on disk, each with the state keys its frontmatter still
    carries, and each state file parsed or the error it gave."""
    directory = root / PLANS
    plans = {
        f"{PLANS}/{path.name}": moved_keys(path.read_text(encoding="utf-8")) for path in directory.glob("*-plan.md")
    }
    raw: dict[str, object] = {}
    for path in directory.glob("*-plan.state.json"):
        plan = f"{PLANS}/{path.name.removesuffix('.state.json')}.md"
        try:
            raw[plan] = json.loads(path.read_text(encoding="utf-8"))
        except ValueError as error:
            raw[plan] = InvalidState(f"not valid JSON: {error}")
    return plans, raw


def problems(plans: dict[str, list[str]], raw: dict[str, object]) -> list[tuple[str, str]]:
    """Every fault in a tree, as (file, rule), in file order."""
    found = [(plan, "has no state file; run `plan_record.py init`") for plan in plans.keys() - raw.keys()]
    # A field kept in both places has two writers, and every reader takes
    # the state file's.
    found += [
        (plan, f"frontmatter carries {', '.join(keys)}, which {state_path(plan)} holds")
        for plan, keys in plans.items()
        if keys
    ]
    states = {}
    for plan, state in raw.items():
        if plan not in plans:
            found.append((state_path(plan), "has no plan beside it"))
        faults = shape_faults(state)
        found += [(state_path(plan), fault) for fault in faults]
        if not faults:
            states[plan] = state
    on_disk = plans.keys() | raw.keys()
    for plan, state in states.items():
        found += [(state_path(plan), fault) for fault in combination_faults(plan, state, states, on_disk)]
    return sorted(found)


def load(plan: str, root: Path | None = None) -> dict:
    path = (root or tree_root()) / state_path(plan)
    if not path.is_file():
        raise MissingState(f"{state_path(plan)} does not exist; run `plan_record.py init {plan}`")
    try:
        state = json.loads(path.read_text(encoding="utf-8"))
    except ValueError as error:
        raise InvalidState(f"{state_path(plan)}: not valid JSON: {error}") from error
    faults = shape_faults(state)
    if faults:
        raise InvalidState(f"{state_path(plan)}: {'; '.join(faults)}")
    return state


def computed_status(state: dict) -> str:
    if state["status"] != "planned" or not (state["phases"] or state["retired"]):
        return state["status"]
    landed = any(entry["landed"] for entry in state["retired"])
    return "implemented" if landed and not state["phases"] else "planned"


def status(plan: str, root: Path | None = None) -> str:
    """The plan's status, a parent's computed from its phases."""
    return computed_status(load(plan, root))


def on_main(sha: str, root: Path | None = None) -> bool:
    return git(root or tree_root(), "merge-base", "--is-ancestor", sha, "main").returncode == 0


def sent_back(state: dict) -> bool:
    """An implemented plan owed a re-plan: its review ended in rework, or
    `plan` took it up and stopped on a decision."""
    return state["status"] == "implemented" and (
        state["review"] == "rework" or state["readiness"] == "needs-decisions"
    )


def finished(phase: str, root: Path | None = None, _seen: frozenset = frozenset()) -> bool:
    """Whether a phase frees the phases that run after it.

    A landed range records implementation only. A dependent starts once the
    phase is on main, retired with a range, or owed nothing but its land,
    so a review's fix round cannot rewrite files the dependent edits.
    """
    root = root or tree_root()
    try:
        state = load(phase, root)
    except MissingState:
        _, raw = read_tree(root)
        return any(
            entry["plan"] == phase and entry["landed"]
            for holder in raw.values()
            if not shape_faults(holder)
            for entry in holder["retired"]
        )
    if state["landed"] and on_main(state["landed"]["last"], root):
        return True
    return (
        state["status"] == "implemented"
        and state["readiness"] == "implementation-ready"
        and state["review"] in ACCEPTED
        and state["compound"] is not None
        and phase not in _seen
        and all(finished(path, root, _seen | {phase}) for path in state["after"])
    )


def fail(message: str, code: int = 1) -> None:
    print(f"plan state: {message}", file=sys.stderr)
    sys.exit(code)


def write_state(root: Path, plan: str, state: dict) -> None:
    # Written whole, then renamed: a reader that races the write sees the
    # previous file, never a truncated one.
    path = root / state_path(plan)
    handle, tmp = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    with os.fdopen(handle, "w", encoding="utf-8") as out:
        out.write(json.dumps({key: state[key] for key in BLANK}, indent=2) + "\n")
    os.replace(tmp, path)


def transition(root: Path, changes: dict[str, dict], retire: str | None = None) -> None:
    """Write the changed states, and delete a retired plan, unless the
    tree would then hold a fault it did not hold before or any fault in a
    file this command writes."""
    plans, raw = read_tree(root)
    before = set(problems(plans, raw))
    raw.update(changes)
    if retire:
        plans.pop(retire, None)
        raw.pop(retire, None)
    written = {state_path(plan) for plan in changes} | set(changes)
    faults = [fault for fault in problems(plans, raw) if fault not in before or fault[0] in written]
    if faults:
        fail("refused, the result would be illegal:\n" + "\n".join(f"  {where}: {rule}" for where, rule in faults))
    # A command that writes two files leaves both or neither: a phase its
    # parent does not list is a fault only a hand-edit could repair.
    paths = [root / state_path(plan) for plan in changes]
    saved = {path: path.read_bytes() if path.exists() else None for path in paths}
    try:
        for plan, state in changes.items():
            write_state(root, plan, state)
        if retire:
            removed = git(root, "rm", "-q", "-f", "--", retire, state_path(retire))
            if removed.returncode != 0:
                raise StateError(f"git rm failed: {removed.stderr.strip()}")
    except (OSError, StateError) as error:
        for path, content in saved.items():
            if content is None:
                path.unlink(missing_ok=True)
            else:
                path.write_bytes(content)
        fail(f"nothing changed: {error}")
    if retire and changes:
        holders = [state_path(plan) for plan in changes]
        staged = git(root, "add", "--", *holders)
        if staged.returncode != 0:
            fail(
                f"{retire} is removed and staged, but staging {', '.join(holders)} failed; "
                f"run `git add` on it before the commit: {staged.stderr.strip()}"
            )


def plan_arg(root: Path, text: str) -> str:
    try:
        rel = Path(text).resolve().relative_to(root.resolve()).as_posix()
    except ValueError:
        fail(f"{text} is outside {root}", 2)
    plan = plan_of(rel)
    if plan is None or not plan.startswith(PLANS + "/"):
        fail(f"{text} is not a plan under {PLANS}/", 2)
    return plan


def stored(root: Path, plan: str) -> dict:
    try:
        return load(plan, root)
    except StateError as error:
        fail(str(error), 2)
    raise AssertionError("unreachable")


def outcome(args: argparse.Namespace, note: str) -> dict:
    for value in (args.start, args.end):
        if not is_time(value):
            fail(f"{value!r} is not a UTC time such as 2026-09-11T10:02Z")
    return {"units": args.units, "from": args.start, "to": args.end, "note": note}


def cmd_init(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    if (root / state_path(plan)).exists():
        fail(f"{state_path(plan)} exists; `replan` resets it")
    if args.after and not args.parent:
        fail("--after needs --parent; only a phase has prerequisites")
    state = copy.deepcopy(BLANK)
    if args.needs_decisions:
        state["readiness"] = "needs-decisions"
    changes = {plan: state}
    if args.parent:
        state["parent"] = plan_arg(root, args.parent)
        state["after"] = [plan_arg(root, path) for path in args.after]
        holder = stored(root, state["parent"])
        holder["phases"].append(plan)
        changes[state["parent"]] = holder
    transition(root, changes)
    print(f"plan state: {state_path(plan)} written, {state['readiness']}")


def cmd_ready(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state["readiness"] = "implementation-ready"
    transition(root, {plan: state})
    print(f"plan state: {plan} implementation-ready")


def cmd_after(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state["after"] = [plan_arg(root, path) for path in args.paths]
    transition(root, {plan: state})
    print(f"plan state: {plan} runs after {', '.join(state['after']) or 'nothing'}")


def cmd_implemented(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    if state["parent"] and not args.landed:
        fail("a phase is implemented with --landed <first>..<last>, the range of this run")
    state["status"] = "implemented"
    state["outcome"] = outcome(args, "")
    if args.landed:
        first, sep, last = args.landed.partition("..")
        if not (sep and SHA.fullmatch(first) and SHA.fullmatch(last)):
            fail(f"--landed takes <first>..<last> commit ids, got {args.landed!r}")
        if git(root, "merge-base", "--is-ancestor", last, "HEAD").returncode != 0:
            fail(f"--landed ends at {last}, which is no ancestor of HEAD")
        state["landed"] = {"first": first, "last": last}
    transition(root, {plan: state})
    print(f"plan state: {plan} implemented, {args.units} units")


def cmd_partial(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    if not args.note.strip():
        fail("--note names the units left and why")
    state = stored(root, plan)
    state["status"] = "partially-implemented"
    state["outcome"] = outcome(args, args.note)
    transition(root, {plan: state})
    print(f"plan state: {plan} partially-implemented, {args.units} units")


def cmd_review(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state["review"] = args.verdict
    if args.rounds is not None:
        state["review_rounds"] = args.rounds
    transition(root, {plan: state})
    print(f"plan state: {plan} review: {args.verdict}, {state['review_rounds']} rounds")


def cmd_compound(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state["compound"] = args.outcome
    transition(root, {plan: state})
    print(f"plan state: {plan} compound: {args.outcome}")


def cmd_replan(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state.update(status="planned", review=None, review_rounds=0, compound=None, outcome=None, landed=None)
    state["superseded_by"] = None
    state["readiness"] = "needs-decisions" if args.needs_decisions else "implementation-ready"
    # The ledger's passed units would make implement skip a redesigned unit
    # that kept its id, so a ledger that cannot be read stops the re-plan.
    ledger = Path(git(root, "rev-parse", "--absolute-git-dir").stdout.strip()) / LEDGER_NAME
    tracked = False
    if ledger.exists():
        try:
            tracked = json.loads(ledger.read_text(encoding="utf-8")).get("plan") == plan
        except (OSError, ValueError, AttributeError) as error:
            fail(f"cannot tell whether {ledger} tracks this plan ({error}); repair or delete it first")
    transition(root, {plan: state})
    print(f"plan state: {plan} planned, {state['readiness']}")
    if tracked:
        ledger.unlink()
        print(f"plan state: deleted {ledger}, which tracked the previous run")


def cmd_supersede(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state["status"] = "superseded"
    state["superseded_by"] = args.by
    transition(root, {plan: state})
    print(f"plan state: {plan} superseded by {args.by}")


def cmd_abandon(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state["status"] = "abandoned"
    state["superseded_by"] = None
    transition(root, {plan: state})
    print(f"plan state: {plan} abandoned")


def cmd_retire(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    final = computed_status(state)
    if final not in FINAL:
        fail(f"{plan} reads {final}; only an implemented, superseded, or abandoned plan retires")
    if git(root, "status", "--porcelain", "--", plan).stdout.strip():
        fail(f"{plan} has uncommitted changes, which retiring it would delete; commit them first")
    changes = {}
    if state["parent"]:
        holder = stored(root, state["parent"])
        holder["phases"] = [path for path in holder["phases"] if path != plan]
        holder["retired"].append({"plan": plan, "status": final, "landed": state["landed"]})
        changes[state["parent"]] = holder
    transition(root, changes, retire=plan)
    done = state["outcome"]
    if final == "implemented" and done:
        print(f"> Implemented. {done['units']} units, {done['from']} to {done['to']}.")
    elif final == "superseded":
        print(f"> Superseded by {state['superseded_by']}.")
    else:
        print(f"> {final.capitalize()}.")
    print(f"review: {state['review'] or 'none'}")
    print(f"compound: {state['compound'] or 'none'}")


def cmd_show(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state["status"] = computed_status(state)
    if args.json:
        print(json.dumps(state, indent=2))
        return
    print(plan)
    for key in BLANK:
        value = state[key]
        print(f"  {key}: {value if isinstance(value, (str, int)) else json.dumps(value)}")


def cmd_is(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    field, sep, wanted = args.test.partition("=")
    negated = field.endswith("!")
    field = field.removesuffix("!")
    if not sep or field not in BLANK:
        fail(f"{args.test!r} is not <field>=<value> over {', '.join(BLANK)}", 2)
    state = stored(root, plan)
    value = computed_status(state) if field == "status" else state[field]
    text = value if isinstance(value, str) else json.dumps(value)
    sys.exit(0 if (text == wanted) != negated else 1)


def cmd_check(root: Path, args: argparse.Namespace) -> None:
    faults = problems(*read_tree(root))
    if args.paths:
        named = [Path(path).resolve().as_posix() for path in args.paths]

        def chosen(where: str) -> bool:
            plan = (root.resolve() / plan_of(where)).as_posix()
            return any(plan == plan_of(path) or plan.startswith(path.rstrip("/") + "/") for path in named)

        faults = [fault for fault in faults if chosen(fault[0])]
    for where, rule in faults:
        print(f"plan state: {where}: {rule}", file=sys.stderr)
    if faults:
        sys.exit(1)
    print("plan state: every state file checked is legal.")


def main(argv: list[str]) -> None:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    commands = parser.add_subparsers(dest="command", required=True)

    def command(name: str, run, summary: str) -> argparse.ArgumentParser:
        sub = commands.add_parser(name, help=summary)
        sub.set_defaults(run=run)
        if name != "check":
            sub.add_argument("plan", help="plan path, or the state file beside it")
        return sub

    def run_times(sub: argparse.ArgumentParser) -> None:
        sub.add_argument("--units", type=int, required=True, help="units that landed")
        sub.add_argument("--from", dest="start", required=True, help="first verified_at of the run")
        sub.add_argument("--to", dest="end", required=True, help="last verified_at of the run")

    init = command("init", cmd_init, "write the state file of a new plan")
    init.add_argument("--needs-decisions", action="store_true")
    init.add_argument("--parent", help="the parent plan this phase belongs to")
    init.add_argument("--after", nargs="+", default=[], metavar="PLAN", help="phases this one runs after")
    command("ready", cmd_ready, "set readiness to implementation-ready")
    after = command("after", cmd_after, "replace a phase's prerequisites")
    after.add_argument("paths", nargs="*", metavar="PLAN")
    implemented = command("implemented", cmd_implemented, "record a finished implement run")
    run_times(implemented)
    implemented.add_argument("--landed", metavar="FIRST..LAST", help="a phase's commit range")
    partial = command("partial", cmd_partial, "record an implement run that left units open")
    run_times(partial)
    partial.add_argument("--note", required=True, help="the units left and why")
    review = command("review", cmd_review, "record a review verdict")
    review.add_argument("verdict", choices=VERDICTS)
    review.add_argument("--rounds", type=int, help="fix rounds run so far; the stored count stays when omitted")
    compound = command("compound", cmd_compound, "record the compound outcome")
    compound.add_argument("outcome", help="a solution path, `no lesson`, or `observation logged`")
    replan = command("replan", cmd_replan, "return a plan to planned and clear its last run")
    replan.add_argument("--needs-decisions", action="store_true")
    supersede = command("supersede", cmd_supersede, "mark a plan superseded")
    supersede.add_argument("--by", required=True, help="the plan or record that replaces it")
    command("abandon", cmd_abandon, "mark a plan abandoned")
    command("retire", cmd_retire, "git rm a finished plan and its state, and print its commit body lines")
    show = command("show", cmd_show, "print the state")
    show.add_argument("--json", action="store_true")
    test = command("is", cmd_is, "exit 0 when a field holds a value")
    test.add_argument("test", metavar="FIELD=VALUE")
    check = command("check", cmd_check, "fail on an illegal state file")
    check.add_argument("paths", nargs="*", metavar="PATH")

    args = parser.parse_args(argv[1:])
    try:
        root = tree_root()
    except StateError as error:
        fail(str(error), 2)
    args.run(root, args)


if __name__ == "__main__":
    main(sys.argv)
