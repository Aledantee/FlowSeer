"""Fixture tests for `verify check-prose`'s provenance markers and literal skipping.

Every PROVENANCE entry has a flagged case below that fails when that entry
alone is removed, and no case is caught by two entries. Add one when you add
an entry.
"""

import unittest

from verify import check_prose


def provenance(text):
    errors, _ = check_prose.check_text(text, "t.md", strict=False)
    return errors


def warnings(text):
    _, found = check_prose.check_text(text, "t.md", strict=False)
    return found


# (text, label) per PROVENANCE entry.
FLAGGED = [
    ("The panic is known (session history).", "cites a session"),
    ("Per the 2026-08-16 session history, allocations fell.", "cites a session"),
    ("Chosen, session-settled, over a tree.", "cites a session"),
    ("Budget is fixed (user, 2026-09-25).", "attributes to a conversation"),
    # An approved tag no longer blocks, but a session-settled prefix still does.
    ("(session-settled: user-approved, chosen over X.)", "cites a session"),
]
_TAG_CASES = {
    # "(user-X" opening a parenthetical, "user-X:" or "user-X)" closing a tag,
    # and "User-X." as a sentence of its own after another or opening the
    # line. Each case matches one form.
    "paren": "Kept flat (user-{tag}, chosen over a tree).",
    "colon": "Why: user-{tag}: a flat list keeps keys stable.",
    "sentence": "The cap stays at four. User-{tag}.",
    "line": "User-{tag}. The cap stays at four.",
}
for _tag in check_prose.TAGS:
    for _form in ("paren", "colon", "sentence", "line"):
        FLAGGED.append((_TAG_CASES[_form].format(tag=_tag), "attributes to a conversation"))
for _name in check_prose.RUN_NAMES:
    FLAGGED.append((f"Per the {_name} transcript, the retry passed.", "cites a transcript"))
    FLAGGED.append((f"The {_name} transcript shows the retry.", "cites a transcript"))
    FLAGGED.append((f"The {_name} session history holds the diff.", "cites a session"))

# The "Instead of" column of docs/doc-style.md's "Cite the tree, never a run".
# The first two carry markers and fail. The other two are judgment calls the
# checker can only warn about.
DOC_STYLE_FAIL = [
    "…panics on non-comparable causes (session history).",
    "(session-settled: user-directed, chosen over X: reason.)",
]
DOC_STYLE_WARN = [
    "Measured in an earlier run at 56 allocs.",
    "The review agent found the lock was held too long.",
]

# Cues that often mean a run but also occur in product prose: they warn and
# never fail.
WARN_ONLY = [
    "The user chose a flat list over a tree.",
    "The user chose it on 2026-09-27.",
    "The user ruled that this satisfies R3.",
    "The chat transcript shows the fix.",
    "The worker agent history shows a retry.",
    "As agreed with the user, the cap stays.",
    "The user's direction is one session per device.",
    "The worker reported a pass.",
    "The coordinator observed a stall.",
    "A previous agent session left the branch dirty.",
    "The cache was cold (this run).",
    "Cap fixed, user-directed, to stay bounded.",
    "User-directed on 2026-09-17 to keep the scope small",
    "Its diff is in this session's history.",
    "`tune` reads Claude transcripts and Codex session files.",
]

# Product and instruction prose that must pass, including every false
# positive an earlier version of the checker produced.
KEPT = [
    "The user chose SSH over Telnet in the wizard.",
    "The user requested a config push over SSH.",
    "The user requested a rollback from the UI.",
    "Once the user approved the change, the edge applies it.",
    "Diff against the state from a previous run.",
    "The previous run of the sweep removed three files.",
    "The CLI keeps a session's history in memory.",
    "As per the vendor manual, the CLI session history is cleared.",
    "The CLI session history buffer holds 20 commands.",
    "The SSH session history is cleared on logout.",
    "The edge agent session history of reconnects is kept for a day.",
    "The edge agent history of reconnects is kept for a day.",
    "Counters reset (this session).",
    "user-approved firmware is installed on every switch.",
    "The firmware must be user-approved.",
    "Only user-approved firmware is installed.",
    "Firmware images (user-approved or vendor-signed) are listed separately.",
    "Roles are either inferred or user-confirmed).",
    "- **user-approved:** an image an operator signed off.",
    "The end user's decision to opt out is stored.",
    "The worker reported an error.",
    "The worker found no items and returned.",
    "Reuse the previous SSH session when the device allows it.",
    "The edge agent log rotates daily.",
    "The agent reported a heartbeat after reconnect.",
    "`cli-session.txt` is the SSH session transcript of the device.",
    "The device session transcript is redacted.",
    "Ask the user which remedy to apply.",
    "It is the user's call.",
    "An SNMP session record keeps its counters.",
    "`drive` runs each stage in worker sessions.",
]


# (text, label) per STYLE entry that states a contrast, a saying, a closer,
# or an answer to nobody. Each case matches one alternative of its entry.
STYLE_FLAGGED = [
    ("It's not a cache, it's a ledger.", "negative parallelism"),
    ("This is not a retry, this is a second request.", "negative parallelism"),
    ("The cap holds. That distinction matters.", "closer"),
    ("At its core the pump is a queue.", "staged saying"),
    ("The real question is whether the edge reconnects.", "staged saying"),
    ("Retries hide brief outages. That is the real win.", "closer"),
    ("The lock is held across the call. Let that sink in.", "closer"),
    ("A tempting approach would be to restart the service.", "answers an objection nobody raised"),
    ("This is not to say the cache is wrong.", "answers an objection nobody raised"),
]

# Plain statements near those patterns that must stay quiet.
STYLE_KEPT = [
    "It is not set when the device omits the field.",
    "This is not supported, and the call returns an error.",
    "That does not mean much without the capture.",
    "The core of the pump is a queue.",
    "The question is whether the edge reconnects.",
    "That is the point where the session closes.",
    "If it is not ready, it is dropped.",
    "When it is not set, it is omitted.",
    "Unless this is not present, this is an error.",
    "That distinction matters because the kernel reorders packets.",
]


class StyleTest(unittest.TestCase):
    def test_flags_each_pattern_with_its_label(self):
        for text, label in STYLE_FLAGGED:
            with self.subTest(text=text):
                found = warnings(text)
                self.assertEqual(len(found), 1, found)
                self.assertIn(f": {label}", found[0])

    def test_keeps_plain_statements(self):
        for text in STYLE_KEPT:
            with self.subTest(text=text):
                self.assertEqual(warnings(text), [], text)


class ProvenanceTest(unittest.TestCase):
    def test_flags_each_marker_with_its_label(self):
        for text, label in FLAGGED:
            with self.subTest(text=text):
                found = provenance(text)
                self.assertEqual(len(found), 1, found)
                self.assertIn(f"provenance, {label}:", found[0])

    def test_doc_style_examples(self):
        for text in DOC_STYLE_FAIL:
            with self.subTest(text=text):
                self.assertTrue(provenance(text), text)
        for text in DOC_STYLE_WARN:
            with self.subTest(text=text):
                self.assertEqual(provenance(text), [], text)
                self.assertTrue(any("possible provenance" in w for w in warnings(text)), text)

    def test_soft_cues_warn_and_do_not_fail(self):
        for text in WARN_ONLY:
            with self.subTest(text=text):
                self.assertEqual(provenance(text), [], text)
                self.assertTrue(any("possible provenance" in w for w in warnings(text)), text)

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
