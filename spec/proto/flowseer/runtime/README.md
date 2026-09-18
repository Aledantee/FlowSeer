# Service Mailbox and Runtime Records

## Identity

The `runtime/` root holds process-local runtime messages, durable mailbox
envelopes, and broker reconciliation records.

## Admission

A package belongs in `runtime/` if it defines runtime mailbox envelopes or
broker reconciliation records for host processes. `runtime/v1` passes because
`Message` is the local mailbox envelope. Connect RPC services fail admission and
belong in `api/` or `edge/`.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: nothing

The `runtime/` root sits outside the import order as a process-local contract,
so `test/conformance/proto/layering_test.go` holds it to importing nothing
FlowSeer-owned rather than to a row of the import table.

## Packages

- `v1/`: Durable service mailbox messages, runtime manifests, and broker reconciliation records.
