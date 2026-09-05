# Device access values

The `flowseer.device.access.v1` package holds what every device-access
boundary says in the same words: the phases a mutation passes through, its
terminal disposition, who asked for it, the typed intent, and the typed
observation that verifies it. The operator API in `api/device/v1`, the
execution envelope between central and an integration, and the durable audit
event each import this package and none of them imports another, so a phase
means one thing on every wire. The
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
fixes that layout and the rules below.

## One description change, phase by phase

An operator sets the description of `ethernet 1/1/1` on an ICX7150. The
messages that exist at each step:

1. The client builds a `MutationIntent`: the device ref, a fresh UUID as
   `idempotency_key`, an `Actor` with the operator's subject, the
   `AccessPolicyHandle` from the device's config, the firmware fingerprint
   the client last saw, and an `interface_description` arm:

   ```prototext
   interface_name: "ethernet 1/1/1"
   description: "uplink to core"
   ```

2. Central records the intent and answers with a `MutationState` in phase
   `INTENT_RECORDED`, then `ADMITTED` once the device's lane assigned
   `sequence` 42. `responsible_edge` names the edge that will execute it.
3. Before the command is submitted, central checkpoints
   `POSSIBLY_APPLIED`. From here the effect is unknown until a read says
   otherwise, so a lost connection cannot turn into a duplicate command.
4. The edge submits the command, then reads the interface over a fresh
   session: phase `OBSERVING`. The read is an `InterfaceObservation` with
   `completeness: COMPLETE` and a `Provenance` naming the binding, the
   protocol that answered, the edge, and the fingerprint.
5. The observation matches the intent: phase `VERIFIED`, with
   `block_reason: UNACKNOWLEDGED` until central holds the result.
6. Central records `disposition: VERIFIED` and the state moves to
   `ACKNOWLEDGED`, then `RELEASED`. Sequence 43 may now start.

If step 4 had returned an observation that did not match, or none at all,
the state would move to `RECOVERING` with `block_reason: INDETERMINATE`. The
edge observes again before any retry. An authorized cancellation ends that
with phase `ABANDONED`, `disposition: INDETERMINATE_ABANDONED`, and
`block_reason: RECOVERY_HOLD`; the lane stays blocked until an operator
accepts or restores the observed state, or a reconciliation intent
replaces it. Abandoned work is never resumed.

## Rules the messages hold

- A disposition is present exactly in the three terminal phases,
  `ACKNOWLEDGED`, `RELEASED`, and `ABANDONED`. A state in `VERIFIED` with a
  disposition fails validation, as does an abandoned state without one.
- `blocked_since` is present exactly when `block_reason` is.
- An intent pins the policy version and the expected fingerprint. A device
  that reports another fingerprint blocks the intent before any write.
- A description is printable ASCII of at most 64 characters. An empty
  string is a valid intent and clears the description; control characters
  are rejected at the schema so no adapter has to decide what a terminal
  would do with them.
- An observation with `completeness: PARTIAL` is evidence for routing and
  nothing else; only a complete observation can verify.

## Typed reads

`TypedRead` is the read-side counterpart to `MutationIntent`'s `change`
oneof: a required, typed-variant wrapper so a read the device's lane admits
is as typed as a write it admits. Today it has one arm,
`InterfaceReadIntent`, naming the interface by the device-local name the
device spells, the same key `InterfaceObservation` uses. The execution
envelope in `integration/device/v1` dispatches a `TypedRead` the same way it
dispatches a `MutationIntent`, and any future read capability joins this
oneof rather than inventing a second read shape.

## What is deliberately absent

- Secrets, sessions, and transcripts. A `Provenance` names a binding, an
  edge, and a protocol, never a credential or a session identifier.
- A protocol path or raw command. The intent is the typed change; how a
  route expresses it belongs to the adapter behind the edge.
- An audit event. The durable `DeviceOperationEvent` lands in its own
  package so the audit stream's contract can evolve without touching the
  API. `MutationState` therefore has no `MutationEvent` sibling here, and
  `MutationIntent` is the intended side without a `MutationConfig`.
- An interface ref. An interface is named by the device ref plus the name
  the device spells, because the Interface entity is undecided per the
  [protobuf conventions](../../../../../../docs/conventions/protobuf.md).
- A tenant. Scope is ambient.
