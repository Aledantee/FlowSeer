"""Calibration launch and accounting contracts."""

from contextlib import redirect_stdout, redirect_stderr
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from skills.tune import bench


class BenchTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.worktree = self.root / "worktree"
        self.worktree.mkdir()
        self.brief = self.root / "brief.md"
        self.brief.write_text("calibrate lane")
        self.out = self.root / "out.json"
        self.payload = b""
        self.error = b""
        self.code = 0
        self.launch_error = None
        self.calls = []
        self.expect_cleared = False

    def run_bench(self, cli="omp", model="synthetic/hf:test", env_extra=None, effort=""):
        argv = ["--lane", "l1", "--cli", cli, "--model", model,
                "--brief", str(self.brief), "--dir", str(self.worktree), "--out", str(self.out)]
        if effort:
            argv += ["--effort", effort]
        env = os.environ.copy()
        env.update(env_extra or {})

        def launch(command, **kwargs):
            self.calls.append((command, kwargs))
            if self.expect_cleared:
                self.assertFalse(self.out.exists(), "stale result must be removed before launch")
            if self.launch_error:
                raise self.launch_error
            kwargs["stdout"].write(self.payload)
            kwargs["stderr"].write(self.error)
            return subprocess.CompletedProcess(command, self.code)

        stdout, stderr = io.StringIO(), io.StringIO()
        with patch.dict(os.environ, env, clear=True), patch.object(bench.subprocess, "run", launch), \
                patch.object(bench.shutil, "which", side_effect=lambda name: name), \
                redirect_stdout(stdout), redirect_stderr(stderr):
            try:
                code = bench.main(argv)
            except SystemExit as error:
                code = error.code
        return subprocess.CompletedProcess(argv, code, stdout.getvalue(), stderr.getvalue())

    def _write_omp(self, lines):
        self.payload = "".join(json.dumps(line) + "\n" for line in lines).encode()

    def _write_claude(self, body):
        self.payload = json.dumps(body).encode()

    def test_omp_reports_inline_usage_and_served_model(self):
        # omp streams JSON lines; each assistant message_end carries usage with
        # an inline cost and the model that answered. bench sums them and
        # records the served model. omp pins its model, so downgrade stays null.
        self._write_omp([
            {"type": "message_end", "message": {"role": "assistant",
             "model": "hf:moonshotai/Kimi-K3",
             "usage": {"input": 15, "output": 30, "cacheRead": 5, "reasoning": 10,
                       "cost": {"total": 0.05}}, "stopReason": "stop"}}])
        proc = self.run_bench(cli="omp", model="synthetic/hf:moonshotai/Kimi-K3")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertEqual(result["exit"], 0)
        self.assertEqual(result["usage"],
                         {"input": 15, "output": 30, "cache_read": 5, "reasoning": 10})
        self.assertEqual(result["cost_usd_reported"], 0.05)
        self.assertEqual(result["served_model"], ["hf:moonshotai/Kimi-K3"])
        self.assertIsNone(result["downgraded"])
        self.assertFalse(result["refused"])

    def test_omp_content_filter_is_a_refusal(self):
        # A safety stop reason on omp is a refusal a zero-usage exit-0 lane hides.
        self._write_omp([
            {"type": "message_end", "message": {"role": "assistant",
             "model": "hf:moonshotai/Kimi-K3", "usage": {"input": 5, "output": 0},
             "stopReason": "content_filter"}}])
        proc = self.run_bench(cli="omp", model="synthetic/hf:moonshotai/Kimi-K3")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertTrue(result["refused"])
        self.assertEqual(result["refuse_reason"], "stopReason=content_filter")

    def test_claude_downgrade_detected(self):
        # A cyber reroute answers under another model. modelUsage names it, so
        # served_model must carry it and downgraded must be true.
        self._write_claude({
            "stop_reason": "end_turn", "total_cost_usd": 0.4,
            "usage": {"input_tokens": 10, "output_tokens": 20},
            "modelUsage": {"claude-opus-4-8": {"canonicalModel": "claude-opus-4-8"}},
        })
        proc = self.run_bench(cli="claude", model="claude-fable-5-1")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertEqual(result["served_model"], ["claude-opus-4-8"])
        self.assertTrue(result["downgraded"])
        self.assertFalse(result["refused"])

    def test_claude_partial_downgrade_detected(self):
        # A cyber reroute answers some turns as another model while the
        # requested one answers the rest; the foreign id must flag a downgrade.
        self._write_claude({
            "stop_reason": "end_turn", "total_cost_usd": 6.0,
            "usage": {"input_tokens": 10, "output_tokens": 20},
            "modelUsage": {"claude-opus-5-5": {}, "claude-opus-4-8": {}},
        })
        proc = self.run_bench(cli="claude", model="claude-opus-5-5")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertTrue(result["downgraded"])
        self.assertEqual(result["served_foreign"], ["claude-opus-4-8"])
        self.assertFalse(result["refused"])

    def test_claude_dated_snapshot_not_a_downgrade(self):
        # A dated snapshot of the requested model is the same model.
        self._write_claude({
            "stop_reason": "end_turn", "total_cost_usd": 0.1,
            "usage": {"input_tokens": 10, "output_tokens": 20},
            "modelUsage": {"claude-sonnet-5-5-20260928": {}},
        })
        proc = self.run_bench(cli="claude", model="claude-sonnet-5-5")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertFalse(result["downgraded"])

    def test_claude_refusal_detected(self):
        self._write_claude({
            "stop_reason": "refusal", "total_cost_usd": 0.0,
            "usage": {"input_tokens": 5, "output_tokens": 0},
            "modelUsage": {"claude-fable-5-1": {}},
        })
        proc = self.run_bench(cli="claude", model="claude-fable-5-1")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertTrue(result["refused"])
        self.assertEqual(result["refuse_reason"], "stop_reason=refusal")

    def test_claude_permission_denial_is_recorded_not_refused(self):
        # A hook in the lane's worktree can deny a tool call, so a denial is
        # evidence to read, not a safety refusal.
        self._write_claude({
            "stop_reason": "end_turn", "total_cost_usd": 0.1,
            "usage": {"input_tokens": 10, "output_tokens": 20},
            "modelUsage": {"claude-opus-5-5": {}},
            "permission_denials": [{"tool_name": "Bash", "tool_use_id": "t1", "tool_input": {}}],
        })
        proc = self.run_bench(cli="claude", model="claude-opus-5-5")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertFalse(result["refused"])
        self.assertEqual(result["denied_tools"], ["Bash"])

    def test_claude_empty_permission_denials_is_no_refusal(self):
        self._write_claude({
            "stop_reason": "end_turn", "total_cost_usd": 0.1,
            "usage": {"input_tokens": 10, "output_tokens": 20},
            "modelUsage": {"claude-opus-5-5": {}},
            "permission_denials": [],
        })
        proc = self.run_bench(cli="claude", model="claude-opus-5-5")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertFalse(json.loads(self.out.read_text())["refused"])

    def test_agy_filter_refusal_detected(self):
        # Google's filter returns status SUCCESS, exit 0, zero usage, with the
        # refusal in response text. The exit code alone reports it as clean.
        self.payload = json.dumps({"status": "SUCCESS", "usage": {"input_tokens": 0,
                                  "output_tokens": 0}, "response": "The prompt could not be submitted. It contains sensitive words."}).encode()
        proc = self.run_bench(cli="agy", model="gemini-3.8-flash-high")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertTrue(result["refused"])
        self.assertIn("google filter", result["refuse_reason"])

    def test_launch_arguments_and_effort(self):
        expected = {
            "claude": ["claude", "-p", "calibrate lane", "--model", "m", "--effort", "high",
                       "--output-format", "json", "--dangerously-skip-permissions"],
            "codex": ["codex", "exec", "--json", "--skip-git-repo-check",
                      "--dangerously-bypass-approvals-and-sandbox", "-c",
                      'plugins."compound-engineering@compound-engineering-plugin".enabled=false',
                      "-m", "m", "-c", "model_reasoning_effort=high", "calibrate lane"],
            "agy": ["agy", "-p", "calibrate lane", "--model", "m", "--output-format", "json",
                    "--dangerously-skip-permissions", "--print-timeout", "60m"],
            "omp": ["omp", "-p", "--model", "m", "--thinking", "high", "--auto-approve",
                    "--mode", "json", "--no-session", "--no-title", "calibrate lane"],
        }
        for cli in expected:
            with self.subTest(cli=cli):
                self.assertEqual(self.run_bench(cli, "m", effort="" if cli == "agy" else "high").returncode, 0)
                self.assertEqual(self.calls[-1][0], expected[cli])
                self.assertEqual(self.calls[-1][1]["cwd"], self.worktree.resolve())
                self.assertEqual(self.calls[-1][1]["stdin"], subprocess.DEVNULL)
                self.assertNotIn("timeout", self.calls[-1][1])
        self.assertEqual(self.run_bench("agy", "m", effort="high").returncode, 2)

    def test_environment_removal(self):
        previous = os.environ.get("CLAUDECODE")
        self.run_bench("claude", env_extra={"CLAUDECODE": "nested", "CLAUDE_CODE_ENTRYPOINT": "x", "KEEP": "ok"})
        env = self.calls[-1][1]["env"]
        self.assertNotIn("CLAUDECODE", env)
        self.assertNotIn("CLAUDE_CODE_ENTRYPOINT", env)
        self.assertEqual(env["KEEP"], "ok")
        self.assertEqual(os.environ.get("CLAUDECODE"), previous)

    def test_multiple_messages_sum_costs(self):
        self._write_omp([{"type": "message_end", "message": {"role": role,
                         "usage": {"input": 10, "output": 20, "cost": {"total": cost}}}}
                        for role, cost in [("assistant", 0.25), ("user", 99), ("assistant", 0.5)]])
        self.assertEqual(self.run_bench().returncode, 0)
        result = json.loads(self.out.read_text())
        self.assertEqual(result["cost_usd_reported"], 0.75)
        self.assertEqual(result["usage"]["input"], 20)
        self.assertEqual(result["usage"]["output"], 40)

    def test_failed_child_records_evidence(self):
        self.code = 7
        self.error = b"failed\r\n"
        result = self.run_bench()
        self.assertEqual(result.returncode, 1)
        recorded = json.loads(self.out.read_text())
        self.assertEqual(recorded["exit"], 7)
        self.assertEqual(recorded["error"], "failed")
        self.assertIsNone(recorded["usage"])
        self.assertEqual(self.out.with_suffix(".raw.err").read_bytes(), self.error)
        self.assertEqual(json.loads(result.stdout), recorded)
        self.assertIn("failed", result.stderr)
        self.payload = b'{"usage":{"input_tokens":4}}'
        self.assertEqual(self.run_bench("claude").returncode, 1)
        self.assertEqual(json.loads(self.out.read_text())["usage"]["input"], 4)
        self.launch_error = OSError("cannot launch")
        self.assertEqual(self.run_bench().returncode, 1)
        recorded = json.loads(self.out.read_text())
        self.assertEqual(recorded["exit"], 127)
        self.assertEqual(recorded["error"], "cannot launch")
        self.assertIsNone(recorded["usage"])

    def test_stale_artifacts_are_cleared(self):
        self.expect_cleared = True
        self.out.write_text('{"stale": true}')
        self.out.with_suffix(".raw").write_text('{"usage": {"input_tokens": 900}}')
        self.out.with_suffix(".raw.err").write_text("old error")
        self.run_bench("claude")
        self.assertEqual(self.out.with_suffix(".raw").read_bytes(), b"")
        self.assertEqual(self.out.with_suffix(".raw.err").read_bytes(), b"")
        self.assertEqual(json.loads(self.out.read_text())["usage"]["input"], 0)

    def test_unusable_directory_stops_before_any_artifact_is_touched(self):
        # A --dir the command cannot enter must exit 2 with the earlier lane's
        # evidence intact and no child started.
        locked = self.root / "locked"
        locked.mkdir()
        locked.chmod(0)
        self.addCleanup(locked.chmod, 0o700)
        plain_file = self.root / "file"
        plain_file.write_text("")
        cases = [("missing", self.root / "missing"), ("file", plain_file)]
        if os.geteuid() != 0:
            cases.append(("locked", locked))
        for name, directory in cases:
            with self.subTest(directory=name):
                artifacts = [self.out, self.out.with_suffix(".raw"), self.out.with_suffix(".raw.err")]
                for artifact in artifacts:
                    artifact.write_text("earlier lane")
                self.worktree = directory
                result = self.run_bench()
                self.assertEqual(result.returncode, 2)
                self.assertIn("--dir", result.stderr)
                self.assertEqual(self.calls, [])
                self.assertEqual([artifact.read_text() for artifact in artifacts], ["earlier lane"] * 3)
                self.assertEqual(result.stdout, "")

    def test_relative_output_is_resolved_from_directory(self):
        self.out = Path("relative.json")
        self.assertEqual(self.run_bench().returncode, 0)
        recorded = self.worktree / "relative.json"
        self.assertTrue(recorded.exists(), "result belongs in the target directory")
        self.assertEqual(json.loads(recorded.read_text())["lane"], "l1")
        self.assertTrue((self.worktree / "relative.raw").exists())

    def test_carriage_returns_remain_in_raw_stream(self):
        self.payload = b'{"type":"turn.completed","usage":{"input_tokens":2}}\r' + \
                       b'{"type":"turn.completed","usage":{"input_tokens":3}}\r\n'
        self.assertEqual(self.run_bench("codex").returncode, 0)
        self.assertEqual(self.out.with_suffix(".raw").read_bytes(), self.payload)
        self.assertEqual(json.loads(self.out.read_text())["usage"]["input"], 5)


if __name__ == "__main__":
    unittest.main()
