"""Regression tests for bench.sh calibration runner."""

from pathlib import Path
import json
import os
import subprocess
import tempfile
import unittest

BENCH_SH = Path(__file__).resolve().parent / "bench.sh"


class BenchTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.bin_dir = self.root / "bin"
        self.bin_dir.mkdir()
        self.worktree = self.root / "worktree"
        self.worktree.mkdir()
        self.brief = self.root / "brief.md"
        self.brief.write_text("calibrate lane")
        self.out = self.root / "out.json"

    def run_bench(self, cli="omp", model="synthetic/hf:test", env_extra=None):
        env = os.environ.copy()
        env["PATH"] = str(self.bin_dir) + ":" + env.get("PATH", "")
        if env_extra:
            env.update(env_extra)
        return subprocess.run(
            [
                str(BENCH_SH),
                "--lane", "l1",
                "--cli", cli,
                "--model", model,
                "--brief", str(self.brief),
                "--dir", str(self.worktree),
                "--out", str(self.out),
            ],
            env=env,
            capture_output=True,
            text=True,
        )

    def _write_omp(self, lines):
        omp = self.bin_dir / "omp"
        omp.write_text(
            "#!/usr/bin/env python3\n"
            "import sys\n"
            "sys.stdout.write(%r)\n" % "".join(json.dumps(line) + "\n" for line in lines)
        )
        omp.chmod(0o755)

    def test_omp_reports_inline_usage_and_served_model(self):
        # omp streams JSON lines; each assistant message_end carries usage with
        # an inline cost and the model that answered. bench.sh sums them and
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

    def _write_claude(self, body):
        claude = self.bin_dir / "claude"
        claude.write_text(
            "#!/usr/bin/env python3\n"
            "import sys\n"
            "sys.stdout.write(%r)\n" % json.dumps(body)
        )
        claude.chmod(0o755)

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

    def test_agy_filter_refusal_detected(self):
        # Google's filter returns status SUCCESS, exit 0, zero usage, with the
        # refusal in response text. The exit code alone reports it as clean.
        agy = self.bin_dir / "agy"
        agy.write_text(
            "#!/usr/bin/env python3\n"
            "import json, sys\n"
            "sys.stdout.write(json.dumps({'status': 'SUCCESS', "
            "'usage': {'input_tokens': 0, 'output_tokens': 0}, "
            "'response': 'The prompt could not be submitted. It contains sensitive words.'}))\n"
        )
        agy.chmod(0o755)
        proc = self.run_bench(cli="agy", model="gemini-3.8-flash-high")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(self.out.read_text())
        self.assertTrue(result["refused"])
        self.assertIn("google filter", result["refuse_reason"])


if __name__ == "__main__":
    unittest.main()
