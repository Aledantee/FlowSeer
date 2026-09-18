# Edge attachment

`flowseer.edge.attach.v1` holds `EdgeService`, everything an edge calls on its
own behalf to get and keep its standing with central: enrollment, rekey,
heartbeat, bus attachment, the listing of the devices it serves, and the two
credential lifecycles. `Enroll` is the only call without an assertion; every
other one carries a fresh `SignedEdgeAssertion` in the Authorization header and
is authorized as the edge that assertion names. The Edge entity itself, its ref
pair, lifecycle, keys, assertion, and provisioning file live in
[`model/edge/v1`](../../../model/edge/v1/README.md), which this package imports
and returns as `EdgeGlobalRef` from `Enroll`.

## Boundaries

Imports: model/credential, model/edge, model/policy, net/addr

Imported by: nothing

Deliberately absent:

- Handlers for the RPCs, and the Connect interceptor that verifies the
  assertion against them. Those need a running device service host; this
  package is the contract they implement against.
- The operator's side of an edge's life. Creating, provisioning, and retiring
  an edge is `EdgeAdminService` in
  [`api/edge/v1`](../../../api/edge/v1/README.md); nothing here is a call an
  operator makes.

An edge is the process, not an integration. The local-network integration and
any on-prem controller adapter a site needs run on the edge and point at it; the
edge's own concerns are identity, liveness, provisioning, which devices it
serves, and the three lifecycles below. The execute and event contracts are
their own packages, `edge/dispatch/v1` and `edge/audit/v1`; nothing here
carries an operation, a device ref, or a durable audit record.

## Three lifecycles, never the same call

Decision 9 of the
[verified device access record](../../../../../../docs/architecture/2026-09-05-verified-device-access-direction.md)
keeps bus attachment, credential delivery, and revocation apart, and this
package keeps them on separate RPCs so that revoking one never touches the
others. The device listing below them is a fourth call and none of the three:
it carries no secret and revokes nothing.

### Bus attachment

`AttachBus` hands an enrolled edge everything its embedded NATS leaf node
needs: an `account_jwt` scoping it to its tenant's subject namespace, a
`user_credential` (JWT and NKey seed) the leaf node authenticates with, a
`subjects` map from logical name to the concrete broker subject the edge
publishes on, and at least one `cluster_url` to dial. The leaf carries what
the edge publishes and nothing the edge must act on; dispatches, reports,
and audit records are Connect calls in `edge/dispatch/v1` and
`edge/audit/v1`. The vocabulary of logical subject names belongs
to the module that builds the leaf node, not to this package. An edge
attaches once, as a whole, never per integration or per device; there is no
request body beyond the assertion that authorizes the call. Re-attaching
after a rotation is calling `AttachBus` again, not a separate RPC.

### The device listing

`ListDevices` answers the calling edge with the devices it hosts, and for
each one what only central's registry knows: the management address and the
SNMP and SSH ports, the binding the device is reached through, the access
policy version it pins, and its measured delayed-apply horizon. Without it an
edge can hold a credential for a device it cannot locate.

It is a listing rather than fields on `AcquireReadCredentialResponse`,
although the address and the host-key pin are needed at the same moment and
look like a pair. They fail in opposite directions: a host key that changed
fails closed, because the pin refuses and the caller gets an error, while an
address that changed fails open and silently, against a different real device
that answers normally. Riding the credential call would also put a stable
fact on a revocable one — and a mutation acquires separately for its command
and for the observation that verifies it, so an address that changed between
the two would send the command to one device and read the verification from
another, with the lane reporting the mutation verified about a box it never
touched.

The edge lists after it attaches and again whenever its dispatch stream
reconnects, and holds what it was last told in between. That is stale by
construction and deliberately so: stale-but-consistent beats
fresh-but-inconsistent inside one operation, which is the same argument that
keeps the address off the credential response.

A device the registry lists without a measured horizon is listed all the
same, with the field unset. The edge serves it and refuses a mutation on it,
which is what central does with the same fact; dropping it from the listing
would leave the edge unable to tell a device it must not mutate from one
central has never heard of.

Device and binding are plain UUID strings here for the same reason the
credential calls take them that way: `edge/attach` sits below `model/inventory`
in the import graph and cannot name a `DeviceGlobalRef` without cycling it.

### Read credential delivery

`AcquireReadCredential` delivers a device read credential for one binding,
under the device's pinned `AccessPolicyHandle`, scoped to a device id and a
binding id passed as plain UUID strings rather than typed refs — `edge/attach`
sits below `model/inventory` in the import graph, so it cannot name a
`DeviceGlobalRef` without cycling it. The response's `DeviceCredential`
pairs a typed `CredentialMaterial` from `model/credential/v1` (an SNMPv3
user or a shell login) with a `CredentialHandle` naming exactly which
version it is, alongside a `HostTrustHandle` and an `expires_at`. The
handle names the trust version; when the material is a shell login the
response also carries `ssh_host_key_sha256`, the pin that version resolves
to, and a rule ties the two together so an SNMP credential never carries a
meaningless pin. There is no standing lease: an edge acquires a fresh
credential for the next read rather than caching one past its expiry.

### Submission credential delivery

`OpenDeviceSubmission` is a server stream the edge opens once central has
durably checkpointed `POSSIBLY_APPLIED` for the named sequence (the same
sequence `flowseer.edge.dispatch.v1.ExecuteRequest` carries). The
first message is always a `SubmissionGrant` — a one-use `DeviceCredential`,
a `HostTrustHandle`, the same `ssh_host_key_sha256` rule as the read
response, and a `deadline` — and every message after it is an
`AuthorityPulse` the edge checks before it submits or continues submitting
the command. The grant is delivered exactly once per stream so a one-use
secret is never double-issued. A pulse's `deadline` only moves forward
within a stream; the edge treats it as a monotonic bound on top of the
grant's own deadline, not a fresh timer for each command.

### Revocation

Revocation is not a fourth RPC. Bus revocation is a NATS JWT revocation
pushed to the account, ahead of the credential fabric this package attaches
to. Credential revocation is an `AuthorityPulse` carrying
`SUBMISSION_AUTHORITY_REVOKED` on an open `OpenDeviceSubmission` stream, or
simply the next `AcquireReadCredential` call returning a credential the
previous one no longer matches. None of the three lifecycles shares a
message with another, so revoking one never touches the others.
