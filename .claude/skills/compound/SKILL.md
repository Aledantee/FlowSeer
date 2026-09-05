---
name: compound
description: Capture a verified FlowSeer lesson as a solution under docs/solutions/ with applies_when frontmatter and quoted evidence, or refresh the existing solutions against the current tree. Use after a fix or decision taught something the code does not make obvious, or when asked to compound, capture a learning, or audit stale solutions.
---

# Capture or refresh a FlowSeer solution

A solution preserves a lesson that reading the happy path would miss. It is
indexed by the conditions in which it applies, so a later agent can reject it
without reading the body. Vocabulary that the lesson introduces goes to
`CONCEPTS.md`. Neither replaces a convention doc or a direction record.

## Gate

Write a solution only when all three hold:

- The lesson is verified: a test, a measurement, or observed behavior backs
  it, and you can quote the source lines that make it true.
- It is not already stated in, or derivable within a few minutes from, the
  code, `AGENTS.md`, a convention doc, or a direction record. When a record
  already states the rule, link to it instead of restating it.
- It will apply again. A one-off fix goes in the commit message.

Otherwise say why no solution is warranted and stop. Volume without curation
turns the corpus into noise.

## Capture

### 1. Find the lesson

Name the mistake or discovery in one sentence, then the rule that prevents or
reuses it. Check `docs/solutions/README.md` for a solution that already covers
it; extend that one rather than adding a near-duplicate. When the change being
compounded already amended a solution, the job shrinks to checking that
solution's citations and index row; say so and do that.

### 2. Ground it

Collect evidence with repository-relative paths and line numbers
(`src/common/service/bus.go:132`), never bare filenames: the code that
embodies the rule, the test that proves it, the measurement if there was one.
Every non-obvious claim in the body cites one of these. Label a measurement
with its date and platform so a refresh can keep it as history. Reuse
existing values for `component`, `category`, and tags when the corpus already
has one for the area.

### 3. Write it

Path: `docs/solutions/<category>/<slug>.md` with `category` one of the
existing directories, or a new one if none fits.

```yaml
---
title: <Statement of the rule, as a sentence>
date: <YYYY-MM-DD>
category: <directory name>
module: <primary path, e.g. src/common/service>
problem_type: architecture_pattern | convention | bug
component: <area, reuse corpus values>
severity: critical | high | medium | low
applies_when:
  - "<concrete situation in which an agent should read this>"
related_components: [<other areas>]
tags: [<lowercase-hyphenated>]
---
```

For `problem_type: bug` add `symptoms`, `root_cause`, and `resolution_type`.

Body sections: the situation, what is true and why, how to apply it (with a
short working example), the evidence (quoted lines with paths), and what it
does not cover. Follow `docs/doc-style.md`. Keep it under about 120 lines.

### 4. Index and vocabulary

Add a row to the table in `docs/solutions/README.md` with a "Read when"
sentence that matches `applies_when`; when you extended a solution's
`applies_when`, extend its row the same way. Set `last_verified` to today on
any solution whose citations you re-checked.

When the lesson introduces a term, propose the `CONCEPTS.md` entry to the
user before adding it. Do not edit `AGENTS.md` or any other policy surface
from this skill; if a rule belongs there, say so and let the user decide.

### 5. Verify

Run the verifier on the changed files and report the path and the single
sentence a reader should remember.

## Refresh

When asked to refresh, audit each solution under `docs/solutions/`. One agent
per solution in parallel is fine; `docs/solutions/README.md` is the only
shared file, so edit it from the coordinating session.

1. Open every cited path and confirm the quoted lines and symbols exist.
   Check the frontmatter too: `module` still exists, `applies_when` still
   describes the situation, the README "Read when" row still matches.
2. Re-run the test it relies on. Skip a gate that needs hardware, Docker, or
   more than a few minutes, and say so in the report. A measurement labelled
   with its date stays as history.
3. Look for a later `docs/conventions/` doc, `docs/architecture/` record,
   `docs/code-style*.md` rule, or sibling solution that now states the same
   thing. When one does, replace the restatement with a link.

Outcomes: kept (nothing changed), fixed (citations only), rewritten (any
claim changed), removed (the tree no longer behaves as described; delete the
file and its index row). Fix style tells only in lines you already rewrite.
Set `last_verified: <YYYY-MM-DD>` in the frontmatter of every solution you
checked. Run the verifier on the changed files and report the outcomes table
with a reason per row.
