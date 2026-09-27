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

    def run_bench(self, cli="opencode", model="test/model", env_extra=None):
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

    def test_failed_final_message_preserves_steps(self):
        # When the final message request fails after steps were executed,
        # opencode writes step-finish parts to $raw.steps. bench.sh must sum
        # the steps' tokens and cost rather than discarding them as null.
        opencode = self.bin_dir / "opencode"
        opencode.write_text(
            "#!/usr/bin/env python3\n"
            "import http.server, json, sys\n"
            "\n"
            "class Handler(http.server.BaseHTTPRequestHandler):\n"
            "    def log_message(self, *args): pass\n"
            "    def do_POST(self):\n"
            "        if self.path == '/session':\n"
            "            self.send_response(200)\n"
            "            self.send_header('Content-Type', 'application/json')\n"
            "            self.end_headers()\n"
            "            self.wfile.write(b'{\"id\": \"s1\"}')\n"
            "        elif '/message' in self.path:\n"
            "            self.close_connection = True\n"
            "    def do_GET(self):\n"
            "        self.send_response(200)\n"
            "        self.send_header('Content-Type', 'application/json')\n"
            "        self.end_headers()\n"
            "        if '/message' in self.path:\n"
            "            steps = [{'parts': [{'type': 'step-finish', 'tokens': "
            "                     {'input': 15, 'output': 30, 'reasoning': 10, "
            "                      'cache': {'read': 5}}, 'cost': 0.05}]}]\n"
            "            self.wfile.write(json.dumps(steps).encode('utf-8'))\n"
            "        else:\n"
            "            self.wfile.write(b'[]')\n"
            "\n"
            "port = int(sys.argv[sys.argv.index('--port') + 1])\n"
            "http.server.HTTPServer(('127.0.0.1', port), Handler).serve_forever()\n"
        )
        opencode.chmod(0o755)

        proc = self.run_bench()
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertTrue(self.out.exists(), "out.json was not created")
        result = json.loads(self.out.read_text())

        self.assertNotEqual(result["exit"], 0)
        self.assertEqual(
            result["usage"],
            {"input": 15, "output": 30, "cache_read": 5, "reasoning": 10},
        )
        self.assertEqual(result["cost_usd_reported"], 0.05)
        self.assertIn("curl: (52)", result.get("error", ""))

    def test_never_started_lane(self):
        # When the server never binds or exits immediately, bench.sh must report
        # null usage and record the startup failure in error.
        opencode = self.bin_dir / "opencode"
        opencode.write_text("#!/bin/sh\nexit 1\n")
        opencode.chmod(0o755)

        proc = self.run_bench()
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertTrue(self.out.exists(), "out.json was not created")
        result = json.loads(self.out.read_text())

        self.assertEqual(result["exit"], 1)
        self.assertIsNone(result["usage"])
        self.assertIsNone(result["cost_usd_reported"])
        self.assertIn("never accepted a session", result.get("error", ""))


if __name__ == "__main__":
    unittest.main()
