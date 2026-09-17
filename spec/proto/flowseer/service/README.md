# Service Mailbox and Runtime Records

## Identity

The `service/` root holds process-local runtime messages, durable service
mailbox envelopes, and broker reconciliation records. The root is renamed
`runtime/` in a later phase so its name does not suggest Connect RPC services.

## Admission

A package belongs in `service/` if it defines runtime mailbox envelopes or
broker reconciliation records for host processes. `service/v1` passes because
`Message` is the local mailbox envelope. Connect RPC services fail admission and
belong in `api/` or `edge/`.

## Boundaries

The `service/` root sits outside the import order as a process-local contract.
It imports nothing FlowSeer-owned and is imported by no boundary package.

## Packages

- `v1/`: Durable service mailbox messages, runtime manifests, and broker reconciliation records.
