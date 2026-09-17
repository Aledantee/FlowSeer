# Device policy handles

`flowseer.model.policy.v1` holds the opaque handles a device record and a
device operation use to name a policy without carrying it. It sits at the
bottom of the device-access packages: `model/inventory` puts a handle on
`DeviceConfig`, and `device/access` pins one on every mutation intent, so the
two boundaries agree on a policy without either importing the other. The
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
places the package.

## Boundaries

Imports: nothing FlowSeer-owned

Imported by: api/device, api/edge, device/access, model/inventory,
store/device

Deliberately absent:

- The policy body. Credentials, host trust, route pins, and per-device
  exceptions are read by the device service from its own store and reach an
  edge over authenticated Connect calls. Nothing in this package can leak
  them.
- A ref pair and a triad. A handle is not an entity: nothing else points at
  a policy, and the store that would answer an existence check lands with
  the device service. The handle therefore stays out of `EntityType` for the
  same reason the Edge does.
- A tenant. Scope is ambient, per the
  [protobuf conventions](../../../../../../docs/conventions/protobuf.md).

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

`CredentialHandle` and `HostTrustHandle` take the same `{key, version}`
shape for the same reason: a device credential or a host-trust record can be
rotated without disturbing an operation that already pinned an earlier
version, and neither the secret material nor the trust material ever rides
on the handle. `api/edge/v1` names both in the responses of its credential
RPCs; the device service's own store is what resolves a handle back to the
material it names.
