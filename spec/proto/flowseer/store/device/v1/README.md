# Device service storage

The `flowseer.store.device.v1` package holds the device service's own files:
the records it writes and the operator-written prototext it reads. The lane
record per device lives in the `device-lanes` key-value bucket and the edge
record in the `edges` bucket; the registry and the service's deployment
configuration are files an operator writes and the service reads at start.
Nothing outside the service reads any of them. They live under `spec/proto`
because every message FlowSeer persists or parses needs a schema someone can
read in five years, and they sit under their own `store` root so the layering
table can say they import boundary packages and are imported by none.

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
`flowseer.model.credential.v1` describes, never here.

## The service configuration

`DeviceServiceConfig` is what one deployment of the service is: the directory
it owns, the files it reads, the two addresses it binds, what an edge is told
when it enrolls, and where telemetry goes. Every interval is optional and
documents the default it falls back to, so a working file is short:

```prototext
state_dir: "/var/lib/flowseer/device"
registry_path: "/etc/flowseer/registry.textproto"
credential_root: "/etc/flowseer/credentials"
listeners {
  api: "0.0.0.0:8443"
  bus: "0.0.0.0:8444"
}
edges {
  central_url: "https://central.example.test"
  assertion_audience: "flowseer-device-central"
  cluster_urls: "wss://central.example.test:8444"
}
telemetry { endpoint: "https://collector.example.test" }
```

That file names no certificate, so the service generates a self-signed pair
into `state_dir` on first start — creating the directory if it is not there
— and prints the digest an edge pins. A deployment with its own chain names
`certificate_file` and `private_key_file` instead, and the two are named
together or not at all.

`log_level` is unset above, which is `INFO`. Raise it to `LOG_LEVEL_DEBUG`
to get the reason behind every refused call: the request interceptor grades
refusals at DEBUG precisely so an incident can turn them on, and before this
field existed there was no way to.

## What is deliberately absent

- A triad or a ref pair for any message here. A record is written and read
  by one service, and its configuration is read by the one process it
  configures; nothing observes or configures either from outside.
- Secrets, with one named exception. The registry names credential versions
  and the lane record holds observations and expectations; the only field
  here that can carry one is `ServiceTelemetry.headers`, which says so, and a
  deployment that puts a token there is choosing to treat the configuration
  file as a secret.
- A tenant. Scope is ambient, and the bucket a record lives in is per
  deployment.
