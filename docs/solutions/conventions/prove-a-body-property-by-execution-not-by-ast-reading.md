---
title: A Static Scan Cannot Decide What a Function Does With Its Arguments; Prove That By Execution
date: 2026-09-15
category: conventions
module: src/protocol/smi/internal/diag
problem_type: convention
component: conformance-gates
severity: high
applies_when:
  - "Writing a go/ast scan that must decide something about a function body rather than about a call expression: that a wrapper forwards its arguments unchanged, that a handler always closes what it opened, that a branch is unreachable."
  - "Exempting a call from a static check because an enclosing function is 'known' to behave a certain way."
  - "A conformance scan reports zero findings and you are deciding whether that means the property holds or that the scan cannot see the violation."
  - "Reviewing a second or third narrowing of the same syntactic predicate after each earlier one turned out to miss a shape."
related_components: [smi-parser, testing]
tags: [go-ast, conformance-gate, static-analysis, proof-obligation, test-design]
---

A scan over `go/ast` decides call expressions well and function bodies badly.
The argument count at a call site is in the syntax. What a body does with a
parameter before passing it on is data flow, and every syntactic approximation
of it admits a shape its author did not think of.

`diag.MustRaise` panics on an uncataloged code or an argument count its catalog
row does not declare. The claim that neither panic is reachable rests on
resolving every first-party call to a row. Five variadic forwarders sit between
the callers and `MustRaise`, each taking a runtime `errs.Code` and a spread
`args...` that no scan can read, so the scan resolves the forwarders' callers
instead and exempts the forwarders' own spread calls. That exemption is the
proof obligation, and two attempts to discharge it by reading failed:

- Exempting any spread inside a registered forwarder. A body that substituted a
  constant code, or appended to `args` first, passed.
- Requiring the spread's arguments to be identifiers whose names match the
  declaration's parameters. `args = append(args, diag.ArgInt(1))` rebinds `args`
  and keeps the name, so it passed too.

Both were found by review rather than by the gate, which is the point: a gate
whose predicate is wrong reports zero findings and looks exactly like a gate
whose predicate is right.

## The rule

Split the obligation. Let the scan decide what is in the syntax, and let a test
decide what is in the behavior by calling the thing and looking at what comes
out.

The forwarding property is now one test per forwarder package, driven over the
whole catalog rather than a sample:

```go
func TestRaiseForwardsCodeAndArgsUnchanged(t *testing.T) {
	for _, row := range catalog.Entries() {
		t.Run(row.Code, func(t *testing.T) {
			code := errs.Code(row.Code)
			args := make([]diag.Arg, row.Arity)
			for i := range args {
				args[i] = diag.ArgInt(i + 1)
			}
			// … call the forwarder, then compare the diagnostic it
			// appended against diag.MustRaise(pos, code, args...)
		})
	}
}
```

Two details carry the weight. Comparing the whole `Diagnostic` value against one
built by `MustRaise` from the same inputs is not circular: `Diagnostic`'s fields
are unexported, so no forwarder can produce one except through `MustRaise`, and
`MustRaise` is injective in its inputs, so equality holds exactly when the
forwarder passed those inputs through. And driving every catalog row rather than
one code matters because the obvious sample is the wrong one: each forwarder's
own cap branch hardcodes `ErrCodeLimitExceeded` a few lines above, so a body that
ignored `code` and substituted that constant passed a single-code test.

`spreadFinding` (`src/protocol/smi/internal/diag/arity_scan_test.go:504-518`)
records the division and why it exists, so the next reader does not attempt a
third narrowing. What the scan still decides about a forwarder is that the
registry's parameter indices match the declaration, which no runtime test would
notice, since a caller resolved at the wrong index is checked against the wrong
row.

## The dependency has to be checked, not just written down

An exemption justified by a test is only as good as that test existing. Prose
saying so is not a check: delete the test, or constrain its file out of the
build, and the scan stays green over an empty proof. `scanCoverage`
(`arity_scan_test.go:565-576`) therefore requires each forwarder's test to be
declared in its package, and `parseForwarderTests` (`:887`) filters candidate
files through `build.Default.MatchFile`, because `parser.ParseFile` ignores build
constraints and would count a `//go:build ignore` file as proof.

## What this does not cover

The name check cannot see a test that runs and proves nothing: an emptied body, a
`t.Skip`, an early return under `testing.Short()`. Those stay a review catch, and
the comment says so rather than implying more. `build.Default` also carries no
build tags, so a proof file behind `//go:build !race` would be miscounted; no file
in the tree does that today.
