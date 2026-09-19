---
title: A Stateless Reverse-Match Must Skip, Not Block On, a Rule Its Dropped Field Qualifies
date: 2026-09-19
last_verified: 2026-09-19
category: conventions
module: src/common/netsim/vswitch/filter
problem_type: convention
component: netsim
severity: high
applies_when:
  - "Reconstructing 'would the other side have accepted this?' from configuration alone — a stateful reverse-match, a reversed 5-tuple, a mirrored predicate — where the reconstructed input carries fewer fields than a rule can test"
  - "Making a matcher ignore a field for one purpose (so a rule qualified on it still matches) while a first-match loop breaks on the first match of any action"
  - "Reviewing two fixes to one predicate that pull opposite ways: one widens what matches, the other makes the first match terminal"
related_components: [netsim, routing, filter]
tags: [stateless-reconstruction, first-match, predicate, reverse-match, shadowing]
---

# A stateless reverse-match must skip a rule its dropped field qualifies

## The situation

netsim answers as a function of configuration and frame, with no state kept
between frames. A stateful filter set therefore admits a reply by
*reconstructing* the forward decision: it reverses the reply's 5-tuple and asks
whether the counterpart set would accept it
(`docs/architecture/2026-09-16-local-network-analysis-direction.md:34`, "the
reversed 5-tuple would be accepted"). The reconstructed input is the 5-tuple
only — it carries no TCP flags and no ICMP type, because the reply is not the
forward packet.

Two independently-correct fixes to the reverse-match predicate combined into a
defect. One made the match *first-match* (stop at the first matching rule; a
non-Accept match is terminal). The other made the 5-tuple predicate *ignore*
TCP-flags/ICMP constraints, so a flag-qualified `Accept` rule still admits a
reply. Together, a flag-qualified **non-Accept** rule — say `Drop TCP dst 443
flags RST` — now matches the reversed 5-tuple (flags ignored) and terminates the
search, shadowing a later `Accept TCP dst 443` and dropping the reply of a flow
the forward direction established. Neither fix is wrong alone; the interaction is.

## The rule

When a reverse-match drops a field from its predicate, a rule that *qualifies on
the dropped field* is undecidable from the reconstructed input, and undecidable
must not mean "matches" for a blocking verdict. Skip it and keep searching:

- first matching rule with `Action == Accept` → accept;
- non-Accept rule carrying a constraint on the dropped field
  (`Match.ICMP`/`Match.TCPFlags`) → **continue** (its condition cannot be
  evaluated from the reversed 5-tuple and did not apply to the
  connection-initiating forward packet);
- non-Accept rule with no such constraint → a genuine 5-tuple block; stop.

This keeps the reverse pass 5-tuple-only, as the direction record commits, while
letting a flag-qualified `Accept` admit a reply and refusing to let a
flag-qualified `Drop` shadow one.

## Evidence

- The skip lives in both reverse loops:
  `src/common/netsim/vswitch/filter/filter.go:319` (`ResolveDeferred`, from
  `:294`) and `:439` (`EvaluateEgress`, from `:375`), each
  `if rule.Match.ICMP != nil || rule.Match.TCPFlags != nil { continue }` inside
  the non-Accept branch. `tupleMatches` (`:620`) stays 5-tuple-only; forward
  evaluation keeps its own flag/ICMP checks.
- The property is enumerated by
  `TestStatefulReverseMatchRuleEnumeration`
  (`src/common/netsim/vswitch/filter/filter_test.go:597`) over both loops: plain
  accept admits; a pure-5-tuple drop before an accept shadows (drops); a
  flag-qualified accept admits; a flag-qualified drop before an accept does
  **not** shadow (admits). Case two fails if the loop skips every non-Accept
  rule; case four fails if it reverts to plain break.

## What it does not cover

A reverse-match that could reconstruct the missing field (synthesize the
forward packet's connection-initiating flags and run full flag-aware matching)
would decide these rules rather than skip them. netsim chose the 5-tuple-only
reading its direction record fixes; a model that reconstructs flags is a
different decision, not covered here.
