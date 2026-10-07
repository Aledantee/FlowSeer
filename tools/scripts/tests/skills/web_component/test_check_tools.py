import contextlib
import importlib
import io
import json
import subprocess
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from unittest.mock import Mock, patch

from skills.web_component import check_tools


class CheckToolsTests(unittest.TestCase):
    def invoke(self, *, node="v22.12.0", absent=(), windows=False, impeccable=False,
               impeccable_output=b"fixture-engine\n", impeccable_code=0, storybook=True, floor=">=22.12.0"):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            web = root / "frontend/web"
            web.mkdir(parents=True)
            (web / "package.json").write_text(json.dumps({"engines": {"node": floor}, "packageManager": "pnpm@9.7.1"}), encoding="utf-8")

            def which(name, **kwargs):
                return None if name.removesuffix(".cmd") in absent else str(Path(kwargs.get("path", directory)) / name)

            def child(argv, **kwargs):
                if "engine-probe" in argv:
                    if isinstance(impeccable_output, Exception):
                        raise impeccable_output
                    return subprocess.CompletedProcess(argv, impeccable_code, impeccable_output, b"")
                elif Path(argv[0]).name == "node":
                    stdout = node.encode()
                else:
                    stdout = b"11.25.0\n"
                return subprocess.CompletedProcess(argv, 0, stdout, b"")

            output, errors = io.StringIO(), io.StringIO()
            with contextlib.ExitStack() as stack:
                stack.enter_context(patch.object(check_tools.repo, "root", return_value=root))
                stack.enter_context(patch.object(check_tools.sys, "platform", "win32" if windows else "darwin"))
                lookup = stack.enter_context(patch.object(check_tools.shutil, "which", side_effect=which))
                launched = stack.enter_context(patch.object(check_tools.subprocess, "run", side_effect=child))
                stack.enter_context(patch.object(check_tools.os, "access", return_value=impeccable))
                stack.enter_context(patch.object(check_tools, "storybook_running", return_value=storybook))
                stack.enter_context(contextlib.redirect_stdout(output))
                stack.enter_context(contextlib.redirect_stderr(errors))
                code = check_tools.main([])
            return code, output.getvalue(), errors.getvalue(), lookup, launched

    def test_report_with_tools(self):
        with patch.object(check_tools.Path, "home", return_value=Path("/fixture-home")):
            code, output, errors, _, _ = self.invoke(impeccable=True)
        self.assertEqual(code, 0, errors)
        self.assertEqual(output, (
            "ok       node               v22.12.0\n"
            "ok       pnpm               11.25.0\n"
            "ok       node_modules       frontend/web dependencies installed\n"
            "ok       agent-browser      11.25.0 (screenshot review of stories)\n"
            "ok       impeccable         fixture-engine (design anti-pattern detector at /fixture-home/.claude/plugins/marketplaces/impeccable/.claude/skills/impeccable/scripts/impeccable)\n"
            "ok       storybook          running on http://127.0.0.1:6006\n"
        ))

    def test_report_with_missing_required_tool(self):
        code, output, errors, _, _ = self.invoke(absent=("pnpm",))
        self.assertEqual(code, 1)
        self.assertEqual(errors, "check-tools: required web tools are missing, outdated, or invalid\n")
        self.assertEqual(output, (
            "ok       node               v22.12.0\n"
            "missing  pnpm               package manager pinned in package.json: pnpm@9.7.1\n"
            "                            install: corepack enable pnpm (package.json: pnpm@9.7.1)\n"
            "ok       node_modules       frontend/web dependencies installed\n"
            "ok       agent-browser      11.25.0 (screenshot review of stories)\n"
            "optional impeccable         design anti-pattern detector used in review mode\n"
            "                            install: claude plugin marketplace add pbakaus/impeccable && claude plugin install impeccable@impeccable\n"
            "ok       storybook          running on http://127.0.0.1:6006\n"
        ))

    def test_node_floor_and_malformed_versions(self):
        for node, status, code in (("v22.12.0", "ok", 0), ("v24.0.0", "ok", 0),
                                   ("v22.11.9", "old", 1), ("v9.0.0", "old", 1),
                                   ("nonsense", "invalid", 1), ("v22.12", "invalid", 1),
                                   ("v22.12.0junk", "invalid", 1), ("", "missing", 1)):
            with self.subTest(node=node):
                got, output, errors, _, _ = self.invoke(node=node)
                self.assertEqual(got, code)
                self.assertRegex(output, rf"{status}\s+node\s")
                if code:
                    self.assertIn("required web tools", errors)

    def test_floor_and_pnpm_pin_from_package(self):
        code, output, _, _, _ = self.invoke(floor=">=24.1.0", node="v24.0.0", absent=("pnpm",))
        self.assertEqual(code, 1)
        self.assertIn("below the engines floor 24.1.0", output)
        self.assertIn("package manager pinned in package.json: pnpm@9.7.1", output)
        self.assertIn("install: corepack enable pnpm (package.json: pnpm@9.7.1)", output)

    def test_missing_required_and_optional_tools(self):
        for name in ("node", "pnpm", "vitest", "vue-tsc", "agent-browser"):
            with self.subTest(name=name):
                code, output, errors, _, _ = self.invoke(absent=(name,), storybook=False)
                self.assertEqual(code, 0 if name == "agent-browser" else 1)
                label = "node_modules" if name in ("vitest", "vue-tsc") else name
                self.assertRegex(output, rf"{'optional' if name == 'agent-browser' else 'missing'}\s+{label}\s")
                self.assertRegex(output, r"optional\s+storybook\s")
                if code:
                    self.assertIn("required web tools", errors)

    def test_windows_bins_and_impeccable_unsupported(self):
        code, output, errors, lookup, launched = self.invoke(windows=True, impeccable=True)
        self.assertEqual(code, 0, errors)
        names = [call.args[0] for call in lookup.call_args_list]
        self.assertEqual(names, ["node", "pnpm.cmd", "vitest.cmd", "vue-tsc.cmd", "agent-browser.cmd"])
        self.assertIn("unsupported on Windows", output)
        self.assertFalse(any("engine-probe" in call.args[0] for call in launched.call_args_list))
        for call in lookup.call_args_list[2:4]:
            self.assertTrue(call.kwargs["path"].endswith("node_modules/.bin"))

    def test_posix_impeccable_probe(self):
        cases = [(b"fixture-engine\n", 0, "ok"), (b"", 0, "ok"),
                 (b"failed", 1, "optional"), (OSError("missing"), 0, "optional")]
        for reply, exit_code, status in cases:
            with self.subTest(reply=reply, exit_code=exit_code):
                code, output, errors, _, launched = self.invoke(
                    impeccable=True, impeccable_output=reply, impeccable_code=exit_code)
                self.assertEqual(code, 0, errors)
                self.assertRegex(output, rf"{status}\s+impeccable\s")
                if reply == b"fixture-engine\n":
                    self.assertIn("fixture-engine", output)
                self.assertTrue(any("engine-probe" in call.args[0] for call in launched.call_args_list))

    def test_child_probe_argv_and_timeout(self):
        runner = Path(__file__).resolve().parents[3] / "run.py"
        for reply, expected in ((subprocess.CompletedProcess([], 0), True),
                                (subprocess.CompletedProcess([], 1), False),
                                (subprocess.TimeoutExpired([], 2), False), (OSError("missing"), False)):
            with self.subTest(reply=reply), patch.object(check_tools.subprocess, "run") as child:
                if isinstance(reply, Exception):
                    child.side_effect = reply
                else:
                    child.return_value = reply
                self.assertEqual(check_tools.storybook_running(), expected)
                self.assertEqual(child.call_args.args[0], [sys.executable, str(runner), "web-component", "check-tools", "--storybook-probe"])
                self.assertEqual(child.call_args.kwargs["timeout"], 2)

    def test_http_probe_success_error_timeout_without_rows(self):
        response = Mock()
        response.__enter__ = Mock(return_value=response)
        response.__exit__ = Mock(return_value=False)
        cases = ((None, 0), (urllib.error.HTTPError("url", 503, "unavailable", {}, None), 1),
                 (TimeoutError("timeout"), 1), (urllib.error.URLError("refused"), 1))
        for error, code in cases:
            with self.subTest(error=error), patch.object(check_tools.urllib.request, "urlopen") as request:
                request.return_value = response
                request.side_effect = error
                output, errors = io.StringIO(), io.StringIO()
                with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                    self.assertEqual(check_tools.main(["--storybook-probe"]), code)
                request.assert_called_once_with("http://127.0.0.1:6006/", timeout=2)
                self.assertEqual(output.getvalue(), "")
                if code:
                    self.assertIn("Storybook probe failed", errors.getvalue())
        response.read.assert_called_once_with()

    def test_dripping_response_has_total_deadline(self):
        stop = threading.Event()
        started = threading.Event()

        class Drip(BaseHTTPRequestHandler):
            def do_GET(self):
                self.send_response(200)
                self.send_header("Content-Length", "100")
                self.end_headers()
                started.set()
                try:
                    for _ in range(100):
                        self.wfile.write(b"x")
                        self.wfile.flush()
                        # Each byte arrives inside the socket timeout, exceeding the total budget.
                        if stop.wait(0.1):
                            break
                except (BrokenPipeError, ConnectionResetError):
                    pass

            def log_message(self, *args):
                pass

        server = HTTPServer(("127.0.0.1", 0), Drip)
        thread = threading.Thread(target=server.handle_request)
        thread.start()
        original_run = subprocess.run
        url = f"http://127.0.0.1:{server.server_port}/"
        runner = Path(__file__).resolve().parents[3] / "run.py"
        program = (
            "import sys,runpy,urllib.request; "
            "original=urllib.request.urlopen; "
            f"urllib.request.urlopen=lambda url,**kw: original({url!r},**kw); "
            f"sys.argv={[str(runner), 'web-component', 'check-tools', '--storybook-probe']!r}; "
            f"runpy.run_path({str(runner)!r},run_name='__main__')"
        )

        def child(argv, **kwargs):
            return original_run([sys.executable, "-c", program], **kwargs)

        try:
            before = time.monotonic()
            with patch.object(check_tools.subprocess, "run", side_effect=child):
                self.assertFalse(check_tools.storybook_running())
            elapsed = time.monotonic() - before
            self.assertTrue(started.is_set())
            self.assertLess(elapsed, 3)
        finally:
            stop.set()
            thread.join(3)
            server.server_close()
        self.assertFalse(thread.is_alive())

    def test_tool_launch_failure_and_invalid_manifest(self):
        with patch.object(check_tools.subprocess, "run", side_effect=OSError("missing")):
            self.assertEqual(check_tools.version(["missing", "--version"]), "")
        with patch.object(check_tools.subprocess, "run", return_value=subprocess.CompletedProcess([], 1, b"wrong")):
            self.assertEqual(check_tools.version(["broken"]), "")
        with patch.object(check_tools.repo, "root", side_effect=RuntimeError("not inside repo")):
            errors = io.StringIO()
            with contextlib.redirect_stderr(errors):
                self.assertEqual(check_tools.main([]), 1)
            self.assertIn("not inside repo", errors.getvalue())
        code, _, errors, _, _ = self.invoke(floor="not a version")
        self.assertEqual(code, 1)
        self.assertIn("unsupported Node engines floor", errors)

    def test_import_emits_nothing(self):
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            importlib.reload(check_tools)
        self.assertEqual((output.getvalue(), errors.getvalue()), ("", ""))
