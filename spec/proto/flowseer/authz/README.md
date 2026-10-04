# Authorization Rule Schema

## Identity

The `authz/` root holds authorization rule schema definitions used to declare
authorization policies on operator-facing Connect RPC services across FlowSeer.

## Admission

A package belongs in `authz/` if it defines cross-cutting authorization rule
options and related metadata for RPC declarations. `authz/v1` passes because
`Rule` is the shared method option for authorization enforcement. An
entity-specific permission or policy model belongs with that entity in `model/`.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: api/capture, api/device, api/edge, api/identity

The `authz/` root is a leaf: it imports nothing FlowSeer-owned. Packages under
`api/` may import `authz/`.

## Packages

- `v1/`: Authorization rule option and enforcement mode definitions for RPC method declarations.
