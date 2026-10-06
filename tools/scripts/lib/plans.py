"""Read the plan files under docs/plans/ and the state file beside each plan.

A plan's prose is Markdown. Its machine state lives in a JSON file beside
it, `<name>-plan.state.json` for `<name>-plan.md`. This module is the read
side: every reader shares one test for a legal file, a finished phase, and a
plan sent back to `plan`. The `plan record` command writes the file.
"""

import datetime
import json
import re
from pathlib import Path

from lib import proc, repo

CONTRACT = "flowseer-plan-state/v1"
PLANS = "docs/plans"
STATUSES = ("planned", "partially-implemented", "implemented", "superseded", "abandoned")
FINAL = ("implemented", "superseded", "abandoned")
READINESS = ("implementation-ready", "needs-decisions")
VERDICTS = ("accept", "accept after fixes", "fixes needed", "rework")
ACCEPTED = ("accept", "accept after fixes")
MOVED = ("status", "artifact_readiness", "review", "review_rounds", "compound", "superseded_by", "parent")
SHA = re.compile(r"[0-9a-f]{7,40}")
BRANCH = re.compile(r"[A-Za-z0-9][A-Za-z0-9._/-]*")
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
    "branch": None,
    "parent": None,
    "after": [],
    "landed": None,
    "phases": [],
    "retired": [],
}

LEDGER_NAME = "flowseer-plan-status.json"
LEDGER_CONTRACT = "flowseer-plan-status/v1"
UNIT_STATUSES = ("pending", "in_progress", "passed", "blocked")

# Every heading under docs/plans/ is `### U<n>. ` or `### U3a. `, the shape
# the plan skill says to copy.
UNIT = re.compile(r"^### (U\d+[a-z]?)[.:]\s")

INIT_COMMAND = "uv run tools/scripts/run.py plan record init"


class StateError(Exception):
    """A state file that cannot be used as it stands."""


class MissingState(StateError):
    """The plan has no state file."""


class InvalidState(StateError):
    """The state file does not hold the keys and values the contract names."""


def unit_ids(text: str) -> list[str]:
    """The ids of the plan's unit headings, in order."""
    return [match.group(1) for line in text.splitlines() if (match := UNIT.match(line))]


def title(text: str) -> str:
    """The first `# ` heading without its ` - Plan` suffix, empty when absent."""
    heading = next((line[2:].strip() for line in text.splitlines() if line.startswith("# ")), "")
    return heading.removesuffix(" - Plan")


def git(root: Path, *args: str) -> proc.Result:
    return proc.run(["git", "-C", str(root), *args])


def tree_root() -> Path:
    try:
        return repo.root()
    except RuntimeError as error:
        raise StateError("not inside a git work tree") from error


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
        (state["branch"] is None or (isinstance(state["branch"], str) and BRANCH.fullmatch(state["branch"]) is not None),
         "branch must be null or a branch-name-safe string"),
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
    if (state["phases"] or state["retired"]) and state["branch"]:
        named = [*state["phases"], *(entry["plan"] for entry in state["retired"])]
        faults.append(
            f"branch is set on a plan with phases or retired; a parent has no branch, "
            f"each phase carries its own: {', '.join(named)}"
        )
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
    found = [(plan, f"has no state file; run `{INIT_COMMAND}`") for plan in plans.keys() - raw.keys()]
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
        raise MissingState(f"{state_path(plan)} does not exist; run `{INIT_COMMAND} {plan}`")
    try:
        state = json.loads(path.read_text(encoding="utf-8"))
    except ValueError as error:
        raise InvalidState(f"{state_path(plan)}: not valid JSON: {error}") from error
    faults = shape_faults(state)
    if faults:
        raise InvalidState(f"{state_path(plan)}: {'; '.join(faults)}")
    return state


def branch_to_follow(state: dict, root: Path) -> str | None:
    branch = state["branch"]
    if branch is None:
        return None
    ref = f"refs/heads/{branch}"
    if git(root, "show-ref", "--verify", "--quiet", ref).code != 0:
        return None
    if git(root, "merge-base", "--is-ancestor", ref, "HEAD").code == 0:
        return None
    return branch


def followed(plan: str, root: Path | None = None) -> dict:
    root = root or tree_root()
    state = load(plan, root)
    branch = branch_to_follow(state, root)
    if branch is None:
        return state
    out = git(root, "show", f"refs/heads/{branch}:{state_path(plan)}")
    if out.code != 0:
        raise InvalidState(f"{state_path(plan)} on branch {branch}: {out.stderr.strip()}")
    try:
        there = json.loads(out.stdout)
    except ValueError as error:
        raise InvalidState(f"{state_path(plan)} on branch {branch}: not valid JSON: {error}") from error
    faults = shape_faults(there)
    if faults:
        raise InvalidState(f"{state_path(plan)} on branch {branch}: {'; '.join(faults)}")
    there["branch"] = branch
    return there


def computed_status(state: dict) -> str:
    if state["status"] != "planned" or not (state["phases"] or state["retired"]):
        return state["status"]
    landed = any(entry["landed"] for entry in state["retired"])
    return "implemented" if landed and not state["phases"] else "planned"


def status(plan: str, root: Path | None = None) -> str:
    """The plan's status, a parent's computed from its phases."""
    return computed_status(followed(plan, root))


def on_main(sha: str, root: Path | None = None) -> bool:
    return git(root or tree_root(), "merge-base", "--is-ancestor", sha, "main").code == 0


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
