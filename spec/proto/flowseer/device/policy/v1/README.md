# Device policy handles

The `flowseer.device.policy.v1` package holds the opaque handles a device
record and a device operation use to name a policy without carrying it. It
imports nothing FlowSeer-owned and sits at the bottom of the device-access
packages: `api/inventory` puts a handle on `DeviceConfig`, and
`device/access` pins one on every mutation intent, so the two boundaries
agree on a policy without either importing the other. The
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
places the package.

A handle is a key and a version:

```prototext
key: "icx7150-lab"
version: 3
```

The key is one path segment of lowercase letters, digits, dot, underscore,
and hyphen, so a store that keeps policies as files under a root directory
cannot be walked out of by a key like `../root`, and a broker subject built
from it cannot gain a separator. The version is at least 1 and only grows. An
operation admitted under version 3 stays pinned to version 3 until it closes;
a policy edit produces version 4 for later operations and never re-reads the
one in flight.

## What is deliberately absent

- The policy body. Credentials, host trust, route pins, and per-device
  exceptions are read by the device service from its own store and reach an
  edge over authenticated Connect calls. Nothing in this package can leak
  them.
- A ref pair and a triad. A handle is not an entity: nothing else points at
  a policy, and the store that would answer an existence check lands with
  the device service. The handle therefore stays out of `EntityType` for the
  same reason the Edge does.
- Credential and host-trust handles. They take the same shape and arrive
  with the plan that delivers credentials to an edge, once a message exists
  to carry them.
- A tenant. Scope is ambient, per the
  [protobuf conventions](../../../../../../docs/conventions/protobuf.md).
