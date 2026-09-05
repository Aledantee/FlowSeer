# Device API

The `flowseer.api.device.v1` package is the Connect service an operator or a
workflow calls to read and change one device now. It speaks in capabilities:
an interface, a description, a mutation and its phase. Which integration
carries the call, which edge executes it, and whether SNMP or SSH answered
are decided behind the service and reported back as provenance, never chosen
by the caller. The
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
fixes that split, and
[`device/access/v1`](../../../device/access/v1/README.md) holds the messages
the RPCs exchange.

## Reading

`ReadInterface` takes a device ref and the interface name the device spells
and returns one `InterfaceObservation`. The answering edge chooses the
lowest-cost route that can answer completely, SNMP first when nothing is
known yet. When an SNMP read comes back valid but missing a field the
capability needs, the edge falls through to SSH inside the same call and
returns that result alone; the observation's `Provenance` says which
protocol produced it. A partial observation is never returned as the answer.

## Writing

`ApplyInterfaceDescription` records a `MutationIntent` whose change is an
`interface_description` and admits it to the device's lane. The response
carries the `MutationState` as admitted, with its sequence, and the call
returns there. The caller then polls `GetDeviceAccessStatus`, whose
`unresolved` field carries the same mutation until it reaches `RELEASED`,
and whose `interfaces` rows carry the observation that verified it. A
verified result is the read-back: the service compares a fresh observation
with the intent, so the API states no atomicity level and needs no diff
field.

With `validate_only` set, the intent is checked against the device's policy,
its firmware epoch, and the state of its lane, and nothing is recorded; the
response carries no mutation. The same intent submitted again with the same
`idempotency_key` returns the mutation already recorded.

`AbandonMutation` ends recovery of a mutation whose effect could not be
established. The mutation becomes `ABANDONED` with a `RECOVERY_HOLD` block,
and the next full read of the device becomes the observed recovery state.
`ResolveDesynchronization` clears that hold, or a `DESYNCHRONIZED` block
found by an ordinary read: `accept` adopts the observed state as the new
expectation and admits nothing, `restore` admits a reconciliation intent
that puts the expected state back, and `replace` admits the new intent it
carries. Under `AUTHORITATIVE` management the service restores on its own
and this call is only needed after an abandonment.

## What is deliberately absent

- A protocol, a path, or a raw command on any request. Raw access is a
  diagnostics concern and will not land in this service.
- A guarantee level on the apply response. Semantic verification through a
  fresh read replaces it.
- Secrets. Credentials reach an edge over its own authenticated channel and
  appear in no message here.
- A tenant. Scope is ambient, from the authenticated request.
- Streaming. A mutation that takes time is followed through status, not a
  stream.
