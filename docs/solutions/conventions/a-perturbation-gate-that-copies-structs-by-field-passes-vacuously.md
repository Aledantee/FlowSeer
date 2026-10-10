---
title: A Reflection Perturbation Gate Must Copy Unexported-Field Structs Whole and Assert the Change It Induced
date: 2026-09-18
last_verified: 2026-10-10
category: conventions
module: src/common/sim/internal/simtest
problem_type: convention
component: coverage-gate
severity: high
applies_when:
  - "Writing or reviewing a reflection-driven coverage or property gate that deep-copies a value, perturbs one leaf, and asserts a checker (a Diff, a validator) reacted."
  - "Deep-copying a struct with unexported fields through reflection, where the copy is compared against the original."
  - "Asserting that a Diff or validator 'reported a change' without checking the change names the leaf you perturbed."
  - "Specifying diff expectations when normalization erases a submitted change or derives several effective changes from one leaf."
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
(`src/common/sim/port/port.go:172`):

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
`len(changes) == 0` fails (the pre-mapping source at
`4944caed:src/common/sim/internal/simtest/diffcoverage.go:490-492`).
With the spurious port delta always present, every leaf passed — including
`.MAC`, whose arm `vswitch.Diff` did not have. The gate that exists to catch a
missing arm could not catch one.

## The rule

Copy a struct that has any unexported field *whole*, then re-copy each exported
field for its own storage, because the walk still descends into exported fields
and an aliased exported pointer, slice, or map would be mutated through when a
leaf beneath it is perturbed
(`src/common/sim/internal/simtest/diffcoverage.go:260`):

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
(`src/common/sim/internal/simtest/diffcoverage_test.go:458-500`) runs the real
`vswitch.Diff` with its `mac` arm removed and asserts `checkLeaf(.MAC, …)`
returns an error. The current test also requires the full producer to match its
independent MAC expectation. A perturbation gate needs a case that rejects a
broken producer.

## Specify the complete independent sequence

The [simulation direction](../../architecture/2026-10-01-simulation-package-shape-direction.md)
now requires complete ordered sequences. Derive each expectation from the
configuration types and the mapping table, before calling `Diff`. Checking only
one matching record still admits unrelated extras. `checkMappedLeaf` obtains
its expectation independently (`src/common/sim/internal/simtest/diffcoverage.go:860-864`):

```go
want, err := m.expectation(e.Path, hits)
if err != nil {
    return err
}
got := m.diff(m.normalize(a), m.normalize(b))
```

One submitted leaf can produce zero or several effective changes. With a zero
recovery duration, loop protection derives `3 * interval`
(`src/common/sim/layer/loopprotect/config.go:108-111,129-135`). Changing interval
from 1s to 2s therefore requires interval and recovery-duration records.
An erased mutation requires an empty sequence. The gate fixtures exercise
both shapes (`src/common/sim/internal/simtest/diffcoverage_test.go:173-202`):

```go
{".Noop", nil},
{".Interval", []changeRecord{{Field: "interval", From: &[2]string{"diffcoverage.fixture", "1"}, To: &[2]string{"diffcoverage.fixture", "2"}}, {Field: "derived", From: &[2]string{"diffcoverage.fixture", "3"}, To: &[2]string{"diffcoverage.fixture", "6"}}}},
```

`TestMappedChangesExactSequence` rejects wrong locations, mistyped facts,
extras and reordering (`src/common/sim/internal/simtest/diffcoverage_test.go:268-309`).
The matcher checks sequence length and every record's layer, subject kind,
key, field and typed From/To facts
(`src/common/sim/internal/simtest/diffcoverage.go:874-891`).

## What it does not cover

Exact matching proves the submitted fixture's attribution. Structural seed
coverage and literal concrete-expectation counts remain separate checks
(`src/common/sim/internal/simtest/diffcoverage.go:481-497`). Use the
[count-guard solution](./a-combinatorial-table-count-guard-must-assert-a-literal.md)
when checking that every oracle branch ran.

This is the reflection-table sibling of
[a gate selected by name stops running silently](./a-gate-selected-by-name-stops-running-silently.md)
(enumerate and count what ran) and
[a slot that carries two roles](../architecture-patterns/one-slot-two-roles-is-a-defect-class-not-a-defect.md)
(a classification table must be read back, not only recorded); it shares the
failure shape of
[a diff that flags everything looks like it is working](./same-typed-metadata-maps-can-carry-different-vocabularies.md).
