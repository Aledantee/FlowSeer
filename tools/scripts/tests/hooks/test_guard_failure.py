import contextlib
import io
import json
from types import SimpleNamespace
import unittest
from unittest import mock

import run


class GuardFailureTest(unittest.TestCase):
    def test_uncaught_guard_exception_becomes_a_denial(self):
        for command in ("worktree-guard", "pre-tool-policy", "protect-generated-bash"):
            with self.subTest(command=command):
                output = io.StringIO()
                module = SimpleNamespace(main=mock.Mock(side_effect=ValueError("unexpected input")))
                with mock.patch.object(run.importlib, "import_module", return_value=module):
                    with contextlib.redirect_stdout(output):
                        result = run.main(["hooks", command])
                self.assertEqual(result, 0)
                decision = json.loads(output.getvalue())["hookSpecificOutput"]
                self.assertEqual(decision["permissionDecision"], "deny")
                self.assertIn(command, decision["permissionDecisionReason"])

    def test_other_command_exceptions_still_propagate(self):
        module = SimpleNamespace(main=mock.Mock(side_effect=ValueError("unexpected input")))
        with mock.patch.object(run.importlib, "import_module", return_value=module):
            with self.assertRaises(ValueError):
                run.main(["hooks", "go-format"])
