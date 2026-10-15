"""Own the state file beside each plan under docs/plans/.

A plan's prose is Markdown. Its machine state lives in a JSON file beside
it, `<name>-plan.state.json` for `<name>-plan.md`, and this command is that
file's only writer. Each transition is one subcommand, and a subcommand
refuses to leave a file `verify check-plan-state` would reject. Readers
import `lib.plans`, so `next`, `drive`, and the verifier share one test for
a finished phase and one for a plan sent back to `plan`.

    run.py plan record init <plan> [--needs-decisions] [--parent <p> [--after <q>...]]
    run.py plan record ready <plan>
    run.py plan record check <plan>
    run.py plan record after <plan> [<path>...]
    run.py plan record implemented <plan> --units <n> --from <t> --to <t> [--landed <first>..<last>]
    run.py plan record partial <plan> --units <n> --from <t> --to <t> --note <units left and why>
    run.py plan record review <plan> <verdict> [--rounds <n>]
    run.py plan record compound <plan> <outcome>
    run.py plan record replan <plan> [--needs-decisions]
    run.py plan record supersede <plan> --by <path>
    run.py plan record abandon <plan>
    run.py plan record branch <plan> <name> | --clear
    run.py plan record retire <plan>
    run.py plan record show <plan> [--json]
    run.py plan record is <plan> <field>=<value> | <field>!=<value>

A phase names its parent, the phases or plans it runs after (a phase of any
parent, or a plan with none, never a parent), and the commit range it
landed as. A parent lists its phases on disk under `phases` and the ones
`retire` deleted under `retired`, and stores nothing else about them. A
parent's status is computed: `implemented` once no phase is left on disk
and a retired one carries a range, `planned` until then. A stored
`superseded` or `abandoned` stands. Retiring a plan with no parent removes
it, and each retired phase it holds that landed, from every `after` list
naming them, since the entries that record them as finished go with it. A
plan or entry not finished refuses to retire while an `after` names it.

A unit that changes an output shape quotes the old output's distinctive
token on a `Replaces:` line. `check`, `ready`, and `replan` without
`--needs-decisions` refuse while a test file (Go, TypeScript, or Python)
or a file under `testdata/` holds that token and neither the unit's
`Files:` nor those of a unit on its `After:` line name it. That file would
keep the unit's verifier red until some other unit lands. A `Replaces:`
line with no backtick-quoted token refuses too, since it would turn the
check off without a word.

`is` exits 0 when the test holds, 1 when it does not, and 2 when it cannot
be evaluated. `null` stands for an unset field, and `status` is the
computed one.
"""

from __future__ import annotations

import argparse
import copy
import json
import os
import re
import sys
import tempfile
from pathlib import Path

from lib.plans import (
    BLANK,
    BRANCH,
    FINAL,
    LEDGER_NAME,
    PLANS,
    SHA,
    UNIT,
    VERDICTS,
    StateError,
    branch_to_follow,
    computed_status,
    followed,
    git,
    is_time,
    load,
    plan_of,
    problems,
    read_tree,
    shape_faults,
    state_path,
    tree_root,
)
from skills.implement import plan_deviations

UNIT_ID = re.compile(r"\bU\d+[a-z]?\b")
TESTS = (":(glob)**/*_test.go", ":(glob)**/*.test.ts", ":(glob)**/*.spec.ts", ":(glob)**/test_*.py", ":(glob)**/testdata/**")


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
    before = set(problems(plans, raw, root))
    raw.update(changes)
    if retire:
        plans.pop(retire, None)
        raw.pop(retire, None)
    written = {state_path(plan) for plan in changes} | set(changes)
    faults = [fault for fault in problems(plans, raw, root) if fault not in before or fault[0] in written]
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
            if removed.code != 0:
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
        if staged.code != 0:
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


def unit_fields(text: str) -> dict[str, dict[str, list[str]]]:
    """Each unit's `After:` unit ids and `Replaces:` tokens."""
    result: dict[str, dict[str, list[str]]] = {}
    current = None
    for line in text.splitlines():
        heading = UNIT.match(line)
        if heading:
            current = heading.group(1)
            result[current] = {"After": [], "Replaces": []}
            continue
        field = plan_deviations.FIELD.match(line.strip())
        if not (current and field) or line.startswith((" ", "\t")):
            continue
        if field.group(1) == "After":
            result[current]["After"] += UNIT_ID.findall(field.group(2))
        elif field.group(1) == "Replaces":
            tokens = plan_deviations.QUOTED.findall(field.group(2))
            if not tokens and field.group(2).strip().lower() != "none":
                tokens = [""]
            result[current]["Replaces"] += tokens
    return result


def unlisted_holders(root: Path, plan: str) -> list[str]:
    """The test and testdata files holding a token a unit replaces that
    neither that unit nor a unit it runs after lists in `Files:`."""
    path = root / plan
    if not path.is_file():
        fail(f"{plan} is not a file", 2)
    fields = unit_fields(path.read_text(encoding="utf-8"))
    files = plan_deviations.units(path, root)
    faults = []
    for unit, held in fields.items():
        named = [entry for owner in [unit, *held["After"]] for entry in files.get(owner, [])]
        for token in held["Replaces"]:
            if not token:
                faults.append(f"{unit}'s Replaces: line quotes no token; write it as Replaces: `<token>` on the same line")
                continue
            found = git(root, "grep", "-l", "-F", "--untracked", "-e", token, "--", *TESTS)
            if found.code not in (0, 1):
                fail(f"git grep for `{token}` failed: {found.stderr.strip()}", 2)
            for hit in found.stdout.splitlines():
                hit = plan_deviations.canonical(hit)
                if not any(plan_deviations.covers(entry, hit) for entry in named):
                    faults.append(f"{unit} replaces `{token}`, which {hit} holds; list it in {unit}'s Files: or in a unit on its After: line")
    return faults


def check_ready(root: Path, plan: str) -> None:
    faults = unlisted_holders(root, plan)
    if faults:
        fail("refused, a unit leaves a file holding the output it replaces unlisted:\n" + "\n".join(f"  {fault}" for fault in faults))


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
    check_ready(root, plan)
    state["readiness"] = "implementation-ready"
    transition(root, {plan: state})
    print(f"plan state: {plan} implementation-ready")


def cmd_check(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    check_ready(root, plan)
    print(f"plan state: {plan} lists every file holding a token it replaces")


def cmd_after(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    state["after"] = [plan_arg(root, path) for path in args.paths]
    transition(root, {plan: state})
    print(f"plan state: {plan} runs after {', '.join(state['after']) or 'nothing'}")


def cmd_branch(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    state = stored(root, plan)
    if args.name and (not BRANCH.fullmatch(args.name) or git(root, "check-ref-format", "--branch", args.name).code):
        fail(f"{args.name!r} is not a branch-name-safe string")
    state["branch"] = None if args.clear else args.name
    transition(root, {plan: state})
    print(f"plan state: {plan} branch: {state['branch'] or 'null'}")


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
        if git(root, "merge-base", "--is-ancestor", last, "HEAD").code != 0:
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
    if not args.needs_decisions:
        check_ready(root, plan)
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
    else:
        # No parent keeps a retired entry for this plan, and the entries of
        # the phases it retired go with it, so a phase of another parent whose
        # `after` names any of them would wait on a path nothing records as
        # finished. The finished ones leave those lists. An unfinished one
        # refuses, since the dependent needs re-planning first.
        _, raw = read_tree(root)
        for other, held in sorted(raw.items()):
            if not isinstance(held, dict) or shape_faults(held):
                fail(f"{state_path(other)} is not a legal state file; repair it before retiring {plan}")
        named = {plan} | {entry["plan"] for entry in state["retired"]}
        finished = {entry["plan"] for entry in state["retired"] if entry["landed"]}
        if final == "implemented":
            finished.add(plan)
        for other, held in sorted(raw.items()):
            if other == plan or not any(path in named for path in held["after"]):
                continue
            unfinished = [path for path in held["after"] if path in named - finished]
            if unfinished:
                fail(f"{other} runs after {', '.join(unfinished)}, which retiring {plan} leaves unfinished; run `plan record after` on it first")
            held = stored(root, other)
            held["after"] = [path for path in held["after"] if path not in named]
            changes[other] = held
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
    try:
        checkout = load(plan, root)
        state = followed(plan, root)
    except StateError as error:
        fail(str(error), 2)
    state["status"] = computed_status(state)
    if args.json:
        print(json.dumps(state, indent=2))
        return
    print(plan)
    for key in BLANK:
        value = state[key]
        if key == "branch" and value and git(root, "show-ref", "--verify", "--quiet", f"refs/heads/{value}").code:
            print(f"  branch: {value} (missing, read from this checkout)")
        else:
            print(f"  {key}: {value if isinstance(value, (str, int)) else json.dumps(value)}")
    if branch_to_follow(checkout, root):
        print(f"  read from: {checkout['branch']}")


def cmd_is(root: Path, args: argparse.Namespace) -> None:
    plan = plan_arg(root, args.plan)
    field, sep, wanted = args.test.partition("=")
    negated = field.endswith("!")
    field = field.removesuffix("!")
    if not sep or field not in BLANK:
        fail(f"{args.test!r} is not <field>=<value> over {', '.join(BLANK)}", 2)
    try:
        state = followed(plan, root)
    except StateError as error:
        fail(str(error), 2)
    value = computed_status(state) if field == "status" else state[field]
    text = value if isinstance(value, str) else json.dumps(value)
    sys.exit(0 if (text == wanted) != negated else 1)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="run.py plan record", description=__doc__.splitlines()[0])
    commands = parser.add_subparsers(dest="command", required=True)

    def command(name: str, run, summary: str) -> argparse.ArgumentParser:
        sub = commands.add_parser(name, help=summary)
        sub.set_defaults(run=run)
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
    command("check", cmd_check, "refuse a unit that leaves a file holding its replaced token unlisted")
    after = command("after", cmd_after, "replace a phase's prerequisites")
    after.add_argument("paths", nargs="*", metavar="PLAN")
    branch = command("branch", cmd_branch, "record or clear the plan's work branch")
    branch.add_argument("name", nargs="?", help="local branch name")
    branch.add_argument("--clear", action="store_true")
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

    args = parser.parse_args(argv)
    if args.command == "branch" and (bool(args.name) == args.clear):
        parser.error("branch takes a name or --clear")
    try:
        root = tree_root()
    except StateError as error:
        fail(str(error), 2)
    args.run(root, args)
    return 0
