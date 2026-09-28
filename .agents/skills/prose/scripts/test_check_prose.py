"""Fixture tests for check-prose.py's provenance patterns and literal skipping."""

import importlib.util
from pathlib import Path
import unittest

_spec = importlib.util.spec_from_file_location("check_prose", Path(__file__).with_name("check-prose.py"))
check_prose = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(check_prose)


def provenance(text):
    errors, _ = check_prose.check_text(text, "t.md", strict=False)
    return errors


def warnings(text):
    _, found = check_prose.check_text(text, "t.md", strict=False)
    return found


class ProvenanceTest(unittest.TestCase):
    def test_flags_run_citations(self):
        for text in [
            "The panic is known (session history).",
            "Per the 2026-08-16 session history, allocations fell.",
            "Its diff is in this session's history.",
            "Chosen (session-settled: user-directed, over X).",
            "- **Name.** (user-directed, chosen over X.)",
            "Why: user-directed: keep it bounded.",
            "User-directed. The lane is FIFO.",
            "Budget is fixed (user, 2026-09-25).",
            "As agreed with the user, the cap stays.",
            "The user chose it on 2026-09-27.",
            "The user ruled that this satisfies R3.",
            "The user's direction is one session per device.",
            "In an earlier run the cache was cold.",
            "Findings from a previous conversation.",
            "The chat transcript shows the fix.",
            "The agent transcript shows the retry.",
            "The worker reported a pass.",
            "The coordinator measured 56 allocs.",
        ]:
            with self.subTest(text=text):
                self.assertTrue(provenance(text), text)

    def test_keeps_product_and_instruction_prose(self):
        for text in [
            "Reuse the previous session when the device allows it.",
            "The edge agent log rotates daily.",
            "The agent reported a heartbeat after reconnect.",
            "The worker found no items and returned.",
            "Only user-approved firmware is installed.",
            "Ask the user which remedy to apply.",
            "It is the user's call.",
            "`cli-session.txt` is the SSH session transcript of the device.",
            "An SNMP session record keeps its counters.",
            "When the user approved it? Record it on disk.",
            "Edge agent logs show the retry.",
            "`drive` runs each stage in worker sessions.",
        ]:
            with self.subTest(text=text):
                self.assertEqual(provenance(text), [], text)


class LiteralTest(unittest.TestCase):
    def test_skips_fenced_blocks(self):
        text = "````markdown\n```\nsession history\n```\n````\n~~~\nsession history\n~~~\n"
        self.assertEqual(provenance(text), [])

    def test_backtick_fence_does_not_close_tilde_fence(self):
        text = "~~~\n```\nsession history\n~~~\n"
        self.assertEqual(provenance(text), [])

    def test_prose_after_fence_is_checked(self):
        text = "```\ncode\n```\nsee session history\n"
        self.assertEqual(len(provenance(text)), 1)

    def test_skips_frontmatter(self):
        self.assertEqual(provenance("---\nnote: session history\n---\nbody\n"), [])

    def test_skips_multiline_html_comment(self):
        text = "before\n<!-- review:\nsession history\n-->\nafter\n"
        self.assertEqual(provenance(text), [])

    def test_checks_text_after_comment_close(self):
        self.assertEqual(len(provenance("<!--\nx\n--> session history\n")), 1)

    def test_skips_inline_code(self):
        self.assertEqual(provenance("Write `(session history)` as a file path."), [])

    def test_url_in_inline_code_does_not_hide_prose(self):
        found = provenance("See `https://example.com/x` for the session history.")
        self.assertEqual(len(found), 1)

    def test_style_is_a_warning(self):
        self.assertEqual(provenance("One — two; three."), [])
        self.assertEqual(len(warnings("One — two; three.")), 2)

    def test_lets_the_verb_is_not_signposting(self):
        self.assertEqual(warnings("The flag lets a caller skip it."), [])
        self.assertTrue(warnings("Let’s look at the flow."))


if __name__ == "__main__":
    unittest.main()
