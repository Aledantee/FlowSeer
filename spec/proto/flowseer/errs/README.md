# Error Wire Payload

## Identity

The `errs/` root holds the canonical error wire payload used across FlowSeer
process boundaries, translating `src/common/errs` Go errors into structured
messages that cross Connect RPC and message brokers.

## Admission

A package belongs in `errs/` if it defines cross-cutting error representations
for RPC and messaging envelopes. `errs/v1` passes because `ErrorPayload` is the
shared wire error contract. An entity-specific error detail message fails
admission and belongs with that entity in `model/`.

## Boundaries

The `errs/` root is a leaf: it imports nothing FlowSeer-owned. Any service,
integration, event, or storage root may import `errs/`.

## Packages

- `v1/`: Canonical `ErrorPayload` wire format supporting recursive causal chains and client-safe attribute projection.
