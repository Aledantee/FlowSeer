---
name: prose
description: Writes or edits FlowSeer prose (Markdown docs, READMEs, plans, solutions, skills, commit and PR text, agent reports) so it follows docs/doc-style.md, and checks Markdown with check-prose.py. Use before writing or rewriting any document, when asked to clean up, de-slop, or tighten prose, or when verify-change reports a provenance finding. Not for code comments' placement rules (code-style.md) or schema contract phrasing (code-style-proto.md).
argument-hint: "[paths to check or rewrite]"
---

# Write FlowSeer prose

[`docs/doc-style.md`](../../../docs/doc-style.md) holds the rules and why they
exist. This skill is the order to apply them in and the check that catches
the mechanical ones.

## While writing

1. Decide what the reader needs and in which order. Write that, and nothing
   you only needed while working it out.
2. Cite only what a reader can open: a file in the tree, a test, a benchmark,
   a published source, a commit hash as a last resort. Never an agent run,
   a session, a transcript, a worker, or who said what in a conversation.
   A decision states its reason, not its author.
3. If a flow or sequence has more than three steps, or the text describes a
   state machine or ownership graph, draw it in a fenced `mermaid` block and
   cut the sentences it replaces.
4. Keep punctuation plain: no em dashes, no semicolons in prose.

## Check

```bash
python3 .claude/skills/prose/scripts/check-prose.py <files.md>
```

| Finding | Severity | Fix |
| --- | --- | --- |
| `provenance, …` (stderr) | fails the check and `verify-change` | Replace the run citation with the file, test, or reason it stood in for. If nothing in the tree backs the claim, drop the claim. |
| `warning: …` | advisory | Fix every warning in text you wrote or rewrote. Leave untouched text alone unless the task is a cleanup. |

`--strict` turns warnings into failures, for a cleanup pass over a file you
own end to end. The checker skips YAML frontmatter, fenced blocks (``` or
~~~), HTML comments, inline code on one line, link targets, and bare URLs.
A pattern quoted as an example goes in backticks on one line. Indented text
is checked, since in this repository it is almost always a list
continuation.

Only literal markers of an agent run fail:

- `(session history)`, `per the <date> session history`, `session-settled`
- `(user, <date>)`
- a `user-directed` or `user-decided` tag: `(user-directed`, `user-directed:`
  or `user-directed)`, or `User-directed.` as a sentence of its own
- a run tool's transcript or session history, in the forms
  `per the <name> transcript`, `<name> transcript shows`, and
  `<name> session history`, where `RUN_NAMES` in `scripts/check-prose.py`
  lists the names (`claude`, `codex`, `worker`, …)

Softer cues also occur in product prose, so they print as
`possible provenance` warnings and need your judgment. They include
`the user chose X over Y`, `the review agent found`, and the
`user-approved` and `user-confirmed` tags, which double as product state
labels. Matching is per line, so a marker wrapped across two lines is not
caught. `scripts/test_check_prose.py` holds a flagged case for every
marker and the product phrases that must pass. Add a case there when you
change a marker.

The checker cannot see overexplaining, rule-of-three lists, uniform rhythm,
or a paragraph that restates its heading. Reread once for those after it
passes: delete every sentence the reader would not miss.

## Cleanup passes

When the task is to clean up existing prose, keep meaning and facts intact
and change only wording. Per file:

1. Run the checker with `--strict` and fix each finding in context. An em
   dash usually becomes a period or a colon. A semicolon becomes two
   sentences.
2. Cut restatement and signposting the checker missed.
3. Leave quoted source text, command output, and captured device transcripts
   verbatim. They are evidence.
