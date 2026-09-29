# Rulings

Load this when a unit needs a decision the plan does not make, or when
evidence contradicts a `Ruled:` line already written.

When the answer changes other units, the wire, or an accepted record, ask the
user (`AGENTS.md`, Agent behavior) with the options you see and your
recommendation, and wait.

Otherwise rule and continue: at the moment of the call, append one line to
the plan's Decisions:

```text
Ruled: <what>. Why: <reason>. Cost if wrong: <what a reversal touches>.
```

Open questions holds only what is still open; a decision filed there after
the fact reads as unresolved to the reviewer and as settled to the next
implementer. Rulings are the first item of the Finish report.

A ruling is provisional until its unit lands. Evidence that contradicts one (a
test that passes when the ruling says it cannot, an experiment whose result
the ruling does not predict) stops the unit: revise the `Ruled:` line and
everything written from it, comments and test docs included, before the next
edit. Write comments from the source, not from the ruling.
