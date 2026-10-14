import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from lib import plans

RUN_PY = Path(__file__).resolve().parents[2] / "run.py"
LEDGER_NAME = "flowseer-plan-status.json"
LEDGER_CONTRACT = "flowseer-plan-status/v1"


class StatusCase(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.base = Path(directory.name).resolve()
        self.env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        self.env.update(GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")

    def git(self, repo: Path, *args: str) -> str:
        done = subprocess.run(
            ["git", "-c", "user.name=Hook", "-c", "user.email=hook@example.invalid", "-C", str(repo), *args],
            capture_output=True,
            text=True,
            check=False,
            env=self.env,
        )
        self.assertEqual(done.returncode, 0, f"git {' '.join(args)}: {done.stderr}")
        return done.stdout.strip()

    def run_script(self, cwd: Path, *args: str) -> subprocess.CompletedProcess[str]:
        """Run `run.py` with args in cwd, standard error merged into standard output."""
        return subprocess.run(
            [sys.executable, str(RUN_PY), *args],
            cwd=cwd,
            env=self.env,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            check=False,
        )


class LedgerShapeTest(StatusCase):
    def setUp(self):
        super().setUp()
        self.repo = self.base / "ledger-fixture"
        (self.repo / "docs/plans").mkdir(parents=True)
        self.git(self.repo, "init", "-q")
        (self.repo / "docs/plans/example-plan.md").write_text("# ledger fixture plan\n")
        done = self.run_script(self.repo, "plan", "record", "init", "docs/plans/example-plan.md")
        self.assertEqual(done.returncode, 0, done.stdout)
        self.ledger = self.repo / ".git" / LEDGER_NAME

    def check(self, *args: str, cwd: Path | None = None) -> subprocess.CompletedProcess[str]:
        return self.run_script(cwd or self.repo, "verify", "check-plan-status", *args)

    def write_ledger(self, status: str):
        self.ledger.write_text(
            json.dumps(
                {
                    "contract": LEDGER_CONTRACT,
                    "plan": "docs/plans/example-plan.md",
                    "resume": ["U2"],
                    "units": [
                        {
                            "id": "U1",
                            "status": "passed",
                            "commit": "abc1234",
                            "verified_at": "2026-09-06T12:00:00Z",
                            "note": None,
                        },
                        {"id": "U2", "status": status, "commit": None, "verified_at": None, "note": None},
                    ],
                },
                indent=2,
            )
        )

    def test_plan_status_check_passes_when_no_ledger_exists(self):
        """plan status check passes when no ledger exists"""
        for args in ((), (str(self.ledger),)):
            with self.subTest(args=args):
                done = self.check(*args)
                self.assertEqual((done.returncode, done.stdout), (0, ""))

    def test_plan_status_check_accepts_a_well_formed_ledger(self):
        """plan status check accepts a well-formed ledger"""
        self.write_ledger("pending")
        for args in ((), (str(self.ledger),)):
            with self.subTest(args=args):
                done = self.check(*args)
                self.assertEqual((done.returncode, done.stdout), (0, ""))

    def test_plan_status_check_names_a_status_outside_the_allowed_values(self):
        """plan status check names a status outside the allowed values"""
        self.write_ledger("done")
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("units[1].status must be one of pending, in_progress, passed, blocked", done.stdout)

    def test_plan_status_check_rejects_a_ledger_whose_plan_does_not_exist(self):
        """plan status check rejects a ledger whose plan does not exist"""
        self.write_ledger("pending")
        self.ledger.write_text(self.ledger.read_text().replace("docs/plans/example-plan.md", "docs/plans/missing-plan.md"))
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("plan 'docs/plans/missing-plan.md' does not exist", done.stdout)

    def test_plan_status_check_resolves_the_plan_from_a_subdirectory_and_rejects_repeated_ids(self):
        """plan status check resolves the plan from a subdirectory and rejects repeated ids"""
        self.write_ledger("pending")
        deeper = self.repo / "src" / "deeper"
        deeper.mkdir(parents=True)
        done = self.check(cwd=deeper)
        self.assertEqual((done.returncode, done.stdout), (0, ""))
        self.ledger.write_text(self.ledger.read_text().replace('"id": "U2"', '"id": "U1"'))
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("units must not repeat an id", done.stdout)


class PhaseTest(StatusCase):
    """A ledger naming a phase plan, in a repository whose parent plan lists two phases."""

    def setUp(self):
        super().setUp()
        self.repo = self.base / "phase-fixture"
        (self.repo / "docs/plans").mkdir(parents=True)
        self.git(self.repo, "init", "-q", "-b", "main")
        for name in ("parent", "phase1", "phase2"):
            (self.repo / f"docs/plans/{name}-plan.md").write_text(f"# {name}\n")
        # Built from the keys the state contract names, since the states below
        # include ones no `plan record` command produces.
        self.state("parent", phases=["docs/plans/phase1-plan.md", "docs/plans/phase2-plan.md"])
        self.state("phase1", parent="docs/plans/parent-plan.md")
        self.state("phase2", parent="docs/plans/parent-plan.md", after=["docs/plans/phase1-plan.md"])
        self.git(self.repo, "add", "docs")
        self.git(self.repo, "commit", "-qm", "plans")
        self.head = self.git(self.repo, "rev-parse", "--short", "HEAD")
        (self.repo / ".git" / LEDGER_NAME).write_text(
            json.dumps(
                {
                    "contract": LEDGER_CONTRACT,
                    "plan": "docs/plans/phase2-plan.md",
                    "resume": ["U1"],
                    "units": [{"id": "U1", "status": "pending", "commit": None, "verified_at": None, "note": None}],
                }
            )
        )

    def state(self, name: str, **fields):
        (self.repo / f"docs/plans/{name}-plan.state.json").write_text(json.dumps({**plans.BLANK, **fields}))

    def phase1_landed(self, landed: dict):
        self.state("phase1", parent="docs/plans/parent-plan.md", status="implemented", landed=landed)

    def check(self) -> subprocess.CompletedProcess[str]:
        return self.run_script(self.repo, "verify", "check-plan-status")

    def landed_range(self) -> dict:
        return {"first": self.head, "last": self.head}

    def test_plan_status_check_refuses_a_phase_whose_prerequisite_has_not_landed(self):
        """plan status check refuses a phase whose prerequisite has not landed"""
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("phase docs/plans/phase1-plan.md has not landed", done.stdout)

    def test_plan_status_check_refuses_a_phase_whose_prerequisite_is_not_an_ancestor(self):
        """plan status check refuses a phase whose prerequisite is not an ancestor"""
        self.phase1_landed({"first": "0000000", "last": "1111111"})
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("landed at 1111111, which is not in this tree", done.stdout)

    def test_plan_status_check_refuses_a_landed_object_without_last(self):
        """plan status check refuses a landed object without last"""
        self.phase1_landed({"first": self.head})
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("landed must be null or {first, last} commit ids", done.stdout)

    def test_plan_status_check_accepts_a_phase_whose_prerequisite_is_in_the_tree(self):
        """plan status check accepts a phase whose prerequisite is in the tree"""
        self.phase1_landed(self.landed_range())
        done = self.check()
        self.assertEqual((done.returncode, done.stdout), (0, ""))

    def test_plan_status_check_accepts_a_prerequisite_retired_by_another_parent(self):
        """plan status check accepts a prerequisite retired by another parent"""
        (self.repo / "docs/plans/other-plan.md").write_text("# other\n")
        self.state(
            "other",
            retired=[{"plan": "docs/plans/otherphase-plan.md", "status": "implemented", "landed": self.landed_range()}],
        )
        self.state("phase2", parent="docs/plans/parent-plan.md", after=["docs/plans/otherphase-plan.md"])
        done = self.check()
        self.assertEqual((done.returncode, done.stdout), (0, ""))

    def test_plan_status_check_reads_a_prerequisite_with_no_parent_as_finished_or_not(self):
        """plan status check reads a prerequisite with no parent as finished or not"""
        (self.repo / "docs/plans/plain-plan.md").write_text("# plain\n")
        self.state("plain")
        self.state("phase2", parent="docs/plans/parent-plan.md", after=["docs/plans/plain-plan.md"])
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("plan docs/plans/plain-plan.md is not finished", done.stdout)
        self.state(
            "plain",
            status="implemented",
            readiness="implementation-ready",
            review="accept",
            compound="no lesson",
            outcome={"units": 1, "from": "2026-01-05T10:00Z", "to": "2026-01-05T11:30:00Z", "note": ""},
        )
        done = self.check()
        self.assertEqual((done.returncode, done.stdout), (0, ""))

    def test_plan_status_check_reads_a_prerequisite_main_retired_as_implemented(self):
        """plan status check reads a prerequisite main retired as implemented"""
        (self.repo / "docs/plans/plain-plan.md").write_text("# plain\n")
        self.state("plain")
        self.state("phase2", parent="docs/plans/parent-plan.md", after=["docs/plans/plain-plan.md"])
        self.git(self.repo, "add", "docs")
        self.git(self.repo, "commit", "-qm", "plain")
        self.git(self.repo, "rm", "-q", "docs/plans/plain-plan.md", "docs/plans/plain-plan.state.json")
        self.git(self.repo, "commit", "-qm", "docs(plans): retire plain", "-m", "> Abandoned.")
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("no retire commit on main", done.stdout)
        (self.repo / "docs/plans/plain-plan.md").write_text("# plain\n")
        self.git(self.repo, "add", "docs")
        self.git(self.repo, "commit", "-qm", "plain again")
        self.git(self.repo, "rm", "-q", "docs/plans/plain-plan.md")
        self.git(self.repo, "commit", "-qm", "docs(plans): retire plain", "-m", "> Implemented. 1 units, 2026-01-05T10:00Z to 2026-01-05T11:30:00Z.")
        done = self.check()
        self.assertEqual((done.returncode, done.stdout), (0, ""))

    def land_both(self):
        self.phase1_landed(self.landed_range())
        self.state(
            "phase2",
            parent="docs/plans/parent-plan.md",
            after=["docs/plans/phase1-plan.md"],
            status="implemented",
            landed=self.landed_range(),
        )
        self.git(self.repo, "add", "docs")
        self.git(self.repo, "commit", "-qm", "landed")

    def test_plan_status_check_refuses_a_phase_the_integration_branch_already_shows_landed(self):
        """plan status check refuses a phase the integration branch already shows landed"""
        self.land_both()
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("already landed on", done.stdout)

    def test_plan_status_check_refuses_a_phase_the_integration_branch_retired(self):
        """plan status check refuses a phase the integration branch retired"""
        self.land_both()
        # main keeps a retired phase only as an entry in its parent's state. A
        # worktree forked before the retire still holds the phase as planned.
        self.git(self.repo, "rm", "-q", "docs/plans/phase2-plan.md", "docs/plans/phase2-plan.state.json")
        self.state(
            "parent",
            phases=["docs/plans/phase1-plan.md"],
            retired=[{"plan": "docs/plans/phase2-plan.md", "status": "implemented", "landed": self.landed_range()}],
        )
        self.git(self.repo, "add", "docs")
        self.git(self.repo, "commit", "-qm", "retired")
        self.git(self.repo, "checkout", "-q", "-b", "forked-early", self.head)
        forked = json.loads((self.repo / "docs/plans/phase2-plan.state.json").read_text())
        self.assertEqual(forked["status"], "planned")
        done = self.check()
        self.assertEqual(done.returncode, 1)
        self.assertIn("is retired on the integration branch as implemented", done.stdout)


if __name__ == "__main__":
    unittest.main()
