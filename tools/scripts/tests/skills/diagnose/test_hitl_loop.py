import contextlib
import importlib
import io
import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from skills.diagnose import hitl_loop


class HitlLoopTests(unittest.TestCase):
    def invoke(self, steps, answers):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "steps.json"
            path.write_text(json.dumps(steps), encoding="utf-8")
            output, errors = io.StringIO(), io.StringIO()
            with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
                with patch("builtins.input", side_effect=answers) as prompts:
                    code = hitl_loop.main(["--steps", str(path)])
            return code, output.getvalue(), errors.getvalue(), prompts

    def test_three_inputs_in_order(self):
        steps = [
            {"step": "Power on the switch and wait for the console prompt."},
            {"capture": "FAILED", "question": "Run the command under test. Did it fail? (y/n)"},
            {"capture": "OUTPUT", "question": "Paste the last line of output (or 'none'):"},
        ]
        code, output, errors, prompts = self.invoke(steps, ["", "y", "  a\\b=c\t"])
        self.assertEqual(code, 0, errors)
        self.assertTrue(output.endswith("--- Captured ---\nFAILED=y\nOUTPUT=a\\b=c\n"))
        self.assertLess(output.index("Power on"), output.index("Run the command"))
        self.assertLess(output.index("Run the command"), output.index("Paste the last"))
        self.assertEqual([call.args[0] for call in prompts.call_args_list],
                         ["    [Enter when done] ", "    > ", "    > "])

    def test_literal_empty_and_space_tab_trimming(self):
        steps = [{"capture": key, "question": key} for key in ("A", "B", "C", "D")]
        code, output, errors, _ = self.invoke(steps, [r"a\b=c", "", " \tanswer\t ", "\u00a0answer\u00a0"])
        self.assertEqual(code, 0, errors)
        self.assertTrue(output.endswith("A=a\\b=c\nB=\nC=answer\nD=\u00a0answer\u00a0\n"))

    def test_invalid_steps_before_prompting(self):
        cases = [[], {}, [1], [{"step": ""}], [{"step": 1}],
                 [{"step": "go", "extra": "field"}], [{"capture": "a", "question": "q"}],
                 [{"capture": "A", "question": ""}], [{"capture": "A"}],
                 [{"capture": 4, "question": "q"}]]
        for invalid in cases:
            with self.subTest(steps=invalid):
                steps = [{"step": "first"}, *invalid] if isinstance(invalid, list) and invalid else invalid
                code, output, errors, prompts = self.invoke(steps, ["", ""])
                self.assertEqual(code, 1)
                self.assertIn("hitl-loop:", errors)
                self.assertEqual(output, "")
                prompts.assert_not_called()

    def test_duplicate_keys_before_prompting(self):
        steps = [{"step": "first"}, {"capture": "A", "question": "q"}, {"capture": "A", "question": "q2"}]
        code, output, errors, prompts = self.invoke(steps, ["", "a", "b"])
        self.assertEqual(code, 1)
        self.assertIn("duplicate capture key: A", errors)
        self.assertEqual(output, "")
        prompts.assert_not_called()

    def test_bad_json_and_missing_file(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "steps.json"
            for exists in (False, True):
                if exists:
                    path.write_text("[broken", encoding="utf-8")
                with self.subTest(exists=exists), patch("builtins.input") as prompts:
                    errors = io.StringIO()
                    with contextlib.redirect_stderr(errors):
                        self.assertEqual(hitl_loop.main(["--steps", str(path)]), 1)
                    self.assertIn("hitl-loop:", errors.getvalue())
                    prompts.assert_not_called()

    def test_eof_and_interruption_without_complete_report(self):
        for exception in (EOFError(), KeyboardInterrupt()):
            with self.subTest(exception=type(exception).__name__):
                code, output, errors, _ = self.invoke(
                    [{"capture": "A", "question": "first"}, {"step": "second"}], ["seen", exception])
                self.assertEqual(code, 1)
                self.assertIn("second", output)
                self.assertNotIn("--- Captured ---", output)
                self.assertNotIn("A=seen", output)
                self.assertIn(type(exception).__name__, errors)

    def test_documented_example(self):
        root = Path(__file__).resolve().parents[5]
        doc = (root / ".agents/skills/diagnose/SKILL.md").read_text(encoding="utf-8")
        example = doc.split("```json\n", 1)[1].split("```", 1)[0]
        self.assertIn('uv run tools/scripts/run.py diagnose hitl-loop --steps "$TMPDIR/hitl-steps.json"', doc)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "hitl-steps.json"
            path.write_text(example, encoding="utf-8")
            result = subprocess.run([sys.executable, str(root / "tools/scripts/run.py"),
                                     "diagnose", "hitl-loop", "--steps", str(path)],
                                    input="\ny\nlink failed\n", text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(result.stdout.endswith("FAILED=y\nOUTPUT=link failed\n"))

    def test_import_emits_nothing(self):
        output, errors = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(output), contextlib.redirect_stderr(errors):
            importlib.reload(hitl_loop)
        self.assertEqual((output.getvalue(), errors.getvalue()), ("", ""))
