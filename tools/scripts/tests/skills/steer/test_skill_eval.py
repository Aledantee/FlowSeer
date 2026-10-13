"""Tests for the skill-eval runner against a temporary repository and a stub bench."""

from contextlib import redirect_stderr, redirect_stdout
import io
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest
from unittest import mock

from lib import repo
from skills.steer import skill_eval

STUB_BENCH = """#!/usr/bin/env python3
import json, os, subprocess, sys
from pathlib import Path

argv = sys.argv[1:]
options = {}
for index, arg in enumerate(argv):
    if arg.startswith("--") and index + 1 < len(argv) and not argv[index + 1].startswith("--"):
        options[arg] = argv[index + 1]
lane = Path(options["--dir"])


def git(*args):
    return subprocess.run(["git", *args], cwd=lane, capture_output=True, text=True).stdout


skill = lane / ".agents/skills/demo/SKILL.md"
record = {
    "name": options["--lane"],
    "argv": argv,
    "runlog": os.environ.get("FLOWSEER_RUNLOG"),
    "runlog_parent_exists": Path(os.environ.get("FLOWSEER_RUNLOG", "/nonexistent")).parent.is_dir(),
    "cwd": os.getcwd(),
    "status": git("status", "--porcelain"),
    "parent": git("rev-parse", "HEAD^").strip(),
    "diff_head": git("diff", "--name-only", "HEAD"),
    "diff_parent": git("diff", "--name-only", "HEAD^"),
    "others": git("ls-files", "--others", "--exclude-standard"),
    "subject": git("log", "-1", "--format=%s").strip(),
    "head_files": git("show", "--name-only", "--format=", "HEAD").split(),
    "evals": [str(p) for p in lane.glob(".agents/skills/*/evals")],
    "skill_text": skill.read_text() if skill.exists() else None,
    "brief": Path(options["--brief"]).read_text(),
}
with open(os.environ["STUB_LOG"], "a") as stream:
    stream.write(json.dumps(record) + "\\n")

mode = os.environ.get("STUB_MODE")
if mode == "no-result":
    Path(options["--out"]).write_text(json.dumps({"exit": 1, "error": "no result event", "refused": False, "downgraded": None, "cost_usd_reported": None, "usage": {}}))
    sys.exit(0)
if mode == "no-listing":
    Path(options["--out"]).write_text(json.dumps({"result_text": "report about demo", "exit": 0, "refused": False, "downgraded": None, "cost_usd_reported": 0.1, "usage": {}}))
    sys.exit(0)

listed = sorted(p.name for p in lane.glob(".agents/skills/*"))
if os.environ.get("STUB_LIST_ALWAYS"):
    listed = sorted(set(listed) | {os.environ["STUB_LIST_ALWAYS"]})
Path(options["--out"]).write_text(json.dumps({
    "result_text": "report about demo",
    "skills_listed": listed,
    "skills_invoked": ["demo"] if skill.exists() else [],
    "cost_usd_reported": float(os.environ.get("STUB_COST", "0.8")),
    "wall_s": 7,
    "exit": 0,
    "refused": False,
    "downgraded": None,
    "denied_tools": [],
    "usage": {},
}))
"""

METADATA = "# /// script\n# dependencies = []\n# ///\n"

ALPHA_CHECK = METADATA + """
import os, sys
lane, text = sys.argv[1], sys.argv[2]
print(("PASS" if "demo" in text else "FAIL") + " names-demo")
present = os.path.isfile(lane + "/.agents/skills/demo/SKILL.md")
print(("PASS" if present else "FAIL") + " skill-present")
"""

BETA_CHECK = METADATA + """
import os, subprocess, sys
head = subprocess.run(["git", "-C", sys.argv[1], "rev-parse", "HEAD"], capture_output=True, text=True).stdout.strip()
print(("PASS" if head == os.environ["EVAL_BASE"] else "FAIL") + " base-is-head")
print(("PASS" if sys.argv[2] else "FAIL") + " has-report")
"""

LAZY_CHECK = METADATA + """
print("PASS always")
"""


def sh(*args, cwd, env=None):
    done = subprocess.run(list(args), cwd=cwd, capture_output=True, text=True, env=env)
    if done.returncode != 0:
        raise AssertionError(f"{args} failed: {done.stderr}")
    return done.stdout


def write(path: Path, text: str, executable=False):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)
    if executable:
        path.chmod(0o755)


def scenario(repo_dir: Path, skill: str, name: str, spec: dict, check: str, setup: str | None = None):
    directory = repo_dir / ".agents/skills" / skill / "evals" / name
    write(directory / "scenario.json", json.dumps(spec))
    write(directory / "check.py", check)
    if setup is not None:
        write(directory / "setup.py", setup)


class Fixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / "repo"
        self.lanes = self.root / "lanes"
        self.out = self.root / "rows.jsonl"
        self.log = self.root / "stub.jsonl"
        self.bench = self.root / "bench"
        write(self.bench, STUB_BENCH, executable=True)

        self.repo.mkdir()
        sh("git", "init", "-q", "-b", "main", cwd=self.repo)
        sh("git", "config", "user.email", "eval@example.com", cwd=self.repo)
        sh("git", "config", "user.name", "eval", cwd=self.repo)
        write(self.repo / ".agents/skills/demo/SKILL.md", "v1\n")
        scenario(
            self.repo, "demo", "alpha",
            {"skill": "demo", "query": "Do the thing.", "budget_usd": 2, "expected_behavior": ["x"]},
            ALPHA_CHECK,
        )
        scenario(
            self.repo, "other", "one",
            {"skill": "other", "query": "q", "budget_usd": 1, "expected_behavior": ["x"]},
            ALPHA_CHECK,
        )
        write(self.repo / ".agents/skills/other/SKILL.md", "other\n")
        sh("git", "add", "-A", cwd=self.repo)
        sh("git", "commit", "-qm", "skill v1", cwd=self.repo)
        write(self.repo / ".agents/skills/demo/SKILL.md", "v2\n")
        sh("git", "commit", "-qam", "skill v2", cwd=self.repo)
        # A scenario that is not committed yet, and uncommitted skill edits.
        scenario(
            self.repo, "demo", "beta",
            {"skill": "demo", "query": "Review the work since {EVAL_PARENT}.", "budget_usd": 5, "expected_behavior": ["x"]},
            BETA_CHECK,
            setup=METADATA + """
import pathlib, subprocess
pathlib.Path("fixture.txt").write_text("fixture\\n")
subprocess.run(["git", "add", "-A"], check=True)
subprocess.run(["git", "commit", "-q", "-m", "fixture commit"], check=True)
""",
        )
        write(self.repo / ".agents/skills/demo/SKILL.md", "v3\n")
        write(self.repo / ".agents/skills/demo/NEW.md", "new\n")

    def run_eval(self, *extra, env=None, skill="demo"):
        argv = ["--checkout", str(self.repo), "--worktree-root", str(self.lanes), "--bench", str(self.bench)]
        if skill:
            argv += ["--skill", skill]
        if "--lint" not in extra:
            argv += ["--out", str(self.out)]
        stdout, stderr = io.StringIO(), io.StringIO()
        patched = {"STUB_LOG": str(self.log), **(env or {})}
        with mock.patch.dict(os.environ, patched), redirect_stdout(stdout), redirect_stderr(stderr):
            code = skill_eval.main([*argv, *extra])
        return code, stdout.getvalue(), stderr.getvalue()

    def rows(self):
        return [json.loads(line) for line in self.out.read_text().splitlines()]

    def calls(self):
        if not self.log.exists():
            return []
        return [json.loads(line) for line in self.log.read_text().splitlines()]


class SkillEvalTest(Fixture):
    def test_one_row_per_scenario_and_variant(self):
        code, stdout, stderr = self.run_eval("--variants", "current,none")
        self.assertEqual(code, 0, stderr)
        rows = self.rows()
        self.assertEqual(
            [(r["scenario"], r["variant"], r["status"], r["pass"], r["invoked"]) for r in rows],
            [
                ("alpha", "current", "ok", "2/2", True),
                ("alpha", "none", "ok", "1/2", False),
                ("beta", "current", "ok", "2/2", True),
                ("beta", "none", "ok", "2/2", False),
            ],
        )
        self.assertEqual({r["cost_usd"] for r in rows}, {0.8})
        self.assertEqual({r["wall_s"] for r in rows}, {7})
        lines = stdout.splitlines()
        self.assertEqual(len(lines), 5, stdout)
        self.assertEqual(lines[0].split(), ["scenario", "variant", "pass", "invoked", "cost_usd", "wall_s", "status"])
        self.assertEqual(lines[1].split(), ["alpha", "current", "2/2", "True", "0.8", "7", "ok"])

    def test_none_lane_listing_the_skill_is_invalid_and_ungraded(self):
        code, stdout, stderr = self.run_eval("--scenario", "alpha", "--variants", "current,none", env={"STUB_LIST_ALWAYS": "demo"})
        self.assertEqual(code, 0, stderr)
        current, none = self.rows()
        self.assertEqual((current["status"], current["pass"]), ("ok", "2/2"))
        self.assertEqual((none["status"], none["pass"], none["checks"]), ("invalid", None, []))
        self.assertIn("invalid", stdout)

    def test_lane_is_clean_with_fixture_head_and_no_evals(self):
        code, _, stderr = self.run_eval("--variants", "current,none", "--keep")
        self.assertEqual(code, 0, stderr)
        by_lane = {call["name"]: call for call in self.calls()}
        for name, call in by_lane.items():
            self.assertEqual(call["status"], "", name)
            self.assertEqual(call["evals"], [], name)
        self.assertEqual(by_lane["eval-demo-beta-current-1"]["subject"], "fixture commit")
        self.assertEqual(by_lane["eval-demo-beta-current-1"]["head_files"], ["fixture.txt"])
        self.assertEqual(by_lane["eval-demo-alpha-current-1"]["subject"], "chore: add skill files")
        self.assertIsNone(by_lane["eval-demo-alpha-none-1"]["skill_text"])

        for name, call in by_lane.items():
            self.assertEqual(call["diff_head"], "", name)
            self.assertEqual(call["others"], "", name)
        self.assertEqual(by_lane["eval-demo-beta-current-1"]["diff_parent"], "fixture.txt\n")
        self.assertEqual(by_lane["eval-demo-alpha-none-1"]["subject"], "skill v2")

        current = self.lanes / "eval-demo-alpha-current-1"
        self.assertEqual((current / ".agents/skills/demo/NEW.md").read_text(), "new\n")
        sh("git", "add", "-A", cwd=current)
        self.assertEqual(sh("git", "diff", "--cached", "--name-only", cwd=current), "")
        none = self.lanes / "eval-demo-alpha-none-1"
        self.assertFalse((none / ".agents/skills/demo").exists())
        self.assertEqual(sh("git", "status", "--porcelain", cwd=none), "")
        self.assertEqual(list(self.lanes.glob("*/.agents/skills/*/evals")), [])
        self.assertEqual(sh("git", "config", "--list", "--local", cwd=self.repo).count("sparse"), 0)
        self.assertNotIn("worktreeconfig", sh("git", "config", "--list", "--local", cwd=self.repo))

    def test_a_bench_result_without_a_report_is_invalid_and_ungraded(self):
        code, stdout, stderr = self.run_eval("--scenario", "alpha", "--variants", "current,none", env={"STUB_MODE": "no-result"})
        self.assertEqual(code, 0, stderr)
        for row in self.rows():
            self.assertEqual((row["status"], row["pass"], row["checks"]), ("invalid", None, []), row)
            self.assertIn("exit 1", row["note"])
            self.assertIn("no result event", row["note"])
        self.assertIn("invalid", stdout)

    def test_a_none_lane_without_a_skill_listing_is_invalid(self):
        code, _, stderr = self.run_eval("--scenario", "alpha", "--variants", "current,none", env={"STUB_MODE": "no-listing"})
        self.assertEqual(code, 0, stderr)
        current, none = self.rows()
        self.assertEqual((current["status"], current["pass"]), ("ok", "2/2"))
        self.assertEqual((none["status"], none["pass"], none["checks"]), ("invalid", None, []))
        self.assertIn("skills_listed", none["note"])

    def test_parent_placeholder_and_flags_reach_bench(self):
        spec = self.repo / ".agents/skills/demo/evals/beta/scenario.json"
        spec.write_text(json.dumps({"query": "Review the work since {EVAL_PARENT}."}))
        code, _, stderr = self.run_eval("--variants", "current")
        self.assertEqual(code, 0, stderr)
        calls = {call["name"]: call for call in self.calls()}
        beta = calls["eval-demo-beta-current-1"]
        self.assertEqual(beta["brief"], f"Review the work since {beta['parent']}.")
        self.assertNotEqual(beta["parent"], sh("git", "rev-parse", "HEAD", cwd=self.repo).strip())
        self.assertEqual(calls["eval-demo-alpha-current-1"]["brief"], "Do the thing.")
        argv = calls["eval-demo-alpha-current-1"]["argv"]
        for flag, value in (("--cli", "claude"), ("--model", "claude-opus-5-5"), ("--budget", "2")):
            self.assertEqual(argv[argv.index(flag) + 1], value)
        self.assertIn("--transcript", argv)
        self.assertEqual(beta["argv"][beta["argv"].index("--budget") + 1], "3")

    def test_cost_cap_skips_the_remaining_lanes(self):
        code, stdout, stderr = self.run_eval("--variants", "current,none", "--max-total-usd", "1", "--keep")
        self.assertEqual(code, 0, stderr)
        rows = self.rows()
        self.assertEqual([r["status"] for r in rows], ["ok", "ok", "skipped", "skipped"])
        self.assertEqual([r["cost_usd"] for r in rows[2:]], [0.0, 0.0])
        self.assertEqual(len(self.calls()), 2)
        self.assertEqual(sorted(p.name for p in self.lanes.iterdir()), ["eval-demo-alpha-current-1", "eval-demo-alpha-none-1"])
        self.assertEqual(len(stdout.splitlines()), 5)

    def test_no_lane_launches_once_cost_equals_the_cap(self):
        code, _, stderr = self.run_eval("--variants", "current,none", "--max-total-usd", "1.0", env={"STUB_COST": "0.5"})
        self.assertEqual(code, 0, stderr)
        rows = self.rows()
        self.assertEqual([r["status"] for r in rows], ["ok", "ok", "skipped", "skipped"])
        self.assertEqual(len(self.calls()), 2)

    def test_ref_variant_restores_that_revisions_skill(self):
        code, _, stderr = self.run_eval("--scenario", "alpha", "--variants", "current,ref:HEAD~1,none")
        self.assertEqual(code, 0, stderr)
        texts = {call["name"]: call["skill_text"] for call in self.calls()}
        self.assertEqual(
            texts,
            {
                "eval-demo-alpha-current-1": "v3\n",
                "eval-demo-alpha-ref-HEAD-1-1": "v1\n",
                "eval-demo-alpha-none-1": None,
            },
        )
        self.assertEqual([r["status"] for r in self.rows()], ["ok", "ok", "ok"])

    def test_bench_sees_a_lane_local_runlog(self):
        code, _, stderr = self.run_eval("--variants", "current,none")
        self.assertEqual(code, 0, stderr)
        paths = [call["runlog"] for call in self.calls()]
        self.assertEqual(len(set(paths)), 4)
        for call in self.calls():
            self.assertIn(call["name"], call["runlog"])
            self.assertTrue(call["runlog_parent_exists"])
            self.assertNotEqual(call["runlog"], str(Path.home() / ".claude/models/runs.jsonl"))

    def test_failing_setup_is_stale_and_skips_bench(self):
        scenario(
            self.repo, "demo", "broken",
            {"skill": "demo", "query": "q", "expected_behavior": ["x"]},
            ALPHA_CHECK,
            setup=METADATA + 'import sys\nsys.exit("no")\n',
        )
        code, _, stderr = self.run_eval("--scenario", "broken", "--variants", "current")
        self.assertEqual(code, 0, stderr)
        (row,) = self.rows()
        self.assertEqual((row["status"], row["pass"]), ("stale", None))
        self.assertIn("no", row["note"])
        self.assertEqual(self.calls(), [])

    def test_lanes_and_branches_are_removed_unless_kept(self):
        code, _, stderr = self.run_eval("--variants", "current,none")
        self.assertEqual(code, 0, stderr)
        self.assertEqual(sh("git", "branch", "--list", "skill-eval/*", cwd=self.repo), "")
        self.assertEqual(len(sh("git", "worktree", "list", cwd=self.repo).splitlines()), 1)
        self.assertEqual(list(self.lanes.iterdir()), [])

    def test_unknown_variant_and_missing_scenario_fail(self):
        with self.assertRaises(SystemExit):
            with redirect_stderr(io.StringIO()):
                self.run_eval("--variants", "bogus")
        code, _, stderr = self.run_eval("--scenario", "nope")
        self.assertEqual(code, 1)
        self.assertIn("no scenario for skill demo named nope", stderr)


class LintTest(Fixture):
    def test_lint_counts_every_scenario(self):
        code, stdout, stderr = self.run_eval("--lint", skill=None)
        self.assertEqual((code, stdout.strip(), stderr), (0, "checked 3 scenarios", ""))
        code, stdout, _ = self.run_eval("--lint", skill="demo")
        self.assertEqual((code, stdout.strip()), (0, "checked 2 scenarios"))

    def test_lint_flags_a_check_that_passes_an_untouched_lane(self):
        scenario(
            self.repo, "demo", "lazy",
            {"skill": "demo", "query": "q", "budget_usd": 1, "expected_behavior": ["x"]},
            LAZY_CHECK,
        )
        code, stdout, stderr = self.run_eval("--lint", skill="demo")
        self.assertEqual(code, 1)
        self.assertEqual(stderr.strip(), "demo/lazy: check.py prints no FAIL on an untouched lane")
        self.assertEqual(stdout.strip(), "checked 3 scenarios")

    def test_lint_flags_missing_fields_and_a_check_that_does_not_parse(self):
        scenario(self.repo, "demo", "thin", {"skill": "demo", "query": "q"}, METADATA + "print(\n")
        code, _, stderr = self.run_eval("--lint", skill="demo")
        self.assertEqual(code, 1)
        self.assertIn("demo/thin: scenario.json lacks budget_usd, expected_behavior", stderr)
        self.assertIn("demo/thin: check.py does not parse", stderr)


class RealTreeTest(unittest.TestCase):
    def test_lint_counts_the_scenarios_git_lists(self):
        root = repo.root(Path(__file__).parent)
        listed = sh("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", cwd=root)
        pattern = re.compile(r"^\.agents/skills/[^/]+/evals/[^/]+/scenario\.json$")
        expected = len({p for p in listed.split("\0") if pattern.match(p)})
        with tempfile.TemporaryDirectory() as lanes:
            stdout = io.StringIO()
            with redirect_stdout(stdout), redirect_stderr(io.StringIO()):
                skill_eval.main(["--lint", "--worktree-root", lanes])
        self.assertEqual(stdout.getvalue().strip().splitlines()[-1], f"checked {expected} scenarios")


if __name__ == "__main__":
    unittest.main()
