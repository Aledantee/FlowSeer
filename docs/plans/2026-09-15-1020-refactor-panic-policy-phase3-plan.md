---
title: Panic Policy Phase 3 - Enforcement - Plan
type: chore
date: 2026-09-15
artifact_contract: flowseer-plan/v1
artifact_readiness: needs-decisions
status: planned
execution: code
parent: docs/plans/2026-09-15-1020-refactor-panic-policy-plan.md
---

# Panic Policy Phase 3 - Enforcement - Plan

> Re-planned by plan when its turn comes; the tree will have moved.

## Goal

A repository check fails when a panic appears outside the sanctioned shapes,
so the rule holds without a reviewer having to remember it. The means: a
Go-AST check registered where the repository's other layout checks run.

This phase is wrong if the documentation half of the rule cannot be checked
mechanically at acceptable cost. In that case the check enforces the placement
and reachability halves, the documentation half stays a review rule, and this
plan says so rather than shipping a check that quietly covers less than the
rule it claims to enforce.

The goroutine half needs no call graph — a bare `go` statement is visible in a
single file's AST, which is why it is the half worth building first. The
first-party-under-a-boundary half is the hard one: `callOwned` takes a `Runner`
interface (`src/common/service/supervisor.go:707`), so whether a given function
runs under a recover is a runtime property, and a static check may only be able
to approximate it by package. If it can only approximate, `docs/code-style.md`
says so.

## Decisions

The parent's Decisions govern. Two are specific to this phase, and both are
provisional:

- The check is a Go AST pass, not a grep. Why: the rule is about the function
  enclosing a `panic` and about doc comments on callers, neither of which a
  line-oriented pattern can see. `src/common/errs/code_test.go:300-320`
  already walks the AST for the code-uniqueness gate and is the model.
- `tools/hooks/` is a policy surface under `AGENTS.md`. Whatever this phase
  produces is staged as a diff for a person to review, and an agent does not
  commit it. Why: `AGENTS.md` says so, and a check that gates every future
  change is exactly the kind of thing that rule exists for.

## Requirements

Requirement 14 of the parent applies. The three halves below are
separable and they are listed in the order they are worth having — the third
is the one that may not survive the cost check:

1. The check fails on a `panic` whose enclosing function is not
   `Must`/`must`-prefixed and whose doc comment does not name a benchmark.
   Acceptance: adding `panic("x")` to a plain function makes the check exit
   non-zero naming the file and line; adding it to `mustFoo` does not.
2. The check fails on a first-party panic that names a `src/common/service`
   recover as its handler, and on a bare `go` statement in non-test `src/`.
   Acceptance: a `must`-prefixed helper panicking inside a NATS handler exits
   non-zero even though `callHandler` catches it; a `go func() { … }()` outside
   phase 4's helper exits non-zero; the same panic in a package-level `var`
   block does not, and neither do the generated MIB calls phase 1 U11 makes
   provably valid.
3. The check fails on a function that calls a panicking function, does not
   recover, and does not document the panic. Acceptance: a new function
   calling `snmp.MustOID` with a doc comment that says nothing about panicking
   makes the check exit non-zero.

## Out of scope

- `_test.go` files.
- Transitive obligation across module boundaries. The check reasons about
  `go.aledante.io/FlowSeer` source only; a panic from a dependency is that
  dependency's contract.

## Units

To be written when this phase is re-planned. The open question below decides
the unit count.

## Verification

```bash
tools/hooks/panic-policy.sh
tools/hooks/tests/run.sh
.claude/skills/verify-change/scripts/verify-change.sh -- tools/hooks
```

`tools/` holds no Go files; its tests are the shell harness at
`tools/hooks/tests/run.sh`. A check written as a Go program lives under
`src/`, not `tools/`, and this phase says which it chose.

The check is proved against the tree it was written for: it must report zero
findings on the tree phases 1 and 2 leave behind, and must report a finding
for each of the acceptance examples above, added to a scratch file and
removed.

## Definition of done

- [ ] The check reports zero findings on the post-phase-2 tree.
- [ ] Each acceptance example produces a finding.
- [ ] The `tools/hooks/` diff staged for guardrail review and not committed by
      an agent, with a note of what it enforces and what it does not.
- [ ] The registration that arms the check is staged the same way:
      `.claude/settings.json` (the Stop entry at line 105) and
      `.codex/hooks.json` are both policy surfaces under `AGENTS.md`, and a
      hook script nothing registers enforces nothing.
- [ ] `docs/code-style.md` updated if the check enforces less than the rule
      says, so the doc does not promise what nothing verifies.
- [ ] This plan's `status` set, and `U3. Landed:` filled in the parent.

## Open questions

- How the check recognizes a proof. The parent requires it to be a check that
  runs, cited at the call site or in the emitter — but a citation is prose, and
  a check that reads prose is back where requirement 3 is. The likely answer is
  a narrow allowlist of proving stages (mibgen's validated emitter is the only
  one today) rather than a general mechanism, and saying so beats inventing a
  marker comment nothing else uses.
- Requirement 2 is the one worth building first even though the parent lists
  the placement rule first. Reachability to a recover boundary is a graph
  question with a definite answer, and it is the half that catches an actual
  production outage rather than a missing sentence. If only one half ships,
  it is this one.
- Whether requirement 3 — the caller documentation obligation — is
  mechanically checkable at acceptable cost. It needs a call graph over the
  repository plus a judgment about what "documents the panic" means in prose,
  and the second half is the hard one. A cheap approximation is to require the
  caller's doc comment to contain the word `panic`; whether that is worth
  having is the decision this phase opens with.
- Whether the check runs as a Stop hook in `tools/hooks/`, as a case in
  `verify-change.sh`, or as a Go test under `src/common/errs`-style
  conventions. The Stop hook is the parent's recommendation because the rule
  is repo-wide rather than diff-local, but a Go test needs no policy-surface
  review and would land faster.
