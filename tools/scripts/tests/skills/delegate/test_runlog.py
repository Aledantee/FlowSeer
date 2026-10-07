import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from skills.delegate import runlog


RUN = Path(__file__).resolve().parents[3] / "run.py"


class RunlogTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.log = Path(self.temp.name) / "runs.jsonl"
        self.env = {**os.environ, "FLOWSEER_RUNLOG": str(self.log)}

    def command(self, *args, cwd=None):
        return subprocess.run(
            [sys.executable, str(RUN), "delegate", "runlog", *args],
            env=self.env, cwd=cwd, capture_output=True, text=True, check=False,
        )

    def test_start_writes_lane_and_prints_run(self):
        result = self.command(
            "start", "--lane", "l1", "--cli", "codex", "--model", "gpt-6-sol",
            "--role", "execute", "--worktree", "/w/l1", "--branch", "u/l1",
            "--base", "abc123",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        events = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertEqual(len(events), 1)
        event = events[0]
        self.assertEqual(result.stdout.strip(), event["run"])
        self.assertEqual(event["event"], "start")
        self.assertEqual(event["v"], 1)
        self.assertEqual(
            {key: event[key] for key in ("lane", "cli", "model", "role", "worktree", "branch", "base")},
            {"lane": "l1", "cli": "codex", "model": "gpt-6-sol", "role": "execute",
             "worktree": "/w/l1", "branch": "u/l1", "base": "abc123"},
        )

    def test_invalid_role_writes_nothing(self):
        result = self.command(
            "start", "--lane", "l1", "--cli", "codex", "--model", "gpt-6-sol",
            "--role", "Execute", "--worktree", "/w/l1", "--branch", "u/l1",
            "--base", "abc123",
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.log.exists())

    def test_parallel_review_writes_complete_lines(self):
        processes = [subprocess.Popen(
            [sys.executable, str(RUN), "delegate", "runlog", "review", "--model", "claude-opus-5-5",
             "--role", "review-unit", "--agent", f"a{index}",
             "--findings", "4", "--held", "3", "--unverified", "1"],
            env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        ) for index in range(20)]
        for process in processes:
            _, stderr = process.communicate(timeout=10)
            self.assertEqual(process.returncode, 0, stderr)
        events = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertEqual(len(events), 20)
        self.assertEqual({event["agent"] for event in events}, {f"a{i}" for i in range(20)})

    def test_review_counts_are_validated(self):
        result = self.command(
            "review", "--model", "claude-opus-5-5", "--role", "review-unit",
            "--agent", "a1", "--findings", "4", "--held", "3", "--unverified", "1",
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        event = json.loads(self.log.read_text().splitlines()[0])
        self.assertEqual(
            (event["model"], event["role"], event["agent"],
             event["findings"], event["held"], event["unverified"]),
            ("claude-opus-5-5", "review-unit", "a1", 4, 3, 1),
        )
        invalid = self.command(
            "review", "--model", "claude-opus-5-5", "--role", "review-unit",
            "--agent", "a2", "--findings", "4", "--held", "5", "--unverified", "0",
        )
        self.assertNotEqual(invalid.returncode, 0)
        self.assertEqual(len(self.log.read_text().splitlines()), 1)

    def test_read_skips_truncated_line(self):
        self.log.write_text('{"v":1,"event":"grade","run":"r","at":"2026-09-23T00:00:00Z"}\n{"v":')
        result = runlog.read(self.log)
        self.assertEqual(len(list(result)), 1)
        self.assertEqual(result.skipped, 1)

    def test_append_after_damaged_tail_keeps_each_event_readable(self):
        commands = {
            "start": ("--lane", "l1", "--cli", "codex", "--model", "gpt-6-sol",
                      "--role", "execute", "--worktree", "/w/l1", "--branch", "u/l1",
                      "--base", "abc123"),
            "grade": ("--run", "r1", "--outcome", "accepted", "--verify", "pass"),
            "end": ("--run", "r1", "--head", "def456"),
            "review": ("--model", "claude-opus-5-5", "--role", "review-unit",
                       "--agent", "a1", "--findings", "1", "--held", "1",
                       "--unverified", "0"),
        }
        for tail in (b'{"v":', b'{"v":"\xe2'):
            for kind, args in commands.items():
                with self.subTest(tail=tail, event=kind):
                    self.log.write_bytes(tail)
                    result = self.command(kind, *args)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    lines = self.log.read_bytes().splitlines()
                    self.assertEqual(len(lines), 2)
                    self.assertEqual(json.loads(lines[1])["event"], kind)
                    read_result = runlog.read(self.log)
                    self.assertEqual(read_result.skipped, 1)
                    self.assertEqual([event["event"] for event in read_result], [kind])

    def seed(self, *events):
        self.log.write_text("".join(json.dumps(event) + "\n" for event in events))

    def start_event(self, run, at, model="claude-opus-5-5"):
        return {"v": 1, "event": "start", "run": run, "at": at, "lane": "l1",
                "cli": "claude", "model": model, "role": "execute",
                "worktree": "/w/l1", "branch": "u/l1", "base": "abc123"}

    def test_last_start_prints_model_and_time_of_the_last_start(self):
        self.seed(
            self.start_event("r1", "2026-09-30T10:00:00Z", "claude-sonnet-5-5"),
            self.start_event("r1", "2026-09-30T12:00:00Z", "claude-opus-5-5"),
            self.start_event("r2", "2026-09-30T13:00:00Z", "claude-haiku-4-5"),
        )
        result = self.command("last-start", "--run", "r1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "claude-opus-5-5\t2026-09-30T12:00:00Z\n")

    def test_last_start_prints_an_empty_line_without_a_start(self):
        self.seed({"v": 1, "event": "end", "run": "r1", "at": "2026-09-30T10:00:00Z", "head": "h"})
        result = self.command("last-start", "--run", "r1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "\n")

    def test_last_start_prints_an_empty_line_without_a_log(self):
        result = self.command("last-start", "--run", "r1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "\n")
        self.assertFalse(self.log.exists())

    def test_has_grade_exits_zero_only_for_a_graded_run(self):
        grade = {"v": 1, "event": "grade", "run": "r1", "at": "2026-09-30T10:00:00Z",
                 "outcome": "accepted", "verify": "pass"}
        self.seed(self.start_event("r1", "2026-09-30T10:00:00Z"), grade)
        self.assertEqual(self.command("has-grade", "--run", "r1").returncode, 0)
        self.assertEqual(self.command("has-grade", "--run", "r2").returncode, 1)
        self.assertFalse(self.command("has-grade", "--run", "r1").stdout)

    def test_has_grade_exits_one_without_a_log(self):
        self.assertEqual(self.command("has-grade", "--run", "r1").returncode, 1)
        self.assertFalse(self.log.exists())

    def test_start_base_prints_the_first_matching_start(self):
        first = self.start_event("r1", "2026-09-30T10:00:00Z")
        later = {**first, "base": "later"}
        self.seed(self.start_event("r2", "2026-09-30T09:00:00Z"), first, later)
        result = self.command("start-base", "--run", "r1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "abc123\n")

    def test_start_base_fails_for_an_unknown_run(self):
        self.seed(self.start_event("r1", "2026-09-30T10:00:00Z"))
        result = self.command("start-base", "--run", "unknown")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stderr, "runlog: no start event for run unknown\n")
        self.assertEqual(result.stdout, "")

    def test_start_base_fails_without_a_log(self):
        result = self.command("start-base", "--run", "missing-run")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stderr, "runlog: no start event for run missing-run\n")
        self.assertEqual(result.stdout, "")
        self.assertFalse(self.log.exists())

    def test_executors_prints_complete_records_with_a_truncated_tail(self):
        self.seed(
            {**self.start_event("r1", "2026-09-30T10:00:00Z"), "plan": "plan.md", "unit": "U1"},
            {"event": "grade", "run": "r1", "outcome": "accepted"},
            {**self.start_event("r2", "2026-09-30T11:00:00Z"), "plan": "plan.md",
             "model": None, "agent": "pool-id"},
            {"event": "grade", "run": "r2", "outcome": "amended"},
        )
        with self.log.open("a") as stream:
            stream.write('{"v":')
        result = self.command("executors", "--plan", "plan.md")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "U1 claude-opus-5-5\n- pool-id\n")
        self.assertEqual(result.stderr, "")

    def test_executors_uses_last_grade_and_excludes_rejected_runs(self):
        def start(run, **fields):
            return {**self.start_event(run, "2026-09-30T10:00:00Z"), "plan": "plan.md", **fields}

        def grade(run, outcome):
            return {"event": "grade", "run": run, "outcome": outcome}

        self.seed(
            start("accepted", unit="U1"), grade("accepted", "rejected"), grade("accepted", "accepted"),
            start("amended", role="execute-sensitive", model=None, agent="pool-id"),
            grade("amended", "amended"),
            start("rejected"), grade("rejected", "accepted"), grade("rejected", "rejected"),
            start("blocked"), grade("blocked", "blocked"), start("ungraded"),
            start("reviewer", role="review-unit"), grade("reviewer", "accepted"),
            start("other-plan", plan="other.md"), grade("other-plan", "accepted"),
        )
        result = self.command("executors", "--plan", "plan.md")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, "U1 claude-opus-5-5\n- pool-id\n")
        empty = self.command("executors", "--plan", "unknown.md")
        self.assertEqual(empty.returncode, 0, empty.stderr)
        self.assertEqual(empty.stdout, "")

    def test_writers_selects_smallest_range_then_latest_start(self):
        repository = Path(self.temp.name) / "repo"
        repository.mkdir()

        def git(*args):
            return subprocess.run(["git", *args], cwd=repository, check=True,
                                  capture_output=True, text=True).stdout.strip()

        git("init", "-b", "main")
        git("config", "user.name", "Test User")
        git("config", "user.email", "test@example.com")
        commits = []
        for name in ("base", "outer", "inner", "coordinator"):
            git("commit", "--allow-empty", "-m", name)
            commits.append(git("rev-parse", "HEAD"))
        base, outer, inner, head = commits

        def events(run, start_base, end_head, at, outcome, **fields):
            return (
                {**self.start_event(run, at), "base": start_base, "plan": "plan.md", **fields},
                {"event": "grade", "run": run, "outcome": "accepted"},
                {"event": "grade", "run": run, "outcome": outcome},
                {"event": "end", "run": run, "head": end_head},
            )

        self.seed(
            *events("outer", base, inner, "2026-09-30T13:00:00Z", "accepted", model="outer-model"),
            *events("inner", outer, inner, "2026-09-30T10:00:00Z", "accepted", model="inner-model"),
            *events("tie", outer, inner, "2026-09-30T11:00:00Z", "amended", model=None, agent="tie-agent"),
            *events("rejected", inner, head, "2026-09-30T12:00:00Z", "rejected"),
            *events("blocked", inner, head, "2026-09-30T12:00:00Z", "blocked"),
            *events("reviewer", inner, head, "2026-09-30T12:00:00Z", "accepted", role="review-unit"),
            *events("other", inner, head, "2026-09-30T12:00:00Z", "accepted", plan="other.md"),
            {**self.start_event("unfinished", "2026-09-30T14:00:00Z"), "plan": "plan.md"},
            {"event": "grade", "run": "unfinished", "outcome": "accepted"},
        )
        result = self.command("writers", "--plan", "plan.md", "--range", f"{base}..{head}",
                              "--coordinator", "coordinator-model", cwd=repository)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, f"{head[:12]} coordinator-model\n{inner[:12]} tie-agent\n"
                                      f"{outer[:12]} outer-model\n")
        for revision in ("missing..HEAD", f"{base}..{head}"):
            with self.subTest(revision=revision):
                if revision != "missing..HEAD":
                    self.seed(*events("invalid", "missing", head, "2026-09-30T10:00:00Z", "accepted"))
                invalid = self.command("writers", "--plan", "plan.md", "--range", revision,
                                       "--coordinator", "coordinator-model", cwd=repository)
                self.assertNotEqual(invalid.returncode, 0)
                self.assertIn("missing", invalid.stderr)


if __name__ == "__main__":
    unittest.main()
