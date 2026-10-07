"""Host reports retain shell fixture sections without live provider reads."""

from contextlib import redirect_stdout, redirect_stderr
from datetime import datetime, timezone
import io
import json
import subprocess
import unittest
from unittest.mock import patch

from skills.tune import discover_host


POOL_ROWS = ('claude: {"signed_in": false}\n'
             'codex: {"signed_in": false}\n'
             'google: {"signed_in": false}\n'
             'synthetic: {"signed_in": false}\n'
             'zai: {"signed_in": false}\n')
# A fixture report with five pools and two Synthetic model ids.
REPORT_TAIL = ('orca:\n  reachable: true\n  model_pinnable: [claude, codex, omp]\npools:\n'
               '  claude: {"signed_in": false}\n'
               '  codex: {"signed_in": false}\n'
               '  google: {"signed_in": false}\n'
               '  synthetic: {"signed_in": false}\n'
               '  zai: {"signed_in": false}\n'
               'omp_models:\n  synthetic: ["hf:a/b", "plain"]\n')


class DiscoverHostTest(unittest.TestCase):
    def report(self, tools=(), status=None, models=None, help_text=None, status_code=0):
        calls = []

        def run(argv, **kwargs):
            calls.append(argv)
            if argv[1:] == ["--version"]:
                body = b"one\r\ntwo\n"
            elif argv[1:] == ["status", "--json"]:
                body = (status or "").encode()
            elif argv[1:] == ["orchestration", "worker-start", "--help"]:
                body = (help_text or "").encode()
            elif argv[1:] == ["models", "synthetic", "--json"]:
                body = (models or "").encode()
            else:
                self.fail(f"unexpected argv: {argv}")
            self.assertEqual(kwargs["stdin"], subprocess.DEVNULL)
            self.assertTrue(kwargs["capture_output"])
            self.assertNotIn("text", kwargs)
            return subprocess.CompletedProcess(argv, status_code if argv[1] == "status" else 0, body, b"")

        def pools(argv):
            self.assertEqual(argv, [])
            print(POOL_ROWS, end="")
            return 0

        stdout = io.StringIO()
        with patch.object(discover_host.shutil, "which", side_effect=lambda name: "/bin/" + name if name in tools else None), \
                patch.object(discover_host.subprocess, "run", side_effect=run), \
                patch.object(discover_host.pool_usage, "main", side_effect=pools), redirect_stdout(stdout):
            self.assertEqual(discover_host.main([]), 0)
        return stdout.getvalue(), calls

    def test_shell_fixture_sections_and_ids(self):
        text, calls = self.report(tools=("claude", "codex", "agy", "omp", "orca"),
                                  status='{"result":{"runtime":{"reachable":true}}}',
                                  models='{"models":[{"id":"hf:a/b"},{"id":"plain"}]}',
                                  help_text="--model supports Claude, Codex and Omp opaque provider model ids.\n")
        self.assertTrue(text.endswith(REPORT_TAIL), text)
        self.assertIn('  claude: {path: /bin/claude, version: "one\\r"}', text)
        self.assertNotIn("two", text)
        self.assertEqual(len(calls), 7)
        pools = text.split("pools:\n")[1].split("omp_models:")[0]
        self.assertEqual(pools.count("\n"), 5)
        self.assertTrue(all(row.startswith("  ") for row in pools.split("\n") if row))

    def test_absent_clis(self):
        text, calls = self.report()
        self.assertIn("clis:\n  claude: null\n  codex: null\n  agy: null\n  omp: null\n", text)
        self.assertIn("orca:\n  reachable: false\n", text)
        self.assertTrue(text.endswith("omp_models: {synthetic: []}\n"))
        self.assertEqual(calls, [])

    def test_false_and_unreadable_orca(self):
        for status in ('{"result":{"runtime":{"reachable":false}}}', 'junk', '{}', 'null'):
            with self.subTest(status=status):
                text, calls = self.report(tools=("orca",), status=status)
                self.assertIn("orca:\n  reachable: false\n", text)
                self.assertEqual(calls, [["/bin/orca", "status", "--json"]])

        text, calls = self.report(tools=("orca",), status='{"result":{"runtime":{"reachable":true}}}', status_code=1)
        self.assertIn("orca:\n  reachable: false\n", text)
        self.assertEqual(calls, [["/bin/orca", "status", "--json"]])

    def test_malformed_model_lists(self):
        for models in ('junk', 'null', '[]', '{"models":null}', '{"models":{}}', '{"models":[null,{}]}'):
            with self.subTest(models=models):
                text, calls = self.report(tools=("omp",), models=models)
                self.assertTrue(text.endswith("omp_models:\n  synthetic: []\n"), text)
                self.assertEqual(calls[-1], ["/bin/omp", "models", "synthetic", "--json"])

    def test_utc_timestamp(self):
        with patch.object(discover_host, "datetime") as clock:
            clock.now.return_value = datetime(2026, 10, 7, 12, 34, 56, tzinfo=timezone.utc)
            text, _ = self.report()
            self.assertTrue(text.startswith("generated: 2026-10-07T12:34:56Z\n"))
            clock.now.assert_called_once_with(timezone.utc)

    def test_unreadable_executable(self):
        with patch.object(discover_host.subprocess, "run", side_effect=OSError("cannot launch")):
            self.assertEqual(discover_host.output(["missing", "--version"]), "")

    def test_raising_pool_reader_keeps_printed_rows(self):
        # pool_usage prints a row per pool as it goes and raises on a provider
        # reply of an unexpected shape, so the rows printed before the raise
        # belong in the report and the omp_models section still follows.
        def pools(argv):
            print('claude: {"signed_in": false}')
            print('codex: {"signed_in": false}')
            raise TypeError("limits is null")

        def run(argv, **kwargs):
            return subprocess.CompletedProcess(argv, 0, b'{"models":[{"id":"plain"}]}', b"")

        stdout, stderr = io.StringIO(), io.StringIO()
        with patch.object(discover_host.shutil, "which", side_effect=lambda name: "/bin/omp" if name == "omp" else None), \
                patch.object(discover_host.subprocess, "run", side_effect=run), \
                patch.object(discover_host.pool_usage, "main", side_effect=pools), \
                redirect_stdout(stdout), redirect_stderr(stderr):
            self.assertEqual(discover_host.main([]), 1)
        self.assertTrue(stdout.getvalue().endswith(
            'pools:\n  claude: {"signed_in": false}\n  codex: {"signed_in": false}\n'
            'omp_models:\n  synthetic: ["plain"]\n'), stdout.getvalue())
        self.assertIn("discover-host: pool usage failed", stderr.getvalue())
        self.assertIn("TypeError: limits is null", stderr.getvalue())
