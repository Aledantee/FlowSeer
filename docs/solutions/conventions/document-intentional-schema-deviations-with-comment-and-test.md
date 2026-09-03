---
title: Documenting Intentional Protobuf Validation Deviations (Comment + Conformance Test)
date: 2026-08-30
category: conventions
module: spec/proto/flowseer/api/inventory/v1
problem_type: convention
component: data_model
severity: medium
applies_when:
  - "a schema field's validation rule deliberately deviates from the package-wide pattern (e.g. an enum rule that omits defined_only where every sibling rule pairs it with not_in)"
  - "reviewing a proto or schema diff where a validation rule looks inconsistent with its neighbors"
  - "designing forward/backward compatibility for enum or repeated-field validation across reader/writer versions"
related_components:
  - api_layer
  - testing_framework
  - documentation
tags: [protovalidate, proto-conventions, forward-compatibility, conformance-test, code-review, schema-comments, defined-only, inventory]
---

# Documenting Intentional Protobuf Validation Deviations (Comment + Conformance Test)

## Context

During a multi-source code review of the inventory protobuf package
(`spec/proto/flowseer/api/inventory/v1/`), reviewers flagged the enum
validation rule on `CapabilitySet.capabilities`
(`spec/proto/flowseer/api/inventory/v1/capability.proto:30-37`). Every other
enum field rule in the package pairs `defined_only: true` with `not_in: [0]`
— for example `ManagementProtocol protocol` and `BindingStatus status` in
`spec/proto/flowseer/api/inventory/v1/binding.proto:89-95` and
`binding.proto:121-127`. The `capabilities` field's `items.enum` block
carries only `not_in: [0]`, with no `defined_only`. Against the rest of the
package this reads as an omission — "it accepts undefined values like 99,
that must be a missed rule" — and the review proposed adding
`defined_only: true` to bring it in line.

Applying that edit broke an existing conformance test:
`TestInventoryCapabilityRules/unknown_nonzero_capabilities_remain_valid` in
`test/conformance/api_inventory_rules_test.go:36-42`, which
constructs a `CapabilitySet` holding `inventoryv1.Capability(99)` — a value
with no defined enum entry — and pins `wantValid: true`. The looseness was
not a gap; it was deliberate forward compatibility: capabilities are
device-reported, and an older core must tolerate kinds it does not know.
The resolution (commit "fix(review): enforce declared inventory invariants
in schema and prose") reverted the `defined_only` addition and instead put
the contract into the field's schema comment, which now reads
(`capability.proto:27-29`):

> Supported capability areas. An empty list means the binding exposes
> none. Unknown non-zero values stay valid so a newer writer's
> capability passes an older reader.

This was the second time the trap fired in one day. (session history) A
schema-validations session earlier the same morning, while adding
`defined_only` to the package's other enum rules, pattern-matched the same
"missing rule", discovered the pinned test before editing, and recorded the
exception — but only in the commit message of "feat(inventory): move
cross-field and range rules into the schema": "CapabilitySet keeps
accepting unknown capabilities: they are device-reported, and an older
core must tolerate kinds it does not know." Hours later, the review
re-flagged the same omission, because reviewers read the schema, not git
history. The test existed and caught the wrong edit both times; nothing at
the point of edit said the looseness was intentional until the comment
landed.

## Guidance

When a schema field's validation rule deliberately deviates from a
package-wide pattern, close the gap in two layers, not one:

1. **State the deviation and its reason in the field's schema comment**, in
   contract phrasing (what the field guarantees), not decision-rationale
   phrasing (why we chose it). The comment describes behavior a caller can
   rely on, not the history of a review thread.

   ```protobuf
   // capability.proto — the deliberately loose enum rule, documented on the field
   message CapabilitySet {
     // Supported capability areas. An empty list means the binding exposes
     // none. Unknown non-zero values stay valid so a newer writer's
     // capability passes an older reader.
     repeated Capability capabilities = 1 [(buf.validate.field).repeated = {
       unique: true
       items: {
         enum: {
           not_in: [0]
         }
       }
     }];
   }
   ```

2. **Pin the loose behavior with an explicit conformance test case whose
   name says the behavior is wanted**, not just that it is tolerated:

   ```go
   // test/conformance/api_inventory_rules_test.go:36-42
   {
       name: "unknown nonzero capabilities remain valid",
       message: inventoryv1.CapabilitySet_builder{
           Capabilities: []inventoryv1.Capability{
               inventoryv1.Capability(99),
           },
       }.Build(),
       wantValid: true,
   },
   ```

   A name like "capabilities accept any nonzero value" would describe the
   mechanism instead of the intent, and would read just as easily as an
   artifact of an unfinished rule. Name the test after the guarantee, so a
   future reader — human or agent — sees that the test asserts a contract,
   not merely records current behavior.

Both layers matter, and neither substitutes for the other. The comment is
the cheap guard: a reviewer scanning the `.proto` file sees the reason
before proposing the "fix". The test is the hard guard: even when the
comment is skipped, a change that reintroduces `defined_only` fails the
build loudly, with a test name that states which behavior it protects. A
commit message is not a third layer — the rationale lived in one for half
a day here and prevented nothing, because nobody re-making the decision
reads history first.

## Why This Matters

Deliberate validation looseness is invisible intent. A validation rule that
is *stricter* than its neighbors announces itself — a reviewer notices new
constraints and asks why. A rule that is *looser* than its neighbors looks
like an absence, and absences read as omissions by default, especially
against a consistent package-wide pattern like `defined_only` +
`not_in: [0]` on every other enum field. The asymmetry means loose rules
must justify themselves explicitly; strict rules mostly need not.

The mechanism worked on one axis and failed on the other: the pinned test
caught the incorrect edit before it landed, so no regression shipped. But
the trap still fired twice in one day — once stopped by the test being
discovered pre-edit, once by the test failing post-edit — and each firing
cost an investigation cycle. The schema comment ends the cycle by making
the deviation visible at the exact point someone is about to "fix" it.

## When to Apply

Apply this pattern whenever a schema field's validation, defaulting, or
constraint diverges from the pattern set by sibling fields in the same
message or package — not only for enum `defined_only`/`not_in` rules.
Signs that a field needs both layers:

- The field's rule is visibly less strict than the same rule on comparable
  fields elsewhere in the package (a missing `not_in`, a missing
  `required`, a wider numeric range, no uniqueness constraint where
  siblings have one).
- The looseness exists for a reason not obvious from the type system alone
  — forward/backward compatibility, a known external producer that emits
  looser data, a phased rollout, a legacy caller that cannot change yet.
- A future reviewer (including an automated review pass or an agent) is
  likely to pattern-match against sibling fields and propose tightening
  the rule to match.

Skip the extra ceremony when the deviation is self-evidently correct from
context alone — the two-layer treatment is for cases where "this looks
like a bug" is a plausible reading.

## Examples

**Before** — the deviation exists in the rule but is invisible in the
comment, so it reads as a missed validation:

```protobuf
// spec/proto/flowseer/api/inventory/v1/capability.proto (prior state)
message CapabilitySet {
  // Supported capability areas.
  repeated Capability capabilities = 1 [(buf.validate.field).repeated = {
    unique: true
    items: {
      enum: {
        not_in: [0]
      }
    }
  }];
}
```

Compare the strict pattern used elsewhere in the same package,
`ManagementProtocol protocol` in
`spec/proto/flowseer/api/inventory/v1/binding.proto:89-95`:

```protobuf
// The management protocol spoken at the endpoint. Must be present; the
// zero value is rejected.
ManagementProtocol protocol = 3 [
  (buf.validate.field).required = true,
  (buf.validate.field).enum = {
    defined_only: true
    not_in: [0]
  }
];
```

Set side by side, `capabilities` looks like an unfinished version of
`protocol` — same package, same enum-rule shape, one field missing
`defined_only`. Adding it broke
`TestInventoryCapabilityRules/unknown_nonzero_capabilities_remain_valid`
(`test/conformance/api_inventory_rules_test.go:36-42`).

**After** — the comment states the contract next to the loose rule, so the
deviation reads as intentional:

```protobuf
// spec/proto/flowseer/api/inventory/v1/capability.proto (current tree)
message CapabilitySet {
  // Supported capability areas. An empty list means the binding exposes
  // none. Unknown non-zero values stay valid so a newer writer's
  // capability passes an older reader.
  repeated Capability capabilities = 1 [(buf.validate.field).repeated = {
    unique: true
    items: {
      enum: {
        not_in: [0]
      }
    }
  }];
}
```

Now a reviewer — or an agent doing an automated pattern-match pass —
reading `capability.proto` alone sees why this field differs from
`protocol` and `status`, without reconstructing the reasoning from a test
failure, a commit message, or a review thread.

## Related

- `docs/code-style-proto.md` — states the comment-the-deviation norm for
  `required` omission ("omitting `required` is how you spell an optional
  field ... State which it is in the field comment") and shows
  `defined_only` as the default enum pattern the package deviates from; it
  also places executable schema tests in `test/conformance/`.
- `docs/conventions/protobuf.md` — the same disclose-the-deliberate-absence
  move at the triad-family level ("the hook reports every missing member
  and cannot tell deliberate from forgotten, so the comment is what lets
  the next reader tell").
- `docs/solutions/architecture-patterns/snmp-collection-library-architecture-and-fast-path-conventions.md`
  — same pattern in a different subsystem: intentional deviations from
  SNMP device behavior pinned in an executable conformance corpus.
