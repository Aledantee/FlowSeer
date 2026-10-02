import json
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

POOL_USAGE = Path(__file__).with_name("pool-usage.sh")


class OrcaMissingTest(unittest.TestCase):
    """Without Orca, the claude row reads the CLI's own token."""

    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.home = Path(self.directory.name) / "home"
        self.home.mkdir()
        # A PATH holding python3 alone: no orca, agy, or omp, so every
        # source fails locally and nothing reaches the network.
        self.bin = Path(self.directory.name) / "bin"
        self.bin.mkdir()
        (self.bin / "python3").symlink_to(sys.executable)

    def rows(self):
        # TMPDIR is kept: bash writes the here document there.
        env = {"HOME": str(self.home), "PATH": str(self.bin), "TMPDIR": tempfile.gettempdir()}
        result = subprocess.run(
            [shutil.which("bash"), str(POOL_USAGE)], capture_output=True, text=True, env=env, check=False
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        rows = {}
        for line in result.stdout.splitlines():
            pool, _, row = line.partition(": ")
            rows[pool] = json.loads(row)
        return rows

    def test_claude_token_marks_the_pool_signed_in(self):
        (self.home / ".claude").mkdir()
        expires = int((time.time() + 3600) * 1000)
        (self.home / ".claude/.credentials.json").write_text(
            json.dumps({"claudeAiOauth": {"expiresAt": expires}})
        )
        rows = self.rows()
        self.assertIs(rows["claude"]["signed_in"], True)
        self.assertEqual(rows["claude"]["error"], "orca not installed")
        self.assertIsNone(rows["codex"]["signed_in"])

    def test_no_claude_token_leaves_the_state_unknown(self):
        rows = self.rows()
        self.assertIsNone(rows["claude"]["signed_in"])
        self.assertEqual(rows["claude"]["error"], "orca not installed")

    def test_unreadable_account_list_reads_the_token_too(self):
        (self.bin / "orca").write_text("#!/bin/sh\necho 'not json'\n")
        (self.bin / "orca").chmod(0o755)
        (self.home / ".claude").mkdir()
        expires = int((time.time() + 3600) * 1000)
        (self.home / ".claude/.credentials.json").write_text(
            json.dumps({"claudeAiOauth": {"refreshTokenExpiresAt": expires}})
        )
        rows = self.rows()
        self.assertIs(rows["claude"]["signed_in"], True)
        self.assertEqual(rows["claude"]["error"], "unreadable account list")


if __name__ == "__main__":
    unittest.main()
