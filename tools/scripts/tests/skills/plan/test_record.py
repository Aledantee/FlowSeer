import contextlib
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest import mock

from lib import plans, proc
from skills.plan import record as record_module

RUN_PY = Path(__file__).resolve().parents[3] / "run.py"
PARENT = "docs/plans/2026-01-01-parent-plan.md"
FIRST = "docs/plans/2026-01-02-first-plan.md"
PHASE = "docs/plans/2026-01-03-phase-plan.md"
PLAIN = "docs/plans/2026-01-04-plain-plan.md"
RUN = ("--units", "3", "--from", "2026-01-05T10:00Z", "--to", "2026-01-05T11:30:00Z")
OUTCOME = {"units": 3, "from": "2026-01-05T10:00Z", "to": "2026-01-05T11:30:00Z", "note": ""}
EMPTY = {
    "contract": "flowseer-plan-state/v1",
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


class PlanRecordTest(unittest.TestCase):
    """Runs `plan record` in a scratch repository whose branch `work` is
    one commit ahead of `main`."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
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
        self.git("init", "-q", "-b", "main")
        (self.root / "docs/plans").mkdir(parents=True)
        for plan in (PARENT, FIRST, PHASE, PLAIN):
            (self.root / plan).write_text("# Plan\n", encoding="utf-8")
        self.on_main = self.commit("plans")
        self.git("checkout", "-q", "-b", "work")
        (self.root / "code").write_text("work\n", encoding="utf-8")
        self.on_work = self.commit("work")

    def tearDown(self):
        self.directory.cleanup()

    def git(self, *args):
        out = subprocess.run(["git", *args], cwd=self.root, env=self.env, check=True, capture_output=True, text=True)
        return out.stdout.strip()

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-q", "-m", message)
        return self.git("rev-parse", "HEAD")

    def record(self, *args, code=0):
        out = subprocess.run(
            [sys.executable, str(RUN_PY), "plan", "record", *args], cwd=self.root, env=self.env, capture_output=True, text=True
        )
        self.assertEqual(out.returncode, code, out.stdout + out.stderr)
        return out

    def state(self, plan):
        return json.loads((self.root / plans.state_path(plan)).read_text(encoding="utf-8"))

    def put(self, plan, **fields):
        """Writes a state file by hand, the way a command never would."""
        path = self.root / plans.state_path(plan)
        path.write_text(json.dumps({**plans.BLANK, **fields}), encoding="utf-8")

    def phases(self):
        """A parent with FIRST and with PHASE, which runs after FIRST."""
        self.record("init", PARENT)
        self.record("init", FIRST, "--parent", PARENT)
        self.record("init", PHASE, "--parent", PARENT, "--after", FIRST)

    def land(self, plan, sha):
        self.record("implemented", plan, *RUN, "--landed", f"{sha}..{sha}")

    def followed_branch(self, plan=PLAIN):
        self.git("checkout", "-q", "main")
        if plan == FIRST:
            self.phases()
        else:
            self.record("init", plan)
        self.record("branch", plan, "work")
        self.commit("record branch")
        self.git("checkout", "-q", "work")
        self.git("merge", "-q", "main")

    def test_branch_sets_and_clears_a_name(self):
        self.record("init", PLAIN)
        self.record("branch", PLAIN, "alice/x-implement")
        self.assertEqual(self.state(PLAIN)["branch"], "alice/x-implement")
        self.assertIn("branch: alice/x-implement", self.record("show", PLAIN).stdout)
        self.record("is", PLAIN, "branch=alice/x-implement")
        self.record("branch", PLAIN, "--clear")
        self.assertIsNone(self.state(PLAIN)["branch"])

    def test_branch_refuses_an_unsafe_name(self):
        self.record("init", PLAIN)
        refused = self.record("branch", PLAIN, "a b", code=1)
        self.assertIn("branch", refused.stderr)
        self.assertIsNone(self.state(PLAIN)["branch"])

    def test_branch_refuses_a_parent(self):
        self.phases()
        refused = self.record("branch", PARENT, "work", code=1)
        for phase in (FIRST, PHASE):
            self.assertIn(phase, refused.stderr)
        self.assertIsNone(self.state(PARENT)["branch"])

    def test_branch_refuses_a_parent_whose_phases_are_retired(self):
        self.phases()
        for plan in (FIRST, PHASE):
            self.record("abandon", plan)
            self.commit("phase done")
            self.record("retire", plan)
            self.commit("phase retired")
        refused = self.record("branch", PARENT, "work", code=1)
        for phase in (FIRST, PHASE):
            self.assertIn(phase, refused.stderr)
        self.assertIsNone(self.state(PARENT)["branch"])

    def test_reads_follow_an_unmerged_branch(self):
        self.followed_branch()
        self.record("implemented", PLAIN, *RUN)
        self.record("branch", PLAIN, "--clear")
        self.commit("implemented on work")
        self.git("checkout", "-q", "main")
        self.record("is", PLAIN, "status=implemented")
        self.assertEqual(plans.status(PLAIN, self.root), "implemented")
        self.assertEqual(plans.followed(PLAIN, self.root)["branch"], "work")
        self.assertIn("read from: work", self.record("show", PLAIN).stdout)
        self.assertEqual(self.state(PLAIN)["status"], "planned")

    def test_reads_checkout_after_branch_merges(self):
        self.followed_branch()
        self.record("implemented", PLAIN, *RUN)
        self.commit("implemented on work")
        self.git("checkout", "-q", "main")
        self.git("merge", "-q", "work")
        self.put(PLAIN, **{**self.state(PLAIN), "status": "planned"})
        self.assertEqual(plans.status(PLAIN, self.root), "planned")
        self.record("is", PLAIN, "status=planned")
        self.assertNotIn("read from:", self.record("show", PLAIN).stdout)

    def test_missing_branch_reads_checkout(self):
        self.record("init", PLAIN)
        self.record("branch", PLAIN, "gone")
        self.record("is", PLAIN, "status=planned")
        self.assertEqual(plans.status(PLAIN, self.root), "planned")
        self.assertIn("branch: gone (missing, read from this checkout)", self.record("show", PLAIN).stdout)

    def test_init_writes_every_key_of_the_contract(self):
        self.record("init", PLAIN)
        written = (self.root / plans.state_path(PLAIN)).read_text(encoding="utf-8")
        self.assertEqual(
            written,
            "{\n"
            '  "contract": "flowseer-plan-state/v1",\n'
            '  "status": "planned",\n'
            '  "readiness": "implementation-ready",\n'
            '  "review": null,\n'
            '  "review_rounds": 0,\n'
            '  "compound": null,\n'
            '  "outcome": null,\n'
            '  "superseded_by": null,\n'
            '  "branch": null,\n'
            '  "parent": null,\n'
            '  "after": [],\n'
            '  "landed": null,\n'
            '  "phases": [],\n'
            '  "retired": []\n'
            "}\n",
        )
        self.record("init", PLAIN, code=1)
        self.record("init", FIRST, "--needs-decisions")
        self.assertEqual(self.state(FIRST)["readiness"], "needs-decisions")

    def test_init_of_a_phase_joins_its_parent(self):
        self.phases()
        self.assertEqual(self.state(PARENT)["phases"], [FIRST, PHASE])
        self.assertEqual(self.state(PHASE)["parent"], PARENT)
        self.assertEqual(self.state(PHASE)["after"], [FIRST])
        self.assertEqual(self.state(PARENT)["parent"], None)

    def test_ready_sets_the_readiness(self):
        self.record("init", PLAIN, "--needs-decisions")
        self.record("ready", PLAIN)
        self.assertEqual(self.state(PLAIN), plans.BLANK)

    def test_after_replaces_the_prerequisites(self):
        self.phases()
        self.record("init", PLAIN, "--parent", PARENT)
        self.record("after", PHASE, PLAIN)
        self.assertEqual(self.state(PHASE)["after"], [PLAIN])
        self.record("after", PHASE)
        self.assertEqual(self.state(PHASE)["after"], [])
        self.record("after", PHASE, PARENT, code=1)

    def test_implemented_records_the_outcome(self):
        self.record("init", PLAIN)
        self.record("implemented", PLAIN, *RUN)
        self.assertEqual(self.state(PLAIN), {**plans.BLANK, "status": "implemented", "outcome": OUTCOME})
        self.record("implemented", PLAIN, "--units", "3", "--from", "yesterday", "--to", "today", code=1)

    def test_outcome_times_are_real_utc_times(self):
        self.record("init", PLAIN)
        for start in ("2026-99-99T25:61Z", "2026-02-30T10:00:00Z", "2026-01-05T10:00"):
            with self.subTest(start=start):
                self.record("implemented", PLAIN, "--units", "1", "--from", start, "--to", "2026-01-05T11:00Z", code=1)

    def test_implemented_phase_needs_a_range_in_this_tree(self):
        self.phases()
        refused = self.record("implemented", FIRST, *RUN, code=1)
        self.assertIn("a phase is implemented with --landed", refused.stderr)
        self.assertEqual(self.state(FIRST)["status"], "planned")
        elsewhere = self.git("commit-tree", "-m", "elsewhere", "HEAD^{tree}")
        self.record("implemented", FIRST, *RUN, "--landed", f"{self.on_main}..{elsewhere}", code=1)
        self.record("implemented", FIRST, *RUN, "--landed", f"{self.on_main}..{self.on_work}")
        self.assertEqual(self.state(FIRST)["landed"], {"first": self.on_main, "last": self.on_work})
        self.assertEqual(self.state(FIRST)["status"], "implemented")
        self.record("implemented", FIRST, "--units", "4", "--from", "2026-01-06T10:00Z", "--to", "2026-01-06T11:00Z", code=1)
        self.assertEqual(self.state(FIRST)["outcome"], OUTCOME)

    def test_partial_records_what_is_left(self):
        self.record("init", PLAIN)
        self.record("partial", PLAIN, *RUN, "--note", "U3 blocked on a ruling")
        self.assertEqual(self.state(PLAIN)["status"], "partially-implemented")
        self.assertEqual(self.state(PLAIN)["outcome"], {**OUTCOME, "note": "U3 blocked on a ruling"})
        self.record("partial", PLAIN, *RUN, "--note", " ", code=1)

    def test_review_keeps_the_round_count_unless_given(self):
        self.record("init", PLAIN)
        self.record("review", PLAIN, "fixes needed", "--rounds", "2")
        self.assertEqual((self.state(PLAIN)["review"], self.state(PLAIN)["review_rounds"]), ("fixes needed", 2))
        self.record("review", PLAIN, "accept after fixes")
        self.assertEqual((self.state(PLAIN)["review"], self.state(PLAIN)["review_rounds"]), ("accept after fixes", 2))
        self.assertEqual(self.state(PLAIN)["status"], "planned")

    def test_compound_records_the_outcome(self):
        self.record("init", PLAIN)
        self.record("compound", PLAIN, "no lesson")
        self.assertEqual(self.state(PLAIN)["compound"], "no lesson")

    def test_replan_clears_the_last_run(self):
        self.phases()
        self.land(FIRST, self.on_work)
        self.record("review", FIRST, "rework", "--rounds", "3")
        self.record("compound", FIRST, "no lesson")
        self.record("replan", FIRST, "--needs-decisions")
        self.assertEqual(
            self.state(FIRST), {**plans.BLANK, "parent": PARENT, "readiness": "needs-decisions"}
        )
        self.record("replan", FIRST)
        self.assertEqual(self.state(FIRST)["readiness"], "implementation-ready")

    def test_replan_deletes_only_a_ledger_naming_the_plan(self):
        ledger = self.root / ".git/flowseer-plan-status.json"
        self.record("init", PLAIN)
        ledger.write_text(json.dumps({"plan": FIRST}), encoding="utf-8")
        self.record("replan", PLAIN)
        self.assertTrue(ledger.exists())
        ledger.write_text(json.dumps({"plan": PLAIN}), encoding="utf-8")
        self.record("replan", PLAIN)
        self.assertFalse(ledger.exists())

    def test_replan_stops_on_a_ledger_it_cannot_read(self):
        ledger = self.root / ".git/flowseer-plan-status.json"
        self.record("init", PLAIN)
        self.record("implemented", PLAIN, *RUN)
        ledger.write_text("{", encoding="utf-8")
        self.record("replan", PLAIN, code=1)
        self.assertEqual(self.state(PLAIN)["status"], "implemented")

    def test_supersede_names_the_successor(self):
        self.record("init", PLAIN)
        self.record("supersede", PLAIN, "--by", FIRST)
        self.assertEqual((self.state(PLAIN)["status"], self.state(PLAIN)["superseded_by"]), ("superseded", FIRST))

    def test_superseded_plan_can_be_replanned_or_abandoned(self):
        self.record("init", PLAIN)
        self.record("supersede", PLAIN, "--by", FIRST)
        self.record("replan", PLAIN)
        self.assertEqual(self.state(PLAIN), EMPTY)
        self.record("supersede", PLAIN, "--by", FIRST)
        self.record("abandon", PLAIN)
        self.assertEqual(self.state(PLAIN), {**EMPTY, "status": "abandoned"})

    def test_abandon_sets_the_status(self):
        self.record("init", PLAIN)
        self.record("abandon", PLAIN)
        self.assertEqual(self.state(PLAIN)["status"], "abandoned")

    def test_retire_moves_a_phase_into_its_parents_retired_list(self):
        self.phases()
        self.land(FIRST, self.on_work)
        self.record("review", FIRST, "accept")
        self.record("compound", FIRST, "docs/solutions/a.md")
        self.commit("phase done")
        printed = self.record("retire", FIRST).stdout.splitlines()
        self.assertEqual(
            printed,
            [
                "> Implemented. 3 units, 2026-01-05T10:00Z to 2026-01-05T11:30:00Z.",
                "review: accept",
                "compound: docs/solutions/a.md",
            ],
        )
        landed = {"first": self.on_work, "last": self.on_work}
        self.assertEqual(self.state(PARENT)["phases"], [PHASE])
        self.assertEqual(self.state(PARENT)["retired"], [{"plan": FIRST, "status": "implemented", "landed": landed}])
        staged = self.git("diff", "--cached", "--name-status").splitlines()
        self.assertEqual(
            staged,
            [f"M\t{plans.state_path(PARENT)}", f"D\t{FIRST}", f"D\t{plans.state_path(FIRST)}"],
        )
        checked = subprocess.run(
            [sys.executable, str(RUN_PY), "verify", "check-plan-state", PARENT, PHASE],
            cwd=self.root,
            env=self.env,
            capture_output=True,
            text=True,
        )
        self.assertEqual(checked.returncode, 0, checked.stdout + checked.stderr)

    def test_retire_refuses_a_plan_with_work_left(self):
        self.record("init", PLAIN)
        self.commit("state")
        self.record("retire", PLAIN, code=1)
        self.assertTrue((self.root / PLAIN).exists())
        self.record("abandon", PLAIN)
        with (self.root / PLAIN).open("a", encoding="utf-8") as plan:
            plan.write("An uncommitted decision.\n")
        self.record("retire", PLAIN, code=1)
        self.assertTrue((self.root / PLAIN).exists())
        self.commit("decision")
        self.assertEqual(self.record("retire", PLAIN).stdout.splitlines()[0], "> Abandoned.")
        self.assertFalse((self.root / PLAIN).exists())

    def test_command_that_fails_halfway_leaves_both_files_as_they_were(self):
        self.record("init", PARENT)
        before = self.state(PARENT)
        child = {**EMPTY, "parent": PARENT}
        real = record_module.write_state

        def first_write_only(root, plan, state):
            if plan == PARENT:
                raise OSError("disk full")
            real(root, plan, state)

        with mock.patch.object(record_module, "write_state", first_write_only):
            with self.assertRaises(SystemExit), contextlib.redirect_stderr(io.StringIO()):
                record_module.transition(self.root, {FIRST: child, PARENT: {**before, "phases": [FIRST]}})
        self.assertFalse((self.root / plans.state_path(FIRST)).exists())
        self.assertEqual(self.state(PARENT), before)

    def test_retire_whose_git_rm_fails_leaves_the_parent_as_it_was(self):
        self.phases()
        self.record("abandon", FIRST)
        self.commit("phase abandoned")
        before = self.state(PARENT)
        (self.root / ".git/index.lock").touch()
        refused = self.record("retire", FIRST, code=1)
        self.assertEqual(self.state(PARENT), before)
        self.assertIn("nothing changed: git rm failed", refused.stderr)
        self.assertTrue((self.root / FIRST).exists())

    def test_retire_reports_a_parent_state_it_could_not_stage(self):
        self.phases()
        self.record("abandon", FIRST)
        self.commit("phase abandoned")
        real = record_module.git

        def git(root, *args):
            return proc.Result(1, "", "index.lock") if args[0] == "add" else real(root, *args)

        holder = {**self.state(PARENT), "phases": [PHASE]}
        holder["retired"] = [{"plan": FIRST, "status": "abandoned", "landed": None}]
        with mock.patch.object(record_module, "git", git):
            with self.assertRaises(SystemExit) as stopped, contextlib.redirect_stderr(io.StringIO()):
                record_module.transition(self.root, {PARENT: holder}, retire=FIRST)
        self.assertEqual(stopped.exception.code, 1)

    def test_show_prints_a_parents_computed_status(self):
        self.phases()
        self.land(FIRST, self.on_work)
        self.commit("phase done")
        self.record("retire", FIRST)
        self.record("abandon", PHASE)
        self.record("retire", PHASE)
        self.assertEqual(self.state(PARENT)["status"], "planned")
        self.assertIn("  status: implemented\n", self.record("show", PARENT).stdout)
        self.assertEqual(json.loads(self.record("show", PARENT, "--json").stdout)["status"], "implemented")

    def test_is_exits_by_the_field(self):
        self.record("init", PLAIN)
        self.record("review", PLAIN, "accept after fixes")
        self.record("is", PLAIN, "review=accept after fixes")
        self.record("is", PLAIN, "review=accept", code=1)
        self.record("is", PLAIN, "compound=null")
        self.record("is", PLAIN, "compound!=null", code=1)
        self.record("compound", PLAIN, "no lesson")
        self.record("is", PLAIN, "compound!=null")
        self.record("is", PLAIN, "review_rounds=0")
        self.record("is", PLAIN, "verdict=accept", code=2)
        self.record("is", FIRST, "status=planned", code=2)

    def test_command_refuses_a_result_check_would_reject(self):
        self.phases()
        before = self.state(PARENT)
        refused = self.record("implemented", PARENT, *RUN, code=1)
        self.assertIn("a parent's status is computed from its phases", refused.stderr)
        self.assertEqual(self.state(PARENT), before)
        self.record("implemented", PLAIN, *RUN, code=2)
        self.assertFalse((self.root / plans.state_path(PLAIN)).exists())


if __name__ == "__main__":
    unittest.main()
