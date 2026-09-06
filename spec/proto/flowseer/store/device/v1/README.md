# Device service storage

The `flowseer.store.device.v1` package holds what the device service writes
to its own stores: the lane record per device in the `device-lanes`
key-value bucket, the edge record in the `edges` bucket, and the registry it
reads from an operator-written prototext file. Nothing outside the service
reads these messages. They live under `spec/proto` because every message
FlowSeer persists needs a schema someone can read in five years, and they
sit under their own `store` root so the layering table can say they import
boundary packages and are imported by none.

## The lane record

`DeviceLaneRecord` is what central derives its outbox from. Every field the
outbox reads is durable, so what central owes an edge is a function of the
record alone, on any replica, after any restart:

```prototext
device { device { id: "0192e6a0-0000-7000-8000-0000000000d1" } }
high_watermark: 7
mutation {
  intent {
    device { device { id: "0192e6a0-0000-7000-8000-0000000000d1" } }
    idempotency_key: "0192e6a0-0000-7000-8000-00000000a001"
    actor { operator { subject: "zitadel|2837" } }
    access_policy { key: "icx7150-lab" version: 3 }
    expected_firmware_fingerprint: "ICX7150-24P SPS10010g"
    interface_description { interface_name: "ethernet 1/1/1" description: "uplink to core" }
  }
  sequence: 7
  phase: OPERATION_PHASE_POSSIBLY_APPLIED
  responsible_edge { edge { id: "0192e6a0-0000-7000-8000-0000000000ed" } }
}
admitted_at { seconds: 1788700000 }
dispatched: true
dispatch_confirmed: true
last_reported_phase: OPERATION_PHASE_ADMITTED
```

This record owes the edge a `CheckpointRequest` and nothing else
(`checkpoint_confirmed` is unset, which is false). The
`dispatched` bit and the two confirmations differ in lifetime: an edge that
restarts loses its lanes, so central clears the confirmations when the edge
reports onboarded and re-derives what to send, while `dispatched` records
that the edge once held the sequence and survives, since it decides whether
a terminal acknowledgement is owed at all. `src/services/device/README.md`
carries the full owed-row table.

Reads draw sequences from the same counter as mutations, so the lane has
one order, and each read in flight is an `OpenRead` keyed by interface. A
closed read keeps its outcome in the entry until the next write removes it,
which is how a waiter on another replica learns what the read returned.

## The registry

`DeviceRegistry` is one integration, its edge, the devices it serves, and
the policies their handles resolve to. It is read once at start and
validated before anything else runs. A policy names credential versions and
a host-key pin; the credential material itself lives in the mounted files
`flowseer.device.credential.v1` describes, never here.

## What is deliberately absent

- A triad or a ref pair for any message here. A record is written and read
  by one service; nothing observes or configures it.
- Secrets. The registry names credential versions; the lane record holds
  observations and expectations.
- A tenant. Scope is ambient, and the bucket a record lives in is per
  deployment.
