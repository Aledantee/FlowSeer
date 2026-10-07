import contextlib
import importlib
import io
import json
import os
import socket
import subprocess
import sys
import time
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import run as dispatcher
from skills.delegate import pool_usage


POOL = vars(pool_usage)


class PoolUsageTest(unittest.TestCase):
    def test_import_has_no_output_or_process(self):
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors), \
                mock.patch.object(subprocess, "Popen", side_effect=AssertionError("process on import")), \
                mock.patch.object(subprocess, "run", side_effect=AssertionError("command on import")), \
                mock.patch.object(pool_usage.threading.Thread, "start",
                                  side_effect=AssertionError("thread on import")), \
                mock.patch.object(pool_usage.urllib.request, "urlopen",
                                  side_effect=AssertionError("network on import")):
            importlib.reload(pool_usage)
        self.assertEqual((output.getvalue(), errors.getvalue()), ("", ""))

    def pipe_read(self, mode, *, timeout=1, fragments=False):
        with tempfile.TemporaryDirectory() as directory:
            fixture = Path(directory) / "peer.py"
            fixture.write_text('''
import json, os, sys
mode = sys.argv[1]
os.write(1, b'{"method":"retained/notification"}\\n')
for line in sys.stdin.buffer:
    message = json.loads(line)
    method = message['method']
    if method == 'initialized':
        continue
    if mode == 'eof':
        sys.exit(0)
    if mode == 'timeout':
        continue
    result = {
        'initialize': {},
        'account/read': {'account': {'type': 'chatgpt', 'planType': 'prolite'}},
        'model/list': {'data': [{'id': 'gpt-6-sol'}], 'nextCursor': None},
        'account/rateLimits/read': {'rateLimits': {'primary': {'usedPercent': 25}}},
    }[method]
    wire = (json.dumps({'method': 'unrelated/notification'}) + '\\n' +
            json.dumps({'id': -1, 'result': {}}) + '\\n' +
            json.dumps({'id': message['id'], 'result': result}) + '\\n').encode()
    os.write(1, wire)
''', encoding="utf-8")
            processes = []
            popen = subprocess.Popen

            class FragmentedPipe:
                def __init__(self, stream):
                    self.stream = stream

                def read(self, size):
                    return self.stream.read(min(size, 3))

                def close(self):
                    self.stream.close()

            def launch(argv, **kwargs):
                process = popen([sys.executable, str(fixture), mode], **kwargs)
                processes.append(process)
                if fragments:
                    process.stdout = FragmentedPipe(process.stdout)
                return process

            with mock.patch.object(pool_usage.shutil, "which", return_value=sys.executable), \
                    mock.patch.object(subprocess, "Popen", side_effect=launch):
                started = time.monotonic()
                info = pool_usage.codex_read(timeout=timeout)
                elapsed = time.monotonic() - started
            process = processes[0]
            self.assertIsNotNone(process.returncode)
            self.assertTrue(process.stdin.closed)
            stream = process.stdout.stream if fragments else process.stdout
            self.assertTrue(stream.closed)
            return info, elapsed

    def test_rpc_fragmented_lines_keep_request_ids(self):
        info, _ = self.pipe_read("reply", fragments=True)
        self.assertEqual(info, {
            "signed_in": True, "plan": "prolite", "models": ["gpt-6-sol"],
            "windows": {"primary": 25}, "resets": {}, "error": None,
        })

    def test_rpc_batched_lines_ignore_notifications_and_unrelated_ids(self):
        info, _ = self.pipe_read("reply")
        self.assertEqual(info["windows"], {"primary": 25})
        self.assertEqual(info["models"], ["gpt-6-sol"])
        self.assertIsNone(info["error"])

    def test_rpc_eof_reports_failure_and_reaps_server(self):
        info, elapsed = self.pipe_read("eof")
        self.assertEqual(info["error"], "unreadable codex app-server quota")
        self.assertIsNone(info["signed_in"])
        self.assertLess(elapsed, 2)

    def test_rpc_timeout_reports_failure_and_reaps_server(self):
        info, elapsed = self.pipe_read("timeout", timeout=0.1)
        self.assertEqual(info["error"], "unreadable codex app-server quota")
        self.assertIsNone(info["signed_in"])
        self.assertLess(elapsed, 2)

    def test_cleanup_kills_and_reaps_after_terminate_timeout(self):
        process = mock.Mock(stdin=io.BytesIO(), stdout=io.BytesIO())
        process.wait.side_effect = [subprocess.TimeoutExpired("fixture", 5), 0]
        with mock.patch.object(pool_usage.shutil, "which", return_value="fixture"), \
                mock.patch.object(subprocess, "Popen", return_value=process):
            info = pool_usage.codex_read()
        self.assertEqual(info["error"], "unreadable codex app-server quota")
        self.assertEqual(process.mock_calls, [
            mock.call.terminate(), mock.call.wait(timeout=5), mock.call.kill(), mock.call.wait(),
        ])
        self.assertTrue(process.stdin.closed)
        self.assertTrue(process.stdout.closed)

    def test_cleanup_bounds_join_and_leaves_stdout_with_reader(self):
        process = mock.Mock(stdin=io.BytesIO(), stdout=mock.Mock())
        chunks = mock.Mock()
        chunks.get.return_value = b""
        with mock.patch.object(pool_usage.shutil, "which", return_value="fixture"), \
                mock.patch.object(subprocess, "Popen", return_value=process), \
                mock.patch.object(pool_usage.queue, "Queue", return_value=chunks), \
                mock.patch.object(pool_usage.threading, "Thread") as thread:
            info = pool_usage.codex_read()
        self.assertEqual(info["error"], "unreadable codex app-server quota")
        self.assertTrue(thread.call_args.kwargs["daemon"])
        thread.return_value.start.assert_called_once_with()
        thread.return_value.join.assert_called_once_with(timeout=5)
        process.stdout.close.assert_not_called()
        process.terminate.assert_called_once_with()
        process.wait.assert_called_once_with(timeout=5)
        process.kill.assert_not_called()
        self.assertTrue(process.stdin.closed)

    def test_descendant_stdout_does_not_hold_command_exit(self):
        # The control socket holds the descendant alive past the five-second join.
        with socket.socket() as control, tempfile.TemporaryDirectory() as directory:
            control.bind(("127.0.0.1", 0))
            control.listen()
            control.settimeout(8)
            peer = Path(directory) / "peer.py"
            peer.write_text('''
import json, subprocess, sys
descendant = "import socket,sys; s=socket.create_connection(('127.0.0.1',int(sys.argv[1]))); s.sendall(b'ready'); s.recv(1)"
subprocess.Popen([sys.executable, '-c', descendant, sys.argv[1]], stdin=subprocess.DEVNULL)
for line in sys.stdin:
    msg = json.loads(line)
    if msg['method'] == 'initialize':
        print(json.dumps({'id': msg['id'], 'result': {}}), flush=True)
    elif msg['method'] == 'account/read':
        print(json.dumps({'id': msg['id'], 'result': {'account': None}}), flush=True)
''', encoding="utf-8")
            driver = '''
import json, subprocess, sys
from unittest import mock
from skills.delegate import pool_usage
popen = subprocess.Popen
with mock.patch.object(pool_usage.shutil, 'which', return_value=sys.executable), mock.patch.object(subprocess, 'Popen', side_effect=lambda argv, **kwargs: popen([sys.executable, sys.argv[1], sys.argv[2]], **kwargs)):
    print(json.dumps(pool_usage.codex_read(timeout=1)), flush=True)
'''
            env = {**os.environ, "PYTHONPATH": str(Path(pool_usage.__file__).resolve().parents[2])}
            process = subprocess.Popen(
                [sys.executable, "-B", "-c", driver, str(peer), str(control.getsockname()[1])],
                stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env,
            )
            connection = None
            try:
                connection, _ = control.accept()
                connection.settimeout(8)
                self.assertEqual(connection.recv(5), b"ready")
                try:
                    stdout, stderr = process.communicate(timeout=8)
                except subprocess.TimeoutExpired:
                    self.fail("command exit waited for descendant-held stdout")
                self.assertEqual(process.returncode, 0, stderr)
                self.assertEqual(json.loads(stdout)["error"], "no ChatGPT subscription sign-in")
                self.assertIs(json.loads(stdout)["signed_in"], False)
            finally:
                if connection is not None:
                    connection.sendall(b"x")
                    self.assertEqual(connection.recv(1), b"")
                    connection.close()
                if process.poll() is None:
                    process.kill()
                process.communicate(timeout=8)

    def row(self, function):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            POOL[function]()
        return {pool: json.loads(row) for pool, row in
                (line.split(": ", 1) for line in output.getvalue().splitlines())}

    def test_full_rows_match_fixed_replies_and_registry(self):
        replies = [
            (json.dumps({"result": {"rateLimits": {
                "claude": {"status": "ok", "session": {"usedPercent": 0}},
                "codex": {"status": "ok", "weekly": {"usedPercent": 85}},
            }}}), None),
            (json.dumps({"loggedIn": True, "subscriptionType": "max"}), None),
            (json.dumps({"command": {"data": {"groups": [{"buckets": [
                {"id": "gemini-5h", "remaining_fraction": 0.5},
            ]}]}}}), None),
            (json.dumps({"reports": [{"provider": "zai", "metadata": {"planType": "lite"},
                                     "limits": []}]}), None),
        ]
        info = {"signed_in": True, "plan": "prolite", "models": ["gpt-6-sol"], "error": None}
        output = io.StringIO()
        with mock.patch.object(POOL["shutil"], "which", return_value="fixture"), \
                mock.patch.dict(POOL, {
                    "run": mock.Mock(side_effect=replies),
                    "codex_read": mock.Mock(return_value=info),
                    "synthetic_key": lambda: None,
                    "registry_pools": lambda: {
                        "claude": {"plans": {"max": {"capacity": {"session": 20}}}},
                        "codex": {"plans": {"prolite": {"capacity": 5}}},
                    },
                }), contextlib.redirect_stdout(output):
            self.assertEqual(dispatcher.main(["delegate", "pool-usage"]), 0)
        expected = (
            'claude: {"signed_in": true, "windows": {"session": 0}, "plan": "max", "capacity": {"session": 20}, "worst": {"window": "session", "used": 0}, "slots": {"session": 6}, "source": "orca"}\n'
            'codex: {"signed_in": true, "windows": {"weekly": 85}, "plan": "prolite", "capacity": 5, "models": ["gpt-6-sol"], "worst": {"window": "weekly", "used": 85}, "slots": {"weekly": 0}, "source": "orca"}\n'
            'google: {"signed_in": true, "windows": {"gemini-5h": 50}, "plan": null, "capacity": 1, "worst": {"window": "gemini-5h", "used": 50}, "slots": {"gemini-5h": 1}, "source": "agy"}\n'
            'synthetic: {"signed_in": false, "windows": null, "plan": null, "capacity": 1, "slots": null, "source": "synthetic-api", "error": "no key in SYNTHETIC_API_KEY or opencode auth.json"}\n'
            'zai: {"signed_in": true, "windows": null, "plan": "lite", "capacity": 1, "plan_unlisted": true, "slots": null, "source": "omp"}\n'
        )
        self.assertEqual(output.getvalue().encode(), expected.encode())

    def test_native_fallback_without_orca(self):
        with mock.patch.object(POOL["shutil"], "which", return_value=None), \
                mock.patch.dict(POOL, {
                    "claude_pool": lambda: POOL["emit"]("claude", True, "claude-cli", {"session": 100}),
                    "codex_pool": lambda: POOL["emit"]("codex", True, "codex-app-server", {"weekly": 54}),
                }):
            rows = self.row("orca_pools")
        self.assertEqual(rows["claude"]["windows"], {"session": 100})
        self.assertEqual(rows["codex"]["windows"], {"weekly": 54})

    def test_claude_plan_is_read_when_orca_supplies_windows(self):
        reply = {"result": {"rateLimits": {
            "claude": {"status": "ok", "session": {"usedPercent": 0}},
        }}}
        auth = {"loggedIn": True, "authMethod": "claude.ai",
                "subscriptionType": "max"}
        runner = mock.Mock(side_effect=[(json.dumps(reply), None),
                                        (json.dumps(auth), None)])
        with mock.patch.object(POOL["shutil"], "which",
                               side_effect=lambda name: name in ("orca", "claude")), \
                mock.patch.dict(POOL, {"run": runner, "codex_pool": mock.Mock()}):
            row = self.row("orca_pools")["claude"]
        self.assertEqual(row["plan"], "max")
        self.assertEqual(row["source"], "orca")
        self.assertNotIn("error", row)

    def test_claude_orca_row_reports_unreadable_plan_source(self):
        reply = {"result": {"rateLimits": {
            "claude": {"status": "ok", "session": {"usedPercent": 0}},
        }}}
        runner = mock.Mock(side_effect=[(json.dumps(reply), None), (None, "timeout")])
        with mock.patch.object(POOL["shutil"], "which",
                               side_effect=lambda name: name in ("orca", "claude")), \
                mock.patch.dict(POOL, {"run": runner, "codex_pool": mock.Mock()}):
            row = self.row("orca_pools")["claude"]
        self.assertTrue(row["signed_in"])
        self.assertEqual(row["windows"], {"session": 0})
        self.assertIsNone(row["plan"])
        self.assertEqual(row["source"], "orca")
        self.assertEqual(row["error"], "unreadable claude auth status")

    def test_codex_orca_row_reports_failed_account_read_without_quota(self):
        reply = {"result": {"rateLimits": {
            "codex": {"status": "ok", "session": {"usedPercent": 0}},
        }}}
        info = {"signed_in": None, "plan": None, "models": None,
                "error": "unreadable codex app-server account"}
        with mock.patch.object(POOL["shutil"], "which", return_value="orca"), \
                mock.patch.dict(POOL, {
                    "run": mock.Mock(return_value=(json.dumps(reply), None)),
                    "claude_pool": mock.Mock(),
                    "codex_read": mock.Mock(return_value=info),
                }):
            row = self.row("orca_pools")["codex"]
        self.assertTrue(row["signed_in"])
        self.assertEqual(row["windows"], {"session": 0})
        self.assertEqual(row["error"], "unreadable codex app-server account")
        self.assertNotIn("quota", row["error"])

    def test_codex_orca_row_keeps_plan_and_models(self):
        reply = {"result": {"rateLimits": {
            "codex": {"status": "ok", "session": {"usedPercent": 0}},
        }}}
        info = {"signed_in": True, "plan": "prolite", "models": ["gpt-6-sol"],
                "error": None}
        with mock.patch.object(POOL["shutil"], "which", return_value="orca"), \
                mock.patch.dict(POOL, {
                    "run": mock.Mock(return_value=(json.dumps(reply), None)),
                    "claude_pool": mock.Mock(),
                    "codex_read": mock.Mock(return_value=info),
                }):
            row = self.row("orca_pools")["codex"]
        self.assertEqual(row["plan"], "prolite")
        self.assertEqual(row["models"], ["gpt-6-sol"])
        self.assertEqual(row["source"], "orca")
        self.assertNotIn("error", row)

    def test_codex_orca_row_without_chatgpt_account_has_no_error(self):
        for account in (None, {"type": "apiKey"}):
            with self.subTest(account=account):
                row = self.codex_row(account, orca_windows={"session": 0})
            self.assertTrue(row["signed_in"])
            self.assertEqual(row["windows"], {"session": 0})
            self.assertEqual(row["source"], "orca")
            self.assertIsNone(row["plan"])
            self.assertNotIn("models", row)
            self.assertNotIn("error", row)

    def test_orca_skips_non_numeric_windows_without_an_error(self):
        reply = {"result": {"rateLimits": {
            "claude": {"status": "ok", "session": {"usedPercent": 0},
                       "weekly": {"usedPercent": "unknown", "resetsAt": "unknown"}},
        }}}
        auth = {"loggedIn": True, "authMethod": "claude.ai", "subscriptionType": "max"}
        with mock.patch.object(POOL["shutil"], "which", return_value="orca"), \
                mock.patch.dict(POOL, {
                    "run": mock.Mock(side_effect=[(json.dumps(reply), None),
                                                  (json.dumps(auth), None)]),
                    "codex_pool": mock.Mock(),
                }):
            row = self.row("orca_pools")["claude"]
        self.assertEqual(row["windows"], {"session": 0})
        self.assertEqual(row["plan"], "max")
        self.assertNotIn("error", row)

    def test_zai_plan_is_read_from_usage_metadata(self):
        usage = {"reports": [{"provider": "zai", "metadata": {"planType": "lite"},
                               "limits": [{"window": {"id": "5h"},
                                            "amount": {"usedFraction": 0.1}}]}]}
        with mock.patch.object(POOL["shutil"], "which", return_value="omp"), \
                mock.patch.dict(POOL, {"run": mock.Mock(return_value=(json.dumps(usage), None))}):
            row = self.row("zai_pool")["zai"]
        self.assertEqual(row["plan"], "lite")

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

    def codex_row(self, account, quota=None, quota_error=False, model_pages=None,
                  timeout=30, requests_path=None, read_quota=True, orca_windows=None):
        with tempfile.TemporaryDirectory() as directory:
            cli = Path(directory) / "codex.py"
            cli.write_text(
                           f"account={account!r}\nquota={quota!r}\n"
                           f"quota_error={quota_error!r}\nmodel_pages={model_pages!r}\n"
                           f"requests_path={requests_path!r}\n" + '''
import json, sys
initialized = False
model_page = 0
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
        result = {} if account == 'malformed' else {'account': account}
    elif method == 'model/list':
        if requests_path:
            with open(requests_path, 'a') as seen:
                seen.write(json.dumps(msg) + '\\n')
        if model_pages == 'timeout':
            continue
        if model_pages == 'exit':
            sys.exit(0)
        if model_pages == 'repeat':
            if model_page >= 2:
                # Bound the reply rate after a repeated cursor if the client keeps requesting.
                __import__('time').sleep(1)
            result = {'data': [{'id': 'gpt-6-sol'}], 'nextCursor': 'same'}
            model_page += 1
        elif model_pages is None:
            print(json.dumps({'id': msg['id'], 'error': {'message': 'unknown method'}}),
                  flush=True)
            continue
        else:
            result = model_pages[model_page]
            model_page += 1
    elif method == 'account/rateLimits/read':
        if requests_path:
            with open(requests_path, 'a') as seen:
                seen.write(json.dumps(msg) + '\\n')
        if quota_error:
            print(json.dumps({'id': msg['id'], 'error': {'message': 'sentinel-private-error'}}), flush=True)
            continue
        result = quota
    else:
        print(json.dumps({'id': msg['id'], 'error': {'message': 'unknown method'}}), flush=True)
        continue
    print(json.dumps({'method': 'unrelated/notification'}), flush=True)
    print(json.dumps({'id': msg['id'], 'result': result}), flush=True)
''')
            popen = subprocess.Popen
            with mock.patch.object(pool_usage.shutil, "which", return_value=sys.executable), \
                    mock.patch.object(pool_usage.subprocess, "Popen",
                                      side_effect=lambda argv, **kwargs:
                                      popen([sys.executable, str(cli)], **kwargs)):
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    if orca_windows is not None:
                        reply = {"result": {"rateLimits": {
                            "codex": {"status": "ok", **{
                                name: {"usedPercent": used}
                                for name, used in orca_windows.items()
                            }},
                        }}}
                        with mock.patch.object(POOL["shutil"], "which", return_value=True), \
                                mock.patch.dict(POOL, {
                                    "run": mock.Mock(return_value=(json.dumps(reply), None)),
                                    "claude_pool": mock.Mock(),
                                }):
                            POOL["orca_pools"]()
                    elif read_quota:
                        POOL["codex_pool"](timeout=timeout)
                    else:
                        info = POOL["codex_read"](timeout=timeout, quota=False)
                        POOL["emit"]("codex", info["signed_in"], "codex-app-server",
                                     info["windows"], info["resets"], info["error"],
                                     plan=info["plan"], models=info["models"])
                return json.loads(output.getvalue().split(": ", 1)[1])

    def test_codex_primary_weekly_uses_duration_and_multibucket_view(self):
        # Shape from Codex CLI 0.142.3 account/rateLimits/read.
        quota = {"rateLimits": {"primary": {"usedPercent": 1}}, "rateLimitsByLimitId": {
            "codex": {"primary": {"usedPercent": 54, "windowDurationMins": 10080,
                                   "resetsAt": 1791303799}, "secondary": None},
        }}
        row = self.codex_row({"type": "chatgpt"}, quota)
        self.assertEqual(row["windows"], {"weekly": 54})
        self.assertEqual(row["worst"]["resets"], "2026-10-06T16:23:19Z")
        self.assertIsNone(row["plan"])

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

    def test_codex_account_read_error_does_not_claim_quota_was_read(self):
        row = self.codex_row("malformed", read_quota=False)
        self.assertEqual(row["error"], "unreadable codex app-server account")
        self.assertNotIn("quota", row["error"])

    def test_codex_exit_during_model_list_still_prints_a_row(self):
        row = self.codex_row({"type": "chatgpt"}, model_pages="exit")
        self.assertTrue(row["signed_in"])
        self.assertIsNone(row["windows"])
        self.assertEqual(row["error"], "unreadable codex app-server quota")

    def test_codex_orca_read_lists_models_without_reading_quota(self):
        account = {"type": "chatgpt", "planType": "prolite"}
        pages = [{"data": [{"id": "gpt-6-sol"}], "nextCursor": None}]
        with tempfile.NamedTemporaryFile() as seen:
            row = self.codex_row(account, model_pages=pages, requests_path=seen.name,
                                 read_quota=False)
            self.assertEqual(row["plan"], "prolite")
            self.assertEqual(row["models"], ["gpt-6-sol"])
            self.assertIsNone(row["windows"])
            self.assertNotIn("error", row)
            row = self.codex_row(account, model_pages=pages, requests_path=seen.name,
                                 orca_windows={"session": 0})
            self.assertEqual(row["plan"], "prolite")
            self.assertEqual(row["models"], ["gpt-6-sol"])
            self.assertEqual(row["windows"], {"session": 0})
            self.assertEqual(row["source"], "orca")
            self.assertNotIn("error", row)
            seen.seek(0)
            requests = [json.loads(line) for line in seen]
        self.assertEqual([request["method"] for request in requests],
                         ["model/list", "model/list"])

    def test_codex_model_list_is_reported_and_omitted_when_unreadable(self):
        quota = {"rateLimits": {"primary": {"usedPercent": 0}}}
        row = self.codex_row(
            {"type": "chatgpt"}, quota,
            model_pages=[{"data": [{"id": "gpt-6-sol"}, {"id": "gpt-6-luna"}],
                          "nextCursor": None}],
        )
        self.assertEqual(row["models"], ["gpt-6-sol", "gpt-6-luna"])
        self.assertNotIn("error", row)

        for pages in ([{"data": [], "nextCursor": None}],
                      [{"account": {}}], "timeout"):
            with self.subTest(pages=pages):
                row = self.codex_row({"type": "chatgpt"}, quota,
                                     model_pages=pages,
                                     **({"timeout": 1} if pages == "timeout" else {}))
                self.assertTrue(row["signed_in"])
                self.assertEqual(row["windows"], {"primary": 0})
                self.assertNotIn("models", row)
                if pages != [{"data": [], "nextCursor": None}]:
                    self.assertIn("error", row)
                else:
                    self.assertNotIn("error", row)

    def test_codex_model_list_follows_cursor(self):
        quota = {"rateLimits": {"primary": {"usedPercent": 0}}}
        with tempfile.NamedTemporaryFile() as seen:
            row = self.codex_row(
                {"type": "chatgpt"}, quota,
                model_pages=[{"data": [{"id": "gpt-6-sol"}], "nextCursor": "page-2"},
                             {"data": [{"id": "gpt-6-luna"}], "nextCursor": None}],
                requests_path=seen.name,
            )
            self.assertEqual(row["models"], ["gpt-6-sol", "gpt-6-luna"])
            seen.seek(0)
            requests = [request for line in seen
                        if (request := json.loads(line))["method"] == "model/list"]
        self.assertEqual(requests[0]["params"], {})
        # The cursor parameter is unverified because codex-cli 0.160.0 returned one page.
        self.assertEqual(requests[1]["params"], {"cursor": "page-2"})

    def test_codex_model_list_stops_on_a_repeated_cursor(self):
        quota = {"rateLimits": {"primary": {"usedPercent": 0}}}
        with tempfile.NamedTemporaryFile() as seen:
            row = self.codex_row({"type": "chatgpt"}, quota,
                                 model_pages="repeat",
                                 requests_path=seen.name)
            self.assertTrue(row["signed_in"])
            self.assertEqual(row["windows"], {"primary": 0})
            self.assertNotIn("models", row)
            seen.seek(0)
            requests = [request for line in seen
                        if (request := json.loads(line))["method"] == "model/list"]
        self.assertEqual(len(requests), 2)

    def test_non_string_plans_are_read_as_null(self):
        orca_reply = {"result": {"rateLimits": {
            "claude": {"status": "ok", "session": {"usedPercent": 0}},
        }}}
        runner = mock.Mock(side_effect=[
            (json.dumps(orca_reply), None),
            (json.dumps({"loggedIn": True, "authMethod": "claude.ai",
                         "subscriptionType": {"name": "max"}}), None),
        ])
        with mock.patch.object(POOL["shutil"], "which",
                               side_effect=lambda name: name in ("orca", "claude")), \
                mock.patch.dict(POOL, {"run": runner, "codex_pool": mock.Mock()}):
            claude = self.row("orca_pools")["claude"]
        with mock.patch.object(POOL["shutil"], "which", return_value="claude"), \
                mock.patch.dict(POOL, {"run": mock.Mock(side_effect=[
                    (json.dumps({"loggedIn": True, "authMethod": "claude.ai",
                                 "subscriptionType": {"name": "max"}}), None),
                    (None, "timeout"),
                ])}):
            native_claude = self.row("claude_pool")["claude"]
        codex = self.codex_row({"type": "chatgpt", "planType": ["prolite"]},
                               {"rateLimits": {"primary": {"usedPercent": 0}}})
        usage = {"reports": [{"provider": "zai", "metadata": {"planType": {"name": "lite"}},
                               "limits": [{"window": {"id": "5h"},
                                            "amount": {"usedFraction": 0.1}}]}]}
        with mock.patch.object(POOL["shutil"], "which", return_value="omp"), \
                mock.patch.dict(POOL, {"run": mock.Mock(return_value=(json.dumps(usage), None))}):
            zai = self.row("zai_pool")["zai"]
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            POOL["emit"]("codex", True, "test", plan=["prolite"])
        emitted = json.loads(output.getvalue().split(": ", 1)[1])
        for row in (claude, native_claude, codex, zai, emitted):
            self.assertIsNone(row["plan"])
            self.assertEqual(row["capacity"], 1)
            self.assertNotIn("plan_unlisted", row)

    def test_non_finite_registry_capacity_defaults_without_stopping_rows(self):
        with mock.patch.dict(POOL, {"registry_pools": lambda: {"claude": {
                "plans": {"inf": {"capacity": float("inf")},
                           "nan": {"capacity": float("nan")}},
            }}}):
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                POOL["emit"]("claude", True, "test", {"session": 0}, plan="inf")
                POOL["emit"]("claude", True, "test", {"session": 0}, plan="nan")
        rows = [json.loads(line.split(": ", 1)[1])
                for line in output.getvalue().splitlines()]
        self.assertEqual(len(rows), 2)
        for row in rows:
            self.assertEqual(row["capacity"], 1)
            self.assertTrue(row["plan_unlisted"])

    def test_null_and_unlisted_plans_have_distinct_capacity_metadata(self):
        with mock.patch.dict(POOL, {"registry_pools": lambda: {"codex": {
                "plans": {"prolite": {"capacity": 5}},
            }}}):
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                POOL["emit"]("codex", True, "test", {"session": 0})
                POOL["emit"]("codex", True, "test", {"session": 0}, plan="plus")
        rows = [json.loads(line.split(": ", 1)[1])
                for line in output.getvalue().splitlines()]
        self.assertEqual(rows[0]["capacity"], 1)
        self.assertNotIn("plan_unlisted", rows[0])
        self.assertEqual(rows[1]["capacity"], 1)
        self.assertTrue(rows[1]["plan_unlisted"])

    def test_registry_nested_plans_lists_quotes_and_comments_read_both_capacities(self):
        with tempfile.TemporaryDirectory() as directory:
            registry = Path(directory) / "registry.yaml"
            registry.write_text(
                'pools:\n'
                '  codex: {cli: codex, plans: {prolite: {capacity: 5, excludes: [a, b]}, '
                'plus: {capacity: 2}}, note: "x, y: #z"}  # c {d, e}\n'
            )
            with mock.patch.dict(POOL, {
                    "registry_pools": lambda: POOL["read_registry"](registry)}):
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    POOL["emit"]("codex", True, "test", plan="prolite")
                    POOL["emit"]("codex", True, "test", plan="plus")
        rows = [json.loads(line.split(": ", 1)[1])
                for line in output.getvalue().splitlines()]
        self.assertEqual(len(rows), 2)
        self.assertEqual([row["capacity"] for row in rows], [5, 2])
        for row in rows:
            self.assertNotIn("plan_unlisted", row)

    def test_google_unreadable_quota_keeps_readable_model_sign_in(self):
        with mock.patch.object(POOL["shutil"], "which", return_value="agy"), \
                mock.patch.dict(POOL, {"run": mock.Mock(side_effect=[
                    ("not json", None), ("gemini", None)])}):
            row = self.row("google_pool")["google"]
        self.assertIs(row["signed_in"], True)
        self.assertIsNone(row["windows"])
        self.assertIn("unreadable /quota reply", row["error"])

    def test_zai_empty_limits_keep_the_plan_without_an_error(self):
        usage = {"reports": [{"provider": "zai", "metadata": {"planType": "lite"},
                               "limits": []}]}
        with mock.patch.object(POOL["shutil"], "which", return_value="omp"), \
                mock.patch.dict(POOL, {"run": mock.Mock(return_value=(json.dumps(usage), None))}):
            row = self.row("zai_pool")["zai"]
        self.assertIs(row["signed_in"], True)
        self.assertEqual(row["plan"], "lite")
        self.assertIsNone(row["windows"])
        self.assertNotIn("error", row)

    def test_zai_missing_limits_keep_the_plan_and_signed_out_result(self):
        usage = {"reports": [{"provider": "zai", "metadata": {"planType": "lite"}}]}
        with mock.patch.object(POOL["shutil"], "which", return_value="omp"), \
                mock.patch.dict(POOL, {"run": mock.Mock(return_value=(json.dumps(usage), None))}):
            row = self.row("zai_pool")["zai"]
        self.assertIs(row["signed_in"], False)
        self.assertEqual(row["plan"], "lite")
        self.assertIsNone(row["windows"])
        self.assertEqual(row["error"], "no zai account in omp")

    def test_invalid_utf8_registry_leaves_a_named_plan_unlisted(self):
        with tempfile.TemporaryDirectory() as directory:
            registry = Path(directory) / "registry.yaml"
            registry.write_bytes(b"pools:\n  codex: \xff\n")
            with mock.patch.dict(POOL, {
                    "registry_pools": lambda: POOL["read_registry"](registry)}):
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    POOL["emit"]("codex", True, "test", plan="prolite")
        row = json.loads(output.getvalue().split(": ", 1)[1])
        self.assertEqual(row["capacity"], 1)
        self.assertTrue(row["plan_unlisted"])

    def test_registry_plan_capacity_and_trailing_comments_are_numeric(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            registry = root / ".claude" / "models" / "registry.yaml"
            registry.parent.mkdir(parents=True)
            registry.write_text(
                "pools:\n"
                "  codex: {cli: codex, plans: {prolite: {capacity: 5}}}  # comment\n"
            )
            with mock.patch.object(POOL["os"], "getcwd", return_value=directory), \
                    mock.patch.dict(os.environ, {"HOME": directory}):
                row = self.codex_row({"type": "chatgpt", "planType": "prolite"},
                                     {"rateLimits": {"primary": {"usedPercent": 0}}})
        self.assertEqual(row["capacity"], 5)
        self.assertNotIn("plan_unlisted", row)

    def test_named_plan_without_readable_registry_is_unlisted(self):
        with tempfile.TemporaryDirectory() as directory, \
                mock.patch.object(POOL["os"], "getcwd", return_value=directory), \
                mock.patch.dict(os.environ, {"HOME": directory}):
            row = self.codex_row({"type": "chatgpt", "planType": "prolite"},
                                 {"rateLimits": {"primary": {"usedPercent": 0}}})
        self.assertEqual(row["capacity"], 1)
        self.assertTrue(row["plan_unlisted"])

    def test_slots_follow_capacity_and_limit_table(self):
        cases = [(1, 0, 2), (1, 49, 2), (1, 50, 1), (1, 84, 1),
                 (1, 85, 0), (5, 80, 2), (20, 50, 6), (0.2, 0, 1),
                 (1, 90, 1)]
        for capacity, used, expected in cases:
            limit = 95 if used == 90 else 85
            with self.subTest(capacity=capacity, used=used), \
                    mock.patch.dict(POOL, {
                        "registry_pools": lambda: {"claude": {
                            "usable_below": limit,
                            "plans": {"tier": {"capacity": capacity}},
                        }}
                    }):
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    POOL["emit"]("claude", True, "test", {"session": used},
                                  plan="tier")
                row = json.loads(output.getvalue().split(": ", 1)[1])
            self.assertEqual(row["slots"], {"session": expected})

        output = io.StringIO()
        with mock.patch.dict(POOL, {"registry_pools": lambda: {}}), \
                contextlib.redirect_stdout(output):
            POOL["emit"]("claude", True, "test", plan="tier")
        self.assertIsNone(json.loads(output.getvalue().split(": ", 1)[1])["slots"])

    def test_capacity_map_applies_to_each_window(self):
        with mock.patch.dict(POOL, {"registry_pools": lambda: {"claude": {
                "usable_below": 95,
                "plans": {"max": {"capacity": {"session": 20}}},
            }}}):
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                POOL["emit"]("claude", True, "test",
                              {"session": 60, "weekly": 60, "fableWeekly": 5},
                              plan="max")
        row = json.loads(output.getvalue().split(": ", 1)[1])
        self.assertEqual(row["capacity"], {"session": 20})
        self.assertEqual(row["slots"], {"session": 6, "weekly": 1, "fableWeekly": 2})

    def test_project_registry_pool_replaces_machine_pool(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            machine = root / ".claude" / "models" / "registry.yaml"
            project = root / "project" / ".claude" / "models" / "registry.yaml"
            machine.parent.mkdir(parents=True)
            project.parent.mkdir(parents=True)
            machine.write_text(
                "pools:\n"
                "  claude: {usable_below: 95, plans: {max: {capacity: 5}}}\n"
            )
            project.write_text("pools:\n  claude: {cli: claude}\n")
            with mock.patch.object(POOL["os"], "getcwd", return_value=str(root / "project")), \
                    mock.patch.dict(os.environ, {"HOME": directory}):
                output = io.StringIO()
                with contextlib.redirect_stdout(output):
                    POOL["emit"]("claude", True, "test", {"session": 90},
                                  plan="max")
        row = json.loads(output.getvalue().split(": ", 1)[1])
        self.assertEqual(row["capacity"], 1)
        self.assertTrue(row["plan_unlisted"])
        self.assertEqual(row["slots"], {"session": 0})


if __name__ == "__main__":
    unittest.main()
