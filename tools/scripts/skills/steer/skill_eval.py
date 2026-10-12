"""Run a skill's evaluation scenarios in throwaway worktrees and grade them mechanically.

A lane is a linked worktree of the runner checkout's HEAD. The variant (the
skill as edited, at a ref, or absent) is applied on disk and then hidden from
git with skip-worktree entries, so a lane that commits with `git add -A` never
stages it. The hiding writes only the lane's own index, never a shared git
config or exclude file.
"""

import argparse
from collections.abc import Iterator
import contextlib
from dataclasses import dataclass
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

from lib import proc, repo

SKILLS = ".agents/skills"
SCENARIO_PATH = re.compile(rf"^{re.escape(SKILLS)}/([^/]+)/evals/([^/]+)/scenario\.json$")
CHECK_LINE = re.compile(r"^(PASS|FAIL) (\S.*)$")
REQUIRED_FIELDS = ("skill", "query", "budget_usd", "expected_behavior")
DEFAULT_BUDGET_USD = 3
COLUMNS = ("scenario", "variant", "run", "pass", "invoked", "cost_usd", "wall_s", "status")


class RunnerError(Exception):
    """A failure that stops the run and is reported on standard error."""


@dataclass(frozen=True)
class Scenario:
    skill: str
    name: str
    directory: Path


@dataclass(frozen=True)
class Config:
    checkout: Path
    worktree_root: Path
    bench: Path
    model: str
    keep: bool


def git(args: list[str], cwd: Path, stdin: str | None = None) -> str:
    result = proc.run(["git", *args], cwd=cwd, stdin=stdin)
    if result.code != 0:
        raise RunnerError(f"git {' '.join(args[:2])} failed in {cwd}: {result.stderr.strip()}")
    return result.stdout


def git_paths(args: list[str], cwd: Path) -> list[str]:
    return [name for name in git(["ls-files", "-z", *args], cwd).split("\0") if name]


def discover(checkout: Path) -> list[Scenario]:
    """List scenarios git knows or could add, so one not yet committed still runs."""
    found = set()
    for path in git_paths(["--cached", "--others", "--exclude-standard", "--", SKILLS], checkout):
        match = SCENARIO_PATH.match(path)
        if match:
            found.add((match[1], match[2]))
    return [
        Scenario(skill, name, checkout / SKILLS / skill / "evals" / name)
        for skill, name in sorted(found)
    ]


def load_scenario(scenario: Scenario) -> dict:
    path = scenario.directory / "scenario.json"
    try:
        data = json.loads(path.read_text())
    except (OSError, ValueError) as error:
        raise RunnerError(f"{path}: {error}") from error
    if not isinstance(data, dict):
        raise RunnerError(f"{path}: not a JSON object")
    return data


def conceal_variant(lane: Path) -> None:
    """Hide every on-disk difference under the skills tree from git status and `git add -A`."""
    for evals in (lane / SKILLS).glob("*/evals"):
        shutil.rmtree(evals)
    changed = set(git_paths(["--modified", "--deleted", "--", SKILLS], lane))
    fresh = git_paths(["--others", "--exclude-standard", "--", SKILLS], lane)
    if fresh:
        # Skip-worktree needs an index entry, and an intent-to-add entry gives an
        # untracked file one without staging its content.
        git(
            ["add", "--intent-to-add", "--pathspec-from-file=-", "--pathspec-file-nul"],
            lane,
            stdin="".join(f"{path}\0" for path in fresh),
        )
    hidden = sorted(changed | set(fresh))
    if hidden:
        git(
            ["update-index", "--skip-worktree", "-z", "--stdin"],
            lane,
            stdin="".join(f"{path}\0" for path in hidden),
        )
    dirty = git(["status", "--porcelain"], lane).strip()
    if dirty:
        raise RunnerError(f"lane {lane} is not clean after applying the variant:\n{dirty}")


def apply_variant(checkout: Path, lane: Path, skill: str, variant: str) -> None:
    target = lane / SKILLS / skill
    if target.exists():
        shutil.rmtree(target)
    if variant == "current":
        source = checkout / SKILLS / skill
        if not source.is_dir():
            raise RunnerError(f"{source} is not a skill directory")
        shutil.copytree(source, target, symlinks=True, ignore=shutil.ignore_patterns("evals"))
    elif variant.startswith("ref:"):
        archive = subprocess.run(
            ["git", "archive", "--format=tar", variant[4:], f"{SKILLS}/{skill}"],
            cwd=checkout,
            capture_output=True,
            check=False,
        )
        if archive.returncode != 0:
            message = archive.stderr.decode(errors="replace").strip()
            raise RunnerError(f"git archive {variant[4:]} {SKILLS}/{skill} failed: {message}")
        with tarfile.open(fileobj=io.BytesIO(archive.stdout)) as tar:
            tar.extractall(lane, filter="data")
    conceal_variant(lane)


def lane_name(skill: str, scenario: str, variant: str, run: int) -> str:
    safe = re.sub(r"\.{2,}", ".", re.sub(r"[^A-Za-z0-9._-]+", "-", variant))
    return f"eval-{skill}-{scenario}-{safe}-{run}"


@contextlib.contextmanager
def lane(cfg: Config, name: str, skill: str, variant: str) -> Iterator[Path]:
    """Add a lane worktree on branch skill-eval/<name> with the variant applied, then remove it."""
    path = cfg.worktree_root / name
    branch = f"skill-eval/{name}"
    git(["worktree", "add", "-b", branch, str(path), "HEAD"], cfg.checkout)
    try:
        apply_variant(cfg.checkout, path, skill, variant)
        yield path
    finally:
        if not cfg.keep:
            proc.run(["git", "worktree", "remove", "--force", str(path)], cwd=cfg.checkout)
            proc.run(["git", "branch", "-D", branch], cwd=cfg.checkout)


def run_setup(scenario: Scenario, path: Path) -> str | None:
    """Run the scenario's setup.sh in the lane, returning why it failed or None."""
    setup = scenario.directory / "setup.sh"
    if not setup.is_file():
        return None
    result = proc.run(["bash", str(setup)], cwd=path)
    if result.code != 0:
        return f"setup.sh exited {result.code}: {result.stderr.strip()[-300:]}"
    dirty = git(["status", "--porcelain"], path).strip()
    if dirty:
        return f"setup.sh left the lane dirty:\n{dirty}"
    return None


def run_check(scenario: Scenario, path: Path, base: str, text: str) -> list[dict]:
    done = subprocess.run(
        [str(scenario.directory / "check.sh"), str(path), text],
        cwd=path,
        env={**os.environ, "EVAL_BASE": base},
        capture_output=True,
        text=True,
        check=False,
    )
    checks = []
    for line in done.stdout.splitlines():
        match = CHECK_LINE.match(line)
        if match:
            checks.append({"name": match[2], "pass": match[1] == "PASS"})
    return checks


def run_bench(cfg: Config, name: str, path: Path, query: str, budget, workdir: Path) -> dict:
    brief = workdir / "brief.md"
    out = workdir / "result.json"
    brief.write_text(query)
    env = os.environ.copy()
    env["FLOWSEER_RUNLOG"] = str(Path(git(["rev-parse", "--absolute-git-dir"], path).strip()) / "runs.jsonl")
    done = subprocess.run(
        [
            str(cfg.bench),
            "--lane", name,
            "--cli", "claude",
            "--model", cfg.model,
            "--transcript",
            "--budget", str(budget),
            "--brief", str(brief),
            "--dir", str(path),
            "--out", str(out),
        ],
        cwd=path,
        env=env,
        capture_output=True,
        text=True,
        check=False,
    )
    try:
        return json.loads(out.read_text())
    except (OSError, ValueError) as error:
        tail = done.stderr.strip()[-300:]
        raise RunnerError(f"bench wrote no result for {name} (exit {done.returncode}): {error}; {tail}") from error


def lane_status(data: dict, skill: str, variant: str, path: Path) -> str:
    listed = data.get("skills_listed") or []
    if variant == "none" and (skill in listed or (path / SKILLS / skill).exists()):
        return "invalid"
    if data.get("refused"):
        return "refused"
    if data.get("downgraded"):
        return "downgraded"
    return "ok"


def blank_row(scenario: Scenario, variant: str, run: int, status: str) -> dict:
    return {
        "skill": scenario.skill,
        "scenario": scenario.name,
        "variant": variant,
        "run": run,
        "status": status,
        "pass": None,
        "invoked": None,
        "cost_usd": 0.0,
        "wall_s": None,
        "checks": [],
    }


def evaluate_lane(cfg: Config, scenario: Scenario, spec: dict, variant: str, run: int) -> dict:
    row = blank_row(scenario, variant, run, "stale")
    name = lane_name(scenario.skill, scenario.name, variant, run)
    with lane(cfg, name, scenario.skill, variant) as path, tempfile.TemporaryDirectory() as workdir:
        row["lane"] = name
        failure = run_setup(scenario, path)
        if failure:
            row["note"] = failure
            return row
        base = git(["rev-parse", "HEAD"], path).strip()
        parent = proc.run(["git", "rev-parse", "HEAD^"], cwd=path).stdout.strip()
        query = spec["query"].replace("{EVAL_PARENT}", parent)
        budget = spec.get("budget_usd", DEFAULT_BUDGET_USD)
        data = run_bench(cfg, name, path, query, budget, Path(workdir))
        row["status"] = lane_status(data, scenario.skill, variant, path)
        invoked = data.get("skills_invoked")
        row.update(
            invoked=None if invoked is None else scenario.skill in invoked,
            cost_usd=data.get("cost_usd_reported"),
            wall_s=data.get("wall_s"),
            model=cfg.model,
            exit=data.get("exit"),
            refused=data.get("refused"),
            downgraded=data.get("downgraded"),
            denied_tools=data.get("denied_tools"),
            skills_listed=data.get("skills_listed"),
            skills_invoked=invoked,
            usage=data.get("usage"),
        )
        if row["status"] != "invalid":
            row["checks"] = run_check(scenario, path, base, data.get("result_text") or "")
            row["pass"] = f"{sum(c['pass'] for c in row['checks'])}/{len(row['checks'])}"
    return row


def format_table(rows: list[dict], with_run: bool) -> str:
    columns = [c for c in COLUMNS if with_run or c != "run"]
    cells = [[str("-" if row.get(c) is None else row[c]) for c in columns] for row in rows]
    widths = [max(len(c), *(len(line[i]) for line in cells)) for i, c in enumerate(columns)]
    lines = [columns, *cells]
    return "\n".join("  ".join(v.ljust(w) for v, w in zip(line, widths)).rstrip() for line in lines)


def evaluate(cfg: Config, scenarios: list[Scenario], args: argparse.Namespace) -> int:
    out = Path(args.out)
    out.parent.mkdir(parents=True, exist_ok=True)
    rows = []
    spent = 0.0
    for scenario in scenarios:
        spec = load_scenario(scenario)
        if "query" not in spec:
            raise RunnerError(f"{scenario.directory / 'scenario.json'}: no query")
        for variant in args.variants:
            for run in range(1, args.runs + 1):
                if spent >= args.max_total_usd:
                    row = blank_row(scenario, variant, run, "skipped")
                else:
                    row = evaluate_lane(cfg, scenario, spec, variant, run)
                spent += row["cost_usd"] or 0.0
                rows.append(row)
                with out.open("a") as stream:
                    stream.write(json.dumps(row) + "\n")
    print(format_table(rows, args.runs > 1))
    return 0


def lint_scenario(cfg: Config, scenario: Scenario) -> list[str]:
    problems = []
    try:
        spec = load_scenario(scenario)
    except RunnerError as error:
        return [str(error)]
    missing = [field for field in REQUIRED_FIELDS if field not in spec]
    if missing:
        problems.append(f"scenario.json lacks {', '.join(missing)}")
    check = scenario.directory / "check.sh"
    if not (check.is_file() and os.access(check, os.X_OK)):
        return [*problems, "check.sh is missing or not executable"]
    try:
        shellcheck = proc.run(["shellcheck", str(check)])
    except FileNotFoundError:
        return [*problems, "shellcheck is not installed"]
    if shellcheck.code != 0:
        problems.append(f"check.sh fails shellcheck:\n{shellcheck.stdout.strip()}")
    name = lane_name(scenario.skill, scenario.name, "lint", 1)
    with lane(cfg, name, scenario.skill, "current") as path:
        failure = run_setup(scenario, path)
        if failure:
            return [*problems, failure]
        base = git(["rev-parse", "HEAD"], path).strip()
        if not any(not c["pass"] for c in run_check(scenario, path, base, "")):
            problems.append("check.sh prints no FAIL on an untouched lane")
    return problems


def lint(cfg: Config, scenarios: list[Scenario]) -> int:
    failed = False
    for scenario in scenarios:
        for problem in lint_scenario(cfg, scenario):
            print(f"{scenario.skill}/{scenario.name}: {problem}", file=sys.stderr)
            failed = True
    print(f"checked {len(scenarios)} scenarios")
    return 1 if failed else 0


def variant_list(value: str) -> list[str]:
    variants = value.split(",")
    for variant in variants:
        valid = variant in ("current", "none") or (variant.startswith("ref:") and len(variant) > 4 and variant[4] != "-")
        if not valid:
            raise argparse.ArgumentTypeError(f"unknown variant {variant!r}: use current, ref:<rev>, or none")
    return variants


def parse(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        prog="run.py steer skill-eval",
        description="Run a skill's scenarios headless in throwaway worktrees and grade them.",
    )
    parser.add_argument("--skill", help="skill whose scenarios run (all skills with --lint)")
    parser.add_argument("--scenario", help="run only this scenario")
    parser.add_argument("--variants", type=variant_list, default=["current", "none"], help="current, ref:<rev>, none (comma separated)")
    parser.add_argument("--runs", type=int, default=1, help="runs per scenario and variant")
    parser.add_argument("--model", default="claude-opus-5-5")
    parser.add_argument("--max-total-usd", type=float, default=30.0, help="launch no lane once reported cost reaches this")
    parser.add_argument("--worktree-root", type=Path, default=Path.home() / "Projects" / "worktrees" / "FlowSeer")
    parser.add_argument("--bench", type=Path, help="bench.sh to call (default: the checkout's tune/scripts/bench.sh)")
    parser.add_argument("--keep", action="store_true", help="keep lane worktrees and branches")
    parser.add_argument("--out", type=Path, help="JSONL file the rows are appended to")
    parser.add_argument("--lint", action="store_true", help="check every scenario's files instead of running lanes")
    parser.add_argument("--checkout", type=Path, help=argparse.SUPPRESS)
    args = parser.parse_args(argv)
    if not args.lint and not (args.skill and args.out):
        parser.error("--skill and --out are required unless --lint is given")
    if args.runs < 1:
        parser.error("--runs must be at least 1")
    return args


def main(argv: list[str]) -> int:
    args = parse(argv)
    try:
        checkout = args.checkout or repo.root(Path(__file__).parent)
        cfg = Config(
            checkout=checkout,
            worktree_root=args.worktree_root,
            bench=args.bench or checkout / ".agents/skills/tune/scripts/bench.sh",
            model=args.model,
            keep=args.keep,
        )
        scenarios = [
            s
            for s in discover(checkout)
            if (args.skill is None or s.skill == args.skill) and args.scenario in (None, s.name)
        ]
        if not args.lint and not scenarios:
            raise RunnerError(f"no scenario for skill {args.skill}" + (f" named {args.scenario}" if args.scenario else ""))
        return lint(cfg, scenarios) if args.lint else evaluate(cfg, scenarios, args)
    except RunnerError as error:
        print(f"run.py steer skill-eval: {error}", file=sys.stderr)
        return 1
