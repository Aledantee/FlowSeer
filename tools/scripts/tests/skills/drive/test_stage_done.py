import contextlib
import io
import json
import os
from pathlib import Path
import re
import shutil
import unittest

from lib import plans, repo
from skills.drive import stage_done
from tests.skills.delegate import test_orca_worker


class StageDoneTests(unittest.TestCase):
    def setUp(self):
        self.worker = test_orca_worker.OrcaWorkerTests()
        self.worker.setUp()
        self.addCleanup(self.worker.doCleanups)
        self.root = self.worker.repo
        self.plan = "docs/plans/x-plan.md"
        path = self.root / self.plan
        path.parent.mkdir(parents=True)
        path.write_text("# Example plan\n")
        self.state = dict(plans.BLANK)
        self.state.update(status="planned", readiness="implementation-ready")
        self.write()

    def write(self):
        (self.root / plans.state_path(self.plan)).write_text(json.dumps(self.state))

    def command(self, stage):
        previous = Path.cwd()
        try:
            os.chdir(self.root)
            with contextlib.redirect_stderr(io.StringIO()):
                return stage_done.main([self.plan, stage])
        finally:
            os.chdir(previous)

    def test_pending_and_done_for_each_stage(self):
        cases = {
            "re-plan": ({"status": "implemented", "review": "rework"}, {"status": "planned", "review": None}),
            "implement": ({"status": "planned"}, {"status": "implemented"}),
            "review": ({"review": "fixes needed"}, {"review": "accept"}),
            "compound": ({"compound": None}, {"compound": "no lesson"}),
        }
        for stage, (pending, done) in cases.items():
            with self.subTest(stage=stage):
                self.state.update(pending)
                self.write()
                self.assertEqual(self.command(stage), 1)
                self.state.update(done)
                self.write()
                self.assertEqual(self.command(stage), 0)
        self.state.update(status="planned", readiness="needs-decisions")
        self.write()
        self.assertEqual(self.command("re-plan"), 1)

    def test_review_accepts_both_verdicts(self):
        for verdict in ("accept", "accept after fixes"):
            with self.subTest(verdict=verdict):
                self.state.update(review=verdict)
                self.write()
                self.assertEqual(self.command("review"), 0)

    def test_implementation_outcome_does_not_finish_compound(self):
        self.state.update(outcome={"units": 1, "from": "2026-01-01T00:00Z", "to": "2026-01-01T00:01Z", "note": "complete"}, compound=None)
        self.write()
        self.assertEqual(self.command("compound"), 1)

    def test_invalid_state_returns_two(self):
        for value in ("{", "{}"):
            with self.subTest(value=value):
                (self.root / plans.state_path(self.plan)).write_text(value)
                self.assertEqual(self.command("implement"), 2)
        (self.root / plans.state_path(self.plan)).unlink()
        self.assertEqual(self.command("implement"), 2)
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as error:
            stage_done.main([self.plan, "unknown"])
        self.assertEqual(error.exception.code, 2)

    def test_follows_the_recorded_unmerged_branch(self):
        self.state.update(branch="work")
        self.write()
        self.worker.git("add", ".")
        self.worker.git("commit", "-m", "plan")
        self.worker.git("checkout", "-b", "work")
        self.state.update(status="implemented")
        self.write()
        self.worker.git("add", ".")
        self.worker.git("commit", "-m", "done")
        self.worker.git("checkout", "main")
        self.assertEqual(self.command("implement"), 0)

    def test_documented_until_examples_through_wait_pending_and_done(self):
        source = repo.root(Path(__file__).parent)
        shutil.copytree(source / "tools/scripts", self.root / "tools/scripts")
        skill = (source / ".agents/skills/drive/SKILL.md").read_text()
        examples = re.findall(r"--until '(\[[^'\n]+\])'", skill)
        self.assertEqual(len(examples), 4)
        solution = (source / "docs/solutions/conventions/a-stage-done-test-must-fail-where-the-stage-applies.md").read_text()
        examples += re.findall(r"--until '(\[[^'\n]+\])'", solution)
        self.assertEqual(len(examples), 5)
        lane = self.worker.live_lane()
        state_file = self.worker.state_dir / "l1.json"
        state = json.loads(state_file.read_text())
        state["path"] = str(self.root)
        state_file.write_text(json.dumps(state))
        self.assertTrue(lane.is_dir())
        self.worker.env["ORCA_STUB_SCREEN"] = "> done"
        pending = {"re-plan": {"status": "implemented", "review": "rework"}, "implement": {"status": "planned"}, "review": {"review": "fixes needed"}, "compound": {"compound": None}}
        done = {"re-plan": {"status": "planned", "review": None}, "implement": {"status": "implemented"}, "review": {"review": "accept after fixes"}, "compound": {"compound": "no lesson"}}
        for example in examples:
            stage = json.loads(example)[-1]
            for fields, outcome in ((pending[stage], "idle"), (done[stage], "done")):
                with self.subTest(stage=stage, outcome=outcome):
                    self.state.update(fields)
                    self.write()
                    self.worker.clock.write_text("0")
                    result = self.worker.command("wait", "l1", "--max", "10", "--until", example.replace("<plan>", self.plan))
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(result.stdout, f"{outcome}\n> done\n")
