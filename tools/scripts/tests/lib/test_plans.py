import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

from lib import plans

PLAN = "docs/plans/2026-01-01-one-plan.md"
OTHER = "docs/plans/2026-01-02-two-plan.md"
PHASE = "docs/plans/2026-01-03-phase-plan.md"


class UnitHeadingTest(unittest.TestCase):
    def test_counts_dot_colon_and_lettered_units(self):
        text = "### U1. a\n### U12: b\n### U3a. c\n"
        self.assertEqual(plans.unit_ids(text), ["U1", "U12", "U3a"])

    def test_skips_headings_the_one_rule_rejects(self):
        text = "### U1ab. d\n###  U1. e\n### U1.f\n#### U2. g\n"
        self.assertEqual(plans.unit_ids(text), [])

    def test_title_drops_the_plan_suffix(self):
        self.assertEqual(plans.title("---\nk: v\n---\n# Name Here - Plan\n"), "Name Here")
        self.assertEqual(plans.title("# Bare\n"), "Bare")
        self.assertEqual(plans.title("no heading\n"), "")

    def test_ledger_contract_lives_here(self):
        self.assertEqual(plans.LEDGER_NAME, "flowseer-plan-status.json")
        self.assertEqual(plans.LEDGER_CONTRACT, "flowseer-plan-status/v1")
        self.assertEqual(plans.UNIT_STATUSES, ("pending", "in_progress", "passed", "blocked"))


class ShapeTest(unittest.TestCase):
    def test_outcome_needs_a_positive_unit_count(self):
        outcome = {"units": 3, "from": "2026-01-05T10:00Z", "to": "2026-01-05T11:30:00Z", "note": ""}
        self.assertEqual(plans.shape_faults({**plans.BLANK, "outcome": outcome}), [])
        faults = plans.shape_faults({**plans.BLANK, "outcome": {**outcome, "units": 0}})
        self.assertEqual(len(faults), 1)
        self.assertIn("outcome must be null or", faults[0])

    def test_a_missing_key_is_named(self):
        state = {key: value for key, value in plans.BLANK.items() if key != "retired"}
        self.assertEqual(plans.shape_faults(state), ["missing key 'retired'"])


class MovedKeysTest(unittest.TestCase):
    def test_finds_status_in_frontmatter(self):
        text = "---\ntitle: x\nstatus: planned\nreview: accept\n---\n# T\n"
        self.assertEqual(plans.moved_keys(text), ["status", "review"])

    def test_no_frontmatter_holds_no_keys(self):
        self.assertEqual(plans.moved_keys("# T\nstatus: planned\n"), [])


class PathTest(unittest.TestCase):
    def test_plan_of_maps_plan_and_state_file(self):
        self.assertEqual(plans.plan_of(PLAN), PLAN)
        self.assertEqual(plans.plan_of(plans.state_path(PLAN)), PLAN)
        self.assertIsNone(plans.plan_of("docs/README.md"))


class RepoTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name).resolve()
        self.env = {
            **os.environ,
            "GIT_CONFIG_GLOBAL": os.devnull,
            "GIT_CONFIG_NOSYSTEM": "1",
            "GIT_AUTHOR_NAME": "test",
            "GIT_AUTHOR_EMAIL": "test@example.com",
            "GIT_COMMITTER_NAME": "test",
            "GIT_COMMITTER_EMAIL": "test@example.com",
        }
        self.git("init", "-q", "-b", "main")
        (self.root / "docs/plans").mkdir(parents=True)
        for plan in (PLAN, OTHER, PHASE):
            (self.root / plan).write_text("# Plan\n", encoding="utf-8")
        self.commit("plans")

    def git(self, *args):
        done = subprocess.run(
            ["git", *args], cwd=self.root, env=self.env, check=True, capture_output=True, text=True
        )
        return done.stdout.strip()

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-q", "-m", message)
        return self.git("rev-parse", "HEAD")

    def put(self, plan, **fields):
        """Writes a state file by hand, the way no command would."""
        path = self.root / plans.state_path(plan)
        path.write_text(json.dumps({**plans.BLANK, **fields}), encoding="utf-8")


class LoadTest(RepoTest):
    def test_missing_state_names_the_new_command(self):
        with self.assertRaises(plans.MissingState) as caught:
            plans.load(PLAN, self.root)
        self.assertIn(f"uv run tools/scripts/run.py plan record init {PLAN}", str(caught.exception))

    def test_loads_a_legal_file(self):
        self.put(PLAN)
        self.assertEqual(plans.load(PLAN, self.root)["status"], "planned")

    def test_invalid_json_and_shape_fault_are_invalid_state(self):
        (self.root / plans.state_path(PLAN)).write_text("{", encoding="utf-8")
        with self.assertRaises(plans.InvalidState):
            plans.load(PLAN, self.root)
        self.put(PLAN, status="bogus")
        with self.assertRaises(plans.InvalidState) as caught:
            plans.load(PLAN, self.root)
        self.assertIn("status must be one of", str(caught.exception))

    def test_outside_a_repository_is_a_state_error(self):
        with tempfile.TemporaryDirectory() as bare:
            cwd = os.getcwd()
            os.chdir(bare)
            self.addCleanup(os.chdir, cwd)
            with self.assertRaises(plans.StateError):
                plans.load(PLAN)


class StatusTest(RepoTest):
    def test_status_is_the_stored_one_for_a_plain_plan(self):
        self.put(PLAN, status="implemented")
        self.assertEqual(plans.status(PLAN, self.root), "implemented")

    def test_a_parent_with_only_a_landed_retired_phase_is_implemented(self):
        landed = {"first": "abcdef1", "last": "abcdef2"}
        self.put(PLAN, retired=[{"plan": PHASE, "status": "implemented", "landed": landed}])
        self.assertEqual(plans.status(PLAN, self.root), "implemented")
        self.put(PLAN, phases=[OTHER])
        self.assertEqual(plans.status(PLAN, self.root), "planned")

    def test_followed_reads_the_state_on_an_unmerged_branch(self):
        self.put(PLAN, branch="work")
        self.commit("record branch")
        self.git("branch", "-q", "work")
        self.git("checkout", "-q", "work")
        self.put(PLAN, branch="work", status="implemented")
        self.commit("work")
        self.git("checkout", "-q", "main")
        self.assertEqual(plans.status(PLAN, self.root), "implemented")

    def test_sent_back_needs_an_implemented_plan_with_rework_or_open_decisions(self):
        base = dict(plans.BLANK)
        self.assertFalse(plans.sent_back({**base, "review": "rework"}))
        self.assertTrue(plans.sent_back({**base, "status": "implemented", "review": "rework"}))
        self.assertTrue(
            plans.sent_back({**base, "status": "implemented", "readiness": "needs-decisions"})
        )

    def test_finished_phase_on_main_frees_dependents(self):
        sha = self.git("rev-parse", "HEAD")
        self.put(PLAN, phases=[PHASE])
        self.put(PHASE, parent=PLAN, status="implemented", landed={"first": sha, "last": sha})
        self.assertTrue(plans.finished(PHASE, self.root))
        self.put(PHASE, parent=PLAN)
        self.assertFalse(plans.finished(PHASE, self.root))


class ProblemsTest(RepoTest):
    def test_tree_without_state_files_names_the_init_command(self):
        found = plans.problems(*plans.read_tree(self.root))
        self.assertIn((PLAN, "has no state file; run `uv run tools/scripts/run.py plan record init`"), found)

    def test_clean_tree_has_no_problems(self):
        for plan in (PLAN, OTHER, PHASE):
            self.put(plan)
        self.assertEqual(plans.problems(*plans.read_tree(self.root)), [])


if __name__ == "__main__":
    unittest.main()
