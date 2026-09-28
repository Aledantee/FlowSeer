"""Fixture tests for check-prose.py's provenance patterns and literal skipping.

Every top-level alternative of every PROVENANCE pattern has a flagged case
below that fails when that alternative alone is removed. Add one when you add
an alternative.
"""

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


# One entry per top-level alternative, in PROVENANCE order.
FLAGGED = [
    # cites a session
    "The panic is known (session history).",
    "Per the 2026-08-16 session history, allocations fell.",
    "Its diff is in this session's history.",
    "The codex session history holds the diff.",
    "Chosen (session-settled over X).",
    # cites a run
    "The cache was cold (this run).",
    "The cache was cold (an earlier conversation).",
    # cites a run
    "A previous agent session left the branch dirty.",
    "Measured in an earlier run at 56 allocs.",
    # cites a transcript
    "The chat transcript shows the fix.",
    "The worker agent history shows a retry.",
    "The agent transcript shows the retry.",
    # attributes to a conversation: tags
    "Chosen (user-confirmed over X).",
    "Cap fixed, user-directed, to stay bounded.",
    "User-directed on 2026-09-17 to keep the scope small",
    # attributes to a conversation: dated
    "Budget is fixed (user, 2026-09-25).",
    # attributes to a conversation: phrasing
    "As agreed with the user, the cap stays.",
    "The user's direction is one session per device.",
    # attributes to a conversation: decision record
    "The user chose a flat list over a tree.",
    "The user chose it on 2026-09-27.",
    "The user ruled that this satisfies R3.",
    # cites an agent run
    "The worker reported a pass.",
    "The coordinator observed a stall.",
    "The review agent found the lock was held too long.",
]

# The "Instead of" column of docs/doc-style.md's "Cite the tree, never a run".
DOC_STYLE_ROWS = [
    "…panics on non-comparable causes (session history).",
    "(session-settled: user-directed, chosen over X: reason.)",
    "Measured in an earlier run at 56 allocs.",
    "The review agent found the lock was held too long.",
]

# Product and instruction prose that must pass.
KEPT = [
    "The edge agent history of reconnects is kept for a day.",
    "The CLI session history buffer holds 20 commands.",
    "The SSH session history is cleared on logout.",
    "The user requested a rollback from the UI.",
    "Once the user approved the change, the edge applies it.",
    "The worker found no items and returned.",
    "Reuse the previous SSH session when the device allows it.",
    "The edge agent log rotates daily.",
    "The agent reported a heartbeat after reconnect.",
    "`cli-session.txt` is the SSH session transcript of the device.",
    "The device session transcript is redacted.",
    "Only user-approved firmware is installed.",
    "Ask the user which remedy to apply.",
    "It is the user's call.",
    "An SNMP session record keeps its counters.",
    "The previous run of the sweep removed three files.",
    "`drive` runs each stage in worker sessions.",
]


class ProvenanceTest(unittest.TestCase):
    def test_flags_run_citations(self):
        for text in FLAGGED:
            with self.subTest(text=text):
                self.assertTrue(provenance(text), text)

    def test_flags_doc_style_examples(self):
        for text in DOC_STYLE_ROWS:
            with self.subTest(text=text):
                self.assertTrue(provenance(text), text)

    def test_keeps_product_and_instruction_prose(self):
        for text in KEPT:
            with self.subTest(text=text):
                self.assertEqual(provenance(text), [], text)


class LiteralTest(unittest.TestCase):
    def test_skips_fenced_blocks(self):
        text = "````markdown\n```\n(session history)\n```\n````\n~~~\n(session history)\n~~~\n"
        self.assertEqual(provenance(text), [])

    def test_backtick_fence_does_not_close_tilde_fence(self):
        text = "~~~\n```\n(session history)\n~~~\n"
        self.assertEqual(provenance(text), [])

    def test_prose_after_fence_is_checked(self):
        text = "```\ncode\n```\nsee (session history)\n"
        self.assertEqual(len(provenance(text)), 1)

    def test_skips_frontmatter(self):
        self.assertEqual(provenance("---\nnote: (session history)\n---\nbody\n"), [])

    def test_skips_multiline_html_comment(self):
        text = "before\n<!-- review:\n(session history)\n-->\nafter\n"
        self.assertEqual(provenance(text), [])

    def test_checks_text_after_comment_close(self):
        self.assertEqual(len(provenance("<!--\nx\n--> (session history)\n")), 1)

    def test_skips_inline_code(self):
        self.assertEqual(provenance("Write `(session history)` as a file path."), [])

    def test_url_in_inline_code_does_not_hide_prose(self):
        # A URL rule that ran first and swallowed the closing backtick would
        # let the stray tick pair with the next span and blank the prose.
        found = provenance("See `https://a.b/x` (session history) and `z`.")
        self.assertEqual(len(found), 1)

    def test_style_is_a_warning(self):
        self.assertEqual(provenance("One — two; three."), [])
        self.assertEqual(len(warnings("One — two; three.")), 2)

    def test_lets_the_verb_is_not_signposting(self):
        self.assertEqual(warnings("The flag lets a caller skip it."), [])
        self.assertTrue(warnings("Let’s look at the flow."))


if __name__ == "__main__":
    unittest.main()
