---
title: A protovalidate value bound without required is skipped when the field is absent
date: 2026-09-27
last_verified: 2026-09-27
category: conventions
module: spec/proto/flowseer/api/capture/v1/capture_service.proto
problem_type: convention
component: data_model
severity: high
applies_when:
  - "Putting a protovalidate value rule (uint64.gte/gt/lte, string.min_len, and the like) on a singular scalar field to express a mandatory floor or contract"
  - "A message validates even though a field carrying such a bound is unset, or an empty message passes a bound you expected it to fail"
related_components: [service_layer, testing_framework]
tags: [protovalidate, schema-bounds, edition-2024, presence, required]
---

# A protovalidate value bound without required is skipped when the field is absent

## The situation

`TailGap` reports how many chunks a live tail dropped, so `dropped_chunks` is
always at least one — a gap that dropped nothing is not a gap. The first cut
expressed that with a value bound alone:

```protobuf
uint64 dropped_chunks = 1 [(buf.validate.field).uint64.gte = 1];
```

A response carrying `gap: {}` — every field unset — passed validation, and so
did a gap that never set `dropped_chunks`. The `gte = 1` floor did nothing for
the case it was written to catch.

## What is true and why

Under edition 2024 a singular scalar field has explicit presence, so an unset
field is *absent*, not a zero. protovalidate evaluates a field's value rules
only when the field is present; an absent field skips them. `uint64.gte = 1`
therefore rejects `dropped_chunks: 0` (present zero) but says nothing about an
absent `dropped_chunks`, which the getter still reports as `0`.

`required = true` is the separate rule that fails an absent field. A bound that
must always hold — a floor a valid message can never sit below — needs both:
`required` to force presence, and the value rule to bound the present value.

```protobuf
uint64 dropped_chunks = 1 [
  (buf.validate.field).required = true,
  (buf.validate.field).uint64.gte = 1
];
```

Verified 2026-09-27: with the bound alone, a `TailGap{}` inside a
`TailCaptureSessionResponse` validated ("empty gap ACCEPTED"); after adding
`required = true`, the same empty gap and a `dropped_chunks: 0` gap both fail,
while the `dropped_chunks: 1, dropped_packets: 256, ...` example still passes
(`src/services/device/internal/captureapi/operator_service_test.go`,
`TestTailGap_SchemaBoundRejectsEmptyGap`).

## How to apply

- When a value rule states a contract the message must always meet, pair it
  with `required = true`. Comment it as "Must be present" per the required-field
  phrasing rule.
- When zero (or the empty string) is itself a valid, meaningful value, leave the
  field optional: do not add `required`, and do not write a value rule that the
  absent default would violate. `TailGap.dropped_packets` stays optional because
  zero means "only stream markers were dropped".
- Prove it with a test that validates both a valid message and the empty/zero
  case, not only the valid one: the bound is
  [documentation unless a test enforces it](a-schema-bound-on-a-streaming-payload-is-documentation-unless-a-test-enforces-it.md).

## What this does not cover

The message-typed variant case — a `oneof` arm whose payload is `required` so an
empty-but-present submessage is caught by its own field rules — is the
[Typed variants](../../conventions/protobuf.md) convention, not this. This is
about a value rule on a singular scalar being bypassed by absence.
