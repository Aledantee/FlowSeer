import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from lib import plans

RUN_PY = Path(__file__).resolve().parents[2] / "run.py"
PARENT = "docs/plans/2026-01-01-parent-plan.md"
FIRST = "docs/plans/2026-01-02-first-plan.md"
PHASE = "docs/plans/2026-01-03-phase-plan.md"
PLAIN = "docs/plans/2026-01-04-plain-plan.md"
OUTCOME = {"units": 3, "from": "2026-01-05T10:00Z", "to": "2026-01-05T11:30:00Z", "note": ""}


class CheckPlanStateTest(unittest.TestCase):
    """Runs `verify check-plan-state` in a scratch repository whose state
    files `plan record` writes or the test writes by hand."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)
        self.env = {
            **os.environ,
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_AUTHOR_NAME": "test",
            "GIT_AUTHOR_EMAIL": "test@example.com",
            "GIT_COMMITTER_NAME": "test",
            "GIT_COMMITTER_EMAIL": "test@example.com",
        }
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=self.root, env=self.env, check=True)
        (self.root / "docs/plans").mkdir(parents=True)
        for plan in (PARENT, FIRST, PHASE, PLAIN):
            (self.root / plan).write_text("# Plan\n", encoding="utf-8")

    def run_command(self, group, command, *args, code=0):
        out = subprocess.run(
            [sys.executable, str(RUN_PY), group, command, *args],
            cwd=self.root,
            env=self.env,
            capture_output=True,
            text=True,
        )
        self.assertEqual(out.returncode, code, out.stdout + out.stderr)
        return out

    def record(self, *args, code=0):
        return self.run_command("plan", "record", *args, code=code)

    def check(self, *args, code=0):
        return self.run_command("verify", "check-plan-state", *args, code=code)

    def state(self, plan):
        return json.loads((self.root / plans.state_path(plan)).read_text(encoding="utf-8"))

    def put(self, plan, **fields):
        """Writes a state file by hand, the way a command never would."""
        path = self.root / plans.state_path(plan)
        path.write_text(json.dumps({**plans.BLANK, **fields}), encoding="utf-8")

    def test_outside_a_repository_exits_2(self):
        with tempfile.TemporaryDirectory() as bare:
            out = subprocess.run(
                [sys.executable, str(RUN_PY), "verify", "check-plan-state"],
                cwd=bare,
                env={**self.env, "GIT_CEILING_DIRECTORIES": str(Path(bare).parent)},
                capture_output=True,
                text=True,
            )
        self.assertEqual(out.returncode, 2, out.stdout + out.stderr)
        self.assertIn("not inside a git work tree", out.stderr)

    def test_a_tree_of_legal_files_prints_the_verdict(self):
        for plan in (PARENT, FIRST, PHASE, PLAIN):
            self.record("init", plan)
        self.assertEqual(self.check().stdout, "plan state: every state file checked is legal.\n")

    def test_outcome_times_are_real_utc_times(self):
        for plan in (PARENT, FIRST, PHASE):
            self.put(plan)
        self.put(PLAIN, status="implemented", outcome={**OUTCOME, "to": "2026-00-00T99:99:99Z"})
        self.assertIn("outcome must be null or", self.check(code=1).stderr)

    def test_check_requires_the_branch_key(self):
        for plan in (PARENT, FIRST, PHASE, PLAIN):
            self.record("init", plan)
        path = self.root / plans.state_path(PLAIN)
        state = self.state(PLAIN)
        del state["branch"]
        path.write_text(json.dumps(state), encoding="utf-8")
        self.assertIn("missing key 'branch'", self.check(code=1).stderr)

    def test_check_rejects_a_branch_that_is_not_a_branch_name(self):
        for plan in (PARENT, FIRST, PHASE, PLAIN):
            self.record("init", plan)
        self.put(PLAIN, branch="a b")
        self.assertIn("branch must be null or a branch-name-safe string", self.check(code=1).stderr)

    def test_check_pairs_each_plan_with_a_state_file(self):
        for plan in (PARENT, FIRST, PHASE):
            self.record("init", plan)
        missing = self.check(code=1)
        self.assertIn(f"{PLAIN}: has no state file", missing.stderr)
        self.record("init", PLAIN)
        self.check()
        (self.root / PLAIN).unlink()
        orphan = self.check(code=1)
        self.assertIn(f"{plans.state_path(PLAIN)}: has no plan beside it", orphan.stderr)
        self.check(plans.state_path(FIRST))
        self.check(plans.state_path(PLAIN), code=1)
        self.check("docs/plans", code=1)

    def test_check_rejects_a_frontmatter_that_still_carries_state(self):
        for plan in (PARENT, FIRST, PHASE, PLAIN):
            self.record("init", plan)
        document = "---\ntitle: Plain - Plan\ntype: fix\nartifact_contract: flowseer-plan/v2\n---\n\n# Plain\n"
        (self.root / PLAIN).write_text(document, encoding="utf-8")
        self.check()
        carried = document.replace("type: fix\n", "type: fix\nstatus: planned\nreview: accept\n")
        (self.root / PLAIN).write_text(carried, encoding="utf-8")
        refused = self.check(code=1)
        self.assertIn(f"{PLAIN}: frontmatter carries status, review, which", refused.stderr)
        self.record("ready", PLAIN, code=1)
        for spelling in ("status : planned", '"status": planned', "parent: docs/plans/x-plan.md"):
            with self.subTest(spelling=spelling):
                spelled = document.replace("type: fix\n", f"type: fix\n{spelling}\n")
                (self.root / PLAIN).write_text(spelled, encoding="utf-8")
                self.check(code=1)
        (self.root / PLAIN).write_text(document + "\nstatus: a word in the body\n", encoding="utf-8")
        self.check()

    def test_check_names_a_key_outside_the_contract(self):
        for plan in (PARENT, FIRST, PHASE):
            self.record("init", plan)
        self.put(PLAIN, Landed="abc1234..def5678")
        self.assertIn("unknown key 'Landed'", self.check(code=1).stderr)
        self.put(PLAIN, review="approved")
        self.assertIn("review must be null or one of", self.check(code=1).stderr)
        (self.root / plans.state_path(PLAIN)).write_text("{", encoding="utf-8")
        self.assertIn("not valid JSON", self.check(code=1).stderr)

    def test_check_rejects_each_illegal_combination(self):
        landed = {"first": "abc1234", "last": "def5678"}
        retired = [{"plan": "docs/plans/2026-01-09-gone-plan.md", "status": "implemented", "landed": landed}]
        phase = {"parent": PARENT}
        cases = {
            "landed is set while status is 'planned'": (FIRST, {**phase, "landed": landed}),
            "landed is set with parent null": (PLAIN, {"status": "implemented", "landed": landed}),
            "status is implemented with parent set and landed null": (FIRST, {**phase, "status": "implemented"}),
            "phases or retired is non-empty with parent set": (FIRST, {**phase, "retired": retired}),
            f"after names {PLAIN}, which is neither": (PHASE, {**phase, "after": [PLAIN]}),
            f"after names {PHASE}, which is neither": (PHASE, {**phase, "after": [PHASE]}),
            "the stored status must not be 'implemented'": (PARENT, {"status": "implemented", "phases": [FIRST, PHASE]}),
            "the stored status must not be 'partially-implemented'": (
                PARENT,
                {"status": "partially-implemented", "retired": retired},
            ),
            "superseded_by is set exactly when status is superseded": (PLAIN, {"status": "superseded"}),
            "after is set with parent null": (PLAIN, {"after": [FIRST]}),
            f"parent {PLAIN} does not list this plan under phases": (FIRST, {"parent": PLAIN}),
            f"phases names {PLAIN}, whose state does not name this plan": (PARENT, {"phases": [FIRST, PHASE, PLAIN]}),
            f"retired names {PLAIN}, which is still on disk": (
                PARENT,
                {"phases": [FIRST, PHASE], "retired": [{"plan": PLAIN, "status": "abandoned", "landed": None}]},
            ),
        }
        for rule, (plan, fields) in cases.items():
            with self.subTest(rule=rule):
                self.put(PARENT, phases=[FIRST, PHASE])
                self.put(FIRST, parent=PARENT)
                self.put(PHASE, parent=PARENT, after=[FIRST])
                self.put(PLAIN)
                self.check()
                self.put(plan, **fields)
                self.assertIn(rule, self.check(code=1).stderr)

    def test_check_rejects_a_phase_with_phases_of_its_own(self):
        self.put(PARENT, phases=[FIRST])
        self.put(FIRST, parent=PARENT, phases=[PHASE])
        self.put(PHASE, parent=FIRST)
        self.put(PLAIN)
        self.assertIn("phases or retired is non-empty with parent set", self.check(code=1).stderr)

    def test_reviewed_plan_that_is_still_planned_is_legal(self):
        self.put(PLAIN, review="fixes needed", review_rounds=1)
        for plan in (PARENT, FIRST, PHASE):
            self.put(plan)
        self.check()


if __name__ == "__main__":
    unittest.main()
