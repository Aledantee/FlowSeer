---
title: A Reflection Perturbation Gate Must Copy Unexported-Field Structs Whole and Assert the Change It Induced
date: 2026-09-18
category: conventions
module: src/common/netsim/internal/netsimtest
problem_type: convention
component: coverage-gate
severity: high
applies_when:
  - "Writing or reviewing a reflection-driven coverage or property gate that deep-copies a value, perturbs one leaf, and asserts a checker (a Diff, a validator) reacted."
  - "Deep-copying a struct with unexported fields through reflection, where the copy is compared against the original."
  - "Asserting that a Diff or validator 'reported a change' without checking the change names the leaf you perturbed."
related_components: [netsim, verify-change, conformance-gates]
tags: [reflection, deep-copy, silent-failure, coverage-gate, go-test]
---

A coverage gate that perturbs one field at a time and asserts the checker under
test reports a change proves nothing unless two things hold: the only thing that
changed between the two values is the field you perturbed, and the assertion
reads the change you induced rather than any change at all. The netsim
diff-coverage gate missed both and passed every vswitch field vacuously,
including one whose `Diff` arm was genuinely absent.

## The two traps

**A field-by-field reflection copy silently empties unexported-field structs.**
`reflect` cannot set an unexported field, so a deep copy that rebuilds a struct
field by field skips them and returns the type with those fields zeroed.
`vswitch.Config.Ports` is a `port.Table` whose only fields are unexported
(`src/common/netsim/vswitch/port/port.go:196`):

```go
type Table struct {
	ports  []Port
	byName map[string]Port
}
```

So the perturbed copy carried an empty port table, `vswitch.Diff` called
`port.Diff(2 ports, 0 ports)`, and that emitted a "removed port" change for
*every* leaf — an always-present difference unrelated to whatever leaf was
perturbed.

**Asserting "some change fired" masks a missing arm.** `checkLeaf` asserted only
`len(changes) == 0` fails (`src/common/netsim/internal/netsimtest/diffcoverage.go:493`).
With the spurious port delta always present, every leaf passed — including
`.MAC`, whose arm `vswitch.Diff` did not have. The gate that exists to catch a
missing arm could not catch one.

## The rule

Copy a struct that has any unexported field *whole*, then re-copy each exported
field for its own storage, because the walk still descends into exported fields
and an aliased exported pointer, slice, or map would be mutated through when a
leaf beneath it is perturbed
(`src/common/netsim/internal/netsimtest/diffcoverage.go:260`):

```go
if hasUnexportedField(t) {
	cp.Set(v)
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).IsExported() {
			cp.Field(i).Set(deepCopyValue(v.Field(i)))
		}
	}
	return cp
}
```

Then prove the gate can still fail: `TestCheckLeafFailsForAVswitchLeafWithNoDiffArm`
(`src/common/netsim/internal/netsimtest/diffcoverage_test.go:71`) runs the real
`vswitch.Diff` with its `mac` arm removed and asserts `checkLeaf(.MAC, …)`
returns an error. It fails against the pre-fix copy (the spurious delta made it
pass) and passes with the fix. A perturbation gate lands with a case that fails
when the property it guards is broken, never green against a tree that only
happens to pass.

## What it does not cover

The whole-copy removes the one always-firing spurious change that made the gate
vacuous; it does not make `len(changes) != 0` sound. Asserting *some* change
fired still lets any future always-present arm mask a missing one. Closing the
class means asserting a change whose subject and field name the perturbed leaf,
which needs a uniform leaf-path→(subject, field) mapping across the diffs — the
capability arms carry an empty field today, so that is a design change, tracked
as an open question on the netsim analysis-completeness plan, not this rule.

This is the reflection-table sibling of
[a gate selected by name stops running silently](./a-gate-selected-by-name-stops-running-silently.md)
(enumerate and count what ran) and
[a slot that carries two roles](../architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md)
(a classification table must be read back, not only recorded); it shares the
failure shape of
[a diff that flags everything looks like it is working](./same-typed-metadata-maps-can-carry-different-vocabularies.md).
