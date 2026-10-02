import ast
import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("pool-usage.sh")
source = SCRIPT.read_text().split("python3 - <<'PY'\n", 1)[1].rsplit("\nPY", 1)[0]
tree = ast.parse(source)
tree.body = [node for node in tree.body if not isinstance(node, ast.Expr)]
POOL = {}
exec(compile(tree, str(SCRIPT), "exec"), POOL)


class PoolUsageTest(unittest.TestCase):
    def row(self, function):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            POOL[function]()
        return {pool: json.loads(row) for pool, row in
                (line.split(": ", 1) for line in output.getvalue().splitlines())}

    def test_native_fallback_without_orca(self):
        with mock.patch.object(POOL["shutil"], "which", return_value=None), \
                mock.patch.dict(POOL, {
                    "claude_pool": lambda: POOL["emit"]("claude", True, "claude-cli", {"session": 100}),
                    "codex_pool": lambda: POOL["emit"]("codex", True, "codex-app-server", {"weekly": 54}),
                }):
            rows = self.row("orca_pools")
        self.assertEqual(rows["claude"]["windows"], {"session": 100})
        self.assertEqual(rows["codex"]["windows"], {"weekly": 54})

    def test_failed_or_unreadable_orca_falls_back_for_both_pools(self):
        for reply in [(None, "unreachable"), ("not json", None), ('{"result": []}', None)]:
            with self.subTest(reply=reply), \
                    mock.patch.object(POOL["shutil"], "which", return_value="orca"), \
                    mock.patch.dict(POOL, {"run": mock.Mock(return_value=reply),
                                          "claude_pool": mock.Mock(), "codex_pool": mock.Mock()}):
                POOL["orca_pools"]()
                POOL["claude_pool"].assert_called_once()
                POOL["codex_pool"].assert_called_once()

    def test_orca_windows_are_authoritative_and_missing_pool_falls_back(self):
        reply = {"result": {"rateLimits": {
            "claude": {"status": "ok", "session": {"usedPercent": 0, "resetsAt": 1790945399927}},
            "codex": {"status": "unavailable"},
        }}}
        with mock.patch.object(POOL["shutil"], "which", return_value="orca"), \
                mock.patch.dict(POOL, {"run": mock.Mock(return_value=(json.dumps(reply), None)),
                                      "claude_pool": mock.Mock(), "codex_pool": mock.Mock()}):
            rows = self.row("orca_pools")
            POOL["claude_pool"].assert_not_called()
            POOL["codex_pool"].assert_called_once()
        self.assertEqual(rows["claude"]["windows"], {"session": 0})
        self.assertEqual(rows["claude"]["source"], "orca")

    def test_claude_reads_structured_usage_without_inference(self):
        # Shape from Claude Code 2.1.287's /usage stream output.
        report = {"type": "assistant", "usage_report": {"rate_limits": {"limits": [
            {"kind": "session", "percent": 100, "resets_at": "2026-10-02T12:49:59+00:00"},
            {"kind": "weekly_all", "percent": 26},
            {"kind": "weekly_scoped", "percent": 95, "scope": {"model": {"display_name": "Fable"}}},
        ]}}}
        reply = '\n'.join(json.dumps(m) for m in [{"type": "system"}, report,
                            {"type": "result", "local_command": "usage", "num_turns": 0}])
        runner = mock.Mock(side_effect=[(json.dumps({"loggedIn": True, "authMethod": "claude.ai"}), None),
                                      (reply, None)])
        with mock.patch.object(POOL["shutil"], "which", return_value="claude"), \
                mock.patch.dict(POOL, {"run": runner}):
            row = self.row("claude_pool")["claude"]
        self.assertEqual(row["windows"], {"session": 100, "weekly": 26, "fableWeekly": 95})
        self.assertEqual(row["worst"]["resets"], "2026-10-02T12:49:59+00:00")
        usage_cmd = runner.call_args_list[1].args[0]
        self.assertEqual(usage_cmd[1:3], ["-p", "/usage"])
        self.assertIn("--no-session-persistence", usage_cmd)

    def test_claude_auth_failure_and_quota_failure_are_distinct(self):
        cases = [
            ([(json.dumps({"loggedIn": False, "authMethod": "none"}), None)], False),
            ([(json.dumps({"loggedIn": True, "authMethod": "api_key"}), None)], False),
            ([(None, "failed")], None),
            ([(json.dumps({"loggedIn": True, "authMethod": "oauth_token"}), None), (None, "timeout")], True),
            ([(json.dumps({"loggedIn": True, "authMethod": "claude.ai"}), None), (None, "timeout")], True),
            ([(json.dumps({"loggedIn": True, "authMethod": "claude.ai"}), None), ('{"type":"result"}', None)], True),
        ]
        for replies, signed_in in cases:
            with self.subTest(signed_in=signed_in), \
                    mock.patch.object(POOL["shutil"], "which", return_value="claude"), \
                    mock.patch.dict(POOL, {"run": mock.Mock(side_effect=replies)}):
                row = self.row("claude_pool")["claude"]
            self.assertIs(row["signed_in"], signed_in)
            self.assertIsNone(row["windows"])
            self.assertIn("error", row)

    def codex_row(self, account, quota=None, quota_error=False):
        with tempfile.TemporaryDirectory() as directory:
            cli = Path(directory) / "codex"
            cli.write_text("#!/usr/bin/env python3\n" + f"account={account!r}\nquota={quota!r}\nquota_error={quota_error!r}\n" + '''
import json, sys
initialized = False
for line in sys.stdin:
    msg = json.loads(line)
    method = msg['method']
    if method == 'initialized':
        initialized = True
        continue
    if method == 'initialize':
        result = {}
    elif method == 'account/read':
        assert initialized
        result = {'account': account}
    elif method == 'account/rateLimits/read':
        if quota_error:
            print(json.dumps({'id': msg['id'], 'error': {'message': 'sentinel-private-error'}}), flush=True)
            continue
        result = quota
    print(json.dumps({'method': 'unrelated/notification'}), flush=True)
    # Multiple messages in one write exercises the buffered stdio reader.
    print(json.dumps({'id': msg['id'], 'result': result}), flush=True)
''')
            cli.chmod(0o755)
            with mock.patch.dict(os.environ, {"PATH": directory + os.pathsep + os.environ["PATH"]}):
                return self.row("codex_pool")["codex"]

    def test_codex_primary_weekly_uses_duration_and_multibucket_view(self):
        # Shape from Codex CLI 0.142.3 account/rateLimits/read.
        quota = {"rateLimits": {"primary": {"usedPercent": 1}}, "rateLimitsByLimitId": {
            "codex": {"primary": {"usedPercent": 54, "windowDurationMins": 10080,
                                   "resetsAt": 1791303799}, "secondary": None},
        }}
        row = self.codex_row({"type": "chatgpt"}, quota)
        self.assertEqual(row["windows"], {"weekly": 54})
        self.assertEqual(row["worst"]["resets"], "2026-10-06T16:23:19Z")

    def test_codex_legacy_windows_and_zero_usage(self):
        row = self.codex_row({"type": "chatgpt"}, {"rateLimits": {
            "primary": {"usedPercent": 0, "windowDurationMins": 300},
            "secondary": {"usedPercent": 85, "windowDurationMins": 10080},
        }})
        self.assertEqual(row["windows"], {"session": 0, "weekly": 85})

    def test_codex_signed_out_or_api_key_is_not_a_prepaid_pool(self):
        for account in [None, {"type": "apiKey"}]:
            with self.subTest(account=account):
                row = self.codex_row(account)
                self.assertIs(row["signed_in"], False)

    def test_codex_quota_failure_preserves_known_sign_in_and_redacts_error(self):
        row = self.codex_row({"type": "chatgpt"}, quota_error=True)
        self.assertIs(row["signed_in"], True)
        self.assertIsNone(row["windows"])
        self.assertNotIn("sentinel-private-error", json.dumps(row))


if __name__ == "__main__":
    unittest.main()
