---
title: A oneof with both arms set is unrepresentable, so a required oneof validates only the empty case
date: 2026-09-26
category: conventions
module: spec/proto/flowseer
problem_type: convention
component: schema
severity: medium
applies_when:
  - "Writing a requirement, test, or CEL rule that a protobuf oneof with more than one arm set should fail validation"
  - "Reviewing a plan requirement phrased as 'a message with both <oneof arms> set fails'"
  - "Specifying a required oneof and its conformance test"
related_components: [test/conformance/proto]
tags: [protobuf, oneof, protovalidate, conformance, edition-2024]
---

A protobuf `oneof` holds at most one arm in memory: setting one member clears
the others, and the generated accessor returns a single active arm or none.
Decoding wire bytes that carry tags for two arms of the same oneof applies
last-tag-wins — the later tag overwrites the earlier and leaves no leftover
field. So a message with "both arms set" cannot be constructed, built, or
unmarshaled; `protovalidate` never sees it.

The consequence for a requirement or test: "a message whose oneof has both arms
set fails validation" is unsatisfiable and cannot be written without also
failing valid messages. `(buf.validate.oneof).required = true` emits its
violation only when `WhichOneof` is nil — the no-arm case. The two enforceable
invariants are therefore:

- **at-most-one** comes free from the `oneof` structure; no rule expresses it,
  and no test can trigger a "too many" failure.
- **at-least-one** is what `required = true` adds; the conformance test asserts
  the empty message fails and each single arm passes.

## How to apply

Write the requirement and its test against the empty case, and record in a
comment that both arms are structurally unrepresentable so a reader does not
add a doomed CEL rule for it later:

```proto
// Exactly one target. A oneof holds at most one arm and wire decoding is
// last-tag-wins, so both arms cannot coexist; required rejects the empty case.
oneof target {
  option (buf.validate.oneof).required = true;
  ForwardingNextHop forwarding = 1;
  SpecialNextHop special = 2;
}
```

```go
// no arm set -> fails at the oneof path
errsAtField(t, &routingv1.NextHop{}, "target", "exactly one field is required")
// exactly one arm -> passes (either arm)
runValidationCases(t, ...single-arm cases..., wantValid: true)
```

Do not assert "both arms set fails": there is no input that reaches it.

## Evidence

- The oneof and its comment: `spec/proto/flowseer/net/routing/v1/next_hop.proto`
  (`NextHop.target`, `(buf.validate.oneof).required = true`).
- The test asserts only the enforceable facts — no-arm fails at path `target`
  with "exactly one field is required", each single arm passes, and wire bytes
  with both tags decode to the last arm:
  `test/conformance/proto/routing_rules_test.go` (`TestNextHopTarget`).
- The same mis-specification appeared twice as a plan requirement
  ("a NextHop with both arms set fails", "an EndpointState whose attachment has
  both arms fails") and was corrected both times to the empty-case invariant;
  the ruling is recorded in
  `docs/plans/2026-09-25-1713-feat-schema-building-blocks-phase2-plan.md`
  (the Decisions section).

## What it does not cover

A `oneof` without `required` allows the empty case too; only add `required`
when at-least-one is the contract. Repeated fields, maps, and independent
optional fields are not oneofs and can genuinely hold conflicting values — a
CEL rule that rejects a bad combination there is real, not vacuous.
